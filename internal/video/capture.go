package video

// Sending a screenshare is the player's pipeline run backwards: one ffmpeg
// child grabs the screen, scales into a box this side chose and encodes,
// writing to stdout exactly a byte stream lksdk's reader track eats — AV1 in
// IVF where this machine's GPU encodes it (frame by frame), H.264 as bare
// Annex-B otherwise (NAL by NAL). Two codec families, one rule each: AV1 is
// **hardware or nothing** — libaom and SVT-AV1 cannot hold a real-time screen
// encode without eating the machine the share is about — where H.264 always
// has libx264 to fall back to, which is the whole reason it is the fallback
// codec: no VP8 encoder exists in silicon on any GPU, and no CPU carries a
// live AV1 encode politely.
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
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

/* Sources */

// CaptureKind says what a capture source is: a monitor, or one window.
type CaptureKind int

const (
	CaptureMonitor CaptureKind = iota
	CaptureWindow
)

// CaptureSource is one thing this machine can share, as the platform names
// it. ID is grabber-specific — an X11 window id, a Win32 handle — and lives
// only as long as the enumeration that produced it, so it is what a grab is
// aimed by and never what a choice is remembered by. The geometry is where
// enumeration found it, which sizes the encode box and, for a monitor on the
// oldest Windows path, aims the grab.
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
// where they disagree. Bitrate is the encoder's target in bits per second, and
// Speed is what the encoder may spend reaching it.
type CaptureConfig struct {
	Source CaptureSource

	Width, Height int
	FPS           int
	Bitrate       int

	// KeyframeSeconds is how often a keyframe is forced, in seconds — the
	// worst case a late joiner waits for a picture, a CLI encoder having no
	// way to answer a viewer's PLI. Zero takes the two-second default;
	// anything else is clamped to [1, 10].
	KeyframeSeconds int

	Codec   CaptureCodec
	Speed   CaptureSpeed
	Latency CaptureLatency
	Rate    CaptureRate
}

// CaptureCodec is which codec family a share may be encoded in. Auto takes
// AV1 where the GPU offers an encoder and H.264 otherwise; H264 skips the AV1
// probe entirely, for a viewer whose client cannot take AV1 yet.
type CaptureCodec int

const (
	CaptureCodecAuto CaptureCodec = iota
	CaptureCodecH264
)

// captureFPS is the frame rates a share may run at. The list reaches a
// command line, so it is an allowlist rather than a range.
var captureFPS = map[int]bool{5: true, 15: true, 30: true, 60: true}

// captureBitrate bounds the encoder's target: a floor under which H.264 is
// porridge at any size, and a ceiling nobody's upstream wants exceeded.
const (
	captureMinBitrate = 200_000
	captureMaxBitrate = 10_000_000
)

// CaptureSpeed is how much work the encoder may spend on a frame. Each
// encoder spells the three levels as presets of its own; on a hardware
// encoder the spend is the silicon's and barely registers either way, which
// is rather the point of probing for one.
type CaptureSpeed int

const (
	CaptureQuality CaptureSpeed = iota
	CaptureBalanced
	CaptureFast
)

// CaptureLatency is how long the encoder may sit on frames before answering
// with bytes. Lowest is frame-in, bytes-out — no lookahead, no reordering,
// nothing queued behind the driver. Buffered lets rate control read a short
// run of frames before spending bits on any of them, which sharpens motion
// at the same bitrate and holds every viewer up to a second behind the
// screen. Neither touches the one-slice contract pacing depends on.
type CaptureLatency int

const (
	CaptureLowestLatency CaptureLatency = iota
	CaptureBuffered
)

// CaptureRate is how the bitrate ceiling is spent. Variable spends bits on
// what moves and nearly nothing on a still screen, the ceiling binding only
// while the picture is busy. Constant sends the ceiling at all times, padding
// what does not need it — which wastes upload and is what a receiver's
// bandwidth estimator, a fixed uplink or an ingest expecting a steady stream
// is easiest on: an estimate probed continuously never has to be re-found when
// motion starts, and a burst out of an idle stream is what overshoots a pipe
// nothing has been measuring.
type CaptureRate int

const (
	CaptureVariable CaptureRate = iota
	CaptureConstant
)

/* The encoder */

// shareEncoder is one way of turning frames into a share's bytes: the -c:v
// name, the global args it needs before the input (a hardware device opened),
// and the suffix the filter chain must end with to hand that hardware its
// frames.
type shareEncoder struct {
	name string
	pre  []string
	tail string
}

// hardware reports whether the encode leaves the CPU.
func (e shareEncoder) hardware() bool { return e.name != "libx264" }

// av1 reports whether the encoder writes AV1 — which also decides the
// container: AV1 goes out as IVF, H.264 as bare Annex-B.
func (e shareEncoder) av1() bool { return strings.HasPrefix(e.name, "av1_") }

