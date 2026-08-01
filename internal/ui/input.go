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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"
	"golang.design/x/clipboard"

	"RGOClient/assets"
	"RGOClient/internal/cache"
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

	// OnEditLast fires when Up is pressed in an empty composer: the app opens the
	// in-place editor on the user's newest message.
	OnEditLast func()

	// OnFocusChanged reports focus changes so the composer card can light its
	// outline while the entry is live.
	OnFocusChanged func(focused bool)

	// Mentions is the @autocomplete list, mounted by the composer above the reply
	// cards and hidden until the caret sits inside a mention. mentionStart is the
	// rune index of the '@' being completed, or -1 — used only to notice when the
	// caret has moved to a *different* mention, so the highlight restarts.
	Mentions     *MentionPicker
	mentionStart int

	Attachments         []Attachment
	AttachmentContainer *fyne.Container
	Replies             []Reply
	ReplyContainer      *fyne.Container

	deps         Deps
	window       fyne.Window
	shiftPressed bool
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

// MinSize grows the entry up to maxInputLines as the user types.
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
	lines := min(max(strings.Count(e.Text, "\n")+1, 1), maxInputLines)
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

// TypedKey sends the message on Enter, inserts a newline on Shift+Enter, cancels
// pending replies/attachments on Escape, starts editing the last own message on
// Up in an empty composer, and otherwise defers to the embedded entry, refreshing
// so MinSize recomputes.
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

// MentionCandidate is one person the mention picker can insert. Name and
// Username are both matched against what the user has typed, so either the
// nickname shown in chat or the underlying @handle finds someone.
type MentionCandidate struct {
	UserID    string
	Name      string // nickname / display name, as chat shows it
	Username  string // the @handle, without the @
	AvatarURL string
	Color     color.Color // role colour; nil falls back to the standard text colour

	// Lowercased match keys, computed once when the candidate is built. The
	// picker filters the whole candidate set on every keystroke, so folding case
	// here rather than per keystroke is what keeps a 2000-member server cheap.
	nameKey, userKey string
}

// NewMentionCandidate builds a candidate with its match keys precomputed.
func NewMentionCandidate(userID, name, username, avatarURL string, roleColor color.Color) MentionCandidate {
	return MentionCandidate{
		UserID:    userID,
		Name:      name,
		Username:  username,
		AvatarURL: avatarURL,
		Color:     roleColor,
		nameKey:   strings.ToLower(name),
		userKey:   strings.ToLower(username),
	}
}

// rank scores a candidate against an already-lowercased query: 0 when the
// display name or handle starts with it, 1 when either merely contains it, -1
// for no match. An empty query (the bare "@") matches everyone at rank 0, so
// typing @ alone opens the picker on the full list.
func (c MentionCandidate) rank(query string) int {
	switch {
	case query == "",
		strings.HasPrefix(c.nameKey, query), strings.HasPrefix(c.userKey, query):
		return 0
	case strings.Contains(c.nameKey, query), strings.Contains(c.userKey, query):
		return 1
	}

	return -1
}

// MentionPicker is the autocomplete list the composer shows while an @mention is
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

	all      []MentionCandidate
	matches  []MentionCandidate
	overflow int // matches beyond mentionMaxRows, reported by the footer
	selected int

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

// SetCandidates replaces the pool the picker filters. Call it when the open
// channel changes or its membership does; the picker snapshots this list and
// never goes to the network itself.
func (p *MentionPicker) SetCandidates(candidates []MentionCandidate) {
	p.all = candidates
}

// Update refilters against query — the text between the "@" and the caret — and
// reports whether anything matched. A false result means the caller should hide
// the picker: there is nobody to offer.
func (p *MentionPicker) Update(query string) bool {
	p.filter(strings.ToLower(query))
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
func (p *MentionPicker) filter(query string) {
	p.matches, p.overflow = p.matches[:0], 0
	for pass := range 2 {
		for _, candidate := range p.all {
			if candidate.rank(query) != pass {
				continue
			}
			if len(p.matches) < mentionMaxRows {
				p.matches = append(p.matches, candidate)
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
func (p *MentionPicker) Reset() {
	if p.selected != 0 && p.selected < len(p.rows) {
		p.rows[p.selected].setSelected(false)
	}
	p.selected = 0
}

// mentionRow is one pooled row of the picker: avatar, display name in the
// author's role colour, and the dim @handle.
type mentionRow struct {
	tapBase
	images *cache.ImageCache

	background  *canvas.Rectangle
	avatar      *fyne.Container
	placeholder *canvas.Circle
	name        *canvas.Text
	handle      *canvas.Text
	content     fyne.CanvasObject

	// generation guards a reused row against a slow avatar load: by the time an
	// image arrives the row may already show somebody else.
	generation int

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
	r.avatar = container.NewGridWrap(size, r.placeholder)
	r.name.TextSize = theme.Sizes.MentionNameSize
	r.name.TextStyle = fyne.TextStyle{Bold: true}
	r.handle.TextSize = theme.Sizes.MentionHandleSize

	// As on the reply card, container.NewCenter is what vertically centres each
	// element inside the row's full height; HBoxNoSpacing keeps the horizontal
	// gaps explicit rather than inheriting theme padding.
	row := HBoxNoSpacing(
		HorizontalSpacer(mentionRowInset),
		container.NewCenter(r.avatar),
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
	r.generation++
	generation := r.generation

	r.name.Text = util.Truncate(candidate.Name, mentionNameMaxRunes)
	r.name.Color = theme.Colors.TextPrimary
	if candidate.Color != nil {
		r.name.Color = candidate.Color
	}
	r.handle.Text = "@" + candidate.Username
	r.name.Refresh()
	r.handle.Refresh()
	r.setSelected(selected)

	// Back to the placeholder first: the row may be showing the previous
	// candidate's face, and this one may have no avatar at all.
	r.avatar.Objects = []fyne.CanvasObject{r.placeholder}
	r.avatar.Refresh()
	if candidate.AvatarURL == "" || r.images == nil {
		return
	}

	size := fyne.NewSize(theme.Sizes.MentionAvatarSize, theme.Sizes.MentionAvatarSize)
	r.images.LoadAsync(avatarCacheID(candidate.AvatarURL), candidate.AvatarURL, true, func(img image.Image) {
		if r.generation != generation {
			return
		}

		face := canvas.NewImageFromImage(img)
		face.FillMode = canvas.ImageFillContain
		face.SetMinSize(size)
		r.avatar.Objects = []fyne.CanvasObject{face}
		r.avatar.Refresh()
	})
}

func (r *mentionRow) setSelected(selected bool) {
	r.background.FillColor = color.Transparent
	if selected {
		r.background.FillColor = theme.Colors.MentionRowSelectedBg
	}
	r.background.Refresh()
}
