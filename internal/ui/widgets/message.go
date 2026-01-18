package widgets

import (
	"fmt"
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
var _ fyne.Widget = (*MessageWidget)(nil)
var _ desktop.Hoverable = (*MessageWidget)(nil)

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

// NewMessageWidget creates a message widget with author, content, and optional attachments.
// onAvatarTapped is called when the avatar is clicked.
// onImageTapped is called when an attachment image is clicked.
// todo: use revoltgo.Message
func NewMessageWidget(
	username, message, avatarID, avatarURL string,
	attachments []MessageAttachment,
	onAvatarTapped func(),
	onImageTapped func(attachment MessageAttachment),
) *MessageWidget {
	avatar := NewClickableAvatar(avatarID, avatarURL, "", onAvatarTapped)
	avatarColumn := container.New(&centeredAvatarLayout{width: theme.Sizes.MessageAvatarColumnWidth}, avatar)

	content := buildMessageContent(username, message, attachments, onImageTapped)
	paddedContent := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageContentPadding), nil, content)

	main := container.NewBorder(nil, nil, avatarColumn, nil, paddedContent)

	vPad := theme.Sizes.MessageVerticalPadding
	hPad := theme.Sizes.MessageHorizontalPadding
	padded := container.NewBorder(
		newHeightSpacer(vPad), newHeightSpacer(vPad),
		newWidthSpacer(hPad), newWidthSpacer(hPad),
		main,
	)

	w := &MessageWidget{
		content:    padded,
		background: canvas.NewRectangle(color.Transparent),
	}
	w.ExtendBaseWidget(w)
	return w
}

// CreateRenderer returns the widget renderer.
func (w *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(w.background, w.content))
}

// MouseIn handles mouse enter.
func (w *MessageWidget) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.MessageHoverBackground
	w.background.Refresh()
}

// MouseMoved handles mouse movement.
func (w *MessageWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse exit.
func (w *MessageWidget) MouseOut() {
	w.background.FillColor = color.Transparent
	w.background.Refresh()
}

// centeredAvatarLayout centers avatar vertically within available space.
type centeredAvatarLayout struct {
	width float32
}

func (l *centeredAvatarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	for _, obj := range objects {
		if obj.Visible() {
			if h := obj.MinSize().Height; h > height {
				height = h
			}
		}
	}
	return fyne.NewSize(l.width, height)
}

func (l *centeredAvatarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objects {
		if obj.Visible() {
			objSize := obj.MinSize()
			y := (size.Height - objSize.Height) / 2
			obj.Resize(objSize)
			obj.Move(fyne.NewPos(0, y))
		}
	}
}

// buildMessageContent creates the message content with username, text, and attachments.
func buildMessageContent(
	username, message string,
	attachments []MessageAttachment,
	onImageTapped func(attachment MessageAttachment),
) fyne.CanvasObject {
	text := createFormattedMessage(username, message)

	if len(attachments) == 0 {
		return text
	}

	images := container.NewVBox()
	for i, att := range attachments {
		size := calculateImageSize(att.Width, att.Height)
		placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		placeholder.SetMinSize(size)
		imgContainer := container.NewGridWrap(size, placeholder)

		if att.URL != "" && att.ID != "" {
			cache.GetImageCache().LoadImageToContainer(att.ID, att.URL, size, imgContainer, false, nil)
		}

		captured := att
		clickable := NewClickableImage(imgContainer, size, func() {
			if onImageTapped != nil {
				onImageTapped(captured)
			}
		})

		if i > 0 {
			images.Add(newHeightSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		paddedImg := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageTextLeftPadding), nil, clickable)
		images.Add(paddedImg)
	}

	return container.NewVBox(text, images)
}

// createFormattedMessage creates a RichText widget with bold username and formatted content.
func createFormattedMessage(username, message string) *widget.RichText {
	content := fmt.Sprintf("**%s**\n\n%s", username, message)
	rt := widget.NewRichTextFromMarkdown(content)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// newWidthSpacer creates a transparent spacer with the given width.
func newWidthSpacer(width float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 0))
	return spacer
}

// newHeightSpacer creates a transparent spacer with the given height.
func newHeightSpacer(height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, height))
	return spacer
}

// calculateImageSize calculates display size respecting max dimensions.
func calculateImageSize(width, height int) fyne.Size {
	maxW := theme.Sizes.MessageImageMaxWidth
	maxH := theme.Sizes.MessageImageMaxHeight

	if width == 0 || height == 0 {
		return fyne.NewSize(maxW, maxH/2)
	}

	w := float32(width)
	h := float32(height)

	if w > maxW {
		h = h * (maxW / w)
		w = maxW
	}
	if h > maxH {
		w = w * (maxH / h)
		h = maxH
	}

	return fyne.NewSize(w, h)
}
