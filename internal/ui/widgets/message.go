package widgets

import (
	"RGOClient/internal/api"
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

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

// NewMessageWidget creates a message widget with author, content, and optional attachments.
// onAvatarTapped is called when the avatar is clicked.
// onImageTapped is called when an attachment image is clicked (receives *revoltgo.Attachment).
func NewMessageWidget(
	message *revoltgo.Message,
	session *api.Session,
	onAvatarTapped func(),
	onImageTapped func(attachment *revoltgo.Attachment),
) *MessageWidget {

	if session == nil {
		return nil
	}

	// Determine username and avatar
	var username, avatarID, avatarURL string
	if message.Webhook != nil {
		username = message.Webhook.Name
		if message.Webhook.Avatar != nil {
			avatarURL = *message.Webhook.Avatar
		}
	} else if message.System != nil {
		username = "System"
	} else {
		username = session.User(message.Author).Username
		if author := session.User(message.Author); author != nil {
			username = author.Username
			avatarID, avatarURL = GetAvatarInfo(author)
		}
	}

	// Determine content text
	content := message.Content
	if message.System != nil {
		content = formatSystemMessage(session, message.System)
	}

	// Build timestamp
	var timestamp string
	if t, err := session.Timestamp(message.ID); err == nil {
		timestamp = formatMessageTimestamp(t)
	}

	// Build avatar column
	avatar := NewClickableAvatar(avatarID, avatarURL, "", onAvatarTapped)
	avatarColumn := container.New(&centeredAvatarLayout{width: theme.Sizes.MessageAvatarColumnWidth}, avatar)

	// Build content widget
	contentWidget := buildMessageContent(message, username, timestamp, content, onImageTapped)
	paddedContent := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageContentPadding), nil, contentWidget)

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
	message *revoltgo.Message,
	username, timestamp, messageText string,
	onImageTapped func(attachment *revoltgo.Attachment),
) fyne.CanvasObject {
	text := createFormattedMessage(username, messageText)

	tsText := canvas.NewText(timestamp, theme.Colors.TimestampText)
	tsText.TextSize = theme.Sizes.MessageTimestampSize

	// Overlay timestamp in top-right
	timestampOverlay := container.NewVBox(
		container.NewHBox(layout.NewSpacer(), tsText),
	)

	textWithTimestamp := container.NewStack(text, timestampOverlay)

	// Check if message has image attachments
	if message.Attachments == nil || len(message.Attachments) == 0 {
		return textWithTimestamp
	}

	images := container.NewVBox()
	firstImage := true
	for _, att := range message.Attachments {
		if att == nil || att.Metadata == nil || att.Metadata.Type != revoltgo.AttachmentMetadataTypeImage {
			continue
		}

		size := calculateImageSize(att.Metadata.Width, att.Metadata.Height)
		placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		placeholder.SetMinSize(size)
		imgContainer := container.NewGridWrap(size, placeholder)

		url := att.URL("")
		if url != "" && att.ID != "" {
			cache.GetImageCache().LoadImageToContainer(att.ID, url, size, imgContainer, false, nil)
		}

		captured := att
		clickable := NewClickableImage(imgContainer, size, func() {
			if onImageTapped != nil {
				onImageTapped(captured)
			}
		})

		if !firstImage {
			images.Add(newHeightSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		firstImage = false

		paddedImg := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageTextLeftPadding), nil, clickable)
		images.Add(paddedImg)
	}

	// Only add images container if we actually added images
	if firstImage {
		return textWithTimestamp
	}

	return container.NewVBox(textWithTimestamp, images)
}

// createFormattedMessage creates a RichText widget with bold username and formatted content.
func createFormattedMessage(username, message string) *widget.RichText {
	content := fmt.Sprintf("**%s**\n\n%s", username, message)
	rt := widget.NewRichTextFromMarkdown(content)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// formatMessageTimestamp formats time relative to now.
func formatMessageTimestamp(t time.Time) string {
	t = t.Local()
	now := time.Now()

	// Normalize to start of day for accurate day calculation
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	days := int(nowDate.Sub(tDate).Hours() / 24)

	// Same day (Today)
	if days == 0 {
		return fmt.Sprintf("Today, %s", t.Format("3:04 PM"))
	}

	// Previous day (Yesterday)
	if days == 1 {
		return fmt.Sprintf("Yesterday, %s", t.Format("3:04 PM"))
	}

	if days < 30 {
		return fmt.Sprintf("%d days ago, %s", days, t.Format("3:04 PM"))
	}

	if days < 365 {
		months := days / 30
		return fmt.Sprintf("%d month(s) ago", months)
	}

	years := days / 365
	return fmt.Sprintf("%d year(s) ago", years)
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

// formatSystemMessage converts system message to readable text.
func formatSystemMessage(session *api.Session, message *revoltgo.MessageSystem) string {
	switch message.Type {
	case revoltgo.MessageSystemUserAdded:
		return "User added to group"
	case revoltgo.MessageSystemUserRemove:
		return "User removed from group"
	case revoltgo.MessageSystemUserJoined:
		user := session.User(message.ID)
		return fmt.Sprintf("%s joined server", user.Username)
	case revoltgo.MessageSystemUserLeft:
		return "User left server"
	case revoltgo.MessageSystemUserKicked:
		return "User kicked"
	case revoltgo.MessageSystemUserBanned:
		return "User banned"
	case revoltgo.MessageSystemChannelRenamed:
		return "Channel renamed"
	case revoltgo.MessageSystemChannelDescriptionChanged:
		return "Channel description changed"
	case revoltgo.MessageSystemChannelIconChanged:
		return "Channel icon changed"
	case revoltgo.MessageSystemChannelOwnershipChanged:
		return "Channel ownership changed"
	case revoltgo.MessageSystemMessagePinned:
		return "Message pinned"
	case revoltgo.MessageSystemMessageUnpinned:
		return "Message unpinned"
	case revoltgo.MessageSystemCallStarted:
		return "Call started"
	case revoltgo.MessageSystemText:
		return "System message"
	default:
		return "System event"
	}
}
