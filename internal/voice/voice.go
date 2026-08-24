// Package voice is the media half of a call: it dials the voice node with the
// credentials the REST route handed back, publishes one Opus track from a
// microphone, subscribes to everybody else, and writes what they say into a
// sink.
//
// It imports `domain` and nothing else internal. The microphone and the speakers
// arrive as PCMSource and PCMSink, which are declared here *structurally* — so
// this package never imports `internal/audio` and `internal/app` stays the only
// place that can hand one to the other. That is the same trick `ui.Keystroke`
// uses: the layer below names the shape, the controller supplies the thing.
//
// Nothing here touches a widget or the UI thread. A Call reports what happens on
// its own channel, which `app.pumpCall` is the single reader of.
//
// The surface is deliberately the one a standalone module would have. Nothing in
// it mentions Revolt, LiveKit or miniaudio, so it lifts into `revoltgo-voice` as
// package `rvoice` without a line changing here or above.
package voice

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"RGOClient/internal/domain"
)

/* What a call is wired to */

// PCMSource is a microphone: 48 kHz mono signed 16-bit, one frame at a time.
// Read blocks until a frame is ready and is called from the publish goroutine
// and nowhere else.
type PCMSource interface {
	Read(pcm []int16) (int, error)

	// Voiced reports whether the frame Read last answered with held speech. The
	// frame is sent either way — a gated frame is silence the encoder answers with
	// comfort noise — so this only decides what the client says about itself.
	Voiced() bool
}

// PCMSink is the speakers: one lane per participant, mixed by whatever is behind
// it. Write is called from that participant's own decode goroutine, so a sink
// must tolerate one writer per user ID at once.
type PCMSink interface {
	Write(userID string, pcm []int16)
	Remove(userID string)
	Reset()

	// Open starts a participant's lane before there is audio for it, so the sink
	// begins asking for one.
	Open(userID string)

	// Wake fires when the speakers have consumed a period and want more. It is the
	// clock the whole receive path is paced by: decoding on a timer of its own
	// drifts against the device, and a lane then either backs up — latency nothing
	// takes back out — or runs dry.
	Wake() <-chan struct{}

	// Want is how many samples a participant's lane is short. Zero means full, and
	// zero for a participant with no lane — so a lane closed underneath the filler
	// is skipped rather than written back into existence.
	Want(userID string) int
}

/* Events */

// Event is something the call reports. The marker is unexported, so nothing
// outside this package can be one and the switch in `app` is exhaustive — the
// same guarantee `client.Event` gives.
type Event interface{ isVoiceEvent() }

// SpeakingChanged is emitted on a transition, never per frame. A call is drawn
// from these and `Canvas.dirty` is one bool, so an event per audio frame would
// be a full window repaint per audio frame.
type SpeakingChanged struct {
	UserID   string
	Speaking bool
}

// ParticipantChanged is somebody joining or leaving the *call*, which is not the
// same as the channel's voice state — that arrives on the gateway. This is the
// media session, and it is what opens and closes a lane.
type ParticipantChanged struct {
	UserID string
	Joined bool
}

// ConnectionChanged is the call's own health, for the dock's state line.
type ConnectionChanged struct {
	State ConnectionState
}

// CallEnded is the last event on the channel, which is closed after it. Err is
// nil when the call was hung up deliberately.
type CallEnded struct {
	Err error
}

func (SpeakingChanged) isVoiceEvent()    {}
func (ParticipantChanged) isVoiceEvent() {}
func (ConnectionChanged) isVoiceEvent()  {}
func (CallEnded) isVoiceEvent()          {}

// ConnectionState is how the call is doing, in the three states worth drawing
// differently. Anything finer belongs in Stats.
type ConnectionState int

const (
	Connecting ConnectionState = iota
	Connected
	Reconnecting
)

func (s ConnectionState) String() string {
	switch s {
	case Connected:
		return "Connected"
	case Reconnecting:
		return "Reconnecting"
	}

	return "Connecting"
}

/* Options */

// Options is what a call is joined with. The zero value is valid: unmuted,
// undeafened, and the codec's own defaults.
type Options struct {
	// Muted and Deafened are the state to join in, applied before the first frame
	// is sent so nobody hears a syllable the reader did not mean to send.
	Muted    bool
	Deafened bool

	// Bitrate is the encoder's target in bits per second. Zero is defaultBitrate.
	Bitrate int

	// DeepPLC asks the decoders to conceal a lost packet with libopus's neural
	// model rather than by extrapolating the last pitch period. It changes nothing
	// on a stream that is not losing anything, and SetDeepPLC moves it mid-call.
	DeepPLC bool

	// SelfID is this account's own identity, so the client's own speaking can be
	// reported alongside everybody else's. The voice server's active-speaker
	// report is about *remote* participants and arrives half a second late; the
	// gate already knows about this end now, so this end is reported from the gate
	// rather than waiting to be told about ourselves.
	SelfID string
}

