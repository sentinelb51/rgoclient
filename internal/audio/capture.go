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

	voiced atomic.Bool

	// soft rounds a peak the gain pushed over the ceiling instead of slicing it
	// flat. Read once per frame, outside the loop it decides.
	soft atomic.Bool

	// gate is Read's own, like the rest of the chain. A threshold change arrives
	// as a number here and is applied by Read, so nothing outside touches a filter
	// that a Read may be inside of.
	gate      *noiseGate
	threshold atomic.Int64 // dBFS

	// hp and ns are the other two filtering stages, always in the chain and
	// bypassed rather than absent, so either setting can move mid-call the way
	// sensitivity does: the flag lands here and Read applies it between frames.
	hp       *highPass
	ns       *noiseSuppressor
	highpass atomic.Bool
	suppress atomic.Bool
	model    atomic.Int32 // NoiseModel

	// suppFloor is the suppression strength as the gain floor it maps to
	// (float32 bits), and vadThreshold the gate's speech veto, 0-100. Both land
	// here and are applied by Read, like the sensitivity.
	suppFloor    atomic.Uint32
	vadThreshold atomic.Int64

	// idle is nobody consuming what Read answers with — a muted call, which still
	// reads to keep the encoder's cadence and throws every frame away. It holds
	// the suppressor off for as long as it stands, that stage being 97 % of what
	// the chain costs and the only one whose output is worth nothing unheard.
	//
	// The model is held, not dropped: Process skips while disabled and leaves the
	// state alone, so resuming is the same room it was already tracking rather
	// than a cold start. Applied by Read, like the sensitivity.
	idle atomic.Bool

	// frameTimer paces the silent-device fallback in Read. One reused timer
	// rather than a time.After per wait: Read waits once or twice every frame,
	// and a fresh timer each pass was the last per-frame allocation on the
	// capture path.
	frameTimer *time.Timer

	// pre is the gain, and holds the level the meter reads. It carries its own
	// atomics rather than being set through Read, having nothing a frame boundary
	// protects: a gain that changes mid-frame is a gain that changes mid-frame.
	pre *preamp

	// Push-to-talk. push swaps who decides what is sent — the key rather than the
	// gate — and transmit is whether it is being held. Both are set from outside
	// and applied by Read, which is the only thing that touches the chain.
	push     atomic.Bool
	transmit atomic.Bool

	// push's own fade, for the reason the noise gate has one: a signal switched
	// to and from zero in one sample clicks.
	pushFade fader

	// echo is where Read additionally writes the frame it has just answered with,
	// for the settings page's microphone test. Nil is off, which is what every
	// frame of an ordinary call costs: one atomic load.
	//
	// It is filled from inside Read rather than by a reader of its own because
	// Read has exactly one caller — during a call that is the publisher — so a
	// test tapping the microphone any other way could not run mid-call at all.
	echo atomic.Pointer[Sink]

	closeOnce sync.Once
	closed    chan struct{}
}

// InputConfig is what opening a microphone takes. Every field is what its own
// setter means, so a capture can be reconfigured to anything it could have been
// opened as — except Gain, whose zero is silence rather than unity and which
// therefore has to be set.
type InputConfig struct {
	// GateThresholdDB is where the gate opens, in dBFS. The zero value is out of
	// the range this package acts on and is clamped to the loudest end, so it is
	// the second field after Gain that has to be set rather than left.
	GateThresholdDB int

	// Gain scales the signal in front of the gate, 0 to maxGain: it is what the
	// gate then measures, so it is also what decides whether a quiet microphone
	// opens one at all. Unity is 1, and 0 is silence.
	Gain float32

	// SoftClip rounds a peak the gain pushed over the ceiling rather than slicing
	// it flat. Off in the zero value, which is the hard clamp this had before the
	// gain range grew far enough to need the curve.
	SoftClip bool

	// HighPass runs the ~90 Hz filter in front of everything else: mains hum, fan
	// rumble, desk knocks — what is below speech, cut before it can hold the gate
	// open. The gate runs either way, that being what the sensitivity slider means.
	HighPass bool

	// NoiseSuppression runs the noise model between the filter and the gate,
	// which is what removes noise *inside* the voice range while somebody is
	// talking — hiss, fans, keyboard — where the gate can only silence the
	// frames between words.
	NoiseSuppression bool

	// NoiseModel is which network that is. The zero value is RNNoise.
	NoiseModel NoiseModel

	// SuppressionFloor caps how deep that suppression cuts, as the linear gain
	// floor audio.SuppressionFloor maps a strength in decibels to. The zero
	// value is full suppression, which is what the stage always did.
	SuppressionFloor float32

	// VADThreshold is the gate's speech veto, 0-100: the suppressor's model must
	// be at least this sure a frame holds speech before loudness may open the
	// gate. 0 — the zero value — leaves the gate to loudness alone, and the veto
	// only runs while NoiseSuppression does, that being what computes it.
	VADThreshold int

	// PushToTalk hands the decision to SetTransmitting instead of the gate.
	PushToTalk bool
}

