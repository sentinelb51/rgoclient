package ui

import (
	"fmt"
	"image/color"
	"io"
	"net/http"
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
	"RGOClient/internal/util"
)

const (
	maxReplyPreviewLength = 80
	hoverHideDelay        = 50 * time.Millisecond
)

// MessageWidget renders a single chat message with a hover state that reveals
// quick-action buttons.
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
	actions    *fyne.Container

	// hover tracking debounces the transition between the message body and the
	// floating action buttons so the buttons don't flicker.
	overMessage bool
	overActions bool
	hideTimer   *time.Timer
}

var (
	_ fyne.Widget       = (*MessageWidget)(nil)
	_ desktop.Hoverable = (*MessageWidget)(nil)
)

// NewMessageWidget builds a message widget for the given message.
func NewMessageWidget(deps Deps, message *revoltgo.Message) *MessageWidget {
	w := &MessageWidget{background: canvas.NewRectangle(color.Transparent)}

	avatarURL := util.DisplayAvatarURL(deps.Session, message)
	avatarID := util.IDFromAttachmentURL(avatarURL)
	name := util.DisplayName(deps.Session, message)

	text := message.Content
	if message.System != nil {
		text = util.FormatSystemMessage(deps.Session, message.System)
	}

	var timestamp string
	if t, err := util.Timestamp(message.ID); err == nil {
		timestamp = util.NiceTime(t)
	}

	w.actions = w.buildActions(deps, message)

	avatar := NewAvatar(deps.Images, avatarID, avatarURL, func() {
		if deps.Actions != nil {
			deps.Actions.OnAvatarTapped(message.Author)
		}
	})
	avatarColumn := container.New(&FixedWidthColumnLayout{Width: theme.Sizes.MessageAvatarColumnWidth}, avatar)

	body := buildMessageContent(deps, message, name, timestamp, text)
	paddedBody := container.NewBorder(nil, nil, HorizontalSpacer(theme.Sizes.MessageContentPadding), nil, body)
	row := container.NewBorder(nil, nil, avatarColumn, nil, paddedBody)

	hPad := theme.Sizes.MessageHorizontalPadding
	inner := container.NewBorder(nil, nil, HorizontalSpacer(hPad), HorizontalSpacer(hPad), row)

	floatingActions := container.New(&OverlayLayout{YOffset: -16, RightOffset: 6}, w.actions)
	messageRow := container.NewStack(inner, floatingActions)

	w.content = messageRow
	if len(message.Replies) > 0 {
		replies := container.NewVBox()
		for _, replyID := range message.Replies {
			replies.Add(buildReplyPreview(deps, message.Channel, replyID))
			replies.Add(VerticalSpacer(-15))
		}
		w.content = container.NewVBox(replies, messageRow)
	}

	w.ExtendBaseWidget(w)
	return w
}

// buildActions creates the hidden, rounded group of reply/edit/delete buttons.
func (w *MessageWidget) buildActions(deps Deps, message *revoltgo.Message) *fyne.Container {
	onHover := func(hovering bool) {
		w.overActions = hovering
		w.updateHover()
	}

	reply := newIconButton("assets/reply.svg", func() {
		if deps.Actions != nil {
			deps.Actions.OnReply(message)
		}
	}, onHover)
	edit := newIconButton("assets/edit.svg", func() {
		if deps.Actions != nil {
			deps.Actions.OnEdit(message.ID)
		}
	}, onHover)
	del := newIconButton("assets/trash.svg", func() {
		if deps.Actions != nil {
			deps.Actions.OnDelete(message.ID)
		}
	}, onHover)

	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.CornerRadius = 8
	background.StrokeColor = theme.Colors.ServerListBackground
	background.StrokeWidth = 1

	group := container.NewStack(background, HBoxNoSpacing(reply, edit, del))
	group.Hide()
	return group
}

func (w *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(w.background, w.content))
}

// updateHover shows the action buttons while the pointer is over the message or
// the buttons, hiding them after a short grace period otherwise.
func (w *MessageWidget) updateHover() {
	if w.overMessage || w.overActions {
		if w.hideTimer != nil {
			w.hideTimer.Stop()
			w.hideTimer = nil
		}
		w.background.FillColor = theme.Colors.MessageHoverBackground
		w.background.Refresh()
		w.actions.Show()
		return
	}

	if w.hideTimer != nil {
		return
	}
	w.hideTimer = time.AfterFunc(hoverHideDelay, func() {
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if w.overMessage || w.overActions {
				return
			}
			w.background.FillColor = color.Transparent
			w.background.Refresh()
			w.actions.Hide()
			w.hideTimer = nil
		}, false)
	})
}

