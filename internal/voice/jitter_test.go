package voice

import (
	"testing"
	"time"
)

// The jitter buffer is where a wrong answer is least visible: everything it gets
// wrong sounds like a bad connection. These cover the cases that are wrong
// silently — ordering, a sequence wrap, the FEC hand-off, and the two depth
// adjustments that are only inaudible because of where they are taken.

// frame is a payload long enough to read as speech rather than as Opus's comfort
// noise, marked so a test can say which packet came back. The distinction is
// load-bearing now: the buffer changes depth for free on comfort noise and only
// grudgingly on anything else, so a one-byte stand-in would silently exercise
// the wrong path.
func frame(mark byte) []byte { return []byte{mark, 0, 0, 0} }

// quiet is the other one: short enough to be the comfort noise a shut gate
// sends, which is where a depth change costs nothing to hear.
func quiet(mark byte) []byte { return []byte{mark} }

// stamp is the RTP timestamp a sequence implies for a sender emitting every
// 20 ms. Nothing below reaches the estimator's minimum sample count, so this is
// only here to be the shape the signature asks for.
func stamp(sequence uint16) uint32 { return uint32(sequence) * frameSize }

func testJitter() *adaptiveJitter { return newAdaptiveJitter(JitterBalanced) }

func TestJitterOrdersAndFills(t *testing.T) {
	j := testJitter()

	// Nothing plays until the buffer reaches its depth.
	j.Push(100, stamp(100), frame(1))
	if _, _, ok := j.Pop(); ok {
		t.Fatal("played before filling")
	}

	j.Push(102, stamp(102), frame(3)) // out of order on purpose
	j.Push(101, stamp(101), frame(2))

	for want := byte(1); want <= 3; want++ {
		payload, _, ok := j.Pop()
		if !ok {
			t.Fatalf("nothing at %d", want)
		}
		if payload == nil || payload[0] != want {
			t.Fatalf("out of order: got %v want %d", payload, want)
		}
	}
}

// A hole with its successor already buffered must offer that successor, which is
// the only thing that makes in-band FEC usable: Opus hides a copy of a frame
// inside the packet after it.
func TestJitterOffersSuccessorForFEC(t *testing.T) {
	j := testJitter()

	j.Push(10, stamp(10), frame(1))
	j.Push(12, stamp(12), frame(3)) // 11 never arrives
	j.Push(13, stamp(13), frame(4))

	if p, _, ok := j.Pop(); !ok || p[0] != 1 {
		t.Fatalf("first: %v %v", p, ok)
	}

	payload, next, ok := j.Pop()
	if !ok {
		t.Fatal("a lost packet stalled the buffer")
	}
	if payload != nil {
		t.Fatalf("lost packet produced %v", payload)
	}
	if next == nil || next[0] != 3 {
		t.Fatalf("successor not offered for FEC: %v", next)
	}

	if p, _, ok := j.Pop(); !ok || p[0] != 3 {
		t.Fatalf("after the hole: %v %v", p, ok)
	}
}

// A hole whose successor is missing too has nothing to recover from, and the
// decoder should be told to conceal rather than handed a wrong frame. Audio is
// still buffered behind the two holes — a buffer with nothing at all waiting is
// a paused stream, which freezes rather than concealing.
func TestJitterOffersNothingWhenSuccessorMissing(t *testing.T) {
	j := testJitter()

	j.Push(20, stamp(20), frame(1))
	j.Push(21, stamp(21), frame(2))
	j.Push(22, stamp(22), frame(3))
	j.Push(25, stamp(25), frame(6)) // 23 and 24 are both absent
	j.Pop()
	j.Pop()
	j.Pop()

	payload, next, ok := j.Pop()
	if !ok || payload != nil || next != nil {
		t.Fatalf("expected a bare hole, got payload=%v next=%v ok=%v", payload, next, ok)
	}
}

