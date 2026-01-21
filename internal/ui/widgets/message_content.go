package widgets

import (
	"fmt"
	"image/color"
	"io"
	"net/http"
	"strings"
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

// buildMessageContent creates the message content with username, text, and attachments.
func buildMessageContent(
	message *revoltgo.Message,
	username, timestamp, messageText string,
	actions MessageActions,
) fyne.CanvasObject {
	header := buildMessageHeader(username, messageText, timestamp)

	if message.Attachments == nil || len(message.Attachments) == 0 {
		return header
	}

	attachmentsContainer := buildAttachmentsContainer(message.Attachments, actions)
	return container.NewVBox(header, attachmentsContainer)
}

func buildMessageHeader(username, messageText, timestamp string) fyne.CanvasObject {
	text := createFormattedMessage(username, messageText)

	tsText := canvas.NewText(timestamp, theme.Colors.TimestampText)
	tsText.TextSize = theme.Sizes.MessageTimestampSize

	// Overlay timestamp in top-right
	timestampOverlay := container.NewVBox(
		newHeightSpacer(theme.Sizes.MessageTimestampTopOffset),
		container.NewHBox(layout.NewSpacer(), tsText),
	)

	return container.NewStack(text, timestampOverlay)
}

func buildAttachmentsContainer(attachments []*revoltgo.Attachment, actions MessageActions) *fyne.Container {
	containerBox := container.NewVBox()
	first := true

	for _, attachment := range attachments {
		if !first {
			containerBox.Add(newHeightSpacer(theme.Sizes.MessageAttachmentSpacing))
		}

		attachmentWidget := buildSingleAttachment(attachment, actions)
		padded := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageTextLeftPadding), nil, container.NewHBox(attachmentWidget))
		containerBox.Add(padded)
		first = false
	}
	return containerBox
}

func buildSingleAttachment(attachment *revoltgo.Attachment, actions MessageActions) fyne.CanvasObject {
	isImage := attachment.Metadata.Type == revoltgo.AttachmentMetadataTypeImage
	isText := isTextAttachment(attachment)

	barStack := createAttachmentBar(attachment)
	var contentStack *fyne.Container

	if isImage {
		contentStack = buildImageAttachment(attachment, barStack)
	} else if isText {
		contentStack = buildTextAttachment(attachment, barStack)
	} else {
		contentStack = buildGenericAttachment(attachment, barStack)
	}

	return NewHoverableStack(contentStack, func() {
		if isImage && actions != nil {
			actions.OnImageTapped(attachment)
		} else if !isImage {
			fmt.Println("File tapped:", attachment.Filename)
		}
	}, nil)
}

func isTextAttachment(attachment *revoltgo.Attachment) bool {
	return strings.Contains(attachment.ContentType, "text/") ||
		strings.HasSuffix(attachment.Filename, ".txt") ||
		strings.HasSuffix(attachment.Filename, ".md") ||
		strings.HasSuffix(attachment.Filename, ".go") ||
		strings.HasSuffix(attachment.Filename, ".json") ||
		strings.HasSuffix(attachment.Filename, ".xml") ||
		strings.HasSuffix(attachment.Filename, ".yml")
}

func createAttachmentBar(attachment *revoltgo.Attachment) fyne.CanvasObject {
	barBg := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	barHeight := float32(28)
	barBg.SetMinSize(fyne.NewSize(0, barHeight))

	nameLabel := canvas.NewText(attachment.Filename, theme.Colors.TextPrimary)
	nameLabel.TextSize = 12
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	nameLabel.Alignment = fyne.TextAlignLeading

	sizeLabel := canvas.NewText(formatFileSize(attachment.Size), theme.Colors.TimestampText)
	sizeLabel.TextSize = 12
	sizeLabel.Alignment = fyne.TextAlignTrailing

	barContent := container.NewBorder(nil, nil,
		container.NewHBox(newWidthSpacer(8), nameLabel),
		container.NewHBox(sizeLabel, newWidthSpacer(8)),
	)

	return container.NewStack(barBg, barContent)
}

// todo: if we're calling this, attachment probably has URL and is not nil?
func buildImageAttachment(attachment *revoltgo.Attachment, barStack fyne.CanvasObject) *fyne.Container {
	size := calculateImageSize(attachment.Metadata.Width, attachment.Metadata.Height)
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	imgContainer := container.NewStack(placeholder)

	attachmentURL := attachment.URL("")
	if attachmentURL != "" && attachment.ID != "" {
		cache.GetImageCache().LoadImageToContainer(attachment.ID, attachmentURL, size, imgContainer, false, nil)
	}

	return container.NewBorder(nil, barStack, nil, nil, imgContainer)
}

