package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// Compile-time interface assertions.
var (
	_ fyne.Widget       = (*TappableContainer)(nil)
	_ fyne.Tappable     = (*TappableContainer)(nil)
	_ desktop.Hoverable = (*TappableContainer)(nil)
)

// TappableContainer wraps a container to make it tappable with hover effects.
type TappableContainer struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
	onTap      func()
	hovered    bool
}

// NewTappableContainer creates a new tappable container.
func NewTappableContainer(content fyne.CanvasObject, onTap func()) *TappableContainer {
	t := &TappableContainer{
		content:    content,
		background: canvas.NewRectangle(color.Transparent),
		onTap:      onTap,
	}
	t.ExtendBaseWidget(t)
	return t
}

// CreateRenderer returns the renderer for this widget.
func (t *TappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(t.background, t.content))
}

// Tapped handles tap events on the widget.
func (t *TappableContainer) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (t *TappableContainer) MouseIn(*desktop.MouseEvent) {
	t.hovered = true
	t.background.FillColor = theme.Colors.TappableHoverBg
	t.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (t *TappableContainer) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (t *TappableContainer) MouseOut() {
	t.hovered = false
	t.background.FillColor = color.Transparent
	t.background.Refresh()
}
