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
// decision are not spelled in different places.
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
func (e shareEncoder) args(speed CaptureSpeed, latency CaptureLatency, fps int) []string {
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
	if buffered {
		return args // the preset's own rc-lookahead and mb-tree come back
	}

	return append(args, "-tune", "zerolatency", "-x264-params", "sliced-threads=0")
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
// ceiling and the buffer. It is capped VBR on every encoder that has one, and
// that is the whole point — a screen is idle most of the time, and CBR pays
// the target rate for a still picture, padding the difference with filler.
// Measured on this machine at 1080p30 with a 6.2 Mbps target: 5.97 Mbps for a
// static screen under CBR against 0.05 under VBR, and an identical 6.35 for
// content that genuinely needs it. The ceiling is what a slow uplink is
// protected by; the average is what an idle one actually spends.
//
// libx264 is already this without being asked — it only pads when told to
// (nal-hrd=cbr), which nothing here says.
func (e shareEncoder) rateControl(bitrate int, latency CaptureLatency) []string {
	rate := fmt.Sprint(bitrate)
	buf := fmt.Sprint(2 * bitrate)

	switch e.name {
	case "av1_nvenc", "h264_nvenc":
		return []string{"-rc", "vbr", "-b:v", rate, "-maxrate", rate, "-bufsize", buf}
	case "av1_amf", "h264_amf":
		// Latency-constrained where the share is meant to be live, peak
		// where it is allowed to read ahead.
		mode := "vbr_latency"
		if latency == CaptureBuffered {
			mode = "vbr_peak"
		}

		return []string{"-rc", mode, "-b:v", rate, "-maxrate", rate, "-bufsize", buf}
	case "av1_qsv", "h264_qsv":
		// QSV has no -rc: it reads the *inequality*, taking CBR where the
		// target and the ceiling agree and VBR where they do not. So the
		// target is set below the ceiling rather than at it.
		return []string{"-b:v", fmt.Sprint(bitrate * 4 / 5), "-maxrate", rate, "-bufsize", buf}
	case "av1_vaapi", "h264_vaapi":
		return []string{"-rc_mode", "VBR",
			"-b:v", fmt.Sprint(bitrate * 4 / 5), "-maxrate", rate, "-bufsize", buf}
	}

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
	// Probed at the lowest latency, which is the default; the buffered
	// flags are older than the low-latency ones on every encoder here, so a
	// machine that passes covers both. The rate control goes in too — a
	// driver that refuses the VBR mode this would run at has to fail here,
	// not at the first frame of a live track.
	args = append(args, enc.args(CaptureBalanced, CaptureLowestLatency, 30)...)
	args = append(args, enc.rateControl(1_000_000, CaptureLowestLatency)...)
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

	// The fps filter makes the stream honestly constant-rate, which is the
	// clock the publisher paces the track by — Annex-B carries no timing at
	// all, and the IVF timestamps are overridden the same way. The pad half
	// of the scale keeps the declared size true through a window resizing
	// mid-share.
	chain := fmt.Sprintf("fps=%d,%s%s", cfg.FPS, liveScaleFilter(cfg.Width, cfg.Height), enc.tail)

	// The container follows the codec: lksdk eats AV1 only from IVF and
	// H.264 only as bare Annex-B.
	format := "h264"
	if enc.av1() {
		format = "ivf"
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
	args = append(args, enc.args(cfg.Speed, cfg.Latency, cfg.FPS)...)
	args = append(args, enc.rateControl(bitrate, cfg.Latency)...)
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

// ShareTee splits a capture child's stream in two: every byte still reaches
// the reader publishing it, and a copy of each whole frame reaches whatever
// is watching this end locally. It is the only place a share's bytes exist
// twice — a LiveKit room never sends a publisher their own track back — so it
// is what a self-preview is made of. It parses whichever of the two streams a
// share here can be: H.264 Annex-B by scanning for start codes, AV1's IVF by
// hopping the length-framed sections.
//
// The copy is lossy on purpose, and that is the whole design. The
// publisher's Read must never wait on the preview: a decoder stalled behind
// a blocked UI thread — a window being dragged is enough on Windows — would
// otherwise stall the share everybody else is watching. So a frame the
// preview is too far behind to take is dropped rather than queued. Dropping
// costs nothing structural either way: an access unit opens with its own
// start code and an IVF frame with its own length, so what a gap breaks is
// the decoder's prediction until the next keyframe, never the framing.
type ShareTee struct {
	src io.ReadCloser

	// The Annex-B side of the parser. It runs whether or not anybody is
	// watching — Annex-B has no lengths to hop by, so framing is a scan for
	// start codes — but bytes are only ever *copied* for an attached
	// preview, or to keep the latest parameter sets for one that attaches
	// later.
	zeros   int    // run of 0x00 bytes ending what has been seen so far
	typed   bool   // a start code just closed; the next byte is a NAL header
	curType int    // the NAL being walked, 0 before the first
	copying bool   // that NAL's bytes are being kept in nal
	nal     []byte // the NAL being assembled, a canonical start code first
	inAU    bool   // between an AU's first NAL and its slice
	hasIDR  bool   // the AU carries an IDR slice

	// The IVF side (AV1): which of the three sections is being walked and
	// what it is still owed. head assembles the one file header.
	ivf       bool
	phase     int
	head      []byte
	fh        [12]byte // the current frame's 12-byte header
	fhn       int      // how much of it has arrived
	remaining int      // body bytes still owed to the current frame

	unit   []byte // the frame being assembled for the preview
	keep   bool   // this frame is being copied, decided as it opens
	broken bool   // not the stream this was promised; stop parsing

	// mu guards the attachment, which is claimed from the worker launching a
	// preview (app.startSelfPreview), released on the UI thread, and fed from
	// whichever goroutine is draining the child.
	mu         sync.Mutex
	sps        []byte // the latest parameter sets, replayed to a preview that
	pps        []byte // attaches after the stream's own copies went past
	fileHeader []byte // IVF's equivalent: the 32-byte header, replayed likewise
	sentHeader bool   // this attachment has been given the file header
	frames     chan []byte
	started    bool // whether this attachment has been given a frame to start on
	closed     bool // the tee is finished; a late attachment gets nothing
}

const (
	// shareTeeQueue is how many frames a preview may fall behind by before
	// one is dropped. Deep enough to ride out a repaint, shallow enough that
	// what it eventually draws is now rather than a second ago.
	shareTeeQueue = 8

	// shareTeeMaxFrame is a sanity bound on an assembled unit: past it the
	// bytes are not the Annex-B this side wrote, and the parser stops rather
	// than growing without limit on a misread.
	shareTeeMaxFrame = 16 << 20
)

// NewShareTee wraps a capture stream — AV1 in IVF where av1, H.264 Annex-B
// otherwise, which must match what the capture child was started to write.
// Reading and closing the tee is reading and closing what it wraps.
func NewShareTee(src io.ReadCloser, av1 bool) *ShareTee {
	return &ShareTee{src: src, ivf: av1}
}

// Read hands the publisher its bytes, keeping a copy of each whole access
// unit for the preview.
func (t *ShareTee) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if n > 0 && !t.broken {
		t.consume(p[:n])
	}

	return n, err
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
// stream first: Annex-B replays the latest SPS and PPS — the encoders here
// repeat them ahead of every keyframe anyway, and a duplicate costs a decoder
// nothing — and IVF replays the file header, without which the bytes are not
// a stream at all. One attachment at a time; a second replaces the first. out
// is closed by Detach, by the stream ending, or by a write failing — never by
// the caller.
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
	if t.ivf {
		t.sentHeader = t.fileHeader != nil
		if t.sentHeader {
			frames <- slices.Clone(t.fileHeader)
		}
	} else {
		if t.sps != nil {
			frames <- slices.Clone(t.sps)
		}
		if t.pps != nil {
			frames <- slices.Clone(t.pps)
		}
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

// consume walks the bytes that just crossed, assembling frames and emitting
// them whole. Whether a frame is copied at all is decided once, as it opens,
// and not revisited: with the preview closed the whole stream is scanned or
// hopped past and nothing is kept, which is all a share nobody is previewing
// pays.
func (t *ShareTee) consume(b []byte) {
	if t.ivf {
		t.consumeIVF(b)
		return
	}

	t.consumeAnnexB(b)
}

// consumeAnnexB is H.264's walk. Annex-B is NALs behind start codes, an
// access unit ending at its one slice — the encoder's side of the contract —
// so a unit is complete when the start code after a slice arrives, which is
// why the preview runs one frame interval behind the publisher's bytes.
func (t *ShareTee) consumeAnnexB(b []byte) {
	for _, c := range b {
		switch {
		case c == 0:
			t.zeros++
			if t.copying {
				t.nal = append(t.nal, 0)
			}
		case c == 1 && t.zeros >= 2:
			t.finishNAL()
		default:
			if t.typed {
				t.beginNAL(c)
			} else if t.copying {
				t.nal = append(t.nal, c)
			}
			t.zeros = 0
		}

		if len(t.nal) > shareTeeMaxFrame || len(t.unit) > shareTeeMaxFrame {
			t.broken = true
			t.Detach()

			return
		}
	}
}

// beginNAL reads the header byte a start code promised. An access unit's
// fate is decided here, at its first NAL: copied for an attached preview,
// or scanned past for nobody.
func (t *ShareTee) beginNAL(c byte) {
	t.typed = false
	t.curType = int(c & 0x1F)
	if !t.inAU {
		t.inAU = true
		t.keep = t.watched()
	}
	if t.curType == 5 {
		t.hasIDR = true
	}

	t.copying = t.keep || t.curType == 7 || t.curType == 8
	if t.copying {
		t.nal = append(t.nal[:0], 0, 0, 0, 1, c)
	}
}

// finishNAL is a start code closing: the NAL before it is complete, and the
// access unit is too if that NAL was its slice.
func (t *ShareTee) finishNAL() {
	if t.copying {
		// The zeros just counted open the next start code, or pad the byte
		// stream; either way they are not this NAL's payload.
		t.nal = t.nal[:len(t.nal)-min(t.zeros, len(t.nal))]
	}
	if t.copying && (t.curType == 7 || t.curType == 8) {
		t.storePS()
	}
	if t.keep && t.copying {
		t.unit = append(t.unit, t.nal...)
	}
	if t.curType >= 1 && t.curType <= 5 {
		if t.keep {
			t.emitUnit()
		}
		t.unit = t.unit[:0]
		t.inAU, t.hasIDR = false, false
	}

	t.typed = true
	t.curType, t.copying = 0, false
	t.zeros = 0
	t.nal = t.nal[:0]
}

// storePS files the completed parameter set for Attach to replay. Under mu
// because Attach reads both from the UI thread.
func (t *ShareTee) storePS() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.curType == 7 {
		t.sps = slices.Clone(t.nal)
	} else {
		t.pps = slices.Clone(t.nal)
	}
}

// watched reports whether anything is attached, asked once per access unit
// rather than once per byte.
func (t *ShareTee) watched() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.frames != nil
}

// emitUnit offers the assembled access unit to the preview, gated on an IDR
// until one has started it — a decoder handed a stream that opens mid-GOP
// answers with a run of complaints and no picture.
func (t *ShareTee) emitUnit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.frames == nil {
		return
	}
	if !t.started {
		if !t.hasIDR {
			return
		}
		t.started = true
	}

	t.offer(t.unit)
}

// offer copies what it is given to the preview, or drops it where the
// preview is behind — see the type's own comment for why that is the point.
// Callers hold mu, which is what keeps the send from racing Detach's close
// and is also what makes the length check sound: one sender, so room seen is
// room still there, and the copy is never made for a frame being dropped.
func (t *ShareTee) offer(unit []byte) {
	if t.frames == nil || len(t.frames) == cap(t.frames) {
		return
	}

	t.frames <- slices.Clone(unit)
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
// declared bytes have crossed.
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
			t.keep = t.watched()
			if t.keep {
				t.unit = append(t.unit[:0], t.fh[:]...)
			}
			t.phase = ivfFrameBody
		case ivfFrameBody:
			n := min(len(b), t.remaining)
			if t.keep {
				t.unit = append(t.unit, b[:n]...)
			}
			t.remaining -= n
			b = b[n:]
			if t.remaining > 0 {
				return
			}
			if t.keep {
				t.emitIVFUnit()
				t.unit = t.unit[:0]
			}
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
		if !av1HasSequenceHeader(t.unit[len(t.fh):]) {
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

	t.offer(t.unit)
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
