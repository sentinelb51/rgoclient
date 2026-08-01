package ui

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	// hoverHideDelay debounces the transition between the message body and the
	// floating action buttons so they don't flicker.
	hoverHideDelay = 50 * time.Millisecond

	maxReplyUsernameLength = 16
	maxReplyPreviewLength  = 80
	replyPreviewAvatarSize = 16
	replyPreviewTextSize   = 12
)

// MessageWidget renders a single chat message, revealing quick-action buttons on
// hover and swapping its body for an editor in place.
type MessageWidget struct {
	widget.BaseWidget
	deps    Deps
	message *revoltgo.Message

	content    fyne.CanvasObject
	background *canvas.Rectangle

	// authorText and avatar are retained so a message whose author resolves after
	// the widget is mounted can be updated in place by RefreshAuthor rather than
	// re-rendering the channel. Both are nil for a grouped continuation, which
	// draws neither a name nor an avatar.
	authorText *canvas.Text
	avatar     *Avatar

	// gutterTimestamp is the small left-gutter time a grouped continuation shows
	// in place of the avatar, revealed on hover. nil for a full message.
	gutterTimestamp *canvas.Text

	// bottomSpacer is the bottom margin, kept so SetFollowedByGroup can tighten it
	// when a continuation is appended directly beneath.
	bottomSpacer *canvas.Rectangle

	// daySeparator is the dated divider above the first message of a calendar day,
	// nil for every other message. It lives on the widget rather than as its own
	// list entry, so the mounted window stays one object per message and every
	// path that re-evaluates a message against a new predecessor re-derives it.
	daySeparator fyne.CanvasObject

	// bodySlot holds the rendered body; StartEdit swaps it for an editor and
	// CancelEdit restores body, leaving header, attachments and replies alone.
	bodySlot *fyne.Container
	body     fyne.CanvasObject

	// The hover quick-actions are built lazily on first reveal (ensureActions), so
	// the buttons and their icons are never constructed for messages the pointer
	// does not touch. actionsOverlay is empty until then.
	actionsOverlay *fyne.Container
	actions        *fyne.Container

	editing     bool
	overMessage bool
	overActions bool
	hideTimer   *time.Timer
}

var (
	_ fyne.Widget            = (*MessageWidget)(nil)
	_ desktop.Hoverable      = (*MessageWidget)(nil)
	_ fyne.SecondaryTappable = (*MessageWidget)(nil)
)

