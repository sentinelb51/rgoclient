package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"
	"golang.design/x/clipboard"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	maxInputLines    = 8
	maxReplies       = 5
	replyPreviewLen  = 60
	attachPreviewW   = 200
	attachPreviewImg = 150
	attachPreviewGen = 64
)

var _ desktop.Keyable = (*MessageInput)(nil)

// Attachment is a local file queued to be sent with the next message.
type Attachment struct {
	Path string
	Name string
}

// Reply is a pending reply to an existing message.
type Reply struct {
	ID        string
	ChannelID string
	Mention   bool
}

// MessageInput is a multi-line text entry that grows with its content, supports
// shift-enter for newlines, and manages pending attachments and replies.
type MessageInput struct {
	widget.Entry
	OnSubmit func(string)

	deps         Deps
	shiftPressed bool

	Attachments         []Attachment
	AttachmentContainer *fyne.Container
	Replies             []Reply
	ReplyContainer      *fyne.Container
}

// NewMessageInput creates a message input wired to the given dependencies.
func NewMessageInput(deps Deps) *MessageInput {
	m := &MessageInput{
		deps:                deps,
		AttachmentContainer: container.NewHBox(),
		ReplyContainer:      container.NewVBox(),
	}
	m.ExtendBaseWidget(m)
	m.MultiLine = true
	m.Wrapping = fyne.TextWrapWord
	return m
}

// MinSize grows the entry up to maxInputLines as the user types.
func (m *MessageInput) MinSize() fyne.Size {
	size := m.Entry.MinSize()
	lines := min(max(strings.Count(m.Text, "\n")+1, 1), maxInputLines)
	lineHeight := fyne.MeasureText("M", fynetheme.TextSize(), fyne.TextStyle{}).Height
	size.Height = lineHeight*float32(lines) + fynetheme.InnerPadding()*2
	return size
}

func (m *MessageInput) FocusLost() {
	m.shiftPressed = false
	m.Entry.FocusLost()
}

func (m *MessageInput) KeyDown(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		m.shiftPressed = true
	}
}

func (m *MessageInput) KeyUp(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		m.shiftPressed = false
	}
}

// TypedKey sends the message on Enter, inserts a newline on Shift+Enter, and
// otherwise defers to the embedded entry (refreshing so MinSize recomputes).
func (m *MessageInput) TypedKey(key *fyne.KeyEvent) {
	switch {
	case key.Name == fyne.KeyBackspace || key.Name == fyne.KeyDelete:
		m.Entry.TypedKey(key)
		m.Refresh()
	case key.Name != fyne.KeyReturn && key.Name != fyne.KeyEnter:
		m.Entry.TypedKey(key)
	case m.shiftPressed:
		m.TypedRune('\n')
		m.Refresh()
	default:
		if m.OnSubmit != nil {
			m.OnSubmit(m.Text)
		}
		m.Refresh()
	}
}

func (m *MessageInput) TypedRune(r rune) {
	m.Entry.TypedRune(r)
	m.Refresh()
}

// TypedShortcut intercepts paste to support pasting an image or a file path as
// an attachment, falling back to the default behaviour.
func (m *MessageInput) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutPaste); ok && m.pasteAsAttachment() {
		m.Refresh()
		return
	}
	m.Entry.TypedShortcut(s)
	m.Refresh()
}

// pasteAsAttachment attaches an image or file path from the clipboard, returning
// whether it consumed the paste.
func (m *MessageInput) pasteAsAttachment() bool {
	if clipboard.Init() == nil {
		if img := clipboard.Read(clipboard.FmtImage); len(img) > 0 {
			path := filepath.Join(os.TempDir(), fmt.Sprintf("%d.png", time.Now().UnixNano()))
			if os.WriteFile(path, img, 0o644) == nil {
				m.AddAttachment(path)
				return true
			}
		}
	}

	content := fyne.CurrentApp().Clipboard().Content()
	if content != "" {
		if _, err := os.Stat(content); err == nil {
			m.AddAttachment(content)
			return true
		}
	}
	return false
}

// RegisterDropHandler attaches files dropped onto the window.
func (m *MessageInput) RegisterDropHandler(window fyne.Window) {
	window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		for _, u := range uris {
			if u.Scheme() == "file" {
				m.AddAttachment(u.Path())
			}
		}
	})
}

// AddAttachment queues a file and rebuilds the attachment previews.
func (m *MessageInput) AddAttachment(path string) {
	m.Attachments = append(m.Attachments, Attachment{Path: path, Name: filepath.Base(path)})
	m.rebuildAttachments()
}

// RemoveAttachment removes a queued file by path.
func (m *MessageInput) RemoveAttachment(path string) {
	for i, a := range m.Attachments {
		if a.Path == path {
			m.Attachments = append(m.Attachments[:i], m.Attachments[i+1:]...)
			m.rebuildAttachments()
			return
		}
	}
}

// ClearAttachments removes all queued files.
func (m *MessageInput) ClearAttachments() {
	m.Attachments = nil
	m.AttachmentContainer.Objects = nil
	m.AttachmentContainer.Refresh()
}

