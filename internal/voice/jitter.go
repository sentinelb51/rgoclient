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
	//
	// timestamp is the RTP one, in 48 kHz samples. It is what the packet was
	// *meant* to play at, and without it lateness cannot be told from a sender
	// that simply stopped for a while: sequence numbers say what order packets go
	// in and nothing at all about when.
	Push(sequence uint16, timestamp uint32, payload []byte)

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

	// Drift is how many frames the buffer is holding beyond the depth it wants,
	// negative when it is short of it. It is what the player time-scales against:
	// a buffer can correct itself for free only in the comfort noise a talker
	// leaves behind, and reporting what is left over is how a source that never
	// goes quiet gets corrected in the decoded audio instead. Zero is nothing to
	// do, and is the right answer for a buffer with no notion of depth.
	Drift() int

	// Loss is the percentage of packets that did not arrive, over a recent
	// window — or negative while no full window has been measured yet, so a
	// caller seeding FEC ahead of a measurement is not overwritten with a zero
	// that measured nothing. It is what retunes the encoder's FEC.
	Loss() int

	// SetProfile moves what the buffer is aiming at, mid-call. A buffer that
	// cannot act on one may ignore it: the profile is a preference, not a
	// contract about depth.
	SetProfile(profile JitterProfile)
}

/* What the buffer is aiming at */

// JitterProfile is the trade the buffer exists to make: how much of the
// network's own lateness to cover, and how much delay may be spent covering it.
//
// Percentile is per *mille*. The interesting values are all at the top of the
// range and per cent cannot express the gap between them — covering 99 % of
// arrivals and covering 99.5 % is the difference between a dropout every two
// seconds and one every four.
type JitterProfile struct {
	Percentile int
	MaxDelay   time.Duration
}

// The three profiles offered, chosen off a simulated sweep rather than picked
// for roundness. On a connection with no tail to speak of they are the same
// buffer — 41 ms against 44 ms, no dropouts at any of them — so what separates
// them only shows once a link starts queueing. Over five minutes of one talker,
// mean delay and dropouts a minute:
//
//	          busy wifi        a link with a 40-110 ms tail on 2 %
//	p98       67 ms  11.6         88 ms  23.0
//	p99       77 ms   9.2        112 ms   4.0
//	p99.5     91 ms   4.8        124 ms   0.6
//
// They differ in how long they take to say anything as well as in what they say:
// a finer percentile needs more arrivals before its answer is about the
// distribution rather than about one packet, which is enough() and works out at
// four, six and twelve seconds of somebody talking. That is the real reason not
// to put the default at the finest one.
var (
	JitterResponsive = JitterProfile{Percentile: 980, MaxDelay: 100 * time.Millisecond}
	JitterBalanced   = JitterProfile{Percentile: 990, MaxDelay: 200 * time.Millisecond}
	JitterSmooth     = JitterProfile{Percentile: 995, MaxDelay: 300 * time.Millisecond}
)

/* The delay estimator */

// Buckets the arrival-lateness histogram is kept in. 5 ms is finer than the
// 20 ms one depth step buys, so the percentile is never the thing rounding.
const (
	lateBucket  = 5 * time.Millisecond
	lateBuckets = 80 // 400 ms, the last of them everything past it
)

// windowSpan is how long one histogram collects before it becomes the previous
// one. A percentile is read across both, so the answer describes between ten and
// twenty seconds of arrivals: long enough that one bad moment does not move it,
// short enough that a connection which has genuinely recovered is believed
// inside a sentence.
const windowSpan = 10 * time.Second

// floorSpan is how far back the zero every lateness is measured from is taken.
// Deliberately far shorter than the histogram, for two reasons.
//
// A delay that went up and *stayed* up is not jitter, it is the connection's new
// length, and buffering against it would spend delay to cover delay. What a
// buffer absorbs is variation around the recent normal, so the normal has to be
// recent.
//
// And it bounds what the two clocks can do. Transit is a wall clock here minus a
// sample clock there, so it carries their drift; over a second that is
// microseconds for any real pair, where over the histogram's own twenty it need
// not be. That is measured rather than assumed: a test publisher paced by a
// coarse Windows sleep drifts about 2.8 %, and taking the floor over the whole
// histogram read it as 400 ms of lateness — the ceiling of the range — and
// pinned the buffer at its own.
const floorSpan = time.Second

