package video

// A Stream is one child process and the pipe it feeds. Frames carry raw RGBA
// at a size this side computed; PCM carries s16le mono at the mixer's rate.
// Audio is a second child rather than a second fd on the first: an extra
// inherited pipe does not exist on Windows, and two children die exactly as
// one does.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FrameConfig is one playback pipe's shape. Width, Height and FPS are the
// caller's numbers — the frame's byte size follows from them and nothing the
// file says can move it. Format comes from Sniff. Loop replays the input
// forever — the demuxer seeking back, not a child per pass — so the pipe
// simply never ends and the caller's stop is the only way out.
type FrameConfig struct {
	Path   string
	Format string

	Width  int
	Height int
	FPS    int

	Start time.Duration
	Loop  bool
}

// PCMConfig is the audio pipe's shape; the rate and channel count are fixed
// to what the mixer takes.
type PCMConfig struct {
	Path   string
	Format string

	Start time.Duration
	Loop  bool
}

// Stream is a running child and its stdout. Read is the only way bytes cross;
// Stop kills the child and reaps it, and is safe from any goroutine, twice.
type Stream struct {
	cmd     *exec.Cmd
	out     io.ReadCloser
	stderr  *tailBuffer
	release func() // sandbox handles to drop once the child is gone

	// reapOnce owns the one cmd.Wait and the close of out that follows it. The
	// pipe is this side's rather than exec's (see launch), so nothing else will
	// close it; it must run exactly once and only after reading is over — a
	// second caller waits on done instead.
	reapOnce sync.Once
	done     chan struct{}
	waitErr  error // written inside reapOnce, read after done closes

	// pcm is ReadPCM's scratch, kept because the sound pump calls it on every
	// speaker wake. Safe unguarded for the same reason out is: one reader.
	pcm []byte
}

// FrameSize is the exact byte length of one frame on a Frames stream.
func (c FrameConfig) FrameSize() int {
	return c.Width * c.Height * 4
}

