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

// Ensure MessageWidget implements necessary interfaces at compile time.
var (
	_ fyne.Widget       = (*MessageWidget)(nil)
	_ desktop.Hoverable = (*MessageWidget)(nil)
)

// MessageWidget displays a chat message with hover effects.
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
}

// MessageAttachment holds display data for a message attachment.
type MessageAttachment struct {
	ID     string
	URL    string
	Width  int
	Height int
}

// AvatarWidget is a clickable avatar component.
type AvatarWidget struct {
	widget.BaseWidget
	container  *fyne.Container
	background *canvas.Rectangle
	onTapped   func()
	userID     string
}

// Ensure AvatarWidget implements necessary interfaces at compile time.
var (
	_ fyne.Widget       = (*AvatarWidget)(nil)
	_ fyne.Tappable     = (*AvatarWidget)(nil)
	_ desktop.Hoverable = (*AvatarWidget)(nil)
)

// ClickableImage is a clickable image container with hover effects.
type ClickableImage struct {
	widget.BaseWidget
	container  *fyne.Container
	background *canvas.Rectangle
	onTapped   func()
	size       fyne.Size
}

// Ensure ClickableImage implements necessary interfaces at compile time.
var (
	_ fyne.Widget       = (*ClickableImage)(nil)
	_ fyne.Tappable     = (*ClickableImage)(nil)
	_ desktop.Hoverable = (*ClickableImage)(nil)
)

// NewClickableImage creates a new clickable image widget.
func NewClickableImage(imageContainer *fyne.Container, size fyne.Size, onTapped func()) *ClickableImage {
	clickable := &ClickableImage{
		container:  imageContainer,
		background: canvas.NewRectangle(color.Transparent),
		onTapped:   onTapped,
		size:       size,
	}
	clickable.ExtendBaseWidget(clickable)
	return clickable
}

// CreateRenderer returns the renderer for the clickable image.
func (c *ClickableImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(c.background, c.container))
}

// Tapped handles tap/click on the image.
func (c *ClickableImage) Tapped(*fyne.PointEvent) {
	if c.onTapped != nil {
		c.onTapped()
	}
}

// MouseIn handles mouse entering the widget.
func (c *ClickableImage) MouseIn(*desktop.MouseEvent) {
	c.background.FillColor = color.RGBA{R: 255, G: 255, B: 255, A: 20}
	c.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (c *ClickableImage) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (c *ClickableImage) MouseOut() {
	c.background.FillColor = color.Transparent
	c.background.Refresh()
}

// MinSize returns the minimum size of the clickable image.
func (c *ClickableImage) MinSize() fyne.Size {
	return c.size
}

// NewAvatarWidget creates a new clickable avatar widget.
func NewAvatarWidget(avatarID, avatarURL, userID string, onTapped func()) *AvatarWidget {
	avatarSize := fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)

	// Create circular placeholder
	avatarPlaceholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	avatarWrapper := container.NewGridWrap(avatarSize, avatarPlaceholder)

	// Load avatar asynchronously if URL is provided
	if avatarURL != "" && avatarID != "" {
		cache.GetImageCache().LoadImageToContainer(avatarID, avatarURL, avatarSize, avatarWrapper, true, nil)
	}

	avatarWidget := &AvatarWidget{
		container:  avatarWrapper,
		background: canvas.NewRectangle(color.Transparent),
		onTapped:   onTapped,
		userID:     userID,
	}
	avatarWidget.ExtendBaseWidget(avatarWidget)
	return avatarWidget
}

// CreateRenderer returns the renderer for the avatar widget.
func (a *AvatarWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(a.background, a.container))
}

// Tapped handles tap/click on the avatar.
func (a *AvatarWidget) Tapped(*fyne.PointEvent) {
	if a.onTapped != nil {
		a.onTapped()
	}
}

// MouseIn handles mouse entering the widget.
func (a *AvatarWidget) MouseIn(*desktop.MouseEvent) {
	// Could add hover effect here if desired
}

