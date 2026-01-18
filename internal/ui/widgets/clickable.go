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

// Compile-time interface assertions.
var (
	_ fyne.Widget       = (*ClickableImage)(nil)
	_ fyne.Tappable     = (*ClickableImage)(nil)
	_ desktop.Hoverable = (*ClickableImage)(nil)

	_ fyne.Widget       = (*ClickableAvatar)(nil)
	_ fyne.Tappable     = (*ClickableAvatar)(nil)
	_ desktop.Hoverable = (*ClickableAvatar)(nil)
)

// ClickableImage wraps an image container with tap support.
type ClickableImage struct {
	widget.BaseWidget
	content  *fyne.Container
	onTapped func()
	size     fyne.Size
}

// NewClickableImage creates a clickable image widget.
func NewClickableImage(content *fyne.Container, size fyne.Size, onTapped func()) *ClickableImage {
	c := &ClickableImage{
		content:  content,
		onTapped: onTapped,
		size:     size,
	}
	c.ExtendBaseWidget(c)
	return c
}

// CreateRenderer returns the widget renderer.
func (c *ClickableImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

// Tapped handles tap events.
func (c *ClickableImage) Tapped(*fyne.PointEvent) {
	if c.onTapped != nil {
		c.onTapped()
	}
}

// MouseIn handles mouse enter events.
func (c *ClickableImage) MouseIn(*desktop.MouseEvent) {}

// MouseMoved handles mouse move events.
func (c *ClickableImage) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse exit events.
func (c *ClickableImage) MouseOut() {}

// MinSize returns the minimum size.
func (c *ClickableImage) MinSize() fyne.Size {
	return c.size
}

// ClickableAvatar displays a circular avatar with tap support.
type ClickableAvatar struct {
	widget.BaseWidget
	content  *fyne.Container
	onTapped func()
	userID   string
}

// NewClickableAvatar creates a clickable avatar widget.
// Loads avatar asynchronously if avatarID and avatarURL are provided.
func NewClickableAvatar(avatarID, avatarURL, userID string, onTapped func()) *ClickableAvatar {
	size := fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)

	// Circular placeholder
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	content := container.NewGridWrap(size, placeholder)

	// Load avatar asynchronously
	if avatarURL != "" && avatarID != "" {
		cache.GetImageCache().LoadImageToContainer(avatarID, avatarURL, size, content, true, nil)
	}

	a := &ClickableAvatar{
		content:  content,
		onTapped: onTapped,
		userID:   userID,
	}
	a.ExtendBaseWidget(a)
	return a
}

// CreateRenderer returns the widget renderer.
func (a *ClickableAvatar) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	return widget.NewSimpleRenderer(container.NewStack(bg, a.content))
}

// Tapped handles tap events.
func (a *ClickableAvatar) Tapped(*fyne.PointEvent) {
	if a.onTapped != nil {
		a.onTapped()
	}
}

// MouseIn handles mouse enter events.
func (a *ClickableAvatar) MouseIn(*desktop.MouseEvent) {}

// MouseMoved handles mouse move events.
func (a *ClickableAvatar) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse exit events.
func (a *ClickableAvatar) MouseOut() {}

// MinSize returns the minimum size.
func (a *ClickableAvatar) MinSize() fyne.Size {
	return fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
}
