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
	"fyne.io/fyne/v2/layout"
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

	// uploadRefused is what a drop or a paste into a channel that forbids uploads
	// reports, from wherever the file came in.
	uploadRefused = "You can't upload files in this channel."
)

var (
	_ desktop.Keyable   = (*MessageInput)(nil)
	_ desktop.Mouseable = (*MessageInput)(nil)
)

// Reply is a reply queued in the composer: domain.Reply plus the channel the
// quoted message lives in, which the preview is resolved against and which is not
// part of what gets sent.
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

	// OnEscape fires on Escape with nothing pending to cancel, which is what the
	// jump bar answers to as well.
	OnEscape func()

	// OnRefused reports an input the composer would not take, so the app can say
	// why — dropping a file into a channel that forbids uploads would otherwise be
	// nothing happening, which is indistinguishable from a bug.
	OnRefused func(reason string)

	// OnTyping reports each keystroke and whether anything survived it. One callback
	// rather than a started/stopped pair: a backspace that empties the composer is
	// the clearest "stopped" there is, and arrives on the same path as the rest.
	OnTyping func(typing bool)

	// OnKeystroke reports which *kind* of key landed, for the click under it. It is
	// apart from OnTyping because the two answer different questions — one is about
	// the gateway announcement, which does not care what was pressed, and the other
	// cannot be answered by "is there text now". It names the kind rather than
	// playing anything: this package has no business knowing there is audio.
	OnKeystroke func(Keystroke)

	// Mentions is the autocomplete list, mounted above the reply cards and hidden
	// until the caret sits inside a mention. mentionStart is the marker's byte
	// offset, or -1, and mentionKind what it opened — together only used to notice
	// the caret moving to a *different* mention, so the highlight restarts.
	Mentions     *MentionPicker
	mentionStart int
	mentionKind  MentionKind

	Attachments         []domain.Attachment
	AttachmentContainer *fyne.Container
	Replies             []Reply
	ReplyContainer      *fyne.Container

	// EmojiButton opens the picker. It belongs to the composer rather than the card
	// around it so one thing decides whether it is offered: a button that inserts
	// into a disabled entry has nowhere to put its answer.
	//
	// AttachButton queues a file the same way a drop does, and is offered on the
	// upload permission rather than on the send one — the two are given separately.
	EmojiButton  *IconButton
	AttachButton *IconButton

	// permissions is what the account may do in the open channel, *pushed* by the
	// app rather than looked up — the composer has no channel of its own. Zero, the
	// state it starts and ends in, allows nothing.
	permissions domain.Permission

	deps         Deps
	window       fyne.Window
	shiftPressed bool
	ctrlPressed  bool
}

// NewMessageInput creates a message input wired to deps. The window is what lets
// the composer accept dropped files and take focus back after a mouse
// interaction — Fyne unfocuses on any click that lands on a non-focusable thing,
// which includes picking a mention.
func NewMessageInput(deps Deps, window fyne.Window) *MessageInput {
	m := &MessageInput{
		deps:                deps,
		window:              window,
		mentionStart:        -1,
		AttachmentContainer: container.NewHBox(),
		ReplyContainer:      container.NewVBox(),
	}
	m.Mentions = NewMentionPicker(deps.Images, m.acceptMention)
	m.EmojiButton = NewIconButton(assets.ActionEmojiIcon, m.pickEmoji, nil)
	m.AttachButton = NewIconButton(assets.ActionAddIcon, m.attachFile, nil)
	m.ExtendBaseWidget(m)
	m.MultiLine = true
	m.Wrapping = fyne.TextWrapWord

	// An empty row still reserves its gap above the input bar — see refill.
	m.ReplyContainer.Hide()
	m.AttachmentContainer.Hide()

	return m
}

// pickEmoji opens the picker beside the button and writes back what it answers.
// The controller owns the picker, so this hands it an anchor.
func (m *MessageInput) pickEmoji() {
	m.deps.Actions.OnPickEmoji(m.EmojiButton, func(choice EmojiChoice) {
		m.insert(choice.Token() + " ")
	})
}

