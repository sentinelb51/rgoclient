package voice

import (
	"errors"
	"io"
	"log"
	"maps"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"layeh.com/gopus"
)

// callbacks is the whole of what the room tells this package. Every one of them
// runs on lksdk's own goroutine, so each does the least it can and reports
// rather than works.
func (c *Call) callbacks() *lksdk.RoomCallback {
	return &lksdk.RoomCallback{
		OnDisconnected: func() {
			c.fail(errors.New("disconnected from the voice server"))
		},

		OnReconnecting: func() { c.emit(ConnectionChanged{State: Reconnecting}) },
		OnReconnected:  func() { c.emit(ConnectionChanged{State: Connected}) },

		OnParticipantConnected: func(p *lksdk.RemoteParticipant) {
			c.emit(ParticipantChanged{UserID: p.Identity(), Joined: true})
		},

		OnParticipantDisconnected: func(p *lksdk.RemoteParticipant) {
			c.closeLane(p.Identity(), nil)
			c.emit(ParticipantChanged{UserID: p.Identity(), Joined: false})
		},

		// lksdk reports the whole speaking set on every change, so this is a diff
		// against what was last reported rather than a stream of transitions.
		OnActiveSpeakersChanged: func(speakers []lksdk.Participant) {
			c.applySpeakers(speakers)
		},

		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: func(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				// Audio only: lksdk subscribes to whatever is published, and a
				// camera or screen share fed to an Opus decoder would replace the
				// participant's working audio lane with garbage.
				if track.Kind() != webrtc.RTPCodecTypeAudio {
					return
				}

				c.subscribe(track, rp.Identity())
			},

			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				c.closeLane(rp.Identity(), track)
			},
		},
	}
}

// applySpeakers turns the room's speaking set into transitions. Anybody in the
// set who was not speaking starts; anybody who was and is not stops.
func (c *Call) applySpeakers(speakers []lksdk.Participant) {
	now := make(map[string]bool, len(speakers))
	for _, p := range speakers {
		now[p.Identity()] = true
	}

	c.mu.Lock()
	was := make([]string, 0, len(c.speaking))
	for userID, speaking := range c.speaking {
		// This end is the publisher's to report — it decides off the gate, now,
		// where this report is about remote participants and is half a second old.
		if userID == c.selfID {
			continue
		}

		if speaking && !now[userID] {
			was = append(was, userID)
		}
	}
	c.mu.Unlock()

	for _, userID := range was {
		c.setSpeaking(userID, false)
	}
	for userID := range now {
		if userID == c.selfID {
			continue
		}

		c.setSpeaking(userID, true)
	}
}

/* One remote track */

// lane is one participant's half of the receive path: the buffer their packets
// are filed into and the decoder that reads it. Both belong to the filler, which
// is the only thing that touches the decoder — libopus decoder state is per
// stream and not safe to share.
type lane struct {
	buffer  Jitter
	decoder *gopus.Decoder

	// track is which subscription this lane belongs to, so a stale reader's exit
	// cannot close a lane a re-subscribe has since replaced.
	track *webrtc.TrackRemote

	// deepPLC is what this decoder was last told, so the setting is pushed on
	// change rather than on every frame. Touched only by the filler.
	deepPLC bool
}

// subscribe starts the one goroutine a participant needs — a reader that takes
// RTP off the track and files it — and registers the lane the shared filler will
// drain.
//
// Splitting reading from playing is the whole point of a jitter buffer. Decoding
// on the reader would play packets at the rate the network delivers them, which
// is the rate that has jitter in it.
func (c *Call) subscribe(track *webrtc.TrackRemote, userID string) {
	if userID == "" {
		return
	}

	decoder, err := gopus.NewDecoder(sampleRate, channels)
	if err != nil {
		log.Printf("voice: opus decoder for %s: %v", userID, err)
		return
	}

	buffer := newAdaptiveJitter()

	c.mu.Lock()
	c.lanes[userID] = &lane{buffer: buffer, decoder: decoder, track: track}
	c.mu.Unlock()

	// The speakers only ask for audio for a lane they can see, so the lane is
	// opened here rather than by the first frame that arrives — otherwise the
	// first frame is waiting on a wake that is waiting on the first frame.
	c.sink.Open(userID)

	go c.readTrack(track, buffer, userID)
}

// readTrack moves RTP from the track into the jitter buffer and does nothing
// else. It ends when the track does, which is what closes the player after it.
func (c *Call) readTrack(track *webrtc.TrackRemote, buffer Jitter, userID string) {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		packet, _, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				select {
				case <-c.done:
				default:
					log.Printf("voice: read %s: %v", userID, err)
				}
			}

			c.closeLane(userID, track)

			return
		}

		if len(packet.Payload) == 0 {
			continue
		}

		// The payload is pion's buffer and is reused, so the jitter buffer gets a
		// copy. This is the one allocation per packet in the receive path and it
		// buys the buffer the right to hold what it was given.
		payload := make([]byte, len(packet.Payload))
		copy(payload, packet.Payload)

		buffer.Push(packet.SequenceNumber, payload)
	}
}

// silence is what a deafened lane is fed. Written rather than skipped so the
// lane keeps the same geometry it has when audio is flowing — the jitter
// buffer's cursor then advances at exactly playout rate, and undeafening resumes
// at the room's present instead of at whatever was buffered when it stopped.
var silence [frameSize]int16

// lossInterval is how often the encoder is retold what the connection is losing.
// Once a second — FEC is a running average's business, not a per-packet one.
// It doubles as the filler's watchdog: a device that has stopped asking stops
// the wakes, and a pass a second keeps the buffers from standing still.
const lossInterval = time.Second

