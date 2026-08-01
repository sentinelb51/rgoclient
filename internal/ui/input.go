package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
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

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	maxInputLines    = 8
	maxReplies       = 5
	attachPreviewW   = 200
	attachPreviewImg = 150
	attachPreviewGen = 64

	// Reply composer card geometry.
	replyCardHeight = 28
	replyAvatarSize = 18
	replyTextSize   = 13
	replyButtonSize = 20
	replyIconSize   = 18
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
// Shift+Enter for newlines, and manages pending attachments and replies.
type MessageInput struct {
	widget.Entry
	OnSubmit func(string)

	// OnEditLast fires when Up is pressed in an empty composer: the app opens the
	// in-place editor on the user's newest message.
	OnEditLast func()

	Attachments         []Attachment
	AttachmentContainer *fyne.Container
	Replies             []Reply
	ReplyContainer      *fyne.Container

	deps         Deps
	shiftPressed bool
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

	// Empty reply/attachment rows would still reserve a gap above the input bar,
	// so keep them hidden until they hold something.
	m.ReplyContainer.Hide()
	m.AttachmentContainer.Hide()

	return m
}

// MinSize grows the entry up to maxInputLines as the user types.
func (m *MessageInput) MinSize() fyne.Size { return composerMinSize(&m.Entry) }

// composerMinSize sizes a growing composer-style entry: one line per newline up
// to maxInputLines, plus the entry's inner padding and border insets.
func composerMinSize(e *widget.Entry) fyne.Size {
	size := e.MinSize()
	lines := min(max(strings.Count(e.Text, "\n")+1, 1), maxInputLines)
	border := e.Theme().Size(fynetheme.SizeNameInputBorder)
	size.Height = lineHeight(fynetheme.TextSize())*float32(lines) + fynetheme.InnerPadding()*2 + border*2

	return size
}

// lineHeights memoises the measured height of one text line per size: MinSize
// runs on every layout pass, so re-measuring there is wasted work. UI thread
// only, hence unsynchronised.
var lineHeights = map[float32]float32{}

func lineHeight(textSize float32) float32 {
	h, ok := lineHeights[textSize]
	if !ok {
		h = fyne.MeasureText("M", textSize, fyne.TextStyle{}).Height
		lineHeights[textSize] = h
	}

	return h
}

/* Keyboard */

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