// NewMessageWidget builds a message widget. When grouped is set the message is a
// continuation of the previous one from the same author: it omits the avatar and
// name header and shows a small hover-revealed timestamp in the avatar gutter
// instead. Spacing is asymmetric — a head or standalone message carries the full
// gap above it while a continuation carries a tight one, and followedByGroup
// tightens the bottom margin so a head sits flush against the continuations
// beneath without changing the gap between separate groups.
//
// A non-empty dayLabel means this message opens a new calendar day, and the
// named day separator is drawn above it.
func NewMessageWidget(deps Deps, message *revoltgo.Message, dayLabel string, grouped, followedByGroup bool) *MessageWidget {
	w := &MessageWidget{
		deps:       deps,
		message:    message,
		background: canvas.NewRectangle(color.Transparent),
	}

	text := message.Content
	if message.System != nil {
		text = util.FormatSystemMessage(deps.Session, message.System)
	}

	var shortTime, fullTime string
	if t, err := util.Timestamp(message.ID); err == nil {
		shortTime, fullTime = util.ShortTime(t), util.NiceTime(t)
	}

	w.body = newFlushContainer(renderMessageBody(text))
	w.bodySlot = container.NewStack(w.body)

	var leftColumn, body fyne.CanvasObject
	if grouped {
		// Transparent until hover: toggling colour rather than visibility keeps the
		// gutter's width fixed, so the body never shifts when the time appears.
		w.gutterTimestamp = canvas.NewText(shortTime, color.Transparent)
		w.gutterTimestamp.TextSize = theme.Sizes.MessageTimestampSize

		gutter := &columnLayout{
			width:     theme.Sizes.MessageAvatarColumnWidth,
			topOffset: theme.Sizes.MessageTimestampTopOffset,
			collapse:  true,
		}
		leftColumn = container.New(gutter, w.gutterTimestamp)
		body = buildGroupedContent(deps, message, w.bodySlot)
	} else {
		name, nameColor, avatarURL := resolveAuthor(deps, message)
		w.avatar = NewAvatar(deps.Images, avatarURL, func() {
			deps.Actions.OnAvatarTapped(message.Author)
		})
		leftColumn = container.New(&columnLayout{width: theme.Sizes.MessageAvatarColumnWidth}, w.avatar)

		w.authorText = canvas.NewText(name, nameColor)
		w.authorText.TextStyle = fyne.TextStyle{Bold: true}
		body = buildMessageContent(deps, message, w.authorText, fullTime, w.bodySlot)
	}

	paddedBody := container.NewBorder(nil, nil, HorizontalSpacer(theme.Sizes.MessageContentPadding), nil, body)
	row := container.NewBorder(nil, nil, leftColumn, nil, paddedBody)

	hPad := theme.Sizes.MessageHorizontalPadding
	w.bottomSpacer = canvas.NewRectangle(color.Transparent)
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, verticalPad(followedByGroup)))
	inner := container.NewBorder(
		VerticalSpacer(verticalPad(grouped)), w.bottomSpacer,
		HorizontalSpacer(hPad), HorizontalSpacer(hPad),
		row,
	)

	w.actionsOverlay = container.New(&overlayLayout{yOffset: -16, rightOffset: 6})
	messageRow := container.NewStack(inner, w.actionsOverlay)

	if dayLabel != "" {
		w.daySeparator = newDaySeparator(dayLabel)
	}

	w.content = messageRow
	if !grouped && len(message.Replies) > 0 {
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

func (w *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	row := container.NewStack(w.background, w.content)
	if w.daySeparator == nil {
		return widget.NewSimpleRenderer(row)
	}

	// The separator sits above the hover-highlight stack, not inside it, so
	// hovering the message doesn't light up the divider as part of the row.
	return widget.NewSimpleRenderer(VBoxNoSpacing(w.daySeparator, row))
}

// Message returns the message this widget renders.
func (w *MessageWidget) Message() *revoltgo.Message { return w.message }

// Author returns the message author's user ID.
func (w *MessageWidget) Author() string { return w.message.Author }

// SetFollowedByGroup tightens (or restores) the bottom margin when a same-author
// continuation is appended directly beneath this message after it was mounted.
func (w *MessageWidget) SetFollowedByGroup(followed bool) {
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, verticalPad(followed)))
	w.bottomSpacer.Refresh()
	w.Refresh()
}

// RefreshAuthor re-resolves the author's name, role colour, and avatar and
// applies them in place, for when a previously-unknown author is fetched or a
// member updates after the widget was mounted. A grouped continuation shows
// neither name nor avatar, so there is nothing to update.
func (w *MessageWidget) RefreshAuthor() {
	if w.authorText == nil {
		return
	}

	name, nameColor, avatarURL := resolveAuthor(w.deps, w.message)
	if w.authorText.Text != name || w.authorText.Color != nameColor {
		w.authorText.Text = name
		w.authorText.Color = nameColor
		w.authorText.Refresh()
	}

	w.avatar.SetSource(w.deps.Images, avatarURL)
}

/* Permissions */

// isOwnMessage reports whether the message was authored by the logged-in user.
func (w *MessageWidget) isOwnMessage() bool {
	if w.deps.Session == nil {
		return false
	}

	self := w.deps.Session.State.Self()
	return self != nil && self.ID == w.message.Author
}

// canEdit reports whether the edit action should be offered: only your own
// regular messages, since system messages have no editable content.
func (w *MessageWidget) canEdit() bool {
	return w.message.System == nil && w.isOwnMessage()
}

// canDelete reports whether the delete action should be offered: your own
// message, or any message in a channel where you hold ManageMessages.
func (w *MessageWidget) canDelete() bool {
	if w.isOwnMessage() {
		return true
	}
	if w.deps.Session == nil {
		return false
	}

	state := w.deps.Session.State
	self, channel := state.Self(), state.Channel(w.message.Channel)
	if self == nil || channel == nil {
		return false
	}

	perms, err := state.ChannelPermissions(self, channel)
	return err == nil && perms&revoltgo.PermissionManageMessages != 0
}

/* Quick actions and context menu */