// minObserved is the floor under enough(), whatever the percentile.
const minObserved = 200

// enough is how many arrivals a percentile needs before its answer is about the
// distribution rather than about one packet: a rank with nothing above it is the
// maximum wearing a percentile's name. It is also why the three profiles adapt
// at different speeds — the finer ones have to watch longer before they can say
// anything at all.
func enough(percentile int) int32 {
	return max(minObserved, int32(3000/(1000-percentile)))
}

// trailingMin is the smallest value seen recently, kept as a pair of windows so
// that forgetting is a rotation rather than a queue to walk.
type trailingMin struct {
	span time.Duration

	current, previous       time.Duration
	hasCurrent, hasPrevious bool
	rotated                 time.Time
}

func (m *trailingMin) observe(at time.Time, value time.Duration) {
	if m.rotated.IsZero() {
		m.rotated = at
	}

	if at.Sub(m.rotated) >= m.span {
		m.previous, m.hasPrevious = m.current, m.hasCurrent
		m.current, m.hasCurrent = 0, false
		m.rotated = at
	}

	if !m.hasCurrent || value < m.current {
		m.current, m.hasCurrent = value, true
	}
}

func (m *trailingMin) value() (time.Duration, bool) {
	switch {
	case m.hasCurrent && m.hasPrevious:
		return min(m.current, m.previous), true
	case m.hasPrevious:
		return m.previous, true
	case m.hasCurrent:
		return m.current, true
	default:
		return 0, false
	}
}

// lateWindow is one collection period: how arrivals were spread above the floor.
type lateWindow struct {
	buckets [lateBuckets]int32
	count   int32
}

// delayEstimator answers one question: how late does a packet arrive, at the
// percentile being asked about.
//
// Transit is arrival minus the packet's own RTP timestamp. The sender's clock
// and this one share no origin, so the number itself means nothing — only the
// spread does, which is why every packet is measured against the smallest
// transit seen in the last floorSpan or two.
type delayEstimator struct {
	current, previous lateWindow
	rotated           time.Time

	// floor is the zero, over its own much shorter pair of windows.
	floor trailingMin

	// base is the first arrival and its timestamp. Only differences from it are
	// ever taken, so it never needs moving.
	baseArrival   time.Time
	baseTimestamp uint32
	based         bool
}

// observe files one arrival.
func (e *delayEstimator) observe(arrival time.Time, timestamp uint32) {
	if !e.based {
		e.baseArrival, e.baseTimestamp, e.based = arrival, timestamp, true
		e.rotated = arrival
		e.floor.span = floorSpan
	}

	if arrival.Sub(e.rotated) >= windowSpan {
		e.previous, e.current = e.current, lateWindow{}
		e.rotated = arrival
	}

	// int32 so the RTP clock's wrap is a difference rather than a jump. At 48 kHz
	// that holds for twelve hours of one call, which is longer than the ring, the
	// token or the reader lasts.
	sent := time.Duration(int64(int32(timestamp-e.baseTimestamp))) * time.Second / sampleRate
	transit := arrival.Sub(e.baseArrival) - sent

	e.floor.observe(arrival, transit)

	// Against the floor as it stands now, which for the first packets of a call is
	// still falling — so the earliest of them read later than they were. That errs
	// deep, which is the safe direction, and it is gone within a floorSpan.
	zero, _ := e.floor.value()
	late := max(transit-zero, 0)

	at := min(int(late/lateBucket), lateBuckets-1)
	e.current.buckets[at]++
	e.current.count++
}

// lateness is how late the packet at the given per-mille rank arrived. ok is
// false until enough arrivals have been seen for the answer to mean anything,
// which is what keeps a buffer at its opening depth for the first few seconds
// rather than chasing four packets.
func (e *delayEstimator) lateness(percentile int) (time.Duration, bool) {
	total := e.current.count + e.previous.count
	if total < enough(percentile) {
		return 0, false
	}

	rank := int32(int64(total) * int64(percentile) / 1000)

	var seen int32
	for at := range lateBuckets {
		seen += e.current.buckets[at] + e.previous.buckets[at]

		// The bucket's top edge rather than its floor: covering a percentile means
		// covering everything filed at it.
		if seen >= rank {
			return time.Duration(at+1) * lateBucket, true
		}
	}

	return lateBuckets * lateBucket, true
}

