package voice

import (
	"sync"
	"time"
)

// Jitter is the buffer between the network and the speakers: packets go in as
// they arrive, and come out in order at the rate they are played.
//
// It is an interface because it is where most of the latency lives — 60-80 % of
// mouth-to-ear — and so it is the one part worth being able to replace without
// anything above noticing.
type Jitter interface {
	// Push files a packet. Out-of-order and duplicate sequence numbers are the
	// buffer's problem, not the caller's.
	Push(sequence uint16, payload []byte)

	// Pop takes the next packet to play. ok is false when the buffer is still
	// filling and there is nothing to play yet.
	//
	// payload nil with ok true is a packet that did not arrive. next is then the
	// packet *after* it, when that one has arrived — which is what makes in-band
	// FEC usable at all: Opus carries a copy of a frame inside its successor, so
	// recovering the hole means decoding next with the FEC flag rather than
	// concealing from the frame before. next is nil when there is nothing to
	// recover from and the decoder should conceal instead.
	Pop() (payload, next []byte, ok bool)

	// Loss is the percentage of packets that did not arrive, over a recent
	// window — or negative while no full window has been measured yet, so a
	// caller seeding FEC ahead of a measurement is not overwritten with a zero
	// that measured nothing. It is what retunes the encoder's FEC.
	Loss() int
}

// Buffer depths, in packets of 20 ms. The buffer starts shallow and deepens only
// when it is actually starved: a call on a good connection should not pay for
// one on a bad connection's worst moment.
//
// The floor is two rather than one because a buffer of one is not a buffer — it
// has nowhere to put a packet that arrives while the previous one is playing.
const (
	minDepth     = 2  // 40 ms
	maxDepth     = 12 // 240 ms, past which a call is unpleasant either way
	initialDepth = 3  // 60 ms
)

// How the depth moves. Growing is immediate because a starve has already been
// heard; shrinking waits for a long clean run, because a buffer that shrinks
// eagerly starves again and the oscillation is worse than the latency.
const (
	cleanRunToShrink = 250 // packets played without running dry, so five seconds
	lossWindow       = 200 // packets the loss percentage is measured over

	// starveWindow tells a starve from a pause when the buffer refills after
	// running dry: packets back this soon were late rather than stopped, and
	// lateness is what depth exists to absorb. A sender that stopped on purpose
	// — a mute, a DTX gap between sentences — resumes far later, and deepening
	// for those would ratchet an ordinary conversation to maxDepth.
	starveWindow = 500 * time.Millisecond
)

// adaptiveJitter is the default Jitter: a sequence-indexed ring, a playout
// cursor, and a depth that answers starvation.
//
// It is small on purpose. The interesting version of this reorders against
// arrival timestamps and models the network's delay distribution; this one holds
// a target depth and adapts it, which is most of the benefit for none of the
// risk, and the interface is what makes replacing it a change of one line.
type adaptiveJitter struct {
	mu sync.Mutex

	// slots is indexed by sequence number modulo its length, so a packet's place
	// is arithmetic rather than a search. Sized well past maxDepth so a burst of
	// early packets is held rather than dropped.
	slots  [64][]byte
	filled [64]bool

	next    uint16 // the sequence to play next
	started bool

	// held is what buffered() used to count by walking every slot, on every Pop of
	// every participant. Maintained instead, the two places that change it being
	// the only ones that can.
	held int

	depth    int
	filling  bool
	cleanRun int

	// emptiedAt is when the buffer last ran dry, so the refill that follows can
	// tell a starve from a sender that stopped.
	emptiedAt time.Time

	arrived, expected int // over the loss window
	lossPercent       int
}

func newAdaptiveJitter() *adaptiveJitter {
	// lossPercent negative until a window completes: zero would read as a
	// measured clean connection before anything has been measured at all.
	return &adaptiveJitter{depth: initialDepth, filling: true, lossPercent: -1}
}

