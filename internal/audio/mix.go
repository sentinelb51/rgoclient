package audio

import (
	"math"
	"sync/atomic"
	"unsafe"
)

// What the mixer is allowed to hold. Every one of these bounds an array that is
// allocated once, because the one rule the render path has is that it allocates
// nothing: a Go allocation can trip a GC assist, and an audio callback that
// stops to help the collector is a dropout.
const (
	// maxVoices is how many notification sounds may ring at once, across every
	// key. Typing clicks are the only sound that overlaps in practice and they
	// are short, so this is generous rather than tuned.
	maxVoices = 32

	// maxLanes is how many remote participants one call may mix. A lane costs a
	// ring and an atomic load per period whether or not anybody is in it.
	maxLanes = 64

	// chunkFrames caps how much of one period is summed at a time, so the
	// accumulator is a fixed array rather than a slice sized by whatever the
	// backend asks for. A larger period is rendered in several passes.
	chunkFrames = 1024

	// commandDepth is how many plays may be waiting on the render callback.
	commandDepth = 64
)

/* Commands */

// playCmd starts one notification sound. It carries the take rather than a key
// so the render path never looks anything up: data points into a Sound the
// engine goroutine is holding, and stays alive because that map does.
type playCmd struct {
	data  []byte
	gain  float32
	group uint16 // which sound, so the pool can bound one sound's overlap
	limit uint8  // how many copies of that sound may ring together
}

/* Voices */

// mixVoice is one ringing notification sound. Owned by the render callback and
// touched nowhere else.
type mixVoice struct {
	data  []byte
	pos   int
	gain  float32
	group uint16
	age   uint64 // when it started, so the oldest can be identified
}

/* Lanes */

// lane is one remote participant's audio: 48 kHz mono, written by whatever
// goroutine decodes for them and drained by the render callback.
//
// active is what the callback reads to decide whether to look at the rest.
// Everything else is set before it goes true and left alone until after it goes
// false, so one atomic is the whole of the handshake.
type lane struct {
	pcm *ring[int16]

	active atomic.Bool
	gain   atomic.Uint32 // float32 bits

	// lim holds the gained lane under the ceiling. The callback's own: a
	// participant boosted past full scale is turned down for the syllable
	// rather than clipped on every cycle of it.
	lim limiter

	// level brings this participant to the same loudness as the rest. Also the
	// callback's own, and ahead of lim rather than after it: what it does is move
	// a gain, so what it produces is what the ceiling is then held by.
	level leveller

	// person is whether this lane is somebody in the call rather than the
	// microphone test or the video player. Both receive-side treatments are about
	// a room of people talking over each other and neither belongs on a lane that
	// is one known thing: the echo test exists to report what is being sent, and a
	// video's own mix is not this client's to normalise or to move.
	person bool
}

// How much a lane holds. laneTarget is the depth the writer is asked to keep it
// at, through Sink.Want — two frames, which is enough to cover a device period
// plus the scheduling delay of the goroutine that answers a wake, and no more:
// every sample sitting here is mouth-to-ear latency.
//
// laneDepth is the ring's size and laneBacklog a backstop for a writer that
// ignores Want. Neither is reachable while one does — the writer only ever
// supplies what was asked for — so a lane past laneBacklog means a producer
// running on a clock of its own, which is the failure this whole arrangement
// exists to remove.
const (
	laneTarget  = sampleRate * 40 / 1000
	laneDepth   = sampleRate * 400 / 1000
	laneBacklog = sampleRate * 120 / 1000
)

/* The mixer */

