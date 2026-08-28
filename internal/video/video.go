// Package video drives a sandboxed ffmpeg subprocess; the client never parses
// video itself. A sender-controlled bitstream is the classic RCE surface, so
// the demuxer and every codec run in a throwaway child with no token, no
// network and nothing writable, and what crosses back is raw pixels and PCM at
// sizes this side chose. Every number a child reports is treated as hostile:
// dimensions, durations and aspect ratios are clamped before anything
// allocates by them. See docs/video-player.md for the design and the rejected
// alternatives.
//
// No internal dependencies: paths, sizes and rates arrive as arguments, the
// way audio and cpu take theirs.
package video

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// maxDimension and maxPixels bound what a probe may claim before this side
	// computes an output size from it — the same 32 Mpx ceiling the image cache
	// decodes under, for the same reason: a few bytes can claim a canvas of
	// billions of pixels.
	maxDimension = 8192
	maxPixels    = 32 << 20

	// maxDuration bounds the figure drawn on the card and used to place a
	// seek. A claim past it fails the probe with the file — a negative or
	// absurd timestamp is the classic crafted-metadata crash.
	maxDuration = 24 * time.Hour

	// toolTimeout fells a probe or poster child that hangs on its input. A
	// playback child has no deadline — it is killed by whoever stops the
	// stream.
	toolTimeout = 30 * time.Second

	// maxProbeOutput caps what ffprobe may say; maxStderr caps what any child
	// may log. Both keep a hostile child from feeding the client unbounded
	// bytes on a side channel.
	maxProbeOutput = 1 << 20
	maxStderr      = 4 << 10

	// decodeThreads keeps a decoding child off most of the machine: a chat
	// card is a few hundred pixels wide and does not need every core.
	decodeThreads = "2"

	// PCMRate and PCMChannels are the shape of the audio stream — exactly
	// what the client's mixer lanes take.
	PCMRate     = 48000
	PCMChannels = 1
)

/* Discovery */

// Tools holds the resolved ffmpeg and ffprobe binaries. Discovery, not
// bundling: inline playback is offered only where both are found.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// Discover looks for ffmpeg and ffprobe on PATH. Both or nothing: they ship
// together, probing is what makes the frame pipe's byte contract computable,
// and a machine with one half is a machine with a broken install.
func Discover() (Tools, bool) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return Tools{}, false
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return Tools{}, false
	}

	return Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}, true
}

/* Sniffing */

// sniffFormats maps magic bytes to the one demuxer the file is handed to. The
// sender's filename and content type are never consulted, and ffmpeg's own
// format guessing is never reached: a playlist or a script dressed as an
// .mp4 must not reach a demuxer that follows references (HLS and concat both
// open whatever their input names).
const (
	formatMP4      = "mov,mp4,m4a,3gp,3g2,mj2"
	formatMatroska = "matroska,webm"
	formatAVI      = "avi"
	formatFLV      = "flv"
	formatASF      = "asf"
)

// asfMagic is the ASF header object's GUID — a .wmv's first sixteen bytes.
var asfMagic = []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11, 0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C}

// Sniff reads the file's magic bytes and answers the demuxer to force, with
// the extension the bytes earn — never the sender's. Not ok is a container
// this player does not speak, which is refused rather than guessed at.
func Sniff(path string) (format, ext string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	return sniffHead(head)
}

func sniffHead(head []byte) (format, ext string, ok bool) {
	switch {
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		return formatMP4, ".mp4", true
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}):
		// One demuxer answers both spellings; the DocType a few bytes in is
		// only which extension the OS player expects.
		if bytes.Contains(head[:min(len(head), 64)], []byte("webm")) {
			return formatMatroska, ".webm", true
		}
		return formatMatroska, ".mkv", true
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("AVI ")):
		return formatAVI, ".avi", true
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{'F', 'L', 'V', 0x01}):
		return formatFLV, ".flv", true
	case len(head) >= 16 && bytes.Equal(head[:16], asfMagic):
		return formatASF, ".wmv", true
	}

	return "", "", false
}

/* Probing */

// Info is what one probe answers, already clamped: dimensions are display
// dimensions (rotation applied, sample aspect folded in) and safe to size a
// buffer by, and Duration is always positive — a file declaring none fails
// the probe.
type Info struct {
	Width, Height int
	Duration      time.Duration
	HasAudio      bool
}

