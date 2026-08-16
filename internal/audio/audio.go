// Package audio plays the client's short sounds: the ping when somebody names
// you, and the clicks under the composer. It imports nothing internal — volumes
// and file paths arrive as arguments — so an Engine can be built in a test with
// no settings file anywhere.
//
// Nothing here blocks the caller and nothing here touches a widget. Play hands a
// request to the engine's own goroutine, which owns every oto call; a full queue
// is dropped rather than waited on, a click that arrives late being worse than
// one that never sounds at all.
package audio

import (
	"io"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// The device format every sound is converted to at load. 48 kHz is what Windows
// mixes at in shared mode, so matching it keeps oto's own conversion out of the
// path.
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

// Keys is every sound in the order the settings page lists them.
var Keys = []string{
	Mention, Direct, Message, Ambient, Send, Friend, Reaction, Error, Offline, Online,
	KeyPress, KeySpace, KeyBackspace, KeyEnter,
}

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

// Sound is a clip in the device's own format, ready to hand to a player.
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

// queueDepth is how many plays may be waiting on the engine goroutine. Deep
// enough that a burst of keystrokes survives a device call, shallow enough that
// a stalled device drops the backlog instead of playing it a second later.
const queueDepth = 32

// Engine owns the device and the loaded sounds. Build one per process — oto
// allows a single context, and a second one is an error rather than a second
// device.
type Engine struct {
	requests chan request
	closed   sync.Once

	/* The engine goroutine's own, touched nowhere else */

	context *oto.Context
	silent  bool // the device failed to open; every later request is dropped

	sounds map[string]*Sound
	voices map[string][]*voice
	last   map[string]time.Time
}

// request is either an install (sound set) or a play (it isn't).
type request struct {
	key    string
	sound  *Sound
	volume float64
}

// voice is one player and the buffer it reads. A sound overlapping itself needs
// a player each — a single one restarted mid-play cuts the earlier click off.
type voice struct {
	player *oto.Player
	buffer *takeReader
}

// NewEngine returns an engine that has not touched the audio device. The device
// is opened by the first sound actually played, so a client whose sounds are all
// off never grabs one.
func NewEngine() *Engine {
	e := &Engine{
		requests: make(chan request, queueDepth),
		sounds:   make(map[string]*Sound),
		voices:   make(map[string][]*voice),
		last:     make(map[string]time.Time),
	}

	go e.run()

	return e
}

// Set installs a sound under a key, replacing whatever was there. Decoding
// happens on the caller's goroutine — a long file must not stall a click already
// queued — so call it from a worker rather than the UI thread.
//
// An empty path is the built-in, which is synthesised rather than read, so it
// cannot fail and needs no file to exist.
func (e *Engine) Set(key, path string) error {
	if path == "" {
		e.install(key, builtin(key))
		return nil
	}

	sound, err := Decode(path)
	if err != nil {
		return err
	}

	e.install(key, sound)

	return nil
}

// install queues a sound. Unlike a play it is never dropped: a queue full of
// keystrokes would otherwise leave the client on the previous sound with nothing
// saying so.
func (e *Engine) install(key string, sound *Sound) {
	e.requests <- request{key: key, sound: sound}
}

// Play sounds a key at volume — 0 to 1, already carrying whatever the settings
// scale it by. An unknown key, a volume of zero and a full queue are all silent.
// Safe from any goroutine, including the UI thread.
func (e *Engine) Play(key string, volume float64) {
	if volume <= 0 {
		return
	}

	select {
	case e.requests <- request{key: key, volume: min(volume, 1)}:
	default: // the device is behind; a click owed to a keystroke already gone is worth nothing
	}
}

// Close stops the engine and releases its players. The oto context stays — it is
// the process's one device — so an engine closed in a test does not take the
// next one's audio with it.
func (e *Engine) Close() {
	e.closed.Do(func() { close(e.requests) })
}

// run is the engine goroutine: the only thing that touches the device or a
// player, so nothing below needs a lock.
func (e *Engine) run() {
	for req := range e.requests {
		switch {
		case req.sound != nil:
			e.setSound(req.key, req.sound)
		default:
			e.play(req.key, req.volume)
		}
	}

	for _, voices := range e.voices {
		for _, v := range voices {
			v.player.Close()
		}
	}
}

// setSound replaces a sound and drops the players reading the old one — their
// buffers are about to stop being what the key means.
func (e *Engine) setSound(key string, sound *Sound) {
	for _, v := range e.voices[key] {
		v.player.Close()
	}
	delete(e.voices, key)

	e.sounds[key] = sound
}

// play sounds one key. It picks the first idle voice, falling back to the oldest
// — a click has to sound even when every copy is still ringing.
func (e *Engine) play(key string, volume float64) {
	sound := e.sounds[key]
	if sound == nil || e.silent {
		return
	}

	now := time.Now()
	if now.Sub(e.last[key]) < minRepeat {
		return
	}

	context := e.device()
	if context == nil {
		return
	}

	v := e.voice(context, key)
	if v == nil {
		return
	}
	e.last[key] = now

	// A typing click plays hundreds of times a minute, and identical gain is most
	// of what makes a run of them read as one repeating sample.
	if IsTyping(key) {
		volume *= 0.88 + rand.Float64()*0.12
	}

	v.player.Reset() // discards whatever the previous play left buffered, and clears its EOF
	v.buffer.reset(sound.take())
	v.player.SetVolume(volume)
	v.player.Play()
}

// device opens the audio device on first use, and gives up for good if it
// refuses: there is no state a retry could reach that the first attempt did not,
// and a client that tries again per keystroke would log a line per keystroke.
func (e *Engine) device() *oto.Context {
	if e.context != nil {
		return e.context
	}

	context, err := openDevice()
	if err != nil {
		log.Printf("open audio device: %v", err)
		e.silent = true

		return nil
	}
	e.context = context

	return context
}

// voice hands back a player for the key, building the pool out as far as the
// sound is allowed to overlap itself before reusing the oldest.
func (e *Engine) voice(context *oto.Context, key string) *voice {
	voices := e.voices[key]

	for _, v := range voices {
		if !v.player.IsPlaying() {
			return v
		}
	}

	if len(voices) < voiceCount(key) {
		buffer := &takeReader{}
		v := &voice{player: context.NewPlayer(buffer), buffer: buffer}
		e.voices[key] = append(voices, v)

		return v
	}

	// Every copy is still ringing, so the oldest is the one whose interruption is
	// least likely to be heard.
	oldest := voices[0]
	e.voices[key] = append(voices[1:], oldest)

	return oldest
}

// voiceCount is how many copies of one sound may overlap. A typing click has to
// survive somebody typing faster than the click is long; nothing else here
// overlaps itself in practice, and a player is a device buffer each.
func voiceCount(key string) int {
	if IsTyping(key) {
		return 6
	}

	return 2
}

/* The device */

// oto allows one context per process and answers a second call with an error, so
// the context is package-level rather than the engine's. The ready channel is
// closed when the device is running; receiving from a closed channel returns at
// once, so a later caller pays nothing.
var (
	deviceOnce  sync.Once
	deviceCtx   *oto.Context
	deviceReady chan struct{}
	deviceErr   error
)

func openDevice() (*oto.Context, error) {
	deviceOnce.Do(func() {
		deviceCtx, deviceReady, deviceErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: channelCount,
			Format:       oto.FormatSignedInt16LE,
		})
	})

	if deviceErr != nil {
		return nil, deviceErr
	}
	<-deviceReady

	return deviceCtx, nil
}

/* The buffer a player reads */

// takeReader is the io.Reader one voice hands its player: a slice it is pointed
// at and reads to the end of.
//
// The mutex is not decoration. oto releases the player's own lock around the
// call to Read (its mux does, to keep an external Read off its critical path),
// so the device goroutine can be inside one while the engine goroutine is
// pointing the voice at its next take.
//
// Read must report io.EOF and never (0, nil): oto's Play fills its buffer with
// `for len(buf) < bufferSize { read() }`, which a reader answering "nothing, no
// error" turns into an infinite loop on the calling goroutine.
type takeReader struct {
	mu   sync.Mutex
	data []byte
	pos  int
}

func (r *takeReader) reset(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data = data
	r.pos = 0
}

func (r *takeReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.pos:])
	r.pos += n

	return n, nil
}
