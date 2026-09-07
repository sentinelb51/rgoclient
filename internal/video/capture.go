package video

// Sending a screenshare is the player's pipeline run backwards: one ffmpeg
// child grabs the screen, scales into a box this side chose and encodes,
// writing to stdout a byte stream the tee frames into the samples lksdk
// packetises — AV1 in IVF, H.265 and H.264 in FLV, every one a container that
// carries each frame's length. Three codecs, two rules: AV1 and H.265 are
// **hardware or nothing** — libaom, SVT-AV1 and x265 cannot hold a real-time
// screen encode without eating the machine the share is about — where H.264
// always has libx264 to fall back to, which is the whole reason it is the
// floor: no VP8 encoder exists in silicon on any GPU, and no CPU carries a
// live AV1 or H.265 encode politely. H.265 sits between the other two because
// of where the silicon is: every GPU that encodes H.264 has encoded H.265
// since about 2015, where AV1 encoders arrived with the RTX 40 / RX 7000 /
// Arc generation, so it is the better-than-H.264 tier most machines actually
// have.
//
// On Windows the grab and the encode meet on the GPU: Graphics Capture hands
// back a D3D11 texture already scaled to the encode box, and NVENC and AMF
// take a D3D11 texture as input and convert its colour on the way in — so
// where a probe finds the pair willing, no frame is ever read back to the
// processor and swscale is not in the chain at all. The download path stays
// for every other pairing.
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
	"sync/atomic"
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

	// Minimised is a window the OS holds no picture for. It is offered
	// anyway — see the Windows enumeration for why the picker would
	// otherwise never show a fullscreen game — but every capture API answers
	// one with nothing until it is back on screen, so the size above is the
	// rectangle it will return to rather than one it currently has.
	//
	// Windows only. X11 leaves it false: an iconified window there fails the
	// grab outright rather than freezing it.
	Minimised bool
}

// ProcessID is the process a window source belongs to, for a caller that wants
// that window's sound rather than the machine's. Zero for a monitor, for a
// platform that cannot answer, and for a window whose process has since gone —
// and zero is the right answer to hand a loopback capture in every one of those
// cases, being what it reads as the whole mix.
//
// It lives here rather than in audio because the ID is this package's: only the
// grabber that produced it knows whether it is a window handle or a monitor's.
func (s CaptureSource) ProcessID() uint32 {
	if s.Kind != CaptureWindow {
		return 0
	}

	return sourceProcess(s.ID)
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

	// Codec is the one codec to encode in — exactly, not a preference. Which
	// codecs to try in what order is policy about the room and the viewers,
	// and lives with the caller; ShareEncoder answers whether this machine
	// can encode a given one.
	Codec   ShareCodec
	Speed   CaptureSpeed
	Latency CaptureLatency
	Rate    CaptureRate

	// Baseline holds an H.264 share to the constrained baseline profile, the
	// one the oldest decoders take. Off, H.264 is Main — CABAC, which on
	// screen content is the same picture for about two thirds of the bits
	// (docs/performance.md) and which every current decoder takes. Nothing
	// to the other two codecs.
	Baseline bool
}

// ShareCodec is which codec a share's bytes are. Declared best first, which
// is the order a preference for the best is walked in — see ShareCodecs.
type ShareCodec int

const (
	ShareAV1 ShareCodec = iota
	ShareHEVC
	ShareH264
)

// ShareCodecs is every codec a share may go out in, best first: AV1 where the
// GPU has an encoder for it, H.265 where it has that, H.264 everywhere.
var ShareCodecs = [...]ShareCodec{ShareAV1, ShareHEVC, ShareH264}

// String is the codec as a reader would say it, which is what a window title
// and a stats card want.
func (c ShareCodec) String() string {
	switch c {
	case ShareAV1:
		return "AV1"
	case ShareHEVC:
		return "H.265"
	}

	return "H.264"
}

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
// name, the codec it writes, the global args it needs before the input (a
// hardware device opened), and the suffix the filter chain must end with to
// hand that hardware its frames.
type shareEncoder struct {
	name  string
	codec ShareCodec
	pre   []string
	tail  string
}