// buildActions creates the hidden, rounded group of quick-action buttons. The
// set is dynamic: reply is always offered, edit only on your own non-system
// message, delete on your own or where you can manage messages.
func (w *MessageWidget) buildActions() *fyne.Container {
	onHover := func(hovering bool) {
		w.overActions = hovering
		w.updateHover()
	}
	act := w.deps.Actions

	buttons := []fyne.CanvasObject{
		NewIconButton(fynetheme.MailReplyIcon(), func() { act.OnReply(w.message) }, onHover),
	}
	if w.canEdit() {
		buttons = append(buttons, NewIconButton(fynetheme.DocumentCreateIcon(), func() { act.OnEdit(w.message) }, onHover))
	}
	if w.canDelete() {
		buttons = append(buttons, NewIconButton(fynetheme.DeleteIcon(), func() { act.OnDelete(w.message) }, onHover))
	}

	// The overflow button is always last and opens the full context menu — the
	// same one right-clicking the message shows — beneath itself.
	more := NewIconButton(fynetheme.MoreVerticalIcon(), nil, onHover)
	more.onTap = func() { ShowContextMenu(more, w.menuItems(), AnchorBelow(more)) }
	buttons = append(buttons, more)

	group := container.NewStack(roundedPanel(), HBoxNoSpacing(buttons...))
	group.Hide()

	return group
}

// menuItems builds the context-menu entries, mirroring the hover quick-actions
// (gated the same way) plus copy helpers. Used by both the overflow button and
// the right-click handler.
func (w *MessageWidget) menuItems() []*fyne.MenuItem {
	act := w.deps.Actions

	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Reply", fynetheme.MailReplyIcon(), func() { act.OnReply(w.message) }),
	}
	if w.canEdit() {
		items = append(items, fyne.NewMenuItemWithIcon("Edit", fynetheme.DocumentCreateIcon(), func() { act.OnEdit(w.message) }))
	}

	items = append(items, fyne.NewMenuItemSeparator())
	if w.message.Content != "" {
		items = append(items, fyne.NewMenuItemWithIcon("Copy message", fynetheme.ContentCopyIcon(), func() {
			copyToClipboard(w.message.Content)
		}))
	}
	items = append(items,
		fyne.NewMenuItemWithIcon("Copy message ID", fynetheme.ContentCopyIcon(), func() {
			copyToClipboard(w.message.ID)
		}),
		fyne.NewMenuItemWithIcon("Copy author ID", fynetheme.AccountIcon(), func() {
			copyToClipboard(w.message.Author)
		}),
	)

	if w.canDelete() {
		items = append(items, fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon("Delete", fynetheme.DeleteIcon(), func() { act.OnDelete(w.message) }))
	}

	return items
}

// TappedSecondary opens the message context menu at the cursor on right-click.
func (w *MessageWidget) TappedSecondary(e *fyne.PointEvent) {
	ShowContextMenu(w, w.menuItems(), e.AbsolutePosition)
}

/* In-place editing */

// StartEdit swaps the message body for an in-place editor, with save/cancel
// buttons floating where the hover quick-actions normally appear. Enter without
// shift, or the save button, submits through onSave; Escape or cancel calls
// onCancel. Saving unchanged or emptied content counts as a cancel. Returns the
// entry for the caller to focus, or nil when the message isn't editable or is
// already being edited.
func (w *MessageWidget) StartEdit(onSave func(newContent string), onCancel func()) *EditEntry {
	if w.editing || !w.canEdit() {
		return nil
	}
	w.editing = true
	w.stopHideTimer()

	entry := NewEditEntry(w.message.Content)
	cancel := func() {
		w.CancelEdit()
		if onCancel != nil {
			onCancel()
		}
	}
	save := func() {
		text := entry.Text
		if strings.TrimSpace(text) == "" || text == w.message.Content {
			cancel()
			return
		}
		w.CancelEdit()
		if onSave != nil {
			onSave(text)
		}
	}
	entry.OnSave, entry.OnCancel = save, cancel

	hint := canvas.NewText("esc to cancel  •  enter to save", theme.Colors.TimestampText)
	hint.TextSize = theme.Sizes.MessageTimestampSize
	w.bodySlot.Objects = []fyne.CanvasObject{container.NewVBox(WithCaret(entry), hint)}
	w.bodySlot.Refresh()

	// The save/cancel pair replaces the hover quick-actions for the whole edit.
	buttons := container.NewStack(roundedPanel(), HBoxNoSpacing(
		NewIconButton(fynetheme.DocumentSaveIcon(), save, nil),
		NewIconButton(fynetheme.CancelIcon(), cancel, nil),
	))
	w.actionsOverlay.Objects = []fyne.CanvasObject{buttons}
	w.actionsOverlay.Refresh()

	w.setHighlighted(true)
	return entry
}

