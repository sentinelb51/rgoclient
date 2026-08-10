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

	"RGOClient/assets"
	"RGOClient/internal/domain"
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
	message *domain.Message

	content    fyne.CanvasObject
	background *canvas.Rectangle

	// authorText and avatar are retained so a message whose author resolves after
	// the widget is mounted can be updated in place by RefreshAuthor rather than
	// re-rendering the channel. Both are nil for a grouped continuation, which
	// draws neither a name nor an avatar.
	authorText *AccentText
	avatar     *Avatar

	// gutterTimestamp is the small left-gutter time a grouped continuation shows
	// in place of the avatar, revealed on hover. nil for a full message.
	gutterTimestamp *canvas.Text

	// systemName is the person a system message is about, drawn as a mention, and
	// systemText the rest of the sentence; systemLine is the row they share with
	// the time beside them. All three are kept so a target resolving after the
	// widget is mounted can be written in place, which moves what follows it.
	// systemName is nil for an event about the channel rather than about somebody,
	// and all three are nil for a message somebody wrote.
	systemName *mentionText
	systemText *canvas.Text
	systemLine *fyne.Container

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

	// mentioned marks a message that names the logged-in account, which is what
	// warms the row's background. Decided once at construction: it is a fact about
	// the message, and every path that re-evaluates one rebuilds the widget.
	mentioned bool

	editing     bool
	emptyBody   bool // the message says nothing, so the slot stays hidden outside an edit
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
func NewMessageWidget(deps Deps, message *domain.Message, dayLabel string, grouped, followedByGroup bool) *MessageWidget {
	w := &MessageWidget{
		deps:       deps,
		message:    message,
		mentioned:  message.MentionsUser(deps.Store.SelfID()),
		background: canvas.NewRectangle(color.Transparent),
	}
	w.background.FillColor = w.fill(false)

	var shortTime, fullTime string
	if t, err := util.Timestamp(message.ID); err == nil {
		shortTime, fullTime = util.ShortTime(t), util.NiceTime(t)
	}

	// A system message is not a message anybody wrote: no avatar, no name, nothing
	// to edit and nothing hanging beneath it. It takes a line of its own and shares
	// only the margins, the hover and the day separator with what people say.
	var content fyne.CanvasObject
	if message.System != nil {
		content = w.buildSystemLine(shortTime)
	} else {
		content = w.buildAuthoredContent(grouped, shortTime, fullTime)
	}

	hPad := theme.Sizes.MessageHorizontalPadding
	w.bottomSpacer = canvas.NewRectangle(color.Transparent)
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, w.verticalPad(followedByGroup)))
	inner := container.NewBorder(
		VerticalSpacer(w.verticalPad(grouped)), w.bottomSpacer,
		HorizontalSpacer(hPad), HorizontalSpacer(hPad),
		content,
	)

	w.actionsOverlay = container.New(&overlayLayout{yOffset: -16, rightOffset: 6})
	w.content = container.NewStack(inner, w.actionsOverlay)

	if dayLabel != "" {
		w.daySeparator = newDaySeparator(dayLabel)
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
func (w *MessageWidget) Message() *domain.Message { return w.message }

// Author returns the user this row names: the message's author, or — for a system
// event — whoever it is about, since that is the name the line has to resolve and
// the one a lazy fetch has to bring back.
func (w *MessageWidget) Author() string {
	if w.message.System != nil {
		return w.message.System.Target
	}

	return w.message.AuthorID
}

// SetFollowedByGroup tightens (or restores) the bottom margin when a same-author
// continuation is appended directly beneath this message after it was mounted.
func (w *MessageWidget) SetFollowedByGroup(followed bool) {
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, w.verticalPad(followed)))
	w.bottomSpacer.Refresh()
	w.Refresh()
}

// RefreshAuthor re-resolves the author's name, role colour, and avatar and
// applies them in place, for when a previously-unknown author is fetched or a
// member updates after the widget was mounted. A grouped continuation shows
// neither name nor avatar, so there is nothing to update.
//
// A system line names its target in the middle of a sentence, so re-resolving it
// changes the sentence's width — hence the relayout, which the row beside a name
// of fixed extent doesn't need.
func (w *MessageWidget) RefreshAuthor() {
	if w.systemText != nil {
		name, rest := w.deps.Store.SystemTextParts(w.message.System)
		if w.systemName != nil {
			w.systemName.SetText(name)
		}
		w.systemText.Text = rest
		w.systemText.Refresh()
		Relayout(w.systemLine)

		return
	}

	if w.authorText == nil {
		return
	}

	name, nameColor, avatarURL := resolveAuthor(w.deps, w.message)
	w.authorText.Set(name, nameColor)
	w.avatar.SetSource(w.deps.Images, avatarURL)
}