// FrameSamples is how many samples one frame carries: 20 ms at 48 kHz mono,
// which is the Opus frame every caller here encodes, and the frame the whole
// chain — the gate's decision included — runs at. periodSamples is the device
// period underneath it, 10 ms, so Read waits for two.
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
		gate:   newNoiseGate(cfg.GateThresholdDB),
		hp:     newHighPass(90),
		ns:     newNoiseSuppressor(),
		pre:    newPreamp(cfg.Gain),
		closed: make(chan struct{}),
	}
	c.threshold.Store(int64(cfg.GateThresholdDB))
	c.push.Store(cfg.PushToTalk)
	c.highpass.Store(cfg.HighPass)
	c.suppress.Store(cfg.NoiseSuppression)
	c.model.Store(int32(cfg.NoiseModel))
	c.suppFloor.Store(floatBits(cfg.SuppressionFloor))
	c.vadThreshold.Store(int64(cfg.VADThreshold))
	c.soft.Store(cfg.SoftClip)

	// The gain is inside the chain and in front of the gate: the gate's threshold
	// is then compared against the signal that is actually sent, and RNNoise ahead
	// of it still sees the level the microphone delivered.
	c.chain = []Processor{c.hp, c.ns, c.pre, c.gate}

	// The gate's speech veto reads the suppressor's estimate, which the chain
	// order above has already computed by the time the gate runs.
	c.gate.vad = c.ns

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

		// Reset flushes a stale expiry since Go 1.23, so no drain dance — and the
		// timer is Read's own, Read having exactly one caller at a time.
		if c.frameTimer == nil {
			c.frameTimer = time.NewTimer(frameTimeout)
		} else {
			c.frameTimer.Reset(frameTimeout)
		}

		select {
		case <-c.wake:
		case <-c.closed:
			return 0, ErrCaptureClosed
		case <-c.frameTimer.C:
			// The device has gone quiet without saying so. Answering with silence
			// keeps the caller's cadence rather than stalling its encoder.
			clear(pcm[:FrameSamples])
			c.voiced.Store(false)

			return FrameSamples, nil
		}
	}

	push := c.push.Load()

	// Idle folds into the setting rather than standing beside it: a disabled
	// suppressor already answers "no opinion" and leaves its state untouched,
	// which is exactly what an unheard frame wants.
	suppress := c.suppress.Load() && !c.idle.Load()

	c.hp.SetBypass(!c.highpass.Load())
	c.ns.SetEnabled(suppress)
	c.ns.SetModel(NoiseModel(c.model.Load()))
	c.ns.SetFloor(bitsFloat(c.suppFloor.Load()))
	c.gate.SetThreshold(int(c.threshold.Load()))
	c.gate.SetBypass(push)

	// The veto is armed only while the suppressor computes the estimate it reads.
	vad := 0
	if suppress {
		vad = int(c.vadThreshold.Load())
	}
	c.gate.SetVADThreshold(vad)

	frame := c.scrap
	c.pcm.PopAll(frame)

	// The preamp inside the chain takes the meter's measurement, after the gain
	// and before the gate: the bar then shows what the gate is deciding about
	// rather than what the microphone delivered, and the two cannot disagree.
	voiced := true
	for _, stage := range c.chain {
		voiced = stage.Process(frame)
	}

	if push {
		voiced = c.applyPush(frame)
	}
	c.voiced.Store(voiced)

	// Hoisted rather than branched per sample, and the setting cannot change
	// inside a frame anyway.
	if c.soft.Load() {
		for i, sample := range frame {
			pcm[i] = floatToSample(softClip(sample))
		}
	} else {
		for i, sample := range frame {
			pcm[i] = floatToSample(sample)
		}
	}

	c.echoFrame(pcm[:FrameSamples])

	return FrameSamples, nil
}