// probeReport mirrors the slice of ffprobe's JSON this package reads. Every
// field is sender-controlled until validated.
type probeReport struct {
	Streams []struct {
		CodecType         string `json:"codec_type"`
		Width             int    `json:"width"`
		Height            int    `json:"height"`
		SampleAspectRatio string `json:"sample_aspect_ratio"`
		SideData          []struct {
			Rotation json.Number `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe asks ffprobe for the file's shape, in the same sandbox playback runs
// in. format comes from Sniff, never from the caller's imagination.
func (t Tools) Probe(path, format string) (Info, error) {
	args := []string{
		"-v", "error",
		"-f", format,
		"-print_format", "json",
		"-show_format", "-show_streams",
		"-i", path,
	}

	out, err := run(t.FFprobe, args, path, maxProbeOutput)
	if err != nil {
		return Info{}, err
	}

	var report probeReport
	if err := json.Unmarshal(out, &report); err != nil {
		return Info{}, fmt.Errorf("video: probe unreadable: %w", err)
	}

	return report.validate()
}

// validate is the boundary every probed number crosses: nothing past it can
// be negative, absurd or NaN. The 2021-era crashers were clients doing
// arithmetic on exactly these fields as sent.
func (r *probeReport) validate() (Info, error) {
	var info Info
	for _, s := range r.Streams {
		switch s.CodecType {
		case "audio":
			info.HasAudio = true
		case "video":
			if info.Width != 0 {
				continue // first video stream wins
			}

			w, h := s.Width, s.Height
			if w < 1 || h < 1 || w > maxDimension || h > maxDimension || w*h > maxPixels {
				return Info{}, fmt.Errorf("video: refusing claimed %dx%d", w, h)
			}

			// A sample aspect ratio widens or narrows the picture; outside a
			// sane band it is a lie and the pixels are taken square.
			if num, den, ok := parseRatio(s.SampleAspectRatio); ok {
				sar := float64(num) / float64(den)
				if sar > 0.25 && sar < 4 && sar != 1 {
					w = clampDim(int(math.Round(float64(w) * sar)))
				}
			}

			// ffmpeg auto-rotates on decode, so a quarter-turn swaps the
			// frame this package will be handed.
			for _, sd := range s.SideData {
				if rot, err := sd.Rotation.Float64(); err == nil && quarterTurn(rot) {
					w, h = h, w
				}
			}

			info.Width, info.Height = w, h
		}
	}

	if info.Width == 0 {
		return Info{}, errors.New("video: no video stream")
	}

	// A file with no declared, finite, positive length is refused whole rather
	// than played "unknown": the scrub would have no scale to place a seek on,
	// and an unbounded or absurd length is the crafted-metadata class the rest
	// of this function refuses.
	seconds, err := strconv.ParseFloat(r.Format.Duration, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return Info{}, errors.New("video: the file declares no length")
	}
	duration := time.Duration(seconds * float64(time.Second))
	if duration <= 0 || duration > maxDuration {
		return Info{}, fmt.Errorf("video: refusing a claimed length of %v", duration)
	}
	info.Duration = duration

	return info, nil
}

func clampDim(v int) int {
	if v < 1 {
		return 1
	}
	if v > maxDimension {
		return maxDimension
	}

	return v
}

func parseRatio(s string) (num, den int, ok bool) {
	a, b, found := strings.Cut(s, ":")
	if !found {
		return 0, 0, false
	}
	num, err1 := strconv.Atoi(a)
	den, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || num <= 0 || den <= 0 {
		return 0, 0, false
	}

	return num, den, true
}

func quarterTurn(rot float64) bool {
	if math.IsNaN(rot) || math.IsInf(rot, 0) {
		return false
	}
	turn := int(math.Round(rot/90)) % 4
	if turn < 0 {
		turn += 4
	}

	return turn == 1 || turn == 3
}

/* Poster */

// Poster decodes the first frame at exactly width x height. The frame's byte
// size is computed from this side's numbers alone, so a child that writes
// more than one frame's worth is killed as a liar, not read.
func (t Tools) Poster(path, format string, width, height int) (*image.RGBA, error) {
	if err := checkFrameSize(width, height); err != nil {
		return nil, err
	}

	args := []string{
		"-v", "error", "-nostdin",
		"-threads", decodeThreads,
		"-f", format,
		"-i", path,
		"-an", "-sn", "-dn",
		"-frames:v", "1",
		"-vf", scaleFilter(width, height),
		"-f", "rawvideo", "-pix_fmt", "rgba",
		"pipe:1",
	}

	want := int64(width) * int64(height) * 4
	out, err := run(t.FFmpeg, args, path, want)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) != want {
		return nil, fmt.Errorf("video: poster came back %d of %d bytes", len(out), want)
	}

	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	copy(frame.Pix, out)

	return frame, nil
}

// scaleFilter forces both output dimensions, so the byte-per-frame contract
// holds even where the probe misjudged rotation or aspect — a wrong guess
// costs a stretched picture, never a misread pipe.
func scaleFilter(width, height int) string {
	return fmt.Sprintf("scale=%d:%d:flags=bilinear", width, height)
}

func checkFrameSize(width, height int) error {
	if width < 1 || height < 1 || width > maxDimension || height > maxDimension {
		return fmt.Errorf("video: bad frame size %dx%d", width, height)
	}

	return nil
}

/* Errors */

// childError folds the tail of a child's stderr into the failure, which is
// the only diagnostic a sandboxed decode leaves behind.
func childError(op string, err error, stderr *tailBuffer) error {
	tail := stderr.String()
	if tail == "" {
		return fmt.Errorf("video: %s: %w", op, err)
	}

	return fmt.Errorf("video: %s: %w: %s", op, err, tail)
}

// tailBuffer keeps the first maxStderr bytes written and discards the rest —
// an io.Writer that can never block or grow, which is what lets the child's
// stderr drain without trusting how much of it there is.
type tailBuffer struct {
	buf bytes.Buffer
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	if room := maxStderr - t.buf.Len(); room > 0 {
		if len(p) > room {
			t.buf.Write(p[:room])
		} else {
			t.buf.Write(p)
		}
	}

	return len(p), nil
}

func (t *tailBuffer) String() string {
	return strings.TrimSpace(t.buf.String())
}
