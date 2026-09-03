package voice

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/sentinelb51/gopus"
)

// disconnectError names why the room let go, since the notice will say it and
// three of the reasons are different things to do about: the server removing
// this participant is what the voice ingress does to a publisher whose declared
// video is over its tier's limits (it never refuses the track), and this
// account joining from a second client is what takes the seat from this one.
func disconnectError(reason lksdk.DisconnectionReason) error {
	switch reason {
	case lksdk.ParticipantRemoved:
		return errors.New("removed from the call by the server")
	case lksdk.DuplicateIdentity:
		return errors.New("this account joined the call from another client")
	case lksdk.RoomClosed:
		return errors.New("the call was closed")
	}

	return fmt.Errorf("disconnected from the voice server: %s", reason)
}

// callbacks is the whole of what the room tells this package. Every one of them
// runs on lksdk's own goroutine, so each does the least it can and reports
// rather than works.
func (c *Call) callbacks() *lksdk.RoomCallback {
	return &lksdk.RoomCallback{
		OnDisconnectedWithReason: func(reason lksdk.DisconnectionReason) {
			c.fail(disconnectError(reason))
		},

		OnReconnecting: func() { c.emit(ConnectionChanged{State: Reconnecting}) },
		OnReconnected:  func() { c.emit(ConnectionChanged{State: Connected}) },

		OnParticipantConnected: func(p *lksdk.RemoteParticipant) {
			c.emit(ParticipantChanged{UserID: p.Identity(), Joined: true})
		},

		OnParticipantDisconnected: func(p *lksdk.RemoteParticipant) {
			c.closeLane(p.Identity(), nil)
			c.closeLane(ShareLane(p.Identity()), nil)
			c.forgetShare(p.Identity())
			c.forgetHeld(p.Identity())
			c.emit(ParticipantChanged{UserID: p.Identity(), Joined: false})
		},

		// lksdk reports the whole speaking set on every change, so this is a diff
		// against what was last reported rather than a stream of transitions.
		OnActiveSpeakersChanged: func(speakers []lksdk.Participant) {
			c.applySpeakers(speakers)
		},

		ParticipantCallback: lksdk.ParticipantCallback{
			// A track arrives with its mute already set, so this is where somebody
			// who was holding their microphone before we joined is first seen —
			// the transition callbacks below only ever report a *change*. The held
			// mark is the microphone's alone: a share's audio track carries a mute
			// of its own that says nothing about the person.
			OnTrackPublished: func(pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				c.registerPublication(pub, rp)

				if pub.Kind() == lksdk.TrackKindAudio && !shareAudioSource(pub) {
					c.setHeld(rp.Identity(), pub.IsMuted())
				}
			},

			OnTrackUnpublished: func(pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				c.unregisterPublication(pub, rp.Identity())
			},

			OnTrackMuted: func(pub lksdk.TrackPublication, p lksdk.Participant) {
				if pub.Kind() == lksdk.TrackKindAudio && !shareAudioSource(pub) {
					c.setHeld(p.Identity(), true)
				}
			},

			OnTrackUnmuted: func(pub lksdk.TrackPublication, p lksdk.Participant) {
				if pub.Kind() == lksdk.TrackKindAudio && !shareAudioSource(pub) {
					c.setHeld(p.Identity(), false)
				}
			},

			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				// Video is read only by a watch that asked for it: everything
				// else was unsubscribed at publish, and whatever slips through
				// before that lands is dropped unread.
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					c.startShareReader(pub, track, rp.Identity())
					return
				}

				c.subscribe(track, audioLane(pub, rp.Identity()))
			},

			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					return // a watch's reader notices its own track ending
				}

				c.closeLane(audioLane(pub, rp.Identity()), track)
			},
		},
	}
}

// shareAudioSource is whether a publication is a screenshare's sound rather
// than somebody's microphone.
func shareAudioSource(pub lksdk.TrackPublication) bool {
	return pub.Source() == livekit.TrackSource_SCREEN_SHARE_AUDIO
}

