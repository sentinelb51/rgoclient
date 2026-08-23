package audio

import (
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

	lanes [maxLanes]lane

	// wake is how the speakers ask for more. The callback sends into it without
	// waiting once a period, and whoever fills the lanes tops them back up to
	// laneTarget — so playout is paced by the device's own clock rather than by a
	// timer running beside it. Buffered by one: a fill still in progress needs no
	// second wake, it re-reads every lane's Want anyway.
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
	}

	return m
}

// play queues a sound. Producer side, and dropped when the ring is full: a
// click owed to a keystroke already gone is worth nothing.
func (m *mixer) play(cmd playCmd) bool { return m.commands.Push(cmd) }

// setMaster sets the gain every lane is scaled by.
func (m *mixer) setMaster(gain float32) { m.master.Store(floatBits(clampGain(gain))) }

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
	// notification sounds with no call open wakes nobody. The send is the
	// one the capture callback already makes: it never waits, so a filler that has
	// not come back yet costs this thread nothing.
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
// stereo, so one sample lands in both ears.
//
// A lane with nothing waiting contributes silence rather than stretching what it
// had: a call whose sender has stopped should go quiet, not buzz.
func (m *mixer) mixLanes(acc []int32, frames int) {
	master := bitsFloat(m.master.Load())

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

		gain := bitsFloat(l.gain.Load()) * master
		for j := range n {
			sample := int32(float32(m.pull[j]) * gain)
			acc[j*channelCount] += sample
			acc[j*channelCount+1] += sample
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

// clampGain bounds anything a gain arrives as. maxGain is where amplification
// stops being loudness and becomes clipping, and is the 200 % the settings offer
// as their own ceiling.
func clampGain(gain float32) float32 { return min(max(gain, 0), maxGain) }

const maxGain = 2

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
