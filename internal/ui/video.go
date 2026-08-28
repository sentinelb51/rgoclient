package ui

// The video card: the poster in an attachment's box, a play badge, a duration
// chip, and — while something is playing — a scrub strip and a sound toggle.
// The card is the dumb half: every decision (fetching, probing, decoding,
// what a tap starts) is the controller's, reached through the OnVideo*
// actions, and what comes back is pushed into these setters on the UI thread.
// Playback paints the way a GIF does — one reusable RGBA buffer into one
// mounted canvas.Image, Refresh per frame — because that is the path
// docs/performance.md already prices.

import (
	"fmt"
	"image"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// FileCacheID is the id a file's cached renditions are keyed under, exported
// for the controller keying what it computes about a file — a video's poster
// — beside what the widgets cache about it.
func FileCacheID(file *domain.File) string { return fileCacheID(file) }

// VideoCard is one video's surface — an attachment's or an embed's. Every
// field and method is UI thread only; the controller hops before touching one.
type VideoCard struct {
	File *domain.File

	// Loop marks a video with a GIF's manners — an unfurl the provider calls
	// one — which the controller reads as: decode forever, silent by default.
	Loop bool

	/* The picture box */

	frame       *fyne.Container
	placeholder *canvas.Rectangle
	picture     *canvas.Image // mounted once a poster or a frame exists
	still       image.Image   // the poster, restored when playback ends
	playBuf     *image.RGBA   // the playback canvas, reused every frame
	box         fyne.Size
	known       bool // box fitted from real dimensions rather than reserved

	/* Chrome */

	badge     *fyne.Container
	badgeIcon *canvas.Image
	chip      *fyne.Container
	chipText  *canvas.Text
	scrub     *videoScrub
	mute      *GlyphButton

	/* State */

	duration time.Duration
	status   string
	active   bool // a playback session exists on this card
	muted    bool

	onMute func(bool)
}

// buildVideoAttachment is the video branch of buildAttachment: the card in
// the image attachment's bounds, and the bar beneath it grown an open-with
// button. The mount is announced so the controller can fill the poster and
// duration in under its own policy.
func buildVideoAttachment(deps Deps, file *domain.File) (*fyne.Container, *VideoCard) {
	content, card := newVideoCard(deps, file)
	deps.Actions.OnVideoMounted(file, card)

	return content, card
}

// buildEmbedVideo is the same card standing in an embed, wearing the tap and
// menu wiring an attachment's stack carries — an embed is otherwise inert. The
// unfurl's own poster fills the box straight away where there is one; the
// probed poster replaces nothing better than a placeholder.
func buildEmbedVideo(deps Deps, embed *domain.Embed, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	file := embed.Video
	content, card := newVideoCard(deps, file)

	if embed.GIF {
		card.Loop = true
		card.muted = true
		card.mute.icon.Resource = assets.SpeakerOffIcon
	}
	if embed.Image != nil && embed.Image.URL != "" {
		poster := embed.Image
		deps.Images.LoadAsync(fileCacheID(poster), poster.URL, false, card.SetPoster)
	}
	deps.Actions.OnVideoMounted(file, card)

	stack := NewHoverableStack(content, func() { deps.Actions.OnVideoTapped(file, card) }, nil)
	stack.onSecondaryTap = onMenu

	return stack
}

// newVideoCard builds the surface both of those share.
func newVideoCard(deps Deps, file *domain.File) (*fyne.Container, *VideoCard) {
	bounds := fyne.NewSize(theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight)
	reserve := fyne.NewSize(bounds.Width, bounds.Height/2)

	w := &VideoCard{File: file}

	w.box = fitWithin(file.Width, file.Height, bounds.Width, bounds.Height)
	w.known = w.box.Width > 0 && w.box.Height > 0
	if !w.known {
		w.box = reserve
	}

	w.placeholder = canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	w.placeholder.SetMinSize(w.box)
	w.frame = container.NewStack(w.placeholder)

	w.badgeIcon = newScaledIcon(assets.PlayIcon, theme.Sizes.VideoBadgeSize/2)
	disc := canvas.NewCircle(theme.Colors.VideoScrim)
	w.badge = container.NewStack(disc, container.NewCenter(w.badgeIcon))

	w.chipText = newText("", theme.Colors.TextPrimary, theme.Sizes.ChipTextSize)
	chipBg := canvas.NewRectangle(theme.Colors.VideoScrim)
	chipBg.CornerRadius = theme.Sizes.ChipRadius
	pad := theme.Sizes.ChipPaddingH
	w.chip = container.NewStack(chipBg, NewInset(w.chipText, theme.Sizes.ChipPaddingV, theme.Sizes.ChipPaddingV, pad, pad))
	w.chip.Hide()

	w.scrub = newVideoScrub(func(frac float64) { deps.Actions.OnVideoSeek(file, w, frac) })
	w.scrub.Hide()

	w.mute = NewGlyphButton(assets.SpeakerIcon, func() { w.toggleMuted() })
	w.mute.saying(deps.Tooltip, "Sound")
	w.mute.Hide()
	w.onMute = func(muted bool) { deps.Actions.OnVideoMuted(file, w, muted) }

	chrome := container.New(&videoChromeLayout{card: w}, w.scrub, w.chip, w.mute, w.badge)

	openButton := NewGlyphButton(assets.ActionOpenIcon, func() { deps.Actions.OnVideoOpen(file, w) })
	openButton.saying(deps.Tooltip, "Open in your player")
	bar := videoBar(file.Name, file.Size, openButton)

	content := VBoxNoSpacing(container.NewStack(w.frame, chrome), bar)

	return content, w
}

// videoBar is attachmentBar with a control seated at its trailing edge. An
// embed's video has no size to state — the unfurl does not carry one — and
// says nothing rather than "0 B".
func videoBar(name string, size int, trailing fyne.CanvasObject) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.SetMinSize(fyne.NewSize(0, attachmentBarHeight))

	stated := ""
	if size > 0 {
		stated = util.FormatFileSize(size)
	}

	nameLabel := newBoldText(name, theme.Colors.TextPrimary, attachmentTextSize)
	sizeLabel := newText(stated, theme.Colors.TimestampText, attachmentTextSize)
	sizeLabel.Alignment = fyne.TextAlignTrailing

	left := container.NewHBox(HorizontalSpacer(8), nameLabel)
	right := container.NewHBox(sizeLabel, vcenter(trailing), HorizontalSpacer(4))

	return container.NewStack(background, container.NewBorder(nil, nil, left, right))
}