func (j *adaptiveJitter) Push(sequence uint16, payload []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.started {
		j.next = sequence
		j.started = true
	}

	// A packet older than the cursor is late past use: it would play out of order
	// and the gap it belonged to has already been concealed.
	if int16(sequence-j.next) < 0 {
		return
	}

	// One further ahead than the ring can hold means the cursor is stuck — the
	// stream jumped, or a long gap was never played through. Restart on it rather
	// than dropping everything until it wraps.
	if int(uint16(sequence-j.next)) >= len(j.slots) {
		j.reset(sequence)
	}

	at := int(sequence) % len(j.slots)
	if !j.filled[at] {
		j.held++
	}
	j.slots[at] = payload
	j.filled[at] = true
}

func (j *adaptiveJitter) Pop() ([]byte, []byte, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.started {
		return nil, nil, false
	}

	if j.filling {
		if j.held < j.depth {
			return nil, nil, false
		}
		j.filling = false

		// Refilled on the heels of running dry: that was a starve — the packets
		// were merely late, and lateness is what depth absorbs. A refill after a
		// long quiet is a sender that had stopped, which says nothing about it.
		if !j.emptiedAt.IsZero() && time.Since(j.emptiedAt) < starveWindow && j.depth < maxDepth {
			j.depth++
		}
	}

	// Nothing at all waiting: the stream has paused — a mute, a DTX gap, or the
	// network dying. Freeze rather than conceal: the cursor stays on the packet
	// the sender produces next, so a resumed stream plays from its first packet
	// instead of losing it as late behind a cursor that walked on, and a pause
	// is not booked as loss for the encoder to buy FEC against. Whether the
	// pause *was* a starve is decided above, by how quickly the refill follows.
	if j.held == 0 {
		j.cleanRun = 0
		j.filling = true
		j.emptiedAt = time.Now()

		return nil, nil, false
	}

	at := int(j.next) % len(j.slots)

	payload, arrived := j.slots[at], j.filled[at]
	j.slots[at], j.filled[at] = nil, false
	if arrived {
		j.held--
	}
	j.next++

	j.expected++
	if arrived {
		j.arrived++
		j.cleanRun++

		if j.cleanRun >= cleanRunToShrink && j.depth > minDepth {
			j.depth--
			j.cleanRun = 0
			j.drain()
		}

		j.rollLoss()

		return payload, nil, true
	}

	// A hole with audio still behind it: loss, not starvation. Recover or
	// conceal it and carry on — playout is unharmed, so the clean run stands:
	// counting a hole would mean any steady loss above one packet in
	// cleanRunToShrink stops the depth ever shrinking.

	// The successor, if it is already here: Opus hid a copy of this frame in it.
	var next []byte
	if after := int(j.next) % len(j.slots); j.filled[after] {
		next = j.slots[after]
	}

	j.rollLoss()

	return nil, next, true
}

// drain drops what stands between the buffer's occupancy and a freshly lowered
// depth. Without it a shrink moves only the refill target, and the audio
// already held keeps playout exactly as far behind as the worst burst pushed
// it, for the rest of the call. One packet per shrink — a 20 ms seam every five
// clean seconds until the latency is taken back out — and never across a hole,
// whose recovery belongs to the FEC hand-off.
func (j *adaptiveJitter) drain() {
	for j.held > j.depth {
		at := int(j.next) % len(j.slots)
		if !j.filled[at] {
			break
		}

		j.slots[at], j.filled[at] = nil, false
		j.held--
		j.next++
	}
}

func (j *adaptiveJitter) Loss() int {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.lossPercent
}

// rollLoss recomputes the window's loss and starts the next one. Recomputing
// rather than decaying keeps the number something a person can reason about: it
// is the last lossWindow packets and nothing else.
func (j *adaptiveJitter) rollLoss() {
	if j.expected < lossWindow {
		return
	}

	j.lossPercent = (j.expected - j.arrived) * 100 / j.expected
	j.arrived, j.expected = 0, 0
}

// reset moves the cursor to a sequence the stream has actually reached, dropping
// what was held for the old one.
func (j *adaptiveJitter) reset(sequence uint16) {
	clear(j.slots[:])
	clear(j.filled[:])
	j.held = 0

	j.next = sequence
	j.filling = true
	j.emptiedAt = time.Time{} // a jump is a new stream, not a starve to deepen for
}