// MouseMoved handles mouse movement within the widget.
func (a *AvatarWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (a *AvatarWidget) MouseOut() {
	// Reset hover effect
}

// MinSize returns the minimum size of the avatar widget.
func (a *AvatarWidget) MinSize() fyne.Size {
	return fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
}

// fixedWidthLayout is a layout that gives a fixed width to its children
type fixedWidthLayout struct {
	width float32
}

func (f *fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for _, obj := range objects {
		if obj.Visible() {
			h := obj.MinSize().Height
			if h > height {
				height = h
			}
		}
	}
	return fyne.NewSize(f.width, height)
}

func (f *fixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objects {
		if obj.Visible() {
			obj.Resize(fyne.NewSize(f.width, size.Height))
			obj.Move(fyne.NewPos(0, 0))
		}
	}
}

// NewMessageWidget creates a new message widget displaying the author and content.
// The layout has a fixed-width left column for avatars, with message content on the right.
// If avatarID and avatarURL are provided, it loads the avatar image asynchronously.
// onAvatarTapped is called when the avatar is clicked.
// onImageTapped is called when an attachment image is clicked, with the attachment info.
func NewMessageWidget(username, message, avatarID, avatarURL string, attachments []MessageAttachment, onAvatarTapped func(), onImageTapped func(attachment MessageAttachment)) *MessageWidget {
	// Create clickable avatar widget
	avatarWidget := NewAvatarWidget(avatarID, avatarURL, "", onAvatarTapped)

	// Avatar column with fixed width - avatar aligned to top
	avatarColumnWidth := theme.Sizes.MessageAvatarColumnWidth
	topPadding := theme.Sizes.MessageAvatarTopPadding

	// Create avatar container with top padding, pinned to top of column
	avatarWithPadding := container.NewVBox(
		newHeightSpacer(topPadding),
		avatarWidget,
	)

	// Use Border layout to pin avatar to top
	avatarColumn := container.NewBorder(
		avatarWithPadding, nil, nil, nil,
		nil,
	)
	// Wrap in a fixed-width container
	avatarColumnSized := container.New(&fixedWidthLayout{width: avatarColumnWidth}, avatarColumn)

	// Message content area
	messageContent := buildMessageContent(username, message, attachments, onImageTapped)

	// Horizontal padding between avatar and content
	contentPadding := theme.Sizes.MessageContentPadding
	paddedContent := container.NewBorder(
		nil, nil,
		newWidthSpacer(contentPadding), nil,
		messageContent,
	)

	// Main layout: [Avatar Column | Content]
	// Avatar column is fixed width on the left, content fills remaining space
	mainLayout := container.NewBorder(
		nil, nil,
		avatarColumnSized, nil,
		paddedContent,
	)

	// Apply vertical and horizontal padding to the entire message
	vPadding := theme.Sizes.MessageVerticalPadding
	hPadding := theme.Sizes.MessageHorizontalPadding
	paddedLayout := container.NewBorder(
		newHeightSpacer(vPadding), newHeightSpacer(vPadding),
		newWidthSpacer(hPadding), newWidthSpacer(hPadding),
		mainLayout,
	)

	messageWidget := &MessageWidget{
		content:    paddedLayout,
		background: canvas.NewRectangle(color.Transparent),
	}
	messageWidget.ExtendBaseWidget(messageWidget)
	return messageWidget
}

// buildMessageContent creates the message content area with username, text, and attachments.
func buildMessageContent(username, message string, attachments []MessageAttachment, onImageTapped func(attachment MessageAttachment)) fyne.CanvasObject {
	// Username label with customizable font
	usernameStyle := fyne.TextStyle{Bold: theme.Fonts.MessageUsernameBold}
	usernameLabel := widget.NewLabelWithStyle(username, fyne.TextAlignLeading, usernameStyle)

	// Message text with word wrapping
	messageLabel := widget.NewLabel(message)
	messageLabel.Wrapping = fyne.TextWrapWord

	// Combine username and message with no extra spacing
	// Use Border layout to stack them tightly
	textContent := container.NewBorder(usernameLabel, nil, nil, nil, messageLabel)

	// If no attachments, return just the text content
	if len(attachments) == 0 {
		return textContent
	}

	// Create attachments container
	attachmentSpacing := theme.Sizes.MessageAttachmentSpacing

	imagesContainer := container.NewVBox()
	for i, attachment := range attachments {
		imageSize := calculateImageSize(attachment.Width, attachment.Height)
		placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		placeholder.SetMinSize(imageSize)
		imageContainer := container.NewGridWrap(imageSize, placeholder)

		// Load image asynchronously
		if attachment.URL != "" && attachment.ID != "" {
			cache.GetImageCache().LoadImageToContainer(attachment.ID, attachment.URL, imageSize, imageContainer, false, nil)
		}

		// Wrap in clickable widget for image viewing
		att := attachment // capture for closure
		clickableImage := NewClickableImage(imageContainer, imageSize, func() {
			if onImageTapped != nil {
				onImageTapped(att)
			}
		})

		// Add spacing between attachments (but not before the first one)
		if i > 0 {
			imagesContainer.Add(newHeightSpacer(attachmentSpacing))
		}

		// Apply left padding to align attachments with text content
		paddedImage := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageTextLeftPadding), nil, clickableImage)
		imagesContainer.Add(paddedImage)
	}

	// Combine text and images vertically with tight spacing
	return container.NewBorder(textContent, nil, nil, nil, imagesContainer)
}

// newWidthSpacer creates a transparent rectangle with the given width.
func newWidthSpacer(width float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 0))
	return spacer
}

// newHeightSpacer creates a transparent rectangle with the given height.
func newHeightSpacer(height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, height))
	return spacer
}

// calculateImageSize calculates the display size for an image, respecting max dimensions.
func calculateImageSize(width, height int) fyne.Size {
	maxWidth := theme.Sizes.MessageImageMaxWidth
	maxHeight := theme.Sizes.MessageImageMaxHeight

	if width == 0 || height == 0 {
		return fyne.NewSize(maxWidth, maxHeight/2)
	}

	scaledWidth := float32(width)
	scaledHeight := float32(height)

	// Scale down if exceeds max dimensions while preserving aspect ratio
	if scaledWidth > maxWidth {
		ratio := maxWidth / scaledWidth
		scaledWidth = maxWidth
		scaledHeight = scaledHeight * ratio
	}
	if scaledHeight > maxHeight {
		ratio := maxHeight / scaledHeight
		scaledHeight = maxHeight
		scaledWidth = scaledWidth * ratio
	}

	return fyne.NewSize(scaledWidth, scaledHeight)
}

// CreateRenderer returns the renderer for this widget.
func (messageWidget *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(messageWidget.background, messageWidget.content))
}

// MouseIn handles mouse entering the widget.
func (messageWidget *MessageWidget) MouseIn(*desktop.MouseEvent) {
	messageWidget.background.FillColor = theme.Colors.MessageHoverBackground
	messageWidget.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (messageWidget *MessageWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (messageWidget *MessageWidget) MouseOut() {
	messageWidget.background.FillColor = color.Transparent
	messageWidget.background.Refresh()
}