// Frames starts a child decoding the file into raw RGBA frames at a constant
// rate. The caller reads exactly FrameSize bytes per frame and stops the
// stream when the card goes; there is no other framing and no other clock.
func (t Tools) Frames(cfg FrameConfig) (*Stream, error) {
	if err := checkFrameSize(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	if cfg.FPS < 1 || cfg.FPS > 60 {
		return nil, fmt.Errorf("video: bad frame rate %d", cfg.FPS)
	}

	args := []string{"-v", "error", "-nostdin", "-threads", decodeThreads}
	args = appendSeek(args, cfg.Start)
	args = appendLoop(args, cfg.Loop)
	args = append(args,
		"-f", cfg.Format,
		"-i", cfg.Path,
		"-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("fps=%d,%s", cfg.FPS, scaleFilter(cfg.Width, cfg.Height)),
		"-f", "rawvideo", "-pix_fmt", "rgba",
		"pipe:1",
	)

	return start(t.FFmpeg, args, cfg.Path)
}

// LiveConfig is a live pipe's shape: a muxed byte stream arrives on the
// child's stdin and exact-size RGBA frames come back, with no file anywhere.
// Format is the demuxer to force and is the caller's word — the codec came
// from the media session, not from sniffing — matched against the allowlist
// below. Width and Height are this side's numbers, as ever.
type LiveConfig struct {
	Format string

	Width  int
	Height int
}

// liveFormats is what LiveFrames accepts: IVF, carrying VP8, VP9, AV1 or —
// under the H264 fourcc, which the demuxer maps like any other — H.264. One
// framing for every codec because it is the one that says where each frame
// ends: bare Annex-B was accepted once, and its parser can only close a
// frame at the start code opening the next, a whole frame of delay on a
// live stream. The string reaches a command line, so it is matched exactly
// rather than trusted.
var liveFormats = map[string]bool{"ivf": true}

// LiveFrames starts a child decoding a live byte stream written to its
// stdin into raw RGBA frames — the player's exact-byte contract with no file
// and no clock: frames come out as the stream delivers them, and the caller
// must always drain the pipe. Its buffer is smaller than one frame, so a
// stalled reader backpressures the decoder into latency, which is the wrong
// trade live.
//
// The child runs in the full sandbox minus the media bind — a remote
// participant's bitstream is exactly as hostile as a message attachment.
// Closing the returned writer ends the stream: the child flushes what it
// holds and exits, and the frame side answers io.EOF.
func (t Tools) LiveFrames(cfg LiveConfig) (*Stream, io.WriteCloser, error) {
	if err := checkFrameSize(cfg.Width, cfg.Height); err != nil {
		return nil, nil, err
	}
	if !liveFormats[cfg.Format] {
		return nil, nil, fmt.Errorf("video: not a live format this player accepts: %q", cfg.Format)
	}

	// No -nostdin: stdin is the input. The latency flags stop the analysis
	// buffering ahead of the decode, and the probe is floored for *both*
	// formats: the demuxer is named rather than sniffed, so nothing is being
	// probed for, and the stream is entered at a keyframe either way — the
	// parser reads H.264's SPS on the way past like any other NAL. Left at
	// the default, libavformat reads its 5 MB before answering with a frame,
	// which measured 3.1 s to first picture at 720p15 against 0.28 s here,
	// for byte-identical output.
	//
	// Deliberately no `-fflags nobuffer`: it makes the IVF demuxer misframe a
	// piped stream — every packet refused as invalid — and what it would buy
	// is already bought by the zeroed analysis.
	args := []string{"-v", "error", "-threads", liveThreads(cfg.Width, cfg.Height),
		"-flags", "low_delay", "-analyzeduration", "0", "-probesize", "32"}
	args = append(args,
		"-f", cfg.Format,
		"-i", "pipe:0",
		"-an", "-sn", "-dn",
		"-vf", liveScaleFilter(cfg.Width, cfg.Height),
		// The output side single-threaded: the rawvideo encoder is otherwise
		// run frame-threaded like any other, and frame threading holds one
		// frame back — measured as exactly one frame of delay on every live
		// stream, a raw-in raw-out control included. The decoder's threads
		// are the ones before the input and are unaffected.
		"-threads", "1",
		// One frame out per frame in. The rawvideo muxer carries no
		// timestamps, which makes ffmpeg's default sync *constant* rate: it
		// pads the decoded frames out to the input's own clock, and the IVF
		// the watch is remuxed into declares the 90 kHz RTP timebase — so it
		// duplicated every frame a thousand-odd times, and the pipe stood
		// seconds deep behind the picture. A live stream is paced by its
		// sender; nothing here may invent or drop a frame.
		"-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "rgba",
		"pipe:1",
	)

	// Both pipes are left at the platform's own small buffer, which is what the
	// paragraph above rests on: a live stream must stall rather than queue. Out,
	// a buffer under one frame is what turns a stalled reader into decoder
	// backpressure instead of stale frames. In, a megabyte of a 1.4 Mbps
	// bitstream is six seconds of latency nothing takes back out again.
	return launch(t.FFmpeg, args, "", true, 0)
}

// liveScaleFilter fits the source into the asked-for box and pads the rest
// black: unlike a file's, a live source's dimensions can move mid-stream,
// and the pad is what keeps every frame exactly Width×Height×4 bytes through
// it — the cost is letterboxing until the caller relaunches at the new
// aspect.
func liveScaleFilter(width, height int) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease:flags=bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
		width, height, width, height)
}

// liveThreads sizes the live decoder by its output area the way
// decodeThreads sizes a chat card's: a stream watched at 1080p is worth
// twice the card's two.
func liveThreads(width, height int) string {
	if width*height > 1280*720 {
		return "4"
	}

	return decodeThreads
}

