package audio

import (
	"math"
	"math/rand/v2"
)

// The built-in sounds are synthesised rather than shipped as files. That is not
// cleverness for its own sake: a sound is a licence and a binary each, a missing
// file is a state every caller would have to handle, and a client with no assets
// at all still has a full set to hear before anybody chooses their own. A custom
// file *replaces* one of these, so nothing is ever unset.
//
// Everything here is rendered at the device's rate and returned as the same
// interleaved stream a decoded file becomes.

// typingTakes is how many renders of one keystroke click are kept. Four is
// enough that a run of them does not read as one sample repeating, and few
// enough that the whole typing set is well under a megabyte.
const typingTakes = 4

// builtin synthesises the default for a key. An unrecognised key answers with
// the plain message blip rather than silence — a sound that cannot be heard is
// indistinguishable from a setting that did not take.
func builtin(key string) *Sound {
	switch key {
	case Mention:
		return single(chord(0.45, 0.17, 0.50, 880, 1320))
	case Direct:
		return single(join(tone(0.11, 659.25, 0.07, 0.42), tone(0.16, 987.77, 0.09, 0.42)))
	case Message:
		return single(tone(0.16, 523.25, 0.06, 0.34))
	case Ambient:
		return single(tone(0.10, 440, 0.04, 0.20))
	case Send:
		return single(sweep(0.09, 380, 720, 0.05, 0.26))
	case Friend:
		return single(join(
			tone(0.09, 587.33, 0.06, 0.40),
			tone(0.09, 739.99, 0.06, 0.40),
			tone(0.18, 987.77, 0.10, 0.40),
		))
	case Reaction:
		return single(tone(0.07, 1174.66, 0.03, 0.30))
	case Error:
		// Two lows a semitone apart: the beating between them is what makes this read
		// as wrong without it having to be loud.
		return single(chord(0.34, 0.15, 0.38, 196, 207.65))
	case Offline:
		return single(sweep(0.24, 660, 330, 0.12, 0.34))
	case Online:
		return single(sweep(0.24, 330, 660, 0.12, 0.34))

	case KeyPress:
		return takes(func(r *rand.Rand) []float32 { return click(r, 0.022, 3800, 0.50) })
	case KeySpace:
		return takes(func(r *rand.Rand) []float32 { return click(r, 0.030, 2200, 0.55) })
	case KeyBackspace:
		return takes(func(r *rand.Rand) []float32 { return click(r, 0.018, 5200, 0.44) })
	case KeyEnter:
		// The one keystroke that finishes something, so it keeps a short pitched tail
		// the others have none of.
		return takes(func(r *rand.Rand) []float32 {
			return mix(click(r, 0.026, 3000, 0.52), tone(0.09, 1108.73, 0.035, 0.16))
		})
	}

	return single(tone(0.16, 523.25, 0.06, 0.34))
}

/* Rendering */

// single wraps one mono render as a sound with one take.
func single(mono []float32) *Sound {
	return &Sound{takes: [][]byte{encode(mono, 1, sampleRate)}}
}

// takes renders the same recipe several times from different noise, for the
// sounds that repeat often enough for one render to be recognisable.
func takes(render func(*rand.Rand) []float32) *Sound {
	sound := &Sound{takes: make([][]byte, typingTakes)}

	for i := range sound.takes {
		// Seeded rather than global, so a click sounds the same from run to run and a
		// report of one being wrong is reproducible.
		r := rand.New(rand.NewPCG(uint64(i)+1, 0x9E3779B97F4A7C15))
		sound.takes[i] = encode(render(r), 1, sampleRate)
	}

	return sound
}

// frames is how many samples a duration is at the device's rate.
func frames(seconds float64) int { return int(seconds * sampleRate) }

// envelope is the shape every sound here is drawn through: a short attack so it
// does not start on a click of its own, then an exponential decay. tau is the
// time the level falls to about a third, which is what makes a sound read as
// short or as ringing.
func envelope(i, count int, tau float64) float32 {
	t := float64(i) / sampleRate

	const attack = 0.004
	level := math.Exp(-t/tau) * min(t/attack, 1)

	// The last few samples are taken to zero whatever the decay had left, a buffer
	// ending mid-cycle being a click at the end of every play.
	if tail := count - i; tail < frames(0.002) {
		level *= float64(tail) / float64(frames(0.002))
	}

	return float32(level)
}

// tone is one sine under that envelope.
func tone(seconds, freq, tau, gain float64) []float32 {
	return chord(seconds, tau, gain, freq)
}

// chord is several sines summed, scaled by how many there are so two partials
// are not twice as loud as one.
func chord(seconds, tau, gain float64, freqs ...float64) []float32 {
	count := frames(seconds)
	out := make([]float32, count)
	level := gain / float64(len(freqs))

	for i := range out {
		t := float64(i) / sampleRate

		var sum float64
		for _, freq := range freqs {
			sum += math.Sin(2 * math.Pi * freq * t)
		}

		out[i] = float32(sum*level) * envelope(i, count, tau)
	}

	return out
}

// sweep is a sine whose frequency moves from one pitch to another. The phase is
// accumulated rather than computed from the instantaneous frequency: multiplying
// a moving frequency by t sweeps at twice the rate and lands on the wrong note.
func sweep(seconds, from, to, tau, gain float64) []float32 {
	count := frames(seconds)
	out := make([]float32, count)

	var phase float64
	for i := range out {
		freq := from + (to-from)*float64(i)/float64(count)
		phase += 2 * math.Pi * freq / sampleRate

		out[i] = float32(math.Sin(phase)*gain) * envelope(i, count, tau)
	}

	return out
}

// click is a keystroke: noise through a one-pole low-pass, decaying fast. The
// cutoff is what makes one key sound deeper than another, and the decay is short
// enough that the next keystroke lands after it rather than over it.
func click(r *rand.Rand, seconds, cutoff, gain float64) []float32 {
	count := frames(seconds)
	out := make([]float32, count)

	alpha := 1 - math.Exp(-2*math.Pi*cutoff/sampleRate)

	var filtered float64
	for i := range out {
		noise := r.Float64()*2 - 1
		filtered += alpha * (noise - filtered)

		// The decay is against the whole click rather than a tau, a keystroke being
		// short enough that an exponential tail is most of its length.
		level := 1 - float64(i)/float64(count)
		out[i] = float32(filtered * gain * level * level)
	}

	return out
}

/* Assembly */

// join concatenates renders, for a sound that is more than one note.
func join(parts ...[]float32) []float32 {
	var total int
	for _, part := range parts {
		total += len(part)
	}

	out := make([]float32, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}

	return out
}

// mix sums renders over one another, the longer one deciding the length.
func mix(parts ...[]float32) []float32 {
	var longest int
	for _, part := range parts {
		longest = max(longest, len(part))
	}

	out := make([]float32, longest)
	for _, part := range parts {
		for i, sample := range part {
			out[i] += sample
		}
	}

	return out
}
