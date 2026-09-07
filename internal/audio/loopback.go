package audio

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"
)

// Loopback is the machine's own output captured back: what a screenshare sends
// beside its picture. It is a *raw* source and deliberately shares nothing with
// Capture's chain — a high-pass, a noise model and a gate are what a microphone
// in a room needs, and every one of them is damage to a game or a track that
// was rendered clean.
//
// Two things can be behind it. A process ID captures that program and its
// children alone, which is what makes sharing a window send that window's sound
// and not a notification arriving over it; zero captures the whole mix. The
// per-OS half decides how, and pushes here.
type Loopback struct {
	pcm *ring[int16]

	format LoopbackFormat

	// stream is the per-OS capture, which owns a thread of its own and pushes
	// into the ring. Closed once, by Close.
	stream loopbackStream

	// wake lets Read block without polling, and is written to without waiting:
	// a full one is a reader that has not come back yet, which costs the
	// producer nothing.
	wake chan struct{}

	closed  chan struct{}
	closing atomic.Bool

	frameTimer *time.Timer // Read's own; Read has one caller at a time
}

// loopbackStream is what a platform's capture answers with: something to stop.
// The rest of its life is its own — it holds the thread, and pushes frames in.
type loopbackStream interface {
	Close() error
}

// loopbackMaxDepth is the ring, sized for the widest format on offer — 48 kHz
// stereo — because the format is not settled until the capture has opened and
// the ring has to exist before it can push. Every negotiated format is at most
// this, so one size fits.
const loopbackMaxDepth = 48000 * 2 * loopbackDepth / 1000

// LoopbackConfig is what to capture and at what.
type LoopbackConfig struct {
	/* What */

	// ProcessID captures that process and every child of it, which is how a
	// shared window sends its own sound and nothing else. Zero captures the
	// whole machine's mix instead.
	ProcessID uint32

	// Exclude inverts ProcessID: everything the machine is playing *but* that
	// process tree. Ignored when ProcessID is zero.
	Exclude bool

	/* At what */

	Format LoopbackFormat
}

// LoopbackFormat is the shape the audio engine is asked to deliver in.
//
// Both fields are the caller's to choose because a loopback stream has no
// format of its own to inherit: the engine is already mixing and converting
// whatever the captured programs render, so naming the far end of that
// conversion costs nothing and saves this process doing it again.
type LoopbackFormat struct {
	// SampleRate must be one Opus encodes natively, which is what keeps a
	// resampler out of the path entirely — see LoopbackRates.
	SampleRate int

	// Channels is 1 or 2. Two is what a screenshare wants: a game's stereo
	// image is most of what makes it sound like the sender's machine.
	Channels int

	// BitDepth is 16 for signed PCM or 32 for float.
	//
	// It changes what the *engine* is asked for and nothing further down: Opus
	// is the wire either way, so this is not a quality dial a listener hears.
	// 16 is the shorter path — the engine converts once and the samples arrive
	// in the encoder's own format — and 32 skips the engine's conversion at the
	// cost of this process doing one instead.
	BitDepth int
}

// LoopbackRates are the sample rates on offer: Opus's own, so that whatever is
// picked, nothing in this process resamples. 44100 is deliberately absent —
// the engine would deliver it happily and the encoder would then have to be
// fed a resampler's output, which is a filter and a delay bought for nothing.
var LoopbackRates = []int{48000, 24000, 16000, 12000, 8000}

// loopbackDepth is how much slack sits between the capture thread and the
// reader — 200 ms, Capture's own, and for the same reason: past any scheduling
// hiccup worth surviving, short enough that a reader that has stopped is caught
// at the ring rather than as delay somebody hears.
const loopbackDepth = 200

// ErrLoopbackClosed is what Read answers once the capture is closed.
var ErrLoopbackClosed = errors.New("loopback closed")

// ErrNoLoopback is what a platform with no system-audio capture answers with.
var ErrNoLoopback = errors.New("this system offers no audio loopback")

// LoopbackAvailable reports whether this machine can capture what it is
// playing. It is what decides whether the setting is *drawn* — the same seam a
// device list crosses on, an answer the controller carries up rather than a
// platform test in a widget.
func LoopbackAvailable() bool { return loopbackAvailable }

