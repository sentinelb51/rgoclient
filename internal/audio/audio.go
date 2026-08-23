// Package audio owns both directions of the machine's sound: the client's short
// sounds — the ping when somebody names you, the clicks under the composer — and
// the call's microphone and speakers.
//
// It imports nothing internal. Volumes, device identifiers and file paths arrive
// as arguments, so an Engine can be built in a test with no settings file
// anywhere.
//
// Nothing here blocks the caller and nothing here touches a widget. Play hands a
// request to the engine's own goroutine, which is the only producer the device
// callback reads from; a full queue is dropped rather than waited on, a click
// that arrives late being worse than one that never sounds at all.
//
// # The rule the callback lives under
//
// mixer.render runs on the backend's thread. It must not allocate, lock, log, or
// call anything that might: a Go allocation there can trip a GC assist and a
// mutex there can be held by a goroutine the scheduler has parked, and either is
// a dropout. Everything crossing into it does so through a ring or an atomic.
package audio

import (
	"log"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

// The device format every sound is converted to at load, and the format the
// speakers are opened in. 48 kHz is what Windows mixes at in shared mode and
// what Opus codes at, so matching it keeps a resampler out of both paths.
const (
	sampleRate    = 48000
	channelCount  = 2
	bytesPerFrame = channelCount * 2 // signed 16-bit little endian
)

// The sounds the client knows about. A key is the name a settings override is
// filed under, so these strings are part of the settings file: renaming one
// silently drops whatever file the user pointed it at.
const (
	Mention  = "mention"
	Direct   = "direct"
	Message  = "message"
	Ambient  = "ambient"
	Send     = "send"
	Friend   = "friend"
	Reaction = "reaction"
	Error    = "error"
	Offline  = "offline"
	Online   = "online"

	KeyPress     = "key"
	KeySpace     = "space"
	KeyBackspace = "backspace"
	KeyEnter     = "enter"
)

// Keys is every sound in the order the settings page lists them. The position of
// a key here is also the group the mixer bounds its overlap by, so this is one
// list rather than two.
var Keys = []string{
	Mention, Direct, Message, Ambient, Send, Friend, Reaction, Error, Offline, Online,
	KeyPress, KeySpace, KeyBackspace, KeyEnter,
}

// groups is Keys inverted, built once so Play looks a key up rather than
// scanning.
var groups = func() map[string]uint16 {
	out := make(map[string]uint16, len(Keys))
	for i, key := range Keys {
		out[key] = uint16(i)
	}

	return out
}()

// IsTyping reports whether a key is one the composer fires per keystroke. Those
// are the ones that have to overlap, that repeat often enough for an identical
// render to read as a machine gun, and that are worth their own volume.
func IsTyping(key string) bool {
	switch key {
	case KeyPress, KeySpace, KeyBackspace, KeyEnter:
		return true
	}

	return false
}

/* Sounds */

// Sound is a clip in the device's own format, ready to hand to the mixer.
//
// It holds *takes* rather than one buffer because a typing click repeats faster
// than anything else here: four renders of the same click, rotated, is what
// stops a run of them sounding like one sample looped. A decoded file has a
// single take and varies by gain alone.
type Sound struct {
	takes [][]byte
}

// take picks one of the renders at random. A single-take sound answers with it
// every time.
func (s *Sound) take() []byte {
	if len(s.takes) == 1 {
		return s.takes[0]
	}

	return s.takes[rand.IntN(len(s.takes))]
}

/* The engine */

// minRepeat is the shortest gap between two plays of one sound. Held keys repeat
// faster than a click is long, and past this the clicks stop being separable
// anyway — so this is a floor on the work, not a musical decision.
const minRepeat = 15 * time.Millisecond

// queueDepth is how many requests may be waiting on the engine goroutine. Deep
// enough that a burst of keystrokes survives a device call, shallow enough that
// a stalled device drops the backlog instead of playing it a second later.
const queueDepth = 32

// Engine owns the playback device, the loaded sounds and the call's output
// lanes. Build one per process: it is the client's speakers.
type Engine struct {
	requests chan request
	closed   sync.Once

	// mix is shared with the device callback and is reached only through its own
	// rings and atomics. sink is the call's half of it.
	mix  *mixer
	sink *Sink

	// generation names the device that is open. A Stop callback carries the one
	// it was built for, so the stop that closeDevice itself causes is told from
	// the one the backend causes and does not queue a reopen.
	generation atomic.Uint64

	/* The engine goroutine's own, touched nowhere else */

	device   *malgo.Device
	outputID string
	silent   bool // the device refused to open; every later play is dropped

	sounds map[string]*Sound
	last   map[string]time.Time
}

// What a request asks for. A zero kind is a play, which is the one that arrives
// often enough to be worth not writing down.
type requestKind uint8

const (
	requestPlay requestKind = iota
	requestInstall
	requestOutput
	requestReopen
	requestOpen
)

type request struct {
	kind requestKind

	key        string
	sound      *Sound
	volume     float64
	device     string
	generation uint64
}

// NewEngine returns an engine that has not touched the audio device. The device
// is opened by the first sound actually played, so a client whose sounds are all
// off and who joins no call never grabs one.
func NewEngine() *Engine {
	m := newMixer()

	e := &Engine{
		requests: make(chan request, queueDepth),
		mix:      m,
		sink:     newSink(m),
		sounds:   make(map[string]*Sound),
		last:     make(map[string]time.Time),
	}

	go e.run()

	return e
}

// Sink is the call's end of the speakers: a lane per remote participant, mixed
// into the same device the notification sounds ring on. It exists from the
// engine's construction, so a call can be wired to it before anything has been
// played.
func (e *Engine) Sink() *Sink { return e.sink }

// StartOutput opens the speakers now rather than on the first sound. A call
// needs it: remote audio reaches the lanes and only the device callback mixes
// them, so a call joined before anything has rung would be inaudible — and with
// the callback also being what asks for the next frame, nothing would even be
// decoded.
func (e *Engine) StartOutput() { e.send(request{kind: requestOpen}, true) }

// Set installs a sound under a key, replacing whatever was there. Decoding
// happens on the caller's goroutine — a long file must not stall a click already
// queued — so call it from a worker rather than the UI thread.
//
// An empty path is the built-in, which is synthesised rather than read, so it
// cannot fail and needs no file to exist.
func (e *Engine) Set(key, path string) error {
	if path == "" {
		e.send(request{kind: requestInstall, key: key, sound: builtin(key)}, true)
		return nil
	}

	sound, err := Decode(path)
	if err != nil {
		return err
	}

	e.send(request{kind: requestInstall, key: key, sound: sound}, true)

	return nil
}

// Play sounds a key at volume — 0 to 1, already carrying whatever the settings
// scale it by. An unknown key, a volume of zero and a full queue are all silent.
// Safe from any goroutine, including the UI thread.
func (e *Engine) Play(key string, volume float64) {
	if volume <= 0 {
		return
	}

	e.send(request{key: key, volume: min(volume, 1)}, false)
}

// UseOutput moves playback to a device, an empty id meaning the system default.
// The call's lanes and any ringing sound move with it: there is one pair of
// speakers, and picking them once is the whole point of the engine owning both.
func (e *Engine) UseOutput(id string) { e.send(request{kind: requestOutput, device: id}, true) }

// SetCallVolume scales every remote participant, 0 to 1. Notification sounds are
// deliberately not scaled by it — a reader who turned the call down did not ask
// for a quieter mention ping.
func (e *Engine) SetCallVolume(volume float64) { e.mix.setMaster(float32(volume)) }

// send hands a request to the engine goroutine. wait is for the ones that must
// not be lost — installing a sound, changing device — where dropping would leave
// the client silently on the previous state with nothing saying so.
func (e *Engine) send(req request, wait bool) {
	defer func() {
		// A send on a closed engine is a shutdown race, not a bug worth a panic:
		// the sound was going to stop anyway.
		_ = recover()
	}()

	if wait {
		e.requests <- req
		return
	}

	select {
	case e.requests <- req:
	default: // the engine is behind; a click owed to a keystroke already gone is worth nothing
	}
}

// Close stops the engine and releases the device. The miniaudio context stays —
// it is the process's one connection to the backend, and tearing it down while a
// callback is in flight is a use-after-free rather than a tidy-up.
func (e *Engine) Close() {
	e.closed.Do(func() { close(e.requests) })
}

// run is the engine goroutine: the only thing that opens a device or writes to
// the mixer's command ring, so nothing below needs a lock and the ring stays
// single-producer.
func (e *Engine) run() {
	for req := range e.requests {
		switch req.kind {
		case requestInstall:
			e.sounds[req.key] = req.sound
		case requestOpen:
			e.open()
		case requestOutput:
			e.useOutput(req.device)
		case requestReopen:
			if req.generation == e.generation.Load() {
				e.reopen()
			}
		default:
			e.play(req.key, req.volume)
		}
	}

	e.closeDevice()
}

// play sounds one key.
func (e *Engine) play(key string, volume float64) {
	sound := e.sounds[key]
	if sound == nil || e.silent {
		return
	}

	now := time.Now()
	if now.Sub(e.last[key]) < minRepeat {
		return
	}

	if !e.open() {
		return
	}
	e.last[key] = now

	// A typing click plays hundreds of times a minute, and identical gain is most
	// of what makes a run of them read as one repeating sample.
	if IsTyping(key) {
		volume *= 0.88 + rand.Float64()*0.12
	}

	e.mix.play(playCmd{
		data:  sound.take(),
		gain:  float32(volume),
		group: groups[key],
		limit: uint8(voiceCount(key)),
	})
}

// voiceCount is how many copies of one sound may overlap. A typing click has to
// survive somebody typing faster than the click is long; nothing else here
// overlaps itself in practice.
func voiceCount(key string) int {
	if IsTyping(key) {
		return 6
	}

	return 2
}

/* The device */

// open starts playback on first use, and gives up for good if the backend
// refuses: there is no state a retry could reach that the first attempt did not,
// and a client that tried again per keystroke would log a line per keystroke.
//
// A device that is taken away later is a different case — reopen handles that,
// and clears silent, because the machine has changed since the refusal.
func (e *Engine) open() bool {
	if e.device != nil {
		return true
	}
	if e.silent {
		return false
	}

	if err := e.startDevice(e.outputID); err != nil {
		log.Printf("open speakers: %v", err)
		e.silent = true

		return false
	}

	return true
}

// useOutput moves playback to another device, keeping the previous one if the
// new one will not open — a picked device that has since been unplugged should
// leave the client audible rather than silent.
func (e *Engine) useOutput(id string) {
	if id == e.outputID && e.device != nil {
		return
	}

	previous := e.outputID
	e.outputID = id
	e.silent = false

	if e.device == nil {
		return // nothing has been played yet; the next play opens the new device
	}

	e.closeDevice()

	if err := e.startDevice(id); err == nil {
		return
	} else {
		log.Printf("open speakers %q: %v", id, err)
	}

	e.outputID = previous
	if err := e.startDevice(previous); err != nil {
		log.Printf("reopen previous speakers: %v", err)
		e.silent = true
	}
}

// reopen answers the device having been taken away underneath us — the endpoint
// was unplugged, or the session was pre-empted. It falls back to the system
// default rather than ending in silence, which is what a reader who just
// unplugged a headset expects.
func (e *Engine) reopen() {
	e.closeDevice()

	if err := e.startDevice(e.outputID); err == nil {
		return
	}

	if e.outputID == "" {
		e.silent = true
		return
	}

	log.Printf("speakers %q went away; falling back to the default", e.outputID)
	e.outputID = ""

	if err := e.startDevice(""); err != nil {
		log.Printf("open default speakers: %v", err)
		e.silent = true
	}
}

func (e *Engine) startDevice(id string) error {
	ctx, err := context()
	if err != nil {
		return err
	}

	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.SampleRate = sampleRate
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = channelCount
	config.Playback.DeviceID = deviceIDPointer(malgo.Playback, id)
	config.PeriodSizeInFrames = sampleRate * 10 / 1000
	config.PerformanceProfile = malgo.LowLatency

	generation := e.generation.Add(1)

	device, err := malgo.InitDevice(ctx.Context, config, malgo.DeviceCallbacks{
		Data: func(out, _ []byte, _ uint32) { e.mix.render(out) },
		Stop: func() { e.onStop(generation) },
	})
	if err != nil {
		return err
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		return err
	}
	e.device = device

	return nil
}

// onStop fires on the backend's thread when a device stops. It only asks the
// engine goroutine to look: reopening from here would be reentering miniaudio
// from inside its own callback, and the generation is what tells a device the
// backend took away from one the engine is closing on purpose.
func (e *Engine) onStop(generation uint64) {
	e.send(request{kind: requestReopen, generation: generation}, false)
}

func (e *Engine) closeDevice() {
	if e.device == nil {
		return
	}

	// Retiring the generation first is what makes the Stop this causes a no-op.
	e.generation.Add(1)

	// Stop before Uninit so no callback is in flight when the state it reads goes
	// away. Its failure is not actionable — the device is being dropped either way.
	_ = e.device.Stop()
	e.device.Uninit()
	e.device = nil
}
