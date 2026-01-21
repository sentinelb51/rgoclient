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
	label   string
	onTap   func()
	onHover func(bool)
	bg      *canvas.Rectangle
	text    *canvas.Text
}

// assertions
var _ fyne.Tappable = (*swiftActionButton)(nil)
var _ desktop.Hoverable = (*swiftActionButton)(nil)

func newSwiftActionButton(label string, onTap func(), onHover func(bool)) *swiftActionButton {
	bg := canvas.NewRectangle(color.Transparent)
	// Make slightly thinner vertically (80% of width)
	height := theme.Sizes.SwiftActionSize * 0.8
	bg.SetMinSize(fyne.NewSize(theme.Sizes.SwiftActionSize, height))

	text := canvas.NewText(label, theme.Colors.SwiftActionText)
	text.Alignment = fyne.TextAlignCenter
	text.TextSize = 14

	b := &swiftActionButton{
		label:   label,
		onTap:   onTap,
		onHover: onHover,
		bg:      bg,
		text:    text,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *swiftActionButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.bg, container.NewCenter(b.text)))
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