// valid reports what is wrong with a format, so a bad setting is refused at the
// open rather than by the audio engine several layers down.
func (f LoopbackFormat) valid() error {
	if !slices.Contains(LoopbackRates, f.SampleRate) {
		return fmt.Errorf("audio: %d Hz is not a rate Opus encodes natively", f.SampleRate)
	}
	if f.Channels != 1 && f.Channels != 2 {
		return fmt.Errorf("audio: %d channels is neither mono nor stereo", f.Channels)
	}
	if f.BitDepth != 16 && f.BitDepth != 32 {
		return fmt.Errorf("audio: %d-bit is neither 16-bit PCM nor 32-bit float", f.BitDepth)
	}

	return nil
}

// String is how a format reads in a log line or a refusal.
func (f LoopbackFormat) String() string {
	return fmt.Sprintf("%d Hz %d-bit %dch", f.SampleRate, f.BitDepth, f.Channels)
}

// FrameSamples is one 20 ms frame's worth of this format, channels included —
// what Read fills and what the encoder takes.
func (f LoopbackFormat) FrameSamples() int {
	return f.SampleRate / 50 * f.Channels
}

// bytesPerFrame is one sample across every channel, which is what the OS side
// steps its buffers by.
func (f LoopbackFormat) bytesPerFrame() int {
	return f.BitDepth / 8 * f.Channels
}

// OpenLoopback starts capturing what the machine is playing.
//
// The format asked for is what the capture is opened with wherever the platform
// lets it be chosen, which on Windows is every path that matters. Where it does
// not, the open settles on the nearest one that resamples nothing and reports it
// through Format — so the caller configures its encoder from what arrived rather
// than from what it asked for, and a machine whose engine mixes at a rate Opus
// cannot take is refused rather than quietly resampled.
func OpenLoopback(cfg LoopbackConfig) (*Loopback, error) {
	if err := cfg.Format.valid(); err != nil {
		return nil, err
	}

	l := &Loopback{
		pcm:    newRing[int16](loopbackMaxDepth),
		format: cfg.Format,
		wake:   make(chan struct{}, 1),
		closed: make(chan struct{}),
	}

	stream, format, err := openLoopback(cfg, l)
	if err != nil {
		return nil, err
	}
	l.stream, l.format = stream, format

	return l, nil
}

// Format is what this capture is delivering, which is what the caller's encoder
// has to be built for. Usually what was asked for; see OpenLoopback.
func (l *Loopback) Format() LoopbackFormat { return l.format }

// push is the per-OS capture's way in. Producer side of the ring and the only
// writer: whatever does not fit is dropped, a backlog being latency the reader
// cannot use.
func (l *Loopback) push(pcm []int16) {
	l.pcm.PushAll(pcm)

	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// Read fills pcm with one 20 ms frame, blocking until there is one. It is
// called from the publish goroutine and nowhere else.
//
// A loopback stream that has nothing to say sends *nothing* rather than
// silence — a process rendering no audio produces no packets at all — so a
// frame that does not arrive in time is answered with silence, which keeps the
// encoder's cadence where stalling would hand the room a gap.
func (l *Loopback) Read(pcm []int16) (int, error) {
	frame := l.format.FrameSamples()
	if len(pcm) < frame {
		return 0, errors.New("frame buffer too small")
	}

	for {
		select {
		case <-l.closed:
			return 0, ErrLoopbackClosed
		default:
		}

		if l.pcm.Len() >= frame {
			l.pcm.PopAll(pcm[:frame])
			return frame, nil
		}

		// Reset flushes a stale expiry since Go 1.23, so no drain dance.
		if l.frameTimer == nil {
			l.frameTimer = time.NewTimer(frameTimeout)
		} else {
			l.frameTimer.Reset(frameTimeout)
		}

		select {
		case <-l.wake:
		case <-l.closed:
			return 0, ErrLoopbackClosed
		case <-l.frameTimer.C:
			clear(pcm[:frame])
			return frame, nil
		}
	}
}

// Close stops the capture. Idempotent, and safe from any goroutine.
func (l *Loopback) Close() {
	if l.closing.Swap(true) {
		return
	}
	close(l.closed)

	if l.stream != nil {
		_ = l.stream.Close()
	}
}
