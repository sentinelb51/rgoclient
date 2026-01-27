package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// Compile-time interface assertions.
var (
	_ fyne.Widget       = (*CloseButton)(nil)
	_ fyne.Tappable     = (*CloseButton)(nil)
	_ desktop.Hoverable = (*CloseButton)(nil)
)

// CloseButton is a simple drawn X button for removing/closing items.
type CloseButton struct {
	widget.BaseWidget
	onTap   func()
	hovered bool
}

// NewCloseButton creates a new close button with the given tap handler.
func NewCloseButton(onTap func()) *CloseButton {
	b := &CloseButton{onTap: onTap}
	b.ExtendBaseWidget(b)
	return b
}

// NewXButton is deprecated. Use NewCloseButton instead.
func NewXButton(onTap func()) *CloseButton {
	return NewCloseButton(onTap)
}

// CreateRenderer returns the renderer for this widget.
func (b *CloseButton) CreateRenderer() fyne.WidgetRenderer {
	line1 := canvas.NewLine(theme.Colors.XButtonNormal)
	line1.StrokeWidth = 2

	line2 := canvas.NewLine(theme.Colors.XButtonNormal)
	line2.StrokeWidth = 2

	return &closeButtonRenderer{
		button: b,
		line1:  line1,
		line2:  line2,
	}
}

// Tapped handles tap events on the widget.
func (b *CloseButton) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (b *CloseButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (b *CloseButton) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (b *CloseButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}

// MinSize returns the minimum size for this widget.
func (b *CloseButton) MinSize() fyne.Size {
	size := theme.Sizes.XButtonSize
	return fyne.NewSize(size, size)
}

type closeButtonRenderer struct {
	button *CloseButton
	line1  *canvas.Line
	line2  *canvas.Line
}

// Layout positions objects within this renderer.
func (r *closeButtonRenderer) Layout(size fyne.Size) {
	padding := float32(6)
	centerX := size.Width / 2
	centerY := size.Height / 2
	halfLine := (size.Width - 2*padding) / 2

	r.line1.Position1 = fyne.NewPos(centerX-halfLine, centerY-halfLine)
	r.line1.Position2 = fyne.NewPos(centerX+halfLine, centerY+halfLine)

	r.line2.Position1 = fyne.NewPos(centerX+halfLine, centerY-halfLine)
	r.line2.Position2 = fyne.NewPos(centerX-halfLine, centerY+halfLine)
}

// MinSize returns the minimum size for this renderer.
func (r *closeButtonRenderer) MinSize() fyne.Size {
	return r.button.MinSize()
}

// Refresh refreshes this renderer.
func (r *closeButtonRenderer) Refresh() {
	col := theme.Colors.XButtonNormal
	if r.button.hovered {
		col = theme.Colors.XButtonHover
	}
	r.line1.StrokeColor = col
	r.line2.StrokeColor = col
	canvas.Refresh(r.line1)
	canvas.Refresh(r.line2)
}

// Objects returns the objects in this renderer.
func (r *closeButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.line1, r.line2}
}

// Destroy cleans up this renderer.
func (r *closeButtonRenderer) Destroy() {}
