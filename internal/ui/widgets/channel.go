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

// Ensure ChannelWidget implements necessary interfaces at compile time.
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
	channelWidget := &ChannelWidget{
		Channel:    channel,
		onTap:      onTap,
		background: canvas.NewRectangle(color.Transparent),
	}
	channelWidget.ExtendBaseWidget(channelWidget)
	return channelWidget
}

// SetSelected updates the selection state and refreshes appearance.
func (channelWidget *ChannelWidget) SetSelected(selected bool) {
	channelWidget.selected = selected
	channelWidget.updateBackground()
}

func (channelWidget *ChannelWidget) updateBackground() {
	if channelWidget.selected {
		channelWidget.background.FillColor = theme.Colors.ChannelSelectedBg
	} else {
		channelWidget.background.FillColor = color.Transparent
	}
	channelWidget.background.Refresh()
}

// CreateRenderer returns the renderer for this widget.
func (channelWidget *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	// Add left spacer to push hashtag icon more to the right (customizable)
	leftSpacer := canvas.NewRectangle(color.Transparent)
	leftSpacer.SetMinSize(fyne.NewSize(theme.Sizes.ChannelLeftPadding, 0))
	icon := GetHashtagIcon()
	label := widget.NewLabel(channelWidget.Channel.Name)
	content := container.NewHBox(leftSpacer, icon, label)
	return widget.NewSimpleRenderer(container.NewStack(channelWidget.background, content))
}

// Tapped handles tap events on the widget.
func (channelWidget *ChannelWidget) Tapped(*fyne.PointEvent) {
	if channelWidget.onTap != nil {
		channelWidget.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (channelWidget *ChannelWidget) MouseIn(*desktop.MouseEvent) {
	if !channelWidget.selected {
		channelWidget.background.FillColor = theme.Colors.ChannelHoverBackground
		channelWidget.background.Refresh()
	}
}

// MouseMoved handles mouse movement within the widget.
func (channelWidget *ChannelWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (channelWidget *ChannelWidget) MouseOut() {
	channelWidget.updateBackground()
}

// GetHashtagIcon returns a new hashtag (#) icon for channel display.
func GetHashtagIcon() fyne.CanvasObject {
	iconColor := theme.Colors.HashtagIcon
	size := theme.Sizes.HashtagIconSize

	// Scale factor based on icon size (designed for size 20)
	scale := size / 20

	// Draw hashtag centered within bounds
	// Vertical lines
	verticalLine1 := canvas.NewLine(iconColor)
	verticalLine1.Position1 = fyne.NewPos(7*scale, 2*scale)
	verticalLine1.Position2 = fyne.NewPos(7*scale, 18*scale)
	verticalLine1.StrokeWidth = 2 * scale

	verticalLine2 := canvas.NewLine(iconColor)
	verticalLine2.Position1 = fyne.NewPos(13*scale, 2*scale)
	verticalLine2.Position2 = fyne.NewPos(13*scale, 18*scale)
	verticalLine2.StrokeWidth = 2 * scale

	// Horizontal lines
	horizontalLine1 := canvas.NewLine(iconColor)
	horizontalLine1.Position1 = fyne.NewPos(2*scale, 7*scale)
	horizontalLine1.Position2 = fyne.NewPos(18*scale, 7*scale)
	horizontalLine1.StrokeWidth = 2 * scale

	horizontalLine2 := canvas.NewLine(iconColor)
	horizontalLine2.Position1 = fyne.NewPos(2*scale, 13*scale)
	horizontalLine2.Position2 = fyne.NewPos(18*scale, 13*scale)
	horizontalLine2.StrokeWidth = 2 * scale

	icon := container.NewWithoutLayout(verticalLine1, verticalLine2, horizontalLine1, horizontalLine2)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}
