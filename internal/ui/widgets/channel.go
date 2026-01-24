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
	Channel *revoltgo.Channel
	onTap   func()

	// UI components
	background         *canvas.Rectangle
	selectionIndicator *canvas.Rectangle
	unreadIndicator    *canvas.Rectangle
	label              *canvas.Text

	// State
	selected bool
	unread   bool
}

// NewChannelWidget creates a new channel widget.
func NewChannelWidget(channel *revoltgo.Channel, onTap func()) *ChannelWidget {
	w := &ChannelWidget{
		Channel:            channel,
		onTap:              onTap,
		background:         canvas.NewRectangle(color.Transparent),
		selectionIndicator: canvas.NewRectangle(color.Transparent),
		unreadIndicator:    canvas.NewRectangle(color.Transparent),
		label:              canvas.NewText(channel.Name, theme.Colors.CategoryText),
	}
	w.label.TextSize = theme.Sizes.MessageTimestampSize + 2 // Slightly larger than timestamp
	w.ExtendBaseWidget(w)
	return w
}

// SetState updates the selection and unread state together.
func (w *ChannelWidget) SetState(selected, unread bool) {
	w.selected = selected
	w.unread = unread
	w.updateAppearance()
	w.Refresh()
}

func (w *ChannelWidget) updateAppearance() {
	// Background
	if w.selected {
		w.background.FillColor = theme.Colors.ChannelSelectedBg
		w.selectionIndicator.FillColor = theme.Colors.TextPrimary // White selection bar
	} else {
		w.background.FillColor = color.Transparent
		w.selectionIndicator.FillColor = color.Transparent
	}
	w.background.Refresh()
	w.selectionIndicator.Refresh()

	// Unread Indicator
	if w.unread {
		w.unreadIndicator.FillColor = theme.Colors.UnreadIndicator
	} else {
		w.unreadIndicator.FillColor = color.Transparent
	}
	w.unreadIndicator.Refresh()

	// Text Color: White if Selected OR Unread, otherwise Grey
	if w.selected || w.unread {
		w.label.Color = theme.Colors.TextPrimary
	} else {
		w.label.Color = theme.Colors.CategoryText
	}
	w.label.TextStyle.Bold = false
	w.label.Refresh()
}

// CreateRenderer returns the renderer for this widget.
func (w *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	// Left vSpacer provides left padding
	spacerBg := NewHSpacer(theme.Sizes.ChannelLeftPadding)

	// Indicators
	w.selectionIndicator.SetMinSize(fyne.NewSize(3, 0))
	w.unreadIndicator.SetMinSize(fyne.NewSize(theme.Sizes.UnreadIndicatorWidth, 0))

	// Stack indicators to occupy same 3px width space
	// Wrap unreadIndicator in HBox to prevent stretching (keeps it 1px width, left aligned)
	unreadWrapper := container.NewHBox(w.unreadIndicator)
	indicatorStack := container.NewStack(w.selectionIndicator, unreadWrapper)

	icon := GetHashtagIcon()
	w.label.Alignment = fyne.TextAlignLeading

	// Content layout
	content := container.NewHBox(indicatorStack, spacerBg, icon, w.label)

	// Enforce minimum height for spacing
	w.background.SetMinSize(fyne.NewSize(0, theme.Sizes.ChannelItemHeight))

	wrapper := container.NewStack(w.background, content)

	// Apply initial state
	w.updateAppearance()

	return widget.NewSimpleRenderer(wrapper)
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
	w.updateAppearance()
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