// attachFile asks the controller for a file and queues what comes back. The
// permission is checked here as well as in AddAttachment: opening a picker only
// to refuse what it answers is worse than saying so first.
func (m *MessageInput) attachFile() {
	if !m.canUpload() {
		m.refuse(uploadRefused)
		return
	}

	m.deps.Actions.OnAttachFile(func(path string) { m.AddAttachment(path) })
}

// insert writes text at the caret and leaves the caret after it, taking canvas
// focus back off the picker so the next keystroke lands here. Any selection is
// left alone: Fyne exposes no way to read one back, and picking from a pop-up has
// already blurred the entry that held it.
func (m *MessageInput) insert(text string) {
	if text == "" {
		return
	}

	cursor := m.cursorOffset()
	updated := m.Text[:cursor] + text + m.Text[cursor:]

	m.SetText(updated)
	m.CursorRow, m.CursorColumn = cursorPosition(updated, cursor+len(text))
	m.Refresh()

	if m.window != nil {
		m.window.Canvas().Focus(m)
	}
}

// MinSize grows the entry up to ComposerMaxLines as the user types.
func (m *MessageInput) MinSize() fyne.Size { return composerMinSize(&m.Entry) }

// SetPermissions tells the composer what the account may do here. Without
// SendMessage the entry is disabled outright rather than refusing on submit — a
// caret blinking in a box that will not send is worse than no caret, and the
// placeholder is then the only thing left to carry the reason.
//
// Whatever was typed is kept: the channel may hand the permission straight back,
// and clearing it would destroy the user's work over a state the client does not
// control. The emoji button goes with the entry, being a control whose one job is
// to put text into it; the attach button answers the upload permission instead,
// which a channel can withhold on its own.
func (m *MessageInput) SetPermissions(permissions domain.Permission) {
	m.permissions = permissions

	if m.canUpload() {
		m.AttachButton.Show()
	} else {
		m.AttachButton.Hide()
	}

	if permissions.Has(domain.PermissionSendMessage) {
		m.Enable()
		m.EmojiButton.Show()
		return
	}

	m.Disable()
	m.EmojiButton.Hide()
	m.AttachButton.Hide()
}

// refuse reports an input the composer would not take. Silent when nobody is
// listening — every caller of this has already declined to act.
func (m *MessageInput) refuse(reason string) {
	if m.OnRefused != nil {
		m.OnRefused(reason)
	}
}

/* The notice that stands in for the entry */

// ComposerNotice is what the card carries where the account may not write. A
// disabled widget.Entry draws its border in ColorNameDisabled, which the scoped
// theme flattening every other input cannot reach without taking the placeholder
// with it — a stray box inside the card's own outline. Nothing is typed into a
// channel that will not take it, so the entry is hidden and this stands where it
// was, saying the same thing behind a mark.
type ComposerNotice struct {
	widget.BaseWidget

	box     *fyne.Container // the ellipsis box around the reason, re-labelled by Set
	content fyne.CanvasObject
}

var _ fyne.Widget = (*ComposerNotice)(nil)

// NewComposerNotice builds an empty, hidden notice. Set gives it something to
// say. The text is the placeholder's own colour and size: it stands in the same
// place and is read the same way.
func NewComposerNotice() *ComposerNotice {
	w := &ComposerNotice{}
	w.box = NewEllipsisText(newText("", theme.Colors.TimestampText, 0))

	side := theme.Sizes.ComposerNoticeMark
	mark := newScaledIcon(tintedIcon(assets.ForbiddenIcon, theme.Colors.TimestampText), side)
	markBox := container.NewCenter(container.NewGridWrap(fyne.NewSize(side, side), mark))

	row := NewFillRow(2, markBox, HorizontalSpacer(theme.Sizes.ComposerNoticeGap), w.box)

	// The same InnerPadding the entry pays above and below its text, so the card
	// keeps its height when the entry goes.
	pad := fyne.CurrentApp().Settings().Theme().Size(fynetheme.SizeNameInnerPadding)
	w.content = NewInset(row, pad, pad, pad, pad)

	w.Hide()
	w.ExtendBaseWidget(w)

	return w
}

func (w *ComposerNotice) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

// Set says why the channel will not take a message, or hides the notice when
// there is nothing to say. Call on the UI thread.
func (w *ComposerNotice) Set(reason string) {
	if reason == "" {
		w.Hide()
		return
	}

	SetEllipsisText(w.box, reason)
	w.Show()
}