// args is the encoder's own flags at one effort and latency. Rate control is
// not among them — that is rateControl's, so the two halves of a bitrate
// decision are not spelled in different places. The one exception is x264's
// constant-rate flag, which has to travel with the tune it shares a parameter
// string with; the rate is a parameter here for that alone.
//
// Every H.264 set holds to the same contract: baseline-family profile
// (WebRTC's common ground, and no B-frames by definition), 4:2:0, and **one
// slice per frame** — lksdk paces the track by sleeping a frame's duration per
// VCL NAL, so a sliced encode would publish at a fraction of its speed.
// Encoders default to one slice; x264's zerolatency tune does not, which is
// what sliced-threads=0 unwinds — and dropping the tune for the buffered mode
// leaves slicing off, so the contract holds in both. The AV1 sets carry no
// profile and no slicing clause: 8-bit 4:2:0 *is* profile 0, the only one
// WebRTC speaks, and IVF frames the stream one temporal unit per sample, so
// pacing cannot split.
func (e shareEncoder) args(speed CaptureSpeed, latency CaptureLatency, rate CaptureRate, fps int) []string {
	pick := func(quality, balanced, fast string) string {
		switch speed {
		case CaptureBalanced:
			return balanced
		case CaptureFast:
			return fast
		}

		return quality
	}
	buffered := latency == CaptureBuffered

	switch e.name {
	case "av1_nvenc":
		args := []string{"-c:v", "av1_nvenc", "-pix_fmt", "yuv420p",
			"-preset", pick("p4", "p3", "p1")}
		if buffered {
			return append(args, "-tune", "hq", "-rc-lookahead", lookahead(fps))
		}

		return append(args, "-tune", "ull", "-zerolatency", "1", "-delay", "0")
	case "av1_amf":
		usage := "ultralowlatency"
		if buffered {
			usage = "transcoding"
		}

		return []string{"-c:v", "av1_amf", "-pix_fmt", "yuv420p",
			"-usage", usage,
			"-quality", pick("quality", "balanced", "speed")}
	case "av1_qsv":
		args := []string{"-c:v", "av1_qsv", "-pix_fmt", "nv12",
			"-preset", pick("medium", "fast", "veryfast")}
		if buffered {
			return args // the default async depth is the buffering
		}

		return append(args, "-async_depth", "1")
	case "av1_vaapi":
		// No latency dial: the driver buffers what it buffers.
		return []string{"-c:v", "av1_vaapi"}
	case "h264_nvenc":
		args := []string{"-c:v", "h264_nvenc", "-pix_fmt", "yuv420p",
			"-preset", pick("p4", "p3", "p1"),
			"-profile:v", "baseline"}
		if buffered {
			return append(args, "-tune", "hq", "-rc-lookahead", lookahead(fps))
		}

		return append(args, "-tune", "ull", "-zerolatency", "1", "-delay", "0")
	case "h264_amf":
		usage := "ultralowlatency"
		if buffered {
			usage = "transcoding"
		}

		return []string{"-c:v", "h264_amf", "-pix_fmt", "yuv420p",
			"-usage", usage,
			"-quality", pick("quality", "balanced", "speed"),
			"-profile:v", "constrained_baseline"}
	case "h264_qsv":
		args := []string{"-c:v", "h264_qsv", "-pix_fmt", "nv12",
			"-preset", pick("medium", "fast", "veryfast"),
			"-profile:v", "baseline"}
		if buffered {
			return args // the default async depth is the buffering
		}

		return append(args, "-async_depth", "1")
	case "h264_vaapi":
		// No latency dial: the driver buffers what it buffers.
		return []string{"-c:v", "h264_vaapi", "-profile:v", "constrained_baseline"}
	}

	args := []string{"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-preset", pick("veryfast", "superfast", "ultrafast"),
		"-profile:v", "baseline"}
	if !buffered {
		args = append(args, "-tune", "zerolatency") // the preset's own rc-lookahead and mb-tree go
	}

	// ffmpeg's -x264-params *replaces* rather than merges, so a second flag
	// would drop the first: the two things that set one are joined here.
	// sliced-threads=0 unwinds the zerolatency tune's slicing (see the header),
	// and nal-hrd=cbr is what makes x264 pad — it is the one encoder here that
	// does not until asked, which is why capped VBR was free on it.
	var params []string
	if !buffered {
		params = append(params, "sliced-threads=0")
	}
	if rate == CaptureConstant {
		params = append(params, "nal-hrd=cbr")
	}
	if len(params) > 0 {
		args = append(args, "-x264-params", strings.Join(params, ":"))
	}

	return args
}

// lookaheadSeconds is how far ahead the buffered mode may read, as a fraction
// of a second rather than a frame count: a lookahead is paid for in *time*,
// and twenty frames is a third of a second at 60 fps and four seconds at five.
const lookaheadSeconds = 0.5

// lookaheadMax caps it where the rate is high enough for half a second to be
// more frames than an encoder gains anything from holding.
const lookaheadMax = 20

// lookahead is the buffered mode's depth at one frame rate.
func lookahead(fps int) string {
	depth := int(float64(fps) * lookaheadSeconds)

	return fmt.Sprint(min(max(depth, 1), lookaheadMax))
}

