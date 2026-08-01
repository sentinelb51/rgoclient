package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

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
// shift-enter for newlines, and manages pending attachments, replies, and the
// @mention picker.
type MessageInput struct {
	widget.Entry
	OnSubmit func(string)

	// OnEditLast fires when Up is pressed in an empty composer, Discord-style:
	// the app opens the in-place editor on the user's newest message.
	OnEditLast func()

	// OnFocusChanged reports focus changes so the composer card can light its
	// outline while the entry is live.
	OnFocusChanged func(focused bool)

	deps         Deps
	window       fyne.Window
	shiftPressed bool

	// Mentions is the @autocomplete list. The composer mounts it above the reply
	// cards; it stays hidden until the caret sits inside a mention. mentionStart
	// is the rune index of the '@' the picker is currently completing, or -1 —
	// used only to notice when the caret has moved to a *different* mention, so
	// the highlight restarts at the top of the new list.
	Mentions     *MentionPicker
	mentionStart int

	Attachments         []Attachment
	AttachmentContainer *fyne.Container
	Replies             []Reply
	ReplyContainer      *fyne.Container
}

// NewMessageInput creates a message input wired to the given dependencies. The
// window is what lets the composer accept dropped files and take keyboard focus
// back after a mouse interaction (picking a mention with the pointer blurs the
// entry, because Fyne unfocuses whenever a click lands on something that isn't
// focusable).
func NewMessageInput(deps Deps, window fyne.Window) *MessageInput {
	m := &MessageInput{
		deps:                deps,
		window:              window,
		mentionStart:        -1,
		AttachmentContainer: container.NewHBox(),
		ReplyContainer:      container.NewVBox(),
	}
	m.Mentions = NewMentionPicker(deps.Images, m.acceptMention)
	m.ExtendBaseWidget(m)
	m.MultiLine = true
	m.Wrapping = fyne.TextWrapWord
	// Empty reply/attachment rows would still reserve a gap above the input bar,
	// so keep them hidden until they actually hold something.
	m.ReplyContainer.Hide()
	m.AttachmentContainer.Hide()
	return m
}

// MinSize grows the entry up to maxInputLines as the user types.
func (m *MessageInput) MinSize() fyne.Size {
	return composerMinSize(&m.Entry)
}

// composerMinSize sizes a growing composer-style entry: one line per newline up
// to maxInputLines, plus the padding Fyne draws around the entry's text.
//
// That padding is InnerPadding above and below, and nothing else. The input
// border looks like it should be added on top — the entry does inset its content
// by the border — but entryRenderer.Layout pays for that inset out of the text
// provider's own padding (it sets textProvider().inset to the border size), so
// the border is already *inside* InnerPadding rather than extra to it. Counting
// it twice made every composer four pixels taller than its content, and since
// the entry top-aligns its text inside its scroller, all four landed as dead
// space beneath the caret.
func composerMinSize(e *widget.Entry) fyne.Size {
	size := e.MinSize()
	lines := min(max(strings.Count(e.Text, "\n")+1, 1), maxInputLines)
	th := e.Theme()
	size.Height = lineHeight(th.Size(fynetheme.SizeNameText))*float32(lines) +
		th.Size(fynetheme.SizeNameInnerPadding)*2
	return size
}

// lineHeights memoises the measured height of one text line per size: MinSize
// runs on every layout pass, so re-measuring there would be wasted work. Only
// touched on the UI thread.
var lineHeights = map[float32]float32{}

func lineHeight(textSize float32) float32 {
	h, ok := lineHeights[textSize]
	if !ok {
		h = fyne.MeasureText("M", textSize, fyne.TextStyle{}).Height
		lineHeights[textSize] = h
	}
	return h
}

func (m *MessageInput) FocusGained() {
	m.Entry.FocusGained()
	if m.OnFocusChanged != nil {
		m.OnFocusChanged(true)
	}
}