// composerMinSize sizes a growing entry: one line per newline up to
// ComposerMaxLines, plus InnerPadding above and below and nothing else. The input
// border is *not* added on top — entryRenderer.Layout pays for that inset out of
// the text provider's own padding, and counting it twice left dead pixels under
// the caret.
func composerMinSize(e *widget.Entry) fyne.Size {
	size := e.MinSize()
	lines := min(max(strings.Count(e.Text, "\n")+1, 1), int(theme.Sizes.ComposerMaxLines))
	th := e.Theme()
	size.Height = lineHeight(th.Size(fynetheme.SizeNameText))*float32(lines) +
		th.Size(fynetheme.SizeNameInnerPadding)*2

	return size
}

// lineHeights memoises one line's height per text size — MinSize runs on every
// layout pass. UI thread only, hence unsynchronised.
var lineHeights = map[float32]float32{}

func lineHeight(textSize float32) float32 {
	h, ok := lineHeights[textSize]
	if !ok {
		h = fyne.MeasureText("M", textSize, fyne.TextStyle{}).Height
		lineHeights[textSize] = h
	}

	return h
}

// NewComposerButtonSlot bottom-anchors a control against the growing entry — one
// riding the middle would drift away from the line being typed — and lifts it by
// the entry's InnerPadding, so it centres on that last *line* rather than on the
// box, whose padding sits below it.
func NewComposerButtonSlot(obj fyne.CanvasObject) fyne.CanvasObject {
	lift := fynetheme.InnerPadding() + (lineHeight(fynetheme.TextSize())-obj.MinSize().Height)/2

	return container.NewVBox(layout.NewSpacer(), NewInset(obj, 0, max(lift, 0), 0, 0))
}

/* Keyboard */

func (m *MessageInput) FocusGained() {
	m.Entry.FocusGained()
	if m.OnFocusChanged != nil {
		m.OnFocusChanged(true)
	}
}

// FocusLost deliberately leaves the picker open. Fyne unfocuses on the mouse
// *press* and hit-tests again on the release, so closing it here resized the
// composer out from under the click and the first click on anything was spent
// dismissing the picker. Visibility follows the caret instead — a half-typed
// mention is still in the text whether or not the entry is live.
func (m *MessageInput) FocusLost() {
	m.shiftPressed = false
	m.Entry.FocusLost()
	if m.OnFocusChanged != nil {
		m.OnFocusChanged(false)
	}
}

// MouseDown lets the entry place the caret, then re-evaluates the picker against
// where it landed — the caret moves on the press, and widget.Entry.Tapped does no
// positioning at all. Hiding here moves nothing under the pointer: the card hangs
// from the bottom of the dock, so it loses that height off its top edge.
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

// TypedKey sends on Enter, newlines on Shift+Enter, cancels pending
// replies/attachments on Escape — or reports the Escape when there is nothing to
// cancel — edits the last own message on Up in an empty composer, and otherwise
// defers to the entry, refreshing so MinSize recomputes.
//
// Which of Enter and Ctrl+Enter sends is a setting; Shift+Enter is a newline
// either way. An open mention picker gets first refusal on the navigation keys,
// binding the same ones.
func (m *MessageInput) TypedKey(key *fyne.KeyEvent) {
	if m.Mentions.Visible() && m.handleMentionKey(key) {
		return
	}
	defer m.syncMentions()
	defer m.reportTyping()

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
		if m.OnEscape != nil {
			m.OnEscape()
		}
		m.Entry.TypedKey(key)
	case key.Name == fyne.KeyBackspace || key.Name == fyne.KeyDelete:
		m.Entry.TypedKey(key)
		m.reportKeystroke(KeystrokeErase)
		m.Refresh()
	case key.Name != fyne.KeyReturn && key.Name != fyne.KeyEnter:
		m.Entry.TypedKey(key)
	case m.shiftPressed || !m.sends():
		m.TypedRune('\n')
		m.Refresh()
	default:
		m.reportKeystroke(KeystrokeSend)
		if m.OnSubmit != nil {
			m.OnSubmit(m.Text)
		}
		m.Refresh()
	}
}