/* The call */

// ErrNoCredentials guards a dial to nowhere: a 200 carrying neither field must
// not become a connection attempt against an empty URL.
var ErrNoCredentials = errors.New("no call credentials")

// Call is one joined call. Every method is safe from any goroutine; none of them
// blocks on the network.
type Call struct {
	room *lksdk.Room
	sink PCMSink

	// selfID is this account's identity. The publisher reports its own speaking
	// off the gate, so the server's active-speaker diff must leave it alone or the
	// two take the ring off each other.
	selfID string

	events chan Event

	// publisher is set after the room connects, and lksdk may deliver a subscribe
	// callback before that returns — so the subscriber reads it atomically rather
	// than racing the assignment.
	publisher atomic.Pointer[publisher]

	muted    atomic.Bool
	deafened atomic.Bool

	// deepPLC is read by the filler when it next touches a lane, so a setting
	// changed mid-call reaches every decoder without anything outside that
	// goroutine touching one — libopus decoder state is per stream and is not safe
	// to reconfigure from under a decode.
	deepPLC atomic.Bool

	mu       sync.Mutex
	speaking map[string]bool  // last reported, so only transitions are emitted
	lanes    map[string]*lane // who has an open lane, so a leave closes exactly one

	// evMu orders emit against the closing of events. A send in a select still
	// panics on a closed channel — default arms only a *full* one — and lksdk
	// delivers callbacks on detached goroutines that outlive Disconnect, so a
	// check-then-send racing the close was a crash with no schedule of its own.
	evMu     sync.Mutex
	evClosed bool

	closeOnce sync.Once
	done      chan struct{}
}

// eventDepth is how many events may be waiting on the reader. Events are
// coalesced at the source and a call produces few, so this is slack rather than
// a buffer — and it is *dropped* rather than blocked on, because the alternative
// is the media path stalling behind the UI thread.
const eventDepth = 64

// Join dials the voice node and starts publishing. It blocks for the length of
// the connection handshake, so it belongs on a worker; everything after that is
// the call's own goroutines.
//
// A failed join leaves nothing running and nothing to close.
func Join(creds domain.CallCredentials, src PCMSource, sink PCMSink, opts Options) (*Call, error) {
	if creds.URL == "" || creds.Token == "" {
		return nil, ErrNoCredentials
	}

	c := &Call{
		selfID:   opts.SelfID,
		sink:     sink,
		events:   make(chan Event, eventDepth),
		speaking: make(map[string]bool),
		lanes:    make(map[string]*lane),
		done:     make(chan struct{}),
	}
	// Deafened implies muted: a reader who cannot hear the room has not agreed to
	// keep talking into it.
	c.muted.Store(opts.Muted || opts.Deafened)
	c.deafened.Store(opts.Deafened)
	c.deepPLC.Store(opts.DeepPLC)

	room, err := lksdk.ConnectToRoomWithToken(creds.URL, creds.Token, c.callbacks())
	if err != nil {
		return nil, fmt.Errorf("dial voice node: %w", err)
	}
	c.room = room

	// The joining mute is newPublisher's to apply, before its loop starts: the
	// capture ring already holds audio from before the dial, so a publisher born
	// unmuted and muted a moment later has already sent a syllable.
	publisher, err := newPublisher(room, src, c, opts)
	if err != nil {
		room.Disconnect()
		return nil, err
	}
	c.publisher.Store(publisher)

	// Everything per-person is keyed on the voice server agreeing that a
	// participant's identity *is* the Revolt user ID — lane routing, the speaking
	// ring, per-user volume. Stoat mints the token that way today (the JWT's sub is
	// the user ID), so this is a check rather than a fix: if it ever stops being
	// true the symptom is audio landing under the wrong name, which is maddening to
	// diagnose from the far end and obvious from one line here.
	if got := c.Identity(); opts.SelfID != "" && got != "" && got != opts.SelfID {
		log.Printf("voice: the voice server calls us %q, not %q — per-user audio will be misfiled", got, opts.SelfID)
	}

	// One filler for every participant, started before anybody has subscribed: it
	// is paced by the speakers rather than by a clock of its own, so it costs
	// nothing until there is a lane to fill.
	go c.playLanes()

	c.emit(ConnectionChanged{State: Connected})

	return c, nil
}