/* Permissions */

// isOwnMessage reports whether the message was authored by the logged-in user.
func (w *MessageWidget) isOwnMessage() bool {
	self := w.deps.Store.SelfID()

	return self != "" && self == w.message.AuthorID
}

// canEdit reports whether the edit action should be offered: only your own
// regular messages, since system messages have no editable content.
func (w *MessageWidget) canEdit() bool {
	return w.message.System == nil && w.isOwnMessage()
}

// canReply reports whether the reply action should be offered. A system event is
// the channel narrating itself and nobody is waiting to be answered, so quoting
// one back at the channel is offered nowhere — and neither is a reply the channel
// would not let you send.
func (w *MessageWidget) canReply(permissions domain.Permission) bool {
	return w.message.System == nil && permissions.Has(domain.PermissionSendMessage)
}

// canDelete reports whether the delete action should be offered: your own
// message, or any message in a channel where you hold ManageMessages.
func (w *MessageWidget) canDelete(permissions domain.Permission) bool {
	return w.isOwnMessage() || permissions.Has(domain.PermissionManageMessages)
}

// permissions is what the account may do in this message's channel. It is asked
// lazily, on the first hover or right-click, so a mounted page costs no
// permission checks at all — and asked once per menu, since one bitfield answers
// every question the menu has.
func (w *MessageWidget) permissions() domain.Permission {
	return w.deps.Store.Permissions(w.message.ChannelID)
}

/* Quick actions and context menu */

// actionMark tints one of the client's action marks in the neutral colour, which
// is what an action that only takes you somewhere wears. The two that commit
// something — saving an edit, deleting a message — name their own colour at the
// call site, since that colour is the warning.
func actionMark(res fyne.Resource) fyne.Resource {
	return tintedIcon(res, theme.Colors.SwiftActionIcon)
}

