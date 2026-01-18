package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// Ensure XButton implements necessary interfaces.
var (
	_ fyne.Widget       = (*XButton)(nil)
	_ fyne.Tappable     = (*XButton)(nil)
	_ desktop.Hoverable = (*XButton)(nil)
)

// XButton is a simple drawn X button for removing items.
type XButton struct {
	widget.BaseWidget
	onTap   func()
	hovered bool
}

// NewXButton creates a new X button with the given tap handler.
func NewXButton(onTap func()) *XButton {
	button := &XButton{onTap: onTap}
	button.ExtendBaseWidget(button)
	return button
}

// CreateRenderer returns the renderer for this widget.
func (button *XButton) CreateRenderer() fyne.WidgetRenderer {
	lineColor := color.RGBA{R: 150, G: 150, B: 150, A: 255}

	line1 := canvas.NewLine(lineColor)
	line1.StrokeWidth = 2

	line2 := canvas.NewLine(lineColor)
	line2.StrokeWidth = 2

	return &xButtonRenderer{
		button: button,
		line1:  line1,
		line2:  line2,
	}
}

// Tapped handles tap events on the widget.
func (button *XButton) Tapped(*fyne.PointEvent) {
	if button.onTap != nil {
		button.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (button *XButton) MouseIn(*desktop.MouseEvent) {
	button.hovered = true
	button.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (button *XButton) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (button *XButton) MouseOut() {
	button.hovered = false
	button.Refresh()
}

// MinSize returns the minimum size for this widget.
func (button *XButton) MinSize() fyne.Size {
	return fyne.NewSize(24, 24)
}

type xButtonRenderer struct {
	button *XButton
	line1  *canvas.Line
	line2  *canvas.Line
}

// Layout positions objects within this renderer.
func (renderer *xButtonRenderer) Layout(size fyne.Size) {
	padding := float32(6)

	// Calculate exact center and line endpoints for symmetry
	centerX := size.Width / 2
	centerY := size.Height / 2
	halfLine := (size.Width - 2*padding) / 2

	// Position lines from center
	renderer.line1.Position1 = fyne.NewPos(centerX-halfLine, centerY-halfLine)
	renderer.line1.Position2 = fyne.NewPos(centerX+halfLine, centerY+halfLine)

	renderer.line2.Position1 = fyne.NewPos(centerX+halfLine, centerY-halfLine)
	renderer.line2.Position2 = fyne.NewPos(centerX-halfLine, centerY+halfLine)
}

// MinSize returns the minimum size for this renderer.
func (renderer *xButtonRenderer) MinSize() fyne.Size {
	return renderer.button.MinSize()
}

// Refresh refreshes this renderer.
func (renderer *xButtonRenderer) Refresh() {
	// Update line colors based on hover state
	lineColor := color.RGBA{R: 150, G: 150, B: 150, A: 255}
	if renderer.button.hovered {
		lineColor = color.RGBA{R: 255, G: 100, B: 100, A: 255}
	}
	renderer.line1.StrokeColor = lineColor
	renderer.line2.StrokeColor = lineColor
	canvas.Refresh(renderer.line1)
	canvas.Refresh(renderer.line2)
}

// Objects returns the objects in this renderer.
func (renderer *xButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.line1, renderer.line2}
}

// Destroy cleans up this renderer.
func (renderer *xButtonRenderer) Destroy() {}
