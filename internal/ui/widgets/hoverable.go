package widgets

import (
	"image/color"

	"RGOClient/internal/ui/theme"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// HoverableStack is a minimal custom widget to handle hover events for attachments.
type HoverableStack struct {
	widget.BaseWidget
	content *fyne.Container
	bg      *canvas.Rectangle
	onHover func(bool)
	onTap   func()
}

var _ fyne.Widget = (*HoverableStack)(nil)
var _ desktop.Hoverable = (*HoverableStack)(nil)
var _ fyne.Tappable = (*HoverableStack)(nil)

func NewHoverableStack(content *fyne.Container, onTap func(), onHover func(bool)) *HoverableStack {
	bg := canvas.NewRectangle(color.Transparent)
	bg.StrokeColor = theme.Colors.ServerListBackground // Default border color (subtle or transparent)
	bg.StrokeWidth = 0

	h := &HoverableStack{
		content: content,
		bg:      bg,
		onHover: onHover,
		onTap:   onTap,
	}
	h.ExtendBaseWidget(h)
	return h
}

func (h *HoverableStack) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(h.content, h.bg))
}

func (h *HoverableStack) MouseIn(*desktop.MouseEvent) {
	if h.onHover != nil {
		h.onHover(true)
	}
	h.bg.StrokeColor = color.Black
	h.bg.StrokeWidth = 1
	h.bg.Refresh()
}

func (h *HoverableStack) MouseOut() {
	if h.onHover != nil {
		h.onHover(false)
	}
	h.bg.StrokeColor = color.Transparent
	h.bg.StrokeWidth = 0
	h.bg.Refresh()
}

func (h *HoverableStack) MouseMoved(*desktop.MouseEvent) {}

func (h *HoverableStack) Tapped(*fyne.PointEvent) {
	if h.onTap != nil {
		h.onTap()
	}
}
