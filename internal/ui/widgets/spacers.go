package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// HorizontalSpacer creates a transparent horizontal space with the given width.
// This is useful for fixed-pixel spacing between widgets.
func HorizontalSpacer(width float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 0))
	return spacer
}

// NewHSpacer is deprecated. Use HorizontalSpacer instead.
func NewHSpacer(width float32) fyne.CanvasObject {
	return HorizontalSpacer(width)
}

// VerticalSpacer creates a transparent vertical space with the given height.
// This is useful for fixed-pixel spacing between widgets.
func VerticalSpacer(height float32) fyne.CanvasObject {
	rectangle := canvas.NewRectangle(color.Transparent)
	rectangle.SetMinSize(fyne.NewSize(0, height))
	return rectangle
}

// NewVSpacer is deprecated. Use VerticalSpacer instead.
func NewVSpacer(height float32) fyne.CanvasObject {
	return VerticalSpacer(height)
}