// sends reports whether the Enter being handled is the sending one. With "Enter
// sends" off it is Ctrl+Enter, which is why the modifier is tracked at all.
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
	m.reportTyping()

	if r == ' ' {
		m.reportKeystroke(KeystrokeSpace)
		return
	}
	m.reportKeystroke(KeystrokeKey)
}

// reportTyping tells the app a keystroke landed and whether anything survived it.
// It rides the typing methods for the same reason syncMentions does: Entry's
// OnChanged misses paths that move the text.
func (m *MessageInput) reportTyping() {
	if m.OnTyping != nil {
		m.OnTyping(m.Text != "")
	}
}

// Keystroke is which kind of key a composer keystroke was — as much as anything
// listening needs to tell one click from another, and no more. Navigation keys
// are not among them: nothing moves in the composer, so nothing should sound.
type Keystroke int

const (
	KeystrokeKey   Keystroke = iota // an ordinary character, a newline included
	KeystrokeSpace                  // the widest key, and the one a typist hears differently
	KeystrokeErase                  // backspace or delete
	KeystrokeSend                   // the Enter that submitted
)

// reportKeystroke names the key that landed. Unlike reportTyping this is called
// per branch rather than deferred over the whole method: the Shift+Enter branch
// delegates to TypedRune, which reports for itself, and a blanket defer would
// make one newline two keystrokes.
func (m *MessageInput) reportKeystroke(kind Keystroke) {
	if m.OnKeystroke != nil {
		m.OnKeystroke(kind)
	}
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
	m.reportTyping()
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

// syncMentions re-evaluates the picker against the caret. Driven from the typing
// methods rather than Entry.OnChanged because the picker also closes when the
// caret merely *moves* out of a mention, which changes no text.
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

// mentionQuery finds the mention the caret sits in: the marker's byte offset,
// what it mentions, and the text typed since. A mention only opens at the start
// of the message or after whitespace, so "foo@bar" in running text summons
// nothing. A heading's "# " opens the channel list for the one keystroke before
// the space — refusing at the start of a line would cost every mention typed there.
func (m *MessageInput) mentionQuery() (start int, kind MentionKind, query string, ok bool) {
	cursor := m.cursorOffset()

	// Bounded by the current word: the walk back stops at the first space. In place
	// rather than over a []rune copy — this runs on every keystroke before any
	// early-out, and a message is not short.
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

// acceptMention swaps the marked query under the caret for a mention token,
// leaving the caret after it and a space ready for the next word. The span is
// re-derived rather than taken from mentionStart: picking with the mouse blurs
// the entry before the tap is delivered, by which time the picker is hidden.
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

// cursorOffset returns the caret as a byte offset into Text, which is what the
// mention helpers want — Fyne tracks a row/column pair, what drawing a caret
// needs. Walked in place: splitting Text into lines per keystroke allocated the
// whole message twice over for one number.
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

	// The column is in runes; ranging a string yields the byte index each starts at.
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
				return m.AddAttachment(path)
			}
		}
	}

	content := fyne.CurrentApp().Clipboard().Content()
	if content != "" {
		if _, err := os.Stat(content); err == nil {
			return m.AddAttachment(content)
		}
	}

	return false
}

// RegisterDropHandler attaches files dropped onto the composer's window. The
// permission is checked once for the whole drop, or a folder's worth of files
// would be a folder's worth of identical notices.
func (m *MessageInput) RegisterDropHandler() {
	m.window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if !m.canUpload() {
			m.refuse(uploadRefused)
			return
		}

		for _, u := range uris {
			if u.Scheme() == "file" {
				m.AddAttachment(u.Path())
			}
		}
	})
}

// AddAttachment queues a file and rebuilds the previews, reporting whether it
// took it — one refusal point for both ways a file arrives, and a refused paste
// falls back to pasting the clipboard as text.
func (m *MessageInput) AddAttachment(path string) bool {
	if !m.canUpload() {
		m.refuse(uploadRefused)
		return false
	}

	m.Attachments = append(m.Attachments, domain.Attachment{Path: path, Name: filepath.Base(path)})
	m.rebuildAttachments()

	return true
}

