package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// Ensure TappableContainer implements necessary interfaces.
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

// NewTappableContainer creates a new tappable container with the given content and tap handler.
func NewTappableContainer(content fyne.CanvasObject, onTap func()) *TappableContainer {
	tappable := &TappableContainer{
		content:    content,
		background: canvas.NewRectangle(color.Transparent),
		onTap:      onTap,
	}
	tappable.ExtendBaseWidget(tappable)
	return tappable
}

// CreateRenderer returns the renderer for this widget.
func (tappable *TappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(tappable.background, tappable.content))
}

// Tapped handles tap events on the widget.
func (tappable *TappableContainer) Tapped(*fyne.PointEvent) {
	if tappable.onTap != nil {
		tappable.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (tappable *TappableContainer) MouseIn(*desktop.MouseEvent) {
	tappable.hovered = true
	tappable.background.FillColor = color.RGBA{R: 70, G: 70, B: 70, A: 255}
	tappable.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (tappable *TappableContainer) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (tappable *TappableContainer) MouseOut() {
	tappable.hovered = false
	tappable.background.FillColor = color.Transparent
	tappable.background.Refresh()
}