// TypedKey sends the message on Enter, inserts a newline on Shift+Enter, cancels
// pending replies/attachments on Escape, starts editing the last own message on
// Up in an empty composer, and otherwise defers to the embedded entry, refreshing
// so MinSize recomputes.
func (m *MessageInput) TypedKey(key *fyne.KeyEvent) {
	switch {
	case key.Name == fyne.KeyUp && m.Text == "":
		if m.OnEditLast != nil {
			m.OnEditLast()
		}
	case key.Name == fyne.KeyEscape:
		if len(m.Replies) > 0 || len(m.Attachments) > 0 {
			m.ClearReplies()
			m.ClearAttachments()
			return
		}
		m.Entry.TypedKey(key)
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

// pasteAsAttachment attaches an image or file path from the clipboard, reporting
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

/* Attachments */

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
	i := slices.IndexFunc(m.Attachments, func(a Attachment) bool { return a.Path == path })
	if i < 0 {
		return
	}

	m.Attachments = slices.Delete(m.Attachments, i, i+1)
	m.rebuildAttachments()
}

// ClearAttachments removes all queued files.
func (m *MessageInput) ClearAttachments() {
	m.Attachments = nil
	m.rebuildAttachments()
}

func (m *MessageInput) rebuildAttachments() {
	m.AttachmentContainer.Objects = nil
	if len(m.Attachments) == 0 {
		m.AttachmentContainer.Hide()
	} else {
		m.AttachmentContainer.Show()
	}

	for _, attachment := range m.Attachments {
		var size int
		if info, err := os.Stat(attachment.Path); err == nil {
			size = int(info.Size())
		}

		path := attachment.Path
		bar := attachmentBar(attachment.Name, size, func() { m.RemoveAttachment(path) })
		card := container.NewBorder(nil, bar, nil, nil, attachmentPreview(path))

		background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		background.CornerRadius = 8
		m.AttachmentContainer.Add(container.NewPadded(container.NewStack(background, container.NewPadded(card))))
	}

	m.AttachmentContainer.Refresh()
	m.Refresh()
}

func attachmentPreview(path string) fyne.CanvasObject {
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

/* Replies */

// AddReply adds a reply target, ignoring duplicates and respecting maxReplies.
func (m *MessageInput) AddReply(message *revoltgo.Message) {
	if len(m.Replies) >= maxReplies {
		return
	}
	if slices.ContainsFunc(m.Replies, func(r Reply) bool { return r.ID == message.ID }) {
		return
	}

	m.Replies = append(m.Replies, Reply{ID: message.ID, ChannelID: message.Channel})
	m.rebuildReplies()
}

// RemoveReply removes a reply target by message ID.
func (m *MessageInput) RemoveReply(messageID string) {
	i := slices.IndexFunc(m.Replies, func(r Reply) bool { return r.ID == messageID })
	if i < 0 {
		return
	}

	m.Replies = slices.Delete(m.Replies, i, i+1)
	m.rebuildReplies()
}

// ClearReplies removes all reply targets.
func (m *MessageInput) ClearReplies() {
	m.Replies = nil
	m.rebuildReplies()
}

func (m *MessageInput) rebuildReplies() {
	m.ReplyContainer.Objects = nil
	if len(m.Replies) == 0 {
		m.ReplyContainer.Hide()
	} else {
		m.ReplyContainer.Show()
	}

	for i := range m.Replies {
		m.ReplyContainer.Add(m.buildReplyCard(&m.Replies[i]))
	}

	m.ReplyContainer.Refresh()
	m.Refresh()
}

// buildReplyCard renders a slim composer chip for one pending reply: avatar,
// author, a truncated preview, a mention toggle, and a remove button. The card is
// outlined in the replied author's role colour, falling back to the app accent,
// and everything is vertically centred so the row reads as distinct elements
// rather than one blob.
func (m *MessageInput) buildReplyCard(reply *Reply) fyne.CanvasObject {
	author, content, avatarURL, accent := resolveReply(m.deps, reply.ChannelID, reply.ID)
	if author == "" {
		author = "Unknown"
	}
	if accent == nil {
		accent = theme.Colors.ServerSelectedBg
	}

	avatar := circularAvatar(m.deps.Images, avatarURL, fyne.NewSize(replyAvatarSize, replyAvatarSize))

	authorLabel := canvas.NewText(author, theme.Colors.TextPrimary)
	authorLabel.TextSize = replyTextSize
	authorLabel.TextStyle = fyne.TextStyle{Bold: true}

	contentLabel := canvas.NewText(content, theme.Colors.TimestampText)
	contentLabel.TextSize = replyTextSize

	// container.NewCenter vertically centres each element within the card's full
	// height; HBoxNoSpacing keeps the horizontal gaps under explicit control.
	left := HBoxNoSpacing(
		HorizontalSpacer(8),
		container.NewCenter(avatar),
		HorizontalSpacer(8),
		container.NewCenter(authorLabel),
		HorizontalSpacer(6),
		container.NewCenter(contentLabel),
	)

	var mention *replyIconButton
	mention = newReplyIconButton(assets.MentionIcon, true, reply.Mention, func() {
		mention.SetActive(!mention.active)
		reply.Mention = mention.active
	})
	right := HBoxNoSpacing(
		container.NewCenter(mention),
		HorizontalSpacer(2),
		container.NewCenter(newReplyIconButton(fynetheme.CancelIcon(), false, false, func() { m.RemoveReply(reply.ID) })),
		HorizontalSpacer(6),
	)

	row := NewMinHeightContainer(replyCardHeight, container.NewBorder(nil, nil, left, right))

	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	background.CornerRadius = 6
	background.StrokeColor = accent
	background.StrokeWidth = 1

	return container.NewStack(background, row)
}

// replyIconButton is a small square icon button used on the reply card, so the
// mention and close controls share one visual language. A momentary button
// (toggle=false) just brightens on hover; a toggle additionally shows an accent
// background and stays bright while active, dimming when idle.
type replyIconButton struct {
	tapBase
	icon *canvas.Image
	bg   *canvas.Rectangle

	toggle  bool
	active  bool
	hovered bool
}

var (
	_ fyne.Tappable     = (*replyIconButton)(nil)
	_ desktop.Hoverable = (*replyIconButton)(nil)
)

func newReplyIconButton(res fyne.Resource, toggle, active bool, onTap func()) *replyIconButton {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 5

	b := &replyIconButton{icon: newScaledIcon(res, replyIconSize), bg: bg, toggle: toggle, active: active}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	b.applyState()

	return b
}

func (b *replyIconButton) MinSize() fyne.Size {
	return fyne.NewSize(replyButtonSize, replyButtonSize)
}

func (b *replyIconButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.bg, container.NewCenter(b.icon)))
}

func (b *replyIconButton) SetActive(active bool) {
	b.active = active
	b.applyState()
}

func (b *replyIconButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.applyState()
}

func (b *replyIconButton) MouseOut() {
	b.hovered = false
	b.applyState()
}

func (b *replyIconButton) applyState() {
	switch {
	case b.toggle && b.active:
		b.bg.FillColor = theme.Colors.ReplyMentionActive
		b.icon.Translucency = 0
	case b.hovered:
		b.bg.FillColor = theme.Colors.SwiftActionHoverBg
		b.icon.Translucency = 0
	case b.toggle:
		b.bg.FillColor = color.Transparent
		b.icon.Translucency = 0.5 // an inactive toggle reads as "off"
	default:
		b.bg.FillColor = color.Transparent
		b.icon.Translucency = 0.25
	}

	b.bg.Refresh()
	canvas.Refresh(b.icon)
}