// audioLane is which sink lane an audio track plays through: the person's
// own, or their share's, kept apart so the two volumes are separate dials —
// and so a share's sound can never replace the working microphone lane the
// same identity already holds.
func audioLane(pub lksdk.TrackPublication, userID string) string {
	if shareAudioSource(pub) {
		return ShareLane(userID)
	}

	return userID
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

	// into is the decoder's caller-buffer half where the binding offers one, and
	// pcm the buffer it fills — one per lane, the filler being its only user.
	// nil falls back to Decode's per-frame allocation.
	into opusDecodeIn
	pcm  []int16

	// track is which subscription this lane belongs to, so a stale reader's exit
	// cannot close a lane a re-subscribe has since replaced.
	track *webrtc.TrackRemote

	// deepPLC is what this decoder was last told, so the setting is pushed on
	// change rather than on every frame. Touched only by the filler.
	deepPLC bool

	// jitter is what the buffer was last told, for the same reason and at the same
	// cost: SetProfile takes the buffer's own lock, which the reader goroutine is
	// holding fifty times a second. Touched only by the filler.
	jitter JitterProfile

	// quiet counts DTX packets in a row, so the lane can stop paying to decode
	// them. Touched only by the filler.
	quiet int

	// scratch is where a time-scaled frame is built, sized for the longest one
	// expand can produce so it is allocated once rather than per correction.
	// allow is the time-scaling ration this lane has earned and not yet spent.
	// compressed and expanded count what has been done to it, which is only ever
	// read by a test. All four are the filler's.
	scratch              []int16
	allow                float64
	compressed, expanded int
}

// dtxPacket is whether a payload is what Opus emits in place of a frame while
// discontinuous transmission has it saying nothing: the table-of-contents byte
// and no frame behind it, one or two bytes. The decoder takes one as a lost
// frame and conceals.
func dtxPacket(payload []byte) bool { return len(payload) > 0 && len(payload) <= 2 }

// quietAfter is how many DTX packets in a row a lane decodes before it stops
// asking the decoder at all. The first few are worth it — concealment fades the
// last real frame out rather than cutting it — and after that the decoder is
// making comfort noise from nothing, at Deep PLC's price where that is on: 97 µs
// a frame against 9, fifty times a second, for every participant who is not
// talking. Silence written straight to the lane is what their gate sent, at no
// cost, and with nothing under it for a boosted lane to lift.
const quietAfter = 5

// opusDecodeIn is what a binding has to offer for the receive path to decode
// without allocating: 1920 B per frame per participant, fifty times a second,
// is otherwise the path's dominant garbage. Upstream layeh.com/gopus does not
// have it; sentinelb51/gopus does, and the assertion lights it up without this
// package knowing which is linked — the same seam opusTuning is.
type opusDecodeIn interface {
	DecodeIn(data []byte, frameSize int, pcm []int16, fec bool) (int, error)
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

	profile := c.jitterProfile()
	buffer := newAdaptiveJitter(profile)

	l := &lane{
		buffer: buffer, decoder: decoder, track: track, jitter: profile,
		scratch: make([]int16, 0, frameSize+maxLag),
	}
	if into, ok := any(decoder).(opusDecodeIn); ok {
		l.into, l.pcm = into, make([]int16, frameSize)
	}

	c.mu.Lock()
	c.lanes[userID] = l
	c.lanesGen++
	c.mu.Unlock()

	// The speakers only ask for audio for a lane they can see, so the lane is
	// opened here rather than by the first frame that arrives — otherwise the
	// first frame is waiting on a wake that is waiting on the first frame.
	c.sink.Open(userID)

	go c.readTrack(track, buffer, userID)
}

