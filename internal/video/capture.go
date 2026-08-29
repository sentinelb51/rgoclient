package video

// Sending a screenshare is the player's pipeline run backwards: one ffmpeg
// child grabs the screen, scales into a box this side chose and encodes VP8,
// writing IVF to stdout — exactly the byte stream the receive half feeds a
// decoder, because it is exactly what lksdk's reader track eats.
//
// The child is contained, not sandboxed. The strict profile forbids what
// capture *is* — bwrap's empty namespaces sever the X11 socket, and the
// Windows low-integrity token cannot read other programs' windows — and the
// input is this machine's own screen: nobody else's bytes reach the child, so
// the player's threat model does not apply. What is kept is the resource
// half: priority, a memory cap, no core files, kill-on-parent where the
// platform has it. Deliberately no CPU-seconds cap — an encoder legitimately
// spends an hour of CPU on an afternoon of sharing, which is exactly what
// RLIMIT_CPU would kill.

import (
	"fmt"
	"os/exec"
)

/* Sources */

// CaptureKind says what a capture source is: a monitor, or one window.
type CaptureKind int

const (
	CaptureMonitor CaptureKind = iota
	CaptureWindow
)

// CaptureSource is one thing this machine can share, as the platform names
// it. ID is grabber-specific — an X11 window id, a window title on Windows —
// and the geometry is where enumeration found it, which sizes the encode box
// and, for a monitor, aims the grab.
type CaptureSource struct {
	ID    string
	Kind  CaptureKind
	Title string

	X, Y          int
	Width, Height int
}

/* The capture child */

// CaptureConfig is one share's shape. Width and Height are the encode box —
// this side's numbers, even, already fitted under whatever the server
// enforces — and the source is scaled into it with its aspect kept, padded
// where they disagree. Bitrate is the encoder's target in bits per second.
type CaptureConfig struct {
	Source CaptureSource

	Width, Height int
	FPS           int
	Bitrate       int
}

// captureFPS is the frame rates a share may run at. The list reaches a
// command line, so it is an allowlist rather than a range.
var captureFPS = map[int]bool{5: true, 15: true, 30: true, 60: true}

// captureBitrate bounds the encoder's target: a floor under which VP8 is
// porridge at any size, and a ceiling nobody's upstream wants exceeded.
const (
	captureMinBitrate = 200_000
	captureMaxBitrate = 10_000_000
)

// CaptureShare starts the one child a running share is: grab, scale, encode,
// IVF on stdout. The caller reads it until the share ends and Stops the
// stream to end it from this side; the child exiting on its own — the
// captured window closed, the display went away — is EOF on the pipe.
func (t Tools) CaptureShare(cfg CaptureConfig) (*Stream, error) {
	if err := checkFrameSize(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	if cfg.Width%2 != 0 || cfg.Height%2 != 0 {
		return nil, fmt.Errorf("video: encode box %dx%d is not even", cfg.Width, cfg.Height)
	}
	if !captureFPS[cfg.FPS] {
		return nil, fmt.Errorf("video: not a share frame rate: %d", cfg.FPS)
	}

	grab, err := grabArgs(cfg)
	if err != nil {
		return nil, err
	}

	bitrate := min(max(cfg.Bitrate, captureMinBitrate), captureMaxBitrate)

	args := []string{"-v", "error", "-nostdin"}
	args = append(args, grab...)
	args = append(args,
		"-an", "-sn", "-dn",
		// The fps filter makes the stream honestly constant-rate — the IVF
		// timebase becomes 1/FPS and the pts steps by one, which is the clock
		// the publisher paces the track by. The pad half of the scale keeps
		// the declared size true through a window resizing mid-share.
		"-vf", fmt.Sprintf("fps=%d,%s", cfg.FPS, liveScaleFilter(cfg.Width, cfg.Height)),
		"-c:v", "libvpx", "-pix_fmt", "yuv420p",
		// Realtime: no lookahead, no alt-refs, speed over quality. The
		// error-resilient bit is what lets a receiver pick the stream back up
		// after loss without waiting out the whole GOP.
		"-deadline", "realtime", "-cpu-used", "8", "-lag-in-frames", "0",
		"-error-resilient", "1",
		"-b:v", fmt.Sprint(bitrate), "-maxrate", fmt.Sprint(bitrate),
		"-bufsize", fmt.Sprint(2*bitrate),
		// A fixed two-second GOP: a CLI encoder cannot answer a viewer's PLI,
		// so the interval is the worst case a late joiner waits for a picture.
		"-g", fmt.Sprint(2*cfg.FPS),
		"-threads", captureThreads(cfg.Width, cfg.Height),
		"-f", "ivf", "pipe:1",
	)

	return captureLaunch(t.FFmpeg, args)
}

// captureThreads sizes the encoder the way liveThreads sizes the decoder —
// by output area, an encode costing roughly what the matching decode does
// times three.
func captureThreads(width, height int) string {
	if width*height > 960*540 {
		return "4"
	}

	return "2"
}

// captureLaunch is launch without the sandbox wrapper: the grabber needs the
// display the sandbox exists to sever. Containment still applies — the
// platform attrs and the capture flavour of hardening.
func captureLaunch(tool string, args []string) (*Stream, error) {
	cmd := exec.Command(tool, args...)
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	captureAttrs(cmd)

	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("video: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("video: %w", err)
	}

	release := hardenCapture(cmd)

	return &Stream{cmd: cmd, out: out, stderr: stderr, release: release, done: make(chan struct{})}, nil
}