// rateControl is everything about *how many bits*: the mode, the target, the
// ceiling and the buffer. Capped VBR is the default and is the largest single
// saving in the send half — a screen is idle most of the time, and CBR pays the
// target rate for a still picture, padding the difference with filler. Measured
// on this machine at 1080p30 with a 6.2 Mbps target: 5.97 Mbps for a static
// screen under CBR against 0.05 under VBR, and an identical 6.35 for content
// that genuinely needs it. The ceiling is what a slow uplink is protected by;
// the average is what an idle one actually spends.
//
// CaptureConstant buys that back deliberately: see CaptureRate for what padding
// is worth paying for. Under it the buffer shrinks to a second, the window a
// ceiling that never moves is enforced over being the point.
func (e shareEncoder) rateControl(bitrate int, latency CaptureLatency, mode CaptureRate) []string {
	rate := fmt.Sprint(bitrate)
	constant := mode == CaptureConstant

	buf := fmt.Sprint(2 * bitrate)
	if constant {
		buf = rate
	}

	// Where an encoder reads the target as well as the ceiling, VBR asks for
	// less than the cap — the gap is the room it spends only on motion.
	target := rate
	if !constant {
		target = fmt.Sprint(bitrate * 4 / 5)
	}

	switch e.name {
	case "av1_nvenc", "h264_nvenc":
		rc := "vbr"
		if constant {
			rc = "cbr"
		}

		return []string{"-rc", rc, "-b:v", rate, "-maxrate", rate, "-bufsize", buf}
	case "av1_amf", "h264_amf":
		// Latency-constrained where the share is meant to be live, peak
		// where it is allowed to read ahead — and neither where the rate is
		// not allowed to move at all.
		rc := "vbr_latency"
		switch {
		case constant:
			rc = "cbr"
		case latency == CaptureBuffered:
			rc = "vbr_peak"
		}

		return []string{"-rc", rc, "-b:v", rate, "-maxrate", rate, "-bufsize", buf}
	case "av1_qsv", "h264_qsv":
		// QSV has no -rc: it reads the *inequality*, taking CBR where the
		// target and the ceiling agree and VBR where they do not.
		return []string{"-b:v", target, "-maxrate", rate, "-bufsize", buf}
	case "av1_vaapi", "h264_vaapi":
		rc := "VBR"
		if constant {
			rc = "CBR" // the driver wants the two equal, which target already is
		}

		return []string{"-rc_mode", rc, "-b:v", target, "-maxrate", rate, "-bufsize", buf}
	}

	// libx264 pads only under nal-hrd=cbr, which args carries — it shares a
	// parameter string with the latency tune and ffmpeg does not merge two.
	return []string{"-b:v", rate, "-maxrate", rate, "-bufsize", buf}
}

// encoderCandidates is one family's probe order, hardware most-capable
// first. The AV1 list is hardware **only** — see the header — where H.264
// ends at libx264, which every build carries. AMF is Windows-only, VAAPI
// Linux-only; NVENC and QSV exist on both but QSV off Windows needs a stack
// most machines lack, so it sits behind VAAPI there.
func encoderCandidates(family CaptureCodec) []shareEncoder {
	vaapiPre := []string{"-init_hw_device", "vaapi=va", "-filter_hw_device", "va"}

	// Deliberately no -init_hw_device cuda on av1_nvenc, though it has been
	// seen answering a failed AV1 capability query: a bare -init_hw_device
	// becomes every filter's default device, and ddagrab refuses anything
	// that is not D3D11 — the flag fixed a probe and broke every monitor
	// share behind it, which the lavfi probe cannot see.
	if family == CaptureCodecAuto { // the AV1 family; H264 is the other
		switch runtime.GOOS {
		case "windows":
			return []shareEncoder{{name: "av1_nvenc"}, {name: "av1_amf"}, {name: "av1_qsv"}}
		case "linux":
			return []shareEncoder{{name: "av1_nvenc"},
				{name: "av1_vaapi", pre: vaapiPre, tail: ",format=nv12,hwupload"},
				{name: "av1_qsv"}}
		}

		return nil
	}

	switch runtime.GOOS {
	case "windows":
		return []shareEncoder{{name: "h264_nvenc"}, {name: "h264_amf"},
			{name: "h264_qsv"}, {name: "libx264"}}
	case "linux":
		return []shareEncoder{{name: "h264_nvenc"},
			{name: "h264_vaapi", pre: vaapiPre, tail: ",format=nv12,hwupload"},
			{name: "h264_qsv"}, {name: "libx264"}}
	}

	return []shareEncoder{{name: "libx264"}}
}

// encProbes remembers what one run answered, per family — the ddagrab
// arrangement, for the same reason: an encoder the drivers cannot back has to
// be found out on a worker before a share starts, not at the first frame of a
// live track, and the answer does not change while the client runs. Lazy per
// family, so forcing H.264 never pays for the AV1 probes.
var (
	encMu     sync.Mutex
	encProbed [2]bool
	encFound  [2]shareEncoder
	encOK     [2]bool
)

// encProbeTimeout bounds one candidate's test encode: a driver that neither
// answers nor fails must not be what a share waits on.
const encProbeTimeout = 8 * time.Second

// shareEncoder answers how a share here is encoded: the AV1 family first
// under Auto, H.264 behind it. Not ok means nothing on this machine encodes
// even H.264 — an ffmpeg build with no libx264 — and sharing is refused with
// a sentence.
func (t Tools) shareEncoder(codec CaptureCodec) (shareEncoder, bool) {
	encMu.Lock()
	defer encMu.Unlock()

	if codec == CaptureCodecAuto {
		if enc, ok := t.familyEncoder(CaptureCodecAuto); ok {
			return enc, true
		}
	}

	return t.familyEncoder(CaptureCodecH264)
}

// familyEncoder probes one family's candidates in order on first ask.
// Callers hold encMu.
func (t Tools) familyEncoder(family CaptureCodec) (shareEncoder, bool) {
	if encProbed[family] {
		return encFound[family], encOK[family]
	}
	encProbed[family] = true
	for _, enc := range encoderCandidates(family) {
		if !encoderWorks(t.FFmpeg, enc) {
			continue
		}
		encFound[family], encOK[family] = enc, true
		where := "CPU"
		if enc.hardware() {
			where = "GPU"
		}
		log.Printf("video: shares encode as %s on the %s", enc.name, where)

		return enc, true
	}
	if family == CaptureCodecAuto {
		log.Printf("video: no hardware AV1 encoder answered; shares fall back to H.264")
	} else {
		log.Printf("video: no H.264 encoder works here; sharing needs an ffmpeg that carries one")
	}

	return shareEncoder{}, false
}