// readTimeout is how long one read may block before the loop looks at c.done
// again. A participant who mutes through the SDK stops sending entirely, so
// without a deadline a hang-up leaves this goroutine parked in ReadRTP holding
// a jitter buffer and an Opus decoder for the rest of the process. Nothing a
// reader tunes: it is the resolution of the exit check, not a media setting.
const readTimeout = time.Second

// readTrack moves RTP from the track into the jitter buffer and does nothing
// else. It ends when the track does, which is what closes the player after it.
func (c *Call) readTrack(track *webrtc.TrackRemote, buffer Jitter, userID string) {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		// Re-armed every pass: a deadline set once expires once and then fails every
		// read after it.
		_ = track.SetReadDeadline(time.Now().Add(readTimeout))

		packet, _, err := track.ReadRTP()
		if err != nil {
			// An expiry is silence, not failure — DTX and a muted publisher both
			// make long gaps ordinary — so it goes back round rather than closing
			// the lane, which would take the participant off the speakers for good
			// with nothing reporting it.
			//
			// Matched through net.Error rather than against os.ErrDeadlineExceeded:
			// a lapsed deadline comes back from pion's own packetio buffer, which
			// answers with an error of its own that is not that sentinel and says
			// what it is only through Timeout.
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}

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

		buffer.Push(packet.SequenceNumber, packet.Timestamp, payload)
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
	profile := c.jitterProfile()

	for _, p := range c.laneSnapshot() {
		userID, l := p.userID, p.lane

		l.applyJitter(profile)

		// One reading per wake rather than one per frame. The loop below pops ahead
		// of the speakers, so occupancy dips inside a pass without the buffer being
		// short of anything — that audio is in the lane rather than missing, and
		// stretching against the dip would be correcting for the filler's own stride.
		drift := l.buffer.Drift()

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

			if dtxPacket(payload) {
				l.quiet++
				if l.quiet > quietAfter {
					c.sink.Write(userID, silence[:])
					continue
				}
			} else {
				l.quiet = 0
			}

			l.applyDeepPLC(c.deepPLC.Load())

			pcm, err := l.decodeFrame(payload, next)
			if err != nil {
				log.Printf("voice: decode %s: %v", userID, err)
				continue
			}

			c.sink.Write(userID, l.retime(pcm, drift))
		}
	}
}

// applyJitter pushes the call's profile onto this lane's buffer when it has
// changed. The buffer holds what it can act on; a profile it cannot is ignored
// there rather than filtered here.
func (l *lane) applyJitter(profile JitterProfile) {
	if l.jitter == profile {
		return
	}

	l.jitter = profile
	l.buffer.SetProfile(profile)
}

// applyDeepPLC pushes the call's setting onto this lane's decoder when it has
// changed. libopus stacks its neural extensions on one dial: the concealer at
// ComplexityDeepPLC, and LACE — a postfilter over decoded SILK — one above it.
// Below either, the classic extrapolation stays and nothing is filtered.
//
// LACE rather than NoLACE, which is one higher again. The concealer is paid only
// on a frame that was lost; a postfilter runs on every frame that arrives, so it
// is the one level whose cost is charged to every talker, and the two differ by
// more than double: 0.20 % of a core a lane against 0.43 % (48 kHz mono, 20 ms
// frames, 13700HX). NoLACE is left on the table until somebody can hear the
// difference it makes.
//
// The bandwidth extension is the one that does not follow from the dial, so it
// is asked for beside it. At the bitrate this client publishes Opus stays in
// hybrid mode, where BWE does nothing at all — it wants a SILK-only packet or a
// concealed one — so what it is really here for is the second of those, which is
// the same thing the switch is.
func (l *lane) applyDeepPLC(on bool) {
	if l.deepPLC == on {
		return
	}

	complexity := gopus.ComplexityOff
	if on {
		complexity = gopus.ComplexityLACE
	}

	if err := l.decoder.SetComplexity(complexity); err != nil {
		// A build without the models, or a system libopus older than 1.5. Say so once
		// per change rather than per frame, and carry on with classic concealment.
		log.Printf("voice: deep PLC unavailable: %v", err)
	}
	if err := l.decoder.SetOSCEBWE(on); err != nil && on {
		// 1.6 and OSCE, so older than the above; not worth a second sentence.
		log.Printf("voice: bandwidth extension unavailable: %v", err)
	}

	l.deepPLC = on
}