// PCM starts a child decoding the file's audio into s16le mono at PCMRate.
func (t Tools) PCM(cfg PCMConfig) (*Stream, error) {
	args := []string{"-v", "error", "-nostdin", "-threads", decodeThreads}
	args = appendSeek(args, cfg.Start)
	args = appendLoop(args, cfg.Loop)
	args = append(args,
		"-f", cfg.Format,
		"-i", cfg.Path,
		"-vn", "-sn", "-dn",
		"-f", "s16le",
		"-ac", fmt.Sprint(PCMChannels),
		"-ar", fmt.Sprint(PCMRate),
		"pipe:1",
	)

	return start(t.FFmpeg, args, cfg.Path)
}

func appendLoop(args []string, loop bool) []string {
	if !loop {
		return args
	}

	return append(args, "-stream_loop", "-1")
}

// appendSeek places the seek before the input, which is the cheap seek — the
// demuxer jumps rather than decoding its way there. Restarting the child with
// a new offset is the only correct seek over a pipe.
func appendSeek(args []string, at time.Duration) []string {
	if at <= 0 {
		return args
	}
	if at > maxDuration {
		at = maxDuration
	}

	return append(args, "-ss", fmt.Sprintf("%.3f", at.Seconds()))
}

// Read hands back whatever the pipe holds, blocking while the child is
// between writes. io.EOF is the file ending.
func (s *Stream) Read(p []byte) (int, error) {
	return s.out.Read(p)
}

// ReadFrame fills dst with exactly one frame. A short final read is the child
// stopping mid-frame — surfaced as EOF, the frame discarded, because a
// half-written frame is not a picture.
func (s *Stream) ReadFrame(dst []byte) error {
	if _, err := io.ReadFull(s.out, dst); err != nil {
		if err == io.ErrUnexpectedEOF {
			return io.EOF
		}
		return err
	}

	return nil
}

// ReadPCM fills buf and reports how many samples arrived. Zero with io.EOF is
// the sound ending; a trailing odd byte is dropped, half a sample not being
// one.
func (s *Stream) ReadPCM(buf []int16) (int, error) {
	if len(s.pcm) < len(buf)*2 {
		s.pcm = make([]byte, len(buf)*2)
	}
	raw := s.pcm[:len(buf)*2]
	n, err := io.ReadFull(s.out, raw)

	samples := n / 2
	for i := 0; i < samples; i++ {
		buf[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	if samples > 0 && err == io.EOF {
		err = nil
	}

	return samples, err
}

// Close is Stop as an io.Closer, so a stream can be handed to something that
// owns a reader — the screenshare publisher's track, which closes its source
// when the track ends. Killing the child is the right answer to either.
func (s *Stream) Close() error {
	s.Stop()

	return nil
}

// Stop kills the child and waits for it. Idempotent, callable from any
// goroutine; a reader blocked in Read unblocks with an error as the pipe
// goes.
func (s *Stream) Stop() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.reap()
}

// reap runs the one Wait and publishes its answer. Every exit path funnels
// here: the reader hitting EOF, Stop, and the tool deadline.
func (s *Stream) reap() {
	s.reapOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
		s.out.Close()
		if s.release != nil {
			s.release()
		}
		close(s.done)
	})
	<-s.done
}

// Usage is the processor time the child spent, known once it has been
// reaped and zero before. For measurement: a sandboxed child's times cannot
// be read from outside the process that made it, and what a decode costs is
// a number docs/performance.md wants.
func (s *Stream) Usage() (user, system time.Duration) {
	select {
	case <-s.done:
	default:
		return 0, 0
	}
	state := s.cmd.ProcessState
	if state == nil {
		return 0, 0
	}

	return state.UserTime(), state.SystemTime()
}

// Stderr reports the tail of what the child logged, for the notice a failed
// play raises.
func (s *Stream) Stderr() string {
	return s.stderr.String()
}