// ShareEncoding is how a share here would be encoded: the -c:v name, and
// whether the bytes are AV1 in IVF rather than H.264 Annex-B — which every
// consumer of the stream keys off.
type ShareEncoding struct {
	Name string
	AV1  bool
}

// ShareEncoder names how a share at this codec preference would be encoded,
// not ok for a machine with no encoder at all. Asking is what runs the
// probes, so it belongs on the worker the source enumeration is already on —
// by the time a share starts, the answer is in hand.
func (t Tools) ShareEncoder(codec CaptureCodec) (ShareEncoding, bool) {
	enc, ok := t.shareEncoder(codec)
	if !ok {
		return ShareEncoding{}, false
	}

	return ShareEncoding{Name: enc.name, AV1: enc.av1()}, true
}

// encoderWorks encodes a few synthetic frames to nowhere with the exact
// flags a share would use, so "listed but the driver refuses" and "an old
// build without the preset" both fail here rather than live.
func encoderWorks(tool string, enc shareEncoder) bool {
	ctx, cancel := context.WithTimeout(context.Background(), encProbeTimeout)
	defer cancel()

	args := []string{"-v", "error", "-nostdin"}
	args = append(args, enc.pre...)
	args = append(args, "-f", "lavfi", "-i", "color=c=black:s=640x360:r=30")
	if enc.tail != "" {
		args = append(args, "-vf", strings.TrimPrefix(enc.tail, ","))
	}
	// Probed at the lowest latency and the variable rate, which are the
	// defaults; the buffered flags are older than the low-latency ones on
	// every encoder here, and CBR older than every VBR spelling, so a machine
	// that passes covers all four. The rate control goes in too — a driver
	// that refuses the VBR mode this would run at has to fail here, not at
	// the first frame of a live track.
	args = append(args, enc.args(CaptureBalanced, CaptureLowestLatency, CaptureVariable, 30)...)
	args = append(args, enc.rateControl(1_000_000, CaptureLowestLatency, CaptureVariable)...)
	args = append(args, "-bf", "0", "-frames:v", "3", "-f", "null", "-")

	cmd := exec.CommandContext(ctx, tool, args...)
	captureAttrs(cmd)

	return cmd.Run() == nil
}

// grab is how one platform gets at pixels. Most grabbers are input devices
// and answer with args alone — everything up to and including the `-i`. Some
// are *filter sources* instead (Windows' gfxcapture and ddagrab), which have
// no input at all: those answer with source, the filter the chain has to begin
// with, and the args carry only what setting it up needs.
type grab struct {
	args   []string
	source string
}

// CaptureShare starts the one child a running share is: grab, scale, encode,
// the track's bytes on stdout. The caller reads it until the share ends and Stops the
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

	enc, ok := t.shareEncoder(cfg.Codec)
	if !ok {
		return nil, fmt.Errorf("video: nothing here encodes a share")
	}

	g, err := grabArgs(t.FFmpeg, cfg)
	if err != nil {
		return nil, err
	}

	bitrate := min(max(cfg.Bitrate, captureMinBitrate), captureMaxBitrate)
	keyint := cfg.KeyframeSeconds
	if keyint == 0 {
		keyint = 2
	}
	keyint = min(max(keyint, 1), 10)

	// The pad half of the scale keeps the declared size true through a
	// window resizing mid-share. No fps filter: the stream is held to a
	// constant rate by the output's own sync instead (below), which fills
	// and drops on the frame in hand where the filter waits for the one after
	// it to choose between them — a whole frame of latency on every share,
	// two hundred milliseconds at 5 fps.
	chain := liveScaleFilter(cfg.Width, cfg.Height) + enc.tail

	// The container follows the codec, and both carry lengths: AV1 in IVF,
	// H.264 in FLV — never bare Annex-B, which would leave the tee to find
	// the end of each frame at the start of the next. FLV stripped of the
	// metadata tag and the sizes it would seek back to write, the output
	// being a pipe.
	format, container := "flv", []string{"-flvflags", "no_metadata+no_duration_filesize+no_sequence_end"}
	if enc.av1() {
		format, container = "ivf", nil
	}

	args := []string{"-v", "error", "-nostdin"}
	args = append(args, enc.pre...)
	args = append(args, g.args...)
	args = append(args, "-an", "-sn", "-dn")
	if g.source == "" {
		args = append(args, "-vf", chain)
	} else {
		// A filter source has no input to hang -vf off; the graph is the
		// whole of it and ffmpeg maps its one output on its own.
		args = append(args, "-filter_complex", g.source+","+chain)
	}
	// Constant rate at the output: a grabber answers only when the screen
	// changes (Graphics Capture idles at about three frames a second on a
	// still window), and the publisher stamps every frame with the asked-for
	// step, so the stream has to be filled to that rate for the timestamps
	// to be honest and the keyframe interval, counted in frames, to be the
	// seconds it was set from. Under capped VBR a repeated frame costs
	// almost nothing to send.
	args = append(args, "-fps_mode", "cfr", "-r", fmt.Sprint(cfg.FPS))
	args = append(args, enc.args(cfg.Speed, cfg.Latency, cfg.Rate, cfg.FPS)...)
	args = append(args, enc.rateControl(bitrate, cfg.Latency, cfg.Rate)...)
	args = append(args,
		// No B-frames, doubly: baseline forbids them, and each frame must be
		// one sample in publish order.
		"-g", fmt.Sprint(keyint*cfg.FPS), "-bf", "0",
	)
	// Only the processor encode has threads to size; every hardware encoder
	// ignores the flag, and a number there reads as if it did something.
	if !enc.hardware() {
		args = append(args, "-threads", captureThreads(cfg.Width, cfg.Height))
	}
	args = append(args, container...)
	args = append(args, "-f", format, "pipe:1")

	return captureLaunch(t.FFmpeg, args)
}

