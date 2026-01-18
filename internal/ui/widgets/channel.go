package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/theme"
)

// Compile-time interface assertions.
var (
	_ fyne.Widget       = (*ChannelWidget)(nil)
	_ fyne.Tappable     = (*ChannelWidget)(nil)
	_ desktop.Hoverable = (*ChannelWidget)(nil)
)

// ChannelWidget displays a channel in the sidebar with selection state.
type ChannelWidget struct {
	widget.BaseWidget
	Channel    *revoltgo.Channel
	onTap      func()
	background *canvas.Rectangle
	selected   bool
}

// NewChannelWidget creates a new channel widget.
func NewChannelWidget(channel *revoltgo.Channel, onTap func()) *ChannelWidget {
	w := &ChannelWidget{
		Channel:    channel,
		onTap:      onTap,
		background: canvas.NewRectangle(color.Transparent),
	}
	w.ExtendBaseWidget(w)
	return w
}

// SetSelected updates the selection state and refreshes appearance.
func (w *ChannelWidget) SetSelected(selected bool) {
	w.selected = selected
	w.updateBackground()
}

func (w *ChannelWidget) updateBackground() {
	if w.selected {
		w.background.FillColor = theme.Colors.ChannelSelectedBg
	} else {
		w.background.FillColor = color.Transparent
	}
	w.background.Refresh()
}

// CreateRenderer returns the renderer for this widget.
func (w *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	leftSpacer := canvas.NewRectangle(color.Transparent)
	leftSpacer.SetMinSize(fyne.NewSize(theme.Sizes.ChannelLeftPadding, 0))

	icon := GetHashtagIcon()
	label := widget.NewLabel(w.Channel.Name)
	content := container.NewHBox(leftSpacer, icon, label)

	return widget.NewSimpleRenderer(container.NewStack(w.background, content))
}

// Tapped handles tap events on the widget.
func (w *ChannelWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (w *ChannelWidget) MouseIn(*desktop.MouseEvent) {
	if !w.selected {
		w.background.FillColor = theme.Colors.ChannelHoverBackground
		w.background.Refresh()
	}
}

// MouseMoved handles mouse movement within the widget.
func (w *ChannelWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (w *ChannelWidget) MouseOut() {
	w.updateBackground()
}

// GetHashtagIcon returns a hashtag (#) icon for channel display.
func GetHashtagIcon() fyne.CanvasObject {
	col := theme.Colors.HashtagIcon
	size := theme.Sizes.HashtagIconSize
	scale := size / 20

	v1 := canvas.NewLine(col)
	v1.Position1 = fyne.NewPos(7*scale, 2*scale)
	v1.Position2 = fyne.NewPos(7*scale, 18*scale)
	v1.StrokeWidth = 2 * scale

	v2 := canvas.NewLine(col)
	v2.Position1 = fyne.NewPos(13*scale, 2*scale)
	v2.Position2 = fyne.NewPos(13*scale, 18*scale)
	v2.StrokeWidth = 2 * scale

	h1 := canvas.NewLine(col)
	h1.Position1 = fyne.NewPos(2*scale, 7*scale)
	h1.Position2 = fyne.NewPos(18*scale, 7*scale)
	h1.StrokeWidth = 2 * scale

	h2 := canvas.NewLine(col)
	h2.Position1 = fyne.NewPos(2*scale, 13*scale)
	h2.Position2 = fyne.NewPos(18*scale, 13*scale)
	h2.StrokeWidth = 2 * scale

	icon := container.NewWithoutLayout(v1, v2, h1, h2)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}
