package ui

// Hover is a GIF's only play control. At rest every surface draws the first
// frame it always drew; the pointer arriving is what fetches the original,
// decodes it and starts the clock, and the pointer leaving frees all of it and
// puts the still back — so a GIF nobody hovers costs nothing, a hover always
// starts from frame zero, and at most one animation ever runs, the pointer
// being in one place. That bound is what pays for the per-tick texture upload,
// the known expensive path (docs/performance.md).
//
// The frames are played by composing each onto one reusable RGBA canvas —
// disposal applied, patch drawn — and refreshing the one canvas.Image in
// place. Decoding every frame up front into RGBA is the memory bomb the caps
// below exist to avoid; the composed buffer is the only full-size pixel
// allocation alive while playing.

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/gif"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
)

// What a GIF may cost to play. The encoded bytes are already capped by the
// cache (its gifMaxBytes); these cap what decoding and composing them may
// allocate. Over any of them the file stays a still — refused, not truncated.
const (
	// gifMaxCanvasPixels bounds the logical canvas, which is the composed
	// buffer's size and the texture uploaded per tick. 4 Mpx is past 1080p;
	// a chat GIF is drawn a few hundred pixels wide.
	gifMaxCanvasPixels = 4 << 20

	// gifMaxFrames and gifMaxTotalPixels bound what DecodeAll keeps resident
	// while playing: paletted frames, one byte per pixel plus a header each.
	gifMaxFrames      = 4096
	gifMaxTotalPixels = 64 << 20
)

// gifStillDelay is the delay played for a frame that declares none, or one so
// short no renderer honours it — the browsers' rule, so a GIF authored against
// them moves at the speed its author saw.
const gifStillDelay = 100 * time.Millisecond

// gifCandidate reports whether an image file is worth offering a player: the
// server recorded it as a GIF, its name or URL says so, or it is an unfurl's
// picture that says nothing at all. The fetch would refuse anything else by
// magic anyway, so this is only what keeps hovers over pictures that announce
// themselves as something else from fetching them twice.
func gifCandidate(file *domain.File) bool {
	if file == nil || file.Kind != domain.FileImage || file.URL == "" {
		return false
	}
	if strings.EqualFold(file.ContentType, "image/gif") {
		return true
	}
	if strings.HasSuffix(strings.ToLower(file.Name), ".gif") {
		return true
	}

	path, _, _ := strings.Cut(file.URL, "?")
	if strings.HasSuffix(strings.ToLower(path), ".gif") {
		return true
	}

	// A foreign picture that says nothing about itself is offered one anyway: an
	// unfurl names a page as often as a file — a gifbox GIF arrives with no
	// extension and no type — and its URL here is the proxy's, whose own path
	// says nothing either. Being wrong costs one request, which the magic check
	// refuses and the animator remembers as failed.
	return file.Foreign && !strings.Contains(file.Name, ".")
}

// gifAnimator plays a GIF into the picture an imageFrame (or a picker tile's
// frame) holds. It is a driver, not a widget: hover reaches it through whatever
// Hoverable already frames the picture — a second Hoverable inside one would
// steal the frame's own (innermost wins).
//
// Every field is UI-thread only. The fetch and decode run on a worker and hop
// back through DoOnUI before touching any of them.
type gifAnimator struct {
	images *cache.ImageCache
	id     string
	url    string
	frame  *fyne.Container

	/* Playback */

	playing bool
	loading bool
	failed  bool // the file itself is unplayable; a network miss retries instead

	frames  *gif.GIF
	buf     *image.RGBA // the composition canvas, reused every tick
	saved   *image.RGBA // snapshot for DisposalPrevious, allocated only if used
	picture *canvas.Image
	still   image.Image
	idx     int

	// gen invalidates scheduled ticks: stop bumps it, and a timer that already
	// fired finds itself stale rather than repainting a reset animator — the
	// fired-timer trap internal/ui/CLAUDE.md names.
	gen   uint64
	timer *time.Timer
}

// newGIFAnimator returns a driver for the GIF at url, or nil where there is
// nothing to drive. frame is the container whose last object becomes the
// mounted canvas.Image — the animator finds it at play time, so it tolerates
// the picture landing after construction.
func newGIFAnimator(images *cache.ImageCache, id, url string, frame *fyne.Container) *gifAnimator {
	if images == nil || id == "" || url == "" || frame == nil {
		return nil
	}

	return &gifAnimator{images: images, id: id, url: url, frame: frame}
}