// canUpload reports whether the open channel takes files at all.
func (m *MessageInput) canUpload() bool {
	return m.permissions.Has(domain.PermissionUploadFiles)
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
	m.refill(m.AttachmentContainer, len(m.Attachments), func(i int) fyne.CanvasObject {
		return m.attachmentCard(m.Attachments[i])
	})
}

// refill replaces a dock row's children in one write — Container.Add refreshes
// the whole row per child — and hides it while it holds nothing, an empty one
// still reserving its gap above the input bar.
func (m *MessageInput) refill(box *fyne.Container, count int, build func(i int) fyne.CanvasObject) {
	box.Objects = make([]fyne.CanvasObject, count)
	for i := range count {
		box.Objects[i] = build(i)
	}

	if count == 0 {
		box.Hide()
	} else {
		box.Show()
	}

	box.Refresh()
	m.Refresh()
}

func (m *MessageInput) attachmentCard(attachment domain.Attachment) fyne.CanvasObject {
	var size int
	if info, err := os.Stat(attachment.Path); err == nil {
		size = int(info.Size())
	}

	path := attachment.Path
	bar := attachmentBar(attachment.Name, size, func() { m.RemoveAttachment(path) })
	card := VBoxNoSpacing(attachmentPreview(path), bar)

	// The preview sits inside the card's padding, so the outline goes on the
	// background rather than over the content the way a message attachment's does.
	background := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	background.CornerRadius = 8
	Outline(background)

	return container.NewPadded(container.NewStack(background, container.NewPadded(card)))
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
	m.refill(m.ReplyContainer, len(m.Replies), func(i int) fyne.CanvasObject {
		return m.buildReplyCard(&m.Replies[i])
	})
}

// buildReplyCard is the slim chip for one pending reply: avatar, author, a
// truncated preview, a mention toggle and a remove button, outlined in the
// author's role colour and falling back to the app accent.
func (m *MessageInput) buildReplyCard(reply *Reply) fyne.CanvasObject {
	author, content, avatarURL, accent := resolveReply(m.deps, reply.ChannelID, reply.ID)
	if author == "" {
		author = "Unknown"
	}
	if accent == nil {
		accent = theme.Colors.ServerSelectedBg
	}

	avatar := circularAvatar(m.deps.Images, avatarURL, fyne.NewSize(replyAvatarSize, replyAvatarSize))

	authorLabel := newBoldText(author, theme.Colors.TextPrimary, replyTextSize)
	contentLabel := newText(content, theme.Colors.TimestampText, replyTextSize)

	// NewCenter vertically centres each element in the card's height; HBoxNoSpacing
	// keeps the horizontal gaps explicit rather than inheriting theme padding.
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

// replyIconButton is the small square button the reply card's mention and close
// controls share. A momentary one (toggle=false) brightens on hover; a toggle
// also takes an accent background and stays bright while active.
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

/* The badge row */

// newDockBadgeSurface puts a pill behind one of the two things hanging over the
// bottom of the message column, sized by its content rather than by the row — a
// surface spanning the row would read as a bar growing out of the card below. It
// accepts no pointer event, so the messages underneath stay hoverable.
func newDockBadgeSurface(content fyne.CanvasObject) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.DockBadgeBg)
	background.CornerRadius = theme.Sizes.DockBadgeRadius
	Outline(background)

	padV, padH := theme.Sizes.DockBadgePaddingV, theme.Sizes.DockBadgePaddingH

	return container.NewStack(background, NewInset(content, padV, padV, padH, padH))
}

/* Slowmode */