// echoFrame hands the finished frame to the microphone test. The lane is fed at
// the microphone's own rate and drained at the speakers', so a long test on two
// devices whose clocks differ drifts — which the lane's own backlog cap answers,
// exactly as it does for a participant whose clock is somebody else's.
func (c *Capture) echoFrame(pcm []int16) {
	if sink := c.echo.Load(); sink != nil {
		sink.Write(echoLane, pcm)
	}
}

// SetEcho plays what Read answers with back through the speakers — the settings
// page's microphone test — or stops it with a nil sink. The frame handed over is
// the one the call would send, so the whole chain is in it: the filters, the
// gate, the gain and the soft clipping.
//
// Sink.StartEcho has to have opened the lane, and Sink.StopEcho is what drops
// what is left in it.
func (c *Capture) SetEcho(sink *Sink) { c.echo.Store(sink) }

// applyPush is the push-to-talk gate: the key decides, and the frame is faded
// rather than switched so releasing it does not click.
func (c *Capture) applyPush(frame []float32) bool {
	held := c.transmit.Load()

	target := float32(0)
	if held {
		target = 1
	}
	c.pushFade.apply(frame, target)

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

// Level is the microphone's level after the gain and before the gate, 0-1, for
// the settings meter — the same measurement the gate's threshold is compared
// against, so the bar and the mark on it mean one thing. Safe from any goroutine
// and cheap enough to poll.
func (c *Capture) Level() float32 { return c.pre.Level() }

// VAD is the noise suppressor's own estimate that the last frame held speech,
// 0-1, for the settings meter drawing it beside the gate's veto — and negative
// where there is no estimate to give, the model running only while suppression
// does. Safe from any goroutine and cheap enough to poll.
func (c *Capture) VAD() float32 { return c.ns.VAD() }

// SetGain scales the captured signal, 0 to maxGain. Applied by the preamp on the
// next frame it processes, mid-call included.
func (c *Capture) SetGain(gain float32) { c.pre.SetGain(gain) }

// SetSoftClip picks how a boosted peak meets the ceiling: rounded, or sliced
// flat. Applied by Read, like the sensitivity.
func (c *Capture) SetSoftClip(on bool) { c.soft.Store(on) }

// SetGateThreshold moves where the gate opens, in dBFS. It records the number
// and leaves the filter alone: Read applies it, so a settings change cannot land
// in the middle of a frame being gated.
func (c *Capture) SetGateThreshold(db int) { c.threshold.Store(int64(db)) }

// SetHighPass turns the rumble filter on or off, mid-call included. Applied by
// Read, like the sensitivity.
func (c *Capture) SetHighPass(on bool) { c.highpass.Store(on) }

// SetNoiseSuppression turns the noise model on or off, mid-call included.
// Applied by Read, like the sensitivity.
func (c *Capture) SetNoiseSuppression(on bool) { c.suppress.Store(on) }

// SetNoiseModel picks which network that is, mid-call included. Applied by
// Read, like the sensitivity.
func (c *Capture) SetNoiseModel(model NoiseModel) { c.model.Store(int32(model)) }

// SetSuppressionFloor caps how deep the suppression cuts — the linear floor
// SuppressionFloor maps a strength in decibels to. Applied by Read.
func (c *Capture) SetSuppressionFloor(floor float32) { c.suppFloor.Store(floatBits(floor)) }

// SetVADThreshold moves the gate's speech veto, 0-100, 0 off. Applied by Read,
// and only while noise suppression runs — the model is what computes the answer.
func (c *Capture) SetVADThreshold(percent int) { c.vadThreshold.Store(int64(percent)) }

// SetIdle says whether anything is consuming what Read answers with. Read still
// runs — it is the caller's clock, and a muted publisher is still paced by it —
// but the suppressor is held off while nobody can hear the result.
//
// Only the caller knows: a muted call is still metered by the settings page,
// which reads the same capture and wants the chain it is tuning. Applied by
// Read, like the sensitivity.
func (c *Capture) SetIdle(idle bool) { c.idle.Store(idle) }

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