// CancelEdit restores the rendered body and the hover quick-actions without
// invoking the edit callbacks. Safe to call when no edit is active.
func (w *MessageWidget) CancelEdit() {
	if !w.editing {
		return
	}
	w.editing = false

	w.bodySlot.Objects = []fyne.CanvasObject{w.body}
	w.bodySlot.Refresh()

	w.actionsOverlay.Objects = nil
	if w.actions != nil {
		w.actions.Hide()
		w.actionsOverlay.Objects = []fyne.CanvasObject{w.actions}
	}
	w.actionsOverlay.Refresh()

	w.setHighlighted(false)
	w.updateHover() // restore the hover state if the pointer is still over the row
}

/* Hover */

func (w *MessageWidget) MouseIn(*desktop.MouseEvent) {
	w.overMessage = true
	w.updateHover()
}

func (w *MessageWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *MessageWidget) MouseOut() {
	w.overMessage = false
	w.updateHover()
}

// updateHover shows the action buttons while the pointer is over the message or
// the buttons, hiding them after a grace period otherwise. Suspended while
// editing, which paints its own highlight and overlay buttons.
func (w *MessageWidget) updateHover() {
	if w.editing {
		return
	}

	if w.overMessage || w.overActions {
		w.stopHideTimer()
		w.ensureActions()
		w.setHighlighted(true)
		w.actions.Show()
		w.setGutterShown(true)
		return
	}

	if w.hideTimer != nil {
		return
	}
	w.hideTimer = time.AfterFunc(hoverHideDelay, func() {
		DoOnUI(func() {
			if w.overMessage || w.overActions || w.editing {
				return
			}
			w.setHighlighted(false)
			if w.actions != nil {
				w.actions.Hide()
			}
			w.setGutterShown(false)
			w.hideTimer = nil
		})
	})
}

func (w *MessageWidget) stopHideTimer() {
	if w.hideTimer != nil {
		w.hideTimer.Stop()
		w.hideTimer = nil
	}
}

// ensureActions builds the hover quick-action buttons on first reveal, mounting
// them into the until-now empty overlay. Later reveals reuse them.
func (w *MessageWidget) ensureActions() {
	if w.actions != nil {
		return
	}

	w.actions = w.buildActions()
	w.actionsOverlay.Objects = []fyne.CanvasObject{w.actions}
	w.actionsOverlay.Refresh()
}

// setHighlighted paints (or clears) the row's hover background.
func (w *MessageWidget) setHighlighted(on bool) {
	fill := color.Color(color.Transparent)
	if on {
		fill = theme.Colors.MessageHoverBackground
	}

	w.background.FillColor = fill
	w.background.Refresh()
}

// setGutterShown reveals or hides a grouped continuation's gutter timestamp by
// toggling its colour, keeping the width fixed so the body never shifts. A no-op
// for full messages, which have no gutter timestamp.
func (w *MessageWidget) setGutterShown(shown bool) {
	if w.gutterTimestamp == nil {
		return
	}

	if shown {
		w.gutterTimestamp.Color = theme.Colors.TimestampText
	} else {
		w.gutterTimestamp.Color = color.Transparent
	}
	w.gutterTimestamp.Refresh()
}

/* Content assembly */

// resolveAuthor resolves the display name, role colour, and avatar URL for a
// message's author. Shared by construction and RefreshAuthor, so a lazily
// fetched author renders identically either way.
func resolveAuthor(deps Deps, message *revoltgo.Message) (name string, nameColor color.Color, avatarURL string) {
	author := util.MessageAuthor(deps.Session, message)

	nameColor = theme.Colors.TextPrimary
	if author.Color != nil {
		nameColor = author.Color
	}

	return author.Name, nameColor, author.AvatarURL
}

// verticalPad returns a message's top or bottom margin: tight when it abuts a
// same-author continuation, the full gap otherwise.
func verticalPad(tight bool) float32 {
	if tight {
		return theme.Sizes.MessageGroupedVerticalPadding
	}

	return theme.Sizes.MessageVerticalPadding
}