// mixer is the state the device callback owns. Everything reaching it does so
// through a ring or an atomic, so no call into it ever blocks.
type mixer struct {
	commands *ring[playCmd]

	// master scales every lane, which is the call's own output volume. Sounds
	// carry their gain per play and are not scaled by it — a reader who turned
	// the call down did not ask for a quieter mention ping.
	master atomic.Uint32 // float32 bits

	// soft rounds a peak over the ceiling instead of slicing it flat. Read once
	// per chunk rather than per sample, so a setting moved mid-call lands between
	// chunks and never inside one.
	soft atomic.Bool

	// levelled and placed are the two receive-side treatments, read the same way
	// and for the same reason. Neither is reset when it goes off: levelling walks
	// its gain back to unity on the slope it is already on, so the switch is heard
	// as the correction coming off rather than as a step.
	levelled atomic.Bool
	placed   atomic.Bool

	lanes [maxLanes]lane

	// wake is how the speakers ask for more. The callback sends into it without
	// waiting once a period, and whoever fills the lanes tops them back up to
	// laneTarget — so playout is paced by the device's own clock rather than by a
	// timer running beside it. Buffered by one: a fill still in progress needs no
	// second wake, it re-reads every lane's Want anyway.
	//
	// This is the one place the callback's no-locking rule is knowingly broken. A
	// non-blocking send is not a lock-free one: it takes the channel's runtime
	// lock whenever there is room, and there nearly always is, because the filler
	// drains it. What buys it is that nothing else can carry the device's clock —
	// an atomic ticket cannot wake a parked goroutine, and every way to wake one
	// either locks in the same place, spins a core, or replaces the device's clock
	// with a timer beside it, which is the drift this arrangement exists to
	// remove. docs/performance.md carries the whole argument.
	wake chan struct{}

	/* The callback's own, touched nowhere else */

	voices     [maxVoices]mixVoice
	clock      uint64
	acc        [chunkFrames * channelCount]int32
	pull       [chunkFrames]int16
	lanesDrawn bool // whether this period found a lane open
}

func newMixer() *mixer {
	m := &mixer{commands: newRing[playCmd](commandDepth), wake: make(chan struct{}, 1)}
	m.master.Store(floatBits(1))

	for i := range m.lanes {
		m.lanes[i].pcm = newRing[int16](laneDepth)
		m.lanes[i].gain.Store(floatBits(1))
		m.lanes[i].lim = newLimiter(float32(softKneeSample))
		m.lanes[i].level = newLeveller()
	}

	return m
}

// play queues a sound. Producer side, and dropped when the ring is full: a
// click owed to a keystroke already gone is worth nothing.
func (m *mixer) play(cmd playCmd) bool { return m.commands.Push(cmd) }

// setMaster sets the gain every lane is scaled by.
func (m *mixer) setMaster(gain float32) { m.master.Store(floatBits(clampGain(gain))) }

// setSoftClip picks how the sum meets the ceiling: rounded, or sliced flat.
func (m *mixer) setSoftClip(on bool) { m.soft.Store(on) }

// setLevelling says whether every participant is brought to one loudness.
func (m *mixer) setLevelling(on bool) { m.levelled.Store(on) }

// setPlacement says whether participants are spread across the stereo image.
func (m *mixer) setPlacement(on bool) { m.placed.Store(on) }

/* Rendering */

