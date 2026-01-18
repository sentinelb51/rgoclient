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
	// No hover effect
}

// MouseMoved handles mouse movement within the widget.
func (c *ClickableImage) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (c *ClickableImage) MouseOut() {
	// No hover effect
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

// centeredAvatarLayout centers the avatar vertically within available space
type centeredAvatarLayout struct {
	width float32
}

func (c *centeredAvatarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for _, obj := range objects {
		if obj.Visible() {
			h := obj.MinSize().Height
			if h > height {
				height = h
			}
		}
	}
	return fyne.NewSize(c.width, height)
}

func (c *centeredAvatarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objects {
		if obj.Visible() {
			objSize := obj.MinSize()
			// Center vertically
			y := (size.Height - objSize.Height) / 2
			obj.Resize(objSize)
			obj.Move(fyne.NewPos(0, y))
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

	// Avatar column with fixed width - avatar centered vertically
	avatarColumnWidth := theme.Sizes.MessageAvatarColumnWidth

	// Use custom layout to center avatar vertically
	avatarColumnSized := container.New(&centeredAvatarLayout{width: avatarColumnWidth}, avatarWidget)

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

// createFormattedMessage creates a RichText widget with bold username followed by formatted message content.
// Supports markdown-like formatting: **bold**, *italic*, > quote
func createFormattedMessage(username, message string) *widget.RichText {
	var segments []widget.RichTextSegment

	// Add bold username segment
	segments = append(segments, &widget.TextSegment{
		Text: username,
		Style: widget.RichTextStyle{
			Inline:    true,
			TextStyle: fyne.TextStyle{Bold: true},
		},
	})

	// Add newline after username
	segments = append(segments, &widget.TextSegment{
		Text: "\n",
		Style: widget.RichTextStyle{
			Inline: true,
		},
	})

	// Parse message content for formatting
	segments = append(segments, parseMessageFormatting(message)...)

	richText := widget.NewRichText(segments...)
	richText.Wrapping = fyne.TextWrapWord
	return richText
}

// parseMessageFormatting parses a message string and returns RichText segments with formatting applied.
// Supports: **bold**, *italic*, > quote lines
func parseMessageFormatting(message string) []widget.RichTextSegment {
	var segments []widget.RichTextSegment

	i := 0
	iterations := 0
	maxIterations := len(message) * 2 // Safety limit

	for i < len(message) {
		iterations++
		if iterations > maxIterations {
			// Add remaining text and break
			if i < len(message) {
				segments = append(segments, &widget.TextSegment{
					Text:  message[i:],
					Style: widget.RichTextStyle{Inline: true},
				})
			}
			break
		}

		// Check for bold (**text**)
		if i+1 < len(message) && message[i:i+2] == "**" {
			end := findClosing(message, i+2, "**")
			if end != -1 {
				text := message[i+2 : end]
				segments = append(segments, &widget.TextSegment{
					Text: text,
					Style: widget.RichTextStyle{
						Inline:    true,
						TextStyle: fyne.TextStyle{Bold: true},
					},
				})
				i = end + 2
				continue
			}
		}

		// Check for italic (*text*)
		if message[i] == '*' {
			end := findClosing(message, i+1, "*")
			if end != -1 {
				text := message[i+1 : end]
				segments = append(segments, &widget.TextSegment{
					Text: text,
					Style: widget.RichTextStyle{
						Inline:    true,
						TextStyle: fyne.TextStyle{Italic: true},
					},
				})
				i = end + 1
				continue
			}
		}

		// Check for quote (> at start of line)
		if message[i] == '>' && (i == 0 || message[i-1] == '\n') {
			// Find end of line
			end := i + 1
			for end < len(message) && message[end] != '\n' {
				end++
			}
			text := message[i:end]
			segments = append(segments, &widget.TextSegment{
				Text: text,
				Style: widget.RichTextStyle{
					Inline:    true,
					TextStyle: fyne.TextStyle{Italic: true},
				},
			})
			i = end
			if i < len(message) && message[i] == '\n' {
				segments = append(segments, &widget.TextSegment{
					Text:  "\n",
					Style: widget.RichTextStyle{Inline: true},
				})
				i++
			}
			continue
		}

		// Regular text - collect until next formatting marker
		start := i
		for i < len(message) {
			if message[i] == '*' || (message[i] == '>' && (i == 0 || message[i-1] == '\n')) {
				break
			}
			i++
		}

		if i > start {
			text := message[start:i]
			segments = append(segments, &widget.TextSegment{
				Text: text,
				Style: widget.RichTextStyle{
					Inline: true,
				},
			})
		} else if i == start {
			// No progress made - advance to prevent infinite loop
			i++
		}
	}

	return segments
}

// findClosing finds the closing marker in a string, skipping escaped markers
func findClosing(text string, start int, marker string) int {
	for i := start; i < len(text); i++ {
		if i+len(marker) <= len(text) && text[i:i+len(marker)] == marker {
			return i
		}
	}
	return -1
}

// buildMessageContent creates the message content area with username, text, and attachments.
func buildMessageContent(username, message string, attachments []MessageAttachment, onImageTapped func(attachment MessageAttachment)) fyne.CanvasObject {
	// Create single RichText with username (bold) and formatted message content
	textContent := createFormattedMessage(username, message)

	// If no attachments, return text content
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

	// Combine text and images vertically
	result := container.NewVBox(textContent, imagesContainer)
	return result
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