func (m *MessageInput) FocusLost() {
	m.shiftPressed = false
	m.Entry.FocusLost()
	// Hide the picker but leave the caret alone: acceptMention re-derives the
	// mention span from the caret, so a click on a picker row still resolves
	// even though that click blurred the entry on its way in.
	m.hideMentions()
	if m.OnFocusChanged != nil {
		m.OnFocusChanged(false)
	}
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

// TypedKey sends the message on Enter, inserts a newline on Shift+Enter,
// cancels pending replies/attachments on Escape, starts editing the last own
// message on Up in an empty composer, and otherwise defers to the embedded
// entry (refreshing so MinSize recomputes).
//
// An open mention picker gets first refusal on the navigation keys, since it
// wants the same ones the composer binds: Enter would send the half-written
// message and Up would open the editor on the previous one.
func (m *MessageInput) TypedKey(key *fyne.KeyEvent) {
	if m.Mentions.Visible() && m.handleMentionKey(key) {
		return
	}
	defer m.syncMentions()

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
	m.syncMentions()
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
	m.syncMentions()
}

// handleMentionKey lets an open picker consume a navigation key, reporting
// whether it did.
func (m *MessageInput) handleMentionKey(key *fyne.KeyEvent) bool {
	switch key.Name {
	case fyne.KeyUp:
		m.Mentions.Step(-1)
	case fyne.KeyDown:
		m.Mentions.Step(1)
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeyTab:
		m.Mentions.Accept()
	case fyne.KeyEscape:
		m.hideMentions()
	default:
		return false
	}
	return true
}

// syncMentions re-evaluates the picker against the caret after an input event.
// It is driven from the typing methods rather than from Entry.OnChanged because
// the picker also has to close when the caret merely *moves* out of a mention,
// which changes no text at all.
func (m *MessageInput) syncMentions() {
	start, query, ok := m.mentionQuery()
	if ok {
		if start != m.mentionStart {
			m.Mentions.Reset() // a different mention starts at the top of its list
		}
		if m.Mentions.Update(query) {
			m.mentionStart = start
			m.Mentions.Show()
			return
		}
	}
	m.mentionStart = -1
	m.hideMentions()
}

// hideMentions closes the picker without disturbing the text or the caret.
func (m *MessageInput) hideMentions() {
	if !m.Mentions.Visible() {
		return
	}
	m.Mentions.Reset()
	m.Mentions.Hide()
}

// mentionQuery finds the @mention the caret sits in: the rune index of its '@'
// and the text typed since. A mention only opens at the start of the message or
// after whitespace, so an email address — or any other "foo@bar" in running
// text — never summons the picker.
func (m *MessageInput) mentionQuery() (start int, query string, ok bool) {
	runes := []rune(m.Text)
	cursor := min(m.cursorIndex(), len(runes))

	// Walking back from the caret stops at the first space, so the query can
	// never span words and the scan is bounded by the current word's length.
	for i := cursor - 1; i >= 0; i-- {
		switch {
		case unicode.IsSpace(runes[i]):
			return 0, "", false
		case runes[i] == '@':
			if i > 0 && !unicode.IsSpace(runes[i-1]) {
				return 0, "", false
			}
			return i, string(runes[i+1 : cursor]), true
		}
	}
	return 0, "", false
}

// acceptMention swaps the "@query" under the caret for a Revolt mention token,
// leaving the caret after it and a space ready for the next word.
//
// The span is re-derived here rather than taken from mentionStart: picking a
// candidate with the mouse blurs the entry before the tap is delivered, so by
// then the picker has already been hidden.
func (m *MessageInput) acceptMention(candidate MentionCandidate) {
	start, _, ok := m.mentionQuery()
	if !ok {
		return
	}

	runes := []rune(m.Text)
	cursor := min(m.cursorIndex(), len(runes))
	token := "<@" + candidate.UserID + "> "
	text := string(runes[:start]) + token + string(runes[cursor:])

	m.mentionStart = -1
	m.hideMentions()

	m.SetText(text)
	m.CursorRow, m.CursorColumn = cursorPosition(text, start+len([]rune(token)))
	m.Refresh()

	if m.window != nil {
		m.window.Canvas().Focus(m)
	}
}

// cursorIndex returns the caret as a rune index into Text. Fyne tracks it as a
// row/column pair, which is what drawing the caret needs but not what editing
// the text around it does.
func (m *MessageInput) cursorIndex() int {
	lines := strings.Split(m.Text, "\n")
	index := 0
	for i := 0; i < m.CursorRow && i < len(lines); i++ {
		index += len([]rune(lines[i])) + 1 // + the newline itself
	}
	return index + m.CursorColumn
}

// cursorPosition is cursorIndex's inverse, turning a rune index back into the
// row/column pair the entry draws from.
func cursorPosition(text string, index int) (row, col int) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		n := len([]rune(line))
		if index <= n || i == len(lines)-1 {
			return i, min(max(index, 0), n)
		}
		index -= n + 1
	}
	return 0, 0
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

// RegisterDropHandler attaches files dropped onto the composer's window.
func (m *MessageInput) RegisterDropHandler() {
	m.window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
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
// author, a truncated preview, a mention toggle, and a remove button. The card
// is outlined in the replied author's role colour (falling back to the app
// accent) and everything is vertically centred so the row reads as distinct
// elements rather than a single blob.
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

	// container.NewCenter vertically centres each element within the card's
	// full height; HBoxNoSpacing keeps the horizontal gaps under explicit control.
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

// replyIconButton is a small square SVG-icon button used on the reply card, so
// the mention and close controls share one visual language. A momentary button
// (toggle=false) just brightens on hover; a toggle (toggle=true) additionally
// shows an accent background and stays bright while active, and dims when idle.
// The icon is a centred canvas.Image, which avoids the vertical-alignment
// guesswork of centring a text glyph.
type replyIconButton struct {
	tapBase
	toggle  bool
	active  bool
	hovered bool
	icon    *canvas.Image
	bg      *canvas.Rectangle
}

var (
	_ fyne.Tappable     = (*replyIconButton)(nil)
	_ desktop.Hoverable = (*replyIconButton)(nil)
)

func newReplyIconButton(res fyne.Resource, toggle, active bool, onTap func()) *replyIconButton {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 5

	b := &replyIconButton{toggle: toggle, active: active, icon: newScaledIcon(res, replyIconSize), bg: bg}
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
		b.icon.Translucency = 0.5 // inactive toggle reads as "off"
	default:
		b.bg.FillColor = color.Transparent
		b.icon.Translucency = 0.25
	}
	b.bg.Refresh()
	canvas.Refresh(b.icon)
}
