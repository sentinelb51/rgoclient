package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// SessionCard displays a saved session as a clickable card.
type SessionCard struct {
	widget.BaseWidget
	background      *canvas.Rectangle
	avatarContainer *fyne.Container
	username        string
	onTap           func()
	onRemove        func()
}

// NewSessionCard creates a new session card widget.
func NewSessionCard(username, avatarID string, onTap, onRemove func()) *SessionCard {
	bg := canvas.NewRectangle(color.RGBA{R: 50, G: 50, B: 50, A: 255})
	bg.CornerRadius = 4

	size := theme.Sizes.SessionCardAvatarSize
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	avatarContainer := container.NewGridWrap(fyne.NewSize(size, size), placeholder)

	if avatarID != "" {
		url := revoltgo.EndpointAutumnFile("avatars", avatarID, "64")
		cache.GetImageCache().LoadImageToContainer(avatarID, url, fyne.NewSize(size, size), avatarContainer, true, nil)
	}

	card := &SessionCard{
		background:      bg,
		avatarContainer: avatarContainer,
		username:        username,
		onTap:           onTap,
		onRemove:        onRemove,
	}
	card.ExtendBaseWidget(card)
	return card
}

// CreateRenderer returns the renderer for this widget.
func (c *SessionCard) CreateRenderer() fyne.WidgetRenderer {
	label := widget.NewLabel(c.username)
	label.TextStyle.Bold = true

	removeBtn := NewXButton(c.onRemove)
	centeredBtn := container.NewCenter(removeBtn)

	content := container.NewBorder(nil, nil, c.avatarContainer, centeredBtn, label)
	tappable := NewTappableContainer(content, c.onTap)

	return widget.NewSimpleRenderer(container.NewStack(c.background, container.NewPadded(tappable)))
}
