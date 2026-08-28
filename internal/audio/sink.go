package audio

import "sync"

// Sink is where a call's remote audio is written: one lane per participant, all
// of them mixed into the same speakers the notification sounds ring on.
//
// Write is called from whatever goroutine decodes for a participant — one each,
// in practice — so a lane is single-producer and needs no lock of its own. The
// map from a user to their lane does need one, but the device callback never
// reads it: the callback walks the fixed lane array and asks each lane one
// atomic whether it is in use.
type Sink struct {
	mix *mixer

	mu    sync.Mutex
	byOne map[string]int     // user ID -> lane index
	gains map[string]float64 // user ID -> gain, unity never stored
}

func newSink(m *mixer) *Sink {
	return &Sink{mix: m, byOne: make(map[string]int), gains: make(map[string]float64)}
}

// Write hands one participant's decoded audio to the speakers: 48 kHz mono,
// signed 16-bit. A write for somebody with no lane is dropped: Open is how a
// lane starts, and a write racing a Remove must not put one back — resurrected,
// it would play a departed participant's tail and hold the slot until the next
// Reset, which is the contract Want's zero-for-no-lane exists to keep.
//
// A lane that is full drops what does not fit. A writer that asks Want first
// never reaches that, which is the arrangement: the speakers say how much they
// are short and get exactly that, so nothing queues up here to go stale.
func (s *Sink) Write(userID string, pcm []int16) {
	if len(pcm) == 0 {
		return
	}

	l := s.find(userID)
	if l == nil {
		return
	}

	l.pcm.PushAll(pcm)
}

// Open starts a participant's lane, and is the only thing that does: the
// speakers only ask for audio for a lane they can see, and a lane nothing but
// Open can create is a lane no late write can resurrect.
func (s *Sink) Open(userID string) { s.lane(userID) }

// echoLane is what the microphone test's own lane is filed under. Not a ULID, so
// it can never be somebody's, and unexported so the identifier never leaves this
// package — StartEcho and StopEcho are the whole of what a caller needs.
const echoLane = "\x00echo"

// StartEcho opens the lane the microphone test plays back through: this account's
// own voice, mixed by the same path a participant's is. That is the point of it
// being a lane at all — what is heard is what a call would send, the call volume
// and the soft clipping included, rather than a second rendering that could
// disagree with the first.
//
// Capture.SetEcho is what fills it. Safe from any goroutine.
func (s *Sink) StartEcho() { s.Open(echoLane) }

// StopEcho closes it, dropping whatever it had buffered. Safe with nothing
// running, and safe to call twice.
func (s *Sink) StopEcho() { s.Remove(echoLane) }

// videoLane is the message video player's lane, reserved the way echoLane is
// and for the same reasons: not a ULID, never a participant's, and the
// identifier never leaves this package. A lane rather than a voice of its own
// because a lane is paced by the speakers (Wake/Want), which is the shape a
// decoder feeding a pipe needs — a fire-and-forget voice is for a buffer
// already rendered.
const videoLane = "\x00video"

// StartVideo opens the video player's lane; the decoder tops it up against
// VideoWant on the speakers' wake, exactly as a participant's is.
func (s *Sink) StartVideo() { s.Open(videoLane) }

// StopVideo closes it, dropping whatever it had buffered — a stopped video
// should not play out a tail.
func (s *Sink) StopVideo() { s.Remove(videoLane) }

// WriteVideo hands the player's decoded audio to the speakers: 48 kHz mono,
// signed 16-bit, like every lane.
func (s *Sink) WriteVideo(pcm []int16) { s.Write(videoLane, pcm) }

// VideoWant is Want for the player's lane.
func (s *Sink) VideoWant() int { return s.Want(videoLane) }

// SetVideoGain scales the player — the card's mute, and nothing else. The
// sink remembers it like any gain, so the controller re-asserts it as each
// playback starts rather than trusting what the last one left.
func (s *Sink) SetVideoGain(gain float64) { s.SetGain(videoLane, gain) }

// Wake fires when the speakers have rendered a period with a lane open. It is
// the whole clock of the playout path: whoever writes a lane waits on this and
// tops every lane back up to what Want reports, so audio is decoded at the rate
// the device consumes it rather than at a timer's rate beside it.
//
// One channel for every lane, buffered by one and sent to without waiting. A
// reader that has not come back yet misses nothing — Want is re-read on the next
// pass and still says how short each lane is.
func (s *Sink) Wake() <-chan struct{} { return s.mix.wake }