// buildMessageContent assembles the author header plus any attachments.
func buildMessageContent(deps Deps, message *revoltgo.Message, author *canvas.Text, timestamp string, body fyne.CanvasObject) fyne.CanvasObject {
	header := buildMessageHeader(author, timestamp, body)
	if len(message.Attachments) == 0 {
		return header
	}

	return container.NewVBox(header, buildAttachments(deps, message.Attachments))
}

// buildGroupedContent renders a grouped continuation: just the body and any
// attachments, with no author/timestamp header.
func buildGroupedContent(deps Deps, message *revoltgo.Message, body fyne.CanvasObject) fyne.CanvasObject {
	if len(message.Attachments) == 0 {
		return body
	}

	return container.NewVBox(body, buildAttachments(deps, message.Attachments))
}

// buildMessageHeader renders the author line — the bold name in its role colour
// followed by a baseline-aligned timestamp — above the message text. Keeping the
// timestamp inline on the name line aligns it with the username and stops long
// body text running under it.
func buildMessageHeader(author *canvas.Text, timestamp string, body fyne.CanvasObject) fyne.CanvasObject {
	ts := canvas.NewText(timestamp, theme.Colors.TimestampText)
	ts.TextSize = theme.Sizes.MessageTimestampSize

	// Drop the smaller timestamp so its baseline lines up with the bold name.
	tsAligned := VBoxNoSpacing(VerticalSpacer(theme.Sizes.MessageTimestampTopOffset), ts)
	nameLine := container.NewHBox(author, HorizontalSpacer(theme.Sizes.MessageContentPadding), tsAligned)

	return VBoxNoSpacing(nameLine, body)
}

/* Day separator */

// newDaySeparator builds the divider announcing a new day of messages: the day's
// name at the left, a hairline running out to the right edge, inset to the same
// horizontal padding as a message row.
func newDaySeparator(label string) fyne.CanvasObject {
	text := canvas.NewText(label, theme.Colors.DaySeparatorText)
	text.TextSize = theme.Sizes.DaySeparatorTextSize
	text.TextStyle = fyne.TextStyle{Bold: true}

	rule := canvas.NewRectangle(theme.Colors.DaySeparatorLine)
	rule.SetMinSize(fyne.NewSize(0, theme.Sizes.DaySeparatorThickness))

	row := container.New(&daySeparatorLayout{gap: theme.Sizes.DaySeparatorGap}, text, rule)
	hPad := theme.Sizes.MessageHorizontalPadding

	return container.NewBorder(
		VerticalSpacer(theme.Sizes.DaySeparatorTopPadding),
		VerticalSpacer(theme.Sizes.DaySeparatorBottomPadding),
		HorizontalSpacer(hPad), HorizontalSpacer(hPad),
		row,
	)
}

// daySeparatorLayout lays out the label and the rule trailing it: the label keeps
// its minimum size, the rule takes the leftover width, and both are vertically
// centred so the hairline meets the middle of the text. It expects exactly two
// children, label first.
type daySeparatorLayout struct{ gap float32 }

func (l *daySeparatorLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}
	label, rule := objects[0], objects[1]

	lm := label.MinSize()
	label.Resize(lm)
	label.Move(fyne.NewPos(0, (size.Height-lm.Height)/2))

	x := lm.Width + l.gap
	height := rule.MinSize().Height
	rule.Resize(fyne.NewSize(max(size.Width-x, 0), height))
	rule.Move(fyne.NewPos(x, (size.Height-height)/2))
}

func (l *daySeparatorLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, child := range objects {
		m := child.MinSize()
		w += m.Width
		h = max(h, m.Height)
	}

	return fyne.NewSize(w+l.gap, h)
}

/* Reply previews */