/* The buffer */

// Buffer depths, in packets of 20 ms.
//
// The floor is two rather than one because a buffer of one is not a buffer — it
// has nowhere to put a packet that arrives while the previous one is playing.
// The ceiling here is the ring's rather than a policy: how deep a buffer may go
// is the profile's business, and this is only what the slots can hold.
const (
	minDepth     = 2
	initialDepth = 3
	maxDepth     = 24 // 480 ms, well past the deepest profile
)

// How the depth moves. Deepening is immediate: the lateness calling for it has
// already been measured, and waiting to confirm it is waiting to drop a packet.
// Shrinking waits for the estimator to keep saying so, because a buffer that
// shrinks on one quiet stretch starves on the next busy one, and the oscillation
// is worse than the delay.
const (
	shrinkHold    = 2 * time.Second
	retargetEvery = 50 // packets, so about once a second per talker
)

// stallBudget bounds how many comfort-noise frames may be replayed in a row to
// deepen the buffer, so a sender that stops in the middle of a gap cannot leave
// the lane replaying one packet for the rest of the call. Only the replay needs
// a budget: it is the one correction that does not advance the cursor, so it is
// the one that cannot end on its own.
const stallBudget = 25

// adaptiveJitter is the default Jitter: a sequence-indexed ring, a playout
// cursor, and a depth taken from the measured spread of arrivals.
//
// The depth is a *measurement* rather than a reaction. An earlier version moved
// it on one bit — did the buffer run dry — which meant it had to produce an
// audible gap to learn it was too shallow, and shrank on a clean run whether or
// not the connection had actually improved. On any link whose jitter sat between
// two depth steps those two rules chased each other: shrink blindly, starve,
// deepen, shrink again, a dropout every few seconds forever. Measuring every
// arrival instead means the spikes that were absorbed count as evidence too,
// which is what there is to hold a depth steady with.
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
	// every participant. Maintained instead, the places that change it being the
	// only ones that can.
	held int

	depth   int
	filling bool

	profile JitterProfile
	delay   delayEstimator

	// pushes counts toward the next retarget, so the histogram is walked about
	// once a second rather than once a packet.
	pushes int

	// shallowSince is when the estimator first asked for less depth than is held.
	// Cleared the moment it stops asking, so only a sustained case ever shrinks.
	shallowSince time.Time

	// stalls counts comfort-noise frames replayed in a row to deepen the buffer.
	stalls int

	arrived, expected int // over the loss window
	lossPercent       int
}

// lossWindow is how many packets the loss percentage is measured over.
const lossWindow = 200

func newAdaptiveJitter(profile JitterProfile) *adaptiveJitter {
	if profile.Percentile <= 0 || profile.MaxDelay <= 0 {
		profile = JitterBalanced
	}

	// lossPercent negative until a window completes: zero would read as a
	// measured clean connection before anything has been measured at all.
	return &adaptiveJitter{depth: initialDepth, filling: true, lossPercent: -1, profile: profile}
}

func (j *adaptiveJitter) Push(sequence uint16, timestamp uint32, payload []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Measured before anything else looks at it. A packet late enough to be past
	// the cursor is useless for playout and is dropped below — but its arrival is
	// precisely the evidence the depth is set from, and measuring after the drop
	// would blind the estimator to the only packets that ever cause a dropout.
	arrival := time.Now()
	j.delay.observe(arrival, timestamp)

	j.pushes++
	if j.pushes >= retargetEvery {
		j.pushes = 0
		j.retarget(arrival)
	}

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
	}

	if j.paused() {
		return nil, nil, false
	}

	// Comfort noise at the head is 20 ms nobody can hear, which makes it the one
	// place a depth change is free. Dropping one takes delay out; replaying one
	// puts delay in, the cursor standing still while arrivals pile up behind it.
	// Converging inside a single gap costs nothing, so the shrink loops.
	for j.held > j.depth && j.headQuiet() {
		j.skipHead()

		if j.paused() {
			return nil, nil, false
		}
	}

	if j.held < j.depth && j.headQuiet() && j.stalls < stallBudget {
		j.stalls++

		return j.slots[int(j.next)%len(j.slots)], nil, true
	}

	// Nothing else is corrected here. A talker who never leaves a gap is put back
	// on depth by time-scaling the decoded frame instead — see stretch.go — which
	// the player asks for through Drift and which costs no audio at all, where
	// every correction this buffer could make on its own is a 20 ms seam.

	// The budget is per deepening rather than per frame: cleared when the buffer
	// has actually reached its target, not by any advance. Clearing it on every
	// advance would let a sender that stopped mid-gap alternate a budget of
	// replays with one real packet, for as long as anything was held.
	if j.held >= j.depth {
		j.stalls = 0
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
		j.rollLoss()

		return payload, nil, true
	}

	// A hole with audio still behind it: loss, not starvation. Recover or conceal
	// it and carry on.

	// The successor, if it is already here: Opus hid a copy of this frame in it.
	var next []byte
	if after := int(j.next) % len(j.slots); j.filled[after] {
		next = j.slots[after]
	}

	j.rollLoss()

	return nil, next, true
}

