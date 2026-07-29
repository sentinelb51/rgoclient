package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// Overlay is a modal layer drawn over the whole window: a dimmed backdrop with
// content centred on it. Fyne sizes an overlay to the canvas when it is pushed
// onto Canvas().Overlays(), and routes every pointer event to the top-most
// overlay, so the backdrop both dims and blocks the UI underneath. Tapping it
// dismisses the overlay.
//
// It is a plain widget rather than a widget.PopUp because a pop-up draws its own
// themed card (padding, radius, shadow) around the content and paints its
// backdrop from the theme's shadow colour — a light seam tint here, far too
// faint to read as a lightbox.
type Overlay struct {
	tapBase
	backdrop *canvas.Rectangle
	content  fyne.CanvasObject
}

var (
	_ fyne.Tappable      = (*Overlay)(nil)
	_ desktop.Cursorable = (*Overlay)(nil)
)

// NewOverlay creates a modal layer showing content, dismissed by tapping the
// backdrop around it.
func NewOverlay(content fyne.CanvasObject, onDismiss func()) *Overlay {
	o := &Overlay{
		backdrop: canvas.NewRectangle(theme.Colors.OverlayBackdrop),
		content:  content,
	}
	o.onTap = onDismiss
	o.ExtendBaseWidget(o)
	return o
}

func (o *Overlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(o.backdrop, container.NewCenter(o.content)))
}

// Cursor keeps the normal pointer over the backdrop: tapBase advertises the
// hand cursor for things that look clickable, and a dimmed background isn't one.
func (o *Overlay) Cursor() desktop.Cursor { return desktop.DefaultCursor }

// tapSink swallows taps on whatever it wraps. Fyne delivers a tap to the
// deepest object that accepts one, so without a sink a click anywhere on
// non-interactive content inside an Overlay — the image itself, most of the
// card — would fall through to the backdrop and dismiss it. Buttons nested
// deeper still win over the sink, and so keep working.
type tapSink struct {
	tapBase
	content fyne.CanvasObject
}

var (
	_ fyne.Tappable      = (*tapSink)(nil)
	_ desktop.Cursorable = (*tapSink)(nil)
)

func newTapSink(content fyne.CanvasObject) *tapSink {
	s := &tapSink{content: content}
	s.ExtendBaseWidget(s)
	return s
}

func (s *tapSink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

func (s *tapSink) Cursor() desktop.Cursor { return desktop.DefaultCursor }