/* What the controller pushes in */

// SetInfo records the probed shape: the box takes the real aspect where the
// server sent no dimensions, and the chip gains the running time. Zero
// duration is the container not saying, and the chip stays what it was.
func (w *VideoCard) SetInfo(width, height int, duration time.Duration) {
	if !w.known && width > 0 && height > 0 {
		bounds := fyne.NewSize(theme.Sizes.MessageImageMaxWidth, theme.Sizes.MessageImageMaxHeight)
		w.box = fitWithin(width, height, bounds.Width, bounds.Height)
		w.known = true
		w.placeholder.SetMinSize(w.box)
		if w.picture != nil {
			w.picture.SetMinSize(w.box)
		}
		w.frame.Refresh()
	}

	if duration > 0 {
		w.duration = duration
	}
	w.syncChip()
}

// SetPoster mounts the first frame as the card's resting picture.
func (w *VideoCard) SetPoster(img image.Image) {
	if img == nil {
		return
	}
	w.still = img
	if !w.active {
		w.ensurePicture(img)
	}
}

// SetStatus puts a line in the chip's place — what a fetch or a failure has
// to say. Empty restores the duration.
func (w *VideoCard) SetStatus(status string) {
	w.status = status
	w.syncChip()
}

// DecodeSize is the box in whole pixels — what a decode is asked to emit, so
// the pipe carries the card's size and nothing larger.
func (w *VideoCard) DecodeSize() (int, int) {
	return max(int(w.box.Width), 1), max(int(w.box.Height), 1)
}

// Muted reports the sound toggle, read when playback starts.
func (w *VideoCard) Muted() bool { return w.muted }

// Mounted reports whether the card is still on a canvas. A discarded widget
// hears nothing, so the frame pump asks this each paint and stops itself.
func (w *VideoCard) Mounted() bool {
	return fyne.CurrentApp().Driver().CanvasForObject(w.placeholder) != nil
}

/* Playback state */