/* Child plumbing */

// start launches one sandboxed child. media names the input file so the
// sandbox can offer exactly that one path read-only.
func start(tool string, args []string, media string) (*Stream, error) {
	s, _, err := launch(tool, args, media, false, pipeBytes)

	return s, err
}

// pipeBytes is how much of a frame the kernel holds between a file-fed child's
// write and this side's read. The platform's own default is a few kilobytes and
// a 1080p frame is 8.3 MB, so a frame crosses in thousands of copies, each one a
// writer blocked and a reader woken.
//
// Measured on Windows against a child writing 256 MiB: the default buffer runs
// at 3.5-4.0 GB/s, 64 KiB at ~5.3 and a megabyte at ~6.0, which is where it
// flattens. The cost is that megabyte per file-fed child, and the one-playback
// rule means there is rarely more than one.
//
// Only a file-fed child takes it; a live stream wants the small buffer, and
// LiveFrames says why.
const pipeBytes = 1 << 20

// launch is the one place a child is spawned. stdin asks for the write end
// of the child's stdin, for a live stream fed by the caller; a file-fed
// child takes none. buffer is how much each pipe holds, zero being the
// platform's own default.
//
// The pipes are made here rather than taken from exec.Cmd, which offers no say
// in that. What it costs is the bookkeeping exec would have done: the ends the
// child holds are closed here once it is running — one left open is an EOF the
// reader never sees — and the end this side reads from is closed by reap rather
// than by Wait.
func launch(tool string, args []string, media string, stdin bool, buffer int) (*Stream, io.WriteCloser, error) {
	argv := sandboxArgv(tool, args, media)

	cmd := exec.Command(argv[0], argv[1:]...)
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	platformAttrs(cmd)

	var childEnds []*os.File
	defer func() {
		for _, end := range childEnds {
			end.Close()
		}
	}()

	var in io.WriteCloser
	if stdin {
		read, write, err := sizedPipe(buffer)
		if err != nil {
			return nil, nil, fmt.Errorf("video: %w", err)
		}
		cmd.Stdin, in = read, write
		childEnds = append(childEnds, read)
	}

	out, write, err := sizedPipe(buffer)
	if err != nil {
		if in != nil {
			in.Close()
		}
		return nil, nil, fmt.Errorf("video: %w", err)
	}
	cmd.Stdout = write
	childEnds = append(childEnds, write)

	if err := cmd.Start(); err != nil {
		out.Close()
		if in != nil {
			in.Close()
		}
		return nil, nil, fmt.Errorf("video: %w", err)
	}

	release := harden(cmd)

	return &Stream{cmd: cmd, out: out, stderr: stderr, release: release, done: make(chan struct{})}, in, nil
}

// run is start for the tools that answer and exit — probe and poster: capped
// stdout, a wall-clock deadline, and the child's own words in the error.
func run(tool string, args []string, media string, limit int64) ([]byte, error) {
	s, err := start(tool, args, media)
	if err != nil {
		return nil, err
	}

	// The deadline stops a child that hangs on hostile input; Stop is
	// idempotent, so the timer firing after a clean exit kills nothing.
	timer := time.AfterFunc(toolTimeout, s.Stop)
	defer timer.Stop()

	out, readErr := io.ReadAll(io.LimitReader(s.out, limit+1))

	if int64(len(out)) > limit {
		s.Stop()
		return nil, fmt.Errorf("video: %s said more than the %d bytes allowed", opName(tool), limit)
	}

	s.reap()
	if s.waitErr != nil {
		return nil, childError(opName(tool), s.waitErr, s.stderr)
	}
	if readErr != nil {
		return nil, fmt.Errorf("video: %s: %w", opName(tool), readErr)
	}

	return out, nil
}

func opName(tool string) string {
	if strings.Contains(filepath.Base(tool), "ffprobe") {
		return "probe"
	}

	return "decode"
}
