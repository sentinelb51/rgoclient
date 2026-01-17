package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// Ensure TappableContainer implements necessary interfaces.
var (
	_ fyne.Widget       = (*TappableContainer)(nil)
	_ fyne.Tappable     = (*TappableContainer)(nil)
	_ desktop.Hoverable = (*TappableContainer)(nil)
)

// TappableContainer wraps a container to make it tappable with hover effects.
type TappableContainer struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
	onTap      func()
	hovered    bool
}

// NewTappableContainer creates a new tappable container with the given content and tap handler.
func NewTappableContainer(content fyne.CanvasObject, onTap func()) *TappableContainer {
	tappable := &TappableContainer{
		content:    content,
		background: canvas.NewRectangle(color.Transparent),
		onTap:      onTap,
	}
	tappable.ExtendBaseWidget(tappable)
	return tappable
}

// CreateRenderer returns the renderer for this widget.
func (tappable *TappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(tappable.background, tappable.content))
}

// Tapped handles tap events on the widget.
func (tappable *TappableContainer) Tapped(*fyne.PointEvent) {
	if tappable.onTap != nil {
		tappable.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (tappable *TappableContainer) MouseIn(*desktop.MouseEvent) {
	tappable.hovered = true
	tappable.background.FillColor = color.RGBA{R: 70, G: 70, B: 70, A: 255}
	tappable.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (tappable *TappableContainer) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (tappable *TappableContainer) MouseOut() {
	tappable.hovered = false
	tappable.background.FillColor = color.Transparent
	tappable.background.Refresh()
}

// Ensure XButton implements necessary interfaces.
var (
	_ fyne.Widget       = (*XButton)(nil)
	_ fyne.Tappable     = (*XButton)(nil)
	_ desktop.Hoverable = (*XButton)(nil)
)

// XButton is a simple drawn X button for removing items.
type XButton struct {
	widget.BaseWidget
	onTap   func()
	hovered bool
}

// NewXButton creates a new X button with the given tap handler.
func NewXButton(onTap func()) *XButton {
	button := &XButton{onTap: onTap}
	button.ExtendBaseWidget(button)
	return button
}

// CreateRenderer returns the renderer for this widget.
func (button *XButton) CreateRenderer() fyne.WidgetRenderer {
	return &xButtonRenderer{button: button}
}

// Tapped handles tap events on the widget.
func (button *XButton) Tapped(*fyne.PointEvent) {
	if button.onTap != nil {
		button.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (button *XButton) MouseIn(*desktop.MouseEvent) {
	button.hovered = true
	button.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (button *XButton) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (button *XButton) MouseOut() {
	button.hovered = false
	button.Refresh()
}

// MinSize returns the minimum size for this widget.
func (button *XButton) MinSize() fyne.Size {
	return fyne.NewSize(24, 24)
}

type xButtonRenderer struct {
	button *XButton
}

// Layout positions objects within this renderer.
func (renderer *xButtonRenderer) Layout(size fyne.Size) {}

// MinSize returns the minimum size for this renderer.
func (renderer *xButtonRenderer) MinSize() fyne.Size {
	return renderer.button.MinSize()
}

// Refresh refreshes this renderer.
func (renderer *xButtonRenderer) Refresh() {}

// Objects returns the objects in this renderer.
func (renderer *xButtonRenderer) Objects() []fyne.CanvasObject {
	size := renderer.button.MinSize()
	padding := float32(6)

	// Draw X lines
	lineColor := color.RGBA{R: 150, G: 150, B: 150, A: 255}
	if renderer.button.hovered {
		lineColor = color.RGBA{R: 255, G: 100, B: 100, A: 255}
	}

	line1 := canvas.NewLine(lineColor)
	line1.StrokeWidth = 2
	line1.Position1 = fyne.NewPos(padding, padding)
	line1.Position2 = fyne.NewPos(size.Width-padding, size.Height-padding)

	line2 := canvas.NewLine(lineColor)
	line2.StrokeWidth = 2
	line2.Position1 = fyne.NewPos(size.Width-padding, padding)
	line2.Position2 = fyne.NewPos(padding, size.Height-padding)

	return []fyne.CanvasObject{line1, line2}
}

// Destroy cleans up this renderer.
func (renderer *xButtonRenderer) Destroy() {}

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

	// Layout: [avatar] [username stretches] [x button]
	content := container.NewBorder(nil, nil, card.avatarContainer, removeButton, usernameLabel)
	tappable := NewTappableContainer(content, card.onTap)

	return widget.NewSimpleRenderer(container.NewStack(card.background, container.NewPadded(tappable)))
}