// buildReplyPreview renders the small quoted line shown above a message that
// replies to another.
func buildReplyPreview(deps Deps, channelID, messageID string) fyne.CanvasObject {
	author, content, avatarURL, _ := resolveReply(deps, channelID, messageID)

	size := fyne.NewSize(replyPreviewAvatarSize, replyPreviewAvatarSize)
	avatar := circularAvatar(deps.Images, avatarURL, size)

	authorLabel := canvas.NewText(author, theme.Colors.TextPrimary)
	authorLabel.TextStyle.Bold = true
	authorLabel.TextSize = replyPreviewTextSize

	contentLabel := canvas.NewText(content, theme.Colors.TimestampText)
	contentLabel.TextSize = replyPreviewTextSize

	row := HBoxNoSpacing(
		container.NewCenter(avatar),
		HorizontalSpacer(8),
		container.NewCenter(authorLabel),
		HorizontalSpacer(5),
		container.NewCenter(contentLabel),
	)
	padded := container.NewBorder(VerticalSpacer(3), VerticalSpacer(3), HorizontalSpacer(3), HorizontalSpacer(3), row)

	// Indent to the message content column so the quoted line sits directly above
	// the body rather than under the avatar gutter.
	// TODO: navigate to the referenced message on tap.
	indent := theme.Sizes.MessageHorizontalPadding + theme.Sizes.MessageAvatarColumnWidth + theme.Sizes.MessageContentPadding

	return container.NewHBox(HorizontalSpacer(indent), NewTappableContainer(padded, func() {}))
}

// resolveReply looks up a referenced message and returns its author, truncated
// content, avatar URL, and role colour (nil when none). A missing reference
// yields a placeholder.
func resolveReply(deps Deps, channelID, messageID string) (author, content, avatarURL string, accent color.Color) {
	message := deps.Actions.ResolveMessage(channelID, messageID)
	if message == nil {
		return "", "Unknown message reference", "", nil
	}

	a := util.MessageAuthor(deps.Session, message)
	return util.Truncate(a.Name, maxReplyUsernameLength),
		util.Truncate(message.Content, maxReplyPreviewLength),
		a.AvatarURL, a.Color
}

/* In-place editor */

var _ desktop.Keyable = (*EditEntry)(nil)

// EditEntry is the multi-line entry used for in-place message editing: Enter
// saves, Shift+Enter inserts a newline, Escape cancels. It grows with its
// content like the main composer.
type EditEntry struct {
	widget.Entry
	OnSave   func()
	OnCancel func()

	shiftPressed bool
	cursorPlaced bool
}

// NewEditEntry creates an edit entry pre-filled with the message's content, the
// cursor placed at the end on the first layout.
func NewEditEntry(content string) *EditEntry {
	e := &EditEntry{}
	e.ExtendBaseWidget(e)
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.SetText(content)

	return e
}

// MinSize grows the entry up to maxInputLines as the user types, matching the
// main composer.
func (e *EditEntry) MinSize() fyne.Size { return composerMinSize(&e.Entry) }

// Resize places the caret at the end of the text the first time the entry gets a
// real size. Setting it in the constructor positions it against zero-width
// word-wrapped row bounds (one rune per visual row), which clamps it a character
// in from the start; deferring to the first real layout makes the entry recompute
// against correct bounds.
func (e *EditEntry) Resize(size fyne.Size) {
	e.Entry.Resize(size)
	if e.cursorPlaced || size.Width <= 0 || size.Height <= 0 {
		return
	}

	e.cursorPlaced = true
	content := e.Text
	e.CursorRow = strings.Count(content, "\n")
	e.CursorColumn = len([]rune(content[strings.LastIndexByte(content, '\n')+1:]))
	e.Refresh()
}

func (e *EditEntry) FocusLost() {
	e.shiftPressed = false
	e.Entry.FocusLost()
}

func (e *EditEntry) KeyDown(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.shiftPressed = true
	}
}

func (e *EditEntry) KeyUp(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.shiftPressed = false
	}
}

// TypedKey saves on Enter, inserts a newline on Shift+Enter, cancels on Escape,
// and otherwise defers to the embedded entry, refreshing so MinSize recomputes.
func (e *EditEntry) TypedKey(key *fyne.KeyEvent) {
	switch {
	case key.Name == fyne.KeyEscape:
		if e.OnCancel != nil {
			e.OnCancel()
		}
	case key.Name != fyne.KeyReturn && key.Name != fyne.KeyEnter:
		e.Entry.TypedKey(key)
		e.Refresh()
	case e.shiftPressed:
		e.TypedRune('\n')
	default:
		if e.OnSave != nil {
			e.OnSave()
		}
	}
}

func (e *EditEntry) TypedRune(r rune) {
	e.Entry.TypedRune(r)
	e.Refresh()
}