// render fills one device period. This runs on the backend's own thread, and the
// whole of its contract is what it must not do: allocate, lock, log, or call
// into anything that might.
func (m *mixer) render(out []byte) {
	if len(out) < 2 {
		return
	}

	samples := unsafe.Slice((*int16)(unsafe.Pointer(unsafe.SliceData(out))), len(out)/2)

	m.start()
	m.lanesDrawn = false

	for len(samples) >= channelCount {
		frames := min(len(samples)/channelCount, chunkFrames)

		m.renderChunk(samples[:frames*channelCount], frames)
		samples = samples[frames*channelCount:]
	}

	// Only a period with a lane open asks for more, so a client ringing
	// notification sounds with no call open wakes nobody — and the one cost this
	// callback pays that it is not supposed to is paid only during a call. The
	// send never waits for the filler, but it does take the channel's own runtime
	// lock; see mixer.wake for why that is the least bad of the options.
	if m.lanesDrawn {
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}

// start drains the command ring and turns each command into a voice.
func (m *mixer) start() {
	for {
		cmd, ok := m.commands.Pop()
		if !ok {
			return
		}

		slot := m.claim(cmd.group, int(cmd.limit))
		m.clock++

		v := &m.voices[slot]
		v.data = cmd.data
		v.pos = 0
		v.gain = cmd.gain
		v.group = cmd.group
		v.age = m.clock
	}
}

// claim picks the voice a new play will use: a free one, or the oldest of its
// own group once that group is at its limit, or the oldest of all. A sound has
// to be heard even when every copy is still ringing.
func (m *mixer) claim(group uint16, limit int) int {
	free, oldest, oldestInGroup, inGroup := -1, -1, -1, 0

	for i := range m.voices {
		v := &m.voices[i]

		if v.data == nil {
			if free < 0 {
				free = i
			}

			continue
		}

		if oldest < 0 || v.age < m.voices[oldest].age {
			oldest = i
		}

		if v.group == group {
			inGroup++
			if oldestInGroup < 0 || v.age < m.voices[oldestInGroup].age {
				oldestInGroup = i
			}
		}
	}

	if inGroup >= limit && oldestInGroup >= 0 {
		return oldestInGroup
	}
	if free >= 0 {
		return free
	}

	return oldest
}

// renderChunk sums every source into the accumulator and writes it out. int32 so
// overlapping sounds add without wrapping, clamped once at the end rather than
// per source — clipping each contribution separately is distortion the sum would
// not have had.
func (m *mixer) renderChunk(out []int16, frames int) {
	acc := m.acc[:frames*channelCount]
	clear(acc)

	m.mixVoices(acc)
	m.mixLanes(acc, frames)

	// Hoisted rather than branched per sample: the sum is the hottest loop in the
	// callback and the setting cannot change inside a chunk anyway.
	if m.soft.Load() {
		for i, sample := range acc {
			out[i] = softClipSample(sample)
		}

		return
	}

	for i, sample := range acc {
		out[i] = clampSample(sample)
	}
}

// mixVoices adds every ringing notification sound. They are already in the
// device's own format — 48 kHz stereo, interleaved — so this is a gain and an
// add.
func (m *mixer) mixVoices(acc []int32) {
	for i := range m.voices {
		v := &m.voices[i]
		if v.data == nil {
			continue
		}

		n := min(len(acc), (len(v.data)-v.pos)/2)
		for j := range n {
			at := v.pos + j*2
			sample := int16(uint16(v.data[at]) | uint16(v.data[at+1])<<8)
			acc[j] += int32(float32(sample) * v.gain)
		}
		v.pos += n * 2

		if v.pos+1 >= len(v.data) {
			v.data = nil
		}
	}
}

// mixLanes adds every remote participant. A lane is mono and the device is
// stereo, so one sample lands in both ears — either ear at the same size, or at
// the pair of sizes that puts the lane somewhere between them.
//
// A lane with nothing waiting contributes silence rather than stretching what it
// had: a call whose sender has stopped should go quiet, not buzz.
func (m *mixer) mixLanes(acc []int32, frames int) {
	master := bitsFloat(m.master.Load())
	levelling := m.levelled.Load()
	placement := m.placed.Load()

	for i := range m.lanes {
		l := &m.lanes[i]
		if !l.active.Load() {
			continue
		}

		// Set for an *open* lane rather than a lane that had something: one running
		// dry is the case that most needs the filler woken, and a lane opened and
		// not yet written to is how a participant's audio starts at all.
		m.lanesDrawn = true

		// A lane the sender has run ahead of is caught up by dropping. Playing the
		// backlog out would hold every later frame behind it.
		if over := l.pcm.Len() - laneBacklog; over > 0 {
			l.pcm.Discard(over)
		}

		n := l.pcm.PopAll(m.pull[:frames])
		if n == 0 {
			continue
		}

		// Where the lane sits. Unity into both ears is what an unplaced lane has
		// always summed to, and is what the pair collapses to at centre.
		left, right := float32(1), float32(1)
		if placement && l.person {
			left, right = panLeft[i], panRight[i]
		}

		// Once a block rather than once a sample: the aim is what the block is loud
		// enough to justify, and next() is what walks the gain to it.
		l.level.retarget(levelling && l.person, m.pull[:n])

		gain := bitsFloat(l.gain.Load()) * master
		for j := range n {
			v := float32(m.pull[j]) * l.level.next() * gain
			if g := l.lim.gain(v); g != 1 {
				v *= g
			}

			acc[j*channelCount] += int32(v * left)
			acc[j*channelCount+1] += int32(v * right)
		}
	}
}

/* Small maths, kept together so the render path reads as arithmetic */

// clampSample folds the accumulator back into the device's format. int16's range
// is asymmetric, so the two ends are not one expression.
func clampSample(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}

	return int16(v)
}