// CaptureFallback reports whether sharing a screen here will go through a
// path slower than the platform's own — today only Windows has one to fall
// back to, when neither Graphics Capture nor Desktop Duplication answers and
// GDI's BitBlt has to do the copying. It answers about the *set* because that
// is what a picker warns about, and asking is what runs the probes behind it,
// so it belongs on the worker the enumeration is already on.
func (t Tools) CaptureFallback(sources []CaptureSource) bool {
	return captureFallback(t.FFmpeg, sources)
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

	// The pipe is this side's rather than exec's, as in launch: exec's Wait
	// closes a StdoutPipe under whatever is still reading it, and reap closes
	// out itself once the Wait is done. At the platform's own size, not
	// pipeBytes — this stream is live and a few Mbps, so a big buffer could
	// only hold latency, the LiveFrames argument from the other direction.
	out, write, err := sizedPipe(0)
	if err != nil {
		return nil, fmt.Errorf("video: %w", err)
	}
	cmd.Stdout = write

	if err := cmd.Start(); err != nil {
		write.Close()
		out.Close()
		return nil, fmt.Errorf("video: %w", err)
	}
	write.Close()

	release := hardenCapture(cmd)

	return &Stream{cmd: cmd, out: out, stderr: stderr, release: release, done: make(chan struct{})}, nil
}

/* Teeing the stream */

// ShareTee frames a capture child's stream for its two consumers. The
// primary is the publisher: ReadFrame answers one whole frame at a time — an
// H.264 access unit in Annex-B, or an AV1 temporal unit with the IVF framing
// stripped, which are exactly the two sample shapes lksdk packetises — and
// blocks only on the child itself, so the pipe is drained at the rate frames
// are produced and nothing between the screen and the room can hold a
// backlog. The secondary is the local preview, the only place a share's
// bytes exist twice — a LiveKit room never sends a publisher their own track
// back — which is offered a copy of each frame as it completes.
//
// The preview's copy is lossy on purpose, and that is the whole design. The
// publisher must never wait on the preview: a decoder stalled behind a
// blocked UI thread — a window being dragged is enough on Windows — would
// otherwise stall the share everybody else is watching. So a frame the
// preview is too far behind to take is dropped rather than queued. Dropping
// costs nothing structural either way: an access unit opens with its own
// start code and an IVF frame with its own length, so what a gap breaks is
// the decoder's prediction until the next keyframe, never the framing.
type ShareTee struct {
	src io.ReadCloser

	// What ReadFrame works from: the chunk buffer the child is read into,
	// frames completed and not yet taken, and the error held back until the
	// queue is empty — bytes already framed are owed before the EOF behind
	// them.
	rbuf    []byte
	queue   [][]byte
	readErr error

	// Both containers are walked section by section — which one is being
	// walked and what it is still owed. head assembles the one file header,
	// fh the current frame's (IVF) or tag's (FLV) header.
	ivf       bool // AV1 in IVF; H.264 in FLV otherwise
	phase     int
	head      []byte
	fh        [12]byte
	fhn       int // how much of fh has arrived
	remaining int // body bytes still owed to the current frame or tag

	// The FLV side (H.264): the tag being assembled and what its NAL units
	// are prefixed with, which the sequence header says.
	tagType byte
	tag     []byte
	nalLen  int
	hasIDR  bool // the access unit in hand carries an IDR slice

	unit   []byte // the frame being assembled
	broken bool   // not the stream this was promised; stop parsing

	// mu guards the attachment, which is claimed from the worker launching a
	// preview (app.startSelfPreview), released on the UI thread, and fed from
	// whichever goroutine is draining the child.
	mu         sync.Mutex
	sps        []byte // the latest parameter sets, replayed to a preview that
	pps        []byte // attaches after the stream's own copies went past
	fileHeader []byte // IVF's equivalent: the 32-byte header, replayed likewise
	sentHeader bool   // this attachment has been given the file header
	previewPTS uint64 // H.264: the frame count, stamped on the preview's IVF frames
	frames     chan []byte
	started    bool // whether this attachment has been given a frame to start on
	closed     bool // the tee is finished; a late attachment gets nothing
}

const (
	// shareTeeQueue is how many frames a preview may fall behind by before
	// one is dropped. Deep enough to ride out a repaint, shallow enough that
	// what it eventually draws is now rather than a second ago.
	shareTeeQueue = 8

	// shareTeeMaxFrame is a sanity bound on a frame or tag: past it the
	// bytes are not the stream this side asked for, and the parser stops
	// rather than growing without limit on a misread.
	shareTeeMaxFrame = 16 << 20

	// shareTeeReadChunk is how much of the child's stream one read asks for.
	shareTeeReadChunk = 64 << 10
)

// errShareStream is a capture child writing something other than the stream
// the tee was promised — the parse cannot continue, so neither can the share.
var errShareStream = errors.New("video: the capture stream is not the format it was started to write")

// NewShareTee wraps a capture stream — AV1 in IVF where av1, H.264 in FLV
// otherwise, which must match what the capture child was started to write.
// width and height are the encode box, which an H.264 preview's file header
// declares (AV1's is the encoder's own, replayed). Closing the tee is closing
// what it wraps.
func NewShareTee(src io.ReadCloser, av1 bool, width, height int) *ShareTee {
	t := &ShareTee{src: src, ivf: av1}
	if !av1 {
		t.fileHeader = newIVFFileHeader("H264", width, height)
	}

	return t
}

