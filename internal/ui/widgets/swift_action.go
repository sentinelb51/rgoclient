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

// swiftActionButton is a simple widget for swift actions (Reply, Delete, Edit).
type swiftActionButton struct {
	widget.BaseWidget
	iconPath string
	onTap    func()
	onHover  func(bool)
	bg       *canvas.Rectangle
	icon     *canvas.Image
}

// assertions
var _ fyne.Tappable = (*swiftActionButton)(nil)
var _ desktop.Hoverable = (*swiftActionButton)(nil)

func newSwiftActionButton(iconPath string, onTap func(), onHover func(bool)) *swiftActionButton {
	size := theme.Sizes.SwiftActionSize

	// Background: Flatter aspect ratio (80% height)
	bg := canvas.NewRectangle(color.Transparent)
	bg.SetMinSize(fyne.NewSize(size, size*0.8))

	icon := canvas.NewImageFromFile(iconPath)
	icon.FillMode = canvas.ImageFillContain
	icon.ScaleMode = canvas.ImageScaleSmooth

	// Icon: 70% of button size to provide padding
	// This scaling is consistent with manual padding approaches in fixed-size widgets
	iconSize := size * 0.7
	icon.SetMinSize(fyne.NewSize(iconSize, iconSize))

	b := &swiftActionButton{
		iconPath: iconPath,
		onTap:    onTap,
		onHover:  onHover,
		bg:       bg,
		icon:     icon,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *swiftActionButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.bg, container.NewCenter(b.icon)))
}

func (b *swiftActionButton) Tapped(_ *fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *swiftActionButton) MouseIn(_ *desktop.MouseEvent) {
	b.bg.FillColor = theme.Colors.SwiftActionHoverBg
	b.bg.Refresh()
	if b.onHover != nil {
		b.onHover(true)
	}
}

func (b *swiftActionButton) MouseMoved(_ *desktop.MouseEvent) {}

func (b *swiftActionButton) MouseOut() {
	b.bg.FillColor = color.Transparent
	b.bg.Refresh()
	if b.onHover != nil {
		b.onHover(false)
	}
}