// playLanes is the whole receive path's clock. It waits for the speakers to say
// they have consumed a period, then tops every participant's lane back up to
// what the sink asks for, decoding only as much as was actually taken.
//
// One goroutine for all of them, not one each. The wake is a single channel, so
// a goroutine per participant would race for it and most would get nothing; and
// the work is small enough — 15 µs a frame — that serialising twenty lanes is a
// third of a millisecond against a 10 ms period.
//
// This is the difference between playout paced by the device and playout paced
// by a timer beside it. A timer drifts against the audio clock, and a lane then
// either backs up — latency nothing ever takes out again — or runs dry.
func (c *Call) playLanes() {
	wake := c.sink.Wake()

	loss := time.NewTicker(lossInterval)
	defer loss.Stop()

	for {
		select {
		case <-c.done:
			return

		case <-loss.C:
			c.reportLoss()
			c.fillLanes()

		case <-wake:
			c.fillLanes()
		}
	}
}

// maxFramesPerPass caps what one lane may be given in a single pass. The target
// is two frames and a period takes half of one, so three is already more than a
// wake can ask for.
const maxFramesPerPass = 3

// fillLanes tops every open lane up to the depth the speakers asked for.
func (c *Call) fillLanes() {
	deafened := c.deafened.Load()

	for userID, l := range c.openLanes() {
		// Bounded as well as conditioned: Want is answered by another goroutine, and
		// a loop that only it can end is one bug away from never ending.
		for range maxFramesPerPass {
			if c.sink.Want(userID) <= 0 {
				break
			}

			payload, next, ok := l.buffer.Pop()
			if !ok {
				break // still filling; silence is what a lane with nothing plays
			}

			// A deafened call decodes nothing, but still moves the cursor.
			if deafened {
				c.sink.Write(userID, silence[:])
				continue
			}

			l.applyDeepPLC(c.deepPLC.Load())

			pcm, err := decodeFrame(l.decoder, payload, next)
			if err != nil {
				log.Printf("voice: decode %s: %v", userID, err)
				continue
			}

			c.sink.Write(userID, pcm)
		}
	}
}

// applyDeepPLC pushes the call's setting onto this lane's decoder when it has
// changed. libopus gates its neural concealer on the decoder's complexity being
// at least ComplexityDeepPLC, and leaves the classic extrapolation in place
// below that.
func (l *lane) applyDeepPLC(on bool) {
	if l.deepPLC == on {
		return
	}

	complexity := gopus.ComplexityOff
	if on {
		complexity = gopus.ComplexityDeepPLC
	}

	if err := l.decoder.SetComplexity(complexity); err != nil {
		// A build without the model, or a system libopus older than 1.5. Say so once
		// per change rather than per frame, and carry on with classic concealment.
		log.Printf("voice: deep PLC unavailable: %v", err)
	}

	l.deepPLC = on
}

// reportLoss tells the encoder what the worst path into this client is losing,
// which is what decides how much redundancy in-band FEC carries. The worst
// rather than the last: one participant on a bad connection is the reason to
// send more, and averaging them hides it.
func (c *Call) reportLoss() {
	p := c.publisher.Load()
	if p == nil {
		return
	}

	// Negative is a window not yet measured, and a room with no lanes measures
	// nothing: either way the encoder keeps what it has — the initial seed at the
	// start, which exists precisely to cover the window before a measurement.
	worst := -1
	for _, l := range c.openLanes() {
		worst = max(worst, l.buffer.Loss())
	}
	if worst < 0 {
		return
	}

	p.setLoss(worst)
}

// openLanes is a snapshot of the lanes to work through, so the map's lock is not
// held across a decode.
func (c *Call) openLanes() map[string]*lane {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.lanes) == 0 {
		return nil
	}

	return maps.Clone(c.lanes)
}

// decodeFrame turns one playout slot into audio, in the three ways a slot can
// come out of the jitter buffer.
//
// The middle case is the whole point of in-band FEC and is easy to get wrong:
// Opus hides a copy of a frame *inside its successor*, so a hole is recovered by
// decoding the **next** packet with the FEC flag — not by decoding the hole, and
// not by decoding the successor normally. That successor is decoded again, in the
// ordinary way, on the following tick; this pass only asks it for what it is
// carrying about the frame before it.
func decodeFrame(decoder *gopus.Decoder, payload, next []byte) ([]int16, error) {
	switch {
	case payload != nil:
		return decoder.Decode(payload, frameSize, false)

	case next != nil:
		// The lost frame, recovered out of the one after it.
		return decoder.Decode(next, frameSize, true)

	default:
		// Nothing to recover from: libopus conceals from the frame before rather
		// than leaving a hole.
		return decoder.Decode(nil, frameSize, false)
	}
}

/* Lanes */

// closeLane retires a participant's audio. Idempotent: a track ending and the
// participant leaving both reach it, and either may be first.
//
// only guards a stale goroutine: after a reconnect resubscribes somebody, the
// old track's reader is still parked in ReadRTP and errors out well after the
// new lane is up — keyed on the user alone, that exit would destroy the
// replacement and leave the participant silent. nil means whatever is there,
// which is what a participant leaving means.
func (c *Call) closeLane(userID string, only *webrtc.TrackRemote) {
	c.mu.Lock()
	l := c.lanes[userID]
	if only != nil && l != nil && l.track != only {
		c.mu.Unlock()
		return
	}
	open := l != nil
	delete(c.lanes, userID)
	delete(c.speaking, userID)
	c.mu.Unlock()

	if !open {
		return
	}

	c.sink.Remove(userID)
}
