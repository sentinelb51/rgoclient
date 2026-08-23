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
	byOne map[string]int // user ID -> lane index
}

func newSink(m *mixer) *Sink {
	return &Sink{mix: m, byOne: make(map[string]int)}
}

// Write hands one participant's decoded audio to the speakers: 48 kHz mono,
// signed 16-bit. The first write for a user opens their lane.
//
// A lane that is full drops what does not fit. A writer that asks Want first
// never reaches that, which is the arrangement: the speakers say how much they
// are short and get exactly that, so nothing queues up here to go stale.
func (s *Sink) Write(userID string, pcm []int16) {
	if len(pcm) == 0 {
		return
	}

	l := s.lane(userID)
	if l == nil {
		return
	}

	l.pcm.PushAll(pcm)
}

// Open starts a participant's lane before there is anything to put in it. The
// speakers only ask for audio for a lane they can see, so this is what gets the
// first frame asked for; Write opens one too, for a caller that has audio in
// hand already.
func (s *Sink) Open(userID string) { s.lane(userID) }

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

// SetGain scales one participant, 0 to 2. It is about this call and is not
// persisted: a voice too quiet today is a room, not a preference.
func (s *Sink) SetGain(userID string, gain float64) {
	if l := s.lane(userID); l != nil {
		l.gain.Store(floatBits(clampGain(float32(gain))))
	}
}

// Gain reports what a participant is scaled by, 1 for anybody never adjusted.
func (s *Sink) Gain(userID string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, ok := s.byOne[userID]
	if !ok {
		return 1
	}

	return float64(bitsFloat(s.mix.lanes[index].gain.Load()))
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
func (s *Sink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, index := range s.byOne {
		s.release(index)
	}
	clear(s.byOne)
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

// lane finds a participant's lane, opening one on the first write. A call with
// more participants than there are lanes drops the ones past the end rather than
// growing: maxLanes is well past any call this client will be in, and an array
// the callback can walk without a lock is worth more than the last few.
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
		// thing this participant says is whatever the last one left behind.
		l.pcm.Discard(l.pcm.Cap())
		l.gain.Store(floatBits(1))
		l.active.Store(true)

		s.byOne[userID] = i

		return l
	}

	return nil
}