// newEncoder names one, reading the codec off the -c:v name: ffmpeg spells
// every hardware encoder as codec_vendor.
func newEncoder(name string) shareEncoder {
	enc := shareEncoder{name: name, codec: ShareH264}
	switch {
	case strings.HasPrefix(name, "av1_"):
		enc.codec = ShareAV1
	case strings.HasPrefix(name, "hevc_"):
		enc.codec = ShareHEVC
	}

	return enc
}

// hardware reports whether the encode leaves the CPU.
func (e shareEncoder) hardware() bool { return e.name != "libx264" }

// ivf reports whether the stream goes out in IVF rather than FLV: AV1's
// container, the FLV muxer refusing it; H.265 and H.264 ride FLV, the IVF
// muxer refusing those.
func (e shareEncoder) ivf() bool { return e.codec == ShareAV1 }

// args is the encoder's own flags at one effort and latency. Rate control is
// not among them — that is rateControl's, so the two halves of a bitrate
// decision are not spelled in different places. The one exception is x264's
// constant-rate flag, which has to travel with the tune it shares a parameter
// string with; the rate is a parameter here for that alone.
//
// Every H.264 set holds to the same contract: Main or constrained Baseline
// by o.baseline (no B-frames either way, -bf 0 seeing to it), 4:2:0, and
// **one slice per frame** — the tee recognises an access unit by its one
// slice, so a sliced encode would fuse frames. Encoders default to one
// slice; x264's zerolatency tune does not, which is what sliced-threads=0
// unwinds — and dropping the tune for the buffered mode leaves slicing off,
// so the contract holds in both. The H.265 sets carry the same clause by
// default and no profile: 8-bit 4:2:0 is Main, the only one WebRTC speaks.
// The AV1 sets carry neither: 8-bit 4:2:0 *is* profile 0, and IVF frames
// the stream one temporal unit per sample, so nothing can split.
//
// No adaptive quantisation, deliberately, and no multipass: both were
// measured on screen content (docs/performance.md) and neither bought
// picture per bit — NVENC's temporal AQ cost a static desktop half again
// its bitrate for nothing, and the spatial half lowered PSNR at the same
// spend. Every encoder's own default is the one that measured best.
func (e shareEncoder) args(o encodeOptions) []string {
	pick := func(quality, balanced, fast string) string {
		switch o.speed {
		case CaptureBalanced:
			return balanced
		case CaptureFast:
			return fast
		}

		return quality
	}
	// The grabber's own GPU texture takes no -pix_fmt, which would pull the
	// frame back to the processor for a conversion the encoder does itself on
	// the way in.
	pix := func(format string) []string {
		if o.direct {
			return nil
		}

		return []string{"-pix_fmt", format}
	}
	// The profile clause, H.264's alone. Every encoder spells Main the same
	// way and the baseline one as its own word.
	profile := func(baseline string) []string {
		if e.codec != ShareH264 {
			return nil
		}
		if o.baseline {
			return []string{"-profile:v", baseline}
		}

		return []string{"-profile:v", "main"}
	}
	buffered := o.latency == CaptureBuffered

	switch e.name {
	case "av1_nvenc", "hevc_nvenc", "h264_nvenc":
		args := append([]string{"-c:v", e.name}, pix("yuv420p")...)
		args = append(args, "-preset", pick("p4", "p3", "p1"))
		args = append(args, profile("baseline")...)
		if buffered {
			return append(args, "-tune", "hq", "-rc-lookahead", lookahead(o.fps))
		}

		return append(args, "-tune", "ull", "-zerolatency", "1", "-delay", "0")
	case "av1_amf", "hevc_amf", "h264_amf":
		usage := "ultralowlatency"
		if buffered {
			usage = "transcoding"
		}

		args := append([]string{"-c:v", e.name}, pix("yuv420p")...)
		args = append(args, "-usage", usage, "-quality", pick("quality", "balanced", "speed"))

		return append(args, profile("constrained_baseline")...)
	case "av1_qsv", "hevc_qsv", "h264_qsv":
		args := append([]string{"-c:v", e.name}, pix("nv12")...)
		args = append(args, "-preset", pick("medium", "fast", "veryfast"))
		args = append(args, profile("baseline")...)
		if buffered {
			return args // the default async depth is the buffering
		}

		return append(args, "-async_depth", "1")
	case "av1_vaapi", "hevc_vaapi", "h264_vaapi":
		// No latency dial: the driver buffers what it buffers.
		return append([]string{"-c:v", e.name}, profile("constrained_baseline")...)
	}

	args := []string{"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-preset", pick("veryfast", "superfast", "ultrafast")}
	args = append(args, profile("baseline")...)
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
	if o.rate == CaptureConstant {
		params = append(params, "nal-hrd=cbr")
	}
	if len(params) > 0 {
		args = append(args, "-x264-params", strings.Join(params, ":"))
	}

	return args
}

