package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

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
	background := canvas.NewRectangle(color.RGBA{R: 50, G: 50, B: 50, A: 255})
	background.CornerRadius = 4

	avatarSize := theme.Sizes.SessionCardAvatarSize
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	avatarContainer := container.NewGridWrap(fyne.NewSize(avatarSize, avatarSize), placeholder)

	if avatarID != "" {
		avatarURL := "https://autumn.revolt.chat/avatars/" + avatarID + "?max_side=64"
		cache.GetImageCache().LoadImageToContainer(avatarID, avatarURL, fyne.NewSize(avatarSize, avatarSize), avatarContainer, true, nil)
	}

	card := &SessionCard{
		background:      background,
		avatarContainer: avatarContainer,
		username:        username,
		onTap:           onTap,
		onRemove:        onRemove,
	}
	card.ExtendBaseWidget(card)
	return card
}

// CreateRenderer returns the renderer for this widget.
func (card *SessionCard) CreateRenderer() fyne.WidgetRenderer {
	usernameLabel := widget.NewLabel(card.username)
	usernameLabel.TextStyle.Bold = true

	removeButton := NewXButton(card.onRemove)
	centeredRemoveButton := container.NewCenter(removeButton)

	// Layout: [avatar] [username stretches] [x button centered]
	content := container.NewBorder(nil, nil, card.avatarContainer, centeredRemoveButton, usernameLabel)
	tappable := NewTappableContainer(content, card.onTap)

	return widget.NewSimpleRenderer(container.NewStack(card.background, container.NewPadded(tappable)))
}