// Events is the call's own channel, read by exactly one goroutine. It is closed
// after CallEnded, which is what ends that reader's loop.
func (c *Call) Events() <-chan Event { return c.events }

// Identity is who the voice server thinks this client is. It is the key every
// remote participant's audio is filed under, so it is the one thing worth being
// able to compare against the account's own ID.
func (c *Call) Identity() string {
	if c.room == nil || c.room.LocalParticipant == nil {
		return ""
	}

	return c.room.LocalParticipant.Identity()
}

// SetMuted stops publishing. Unlike the gate, this is announced: the track is
// marked muted so every other client draws it.
func (c *Call) SetMuted(muted bool) {
	if c.deafened.Load() && !muted {
		return // undeafen first; a deafened call is not one to start talking into
	}

	c.muted.Store(muted)
	if p := c.publisher.Load(); p != nil {
		p.setMuted(muted)
	}
}

// Muted reports whether the microphone is held.
func (c *Call) Muted() bool { return c.muted.Load() }

// SetDeafened stops listening, and implies muted. Undeafening does not
// un-mute — a reader who muted before deafening still means to be muted.
func (c *Call) SetDeafened(deafened bool) {
	c.deafened.Store(deafened)

	if deafened {
		c.muted.Store(true)
		if p := c.publisher.Load(); p != nil {
			p.setMuted(true)
		}
		c.sink.Reset() // whatever is buffered must not be heard through the silence

		// Reset is the hang-up primitive and closed every lane with its contents.
		// Reopened empty, the filler keeps feeding each one silence — which is
		// what holds the jitter cursors at playout rate, and the only reason
		// undeafening hears anybody subscribed before the deafen at all.
		c.mu.Lock()
		ids := make([]string, 0, len(c.lanes))
		for id := range c.lanes {
			ids = append(ids, id)
		}
		c.mu.Unlock()

		for _, id := range ids {
			c.sink.Open(id)
		}
	}
}

// Deafened reports whether the speakers are held.
func (c *Call) Deafened() bool { return c.deafened.Load() }

// SetDeepPLC moves neural loss concealment on or off for every participant. It
// takes effect on the next frame each decoder is asked for, so it is safe to
// call at any point in a call and from any goroutine.
func (c *Call) SetDeepPLC(on bool) { c.deepPLC.Store(on) }

// DeepPLC reports whether neural loss concealment is asked for.
func (c *Call) DeepPLC() bool { return c.deepPLC.Load() }

// Close hangs up. There is no leave route — leaving *is* disconnecting, after
// which the gateway announces it — so this is the whole of it. Safe to call
// twice.
func (c *Call) Close() {
	c.closeOnce.Do(func() {
		close(c.done)

		c.teardown()
		c.endEvents(CallEnded{})
	})
}

/* Reporting */

// emit reports one event, dropping it if the reader is behind. A dropped
// speaking transition is a stale ring for a moment; a blocked media goroutine is
// a broken call.
func (c *Call) emit(event Event) {
	c.evMu.Lock()
	defer c.evMu.Unlock()

	if c.evClosed {
		return
	}

	select {
	case c.events <- event:
	default:
		log.Printf("voice: dropped %T, reader is behind", event)
	}
}

// endEvents reports the last event and closes the channel behind it, under the
// same lock emit sends under — the one arrangement in which a late callback
// cannot send into the close.
func (c *Call) endEvents(last CallEnded) {
	c.evMu.Lock()
	defer c.evMu.Unlock()

	select {
	case c.events <- last:
	default:
	}
	c.evClosed = true
	close(c.events)
}

// fail ends the call because something went wrong rather than because anybody
// asked. The reader gets CallEnded carrying why.
func (c *Call) fail(err error) {
	c.closeOnce.Do(func() {
		close(c.done)

		c.teardown()
		c.endEvents(CallEnded{Err: err})
	})
}

// teardown stops the media and lets go of the speakers. Shared by Close and
// fail, which differ only in what they report.
func (c *Call) teardown() {
	if p := c.publisher.Load(); p != nil {
		p.close()
	}
	if c.room != nil {
		c.room.Disconnect()
	}

	c.sink.Reset()
}

// setSpeaking records a transition and reports only the ones that are. lksdk
// already speaks in transitions rather than frames, and this is the second half
// of that contract: a participant reported as speaking twice is one event.
func (c *Call) setSpeaking(userID string, speaking bool) {
	c.mu.Lock()
	if c.speaking[userID] == speaking {
		c.mu.Unlock()
		return
	}
	c.speaking[userID] = speaking
	c.mu.Unlock()

	c.emit(SpeakingChanged{UserID: userID, Speaking: speaking})
}
