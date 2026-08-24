package audio

import (
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

// Capture is an open microphone: 48 kHz mono, handed out a frame at a time.
//
// The device callback does one thing — copy into a ring — and everything that
// costs anything happens on whichever goroutine calls Read. That is the
// transport's goroutine, which is already paced by the encoder, so the chain
// adds no hop and no thread of its own.
type Capture struct {
	pcm *ring[float32]

	// mu guards the device and the two names below it. The supervisor is the only
	// thing that opens or closes one, but Close and SetDevice reach in from
	// wherever they are called.
	mu       sync.Mutex
	device   *malgo.Device
	deviceID string // what is open now
	wantID   string // what SetDevice last asked for

	// generation tells a device the backend took away from one being replaced on
	// purpose: a Stop callback carrying anything but the current generation is the
	// tail of a device already being closed. Same guard as Engine's.
	generation atomic.Uint64

	// swap and revive are the supervisor's inbox, both buffered so neither the UI
	// thread nor a miniaudio callback ever waits on it. Neither carries a value —
	// wantID and stopped are where the answers are — so several rapid changes
	// coalesce into one wake that reads the latest.
	swap   chan struct{}
	revive chan struct{}

	// stopped is the newest generation a Stop callback has reported. An atomic
	// beside the signal rather than a value in it: a queued token would go stale
	// — during a swap the old device's stop parks one, and a fresh stop hitting
	// the full channel would then be masked by it, leaving a dead microphone
	// nothing recovers.
	stopped atomic.Uint64

	// wake lets Read block without polling. The callback sends into it without
	// waiting, so a full one — a reader that has not come back yet — costs the
	// callback nothing.
	wake chan struct{}

	chain []Processor
	scrap []float32 // Read's own working frame; never escapes

	gain   atomic.Uint32 // float32 bits
	level  atomic.Uint32 // float32 bits, the settings meter reads it
	voiced atomic.Bool

	// gate is Read's own, like the rest of the chain. A sensitivity change arrives
	// as a number here and is applied by Read, so nothing outside touches a filter
	// that a Read may be inside of.
	gate        *noiseGate
	sensitivity atomic.Int64

	// hp and ns are the other two stages, always in the chain and bypassed rather
	// than absent, so either setting can move mid-call the way sensitivity does:
	// the flag lands here and Read applies it between frames.
	hp       *highPass
	ns       *noiseSuppressor
	highpass atomic.Bool
	suppress atomic.Bool

	// Push-to-talk. push swaps who decides what is sent — the key rather than the
	// gate — and transmit is whether it is being held. Both are set from outside
	// and applied by Read, which is the only thing that touches the chain.
	push     atomic.Bool
	transmit atomic.Bool

	// ramp is the push gate's own fade, for the reason the noise gate has one: a
	// signal switched to and from zero in one sample clicks.
	ramp float32

	closeOnce sync.Once
	closed    chan struct{}
}

// InputConfig is what opening a microphone takes. The zero value is a usable
// default: the system device, unity gain and a middling gate.
type InputConfig struct {
	// Sensitivity is the gate's threshold, 0-100.
	Sensitivity int

	// Gain scales the signal after the gate, 0-2.
	Gain float32

	// HighPass runs the ~90 Hz filter in front of everything else: mains hum, fan
	// rumble, desk knocks — what is below speech, cut before it can hold the gate
	// open. The gate runs either way, that being what the sensitivity slider means.
	HighPass bool

	// NoiseSuppression runs RNNoise between the filter and the gate, which is
	// what removes noise *inside* the voice range while somebody is talking —
	// hiss, fans, keyboard — where the gate can only silence the frames between
	// words.
	NoiseSuppression bool

	// PushToTalk hands the decision to SetTransmitting instead of the gate.
	PushToTalk bool
}

// FrameSamples is how many samples one frame carries: 20 ms at 48 kHz mono,
// which is the Opus frame every caller here encodes. periodSamples is the device
// period underneath it, 10 ms, so a frame is two periods and the gate makes its
// decision twice as often as a packet is sent.
const (
	FrameSamples  = sampleRate * 20 / 1000
	periodSamples = sampleRate * 10 / 1000

	// captureDepth is 200 ms of slack between the device and the reader. Deeper
	// than any scheduling hiccup worth surviving, shallow enough that a reader
	// that stopped is caught at the ring rather than at the ear.
	captureDepth = sampleRate * 200 / 1000
)

// ErrCaptureClosed is what Read answers once the microphone is closed.
var ErrCaptureClosed = errors.New("capture closed")

// OpenInput opens a microphone. id is what a setting stored; an empty one, and
// one nothing answers to, both open the system default — a reader whose headset
// is unplugged gets the built-in microphone rather than an error.
//
// The device is not the Capture: it can be taken away, recovered onto the
// default, or swapped for another by SetDevice, and none of that is visible to
// Read. Only running out of microphones altogether ends a capture.
func OpenInput(id string, cfg InputConfig) (*Capture, error) {
	c := &Capture{
		pcm:    newRing[float32](captureDepth),
		wantID: id,
		swap:   make(chan struct{}, 1),
		revive: make(chan struct{}, 1),
		wake:   make(chan struct{}, 1),
		scrap:  make([]float32, FrameSamples),
		gate:   newNoiseGate(cfg.Sensitivity),
		hp:     newHighPass(90),
		ns:     &noiseSuppressor{},
		closed: make(chan struct{}),
	}
	c.gain.Store(floatBits(clampGain(cfg.Gain)))
	c.sensitivity.Store(int64(cfg.Sensitivity))
	c.push.Store(cfg.PushToTalk)
	c.highpass.Store(cfg.HighPass)
	c.suppress.Store(cfg.NoiseSuppression)

	c.chain = []Processor{c.hp, c.ns, c.gate}

	if err := c.startDevice(id); err != nil {
		return nil, err
	}

	go c.supervise()

	return c, nil
}

// supervise owns the device for the rest of the capture's life. It exists for
// the same reason Engine has a goroutine of its own: reopening from inside a
// Stop callback would be reentering miniaudio from its own thread, and doing it
// from Read would tie recovery to somebody happening to be reading.
func (c *Capture) supervise() {
	for {
		select {
		case <-c.closed:
			return

		case <-c.swap:
			c.mu.Lock()
			want := c.wantID
			c.mu.Unlock()

			c.useDevice(want)

		case <-c.revive:
			// A device already replaced has nothing to say about the current one.
			if c.stopped.Load() == c.generation.Load() {
				c.recoverDevice()
			}
		}
	}
}

// SetDevice moves capture to another microphone. Safe from any goroutine and
// safe mid-call: the swap happens on the supervisor, so a Read blocked on the
// old device is not disturbed — it sees the ring go quiet for a period or two
// and then fill again from the new one.
func (c *Capture) SetDevice(id string) {
	c.mu.Lock()
	c.wantID = id
	c.mu.Unlock()

	select {
	case c.swap <- struct{}{}:
	default: // one is already queued and will read the same wantID
	}
}

// useDevice opens id and leaves the current device alone if it will not open — a
// microphone picked in settings and since unplugged should leave the reader
// audible rather than silent. Mirrors Engine.useOutput.
func (c *Capture) useDevice(id string) {
	if c.isClosed() {
		return
	}

	c.mu.Lock()
	previous := c.deviceID
	c.mu.Unlock()

	if id == previous {
		return
	}

	if err := c.startDevice(id); err == nil {
		return
	} else {
		log.Printf("open microphone %q: %v", id, err)
	}

	if err := c.startDevice(previous); err != nil {
		log.Printf("reopen previous microphone: %v", err)
		c.abandon()
	}
}

// recoverDevice answers the microphone having been taken away underneath us —
// unplugged, or the session pre-empted. It falls back to the system default
// rather than ending the call, which is what a reader who just pulled a headset
// out expects. Mirrors Engine.reopen.
func (c *Capture) recoverDevice() {
	if c.isClosed() {
		return
	}

	c.mu.Lock()
	id := c.deviceID
	c.mu.Unlock()

	if err := c.startDevice(id); err == nil {
		return
	}

	if id == "" {
		c.abandon()
		return
	}

	log.Printf("microphone %q went away; falling back to the default", id)

	if err := c.startDevice(""); err != nil {
		log.Printf("open default microphone: %v", err)
		c.abandon()
	}
}

// isClosed reports whether the capture is done with, so the supervisor does not
// open a device for one that is. Close and abandon are the two things that
// answer yes.
func (c *Capture) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// abandon gives up on there being a microphone at all. Read then answers
// ErrCaptureClosed, which ends the call — the honest outcome, and better than
// publishing silence into a room nobody can be heard in.
func (c *Capture) abandon() {
	log.Print("no microphone left to capture from")
	c.closeOnce.Do(func() { close(c.closed) })
}

// startDevice replaces whatever is open with a device on id, leaving the ring
// and the chain alone: a swap is a new source for the same capture, not a new
// capture.
func (c *Capture) startDevice(id string) error {
	ctx, err := context()
	if err != nil {
		return err
	}

	// Bumped before the old device is stopped, so that device's Stop callback
	// carries a stale generation and is read as a teardown rather than as the
	// backend taking a microphone away.
	generation := c.generation.Add(1)
	c.closeDevice()

	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.SampleRate = sampleRate
	config.Capture.Format = malgo.FormatF32
	config.Capture.Channels = 1
	config.Capture.DeviceID = deviceIDPointer(malgo.Capture, id)
	config.PeriodSizeInFrames = periodSamples
	config.PerformanceProfile = malgo.LowLatency
	// miniaudio otherwise splits a period across two callbacks when the backend's
	// own is a different size, which would hand the ring a partial period.
	config.NoFixedSizedCallback = 0

	device, err := malgo.InitDevice(ctx.Context, config, malgo.DeviceCallbacks{
		Data: c.onData,
		Stop: func() { c.onStop(generation) },
	})
	if err != nil {
		return err
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		return err
	}

	c.mu.Lock()
	c.device, c.deviceID = device, id
	c.mu.Unlock()

	// Close may have run between the init and here, in which case it saw no device
	// and this one would be left open for the rest of the process.
	select {
	case <-c.closed:
		c.closeDevice()
	default:
	}

	return nil
}

// closeDevice stops and releases whatever is open. Idempotent, and safe from
// either the supervisor or Close: the pointer is taken under the lock and
// cleared, so no device is uninitialised twice.
func (c *Capture) closeDevice() {
	c.mu.Lock()
	device := c.device
	c.device = nil
	c.mu.Unlock()

	if device == nil {
		return
	}

	if err := device.Stop(); err != nil {
		log.Printf("stop microphone: %v", err)
	}
	device.Uninit()
}

// onData is the device callback. It copies and returns: no processing, no
// allocation, no lock, no log. Everything else waits for Read.
func (c *Capture) onData(_, in []byte, frames uint32) {
	if len(in) == 0 || frames == 0 {
		return
	}

	samples := asFloats(in)
	c.pcm.PushAll(samples) // a reader that has stalled loses the overflow, not the device

	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// onStop fires on the backend's thread when the device stops — the endpoint was
// unplugged, or the session was pre-empted. It only asks the supervisor to look:
// reopening from here would be reentering miniaudio from inside its own
// callback.
func (c *Capture) onStop(generation uint64) {
	// The highest generation wins: two devices' callbacks can interleave during
	// a swap, and an older one landing later must not hide that the current
	// device stopped.
	for {
		latest := c.stopped.Load()
		if generation <= latest || c.stopped.CompareAndSwap(latest, generation) {
			break
		}
	}

	select {
	case c.revive <- struct{}{}:
	default: // one is already queued; the supervisor reads the newest generation anyway
	}
}

// Read fills pcm with the next frame and reports how many samples that was,
// blocking until the microphone has one. pcm must hold FrameSamples.
//
// It is the whole capture chain: the filters, the gate and the gain all run
// here, on the caller's goroutine.
func (c *Capture) Read(pcm []int16) (int, error) {
	if len(pcm) < FrameSamples {
		return 0, errors.New("frame buffer too small")
	}

	for {
		select {
		case <-c.closed:
			return 0, ErrCaptureClosed
		default:
		}

		if c.pcm.Len() >= FrameSamples {
			break
		}

		select {
		case <-c.wake:
		case <-c.closed:
			return 0, ErrCaptureClosed
		case <-time.After(frameTimeout):
			// The device has gone quiet without saying so. Answering with silence
			// keeps the caller's cadence rather than stalling its encoder.
			clear(pcm[:FrameSamples])
			c.voiced.Store(false)

			return FrameSamples, nil
		}
	}

	push := c.push.Load()

	c.hp.SetBypass(!c.highpass.Load())
	c.ns.SetEnabled(c.suppress.Load())
	c.gate.SetSensitivity(int(c.sensitivity.Load()))
	c.gate.SetBypass(push)

	frame := c.scrap
	c.pcm.PopAll(frame)

	// Measured before the chain, so the settings meter shows what the microphone
	// is hearing rather than what survived the gate.
	c.level.Store(floatBits(rms(frame)))

	voiced := true
	for _, stage := range c.chain {
		voiced = stage.Process(frame)
	}

	if push {
		voiced = c.applyPush(frame)
	}
	c.voiced.Store(voiced)

	gain := bitsFloat(c.gain.Load())
	for i, sample := range frame {
		pcm[i] = floatToSample(sample * gain)
	}

	return FrameSamples, nil
}

// applyPush is the push-to-talk gate: the key decides, and the frame is faded
// rather than switched so releasing it does not click.
func (c *Capture) applyPush(frame []float32) bool {
	held := c.transmit.Load()

	target := float32(0)
	if held {
		target = 1
	}

	step := (target - c.ramp) / float32(len(frame))
	for i := range frame {
		c.ramp += step
		frame[i] *= c.ramp
	}
	c.ramp = target

	return held
}

// SetPushToTalk swaps who decides what is sent. Applied by Read, so a mode
// changed mid-call takes effect on the next frame rather than mid-frame.
func (c *Capture) SetPushToTalk(push bool) { c.push.Store(push) }

// SetTransmitting is the key being held. Meaningless outside push-to-talk, and
// harmless to call anyway.
func (c *Capture) SetTransmitting(on bool) { c.transmit.Store(on) }

// frameTimeout is how long Read waits on a silent device before answering with
// silence. Two frames: long enough that ordinary scheduling never reaches it.
const frameTimeout = 40 * time.Millisecond

// Voiced reports whether the frame Read last answered with passed the gate. The
// caller sends it either way — a silent frame is what lets the encoder emit
// comfort noise instead of dropping the track — so this only says whether
// anybody is speaking.
func (c *Capture) Voiced() bool { return c.voiced.Load() }

// Level is the microphone's level before the gate, 0-1, for the settings meter.
// Safe from any goroutine and cheap enough to poll.
func (c *Capture) Level() float32 { return bitsFloat(c.level.Load()) }

// SetGain scales the captured signal, 0-2.
func (c *Capture) SetGain(gain float32) { c.gain.Store(floatBits(clampGain(gain))) }

// SetSensitivity moves the gate's threshold, 0-100. It records the number and
// leaves the filter alone: Read applies it, so a settings change cannot land in
// the middle of a frame being gated.
func (c *Capture) SetSensitivity(sensitivity int) { c.sensitivity.Store(int64(sensitivity)) }

// SetHighPass turns the rumble filter on or off, mid-call included. Applied by
// Read, like the sensitivity.
func (c *Capture) SetHighPass(on bool) { c.highpass.Store(on) }

// SetNoiseSuppression turns RNNoise on or off, mid-call included. Applied by
// Read, like the sensitivity.
func (c *Capture) SetNoiseSuppression(on bool) { c.suppress.Store(on) }

// Close stops the microphone. Safe from any goroutine, safe to call twice, and
// safe while a Read is waiting — that Read answers ErrCaptureClosed.
func (c *Capture) Close() {
	c.closeOnce.Do(func() { close(c.closed) })

	// Past the open device's generation before stopping it, for the reason
	// startDevice does the same: otherwise its Stop callback reaches the
	// supervisor as a microphone the backend took away, and the supervisor —
	// picking between two ready channels — may act on it before it sees the close.
	c.generation.Add(1)
	c.closeDevice()
}