// newIVFFileHeader is IVF's 32-byte file header: the fourcc, the box, and a
// 1/90000 timebase, the RTP clock the preview's frames are stamped in. A
// copy of voice.ivfMux.writeHeader by construction — voice imports only
// domain — so a fix to either must be carried to the other.
func newIVFFileHeader(fourCC string, width, height int) []byte {
	h := make([]byte, ivfHeaderLen)
	copy(h[0:], "DKIF")
	binary.LittleEndian.PutUint16(h[6:], ivfHeaderLen)
	copy(h[8:], fourCC)
	binary.LittleEndian.PutUint16(h[12:], uint16(min(max(width, 0), 0xFFFF)))
	binary.LittleEndian.PutUint16(h[14:], uint16(min(max(height, 0), 0xFFFF)))
	binary.LittleEndian.PutUint32(h[16:], 90000)
	binary.LittleEndian.PutUint32(h[20:], 1)

	return h
}

// ivfFrame is one IVF frame: the 12-byte header — length, then the pts —
// ahead of the body. What a preview reads its H.264 as, so the demuxer has
// the length of every frame and closes it the moment it lands rather than at
// the next one's start code.
func ivfFrame(pts uint64, body []byte) []byte {
	framed := make([]byte, 12, 12+len(body))
	binary.LittleEndian.PutUint32(framed[0:], uint32(len(body)))
	binary.LittleEndian.PutUint64(framed[4:], pts)

	return append(framed, body...)
}

// ReadFrame hands the publisher the next whole frame, blocking on the child
// while none is complete. One caller — the publisher's write loop owns the
// parse the way lksdk's reader goroutine used to own Read. The error is the
// stream ending, or a stream that stopped being the format promised.
func (t *ShareTee) ReadFrame() ([]byte, error) {
	for len(t.queue) == 0 {
		if t.broken {
			return nil, errShareStream
		}
		if t.readErr != nil {
			return nil, t.readErr
		}
		if t.rbuf == nil {
			t.rbuf = make([]byte, shareTeeReadChunk)
		}
		n, err := t.src.Read(t.rbuf)
		if n > 0 && !t.broken {
			t.consume(t.rbuf[:n])
		}
		if err != nil {
			t.readErr = err
		}
	}

	frame := t.queue[0]
	n := copy(t.queue, t.queue[1:])
	t.queue[n] = nil
	t.queue = t.queue[:n]

	return frame, nil
}

// Close detaches any preview and kills what the tee wraps. Nothing attaches
// after it: a preview whose launch raced the share's own end would otherwise
// wait on frames that can no longer come.
func (t *ShareTee) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()

	t.Detach()

	return t.src.Close()
}

// Attach starts copying frames to out, what the demuxer needs to enter the
// stream first: the IVF file header, without which the bytes are not a
// stream at all — the encoder's own for AV1, one written here for H.264 —
// and for H.264 the latest SPS and PPS as a frame of their own (the encoders
// here repeat them ahead of every keyframe anyway, and a duplicate costs a
// decoder nothing). One attachment at a time; a second replaces the first.
// out is closed by Detach, by the stream ending, or by a write failing —
// never by the caller.
func (t *ShareTee) Attach(out io.WriteCloser) {
	t.Detach()

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = out.Close()

		return
	}
	frames := make(chan []byte, shareTeeQueue)
	t.frames, t.started = frames, false
	t.sentHeader = t.fileHeader != nil
	if t.sentHeader {
		frames <- slices.Clone(t.fileHeader)
	}
	if !t.ivf && (t.sps != nil || t.pps != nil) {
		frames <- ivfFrame(t.previewPTS, slices.Concat(t.sps, t.pps))
	}
	t.mu.Unlock()

	go func() {
		defer func() { _ = out.Close() }()

		for frame := range frames {
			if _, err := out.Write(frame); err != nil {
				return
			}
		}
	}()
}

// Detach ends the copy, which closes the writer under it — the preview's
// decoder reads that as the stream ending. Safe with nothing attached.
func (t *ShareTee) Detach() {
	t.mu.Lock()
	frames := t.frames
	t.frames = nil
	t.mu.Unlock()

	if frames != nil {
		close(frames)
	}
}

// consume walks the bytes that just crossed, assembling every frame whole:
// the publisher takes each one through ReadFrame, and the preview is offered
// a copy as it completes.
func (t *ShareTee) consume(b []byte) {
	if t.ivf {
		t.consumeIVF(b)
		return
	}

	t.consumeFLV(b)
}

// consumeAnnexB is H.264's walk. Annex-B is NALs behind start codes, an
// access unit ending at its one slice — the encoder's side of the contract —
// so a unit is complete when the start code after a slice arrives, which is
// why both consumers run one frame interval behind the encoder's bytes.
/* FLV (H.264) */

const (
	flvFileHeaderLen = 13 // the 9-byte header and the first "previous tag size"
	flvTagHeaderLen  = 11
	flvPrevSizeLen   = 4

	flvTagVideo  = 9
	flvCodecAVC  = 7
	flvAVCConfig = 0 // the tag body is an AVCDecoderConfigurationRecord
	flvAVCNALUs  = 1 // the tag body is one access unit, its NAL units length-prefixed
)

// The sections an FLV stream alternates between after its file header.
const (
	flvFileHeader = iota
	flvTagHeader
	flvTagBody
	flvPrevSize
)

