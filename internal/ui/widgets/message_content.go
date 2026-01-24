package widgets

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
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
		NewVSpacer(theme.Sizes.MessageTimestampTopOffset),
		container.NewHBox(layout.NewSpacer(), tsText),
	)

	return container.NewStack(text, timestampOverlay)
}

func buildAttachmentsContainer(attachments []*revoltgo.Attachment, actions MessageActions) *fyne.Container {
	containerBox := container.NewVBox()
	first := true

	for _, attachment := range attachments {
		if !first {
			containerBox.Add(NewVSpacer(theme.Sizes.MessageAttachmentSpacing))
		}

		attachmentWidget := buildSingleAttachment(attachment, actions)
		padded := container.NewBorder(nil, nil, NewHSpacer(theme.Sizes.MessageTextLeftPadding), nil, container.NewHBox(attachmentWidget))
		containerBox.Add(padded)
		first = false
	}
	return containerBox
}

func buildSingleAttachment(attachment *revoltgo.Attachment, actions MessageActions) fyne.CanvasObject {
	isImage := attachment.Metadata.Type == revoltgo.AttachmentMetadataTypeImage
	isText := util.Filetype(attachment.Filename) == util.FileTypeText

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
		container.NewHBox(NewHSpacer(8), nameLabel),
		container.NewHBox(sizeLabel, NewHSpacer(8)),
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