// Want is how many samples a participant's lane is short of the depth the
// speakers keep it at. Zero means full — and zero for somebody with no lane,
// which is what stops a writer filling one for a participant who has left. Open
// is how a lane starts, not a write against a Want that assumed one.
//
// Safe from any goroutine, and the only thing a writer needs to know about how
// deep the speakers buffer.
func (s *Sink) Want(userID string) int {
	l := s.find(userID)
	if l == nil {
		return 0
	}

	return max(0, laneTarget-l.pcm.Len())
}

// find is lane without the side effect: a lookup that does not open one.
func (s *Sink) find(userID string) *lane {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, ok := s.byOne[userID]
	if !ok {
		return nil
	}

	return &s.mix.lanes[index]
}

// SetGain scales one participant, 0 to maxGain. The sink remembers it rather
// than the lane: a lane lasts as long as somebody is in the call, and how loud
// they are heard is about the person — set once, it holds across their leaving,
// the call ending and, once the caller writes it down, a restart. Unity is
// forgotten rather than stored, so the map is as long as the list of people
// actually moved.
//
// Somebody with no lane is still left alone — a gain arriving from a menu open
// across their leave must not conjure a lane back — but it is recorded, and the
// lane opened for them next takes it.
func (s *Sink) SetGain(userID string, gain float64) {
	scale := clampGain(float32(gain))

	s.mu.Lock()
	defer s.mu.Unlock()

	if scale == 1 {
		delete(s.gains, userID)
	} else {
		s.gains[userID] = float64(scale)
	}

	if index, ok := s.byOne[userID]; ok {
		s.mix.lanes[index].gain.Store(floatBits(scale))
	}
}

// Gain reports what a participant is scaled by, 1 for anybody never adjusted.
// The sink's own record rather than the lane's, so it answers for somebody who
// has not spoken yet — which is what the menu asks before the first packet.
func (s *Sink) Gain(userID string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return float64(s.gain(userID))
}

// Remove closes a participant's lane and gives the slot back. Whatever they had
// buffered goes with it, which is what leaving a call should sound like.
func (s *Sink) Remove(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, ok := s.byOne[userID]
	if !ok {
		return
	}
	delete(s.byOne, userID)

	s.release(index)
}

// Reset closes every lane. This is hanging up: the call is over and nothing
// buffered for it should still be heard.
//
// The microphone test's lane is not a call's and survives one, or a test running
// while a call ended would go silent with its switch still on. The video
// player's survives for the same reason: a call ending must not mute a video
// mid-play.
func (s *Sink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, index := range s.byOne {
		if id == echoLane || id == videoLane {
			continue
		}

		s.release(index)
		delete(s.byOne, id)
	}
}

// release retires one lane. active goes false first, so the callback has stopped
// looking at the ring before it is drained; a period already inside mixLanes
// plays out what it took, which is a few milliseconds nobody hears the end of.
func (s *Sink) release(index int) {
	l := &s.mix.lanes[index]

	l.active.Store(false)
	l.gain.Store(floatBits(1))
	l.pcm.Discard(l.pcm.Cap())
}

// gain is what one participant is scaled by, unity for anybody never moved.
// Callers hold the lock.
func (s *Sink) gain(userID string) float32 {
	if gain, ok := s.gains[userID]; ok {
		return float32(gain)
	}

	return 1
}

// lane finds a participant's lane, opening one for a user who has none — which
// is Open's job and nobody else's. A call with more participants than there are
// lanes drops the ones past the end rather than growing: maxLanes is well past
// any call this client will be in, and an array the callback can walk without a
// lock is worth more than the last few.
func (s *Sink) lane(userID string) *lane {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index, ok := s.byOne[userID]; ok {
		return &s.mix.lanes[index]
	}

	for i := range s.mix.lanes {
		l := &s.mix.lanes[i]
		if l.active.Load() {
			continue
		}

		// The ring is emptied before the callback is told to read it, or the first
		// thing this participant says is whatever the last one left behind. The
		// gain is this participant's rather than unity, or somebody turned down
		// would come back at full volume the moment they rejoined.
		l.pcm.Discard(l.pcm.Cap())
		l.gain.Store(floatBits(s.gain(userID)))
		l.active.Store(true)

		s.byOne[userID] = i

		return l
	}

	return nil
}
