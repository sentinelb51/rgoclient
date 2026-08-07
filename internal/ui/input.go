package ui

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.design/x/clipboard"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
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

var (
	_ desktop.Keyable   = (*MessageInput)(nil)
	_ desktop.Mouseable = (*MessageInput)(nil)
)

// Reply is a reply queued in the composer. It is domain.Reply plus the channel
// the quoted message lives in, which is bookkeeping of the composer's own: it is
// what the preview is resolved against and is not part of what gets sent.
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

	// OnEditLast fires when Up is pressed in an empty composer: the app opens the
	// in-place editor on the user's newest message.
	OnEditLast func()

	// OnFocusChanged reports focus changes so the composer card can light its
	// outline while the entry is live.
	OnFocusChanged func(focused bool)

	// Mentions is the autocomplete list, mounted by the composer above the reply
	// cards and hidden until the caret sits inside a mention. mentionStart is the
	// byte offset of the marker being completed, or -1, and mentionKind what it
	// opened — used only to notice when the caret has moved to a *different*
	// mention, so the highlight restarts.
	Mentions     *MentionPicker
	mentionStart int
	mentionKind  MentionKind

	Attachments         []domain.Attachment
	AttachmentContainer *fyne.Container
	Replies             []Reply
	ReplyContainer      *fyne.Container

	deps         Deps
	window       fyne.Window
	shiftPressed bool
	ctrlPressed  bool
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
	// so keep them hidden until they hold something.
	m.ReplyContainer.Hide()
	m.AttachmentContainer.Hide()

	return m
}

// MinSize grows the entry up to ComposerMaxLines as the user types.
func (m *MessageInput) MinSize() fyne.Size { return composerMinSize(&m.Entry) }

// composerMinSize sizes a growing composer-style entry: one line per newline up
// to maxInputLines, plus the padding Fyne draws around the entry's text.
//
// That padding is InnerPadding above and below, and nothing else. The input
// border looks like it should be added on top, but entryRenderer.Layout pays for
// that inset out of the text provider's own padding, so the border is already
// *inside* InnerPadding. Counting it twice left four dead pixels under the caret.
func composerMinSize(e *widget.Entry) fyne.Size {
	size := e.MinSize()
	lines := min(max(strings.Count(e.Text, "\n")+1, 1), int(theme.Sizes.ComposerMaxLines))
	th := e.Theme()
	size.Height = lineHeight(th.Size(fynetheme.SizeNameText))*float32(lines) +
		th.Size(fynetheme.SizeNameInnerPadding)*2

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

func (m *MessageInput) FocusGained() {
	m.Entry.FocusGained()
	if m.OnFocusChanged != nil {
		m.OnFocusChanged(true)
	}
}

// FocusLost deliberately leaves the picker open. Fyne unfocuses on the mouse
// *press* and then decides where the tap lands by hit-testing again on the
// release, so closing the picker here resized the composer out from under the
// click: the first click on anything — a picker row included — was spent
// dismissing the picker and never reached its target. Visibility follows the
// caret instead, which is also the truth of it: a half-typed mention is still in
// the text whether or not the entry is live.
func (m *MessageInput) FocusLost() {
	m.shiftPressed = false
	m.Entry.FocusLost()
	if m.OnFocusChanged != nil {
		m.OnFocusChanged(false)
	}
}

// MouseDown lets the embedded entry place the caret, then re-evaluates the
// picker against where it landed. The caret moves on the press, so this is the
// hook rather than Tapped — widget.Entry.Tapped does no positioning at all — and
// clicking out of a half-typed mention has to close the picker now that blurring
// no longer does.
//
// Hiding it here moves nothing under the pointer: the composer card is hung from
// the bottom of the dock and the picker sits above the entry, so the card loses
// that height off its top edge and the entry stays where it was pressed.
func (m *MessageInput) MouseDown(e *desktop.MouseEvent) {
	m.Entry.MouseDown(e)
	m.syncMentions()
}

func (m *MessageInput) KeyDown(key *fyne.KeyEvent) {
	switch key.Name {
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		m.shiftPressed = true
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		m.ctrlPressed = true
	}
}

func (m *MessageInput) KeyUp(key *fyne.KeyEvent) {
	switch key.Name {
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		m.shiftPressed = false
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		m.ctrlPressed = false
	}
}

// TypedKey sends the message on Enter, inserts a newline on Shift+Enter, cancels
// pending replies/attachments on Escape, starts editing the last own message on
// Up in an empty composer, and otherwise defers to the embedded entry, refreshing
// so MinSize recomputes.
//
// Which of Enter and Ctrl+Enter sends is a setting; Shift+Enter is a newline
// either way, so the habit that works everywhere else keeps working.
//
// An open mention picker gets first refusal on the navigation keys, since it
// binds the same ones: Enter would send the half-written message and Up would
// open the editor on the previous one.
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
	case m.shiftPressed || !m.sends():
		m.TypedRune('\n')
		m.Refresh()
	default:
		if m.OnSubmit != nil {
			m.OnSubmit(m.Text)
		}
		m.Refresh()
	}
}