// clampGain bounds anything a gain arrives as. maxGain is ×10, the +20 dB the
// settings offer as their own ceiling: past that a microphone quiet enough to
// need it is amplifying its own noise floor rather than a voice.
//
// Gains multiply — a lane's against the master's — so a pair at the ceiling
// exceeds it. That is deliberate and is what softClip is for: what the sum meets
// is the sample ceiling, not this one.
func clampGain(gain float32) float32 { return min(max(gain, 0), maxGain) }

const maxGain = 10

/* The limiter */

// limiter holds a peak under a ceiling by turning the gain down rather than by
// bending the wave. A boost that takes a loud voice past full scale is
// otherwise clipped on every cycle, and a clipped wave is harmonics the voice
// never had — the buzz on a participant turned up to +20 dB. Turning the gain
// down for the length of a syllable instead leaves the wave its shape, and
// costs a compare a sample and, over the ceiling, a divide.
//
// The attack is instant — the envelope jumps to a peak the moment it arrives —
// so nothing gets past the ceiling. The release is what makes it a limiter
// rather than a clipper: the gain recovers over limiterRelease, so a wave's
// cycles are scaled alike instead of each one being flattened.
//
// Its ceiling is the soft clip's knee, in whichever units the caller counts
// in. Below the knee the curve is the identity, so a single limited source
// never meets it; what the clip is then for is the sum, which two lanes and a
// notification sound can still take over the top.
type limiter struct {
	env     float32 // the peak being tracked, in the caller's units
	ceiling float32
}

func newLimiter(ceiling float32) limiter { return limiter{ceiling: ceiling} }

// limiterRelease is how long a turned-down gain takes to come back, as the
// per-sample factor the envelope decays by: a time constant of 100 ms.
const limiterRelease = 0.99979171 // exp(-1 / (0.1 s × 48 kHz))

// gain answers what to scale one sample by, given the sample. 1 nearly
// always — the envelope is only decayed while it is over the ceiling, so a
// source under it pays the compare and nothing else.
func (l *limiter) gain(v float32) float32 {
	if v < 0 {
		v = -v
	}

	if v > l.env {
		l.env = v
	} else if l.env > l.ceiling {
		l.env *= limiterRelease
	}

	if l.env <= l.ceiling {
		return 1
	}

	return l.ceiling / l.env
}

// apply limits a frame in place.
func (l *limiter) apply(frame []float32) {
	for i, v := range frame {
		if g := l.gain(v); g != 1 {
			frame[i] = v * g
		}
	}
}

/* The leveller */

// leveller brings one participant to the same loudness as the rest by moving a
// gain slowly against how loud they have been. Everybody arrives at whatever
// their own microphone, distance and voice add up to, and a call spent reaching
// for the volume is what this removes.
//
// Slow is the whole of what makes it a leveller rather than a compressor: the
// gain settles over a sentence, so it evens out who is talking and not the shape
// of what they said. The limiter beside it is what catches a peak, on a
// timescale three orders shorter.
//
// Nothing here is reset when the setting goes off. target returns to unity and
// the same slope carries the gain back, so the switch moves the level over a
// second rather than stepping it.
type leveller struct {
	// target is the gain the block just measured justifies, and gain is what is
	// actually being applied — the one walked toward the other a sample at a time.
	// step is which slope that walk is on, chosen once a block so the sample loop
	// carries no branch.
	target float32
	gain   float32
	step   float32

	// blocks is how much speech this lane has been heard to make, counted only
	// while it is loud enough to learn from and stopped at levelGrabBlocks. It is
	// the whole of what says a lane is still finding its level rather than holding
	// one.
	blocks int
}