// ShowPlaying is frames arriving: the badge goes, the transport chrome comes.
// A video whose container never said its length gets no scrub — a strip that
// cannot place a seek only promises one.
func (w *VideoCard) ShowPlaying(hasSound bool) {
	w.active = true
	w.status = ""
	w.badge.Hide()
	if w.duration > 0 {
		w.scrub.Show()
	}
	if hasSound {
		w.mute.Show()
	}
	w.syncChip()
}

// ShowPaused holds the frame and offers the play badge again.
func (w *VideoCard) ShowPaused() {
	w.setBadge(assets.PlayIcon)
	w.badge.Show()
	w.badge.Refresh()
}

// ShowFrame paints one decoded frame. pix is the pump's scratch, valid only
// for this call, so it is copied into the card's own buffer — which is also
// what keeps the renderer off bytes the pump is about to overwrite.
func (w *VideoCard) ShowFrame(pix []byte) {
	width, height := w.DecodeSize()
	if len(pix) != width*height*4 {
		return
	}
	if w.playBuf == nil || w.playBuf.Rect.Dx() != width || w.playBuf.Rect.Dy() != height {
		w.playBuf = image.NewRGBA(image.Rect(0, 0, width, height))
	}
	copy(w.playBuf.Pix, pix)

	if w.badge.Visible() {
		w.badge.Hide()
	}
	w.ensurePicture(w.playBuf)
	w.picture.Refresh()
}

// SetProgress moves the scrub and the chip's clock. Call once a second or so;
// the paint rides the frame refresh that is already dirtying the canvas.
func (w *VideoCard) SetProgress(elapsed time.Duration) {
	if w.duration > 0 {
		w.scrub.setFraction(float64(elapsed) / float64(w.duration))
	}
	if w.status == "" && w.active {
		text := formatClock(elapsed)
		if w.duration > 0 {
			text += " / " + formatClock(w.duration)
		}
		w.setChip(text)
	}
}

// EndPlayback puts the card back at rest: poster, badge, no transport. The
// playback buffer goes with the session, as a GIF's frames do.
func (w *VideoCard) EndPlayback() {
	w.active = false
	w.playBuf = nil
	w.scrub.setFraction(0)
	w.scrub.Hide()
	w.mute.Hide()
	w.setBadge(assets.PlayIcon)
	w.badge.Show()

	if w.picture != nil {
		w.picture.Image = w.still
		if w.still == nil {
			w.frame.Objects = []fyne.CanvasObject{w.placeholder}
		}
		w.frame.Refresh()
	}
	w.syncChip()
	w.badge.Refresh()
}

/* Internals */

// ensurePicture mounts the canvas.Image on first use, the way imageFrame's
// load callback does, and points it at img thereafter.
func (w *VideoCard) ensurePicture(img image.Image) {
	if w.picture == nil {
		w.picture = canvas.NewImageFromImage(img)
		w.picture.FillMode = canvas.ImageFillContain
		w.picture.ScaleMode = canvas.ImageScaleSmooth
		w.picture.SetMinSize(w.box)
		w.frame.Objects = []fyne.CanvasObject{w.placeholder, w.picture}
		w.frame.Refresh()
		return
	}

	if w.picture.Image != img {
		w.picture.Image = img
		w.picture.Refresh()
	}
}

func (w *VideoCard) toggleMuted() {
	w.muted = !w.muted

	icon := assets.SpeakerIcon
	if w.muted {
		icon = assets.SpeakerOffIcon
	}
	w.mute.icon.Resource = icon
	w.mute.icon.Refresh()

	if w.onMute != nil {
		w.onMute(w.muted)
	}
}

func (w *VideoCard) setBadge(res fyne.Resource) {
	if w.badgeIcon.Resource != res {
		w.badgeIcon.Resource = res
		w.badgeIcon.Refresh()
	}
}

func (w *VideoCard) setChip(text string) {
	if text == "" {
		w.chip.Hide()
		return
	}
	if w.chipText.Text != text {
		w.chipText.Text = text
		w.chipText.Refresh()
	}
	if !w.chip.Visible() {
		w.chip.Show()
	}
	w.chip.Refresh()
}

// syncChip re-decides what the chip says: a status outranks the clock, and a
// card with neither wears no chip at all.
func (w *VideoCard) syncChip() {
	switch {
	case w.status != "":
		w.setChip(w.status)
	case w.duration > 0 && !w.active:
		w.setChip(formatClock(w.duration))
	case !w.active:
		w.setChip("")
	}
}