// A stream that pauses — a mute, a DTX gap — must not read as starvation: the
// cursor freezes where the sender stopped, so the resumed stream plays from its
// first packet, and nothing about the pause moves the depth.
func TestJitterFreezesOnPause(t *testing.T) {
	j := testJitter()

	for seq := uint16(30); seq < 33; seq++ {
		j.Push(seq, stamp(seq), frame(byte(seq)))
	}
	for range 3 {
		j.Pop()
	}

	if _, _, ok := j.Pop(); ok {
		t.Fatal("an empty buffer produced a frame")
	}
	if j.next != 33 {
		t.Fatalf("the cursor walked on during the pause: %d", j.next)
	}

	// The sender resumes where it left off; its packets must not be read as late.
	before := j.depth
	for seq := uint16(33); seq < 36; seq++ {
		j.Push(seq, stamp(seq), frame(byte(seq)))
	}

	payload, _, ok := j.Pop()
	if !ok || payload == nil || payload[0] != 33 {
		t.Fatalf("resumed stream lost its first packet: %v %v", payload, ok)
	}
	if j.depth != before {
		t.Fatalf("a pause moved the depth: %d -> %d", before, j.depth)
	}
}

func TestJitterDropsLatePackets(t *testing.T) {
	j := testJitter()

	j.Push(50, stamp(50), frame(1))
	j.Push(51, stamp(51), frame(2))
	j.Push(52, stamp(52), frame(3))
	j.Pop()
	j.Pop()

	j.Push(50, stamp(50), frame(99)) // now in the past: must not reappear

	if p, _, ok := j.Pop(); !ok || p[0] != 3 {
		t.Fatalf("a late packet displaced the cursor: %v %v", p, ok)
	}
}

func TestJitterSurvivesSequenceWrap(t *testing.T) {
	j := testJitter()

	for i, seq := range []uint16{65534, 65535, 0, 1} {
		j.Push(seq, stamp(seq), frame(byte(i)))
	}

	for want := byte(0); want < 4; want++ {
		p, _, ok := j.Pop()
		if !ok {
			t.Fatalf("nothing at %d", want)
		}
		if p == nil || p[0] != want {
			t.Fatalf("wrap lost order at %d: %v", want, p)
		}
	}
}

// held is maintained rather than counted, so it has to survive the operations
// that can double-count: an overwrite, and a reset.
func TestJitterHeldCountStaysHonest(t *testing.T) {
	j := testJitter()

	j.Push(5, stamp(5), frame(1))
	j.Push(5, stamp(5), frame(2)) // duplicate sequence, already held
	if j.held != 1 {
		t.Fatalf("duplicate double-counted: held=%d", j.held)
	}

	j.Push(4000, stamp(4000), frame(3)) // far ahead: forces a reset
	if j.held != 1 {
		t.Fatalf("reset left a stale count: held=%d", j.held)
	}
}

/* The depth */

// The estimator is what depth is taken from now, and it is pure — arrival times
// in, a percentile out — so it can be driven at speed rather than in real time.
func TestDelayEstimatorPercentile(t *testing.T) {
	var e delayEstimator

	// Four hundred arrivals on a 20 ms cadence, every twentieth of them 60 ms
	// late. One arrival in twenty is the 95th percentile exactly, so the 90th
	// should see none of it and the 99th all of it.
	base := time.Now()
	for i := range 400 {
		at := base.Add(time.Duration(i) * frameMillis * time.Millisecond)
		if i%20 == 19 {
			at = at.Add(60 * time.Millisecond)
		}

		e.observe(at, uint32(i)*frameSize)
	}

	if late, ok := e.lateness(900); !ok || late > lateBucket {
		t.Errorf("p90 should have been on time: %v ok=%v", late, ok)
	}
	if late, ok := e.lateness(990); !ok || late < 55*time.Millisecond {
		t.Errorf("p99 should have seen the late twentieth: %v ok=%v", late, ok)
	}
}

// Nothing is worth acting on before enough has been measured, or the first four
// packets of a call set the depth for it.
func TestDelayEstimatorWaitsForEvidence(t *testing.T) {
	var e delayEstimator

	base := time.Now()
	for i := range minObserved - 1 {
		e.observe(base.Add(time.Duration(i)*frameMillis*time.Millisecond), uint32(i)*frameSize)
	}

	if _, ok := e.lateness(980); ok {
		t.Fatalf("answered a percentile from %d arrivals", minObserved-1)
	}
}