// SetPlaying is the hover callback: true starts the animation, false stops it,
// frees the frames and puts the still back at frame zero. Nil-safe, so a
// surface can hand it over without knowing whether its picture animates.
func (a *gifAnimator) SetPlaying(on bool) {
	if a == nil {
		return
	}
	if !on {
		a.stop()
		return
	}
	if a.playing || a.failed || !a.capture() {
		return
	}
	a.playing = true

	if a.loading {
		// The previous hover's fetch is still on its way; its landing checks
		// playing and adopts.
		return
	}
	a.loading = true

	go func() {
		raw := a.images.GIF(a.id, a.url)
		frames, err := decodeGIFFrames(raw)

		DoOnUI(func() {
			a.loading = false
			if err != nil {
				a.failed = true
				return
			}
			if frames == nil || !a.playing || !a.capture() {
				return
			}

			a.frames = frames
			a.buf = image.NewRGBA(image.Rect(0, 0, frames.Config.Width, frames.Config.Height))
			a.idx = 0
			a.gen++
			a.tick(a.gen)
		})
	}()
}

// capture finds the mounted picture, which imageFrame swaps in after the still
// loads — so a hover before it lands plays nothing and the next one plays.
func (a *gifAnimator) capture() bool {
	objects := a.frame.Objects
	if len(objects) == 0 {
		return false
	}

	picture, ok := objects[len(objects)-1].(*canvas.Image)
	if !ok {
		return false
	}
	if picture != a.picture {
		a.picture = picture
		a.still = picture.Image
	}

	return true
}

func (a *gifAnimator) stop() {
	if !a.playing {
		return
	}
	a.playing = false
	a.gen++

	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	if a.picture != nil && a.buf != nil && a.picture.Image == a.buf {
		a.picture.Image = a.still
		a.picture.Refresh()
	}

	a.frames = nil
	a.buf = nil
	a.saved = nil
	a.idx = 0
}

// tick composes and shows the current frame, then arms the next. Frame delays
// vary per frame, so this is a rescheduled timer rather than a fyne.Animation,
// whose tick is a fraction of one fixed duration.
func (a *gifAnimator) tick(gen uint64) {
	if gen != a.gen || !a.playing || a.frames == nil {
		return
	}

	// A discarded widget hears nothing (internal/ui/CLAUDE.md), so the tick is
	// what notices: a picture no canvas holds is a row rebuilt or dropped.
	if fyne.CurrentApp().Driver().CanvasForObject(a.picture) == nil {
		a.stop()
		return
	}

	a.compose(a.idx)
	a.picture.Image = a.buf
	a.picture.Refresh()

	delay := a.delay(a.idx)
	// Looping deliberately ignores LoopCount: hover is the reader asking to
	// watch, and a hover outliving a play-once GIF frozen on its last frame
	// reads as broken.
	a.idx = (a.idx + 1) % len(a.frames.Image)

	a.timer = time.AfterFunc(delay, func() { DoOnUI(func() { a.tick(gen) }) })
}

// compose draws frame i onto the canvas: the previous frame's disposal first,
// then the patch — GIF frames are diffs against what is already there, which is
// what makes one buffer the whole of playback's pixel cost.
func (a *gifAnimator) compose(i int) {
	if i == 0 {
		draw.Draw(a.buf, a.buf.Rect, image.Transparent, image.Point{}, draw.Src)
		a.saved = nil
	} else {
		switch previous := a.frames.Image[i-1]; a.disposal(i - 1) {
		case gif.DisposalBackground:
			draw.Draw(a.buf, previous.Rect, image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			if a.saved != nil {
				copy(a.buf.Pix, a.saved.Pix)
			}
		}
	}

	if a.disposal(i) == gif.DisposalPrevious {
		if a.saved == nil {
			a.saved = image.NewRGBA(a.buf.Rect)
		}
		copy(a.saved.Pix, a.buf.Pix)
	}

	frame := a.frames.Image[i]
	draw.Draw(a.buf, frame.Rect, frame, frame.Rect.Min, draw.Over)
}

func (a *gifAnimator) disposal(i int) byte {
	if i >= len(a.frames.Disposal) {
		return gif.DisposalNone
	}

	return a.frames.Disposal[i]
}

func (a *gifAnimator) delay(i int) time.Duration {
	if i >= len(a.frames.Delay) || a.frames.Delay[i] <= 1 {
		return gifStillDelay
	}

	return time.Duration(a.frames.Delay[i]) * 10 * time.Millisecond
}

// decodeGIFFrames decodes the encoded bytes under the playback caps. Nil bytes
// are a miss, not a failure — (nil, nil), retried on the next hover. An error
// is the file's own and is memoised as failed. Call off the UI thread: a
// decode is tens of milliseconds of LZW.
func decodeGIFFrames(raw []byte) (*gif.GIF, error) {
	if raw == nil {
		return nil, nil
	}

	frames, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	if len(frames.Image) < 2 {
		return nil, errors.New("gif: nothing to animate")
	}
	if len(frames.Image) > gifMaxFrames {
		return nil, errors.New("gif: too many frames")
	}
	if int64(frames.Config.Width)*int64(frames.Config.Height) > gifMaxCanvasPixels {
		return nil, errors.New("gif: canvas too large")
	}

	var total int64
	for _, frame := range frames.Image {
		total += int64(frame.Rect.Dx()) * int64(frame.Rect.Dy())
	}
	if total > gifMaxTotalPixels {
		return nil, errors.New("gif: too many pixels")
	}

	return frames, nil
}