// formatClock is a running time the way every player writes one: M:SS, with
// an hour part once there is one.
func formatClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second) / time.Second)
	h, m, s := total/3600, (total/60)%60, total%60

	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}

	return fmt.Sprintf("%d:%02d", m, s)
}

/* Chrome layout */

// videoChromeLayout hangs the card's controls over the picture: the scrub
// strip along the bottom edge, the chip in the lower-left corner above it,
// the sound toggle in the upper-right, the badge in the centre. It reports no
// minimum — the picture box under it is what sizes the stack.
type videoChromeLayout struct {
	card *VideoCard
}

const videoChromeInset = 6

func (l *videoChromeLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (l *videoChromeLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	w := l.card

	scrubHeight := theme.Sizes.VideoScrubHeight
	w.scrub.Resize(fyne.NewSize(size.Width, scrubHeight))
	w.scrub.Move(fyne.NewPos(0, size.Height-scrubHeight))

	chip := w.chip.MinSize()
	w.chip.Resize(chip)
	chipBottom := size.Height - videoChromeInset - chip.Height
	if w.scrub.Visible() {
		chipBottom = size.Height - scrubHeight - chip.Height
	}
	w.chip.Move(fyne.NewPos(videoChromeInset, chipBottom))

	mute := w.mute.MinSize()
	w.mute.Resize(mute)
	w.mute.Move(fyne.NewPos(size.Width-videoChromeInset-mute.Width, videoChromeInset))

	badge := fyne.NewSize(theme.Sizes.VideoBadgeSize, theme.Sizes.VideoBadgeSize)
	w.badge.Resize(badge)
	w.badge.Move(fyne.NewPos((size.Width-badge.Width)/2, (size.Height-badge.Height)/2))
}

/* Scrub strip */

// videoScrub is the thin seek strip along the card's bottom edge: a track,
// the played span in the accent, and a tap that names a fraction. The strip
// is taller than the line it draws — the gap above is the hover room a
// 3-pixel target does not have, the stateBar's arrangement.
type videoScrub struct {
	widget.BaseWidget

	track  *canvas.Rectangle
	fill   *canvas.Rectangle
	frac   float64
	onSeek func(frac float64)
}

var (
	_ fyne.Tappable      = (*videoScrub)(nil)
	_ desktop.Cursorable = (*videoScrub)(nil)
)

func newVideoScrub(onSeek func(float64)) *videoScrub {
	s := &videoScrub{
		track:  canvas.NewRectangle(theme.Colors.VideoScrim),
		fill:   canvas.NewRectangle(ToneInfo.Color()),
		onSeek: onSeek,
	}
	s.ExtendBaseWidget(s)

	return s
}

func (s *videoScrub) CreateRenderer() fyne.WidgetRenderer {
	return &videoScrubRenderer{scrub: s}
}

func (s *videoScrub) Tapped(event *fyne.PointEvent) {
	width := s.Size().Width
	if s.onSeek == nil || width <= 0 {
		return
	}

	frac := float64(event.Position.X / width)
	s.onSeek(min(max(frac, 0), 1))
}

func (s *videoScrub) Cursor() desktop.Cursor { return desktop.PointerCursor }

// setFraction moves the played span. The resize is the placement a layout
// would do; the refresh rides on the canvas already being dirtied by the
// frame that moved the clock.
func (s *videoScrub) setFraction(frac float64) {
	s.frac = min(max(frac, 0), 1)
	s.placeFill()
	s.fill.Refresh()
}

func (s *videoScrub) placeFill() {
	size := s.Size()
	line := theme.Sizes.VideoScrubLine

	s.track.Resize(fyne.NewSize(size.Width, line))
	s.track.Move(fyne.NewPos(0, size.Height-line))
	s.fill.Resize(fyne.NewSize(size.Width*float32(s.frac), line))
	s.fill.Move(fyne.NewPos(0, size.Height-line))
}

type videoScrubRenderer struct {
	scrub *videoScrub
}

func (r *videoScrubRenderer) Layout(fyne.Size) { r.scrub.placeFill() }
func (r *videoScrubRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, theme.Sizes.VideoScrubHeight)
}
func (r *videoScrubRenderer) Refresh() { r.scrub.placeFill() }
func (r *videoScrubRenderer) Destroy() {}
func (r *videoScrubRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.scrub.track, r.scrub.fill}
}