// encodeOptions is what args is asked at: the three dials and the rate the
// lookahead is sized from, plus the two facts about this particular pairing
// — whether the encoder is handed the grabber's texture, and whether H.264
// is held to the baseline profile.
type encodeOptions struct {
	speed   CaptureSpeed
	latency CaptureLatency
	rate    CaptureRate
	fps     int

	direct   bool
	baseline bool
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

// encoderCandidates is one codec's probe order, hardware most-capable first.
// The AV1 and H.265 lists are hardware **only** — see the header — where
// H.264 ends at libx264, which every build carries. AMF is Windows-only,
// VAAPI Linux-only; NVENC and QSV exist on both but QSV off Windows needs a
// stack most machines lack, so it sits behind VAAPI there.
func encoderCandidates(codec ShareCodec) []shareEncoder {
	prefix := map[ShareCodec]string{ShareAV1: "av1_", ShareHEVC: "hevc_", ShareH264: "h264_"}[codec]
	vaapi := newEncoder(prefix + "vaapi")
	vaapi.pre = []string{"-init_hw_device", "vaapi=va", "-filter_hw_device", "va"}
	vaapi.tail = ",format=nv12,hwupload"

	// Deliberately no -init_hw_device cuda on the NVENC entries, though it
	// has been seen answering a failed AV1 capability query: a bare
	// -init_hw_device becomes every filter's default device, and ddagrab
	// refuses anything that is not D3D11 — the flag fixed a probe and broke
	// every monitor share behind it, which the lavfi probe cannot see.
	var found []shareEncoder
	switch runtime.GOOS {
	case "windows":
		found = []shareEncoder{newEncoder(prefix + "nvenc"), newEncoder(prefix + "amf"), newEncoder(prefix + "qsv")}
	case "linux":
		found = []shareEncoder{newEncoder(prefix + "nvenc"), vaapi, newEncoder(prefix + "qsv")}
	}
	if codec == ShareH264 {
		found = append(found, newEncoder("libx264"))
	}

	return found
}

// encProbes remembers what one run answered, per codec — the ddagrab
// arrangement, for the same reason: an encoder the drivers cannot back has to
// be found out on a worker before a share starts, not at the first frame of a
// live track, and the answer does not change while the client runs. Lazy per
// codec, so forcing H.264 never pays for the AV1 and H.265 probes.
var (
	encMu     sync.Mutex
	encProbed [len(ShareCodecs)]bool
	encFound  [len(ShareCodecs)]shareEncoder
	encOK     [len(ShareCodecs)]bool
)

// encProbeTimeout bounds one candidate's test encode: a driver that neither
// answers nor fails must not be what a share waits on.
const encProbeTimeout = 8 * time.Second

// shareEncoder answers how this machine encodes one codec, probing the
// candidates in order on first ask. Not ok is a codec nothing here encodes:
// for AV1 and H.265 that is a GPU without the block, for H.264 an ffmpeg
// build with no libx264, after which sharing is refused with a sentence.
func (t Tools) shareEncoder(codec ShareCodec) (shareEncoder, bool) {
	encMu.Lock()
	defer encMu.Unlock()

	if encProbed[codec] {
		return encFound[codec], encOK[codec]
	}
	encProbed[codec] = true
	for _, enc := range encoderCandidates(codec) {
		if !encoderWorks(t.FFmpeg, enc) {
			continue
		}
		encFound[codec], encOK[codec] = enc, true
		where := "CPU"
		if enc.hardware() {
			where = "GPU"
		}
		log.Printf("video: %s shares encode as %s on the %s", codec, enc.name, where)

		return enc, true
	}
	if codec == ShareH264 {
		log.Printf("video: no H.264 encoder works here; sharing needs an ffmpeg that carries one")
	} else {
		log.Printf("video: no hardware %s encoder answered", codec)
	}

	return shareEncoder{}, false
}

// ShareEncoding is how a share here would be encoded: the -c:v name, and the
// codec — which every consumer of the stream keys off, the container and the
// frame shape following it.
type ShareEncoding struct {
	Name  string
	Codec ShareCodec
}

// ShareEncoder names how a share in this codec would be encoded, not ok for
// a machine that cannot. Asking is what runs the probes, so it belongs on the
// worker the source enumeration is already on — by the time a share starts,
// the answer is in hand.
func (t Tools) ShareEncoder(codec ShareCodec) (ShareEncoding, bool) {
	enc, ok := t.shareEncoder(codec)
	if !ok {
		return ShareEncoding{}, false
	}
	probeDirect(t.FFmpeg, enc)

	return ShareEncoding{Name: enc.name, Codec: enc.codec}, true
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
	args = append(args, enc.args(probeOptions)...)
	args = append(args, enc.rateControl(1_000_000, CaptureLowestLatency, CaptureVariable)...)
	args = append(args, "-bf", "0", "-frames:v", "3", "-f", "null", "-")

	cmd := exec.CommandContext(ctx, tool, args...)
	captureAttrs(cmd)

	return cmd.Run() == nil
}

// probeOptions is what every probe asks an encoder at: the defaults, which
// are also the flags older than the others on every encoder here.
var probeOptions = encodeOptions{speed: CaptureBalanced, latency: CaptureLowestLatency, rate: CaptureVariable, fps: 30}

// grab is how one platform gets at pixels. Most grabbers are input devices
// and answer with args alone — everything up to and including the `-i`. Some
// are *filter sources* instead (Windows' gfxcapture and ddagrab), which have
// no input at all: those answer with source, the filter the chain has to begin
// with, and the args carry only what setting it up needs.
//
// direct is the source already scaled into the encode box and handed to the
// encoder as the GPU texture it arrived in: the graph is the source alone,
// with no download, no scale and no format conversion between the two.
type grab struct {
	args   []string
	source string
	direct bool
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

	// A minimised window holds no picture, so the child would start and sit
	// there producing nothing. Asked here rather than in the picker because
	// it is the start that needs the content: a reader who opened the card
	// and changed their mind has moved nobody's windows.
	wakeSource(cfg.Source)

	g, err := grabArgs(t.FFmpeg, cfg, enc)
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
	// H.265 and H.264 in FLV — never bare Annex-B, which would leave the tee
	// to find the end of each frame at the start of the next. FLV stripped
	// of the metadata tag and the sizes it would seek back to write, the
	// output being a pipe.
	format, container := "flv", []string{"-flvflags", "no_metadata+no_duration_filesize+no_sequence_end"}
	if enc.ivf() {
		format, container = "ivf", nil
	}

	args := []string{"-v", "error", "-nostdin"}
	args = append(args, enc.pre...)
	args = append(args, g.args...)
	args = append(args, "-an", "-sn", "-dn")
	switch {
	case g.direct:
		// The grabber scaled into the box on the GPU and the encoder takes
		// the texture as it is: the graph is the source and nothing after.
		args = append(args, "-filter_complex", g.source)
	case g.source == "":
		args = append(args, "-vf", chain)
	default:
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
	args = append(args, enc.args(encodeOptions{
		speed: cfg.Speed, latency: cfg.Latency, rate: cfg.Rate, fps: cfg.FPS,
		direct: g.direct, baseline: cfg.Baseline,
	})...)
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
	codec     ShareCodec // AV1 in IVF; H.265 and H.264 in FLV
	phase     int
	head      []byte
	fh        [12]byte
	fhn       int // how much of fh has arrived
	remaining int // body bytes still owed to the current frame or tag

	// The FLV side (H.265 and H.264): the tag being assembled and what its
	// NAL units are prefixed with, which the sequence header says.
	tagType byte
	tag     []byte
	nalLen  int
	hasKey  bool // the access unit in hand opens a GOP: an IDR or IRAP slice

	unit   []byte // the frame being assembled
	broken bool   // not the stream this was promised; stop parsing

	// arrived is when the publisher last had a frame, for the one question
	// nothing else here can answer: a share that is up and sending nothing.
	// Atomic because it is read from whichever goroutine wants to know and
	// written by the one draining the child.
	arrived atomic.Int64

	// mu guards the attachment, which is claimed from the worker launching a
	// preview (app.startSelfPreview), released on the UI thread, and fed from
	// whichever goroutine is draining the child.
	mu         sync.Mutex
	vps        []byte // the latest parameter sets, put back in front of every
	sps        []byte // keyframe — the container carried them once — and
	pps        []byte // replayed to an H.264 preview that attaches mid-stream
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

// NewShareTee wraps a capture stream — AV1 in IVF, H.265 or H.264 in FLV —
// which must match what the capture child was started to write. width and
// height are the encode box, which an H.264 preview's file header declares
// (AV1's is the encoder's own, replayed; an H.265 preview reads raw Annex-B
// and has none). Closing the tee is closing what it wraps.
func NewShareTee(src io.ReadCloser, codec ShareCodec, width, height int) *ShareTee {
	t := &ShareTee{src: src, codec: codec}
	if codec == ShareH264 {
		t.fileHeader = newIVFFileHeader("H264", width, height)
	}
	// Stamped from the start rather than at the first frame, so a share whose
	// source never draws at all reads as stalled instead of as brand new.
	t.arrived.Store(time.Now().UnixNano())

	return t
}

// Idle is how long the publisher has gone without a frame — a source that has
// stopped drawing, which for a window means minimised or a game paused behind
// the client. What that is worth saying about is the caller's.
func (t *ShareTee) Idle() time.Duration {
	return time.Since(time.Unix(0, t.arrived.Load()))
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
	t.arrived.Store(time.Now().UnixNano())

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
// and for H.264 the latest SPS and PPS as a frame of their own (every
// keyframe carries them again anyway, and a duplicate costs a decoder
// nothing). An H.265 preview reads raw Annex-B and is gated to a keyframe
// carrying its own parameter sets, so it is given nothing ahead. One
// attachment at a time; a second replaces the first. out is closed by
// Detach, by the stream ending, or by a write failing — never by the caller.
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
	if t.codec == ShareH264 && (t.sps != nil || t.pps != nil) {
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
	if t.codec == ShareAV1 {
		t.consumeIVF(b)
		return
	}

	t.consumeFLV(b)
}

/* FLV (H.265 and H.264) */

const (
	flvFileHeaderLen = 13 // the 9-byte header and the first "previous tag size"
	flvTagHeaderLen  = 11
	flvPrevSizeLen   = 4

	flvTagVideo  = 9
	flvCodecAVC  = 7
	flvAVCConfig = 0 // the tag body is an AVCDecoderConfigurationRecord
	flvAVCNALUs  = 1 // the tag body is one access unit, its NAL units length-prefixed

	// The enhanced tag (E-RTMP), which is how FLV carries anything newer
	// than H.264: the first byte's top bit marks it, the low nibble is the
	// packet type, and a four-character code names the codec where the
	// legacy header had a nibble.
	flvExHeader     = 0x80
	flvExSeqStart   = 0 // an HEVCDecoderConfigurationRecord
	flvExFrames     = 1 // one access unit behind a 3-byte composition offset
	flvExFramesNoCT = 3 // one access unit, no offset
)

// hevcAUD is one access unit delimiter, the NAL that opens an H.265 access
// unit: type 35, pic_type "any". Written after every unit the preview is
// handed, because the raw demuxer's parser can only close a unit at the
// NAL that starts the next — which for a live stream is a whole frame of
// delay — and the delimiter *is* that NAL, sent ahead of the frame it will
// belong to. Legal Annex-B: a delimiter is optional and a stream may open a
// unit with one. Measured (docs/performance.md): the held frame goes from
// one interval to under a tenth of one.
var hevcAUD = []byte{0, 0, 0, 1, 0x46, 0x01, 0x50}

// The sections an FLV stream alternates between after its file header.
const (
	flvFileHeader = iota
	flvTagHeader
	flvTagBody
	flvPrevSize
)

// consumeFLV is the H.265 and H.264 walk, and it is the IVF one again with a
// tag in place of the 12-byte frame header: the container carries every
// tag's length, so an access unit is complete the moment its bytes have
// crossed. Bare Annex-B was here before it, and with no lengths to hop by it
// could only close a unit at the start code opening the next — a frame of
// latency on every frame, for both the room and the preview. The muxer
// converts the encoder's start codes to length prefixes on the way in, and
// the parameter sets arrive once, in a sequence header tag, rather than
// ahead of every keyframe; the publisher's frame is the unit back in
// Annex-B with the sets put in front of each keyframe, which is what the
// packetiser and every decoder expect.
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
// parameter sets, or one access unit. H.264 arrives in the legacy header,
// H.265 in the enhanced one; a tag of the other shape is a stream that is
// not the one promised.
func (t *ShareTee) videoTag(body []byte) {
	if len(body) < 5 {
		return
	}

	if body[0]&flvExHeader != 0 {
		if t.codec != ShareHEVC || string(body[1:5]) != "hvc1" {
			t.breakOff()
			return
		}

		switch body[0] & 0x0F {
		case flvExSeqStart:
			t.readHVCC(body[5:])
		case flvExFrames:
			if len(body) >= 8 {
				t.readNALUs(body[8:])
			}
		case flvExFramesNoCT:
			t.readNALUs(body[5:])
		}

		return
	}

	if t.codec != ShareH264 || body[0]&0x0F != flvCodecAVC {
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

// readHVCC is readAVCC for the HEVCDecoderConfigurationRecord: the length
// width sits at byte 21, and the parameter sets follow as arrays, one per
// NAL type, each counted.
func (t *ShareTee) readHVCC(c []byte) {
	if len(c) < 23 || c[0] != 1 {
		t.breakOff()
		return
	}
	t.nalLen = int(c[21]&3) + 1

	var vps, sps, pps []byte
	i := 23
	for a, arrays := 0, int(c[22]); a < arrays && i+3 <= len(c); a++ {
		kind := c[i] & 0x3F
		count := int(binary.BigEndian.Uint16(c[i+1:]))
		i += 3
		for k := 0; k < count && i+2 <= len(c); k++ {
			l := int(binary.BigEndian.Uint16(c[i:]))
			i += 2
			if i+l > len(c) {
				break
			}
			nal := append([]byte{0, 0, 0, 1}, c[i:i+l]...)
			i += l
			switch kind {
			case hevcVPS:
				vps = nal
			case hevcSPS:
				sps = nal
			case hevcPPS:
				pps = nal
			}
		}
	}

	t.storePS(vps, sps, pps)
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

	t.storePS(nil, sps, pps)
}

// The NAL unit types either codec's walk has to recognise: the parameter
// sets, and the slices a decoder can enter the stream at. H.264's type is
// the low five bits of its one header byte; H.265's is six bits from the
// top of a two-byte header, and its keyframes are the IRAP range — IDR, CRA
// and the BLA kinds together.
const (
	avcSPS, avcPPS, avcIDR = 7, 8, 5

	hevcVPS, hevcSPS, hevcPPS   = 32, 33, 34
	hevcIRAPFirst, hevcIRAPLast = 16, 23
)

// nalKind reads a NAL unit's type the way its codec spells it, and whether
// it is a parameter set or a keyframe slice in that codec's numbering.
func (t *ShareTee) nalKind(nal []byte) (paramSet, key bool, kind byte) {
	if t.codec == ShareHEVC {
		kind = (nal[0] >> 1) & 0x3F

		return kind == hevcVPS || kind == hevcSPS || kind == hevcPPS,
			kind >= hevcIRAPFirst && kind <= hevcIRAPLast, kind
	}
	kind = nal[0] & 0x1F

	return kind == avcSPS || kind == avcPPS, kind == avcIDR, kind
}

// readNALUs is one access unit: its length-prefixed NAL units back in
// Annex-B, the parameter sets put in front where it carries a keyframe and
// none of its own — the encoder, told to keep them for the container's
// header, stops repeating them.
func (t *ShareTee) readNALUs(d []byte) {
	nalLen := t.nalLen
	if nalLen == 0 {
		nalLen = 4
	}

	unit := make([]byte, 0, len(d)+16)
	hasKey, inlinePS := false, false
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

		paramSet, key, kind := t.nalKind(nal)
		switch {
		case paramSet:
			inlinePS = true
			framed := append([]byte{0, 0, 0, 1}, nal...)
			switch kind {
			case hevcVPS:
				t.storePS(framed, nil, nil)
			case hevcSPS, avcSPS:
				t.storePS(nil, framed, nil)
			default:
				t.storePS(nil, nil, framed)
			}
		case key:
			hasKey = true
		}
		unit = append(unit, 0, 0, 0, 1)
		unit = append(unit, nal...)
	}
	if len(unit) == 0 {
		return
	}
	if hasKey && !inlinePS && t.sps != nil {
		unit = slices.Concat(t.vps, t.sps, t.pps, unit)
	}

	t.unit, t.hasKey = unit, hasKey
	t.emitUnit()
	t.queue = append(t.queue, t.unit)
	t.unit, t.hasKey = nil, false
}

// storePS files the parameter sets, any of the three alone. Under mu because
// Attach reads them from the UI thread.
func (t *ShareTee) storePS(vps, sps, pps []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if vps != nil {
		t.vps = vps
	}
	if sps != nil {
		t.sps = sps
	}
	if pps != nil {
		t.pps = pps
	}
}

// emitUnit offers the assembled access unit to the preview, gated on a
// keyframe until one has started it — a decoder handed a stream that opens
// mid-GOP answers with a run of complaints and no picture. H.264 goes as an
// IVF frame, whose pts only counts (the preview reads what arrives when it
// arrives, so the demuxer's clock is never consulted); H.265 goes as the
// Annex-B unit itself with the next unit's delimiter behind it, which is
// what closes it at the parser — see hevcAUD.
func (t *ShareTee) emitUnit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.previewPTS++
	if t.frames == nil {
		return
	}
	if !t.started {
		if !t.hasKey {
			return
		}
		t.started = true
	}

	if t.codec == ShareHEVC {
		t.offer(slices.Concat(t.unit, hevcAUD))
		return
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
			// Sized once from the header: the body lands across several
			// reads, and growing by append would copy it once per chunk.
			t.unit = make([]byte, 0, size)
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
