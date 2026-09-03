package voice

import "math"

// Time-scaling one decoded frame, so a buffer sitting off its depth can be
// brought back without anybody hearing the correction.
//
// Playing 20 ms of audio in 15 by resampling would raise every frequency in it
// by a third: sample rate *is* pitch, and a voice run fast is a chipmunk. What
// makes the correction free instead is that voiced speech is a pulse train —
// the vocal folds close at the fundamental, 85-255 Hz for a person, and each
// closure sends the same shape through the same vocal tract. The waveform
// therefore already contains near-copies of itself a few milliseconds apart,
// and deleting exactly one period leaves the pitch alone, a period's length
// being what pitch is. A vowel fifty cycles long becomes one cycle shorter,
// which is not something an ear has any way to notice.
//
// The period is *found* rather than estimated. Pitch detection is its own hard
// problem and has no answer at all for the unvoiced sounds — s, f, sh have no
// period to detect — where a search instead settles for the least bad lag, and
// noise splicing into noise is inaudible whatever lag it picked.
//
// This is what a screenshare's sound needs and a conversation mostly does not:
// the buffer's own corrections are taken in Opus comfort noise, which a talker
// supplies every few seconds for free, and only a source that never goes quiet
// — music, a shared video, a client with DTX switched off — ever gets here.

const (
	// The lag search, in samples at 48 kHz. 2 ms is a 500 Hz fundamental and
	// 10 ms a 100 Hz one, which spans a speaking voice; the top of it is also
	// what keeps a merge inside a single 20 ms frame, since removing two periods
	// from 960 samples needs the lag under half of them. A frame therefore never
	// depends on the frame before it, and the stretcher holds no state a lost
	// packet or a re-subscribe could leave stale.
	minLag = sampleRate * 2 / 1000
	maxLag = sampleRate * 10 / 1000

	// matchWindow is what a candidate lag is scored over, fixed at the end of the
	// frame because the end is the part a merge actually joins.
	matchWindow = sampleRate * 5 / 1000

	// stretchable is the shortest frame either direction can be applied to.
	stretchable = maxLag + matchWindow
)

// driftMargin is how many frames the buffer may sit away from its depth before
// the decoded audio is time-scaled to close the gap. Two, because playout takes
// frames in bursts and arrival puts them back one at a time: a tighter margin
// would be outside itself half the time in a steady state, and this correction
// is cheap rather than free.
const driftMargin = 2

// scaleLimit is the most of the stream time-scaling may take out or put in,
// averaged over the frames it runs on. One pitch period out of a 20 ms frame is
// 11 % for a 440 Hz tone and over 40 % for a low voice, and sustaining 40 % for
// a second is speech that is audibly hurried even though every splice in it is
// clean. A lag is never trimmed to fit — part of a period does not splice — so
// what is rationed is how many frames in a row may carry one.
const scaleLimit = 0.25

// retime shortens or lengthens one decoded frame where the buffer has drifted
// off its depth and could not fix it in silence. drift is the buffer's, read
// once per fill pass rather than per frame.
//
// Nothing is reported back to the buffer, because nothing needs to be: the
// speakers are filled against Want, so a frame handed over short leaves the lane
// hungry and the filler pops another, and a long one leaves it full and the
// filler pops none. Occupancy follows the length of what was written, which is
// the same accounting a plain frame gets.
func (l *lane) retime(pcm []int16, drift int) []int16 {
	if len(pcm) < stretchable {
		return pcm
	}

	if drift <= driftMargin && drift >= -driftMargin {
		l.allow = 0 // no credit banked while the buffer is where it wants to be

		return pcm
	}

	lag := bestLag(pcm)
	if lag == 0 {
		return pcm
	}

	// The ration, paid per frame and spent a whole period at a time: a lag worth
	// more than the allowance waits for the frames either side of it to earn it.
	l.allow += scaleLimit * float64(len(pcm))
	if l.allow < float64(lag) {
		return pcm
	}
	l.allow -= float64(lag)

	if drift > 0 {
		l.compressed++

		return compress(pcm, l.scratch, lag)
	}

	l.expanded++

	return expand(pcm, l.scratch, lag)
}

// bestLag is the offset at which the end of pcm most resembles itself, which
// for voiced speech is one pitch period and for anything else is whatever
// splices most quietly. Zero when there is nothing to go on.
func bestLag(pcm []int16) int {
	n := len(pcm)
	if n < stretchable {
		return 0
	}

	tail := pcm[n-matchWindow:]

	best, score := 0, 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		at := pcm[n-matchWindow-lag : n-lag]

		var dot, energy float64
		for i, s := range at {
			v := float64(s)
			dot += v * float64(tail[i])
			energy += v * v
		}

		if dot <= 0 || energy == 0 {
			continue
		}

		// The tail's own energy is the same for every candidate, so it is left out
		// of the normalisation: what is being ranked is shape, and dividing every
		// candidate by one constant cannot reorder them.
		if at := dot / math.Sqrt(energy); at > score {
			best, score = lag, at
		}
	}

	return best
}

// compress returns pcm one pitch period shorter, by merging the last two
// periods into one. into is scratch the result is built in.
//
// The head is untouched and the merge ends on the frame's own last sample, so
// both joins — to the frame before and the frame after — are the samples that
// were always going to be there. Only the middle is a blend, of two segments
// picked for being near-identical.
func compress(pcm []int16, into []int16, lag int) []int16 {
	if lag <= 0 || 2*lag > len(pcm) {
		return pcm
	}

	n := len(pcm)
	first, second := pcm[n-2*lag:n-lag], pcm[n-lag:]

	out := append(into[:0], pcm[:n-2*lag]...)
	for i := range lag {
		out = append(out, blend(first[i], second[i], i, lag))
	}

	return out
}

// expand returns pcm one pitch period longer, by playing the last period twice
// with a blend across the seam. into is scratch the result is built in.
func expand(pcm []int16, into []int16, lag int) []int16 {
	if lag <= 0 || 2*lag > len(pcm) {
		return pcm
	}

	n := len(pcm)
	first, second := pcm[n-2*lag:n-lag], pcm[n-lag:]

	out := append(into[:0], pcm[:n-lag]...)
	for i := range lag {
		out = append(out, blend(second[i], first[i], i, lag))
	}

	return append(out, second...)
}

// blend is the i-th of n samples fading from one segment to the other. Linear
// rather than a constant-power curve: the two segments are near-identical by
// construction, so they sum coherently and an equal-power fade would put a bump
// in the middle of every seam. A convex combination of two int16s is one too,
// so nothing here can clip.
func blend(from, to int16, i, n int) int16 {
	w := float64(i) / float64(n)

	return int16(float64(from)*(1-w) + float64(to)*w)
}