// paused reports an empty buffer and arms the refill, which is the whole of what
// a stream stopping means here — a mute, a DTX gap, or the network dying.
//
// Freeze rather than conceal: the cursor stays on the packet the sender produces
// next, so a resumed stream plays from its first packet instead of losing it as
// late behind a cursor that walked on, and a pause is not booked as loss for the
// encoder to buy FEC against. Nothing deepens here either — a sender that has
// stopped says nothing about the network, and the estimator has already measured
// whatever lateness preceded the silence.
func (j *adaptiveJitter) paused() bool {
	if j.held > 0 {
		return false
	}

	j.filling = true
	j.stalls = 0

	return true
}

// Drift is how far the buffer is from the depth it wants, in frames, once the
// free corrections have had their chance at it: positive is holding too much
// delay, negative is too little.
//
// Zero while filling, which is not a state anything should correct — the buffer
// is deliberately below depth there and is about to reach it.
func (j *adaptiveJitter) Drift() int {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.started || j.filling {
		return 0
	}

	return j.held - j.depth
}

// headQuiet is whether the packet about to play is Opus's comfort noise.
func (j *adaptiveJitter) headQuiet() bool {
	at := int(j.next) % len(j.slots)

	return j.filled[at] && dtxPacket(j.slots[at])
}

// skipHead drops the packet at the cursor and takes back the 20 ms of delay it
// was holding. It arrived, so it counts as arrived: what happened to it was this
// buffer's decision and not the network's.
func (j *adaptiveJitter) skipHead() {
	at := int(j.next) % len(j.slots)
	if !j.filled[at] {
		return
	}

	j.slots[at], j.filled[at] = nil, false
	j.held--
	j.next++

	j.expected++
	j.arrived++
	j.rollLoss()
}

// retarget moves the depth toward what the arrival distribution says is needed.
// now is the arrival that triggered it rather than the clock, so the one reading
// Push already took is the one the hold below is measured against.
func (j *adaptiveJitter) retarget(now time.Time) {
	late, ok := j.delay.lateness(j.profile.Percentile)
	if !ok {
		return
	}

	want := j.depthFor(late)

	switch {
	case want > j.depth:
		j.depth = want
		j.shallowSince = time.Time{}

	case want < j.depth:
		switch {
		case j.shallowSince.IsZero():
			j.shallowSince = now
		case now.Sub(j.shallowSince) >= shrinkHold:
			j.depth--
			j.shallowSince = now
		}

	default:
		j.shallowSince = time.Time{}
	}
}

// depthFor is how many packets have to be held to absorb a given lateness, plus
// the floor that stands whatever the network is doing.
func (j *adaptiveJitter) depthFor(late time.Duration) int {
	frames := int(late / (frameMillis * time.Millisecond))

	return min(max(minDepth+frames, minDepth), j.ceiling())
}

// ceiling is the deepest the profile allows, in packets.
func (j *adaptiveJitter) ceiling() int {
	frames := int(j.profile.MaxDelay / (frameMillis * time.Millisecond))

	return min(max(frames, minDepth), maxDepth)
}

func (j *adaptiveJitter) SetProfile(profile JitterProfile) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if profile.Percentile <= 0 || profile.MaxDelay <= 0 {
		return
	}

	j.profile = profile

	// A lowered ceiling applies at once. The depth it clamps to is then reached
	// the ordinary way — through a gap where there is one, and forced where there
	// is not — rather than by dropping the difference here.
	j.depth = min(j.depth, j.ceiling())
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

	j.stalls = 0
}