// buildActions creates the hidden, rounded group of quick-action buttons. The
// set is dynamic: reply is always offered, edit only on your own non-system
// message, delete on your own or where you can manage messages.
func (w *MessageWidget) buildActions() *fyne.Container {
	onHover := func(hovering bool) {
		w.overActions = hovering
		w.updateHover()
	}
	act := w.deps.Actions
	permissions := w.permissions()

	var buttons []fyne.CanvasObject
	if w.canReply(permissions) {
		buttons = append(buttons, NewIconButton(actionMark(assets.ActionReplyIcon), func() { act.OnReply(w.message) }, onHover))
	}
	if w.canEdit() {
		buttons = append(buttons, NewIconButton(actionMark(assets.ActionEditIcon), func() { act.OnEdit(w.message) }, onHover))
	}
	if w.canDelete(permissions) {
		buttons = append(buttons, NewIconButton(tintedIcon(assets.ActionDeleteIcon, theme.Colors.SwiftActionDanger), func() { act.OnDelete(w.message) }, onHover))
	}

	// The overflow button is always last and opens the full context menu — the
	// same one right-clicking the message shows — beneath itself.
	more := NewIconButton(actionMark(assets.ActionMoreIcon), nil, onHover)
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
	permissions := w.permissions()

	var items []*fyne.MenuItem
	if w.canReply(permissions) {
		items = append(items, fyne.NewMenuItemWithIcon("Reply", actionMark(assets.ActionReplyIcon), func() { act.OnReply(w.message) }))
	}
	if w.canEdit() {
		items = append(items, fyne.NewMenuItemWithIcon("Edit", actionMark(assets.ActionEditIcon), func() { act.OnEdit(w.message) }))
	}

	if len(items) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	if w.message.Content != "" {
		items = append(items, fyne.NewMenuItemWithIcon("Copy message", actionMark(assets.ActionCopyIcon), func() {
			CopyToClipboard(w.message.Content)
		}))
	}
	items = append(items,
		fyne.NewMenuItemWithIcon("Copy message ID", actionMark(assets.ActionCopyIcon), func() {
			CopyToClipboard(w.message.ID)
		}),
		fyne.NewMenuItemWithIcon("Copy author ID", actionMark(assets.AccountIcon), func() {
			CopyToClipboard(w.message.AuthorID)
		}),
	)

	if w.canDelete(permissions) {
		items = append(items, fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon("Delete", tintedIcon(assets.ActionDeleteIcon, theme.Colors.SwiftActionDanger), func() { act.OnDelete(w.message) }))
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
	w.bodySlot.Show()
	w.bodySlot.Refresh()

	// The save/cancel pair replaces the hover quick-actions for the whole edit.
	buttons := container.NewStack(roundedPanel(), HBoxNoSpacing(
		NewIconButton(tintedIcon(assets.ActionSaveIcon, theme.Colors.SwiftActionConfirm), save, nil),
		NewIconButton(actionMark(assets.ActionCancelIcon), cancel, nil),
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
	if w.emptyBody {
		w.bodySlot.Hide()
	}
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
			// Cleared first: a timer that fires while the row is being edited used
			// to return with the field still set, and the guard above then refused
			// to arm another one — leaving that message's actions up for good.
			w.hideTimer = nil
			if w.overMessage || w.overActions || w.editing {
				return
			}

			w.setHighlighted(false)
			if w.actions != nil {
				w.actions.Hide()
			}
			w.setGutterShown(false)
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
	w.background.FillColor = w.fill(on)
	w.background.Refresh()
}

// fill is the row's background at rest and under the pointer. A message that
// names the account keeps its warm wash either way — it is what tells the reader
// they were addressed, and it has to survive being read past — so hovering one
// lifts that colour rather than replacing it with the ordinary hover.
func (w *MessageWidget) fill(hovered bool) color.Color {
	switch {
	case w.mentioned && hovered:
		return theme.Colors.MessageMentionHoverBackground
	case w.mentioned:
		return theme.Colors.MessageMentionBackground
	case hovered:
		return theme.Colors.MessageHoverBackground
	}

	return color.Transparent
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

// buildAuthoredContent assembles a message somebody wrote: the avatar gutter — or,
// for a grouped continuation, the hover timestamp standing in it — the header, the
// body slot, and any reply previews above them.
func (w *MessageWidget) buildAuthoredContent(grouped bool, shortTime, fullTime string) fyne.CanvasObject {
	deps, message := w.deps, w.message

	// Every interactive piece of the row takes the same context menu, so the
	// pointer never lands somewhere that swallows a right-click silently.
	w.body = newFlushContainer(renderMessageBody(deps, message.Content, w.TappedSecondary))
	w.bodySlot = container.NewStack(w.body)

	// A message that says nothing — a bot's embed, an attachment on its own —
	// still renders an empty body one text line tall, which draws as a gap above
	// whatever it does carry. Hiding the slot is what removes it: the layouts
	// around it skip hidden children entirely, so no spacing is charged for it
	// either. StartEdit shows it again, since an editor needs somewhere to sit.
	w.emptyBody = message.Content == ""
	if w.emptyBody {
		w.bodySlot.Hide()
	}

	var leftColumn, body fyne.CanvasObject
	if grouped {
		// Transparent until hover: toggling colour rather than visibility keeps the
		// gutter's width fixed, so the body never shifts when the time appears.
		w.gutterTimestamp = canvas.NewText(shortTime, color.Transparent)
		w.gutterTimestamp.TextSize = theme.Sizes.MessageTimestampSize

		gutter := &columnLayout{
			width:     theme.Sizes.MessageAvatarColumnWidth,
			topOffset: gutterTimestampTopOffset(),
			collapse:  true,
		}
		leftColumn = container.New(gutter, w.gutterTimestamp)
		body = buildGroupedContent(deps, message, w.bodySlot, w.TappedSecondary)
	} else {
		name, nameColor, avatarURL := resolveAuthor(deps, message)
		w.avatar = NewAvatar(deps.Images, avatarURL, func() {
			deps.Actions.OnUserTapped(message.AuthorID, w.avatar)
		})
		w.avatar.onSecondaryTap = w.TappedSecondary
		leftColumn = container.New(&columnLayout{
			width:     theme.Sizes.MessageAvatarColumnWidth,
			topOffset: avatarTopOffset(),
		}, w.avatar)

		w.authorText = NewAccentText(name, nameColor, 0, fyne.TextStyle{Bold: true})
		body = buildMessageContent(deps, message, w.authorText, fullTime, w.bodySlot, w.TappedSecondary)
	}

	// One row rather than a Border inside a Border: each of those inserts theme
	// padding between its edges and its centre, so the gap after the avatar gutter
	// was three times MessageContentPadding with nothing saying so.
	row := NewFillRow(2, leftColumn, HorizontalSpacer(theme.Sizes.MessageContentPadding), body)

	// Replies belong inside the row's margins, above the message they answer, so
	// carrying one leaves the avatar and the name exactly where a message without
	// one puts them.
	if grouped || len(message.Replies) == 0 {
		return row
	}

	return VBoxNoSpacing(buildReplyBlock(deps, message, w.TappedSecondary), row)
}

// resolveAuthor resolves the display name, role colour, and avatar URL for a
// message's author. Shared by construction and RefreshAuthor, so a lazily
// fetched author renders identically either way.
func resolveAuthor(deps Deps, message *domain.Message) (name string, nameColor color.Color, avatarURL string) {
	author := deps.Store.MessageAuthor(message)

	nameColor = theme.Colors.TextPrimary
	if author.Color != nil {
		nameColor = author.Color
	}

	return author.Name, nameColor, author.AvatarURL
}

/* System events */

// buildSystemLine renders a server-generated event as the one line it is: the
// mark for what happened, standing in the gutter an avatar would occupy, then the
// sentence and the time it happened at.
//
// The time is drawn rather than revealed on hover, as a full message's is: there
// is no name for it to follow here, and a line that reads "Someone left" with
// nothing to say when is the one case where the timestamp is most of the content.
//
// The name it announces is the one thing here that accepts a pointer: it opens
// that person's profile, as their name does anywhere else in the client. It
// carries the row's context menu for the reason mentionText documents, and it is
// deliberately not hoverable, so the row keeps the hover the whole line lights up
// with. Nothing else in here answers anything.
func (w *MessageWidget) buildSystemLine(timestamp string) fyne.CanvasObject {
	name, rest := w.deps.Store.SystemTextParts(w.message.System)

	w.systemText = canvas.NewText(rest, theme.Colors.SystemMessageText)
	w.systemText.TextSize = theme.Sizes.SystemMessageTextSize

	time := canvas.NewText(timestamp, theme.Colors.TimestampText)
	time.TextSize = theme.Sizes.MessageTimestampSize

	mark, tone := systemMark(w.message.System.Kind)
	icon := newScaledIcon(tintedIcon(mark, tone), theme.Sizes.SystemMessageIconSize)
	gutter := container.New(&columnLayout{
		width:     theme.Sizes.MessageAvatarColumnWidth,
		topOffset: systemIconTopOffset(),
	}, icon)

	// Every text here is a sibling in the one row, so the smaller time centres
	// itself against the line rather than needing an offset of its own.
	line := make([]fyne.CanvasObject, 0, 4)
	if name != "" {
		target := w.message.System.Target
		w.systemName = newMentionText(name, theme.Sizes.SystemMessageTextSize, fyne.TextStyle{Bold: true},
			func(anchor fyne.CanvasObject) { w.deps.Actions.OnUserTapped(target, anchor) },
			w.TappedSecondary)
		line = append(line, w.systemName)
	}
	w.systemLine = HBoxNoSpacing(append(line, w.systemText, HorizontalSpacer(theme.Sizes.MessageContentPadding), time)...)

	return NewFillRow(2, gutter, HorizontalSpacer(theme.Sizes.MessageContentPadding), w.systemLine)
}

// systemMark is the mark an event is announced by and the colour it is drawn in.
// The two are decided together because they say different halves of one thing:
// the tone is the class of event — an arrival, a departure, a removal, a change
// to the channel — which is what a column of these is skimmed by, and the glyph
// is which event of that class it was.
//
// SystemKind is Revolt's own vocabulary, carried verbatim, so an event the
// platform adds later falls through to the generic mark in the neutral grey
// rather than drawing nothing.
func systemMark(kind domain.SystemKind) (fyne.Resource, color.Color) {
	switch kind {
	case domain.SystemUserJoined:
		return assets.SystemJoinedIcon, theme.Colors.SystemMessageJoin
	case domain.SystemUserAdded:
		return assets.SystemAddedIcon, theme.Colors.SystemMessageJoin
	case domain.SystemUserLeft:
		return assets.SystemLeftIcon, theme.Colors.SystemMessageLeave
	case domain.SystemUserRemove:
		return assets.SystemRemovedIcon, theme.Colors.SystemMessageLeave
	case domain.SystemUserKicked:
		return assets.SystemKickedIcon, theme.Colors.SystemMessageDanger
	case domain.SystemUserBanned:
		return assets.SystemBannedIcon, theme.Colors.SystemMessageDanger
	case domain.SystemChannelRenamed:
		return assets.SystemRenamedIcon, theme.Colors.SystemMessageChange
	case domain.SystemChannelDescriptionChanged:
		return assets.SystemDescriptionIcon, theme.Colors.SystemMessageChange
	case domain.SystemChannelIconChanged:
		return assets.SystemPictureIcon, theme.Colors.SystemMessageChange
	case domain.SystemChannelOwnershipChanged:
		return assets.SystemOwnerIcon, theme.Colors.SystemMessageChange
	case domain.SystemMessagePinned:
		return assets.SystemPinnedIcon, theme.Colors.SystemMessageChange
	case domain.SystemMessageUnpinned:
		return assets.SystemUnpinnedIcon, theme.Colors.SystemMessageChange
	case domain.SystemCallStarted:
		return assets.SystemCallIcon, theme.Colors.SystemMessageCall
	}

	return assets.SystemEventIcon, theme.Colors.SystemMessageIcon
}

/* Vertical alignment */

// messageLineHeight is the height of one line of message text. The author name
// and a single-line body share it, both being drawn at the theme's text size.
func messageLineHeight() float32 { return lineHeight(fynetheme.TextSize()) }

// avatarTopOffset places the avatar centred on the block a single-line message
// occupies: the author line plus one line of body. It is an offset from the top
// of the row rather than a centring of the whole row, so a longer body, an
// attachment or a reply grows away from it and every message's avatar sits at
// the same height whatever it says.
func avatarTopOffset() float32 {
	return (messageLineHeight()*2 - theme.Sizes.MessageAvatarSize) / 2
}

// gutterTimestampTopOffset centres a grouped continuation's hover timestamp on
// the one body line it stands beside. It is smaller text than the body, so
// sharing that line's centre takes an offset of its own.
func gutterTimestampTopOffset() float32 {
	return (messageLineHeight() - lineHeight(theme.Sizes.MessageTimestampSize)) / 2
}

// systemIconTopOffset centres an event's mark on the one line of text beside it.
// The mark is a different height from a line of type, so sharing that line's
// centre takes an offset of its own — as the grouped timestamp above does.
func systemIconTopOffset() float32 {
	return (lineHeight(theme.Sizes.SystemMessageTextSize) - theme.Sizes.SystemMessageIconSize) / 2
}

// verticalPad returns this message's top or bottom margin: tight when it abuts a
// same-author continuation, the full gap otherwise. A system line keeps its own
// margin whatever surrounds it — it can neither open nor continue a group, so the
// question the flag answers doesn't arise, and a run of joins reads as one block.
func (w *MessageWidget) verticalPad(tight bool) float32 {
	switch {
	case w.message.System != nil:
		return theme.Sizes.SystemMessagePadding
	case tight:
		return theme.Sizes.MessageGroupedVerticalPadding
	}

	return theme.Sizes.MessageVerticalPadding
}

// buildMessageContent assembles the author header plus whatever the message
// carries beneath its text.
func buildMessageContent(deps Deps, message *domain.Message, author *AccentText, timestamp string, body fyne.CanvasObject, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	header := buildMessageHeader(author, timestamp, body)

	extras := buildMessageExtras(deps, message, onMenu)
	if len(extras) == 0 {
		return header
	}

	return container.NewVBox(append([]fyne.CanvasObject{header}, extras...)...)
}

// buildGroupedContent renders a grouped continuation: the body and its extras,
// with no author/timestamp header.
func buildGroupedContent(deps Deps, message *domain.Message, body fyne.CanvasObject, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	extras := buildMessageExtras(deps, message, onMenu)
	if len(extras) == 0 {
		return body
	}

	return container.NewVBox(append([]fyne.CanvasObject{body}, extras...)...)
}

// buildMessageExtras is what hangs below a message's text: its attachments, then
// the embeds, then any invite it links to — what was uploaded before what was
// unfurled, since only the first was deliberate, and the invite last because it
// is the only one the client composed rather than the server. Empty for the
// great majority of messages, which is why both callers check before wrapping
// the body in a box at all.
func buildMessageExtras(deps Deps, message *domain.Message, onMenu func(*fyne.PointEvent)) []fyne.CanvasObject {
	var extras []fyne.CanvasObject

	if len(message.Attachments) > 0 {
		extras = append(extras, buildAttachments(deps, message.Attachments, onMenu))
	}
	if len(message.Embeds) > 0 {
		extras = append(extras, buildEmbeds(deps, message.Embeds, onMenu))
	}
	if codes := inviteCodesIn(message.Content); len(codes) > 0 {
		extras = append(extras, buildInvites(deps, codes))
	}

	return extras
}

// buildMessageHeader renders the author line — the bold name in its role colour
// followed by the timestamp — above the message text. Keeping the timestamp
// inline on the name line aligns it with the username and stops long body text
// running under it.
//
// Both texts go straight into the HBox, which stretches each to the line's full
// height; canvas.Text centres its glyphs in whatever height it is given, so the
// smaller timestamp lands centred against the name with no offset of our own.
func buildMessageHeader(author *AccentText, timestamp string, body fyne.CanvasObject) fyne.CanvasObject {
	ts := canvas.NewText(timestamp, theme.Colors.TimestampText)
	ts.TextSize = theme.Sizes.MessageTimestampSize

	nameLine := HBoxNoSpacing(author, HorizontalSpacer(theme.Sizes.MessageContentPadding), ts)

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

// buildReplyBlock stacks the quoted lines above the message answering them,
// ending in the gap that separates the two.
func buildReplyBlock(deps Deps, message *domain.Message, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	quotes := make([]fyne.CanvasObject, 0, len(message.Replies)+1)
	for _, replyID := range message.Replies {
		quotes = append(quotes, buildReplyPreview(deps, message.ChannelID, replyID, onMenu))
	}

	return VBoxNoSpacing(append(quotes, VerticalSpacer(theme.Sizes.MessageReplyBlockGap))...)
}

// newReplyLine draws the elbow that ties a quoted line to the message answering
// it: a leg standing in the avatar gutter and an arm running right to the quote.
// Both are plain rectangles meeting at a square corner — nothing rounds the turn.
// Every quoted line carries its own, so a stack of them reads as several separate
// answers rather than one bracket around the group.
func newReplyLine() fyne.CanvasObject {
	leg := canvas.NewRectangle(theme.Colors.ReplyLine)
	arm := canvas.NewRectangle(theme.Colors.ReplyLine)

	return container.New(&replyLineLayout{}, leg, arm)
}

// replyLineLayout draws the elbow across the width the avatar gutter and the gap
// after it occupy, so the quote it leads starts exactly where the message body
// below does. It reports no height of its own — the quoted line decides the row,
// and the elbow is measured against whatever that comes to. Exactly two children,
// leg first.
type replyLineLayout struct{}

func (l *replyLineLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}
	leg, arm := objects[0], objects[1]

	thickness := theme.Sizes.MessageReplyLineThickness
	x := theme.Sizes.MessageReplyLineInset
	// The arm sits on the quoted line's centre and the leg hangs from the corner to
	// the foot of the row, pointing at the message the quote belongs to.
	y := (size.Height - thickness) / 2

	leg.Resize(fyne.NewSize(thickness, max(size.Height-y, 0)))
	leg.Move(fyne.NewPos(x, y))

	arm.Resize(fyne.NewSize(max(size.Width-x-theme.Sizes.MessageReplyLineGap, 0), thickness))
	arm.Move(fyne.NewPos(x, y))
}

func (l *replyLineLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	width := theme.Sizes.MessageAvatarColumnWidth + theme.Sizes.MessageContentPadding

	return fyne.NewSize(width, 0)
}

// buildReplyPreview renders the small quoted line shown above a message that
// replies to another.
func buildReplyPreview(deps Deps, channelID, messageID string, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
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
	quote := NewTappableContainer(row, func() {})
	quote.onSecondaryTap = onMenu

	// The elbow both indents the quote to the message content column and draws the
	// line down to the message. The row's own horizontal margin is already applied
	// around the block, so it only has the gutter and the gap after it to span.
	// TODO: navigate to the referenced message on tap.
	return HBoxNoSpacing(newReplyLine(), quote)
}

// resolveReply looks up a referenced message and returns its author, truncated
// content, avatar URL, and role colour (nil when none). A missing reference
// yields a placeholder.
func resolveReply(deps Deps, channelID, messageID string) (author, content, avatarURL string, accent color.Color) {
	message := deps.Actions.ResolveMessage(channelID, messageID)
	if message == nil {
		return "", "Unknown message reference", "", nil
	}

	a := deps.Store.MessageAuthor(message)
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
