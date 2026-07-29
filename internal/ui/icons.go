package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// newScaledIcon builds a smoothly scaled canvas image for a resource (a Fyne
// theme icon or one of the embedded assets). A positive side pins it to that
// square minimum; zero leaves it free to take whatever space its parent gives.
// Every icon-bearing widget in this package draws through here so they share one
// fill/scale policy.
func newScaledIcon(res fyne.Resource, side float32) *canvas.Image {
	icon := canvas.NewImageFromResource(res)
	icon.FillMode = canvas.ImageFillContain
	icon.ScaleMode = canvas.ImageScaleSmooth
	if side > 0 {
		icon.SetMinSize(fyne.NewSize(side, side))
	}
	return icon
}