func buildTextAttachment(attachment *revoltgo.Attachment, barStack fyne.CanvasObject) *fyne.Container {
	width := float32(300)
	if theme.Sizes.MessageImageMaxWidth < width {
		width = theme.Sizes.MessageImageMaxWidth
	}
	height := float32(150)

	rt := widget.NewRichTextFromMarkdown("Loading preview...")
	rt.Wrapping = fyne.TextWrapWord

	bg := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	bg.SetMinSize(fyne.NewSize(width, height))

	contentStack := container.NewStack(bg, container.NewPadded(rt))

	go fetchTextConfig(attachment.URL(""), rt)

	return container.NewBorder(nil, barStack, nil, nil, contentStack)
}

func fetchTextConfig(url string, target *widget.RichText) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 512)
	n, _ := io.ReadFull(resp.Body, buf)
	if n > 0 {
		content := string(buf[:n])
		runes := []rune(content)
		if len(runes) > 256 {
			content = string(runes[:256]) + "..."
		}

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			target.ParseMarkdown("```\n" + content + "\n```")
			target.Refresh()
		}, false)
	}
}

func buildGenericAttachment(_ *revoltgo.Attachment, barStack fyne.CanvasObject) *fyne.Container {
	width := theme.Sizes.MessageImageMaxWidth
	if width > 300 {
		width = 300
	}
	height := float32(64)

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(width, height))

	icon := canvas.NewImageFromFile("assets/file.svg")
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(32, 32))

	return container.NewBorder(nil, barStack, nil, nil, container.NewStack(placeholder, container.NewCenter(icon)))
}

// createFormattedMessage creates a RichText widget with bold username and formatted content.
func createFormattedMessage(username, message string) *widget.RichText {
	content := fmt.Sprintf("**%s**\n\n%s", username, message)
	rt := widget.NewRichTextFromMarkdown(content)
	rt.Wrapping = fyne.TextWrapWord
	return rt
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
func formatSystemMessage(session *revoltgo.Session, message *revoltgo.MessageSystem) string {
	switch message.Type {
	case revoltgo.MessageSystemUserAdded:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s added to group", user.Username)
	case revoltgo.MessageSystemUserRemove:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s removed from group", user.Username)
	case revoltgo.MessageSystemUserJoined:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s joined", user.Username)
	case revoltgo.MessageSystemUserLeft:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s left", user.Username)
	case revoltgo.MessageSystemUserKicked:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s was kicked", user.Username)
	case revoltgo.MessageSystemUserBanned:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s banned", user.Username)
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

// HoverableStack is a minimal custom widget to handle hover events for attachments.
type HoverableStack struct {
	widget.BaseWidget
	content *fyne.Container
	bg      *canvas.Rectangle
	onHover func(bool)
	onTap   func()
}

var _ fyne.Widget = (*HoverableStack)(nil)
var _ desktop.Hoverable = (*HoverableStack)(nil)
var _ fyne.Tappable = (*HoverableStack)(nil)

func NewHoverableStack(content *fyne.Container, onTap func(), onHover func(bool)) *HoverableStack {
	bg := canvas.NewRectangle(color.Transparent)
	bg.StrokeColor = theme.Colors.ServerListBackground // Default border color (subtle or transparent)
	bg.StrokeWidth = 0

	h := &HoverableStack{
		content: content,
		bg:      bg,
		onHover: onHover,
		onTap:   onTap,
	}
	h.ExtendBaseWidget(h)
	return h
}

func (h *HoverableStack) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(h.content, h.bg))
}

func (h *HoverableStack) MouseIn(*desktop.MouseEvent) {
	if h.onHover != nil {
		h.onHover(true)
	}
	h.bg.StrokeColor = color.Black
	h.bg.StrokeWidth = 1
	h.bg.Refresh()
}

func (h *HoverableStack) MouseOut() {
	if h.onHover != nil {
		h.onHover(false)
	}
	h.bg.StrokeColor = color.Transparent
	h.bg.StrokeWidth = 0
	h.bg.Refresh()
}

func (h *HoverableStack) MouseMoved(*desktop.MouseEvent) {}

func (h *HoverableStack) Tapped(*fyne.PointEvent) {
	if h.onTap != nil {
		h.onTap()
	}
}

// formatFileSize formats bytes to human readable string.
func formatFileSize(size int) string {
	const unit = 1000
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "kMGTPE"[exp])
}