func newLeveller() leveller { return leveller{target: 1, gain: 1} }

// What the follower aims at and how fast it gets there.
const (
	// levelTarget is the RMS a voice is brought to, in whole samples: -23 dBFS,
	// which leaves speech's own crest factor room to peak below full scale instead
	// of into the limiter.
	levelTarget = 2320

	// levelFloor is the quietest block worth learning from, -55 dBFS. Below it the
	// aim is held rather than recomputed, which is what stops the gap between two
	// words — or a sender whose own suppressor has gated the room out — winding the
	// gain up to the ceiling and handing it back as a roar on the next syllable.
	// Well under any real voice and well over what a silent lane carries.
	levelFloor = 58

	// How far the gain may move either way, ±12 dB. A ceiling because the correction
	// is worth having and the belief behind it is not: a lane this far out is a
	// microphone problem, and going further only amplifies its noise floor.
	levelMin = 0.25
	levelMax = 4

	// The per-sample one-pole steps, 1 - exp(-1 / (t × 48 kHz)). Down over 300 ms
	// and up over 2 s: a voice that has got louder has to stop being loud within a
	// syllable, where one that has gone quiet can be brought up across a sentence
	// without the room audibly coming up with it.
	levelAttackStep  = 0.00006944
	levelReleaseStep = 0.00001042

	// The slope a lane that has not found its level yet is walked at, and how much
	// speech it takes to have found one — 150 ms, over the first half-second.
	//
	// Somebody who has just joined sits at unity because nothing has been measured
	// yet, not because unity suits them, and on the ordinary slopes a quiet talker
	// stays quiet for their whole first turn: the release is two seconds of *speech*,
	// which is most of a minute of conversation. Half a second averages a syllable
	// or two rather than snapping to one block, which at this length is a swell
	// nobody hears as an edge.
	levelGrabStep   = 0.00013888
	levelGrabBlocks = 25
)

// retarget picks what the lane is aiming at from the block about to be played,
// and which slope to approach it on. Once a block; next is what walks it.
func (lv *leveller) retarget(on bool, block []int16) {
	if !on {
		lv.target = 1
	} else if rms := blockRMS(block); rms > levelFloor {
		lv.target = min(max(levelTarget/rms, levelMin), levelMax)

		if lv.blocks < levelGrabBlocks {
			lv.blocks++
		}
	}

	switch {
	case lv.blocks < levelGrabBlocks:
		lv.step = levelGrabStep
	case lv.target < lv.gain:
		lv.step = levelAttackStep
	default:
		lv.step = levelReleaseStep
	}
}

// next advances the gain one sample and answers with it. The only smoothing in
// the whole arrangement, which is why the aim itself needs none: a block's raw
// RMS is noisy and this is what a 300 ms slope does to noise.
func (lv *leveller) next() float32 {
	lv.gain += (lv.target - lv.gain) * lv.step

	return lv.gain
}

// blockRMS is how loud one block was. Summed as int64 rather than as float32,
// which a thousand squared samples would overflow the mantissa of long before
// the end of the block.
func blockRMS(block []int16) float32 {
	var sum int64
	for _, s := range block {
		sum += int64(s) * int64(s)
	}

	return float32(math.Sqrt(float64(sum) / float64(len(block))))
}

/* Placement */

// panLeft and panRight are what one lane's mono sample is scaled by into each
// ear. Written once at init and read-only after, the arc being the same for
// every call this process ever mixes.
//
// Constant power: the pair squares to 2 wherever a lane sits, which is what an
// unmoved lane — unity into both — already summed to. A linear pan would leave
// whoever is at centre about 3 dB down on whoever is at the edge, so moving
// somebody would be a change in how loud they are as well as where.
var panLeft, panRight [maxLanes]float32

