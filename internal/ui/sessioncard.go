package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// SessionCard is a clickable card for a saved login, with a remove button.
type SessionCard struct {
	widget.BaseWidget
	background *canvas.Rectangle
	avatar     *fyne.Container
	username   string
	onTap      func()
	onRemove   func()
}

// NewSessionCard creates a saved-session card, loading the avatar if available.
func NewSessionCard(images *cache.ImageCache, username, avatarID string, onTap, onRemove func()) *SessionCard {
	background := canvas.NewRectangle(theme.Colors.SessionCardBg)
	background.CornerRadius = 4

	size := theme.Sizes.SessionCardAvatarSize
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	avatar := container.NewGridWrap(fyne.NewSize(size, size), placeholder)
	if avatarID != "" {
		url := revoltgo.EndpointAutumnFile("avatars", avatarID, "64")
		images.LoadIntoContainer(avatarID, url, fyne.NewSize(size, size), avatar, true, nil)
	}

	c := &SessionCard{
		background: background,
		avatar:     avatar,
		username:   username,
		onTap:      onTap,
		onRemove:   onRemove,
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *SessionCard) CreateRenderer() fyne.WidgetRenderer {
	label := widget.NewLabel(c.username)
	label.TextStyle.Bold = true

	remove := container.NewCenter(NewCloseButton(c.onRemove))
	content := container.NewBorder(nil, nil, c.avatar, remove, label)
	tappable := NewTappableContainer(content, c.onTap)

	return widget.NewSimpleRenderer(container.NewStack(c.background, container.NewPadded(tappable)))
}