// consumeFLV is H.264's walk, and it is the IVF one again with a tag in
// place of the 12-byte frame header: the container carries every tag's
// length, so an access unit is complete the moment its bytes have crossed.
// Bare Annex-B was here before it, and with no lengths to hop by it could
// only close a unit at the start code opening the next — a frame of latency
// on every frame, for both the room and the preview. The muxer converts the
// encoder's start codes to length prefixes on the way in, and the parameter
// sets arrive once, in a sequence header tag, rather than ahead of every
// keyframe; the publisher's frame is the unit back in Annex-B with the sets
// put in front of each IDR, which is what the packetiser and every decoder
// expect.
func (t *ShareTee) consumeFLV(b []byte) {
	for len(b) > 0 && !t.broken {
		switch t.phase {
		case flvFileHeader:
			n := min(len(b), flvFileHeaderLen-len(t.head))
			t.head = append(t.head, b[:n]...)
			b = b[n:]
			if len(t.head) < flvFileHeaderLen {
				return
			}
			if string(t.head[:3]) != "FLV" || t.head[3] != 1 ||
				binary.BigEndian.Uint32(t.head[5:9]) != 9 {
				t.breakOff()
				return
			}
			t.phase = flvTagHeader
		case flvTagHeader:
			n := min(len(b), flvTagHeaderLen-t.fhn)
			copy(t.fh[t.fhn:], b[:n])
			t.fhn += n
			b = b[n:]
			if t.fhn < flvTagHeaderLen {
				return
			}
			t.tagType = t.fh[0]
			size := int(t.fh[1])<<16 | int(t.fh[2])<<8 | int(t.fh[3])
			if size > shareTeeMaxFrame {
				t.breakOff()
				return
			}
			t.remaining = size
			t.fhn = 0
			t.phase = flvTagBody
			if size == 0 {
				t.phase = flvPrevSize
			}
		case flvTagBody:
			n := min(len(b), t.remaining)
			t.tag = append(t.tag, b[:n]...)
			t.remaining -= n
			b = b[n:]
			if t.remaining > 0 {
				return
			}
			if t.tagType == flvTagVideo {
				t.videoTag(t.tag)
			}
			t.tag = t.tag[:0]
			t.phase = flvPrevSize
		case flvPrevSize:
			n := min(len(b), flvPrevSizeLen-t.fhn)
			t.fhn += n
			b = b[n:]
			if t.fhn < flvPrevSizeLen {
				return
			}
			t.fhn = 0
			t.phase = flvTagHeader
		}
	}
}

// videoTag is one video tag: the sequence header, which carries the
// parameter sets, or one access unit.
func (t *ShareTee) videoTag(body []byte) {
	if len(body) < 5 {
		return
	}
	if body[0]&0x0F != flvCodecAVC {
		t.breakOff()
		return
	}

	switch body[1] {
	case flvAVCConfig:
		t.readAVCC(body[5:])
	case flvAVCNALUs:
		t.readNALUs(body[5:])
	}
}

// readAVCC takes the parameter sets out of the decoder configuration record
// and the width of the length every NAL unit is prefixed with.
func (t *ShareTee) readAVCC(c []byte) {
	if len(c) < 7 || c[0] != 1 {
		t.breakOff()
		return
	}
	t.nalLen = int(c[4]&3) + 1

	var sps, pps []byte
	i := 6
	for k, count := 0, int(c[5]&0x1F); k < count && i+2 <= len(c); k++ {
		l := int(binary.BigEndian.Uint16(c[i:]))
		i += 2
		if i+l > len(c) {
			break
		}
		sps = append([]byte{0, 0, 0, 1}, c[i:i+l]...)
		i += l
	}
	if i < len(c) {
		for k, count := 0, int(c[i]); k < count; k++ {
			i++
			if i+2 > len(c) {
				break
			}
			l := int(binary.BigEndian.Uint16(c[i:]))
			i += 2
			if i+l > len(c) {
				break
			}
			pps = append([]byte{0, 0, 0, 1}, c[i:i+l]...)
			i += l - 1
		}
	}

	t.storePS(sps, pps)
}

// readNALUs is one access unit: its length-prefixed NAL units back in
// Annex-B, the parameter sets put in front where it carries an IDR and none
// of its own — the encoder, told to keep them for the container's header,
// stops repeating them.
func (t *ShareTee) readNALUs(d []byte) {
	nalLen := t.nalLen
	if nalLen == 0 {
		nalLen = 4
	}

	unit := make([]byte, 0, len(d)+16)
	hasIDR, inlinePS := false, false
	for i := 0; i+nalLen <= len(d); {
		l := 0
		for _, c := range d[i : i+nalLen] {
			l = l<<8 | int(c)
		}
		i += nalLen
		if l <= 0 || i+l > len(d) {
			t.breakOff()
			return
		}
		nal := d[i : i+l]
		i += l

		switch nal[0] & 0x1F {
		case 7:
			inlinePS = true
			t.storePS(append([]byte{0, 0, 0, 1}, nal...), nil)
		case 8:
			t.storePS(nil, append([]byte{0, 0, 0, 1}, nal...))
		case 5:
			hasIDR = true
		}
		unit = append(unit, 0, 0, 0, 1)
		unit = append(unit, nal...)
	}
	if len(unit) == 0 {
		return
	}
	if hasIDR && !inlinePS && t.sps != nil {
		unit = slices.Concat(t.sps, t.pps, unit)
	}

	t.unit, t.hasIDR = unit, hasIDR
	t.emitUnit()
	t.queue = append(t.queue, t.unit)
	t.unit, t.hasIDR = nil, false
}

// storePS files the parameter sets for Attach to replay, either alone. Under
// mu because Attach reads both from the UI thread.
func (t *ShareTee) storePS(sps, pps []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if sps != nil {
		t.sps = sps
	}
	if pps != nil {
		t.pps = pps
	}
}