// sends reports whether the Enter now being handled is the sending one. With
// "Enter sends" off it is Ctrl+Enter instead, which is why the modifier is
// tracked at all.
func (m *MessageInput) sends() bool {
	if config.Current().Behaviour.EnterSends {
		return !m.ctrlPressed
	}

	return m.ctrlPressed
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

/* The mention picker */

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
	start, kind, query, ok := m.mentionQuery()
	if ok {
		if start != m.mentionStart || kind != m.mentionKind {
			m.Mentions.Reset() // a different mention starts at the top of its list
		}
		if m.Mentions.Update(kind, query) {
			m.mentionStart, m.mentionKind = start, kind
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

// mentionQuery finds the mention the caret sits in: the byte offset of its
// marker, what that marker mentions, and the text typed since. A mention only
// opens at the start of the message or after whitespace, so an email address —
// or any other "foo@bar" in running text — never summons the picker, and a "#"
// mid-word stays a "#".
//
// A heading's "# " opens the channel list for the one keystroke before the
// space, which then closes it: the marker is the same character, and refusing
// the picker at the start of a line would cost every mention typed there.
func (m *MessageInput) mentionQuery() (start int, kind MentionKind, query string, ok bool) {
	cursor := m.cursorOffset()

	// Walking back from the caret stops at the first space, so the query can
	// never span words and the scan is bounded by the current word's length. It
	// walks the string itself rather than a []rune copy of it: this runs on every
	// keystroke, before any early-out, and a message is not short.
	for i := cursor; i > 0; {
		r, size := utf8.DecodeLastRuneInString(m.Text[:i])
		i -= size

		if unicode.IsSpace(r) {
			return 0, 0, "", false
		}

		marked, isMarker := markerKind(r)
		if !isMarker {
			continue
		}
		if before, _ := utf8.DecodeLastRuneInString(m.Text[:i]); i > 0 && !unicode.IsSpace(before) {
			return 0, 0, "", false
		}

		return i, marked, m.Text[i+1 : cursor], true
	}

	return 0, 0, "", false
}

// acceptMention swaps the marked query under the caret for a Revolt mention
// token, leaving the caret after it and a space ready for the next word.
//
// The span is re-derived here rather than taken from mentionStart: picking a
// candidate with the mouse blurs the entry before the tap is delivered, so by
// then the picker has already been hidden.
func (m *MessageInput) acceptMention(candidate MentionCandidate) {
	start, _, _, ok := m.mentionQuery()
	if !ok {
		return
	}

	cursor := m.cursorOffset()
	token := candidate.token()
	text := m.Text[:start] + token + m.Text[cursor:]

	m.mentionStart = -1
	m.hideMentions()

	m.SetText(text)
	m.CursorRow, m.CursorColumn = cursorPosition(text, start+len(token))
	m.Refresh()

	if m.window != nil {
		m.window.Canvas().Focus(m)
	}
}

// cursorOffset returns the caret as a byte offset into Text. Fyne tracks it as a
// row/column pair — what drawing the caret needs, not what slicing the text
// around it does. A byte offset is what the mention helpers actually want, and
// finding one walks the string in place: splitting Text into lines on every
// keystroke allocated the whole message twice over for one number.
func (m *MessageInput) cursorOffset() int {
	var offset int
	for range m.CursorRow {
		i := strings.IndexByte(m.Text[offset:], '\n')
		if i < 0 {
			return len(m.Text)
		}
		offset += i + 1
	}

	line := m.Text[offset:]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}

	// The column is counted in runes, so step the line one rune at a time; ranging
	// a string yields exactly the byte index each rune starts at.
	var col int
	for i := range line {
		if col == m.CursorColumn {
			return offset + i
		}
		col++
	}

	return offset + len(line)
}

// cursorPosition is cursorOffset's inverse, turning a byte offset back into the
// row and rune column the entry draws from.
func cursorPosition(text string, offset int) (row, col int) {
	offset = min(max(offset, 0), len(text))

	var start int
	for {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 || start+i >= offset {
			break
		}
		start += i + 1
		row++
	}

	return row, utf8.RuneCountInString(text[start:offset])
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
	m.Attachments = append(m.Attachments, domain.Attachment{Path: path, Name: filepath.Base(path)})
	m.rebuildAttachments()
}

// RemoveAttachment removes a queued file by path.
func (m *MessageInput) RemoveAttachment(path string) {
	i := slices.IndexFunc(m.Attachments, func(a domain.Attachment) bool { return a.Path == path })
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

		// The preview sits inside the card's padding, so the outline goes on the
		// background itself rather than over the content the way a message
		// attachment's does.
		background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		background.CornerRadius = 8
		Outline(background)
		m.AttachmentContainer.Add(container.NewPadded(container.NewStack(background, container.NewPadded(card))))
	}

	m.AttachmentContainer.Refresh()
	m.Refresh()
}

func attachmentPreview(path string) fyne.CanvasObject {
	if domain.FileKindOf(path) == domain.FileImage {
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
func (m *MessageInput) AddReply(message *domain.Message) {
	if len(m.Replies) >= maxReplies {
		return
	}
	if slices.ContainsFunc(m.Replies, func(r Reply) bool { return r.ID == message.ID }) {
		return
	}

	m.Replies = append(m.Replies, Reply{ID: message.ID, ChannelID: message.ChannelID})
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

/* Slowmode */

// SlowmodeBadge floats over the bottom-right of the message column, just above
// the composer card, and reports the channel's send cooldown: what it is while
// the user may send, and what is left of it while they may not.
//
// It sits *outside* the card rather than in it. Inside, it was furniture the
// entry had to make room for — every relabelling took width off what was being
// typed. Out here it is a marker on the conversation instead, and the entry
// spans the card in every channel.
//
// Nothing is drawn behind it: like the composer card it hangs over the message
// column, and a second filled surface a few pixels above the first read as a
// bar growing out of the card. Bare text is instead sized up until it holds
// itself apart from the conversation passing underneath. The stopwatch is drawn
// from canvas primitives rather than an icon resource for the same reason the
// channel glyphs are — a stroked shape takes a palette colour directly, so the
// mark and the text beside it change tone as one. Nothing here accepts a pointer
// event, so the messages behind the chip stay hoverable and right-clickable.
type SlowmodeBadge struct {
	widget.BaseWidget

	// OnResize fires when the chip's width or visibility changes, so whoever
	// mounted it can re-run that one row's layout: the chip is pinned to the row's
	// trailing edge, so every relabelling moves where it starts. Fyne re-lays out
	// for a *growing* minimum on its own; a shrinking one it leaves reserved.
	OnResize func()

	glyph   *fyne.Container // holds the stopwatch, redrawn when the tone changes
	label   *canvas.Text
	content *fyne.Container

	tone color.Color // what the glyph and label are currently drawn in
}

var _ fyne.Widget = (*SlowmodeBadge)(nil)

// NewSlowmodeBadge builds an empty, hidden badge. Set gives it something to say.
func NewSlowmodeBadge() *SlowmodeBadge {
	b := &SlowmodeBadge{
		glyph: container.NewStack(),
		label: canvas.NewText("", theme.Colors.SlowmodeText),
		tone:  theme.Colors.SlowmodeText,
	}
	b.label.TextSize = theme.Sizes.SlowmodeTextSize
	b.glyph.Objects = []fyne.CanvasObject{stopwatchGlyph(b.tone)}

	// As on the reply card, container.NewCenter is what lines the glyph up with the
	// text beside it: each takes its own minimum size inside a row as tall as the
	// larger of them.
	chip := HBoxNoSpacing(
		container.NewCenter(b.glyph),
		HorizontalSpacer(theme.Sizes.SlowmodeGap),
		container.NewCenter(b.label),
	)

	// The gap to the card below belongs to the chip, not to a spacer in the column
	// it hangs in: a spacer would hold that room open in every channel that has no
	// slowmode, where the chip itself simply isn't there.
	b.content = NewInset(chip, 0, theme.Sizes.SlowmodeDockGap, 0, theme.Sizes.SlowmodeInsetH)

	b.Hide()
	b.ExtendBaseWidget(b)

	return b
}

func (b *SlowmodeBadge) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

// Set labels the badge: cooldown is what the channel enforces, remaining what is
// left of it — zero when the user may send now. A zero cooldown hides the badge,
// which is every channel that has none.
func (b *SlowmodeBadge) Set(cooldown, remaining time.Duration) {
	if cooldown <= 0 {
		if b.Visible() {
			b.Hide()
			b.resized()
		}
		return
	}

	text, tone := util.ShortDuration(cooldown)+" slowmode", theme.Colors.SlowmodeText
	if remaining > 0 {
		text, tone = util.ShortDuration(remaining), theme.Colors.SlowmodeWaiting
	}

	// This runs once a second for the length of a cooldown, so a repaint — and the
	// relayout of the row it sits in — is worth asking whether anything actually
	// changed first.
	if tone != b.tone {
		b.tone = tone
		b.label.Color = tone
		b.glyph.Objects = []fyne.CanvasObject{stopwatchGlyph(tone)}
		b.glyph.Refresh()
	}

	moved := !b.Visible()
	if text != b.label.Text {
		b.label.Text = text
		b.content.Refresh() // the chip's own width moved with its text
		moved = true
	}

	b.Show()
	if moved {
		b.resized()
	}
}

// resized tells whoever mounted the badge that the width it occupies has changed.
func (b *SlowmodeBadge) resized() {
	if b.OnResize != nil {
		b.OnResize()
	}
}

// stopwatchGlyph draws a stopwatch in col, on the same 20-unit grid the channel
// glyphs are drawn on so the two read as one icon set.
func stopwatchGlyph(col color.Color) fyne.CanvasObject {
	size := theme.Sizes.SlowmodeGlyphSize
	scale := size / 20

	line := func(x1, y1, x2, y2 float32) *canvas.Line {
		l := canvas.NewLine(col)
		l.Position1 = fyne.NewPos(x1*scale, y1*scale)
		l.Position2 = fyne.NewPos(x2*scale, y2*scale)
		l.StrokeWidth = 2 * scale
		return l
	}

	dial := canvas.NewCircle(color.Transparent)
	dial.StrokeColor = col
	dial.StrokeWidth = 2 * scale
	dial.Move(fyne.NewPos(3.5*scale, 5.5*scale))
	dial.Resize(fyne.NewSize(13*scale, 13*scale))

	glyph := container.NewWithoutLayout(
		dial,
		line(7.5, 2.5, 12.5, 2.5), // the crown
		line(10, 2.5, 10, 5.5),    // its stem
		line(10, 12, 13.2, 9.2),   // the hand
	)

	return container.NewGridWrap(fyne.NewSize(size, size), glyph)
}

/* The mention picker */

const (
	// mentionMaxRows bounds both the picker's height and its per-keystroke work:
	// filtering stops counting matches past this and the surplus is reported as a
	// "+N more" hint instead of a scrolling list. A picker you have to scroll is
	// slower to use than one that tells you to keep typing.
	mentionMaxRows = 8

	// mentionNameMaxRunes keeps one very long display name from stretching a row
	// wider than the composer. The picker's rows are packed left, so unlike a
	// sidebar row there is no column to ellipsise against.
	mentionNameMaxRunes = 32

	mentionRowInset = 8 // left/right breathing room inside a row
	mentionRowGap   = 8 // between avatar, name and handle
)

// MentionKind is what a mention names, which is both the marker that opens the
// picker and the wire form it is accepted as: Revolt writes a user as <@id> and
// a channel as <#id>.
type MentionKind uint8

const (
	MentionUser MentionKind = iota
	MentionChannel
)

// marker is the character that opens a mention of this kind.
func (k MentionKind) marker() string {
	if k == MentionChannel {
		return "#"
	}

	return "@"
}

// markerKind reports what a rune opens a mention of, or ok=false when it opens
// nothing. It is marker's inverse and the pair is the only place the two
// characters are named.
func markerKind(r rune) (MentionKind, bool) {
	switch r {
	case '@':
		return MentionUser, true
	case '#':
		return MentionChannel, true
	}

	return 0, false
}

// MentionCandidate is one person or channel the mention picker can insert. Name
// and Username are both matched against what the user has typed, so either the
// nickname shown in chat or the underlying @handle finds someone; a channel has
// only its name.
type MentionCandidate struct {
	Kind MentionKind
	ID   string

	Name      string // nickname / display name / channel name, as the client shows it
	Username  string // the @handle, without the @; "" for a channel
	AvatarURL string
	Color     color.Color // role colour; nil falls back to the standard text colour

	// ChannelKind decides the glyph a channel candidate is led by, where a user is
	// led by their avatar. Meaningless for a user.
	ChannelKind domain.ChannelKind

	// Lowercased match keys, computed once when the candidate is built. The
	// picker filters the whole candidate set on every keystroke, so folding case
	// here rather than per keystroke is what keeps a 2000-member server cheap.
	nameKey, userKey string
}

// NewMentionCandidate builds a candidate for a person, with its match keys
// precomputed.
func NewMentionCandidate(userID, name, username, avatarURL string, roleColor color.Color) MentionCandidate {
	return MentionCandidate{
		Kind:      MentionUser,
		ID:        userID,
		Name:      name,
		Username:  username,
		AvatarURL: avatarURL,
		Color:     roleColor,
		nameKey:   strings.ToLower(name),
		userKey:   strings.ToLower(username),
	}
}

// NewChannelCandidate builds a candidate for a channel. It takes the resolved
// channel rather than its parts because that is what the sidebar already holds:
// one Store.Channel walk builds the rows and the candidates alike.
func NewChannelCandidate(channel domain.Channel) MentionCandidate {
	return MentionCandidate{
		Kind:        MentionChannel,
		ID:          channel.ID,
		Name:        channel.Name,
		ChannelKind: channel.Kind,
		nameKey:     strings.ToLower(channel.Name),
	}
}

// token is the wire form the composer inserts for this candidate, with the space
// that readies the next word.
func (c *MentionCandidate) token() string {
	return "<" + c.Kind.marker() + c.ID + "> "
}

// SortCandidates orders a list by display name, case-insensitively, in place.
// The server's members already arrive sorted; this is for the ones assembled a
// recipient at a time, which would otherwise shuffle every time the list was
// rebuilt. It sorts on the key the candidate already carries, so nothing is
// lowered twice.
func SortCandidates(candidates []MentionCandidate) {
	slices.SortFunc(candidates, func(x, y MentionCandidate) int {
		return strings.Compare(x.nameKey, y.nameKey)
	})
}

// rank scores a candidate against an already-lowercased query: 0 when the
// display name or handle starts with it, 1 when either merely contains it, -1
// for no match. An empty query (the bare "@") matches everyone at rank 0, so
// typing @ alone opens the picker on the full list.
//
// The receiver is a pointer only because filter calls this twice per candidate
// per keystroke, and a MentionCandidate is wide enough that copying one that
// many times is the bulk of the work on a large server.
func (c *MentionCandidate) rank(query string) int {
	switch {
	case query == "",
		strings.HasPrefix(c.nameKey, query), strings.HasPrefix(c.userKey, query):
		return 0
	case strings.Contains(c.nameKey, query), strings.Contains(c.userKey, query):
		return 1
	}

	return -1
}

// MentionPicker is the autocomplete list the composer shows while a mention is
// being typed. It lives inside the composer card rather than floating over the
// message area: a Fyne pop-up takes canvas focus, which would pull it away from
// the entry and stop the typing that drives the picker in the first place.
//
// Its row widgets are pooled — mentionMaxRows of them, built once and re-set as
// the query changes — so a keystroke re-labels existing widgets instead of
// building and discarding a list of new ones.
type MentionPicker struct {
	widget.BaseWidget
	images   *cache.ImageCache
	onAccept func(MentionCandidate)

	// One pool per kind, replaced independently: a server's people change as the
	// gateway resolves them, its channels only when the sidebar is rebuilt.
	users    []MentionCandidate
	channels []MentionCandidate

	matches  []MentionCandidate
	overflow int // matches beyond mentionMaxRows, reported by the footer
	selected int

	// The kind and query the rows currently show, and whether they still reflect
	// them. A keystroke that leaves both alone — a caret move inside the same
	// mention, a Refresh — then costs nothing at all.
	kind  MentionKind
	query string
	fresh bool

	rows      []*mentionRow
	footer    *canvas.Text
	footerRow *fyne.Container // the footer's padded wrapper, shown/hidden as a unit
	content   fyne.CanvasObject
}

var _ fyne.Widget = (*MentionPicker)(nil)

// NewMentionPicker builds an empty, hidden picker. onAccept receives the chosen
// candidate; the composer turns it into a mention token.
func NewMentionPicker(images *cache.ImageCache, onAccept func(MentionCandidate)) *MentionPicker {
	p := &MentionPicker{images: images, onAccept: onAccept}

	rowBox := VBoxNoSpacing()
	for i := range mentionMaxRows {
		row := newMentionRow(images, func() { p.selectRow(i) }, func() { p.selectRow(i); p.Accept() })
		row.Hide()
		p.rows = append(p.rows, row)
		rowBox.Add(row)
	}

	p.footer = canvas.NewText("", theme.Colors.MentionHandleText)
	p.footer.TextSize = theme.Sizes.MentionHandleSize
	p.footerRow = NewInset(p.footer, 2, 4, mentionRowInset, mentionRowInset)
	p.footerRow.Hide()

	rule := canvas.NewRectangle(theme.Colors.DaySeparatorLine)
	rule.SetMinSize(fyne.NewSize(0, theme.Sizes.DaySeparatorThickness))

	p.content = VBoxNoSpacing(rowBox, p.footerRow, rule)
	p.Hide()
	p.ExtendBaseWidget(p)

	return p
}

func (p *MentionPicker) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.content)
}

// SetCandidates replaces one of the pools the picker filters. Call it when the
// open channel changes, its membership does, or the channel sidebar is rebuilt;
// the picker snapshots the list and never goes to the network itself.
func (p *MentionPicker) SetCandidates(kind MentionKind, candidates []MentionCandidate) {
	if kind == MentionChannel {
		p.channels = candidates
	} else {
		p.users = candidates
	}
	p.fresh = false

	// The rows recognise a candidate by ID and skip rebuilding for one they
	// already show. The same person can arrive here under a new nickname, colour
	// or avatar, so a new list is what un-teaches them.
	for _, row := range p.rows {
		row.invalidate()
	}

	// An open picker outlives the entry's focus, so it can outlive the channel it
	// was opened in — re-run its query rather than leave it offering people from
	// the list just replaced.
	if p.Visible() && !p.Update(p.kind, p.query) {
		p.Reset()
		p.Hide()
	}
}

// pool is the candidate list a kind filters against.
func (p *MentionPicker) pool(kind MentionKind) []MentionCandidate {
	if kind == MentionChannel {
		return p.channels
	}

	return p.users
}

// Update refilters kind's pool against query — the text between the marker and
// the caret — and reports whether anything matched. A false result means the
// caller should hide the picker: there is nothing to offer.
func (p *MentionPicker) Update(kind MentionKind, query string) bool {
	query = strings.ToLower(query)
	if p.fresh && kind == p.kind && query == p.query {
		return len(p.matches) > 0
	}

	p.filter(kind, query)
	p.kind, p.query, p.fresh = kind, query, true
	if len(p.matches) == 0 {
		return false
	}

	p.selected = min(p.selected, len(p.matches)-1)
	for i, row := range p.rows {
		if i >= len(p.matches) {
			row.Hide()
			continue
		}
		row.set(p.matches[i], i == p.selected)
		row.Show()
	}

	if p.overflow > 0 {
		p.footer.Text = fmt.Sprintf("+%d more — keep typing", p.overflow)
		p.footer.Refresh()
		p.footerRow.Show()
	} else {
		p.footerRow.Hide()
	}

	p.Refresh()

	return true
}

// filter collects the best mentionMaxRows matches, prefix hits before substring
// hits. Two passes over the candidates beat one pass plus a sort: the set is
// walked at most twice with a string comparison per entry and nothing is
// allocated, which is what lets this run on every keystroke.
func (p *MentionPicker) filter(kind MentionKind, query string) {
	all := p.pool(kind)

	p.matches, p.overflow = p.matches[:0], 0
	for pass := range 2 {
		for i := range all {
			candidate := &all[i]
			if candidate.rank(query) != pass {
				continue
			}
			if len(p.matches) < mentionMaxRows {
				p.matches = append(p.matches, *candidate)
			} else {
				p.overflow++
			}
		}
	}
}

// Step moves the highlight by delta, wrapping at both ends so Up from the first
// row lands on the last. Named Step rather than Move because a fyne.Widget's
// Move is the one that positions it on the canvas.
func (p *MentionPicker) Step(delta int) {
	if len(p.matches) == 0 {
		return
	}

	n := len(p.matches)
	p.selectRow(((p.selected+delta)%n + n) % n)
}

// selectRow highlights row i, repainting only the two rows that changed.
func (p *MentionPicker) selectRow(i int) {
	if i == p.selected || i >= len(p.matches) {
		return
	}

	p.rows[p.selected].setSelected(false)
	p.selected = i
	p.rows[i].setSelected(true)
}

// Accept hands the highlighted candidate to the composer.
func (p *MentionPicker) Accept() {
	if p.selected < len(p.matches) && p.onAccept != nil {
		p.onAccept(p.matches[p.selected])
	}
}

// Reset clears the highlight so the next mention starts at the top of its list.
// The rows no longer reflect their query afterwards — the new first row has to be
// highlighted — so the next Update refilters even if nothing was typed.
func (p *MentionPicker) Reset() {
	if p.selected != 0 && p.selected < len(p.rows) {
		p.rows[p.selected].setSelected(false)
	}
	p.selected = 0
	p.fresh = false
}

// mentionRow is one pooled row of the picker: a lead — a person's avatar or a
// channel's type glyph — the name in the author's role colour, and the dim
// @handle a channel leaves empty.
type mentionRow struct {
	tapBase
	images *cache.ImageCache

	background  *canvas.Rectangle
	lead        *fyne.Container
	placeholder *canvas.Circle
	name        *canvas.Text
	handle      *canvas.Text
	content     fyne.CanvasObject

	// generation guards a reused row against a slow avatar load: by the time an
	// image arrives the row may already show somebody else.
	generation int

	// What the row currently shows and how it is drawn, so a keystroke that leaves
	// a row on the same candidate doesn't rebuild it. Filtering re-sets all eight
	// rows on every character typed, and most of them hold still. The kind is part
	// of the identity: nothing stops the two pools sharing an ID.
	kind     MentionKind
	id       string
	selected bool

	onHover func()
}

var (
	_ fyne.Tappable     = (*mentionRow)(nil)
	_ desktop.Hoverable = (*mentionRow)(nil)
)

func newMentionRow(images *cache.ImageCache, onHover, onTap func()) *mentionRow {
	size := fyne.NewSize(theme.Sizes.MentionAvatarSize, theme.Sizes.MentionAvatarSize)

	r := &mentionRow{
		images:      images,
		background:  canvas.NewRectangle(color.Transparent),
		placeholder: canvas.NewCircle(theme.Colors.AvatarPlaceholder),
		name:        canvas.NewText("", theme.Colors.TextPrimary),
		handle:      canvas.NewText("", theme.Colors.MentionHandleText),
		onHover:     onHover,
	}
	r.lead = container.NewGridWrap(size, r.placeholder)
	r.name.TextSize = theme.Sizes.MentionNameSize
	r.name.TextStyle = fyne.TextStyle{Bold: true}
	r.handle.TextSize = theme.Sizes.MentionHandleSize

	// As on the reply card, container.NewCenter is what vertically centres each
	// element inside the row's full height; HBoxNoSpacing keeps the horizontal
	// gaps explicit rather than inheriting theme padding.
	row := HBoxNoSpacing(
		HorizontalSpacer(mentionRowInset),
		container.NewCenter(r.lead),
		HorizontalSpacer(mentionRowGap),
		container.NewCenter(r.name),
		HorizontalSpacer(mentionRowGap),
		container.NewCenter(r.handle),
	)
	r.content = container.NewStack(r.background, row)

	r.onTap = onTap
	r.ExtendBaseWidget(r)

	return r
}

func (r *mentionRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (r *mentionRow) MinSize() fyne.Size {
	return fyne.NewSize(0, theme.Sizes.MentionRowHeight)
}

func (r *mentionRow) MouseIn(*desktop.MouseEvent) {
	if r.onHover != nil {
		r.onHover()
	}
}

func (r *mentionRow) MouseOut() {}

// set re-labels the row for a candidate. Only the avatar can be slow, and it is
// fetched through the shared cache, so a row that scrolls past under a fast
// typist costs one map lookup.
func (r *mentionRow) set(candidate MentionCandidate, selected bool) {
	if r.id != "" && r.id == candidate.ID && r.kind == candidate.Kind {
		r.setSelected(selected)
		return
	}
	r.kind, r.id = candidate.Kind, candidate.ID

	r.generation++
	generation := r.generation

	r.name.Text = util.Truncate(candidate.Name, mentionNameMaxRunes)
	r.name.Color = theme.Colors.TextPrimary
	if candidate.Color != nil {
		r.name.Color = solidColor(candidate.Color)
	}

	// A channel has no handle behind its name; the marker it was found by is
	// already in the glyph beside it, so the slot is simply left empty.
	r.handle.Text = ""
	if candidate.Kind == MentionUser {
		r.handle.Text = "@" + candidate.Username
	}
	r.name.Refresh()
	r.handle.Refresh()
	r.setSelected(selected)

	if candidate.Kind == MentionChannel {
		r.lead.Objects = []fyne.CanvasObject{ChannelGlyph(candidate.ChannelKind)}
		r.lead.Refresh()
		return
	}

	// Back to the placeholder first: the row may be showing the previous
	// candidate's face, and this one may have no avatar at all.
	r.lead.Objects = []fyne.CanvasObject{r.placeholder}
	r.lead.Refresh()
	if candidate.AvatarURL == "" || r.images == nil {
		return
	}

	size := fyne.NewSize(theme.Sizes.MentionAvatarSize, theme.Sizes.MentionAvatarSize)
	r.images.LoadAsync(imageCacheID(candidate.AvatarURL), candidate.AvatarURL, true, func(img image.Image) {
		if r.generation != generation {
			return
		}

		face := canvas.NewImageFromImage(img)
		face.FillMode = canvas.ImageFillContain
		face.SetMinSize(size)
		r.lead.Objects = []fyne.CanvasObject{face}
		r.lead.Refresh()
	})
}

// invalidate forgets what the row shows, so the next set rebuilds it even for
// the same candidate.
func (r *mentionRow) invalidate() { r.id = "" }

func (r *mentionRow) setSelected(selected bool) {
	if r.selected == selected {
		return
	}
	r.selected = selected

	r.background.FillColor = color.Transparent
	if selected {
		r.background.FillColor = theme.Colors.MentionRowSelectedBg
	}
	r.background.Refresh()
}