func (w *MessageWidget) MouseIn(*desktop.MouseEvent) {
	w.overMessage = true
	w.updateHover()
}

func (w *MessageWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *MessageWidget) MouseOut() {
	w.overMessage = false
	w.updateHover()
}

// buildMessageContent assembles the author/text header plus any attachments.
func buildMessageContent(deps Deps, message *revoltgo.Message, name, timestamp, text string) fyne.CanvasObject {
	header := buildMessageHeader(name, text, timestamp)
	if len(message.Attachments) == 0 {
		return header
	}
	return container.NewVBox(header, buildAttachments(deps, message.Attachments))
}

// buildMessageHeader renders the bold author line, message text, and a
// top-right timestamp.
func buildMessageHeader(name, text, timestamp string) fyne.CanvasObject {
	body := widget.NewRichTextFromMarkdown(fmt.Sprintf("**%s**\n\n%s", name, text))
	body.Wrapping = fyne.TextWrapWord

	ts := canvas.NewText(timestamp, theme.Colors.TimestampText)
	ts.TextSize = theme.Sizes.MessageTimestampSize
	tsOverlay := container.NewVBox(
		VerticalSpacer(theme.Sizes.MessageTimestampTopOffset),
		container.NewHBox(layout.NewSpacer(), ts),
	)

	return container.NewStack(body, tsOverlay)
}

// buildAttachments stacks each attachment with a small gap between them.
func buildAttachments(deps Deps, attachments []*revoltgo.Attachment) *fyne.Container {
	box := container.NewVBox()
	for i, attachment := range attachments {
		if i > 0 {
			box.Add(VerticalSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		item := buildAttachment(deps, attachment)
		box.Add(container.NewBorder(nil, nil, HorizontalSpacer(theme.Sizes.MessageTextLeftPadding), nil, container.NewHBox(item)))
	}
	return box
}

// buildAttachment renders one attachment as an image, text preview, or generic
// file card, with a name/size bar beneath it.
func buildAttachment(deps Deps, attachment *revoltgo.Attachment) fyne.CanvasObject {
	isImage := attachment.Metadata.Type == revoltgo.AttachmentMetadataTypeImage
	bar := attachmentBar(attachment.Filename, attachment.Size, nil)

	var content *fyne.Container
	switch {
	case isImage:
		content = buildImageAttachment(deps.Images, attachment, bar)
	case util.Filetype(attachment.Filename) == util.FileTypeText:
		content = buildTextAttachment(attachment, bar)
	default:
		content = buildGenericAttachment(bar)
	}

	return NewHoverableStack(content, func() {
		if isImage && deps.Actions != nil {
			deps.Actions.OnImageTapped(attachment)
		}
	}, nil)
}

func buildImageAttachment(images *cache.ImageCache, attachment *revoltgo.Attachment, bar fyne.CanvasObject) *fyne.Container {
	size := imageDisplaySize(attachment.Metadata.Width, attachment.Metadata.Height)
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	image := container.NewStack(placeholder)

	if url := attachment.URL(""); url != "" && attachment.ID != "" {
		images.LoadIntoContainer(attachment.ID, url, size, image, false, nil)
	}
	return container.NewBorder(nil, bar, nil, nil, image)
}

func buildTextAttachment(attachment *revoltgo.Attachment, bar fyne.CanvasObject) *fyne.Container {
	width := min(float32(300), theme.Sizes.MessageImageMaxWidth)

	preview := widget.NewRichTextFromMarkdown("Loading preview...")
	preview.Wrapping = fyne.TextWrapWord

	background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	background.SetMinSize(fyne.NewSize(width, 150))

	content := container.NewStack(background, container.NewPadded(preview))
	go fetchTextPreview(attachment.URL(""), preview)
	return container.NewBorder(nil, bar, nil, nil, content)
}

func buildGenericAttachment(bar fyne.CanvasObject) *fyne.Container {
	width := min(float32(300), theme.Sizes.MessageImageMaxWidth)

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(width, 64))

	icon := canvas.NewImageFromFile("assets/file.svg")
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(32, 32))

	return container.NewBorder(nil, bar, nil, nil, container.NewStack(placeholder, container.NewCenter(icon)))
}

