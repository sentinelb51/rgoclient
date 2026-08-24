package voice

import (
	"testing"
	"time"
)

// The jitter buffer is where a wrong answer is least visible: everything it gets
// wrong sounds like a bad connection. These cover the cases that are wrong
// silently — ordering, a sequence wrap, and the FEC hand-off.

func TestJitterOrdersAndFills(t *testing.T) {
	j := newAdaptiveJitter()

	// Nothing plays until the buffer reaches its depth.
	j.Push(100, []byte{1})
	if _, _, ok := j.Pop(); ok {
		t.Fatal("played before filling")
	}

	j.Push(102, []byte{3}) // out of order on purpose
	j.Push(101, []byte{2})

	for want := byte(1); want <= 3; want++ {
		payload, _, ok := j.Pop()
		if !ok {
			t.Fatalf("nothing at %d", want)
		}
		if len(payload) != 1 || payload[0] != want {
			t.Fatalf("out of order: got %v want %d", payload, want)
		}
	}
}

// A hole with its successor already buffered must offer that successor, which is
// the only thing that makes in-band FEC usable: Opus hides a copy of a frame
// inside the packet after it.
func TestJitterOffersSuccessorForFEC(t *testing.T) {
	j := newAdaptiveJitter()

	j.Push(10, []byte{1})
	j.Push(12, []byte{3}) // 11 never arrives
	j.Push(13, []byte{4})

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
	j := newAdaptiveJitter()

	j.Push(20, []byte{1})
	j.Push(21, []byte{2})
	j.Push(22, []byte{3})
	j.Push(25, []byte{6}) // 23 and 24 are both absent
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
// first packet, and a refill that long after the drain leaves the depth alone.
func TestJitterFreezesOnPause(t *testing.T) {
	j := newAdaptiveJitter()

	for seq := uint16(30); seq < 33; seq++ {
		j.Push(seq, []byte{byte(seq)})
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
	j.emptiedAt = time.Now().Add(-time.Second) // the pause was long, not a starve
	for seq := uint16(33); seq < 36; seq++ {
		j.Push(seq, []byte{byte(seq)})
	}

	payload, _, ok := j.Pop()
	if !ok || payload == nil || payload[0] != 33 {
		t.Fatalf("resumed stream lost its first packet: %v %v", payload, ok)
	}
	if j.depth != before {
		t.Fatalf("a pause deepened the buffer: %d -> %d", before, j.depth)
	}
}

func TestJitterDropsLatePackets(t *testing.T) {
	j := newAdaptiveJitter()

	j.Push(50, []byte{1})
	j.Push(51, []byte{2})
	j.Push(52, []byte{3})
	j.Pop()
	j.Pop()

	j.Push(50, []byte{99}) // now in the past: must not reappear

	if p, _, ok := j.Pop(); !ok || p[0] != 3 {
		t.Fatalf("a late packet displaced the cursor: %v %v", p, ok)
	}
}

func TestJitterSurvivesSequenceWrap(t *testing.T) {
	j := newAdaptiveJitter()

	for i, seq := range []uint16{65534, 65535, 0, 1} {
		j.Push(seq, []byte{byte(i)})
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

func TestJitterDeepensOnStarvation(t *testing.T) {
	j := newAdaptiveJitter()

	before := j.depth
	for range 3 {
		for seq := range initialDepth {
			j.Push(uint16(int(j.next)+seq), []byte{1})
		}
		for range initialDepth + 1 {
			j.Pop()
		}
	}

	if j.depth <= before {
		t.Fatalf("depth stayed at %d after repeated starvation", j.depth)
	}
	if j.depth > maxDepth {
		t.Fatalf("depth ran past the ceiling: %d", j.depth)
	}
}

// held is maintained rather than counted, so it has to survive the operations
// that can double-count: an overwrite, and a reset.
func TestJitterHeldCountStaysHonest(t *testing.T) {
	j := newAdaptiveJitter()

	j.Push(5, []byte{1})
	j.Push(5, []byte{2}) // duplicate sequence, already held
	if j.held != 1 {
		t.Fatalf("duplicate double-counted: held=%d", j.held)
	}

	j.Push(4000, []byte{3}) // far ahead: forces a reset
	if j.held != 1 {
		t.Fatalf("reset left a stale count: held=%d", j.held)
	}
}