// Where each lane sits, as an offset from centre in radians. Lanes take these in
// the order the sink hands slots out, alternating either side so the first person
// in front of the reader and the next two one step out on each side.
//
// panSpread is the arc's half-width, about 14°. Wide enough for the ear to pull
// two simultaneous talkers apart — which is the whole win, the brain separating
// voices by the level difference between the ears — and narrow enough that nobody
// sounds like they are in the next room. Seven distinct places, then the ends
// repeat: past that a call is beyond what anybody localises anyway.
//
// It is bounded from the other side too. The pan is applied *after* the limiter,
// so the loud ear of a limited lane lands at the ceiling times the widest scale
// here — ×1.216 of the 70 % knee, which is 85 % of full scale. Widening this past
// ×1.43 would put a limited lane over the top and leave softClip holding what the
// limiter was there to prevent.
const (
	panSpread = 0.25
	panStep   = panSpread / 3
)

func init() {
	for i := range maxLanes {
		steps := (i + 1) / 2
		if i%2 == 0 {
			steps = -steps
		}

		offset := min(max(float64(steps)*panStep, -panSpread), panSpread)

		// π/4 is centre, where cos and sin are equal and √2 × either is exactly 1.
		angle := math.Pi/4 + offset

		panLeft[i] = float32(math.Sqrt2 * math.Cos(angle))
		panRight[i] = float32(math.Sqrt2 * math.Sin(angle))
	}
}

// Where soft clipping starts, as a percentage of full scale. Below the knee a
// sample is untouched, which is nearly all of them: the branch is what keeps the
// curve off the ordinary path.
//
// A percentage because the two forms are then both constants — an untyped float
// does not convert to int32 — and because one number is one number: a knee the
// float path and the sample path disagreed about would be a step at the seam.
const (
	softKneePercent = 70
	softKnee        = softKneePercent / 100.0
)

// softClip folds a normalised sample towards the ceiling instead of against it.
//
// A hard clamp slices the top off a wave, and a sliced sinusoid is a square one —
// harmonics the signal never had, which is the buzz on the loudest syllable of an
// over-amplified microphone. tanh approaches the ceiling instead of meeting it,
// so a peak rounds over: the loudest part of a word loses some of its shape and
// none of the rest of it does.
//
// Scaled so the curve leaves the knee at slope 1 (sech²(0) = 1), which is what
// makes it continuous with the untouched signal below — a knee with a corner in
// it would be audible in its own right.
func softClip(v float32) float32 {
	if v > -softKnee && v < softKnee {
		return v
	}

	sign := float32(1)
	if v < 0 {
		sign, v = -1, -v
	}

	const headroom = 1 - softKnee

	return sign * (softKnee + headroom*float32(math.Tanh(float64((v-softKnee)/headroom))))
}

// softClipSample is the same curve on the mixer's accumulator, which counts in
// whole samples rather than in a normalised range. tanh never reaches 1, so the
// result is always inside int16 and needs no clamp after it.
func softClipSample(v int32) int16 {
	if v > -softKneeSample && v < softKneeSample {
		return int16(v)
	}

	return int16(softClip(float32(v)/sampleCeiling) * sampleCeiling)
}

const (
	sampleCeiling  = 32767
	softKneeSample = int32(sampleCeiling * softKneePercent / 100)
)

func floatBits(v float32) uint32 { return *(*uint32)(unsafe.Pointer(&v)) }
func bitsFloat(v uint32) float32 { return *(*float32)(unsafe.Pointer(&v)) }

// asFloats views a device callback's bytes as the float32 samples they already
// are. The backend hands the same buffer back every period, so this is a view
// rather than a copy and must not outlive the callback.
func asFloats(buf []byte) []float32 {
	return unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(buf))), len(buf)/4)
}

// floatToSample converts one normalised sample to the device's own format,
// clamping rather than wrapping: a gain past unity would otherwise turn a loud
// voice into noise at the opposite polarity.
func floatToSample(v float32) int16 {
	return clampSample(int32(v * 32767))
}