// reportLoss tells the encoder what the *sending* half of the connection is
// losing, which is what decides how much redundancy in-band FEC carries.
//
// The node's own receiver reports are the only place that number exists, and
// they are the right one: FEC protects what goes out, so what this client is
// missing on the way in — a different path, and often the opposite verdict —
// says nothing about how much of it to buy.
func (c *Call) reportLoss() {
	p := c.publisher.Load()
	if p == nil {
		return
	}

	// Negative is a node that has reported nothing yet: the encoder keeps what it
	// has — the initial seed, which exists precisely to cover the window before a
	// measurement.
	loss := p.uplink.Loss()
	if loss < 0 {
		return
	}

	p.setLoss(loss)
}

// worstLoss is what the worst path into this client is losing, negative where
// nothing has completed a window. The worst rather than the mean: one
// participant on a bad connection is the reason to buy more redundancy, and
// averaging them hides it — it is also the one a reader actually hears, which is
// why the state bar is graded on the same number.
//
// A walk of its own rather than the filler's snapshot, which only playLanes may
// take.
func (c *Call) worstLoss() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	worst := -1
	for _, l := range c.lanes {
		worst = max(worst, l.buffer.Loss())
	}

	return worst
}

// lanePair is one entry of the filler's snapshot: who a lane belongs to, and the
// lane. A slice rather than a map because the filler only ever walks it.
type lanePair struct {
	userID string
	lane   *lane
}

// laneSnapshot is the lanes to work through, so the map's lock is not held
// across a decode. Rebuilt only when c.lanes has actually changed — this runs at
// the device's period, and a fresh map every 10 ms was garbage for an answer
// that is the same one nearly every time.
//
// The buffer is the filler's own. Only playLanes and what it calls may reach
// this: another goroutine walking c.lanes builds its own slice.
func (c *Call) laneSnapshot() []lanePair {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.snapGen == c.lanesGen {
		return c.snap
	}

	c.snap = c.snap[:0]
	for userID, l := range c.lanes {
		c.snap = append(c.snap, lanePair{userID: userID, lane: l})
	}
	c.snapGen = c.lanesGen

	return c.snap
}

// decodeFrame turns one playout slot into audio, in the three ways a slot can
// come out of the jitter buffer: the packet itself, a hole recovered out of its
// successor, or a hole concealed from nothing.
//
// The middle case is the whole point of in-band FEC and is easy to get wrong:
// Opus hides a copy of a frame *inside its successor*, so a hole is recovered by
// decoding the **next** packet with the FEC flag — not by decoding the hole, and
// not by decoding the successor normally. That successor is decoded again, in the
// ordinary way, on the following tick; this pass only asks it for what it is
// carrying about the frame before it.
//
// The answer aliases the lane's own buffer when the binding decodes in place,
// and is good only until the next decode — the sink copies it in the same
// breath, which is the whole of why that is enough.
func (l *lane) decodeFrame(payload, next []byte) ([]int16, error) {
	data, fec := payload, false
	if payload == nil && next != nil {
		// The lost frame, recovered out of the one after it. Both nil stays a
		// nil decode: libopus conceals from the frame before rather than
		// leaving a hole.
		data, fec = next, true
	}

	if l.into == nil {
		return l.decoder.Decode(data, frameSize, fec)
	}

	n, err := l.into.DecodeIn(data, frameSize, l.pcm, fec)
	if err != nil {
		return nil, err
	}

	return l.pcm[:n], nil
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
	c.lanesGen++
	c.mu.Unlock()

	if !open {
		return
	}

	c.sink.Remove(userID)
}