// SlowmodeBadge floats over the bottom-right of the message column and reports
// the channel's send cooldown: what it is while the user may send, what is left
// of it while they may not.
//
// It sits *outside* the composer card. Inside, it was furniture the entry had to
// make room for, every relabelling taking width off what was being typed. The
// stopwatch is canvas primitives rather than an icon resource, as the channel
// glyphs are: a stroked shape takes a palette colour directly, so the mark and
// the text beside it change tone as one.
type SlowmodeBadge struct {
	widget.BaseWidget

	// OnResize fires when the chip's width or visibility changes, so its row can be
	// laid out again — pinned to the trailing edge, every relabelling moves where it
	// starts. Fyne re-lays out for a *growing* minimum itself; a shrinking one it
	// leaves reserved.
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
		label: newText("", theme.Colors.SlowmodeText, theme.Sizes.SlowmodeTextSize),
		tone:  theme.Colors.SlowmodeText,
	}
	b.glyph.Objects = []fyne.CanvasObject{stopwatchGlyph(b.tone)}

	// NewCenter lines the glyph up with the text: each takes its own minimum inside
	// a row as tall as the larger.
	chip := HBoxNoSpacing(
		container.NewCenter(b.glyph),
		HorizontalSpacer(theme.Sizes.SlowmodeGap),
		container.NewCenter(b.label),
	)

	// The gap to the card belongs to the chip rather than a spacer in the column,
	// which would hold that room open in every channel with no slowmode. The pill
	// sits inside the gap and outside the row inset, so it covers the text alone.
	b.content = NewInset(newDockBadgeSurface(chip), 0, theme.Sizes.SlowmodeDockGap, 0, theme.Sizes.SlowmodeInsetH)

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

	// Once a second for the length of a cooldown, so a repaint — and the row's
	// relayout with it — is worth a change guard.
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

// stopwatchGlyph draws a stopwatch in col, on the channel glyphs' 20-unit grid so
// the two read as one icon set.
func stopwatchGlyph(col color.Color) fyne.CanvasObject {
	size := theme.Sizes.SlowmodeGlyphSize
	scale := size / 20
	line := glyphLine(col, scale)

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

/* Typing indicator */

// TypingIndicator names who is composing, at the other end of the row the
// slowmode chip is pinned to and under the same rules, pill included: a name over
// a conversation of other names has nothing else to hold it apart.
//
// Avatars are rebuilt outright rather than pooled — who is typing changes every
// few seconds at worst, so there is no stale load to guard against and none of
// the generation machinery MemberRow needs.
type TypingIndicator struct {
	widget.BaseWidget

	// OnResize fires when the line's height or visibility changes, so the dock can
	// be re-hung. Fyne re-lays out for a growing minimum itself; a shrinking one it
	// leaves reserved.
	OnResize func()

	images *cache.ImageCache

	mark    *TypingMark
	faces   *fyne.Container
	label   *canvas.Text
	content *fyne.Container

	shown []string // the avatars currently mounted, to notice when they move
}

// NewTypingIndicator creates the line, hidden and saying nothing.
func NewTypingIndicator(images *cache.ImageCache) *TypingIndicator {
	t := &TypingIndicator{
		images: images,
		mark:   NewTypingMark(theme.Sizes.TypingMarkSize, theme.Colors.TypingMark),
		faces:  HBoxNoSpacing(),
		label:  newText("", theme.Colors.TypingText, theme.Sizes.TypingTextSize),
	}

	t.faces.Hide()

	line := HBoxNoSpacing(
		container.NewCenter(t.mark),
		HorizontalSpacer(theme.Sizes.TypingGap),
		container.NewCenter(t.faces),
		container.NewCenter(t.label),
	)

	// The gap belongs to the line rather than a spacer in the column, as on the
	// chip: a spacer holds the room open in every channel where nobody is typing.
	t.content = NewInset(newDockBadgeSurface(line), 0, theme.Sizes.SlowmodeDockGap, theme.Sizes.TypingInsetH, 0)

	t.Hide()
	t.ExtendBaseWidget(t)

	return t
}

func (t *TypingIndicator) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

// Set draws the line, or hides it when there is nothing to say. avatarURLs may be
// empty; animate runs the mark. Only a change is acted on: this fires on every
// typing event and every lapse, so one steady typist must not re-mount an avatar
// row per keystroke.
func (t *TypingIndicator) Set(text string, avatarURLs []string, animate bool) {
	if text == "" {
		if t.Visible() {
			t.Hide()
			t.mark.SetActive(false, false)
			t.resized()
		}
		return
	}

	moved := !t.Visible()

	if text != t.label.Text {
		t.label.Text = text
		t.label.Refresh()
		moved = true
	}

	if !slices.Equal(avatarURLs, t.shown) {
		t.setFaces(avatarURLs)
		moved = true
	}

	t.Show()
	t.mark.SetActive(true, animate)

	if moved {
		t.resized()
	}
}

// setFaces re-mounts the avatar row. Each face carries its own trailing spacer,
// so an empty row costs nothing — the container is hidden whole.
func (t *TypingIndicator) setFaces(avatarURLs []string) {
	t.shown = slices.Clone(avatarURLs)
	t.faces.Objects = nil

	if len(avatarURLs) == 0 {
		t.faces.Hide()
		t.faces.Refresh()
		return
	}

	side := fyne.NewSize(theme.Sizes.TypingAvatarSize, theme.Sizes.TypingAvatarSize)
	faces := make([]fyne.CanvasObject, 0, len(avatarURLs)*2)
	for _, url := range avatarURLs {
		faces = append(faces, circularAvatar(t.images, url, side), HorizontalSpacer(theme.Sizes.TypingGap))
	}
	t.faces.Objects = faces

	t.faces.Show()
	t.faces.Refresh()
}

// resized tells whoever mounted the line that the room it takes has changed.
func (t *TypingIndicator) resized() {
	if t.OnResize != nil {
		t.OnResize()
	}
}

/* The jump bar */

// What the bar says. The state and the way out of it in one sentence rather than
// pinned to opposite ends: the whole bar is the button, so a separate action at
// the trailing edge only invites the reader to aim at it.
const jumpBarLabel = "Viewing older messages, tap to jump to present"

// JumpBar spans the dock above the composer while the column is showing anything
// but the live tail — deep scrollback, or the window a jump landed in — and takes
// the reader back on a tap.
//
// It is a second bar over the card rather than a pill in the badge row beside it:
// what it reports is a state the whole column is in, and it is the one thing
// hanging over the messages that answers a pointer, which a chip the width of its
// own text would not advertise. It takes the card's radius and its gutter, so the
// two read as one dock rather than as something that landed on it.
type JumpBar struct {
	tapBase

	// OnResize fires when the bar appears or disappears, so the dock can be
	// re-hung. Fyne re-lays out for a growing minimum itself; a shrinking one it
	// leaves reserved.
	OnResize func()

	background *canvas.Rectangle
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*JumpBar)(nil)
	_ desktop.Hoverable = (*JumpBar)(nil)
)