// fetchTextPreview loads the first few hundred characters of a text attachment
// into preview, formatted as a code block.
func fetchTextPreview(url string, preview *widget.RichText) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	buf := make([]byte, 512)
	n, _ := io.ReadFull(resp.Body, buf)
	if n == 0 {
		return
	}

	runes := []rune(string(buf[:n]))
	if len(runes) > 256 {
		runes = append(runes[:256], []rune("...")...)
	}

	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		preview.ParseMarkdown("```\n" + string(runes) + "\n```")
		preview.Refresh()
	}, false)
}

// attachmentBar renders a name/size strip. When onRemove is non-nil it also
// shows a close button (used by the message composer).
func attachmentBar(name string, size int, onRemove func()) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.SetMinSize(fyne.NewSize(0, 28))

	nameLabel := canvas.NewText(name, theme.Colors.TextPrimary)
	nameLabel.TextSize = 12
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}

	sizeLabel := canvas.NewText(util.FormatFileSize(size), theme.Colors.TimestampText)
	sizeLabel.TextSize = 12
	sizeLabel.Alignment = fyne.TextAlignTrailing

	left := container.NewHBox(HorizontalSpacer(8), nameLabel)
	right := container.NewHBox(sizeLabel, HorizontalSpacer(8))
	if onRemove != nil {
		right = container.NewHBox(sizeLabel, container.NewPadded(NewCloseButton(onRemove)), HorizontalSpacer(8))
	}

	return container.NewStack(background, container.NewBorder(nil, nil, left, right))
}

// imageDisplaySize scales an image to fit the configured maximum dimensions
// while preserving aspect ratio.
func imageDisplaySize(width, height int) fyne.Size {
	maxW := theme.Sizes.MessageImageMaxWidth
	maxH := theme.Sizes.MessageImageMaxHeight
	if width == 0 || height == 0 {
		return fyne.NewSize(maxW, maxH/2)
	}

	w, h := float32(width), float32(height)
	if w > maxW {
		h *= maxW / w
		w = maxW
	}
	if h > maxH {
		w *= maxH / h
		h = maxH
	}
	return fyne.NewSize(w, h)
}

// buildReplyPreview renders the small quoted line shown above a message that
// replies to another.
func buildReplyPreview(deps Deps, channelID, messageID string) fyne.CanvasObject {
	author, content, avatarURL := resolveReply(deps, channelID, messageID, maxReplyPreviewLength)

	avatar := circularAvatar(deps.Images, avatarURL, fyne.NewSize(16, 16))

	authorLabel := canvas.NewText(author, theme.Colors.TextPrimary)
	authorLabel.TextStyle.Bold = true
	authorLabel.TextSize = 12

	contentLabel := canvas.NewText(content, theme.Colors.TimestampText)
	contentLabel.TextSize = 12

	row := HBoxNoSpacing(
		container.NewCenter(avatar),
		HorizontalSpacer(8),
		container.NewCenter(authorLabel),
		HorizontalSpacer(5),
		container.NewCenter(contentLabel),
	)
	padded := container.NewBorder(VerticalSpacer(3), VerticalSpacer(3), HorizontalSpacer(3), HorizontalSpacer(3), row)

	// TODO: navigate to the referenced message on tap.
	tappable := NewTappableContainer(padded, func() {})
	return container.NewHBox(HorizontalSpacer(40), tappable)
}

// resolveReply looks up a referenced message and returns its author, truncated
// content, and avatar URL. Missing references yield a placeholder.
func resolveReply(deps Deps, channelID, messageID string, maxLen int) (author, content, avatarURL string) {
	if deps.Actions == nil {
		return "", "Unknown message reference", ""
	}
	msg := deps.Actions.ResolveMessage(channelID, messageID)
	if msg == nil {
		return "", "Unknown message reference", ""
	}

	author = util.DisplayName(deps.Session, msg)
	avatarURL = util.DisplayAvatarURL(deps.Session, msg)
	content = msg.Content
	if len(content) > maxLen {
		content = content[:maxLen-3] + "..."
	}
	return author, content, avatarURL
}

// circularAvatar builds a circular avatar of the given size, loading the image
// from avatarURL when present.
func circularAvatar(images *cache.ImageCache, avatarURL string, size fyne.Size) *fyne.Container {
	placeholder := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	avatar := container.NewGridWrap(size, placeholder)

	if avatarURL != "" {
		id := util.IDFromAttachmentURL(avatarURL)
		if id == "" {
			id = avatarURL
		}
		images.LoadIntoContainer(id, avatarURL, size, avatar, true, nil)
	}
	return avatar
}