// The defect this replaced: depth used to move on whether the buffer had run
// dry, so it had to produce a dropout to learn it was too shallow. Lateness that
// was absorbed is evidence too, and this is what says it now counts.
func TestJitterDepthFollowsLateness(t *testing.T) {
	j := testJitter()

	base := time.Now()
	for i := range 400 {
		at := base.Add(time.Duration(i) * frameMillis * time.Millisecond)
		if i%20 == 19 {
			at = at.Add(70 * time.Millisecond)
		}

		j.delay.observe(at, uint32(i)*frameSize)
	}

	before := j.depth
	j.retarget(base.Add(400 * frameMillis * time.Millisecond))

	if j.depth <= before {
		t.Fatalf("70 ms of measured lateness moved nothing: depth %d -> %d", before, j.depth)
	}
	if j.depth > j.ceiling() {
		t.Fatalf("depth ran past the profile's ceiling: %d > %d", j.depth, j.ceiling())
	}
}

// A profile is a ceiling as well as a target, and lowering it must bring a
// buffer already deeper than the new one back under it.
func TestJitterProfileCapsDepth(t *testing.T) {
	j := testJitter()

	j.depth = 9
	j.SetProfile(JitterResponsive)

	if want := j.ceiling(); j.depth > want {
		t.Fatalf("depth %d survived a profile capped at %d", j.depth, want)
	}
}

/* Where a depth change is taken */

// Comfort noise is where shrinking is free, and the buffer converges inside one
// gap because nothing in it can be heard.
func TestJitterShrinksThroughSilence(t *testing.T) {
	j := testJitter()

	for seq := range uint16(8) {
		j.Push(seq, stamp(seq), quiet(byte(seq)))
	}

	payload, _, ok := j.Pop()
	if !ok || payload == nil {
		t.Fatalf("nothing played: %v %v", payload, ok)
	}

	// Five of the eight were silence held as delay. They go, and what plays is
	// what stood behind them.
	if payload[0] != 5 {
		t.Fatalf("silence was played rather than skipped: got %d, want 5", payload[0])
	}
	if want := j.depth - 1; j.held != want {
		t.Fatalf("held %d after the shrink, want %d", j.held, want)
	}
}

// The same buffer with somebody talking in it must keep every packet: the grace
// is there so a talker who does not pause is not cut mid-word to save 20 ms.
func TestJitterKeepsSpeechWhileOverDeep(t *testing.T) {
	j := testJitter()

	for seq := range uint16(8) {
		j.Push(seq, stamp(seq), frame(byte(seq)))
	}

	payload, _, ok := j.Pop()
	if !ok || payload == nil || payload[0] != 0 {
		t.Fatalf("a talker was cut to shrink the buffer: %v ok=%v", payload, ok)
	}
	if j.held != 7 {
		t.Fatalf("held %d, want 7 — nothing should have been dropped", j.held)
	}
}

// And the other direction. A depth just raised takes the delay back in by
// replaying comfort noise: the cursor stands still, and what arrives behind it
// is what deepens the buffer.
func TestJitterGrowsThroughSilence(t *testing.T) {
	j := testJitter()

	for seq := range uint16(3) {
		j.Push(seq, stamp(seq), quiet(byte(seq)))
	}
	if _, _, ok := j.Pop(); !ok {
		t.Fatal("nothing played")
	}

	j.depth = 6 // the estimator has just asked for more

	before := j.next
	if _, _, ok := j.Pop(); !ok {
		t.Fatal("nothing played while deepening")
	}

	if j.next != before {
		t.Fatalf("the cursor advanced while deepening: %d -> %d", before, j.next)
	}
	if j.stalls != 1 {
		t.Fatalf("stalls %d, want 1", j.stalls)
	}
}

// A sender that stops in the middle of a gap would otherwise leave the lane
// replaying one packet for the rest of the call, and nothing would report it.
func TestJitterStallsAreBounded(t *testing.T) {
	j := testJitter()

	for seq := range uint16(3) {
		j.Push(seq, stamp(seq), quiet(byte(seq)))
	}
	j.Pop()

	j.depth = maxDepth // deeper than anything arriving can fill

	for range stallBudget + 4 {
		j.Pop()
	}

	if j.stalls > stallBudget {
		t.Fatalf("stalls ran past the budget: %d > %d", j.stalls, stallBudget)
	}
	if j.next == 1 {
		t.Fatal("the cursor never moved on: the lane is stuck replaying one packet")
	}
}
