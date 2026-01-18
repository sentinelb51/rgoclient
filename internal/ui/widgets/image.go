package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// Image is a clickable image widget that responds to mouse events.
type Image struct {
	widget.BaseWidget
	image   *canvas.Image
	onTap   func()
	hovered bool
}

// NewImage creates a clickable image widget.
func NewImage(source fyne.Resource, onTap func()) *Image {
	img := &Image{
		image: canvas.NewImageFromResource(source),
		onTap: onTap,
	}
	img.ExtendBaseWidget(img)
	return img
}

// Tapped handles tap events.
func (img *Image) Tapped(*fyne.PointEvent) {
	if img.onTap != nil {
		img.onTap()
	}
}

// MouseIn handles mouse entering.
func (img *Image) MouseIn(*desktop.MouseEvent) {
	img.hovered = true
	img.Refresh()
}

// MouseMoved handles mouse movement.
func (img *Image) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving.
func (img *Image) MouseOut() {
	img.hovered = false
	img.Refresh()
}

// CreateRenderer returns the widget renderer.
func (img *Image) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(img.image)
}