// NewJumpBar builds the bar, hidden. Set is what puts it up.
func NewJumpBar(onTap func()) *JumpBar {
	b := &JumpBar{background: canvas.NewRectangle(theme.Colors.JumpBarBg)}
	b.background.CornerRadius = theme.Sizes.JumpBarRadius
	Outline(b.background)
	b.onTap = onTap

	// The line is drawn in the accent rather than the greys the badges beside it
	// take: those report and this one is pressed, and centred text on a bar wide
	// enough to be furniture needs the tone to say which.
	label := container.NewCenter(newText(jumpBarLabel, theme.Colors.JumpBarAction, theme.Sizes.JumpBarTextSize))

	padV, padH := theme.Sizes.JumpBarPaddingV, theme.Sizes.JumpBarPaddingH
	surface := container.NewStack(b.background, NewInset(label, padV, padV, padH, padH))

	// The gap to the card belongs to the bar rather than to a spacer in the dock,
	// which would hold that room open in every channel sitting at the live tail.
	b.content = NewInset(surface, 0, theme.Sizes.JumpBarDockGap, 0, 0)

	b.Hide()
	b.ExtendBaseWidget(b)

	return b
}

func (b *JumpBar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

// Set puts the bar up or takes it down, telling the dock when that moved. Only a
// change is acted on: the answer is a scroll position, so this is asked on every
// wheel tick and every frame of a pan.
func (b *JumpBar) Set(showing bool) {
	if showing == b.Visible() {
		return
	}

	if showing {
		b.Show()
	} else {
		b.Hide()
	}

	if b.OnResize != nil {
		b.OnResize()
	}
}

func (b *JumpBar) MouseIn(*desktop.MouseEvent) {
	b.background.FillColor = theme.Colors.JumpBarHoverBg
	b.background.Refresh()
}

func (b *JumpBar) MouseOut() {
	b.background.FillColor = theme.Colors.JumpBarBg
	b.background.Refresh()
}

/* The mention picker */

const (
	// mentionMaxRows bounds the picker's height and its per-keystroke work: the
	// surplus becomes a "+N more" hint rather than a scrolling list, which is slower
	// to use than one telling you to keep typing.
	mentionMaxRows = 8

	// mentionNameMaxRunes stops one long display name stretching a row wider than
	// the composer. The rows are packed left, so there is no column to ellipsise
	// against the way a sidebar row has.
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

// markerKind is marker's inverse; the pair is the only place the two characters
// are named.
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

	// Lowercased match keys, folded once at construction: the picker filters the
	// whole set on every keystroke, which is what keeps a 2000-member server cheap.
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

// NewChannelCandidate builds a candidate for a channel, taking the resolved
// channel because that is what the sidebar already holds: one walk builds the
// rows and the candidates alike.
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

// SortCandidates orders a list by display name in place, on the key the candidate
// already carries. A server's members arrive sorted; this is for the ones
// assembled a recipient at a time, which would otherwise shuffle per rebuild.
func SortCandidates(candidates []MentionCandidate) {
	slices.SortFunc(candidates, func(x, y MentionCandidate) int {
		return strings.Compare(x.nameKey, y.nameKey)
	})
}

// rank scores a candidate against an already-lowercased query: 0 for a prefix
// match on name or handle, 1 for a substring, -1 for none. An empty query — the
// bare "@" — matches everyone at 0. The receiver is a pointer only because filter
// calls this twice per candidate per keystroke, and the struct is wide.
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

// MentionPicker is the autocomplete list shown while a mention is typed. It lives
// inside the composer card rather than floating: a Fyne pop-up takes canvas focus
// away from the entry, stopping the typing that drives it. Its rows are pooled —
// mentionMaxRows, built once and re-set — so a keystroke re-labels rather than
// building and discarding a list.
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

	// What the rows currently show, and whether they still reflect it. A caret move
	// inside the same mention then costs nothing.
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

	pooled := make([]fyne.CanvasObject, mentionMaxRows)
	for i := range mentionMaxRows {
		row := newMentionRow(images, func() { p.selectRow(i) }, func() { p.selectRow(i); p.Accept() })
		row.Hide()

		p.rows = append(p.rows, row)
		pooled[i] = row
	}
	rowBox := VBoxNoSpacing(pooled...)

	p.footer = newText("", theme.Colors.MentionHandleText, theme.Sizes.MentionHandleSize)
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

// SetCandidates replaces one of the pools. Call it when the open channel changes,
// its membership does, or the sidebar is rebuilt; the picker snapshots the list
// and never goes to the network itself.
func (p *MentionPicker) SetCandidates(kind MentionKind, candidates []MentionCandidate) {
	if kind == MentionChannel {
		p.channels = candidates
	} else {
		p.users = candidates
	}
	p.fresh = false

	// Rows skip a rebuild for a candidate they already show, and the same person can
	// arrive under a new nickname, colour or avatar.
	for _, row := range p.rows {
		row.invalidate()
	}

	// An open picker outlives the entry's focus, so it can outlive the channel it
	// was opened in: re-run the query rather than offer people from the old list.
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
// the caret — and reports whether anything matched. False means hide the picker.
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
// hits. Two passes beat one pass plus a sort: two walks, a string comparison per
// entry, and nothing allocated — which is what lets it run on every keystroke.
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

// Step moves the highlight by delta, wrapping at both ends. Named Step because a
// fyne.Widget's Move is the one that positions it on the canvas.
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
// The rows no longer reflect their query afterwards, so the next Update refilters
// even if nothing was typed.
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

	// What the row shows and how, so a keystroke leaving it on the same candidate
	// rebuilds nothing — filtering re-sets every row per character and most hold
	// still. Kind is part of the identity: nothing stops the pools sharing an ID.
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
		name:        newBoldText("", theme.Colors.TextPrimary, theme.Sizes.MentionNameSize),
		handle:      newText("", theme.Colors.MentionHandleText, theme.Sizes.MentionHandleSize),
		onHover:     onHover,
	}
	r.lead = container.NewGridWrap(size, r.placeholder)

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

// set re-labels the row for a candidate. Only the avatar can be slow, and it goes
// through the shared cache, so a row passing under a fast typist costs a lookup.
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

	// A channel has no handle: the marker it was found by is already in its glyph.
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

	// Back to the placeholder first: the row may still show the previous face, and
	// this candidate may have no avatar at all.
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