func (m *MessageInput) rebuildAttachments() {
	m.AttachmentContainer.Objects = nil
	for _, attachment := range m.Attachments {
		var size int
		if info, err := os.Stat(attachment.Path); err == nil {
			size = int(info.Size())
		}

		path := attachment.Path
		bar := attachmentBar(attachment.Name, size, func() { m.RemoveAttachment(path) })
		card := container.NewBorder(nil, bar, nil, nil, m.attachmentPreview(path))

		background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		background.CornerRadius = 8
		m.AttachmentContainer.Add(container.NewPadded(container.NewStack(background, container.NewPadded(card))))
	}
	m.AttachmentContainer.Refresh()
	m.Refresh()
}

func (m *MessageInput) attachmentPreview(path string) fyne.CanvasObject {
	if util.Filetype(path) == util.FileTypeImage {
		img := canvas.NewImageFromFile(path)
		img.FillMode = canvas.ImageFillContain
		img.ScaleMode = canvas.ImageScaleFastest
		img.SetMinSize(fyne.NewSize(attachPreviewW, attachPreviewImg))
		return img
	}

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(attachPreviewW, attachPreviewGen))
	return placeholder
}

// AddReply adds a reply target, ignoring duplicates and respecting maxReplies.
func (m *MessageInput) AddReply(message *revoltgo.Message) {
	if len(m.Replies) >= maxReplies {
		return
	}
	for _, r := range m.Replies {
		if r.ID == message.ID {
			return
		}
	}
	m.Replies = append(m.Replies, Reply{ID: message.ID, ChannelID: message.Channel})
	m.rebuildReplies()
}

// RemoveReply removes a reply target by message ID.
func (m *MessageInput) RemoveReply(messageID string) {
	for i, r := range m.Replies {
		if r.ID == messageID {
			m.Replies = append(m.Replies[:i], m.Replies[i+1:]...)
			m.rebuildReplies()
			return
		}
	}
}

// ClearReplies removes all reply targets.
func (m *MessageInput) ClearReplies() {
	m.Replies = nil
	m.rebuildReplies()
}

func (m *MessageInput) rebuildReplies() {
	m.ReplyContainer.Objects = nil
	for i := range m.Replies {
		m.ReplyContainer.Add(m.buildReplyCard(&m.Replies[i]))
	}
	m.ReplyContainer.Refresh()
	m.Refresh()
}

// buildReplyCard renders a composer chip for one pending reply, with a mention
// toggle and a remove button.
func (m *MessageInput) buildReplyCard(reply *Reply) fyne.CanvasObject {
	author, content, avatarURL := resolveReply(m.deps, reply.ChannelID, reply.ID, replyPreviewLen)
	if author == "" {
		author = "Unknown"
	}

	avatar := container.NewCenter(circularAvatar(m.deps.Images, avatarURL, fyne.NewSize(22, 22)))

	authorLabel := canvas.NewText(author, theme.Colors.TextPrimary)
	authorLabel.TextSize = 14
	authorLabel.TextStyle = fyne.TextStyle{Bold: true}

	contentLabel := canvas.NewText(content, theme.Colors.TimestampText)
	contentLabel.TextSize = 14

	left := HBoxNoSpacing(
		HorizontalSpacer(12),
		avatar,
		HorizontalSpacer(4),
		HBoxNoSpacing(authorLabel, HorizontalSpacer(10), contentLabel),
	)

	var mention *mentionToggle
	mention = newMentionToggle(reply.Mention, func() {
		reply.Mention = !reply.Mention
		mention.SetActive(reply.Mention)
		m.ReplyContainer.Refresh()
	})
	right := container.NewHBox(mention, NewCloseButton(func() { m.RemoveReply(reply.ID) }))

	row := container.NewBorder(nil, nil, left, right)
	padded := container.NewBorder(VerticalSpacer(2), VerticalSpacer(2), HorizontalSpacer(4), HorizontalSpacer(4), row)

	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.CornerRadius = 8
	return container.NewStack(background, padded)
}

// mentionToggle is the "@" button on a reply chip. Its rendered highlight is
// active XOR hovered, so hovering previews the opposite state.
type mentionToggle struct {
	tapBase
	active  bool
	hovered bool
	text    *canvas.Text
	content *fyne.Container
}

var (
	_ fyne.Tappable     = (*mentionToggle)(nil)
	_ desktop.Hoverable = (*mentionToggle)(nil)
)

func newMentionToggle(active bool, onTap func()) *mentionToggle {
	size := fyne.NewSize(20, 20)

	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.SetMinSize(size)

	text := canvas.NewText("@", theme.Colors.TimestampText)
	text.TextSize = 20
	text.TextStyle = fyne.TextStyle{Bold: true}

	// The glyph is nudged up via an overlay; Move() would be overridden by the
	// surrounding layout.
	glyph := container.NewCenter(container.New(&OverlayLayout{YOffset: -15}, text))
	content := container.NewStack(background, glyph)
	content.Resize(size)

	b := &mentionToggle{active: active, text: text, content: content}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	b.applyState()
	return b
}

func (b *mentionToggle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

func (b *mentionToggle) SetActive(active bool) {
	b.active = active
	b.applyState()
}

func (b *mentionToggle) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.applyState()
}

func (b *mentionToggle) MouseOut() {
	b.hovered = false
	b.applyState()
}

func (b *mentionToggle) applyState() {
	if b.active != b.hovered {
		b.text.Color = theme.Colors.TextPrimary
	} else {
		b.text.Color = theme.Colors.TimestampText
	}
	b.text.Refresh()
}