// emitUnit offers the assembled access unit to the preview, framed as an
// IVF frame and gated on an IDR until one has started it — a decoder handed
// a stream that opens mid-GOP answers with a run of complaints and no
// picture. The pts only counts: the preview reads what arrives when it
// arrives, so the demuxer's clock is never consulted.
func (t *ShareTee) emitUnit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.previewPTS++
	if t.frames == nil {
		return
	}
	if !t.started {
		if !t.hasIDR {
			return
		}
		t.started = true
	}

	t.offer(ivfFrame(t.previewPTS, t.unit))
}

// offer hands the preview a frame of its own, or drops it where the preview
// is behind — see the type's own comment for why that is the point. Callers
// hold mu, which is what keeps the send from racing Detach's close and is
// also what makes the length check sound: one sender, so room seen is room
// still there.
func (t *ShareTee) offer(frame []byte) {
	if t.frames == nil || len(t.frames) == cap(t.frames) {
		return
	}

	t.frames <- frame
}

// ivfHeaderLen is the one size IVF's file header comes in; the frame headers
// behind it are twelve bytes each.
const ivfHeaderLen = 32

// The three sections an IVF stream alternates between after its file header.
const (
	ivfFileHeader = iota
	ivfFrameHeader
	ivfFrameBody
)

// consumeIVF is AV1's walk, and it is the cheaper of the two: IVF frames its
// stream with lengths, so parsing is hopping section to section rather than
// looking at every byte. A frame is one temporal unit, complete when its
// declared bytes have crossed; the publisher's frame is the body alone — the
// track wants the OBUs, not the container — where the preview's carries the
// 12-byte header its demuxer walks by.
func (t *ShareTee) consumeIVF(b []byte) {
	for len(b) > 0 && !t.broken {
		switch t.phase {
		case ivfFileHeader:
			n := min(len(b), ivfHeaderLen-len(t.head))
			t.head = append(t.head, b[:n]...)
			b = b[n:]
			if len(t.head) < ivfHeaderLen {
				return
			}
			if string(t.head[:4]) != "DKIF" ||
				int(binary.LittleEndian.Uint16(t.head[6:8])) != ivfHeaderLen {
				t.breakOff()
				return
			}
			t.storeFileHeader()
			t.phase = ivfFrameHeader
		case ivfFrameHeader:
			n := min(len(b), len(t.fh)-t.fhn)
			copy(t.fh[t.fhn:], b[:n])
			t.fhn += n
			b = b[n:]
			if t.fhn < len(t.fh) {
				return
			}
			size := int(binary.LittleEndian.Uint32(t.fh[:4]))
			if size <= 0 || size > shareTeeMaxFrame {
				t.breakOff()
				return
			}
			t.remaining = size
			t.phase = ivfFrameBody
		case ivfFrameBody:
			n := min(len(b), t.remaining)
			t.unit = append(t.unit, b[:n]...)
			t.remaining -= n
			b = b[n:]
			if t.remaining > 0 {
				return
			}
			t.emitIVFUnit()
			t.queue = append(t.queue, t.unit)
			t.unit = nil
			t.fhn = 0
			t.phase = ivfFrameHeader
		}
	}
}

// breakOff is a stream that is not what the tee was promised: parsing stops
// rather than growing without limit on a misread, and the preview is let go.
func (t *ShareTee) breakOff() {
	t.broken = true
	t.Detach()
}

// storeFileHeader files the parsed file header for Attach to replay. Under mu
// because Attach reads it from the UI thread.
func (t *ShareTee) storeFileHeader() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.fileHeader = slices.Clone(t.head)
}

// emitIVFUnit offers the assembled frame to the preview, gated on a keyframe
// until one has started it — recognised by the sequence header the encoders
// here write ahead of every one, which is also the OBU the decoder cannot
// enter the stream without.
func (t *ShareTee) emitIVFUnit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.frames == nil {
		return
	}
	if !t.started {
		if !av1HasSequenceHeader(t.unit) {
			return
		}
		// An attachment made before the stream's own file header had passed
		// is given it here instead; nothing precedes the first frame, so the
		// queue cannot be full.
		if !t.sentHeader {
			if t.fileHeader == nil || len(t.frames) == cap(t.frames) {
				return
			}
			t.frames <- slices.Clone(t.fileHeader)
			t.sentHeader = true
		}
		t.started = true
	}

	framed := make([]byte, 0, len(t.fh)+len(t.unit))
	framed = append(framed, t.fh[:]...)
	t.offer(append(framed, t.unit...))
}

// av1HasSequenceHeader hops a temporal unit's OBUs — every one the encoders
// here write carries a size field — asking for a sequence header, which is
// what marks the frame a decoder can enter at.
//
// A copy of voice.av1SequenceHeaderIn by construction: voice imports only
// domain (the rvoice seam), so the walk cannot live in one place. A fix to
// either must be carried to the other.
func av1HasSequenceHeader(unit []byte) bool {
	for i := 0; i < len(unit); {
		header := unit[i]
		if header&0x80 != 0 {
			return false // the forbidden bit; this is not an OBU
		}
		if (header>>3)&0xF == 1 {
			return true
		}
		i++
		if header&0x04 != 0 {
			i++ // the extension byte
		}
		if header&0x02 == 0 {
			return false // no size field, so nothing to hop by
		}

		size, shift := 0, 0
		for {
			if i >= len(unit) || shift > 28 {
				return false
			}
			c := unit[i]
			i++
			size |= int(c&0x7F) << shift
			shift += 7
			if c&0x80 == 0 {
				break
			}
		}
		i += size
	}

	return false
}
