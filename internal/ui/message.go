package ui

import (
	"image/color"
	"math"
	"slices"
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
	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	// hoverHideDelay debounces the transition between the message body and the
	// floating action buttons so they don't flicker.
	hoverHideDelay = 50 * time.Millisecond

	// flashDuration is how long the wash marking a jumped-to message takes to fade:
	// long enough to find the row, short enough not to read as a state it is in.
	flashDuration = 1200 * time.Millisecond

	// editFlashDuration is the wash an edit lands with, up and back down inside it.
	// Shorter than a jump's and symmetric: nobody asked to be shown this row, so it
	// has to be noticed in passing rather than sat through.
	editFlashDuration = 1000 * time.Millisecond

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

	content    *fyne.Container
	background *canvas.Rectangle

	// Kept so an author resolving after the mount is written in place by
	// RefreshAuthor rather than re-rendering the channel. Nil for a grouped
	// continuation, which draws neither name nor avatar.
	authorText *AccentText
	avatar     *Avatar

	// authorLine is the header the name sits on and authorMarks the slot after it,
	// holding the glyph that says who is posting. The slot is empty for a person,
	// and an empty one is zero-width, so a name with nothing to say about it pays
	// no gap; it is filled here or by RefreshAuthor, whichever learns first.
	authorLine  *fyne.Container
	authorMarks *fyne.Container

	// gutterTimestamp stands in the avatar's place on a grouped continuation,
	// revealed on hover. Nil for a full message.
	gutterTimestamp *canvas.Text

	// editMark is the pencil-and-span note an edited message trails, and editMarkRow
	// the box holding it. Both nil when the message has never been edited. Kept so
	// the span can be rewritten as it grows — see RefreshEditMark — without
	// rebuilding the row.
	editMark    *canvas.Text
	editMarkRow *fyne.Container

	// A system message's target, the rest of its sentence, and the row they share
	// with the time — kept so a target resolving later can be written in place,
	// which moves what follows it. systemName is nil for an event about the channel
	// rather than a person, all three for a message somebody wrote.
	systemName *mentionText
	systemText *canvas.Text
	systemLine *fyne.Container

	// replies are the quoted lines above the message, kept because what a reply
	// quotes can be older than anything cached: resolving one is a request, and the
	// line fills itself in when the answer lands.
	replies []*replyPreview

	// bottomSpacer is the bottom margin, kept for SetFollowedByGroup.
	bottomSpacer *canvas.Rectangle

	// daySeparator is the dated divider above the first message of a day. It lives
	// on the widget rather than as its own list entry, so the mounted window stays
	// one object per message and re-evaluating a predecessor re-derives it.
	daySeparator fyne.CanvasObject

	// bodySlot holds the rendered body; StartEdit swaps it for an editor and
	// CancelEdit restores body, leaving header, attachments and replies alone.
	bodySlot *fyne.Container
	body     fyne.CanvasObject

	// codes are the invite links the body carries, taken off the document the body
	// was rendered from. Nil for the great majority of messages.
	codes []string

	// relativeTime marks a body carrying a <t:…:R>, whose reading is resolved at
	// mount and goes out of date on screen. False for the great majority, which is
	// what keeps the clock in app off every other row.
	relativeTime bool

	// reactionRow is the box the chips are drawn in, kept so a reaction anybody
	// adds repaints them alone — see SetReactions. Nil while the message carries
	// none, that box not being built at all.
	reactionRow *fyne.Container

	// The quick-actions are built on first reveal (ensureActions), so a message the
	// pointer never touches builds no buttons. actionsOverlay is empty until then.
	actionsOverlay *fyne.Container
	actions        *fyne.Container

	// selectOverlay holds the tick while the column is picking messages for a bulk
	// delete, in the same trailing corner the quick-actions use: the two are never
	// up together, selecting being a mode in which nothing else about a message is
	// on offer. Empty until the first time this row is asked to select.
	selectOverlay *fyne.Container
	selectTick    *selectTick

	// selecting is the column in that mode and selected this row being in the set.
	// Both are pushed in by MessageList rather than read from it — a row is built
	// already knowing, so one scrolling back in does not come back unticked.
	selecting bool
	selected  bool

	// mentioned warms the row's background. Decided once: it is a fact about the
	// message, and every path that re-evaluates one rebuilds the widget.
	mentioned bool

	// deleted is a message that has gone and whose row is still standing. Pushed in
	// by MessageList, which holds the set for the controller — a row scrolling back
	// in is built already marked, the same way a picked one is.
	deleted bool

	editing   bool
	emptyBody bool // says nothing, so the slot stays hidden outside an edit

	// overChild is the pointer being over something in the row that takes hover for
	// itself — the action group, a reaction chip. Innermost wins and nothing above
	// hears of it, so each reports back here or the row drops its buttons the moment
	// the pointer crosses one on the way to them.
	overMessage bool
	overChild   bool
	hideTimer   *time.Timer

	// flash is the wash a jump leaves, kept so a second jump — or the pointer
	// arriving, the reader having found the row — takes it off rather than fights it.
	flash *fyne.Animation
}

var (
	_ fyne.Widget            = (*MessageWidget)(nil)
	_ desktop.Hoverable      = (*MessageWidget)(nil)
	_ fyne.SecondaryTappable = (*MessageWidget)(nil)
)

// NewMessageWidget builds a message widget. grouped marks a continuation of the
// previous message from the same author: no avatar or name header, a
// hover-revealed timestamp in the gutter instead. Spacing is asymmetric — a head
// carries the full gap above, a continuation a tight one, and followedByGroup
// tightens the bottom so a head sits flush against what follows without changing
// the gap between groups. A non-empty dayLabel draws the day separator above.
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

	// A system message is not a message anybody wrote: no avatar, name, edit or
	// anything hanging beneath. It shares only the margins, the hover and the day
	// separator with what people say.
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

	// The tick sits inside the row rather than bleeding above it the way the
	// quick-actions do: it is up for as long as the mode is, so a row overlapping
	// the one above would put two ticks in one gutter at the seam between groups.
	// Hidden until first filled: the driver's walks prune on visibility, and two
	// standing containers per mounted row is a real slice of the per-frame walk.
	// Once filled they stay shown — their children carry their own visibility.
	w.actionsOverlay = container.New(&overlayLayout{yOffset: -16, rightOffset: 6})
	w.actionsOverlay.Hide()
	w.selectOverlay = container.New(&overlayLayout{yOffset: 2, rightOffset: 6})
	w.selectOverlay.Hide()
	w.content = container.NewStack(inner, w.actionsOverlay, w.selectOverlay)

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

	// Above the hover-highlight stack, not inside it, so hovering the message does
	// not light the divider as part of the row.
	return widget.NewSimpleRenderer(VBoxNoSpacing(w.daySeparator, row))
}

// Message returns the message this widget renders.
func (w *MessageWidget) Message() *domain.Message { return w.message }

// Editing reports whether the body has been swapped for an editor. The list
// keeps such a row mounted wherever it scrolls: the draft lives in the widget.
func (w *MessageWidget) Editing() bool { return w.editing }

// Author is the user this row names — the message's author, or for a system event
// whoever the line's name stands for, that being what a lazy fetch has to bring
// back. An event naming nobody answers "".
func (w *MessageWidget) Author() string {
	if system := w.message.System; system != nil {
		return system.Subject()
	}

	return w.message.AuthorID
}

// NamesAny reports whether the row draws any of these accounts, so a batch of
// people just resolved knows which rows to repaint. A system line can name two —
// whoever it is about and whoever did it — and only the first of them is Author.
func (w *MessageWidget) NamesAny(userIDs []string) bool {
	// An empty ID would otherwise match every system line that names nobody.
	if author := w.Author(); author != "" && slices.Contains(userIDs, author) {
		return true
	}

	system := w.message.System

	return system != nil && system.By != "" && slices.Contains(userIDs, system.By)
}

// SetFollowedByGroup tightens (or restores) the bottom margin when a same-author
// continuation is appended directly beneath this message after it was mounted.
func (w *MessageWidget) SetFollowedByGroup(followed bool) {
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, w.verticalPad(followed)))
	w.bottomSpacer.Refresh()
	w.Refresh()
}

// RefreshAuthor re-resolves the name, role colour and avatar in place, for an
// author fetched after the mount. Nothing to do on a grouped continuation. A
// system line names its target mid-sentence, so re-resolving moves everything
// after it — hence the relayout a name of fixed extent does not need.
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

	author, nameColor := resolveAuthor(w.deps, w.message)
	w.authorText.Set(author.Name, nameColor)
	w.avatar.SetSource(w.deps.Images, author.AvatarURL)

	// The mark can arrive with the answer: an unfetched account is not yet known to
	// be a bot. A mask is known from the message and was drawn at the mount.
	w.setAuthorMark(author.Mark)
}

// RefreshReplies re-reads the quoted lines whose target is in resolved. It takes
// the set rather than refreshing unconditionally because every mounted row is
// offered the batch and re-laying a line out is not free.
func (w *MessageWidget) RefreshReplies(resolved map[string]bool) {
	for _, preview := range w.replies {
		if resolved[preview.messageID] {
			preview.Refresh(w.deps)
		}
	}
}

/* Permissions */

// isOwnMessage reports whether the message was authored by the logged-in user.
func (w *MessageWidget) isOwnMessage() bool {
	self := w.deps.Store.SelfID()

	return self != "" && self == w.message.AuthorID
}

// canEdit: your own non-system message. A system one has no editable content.
func (w *MessageWidget) canEdit() bool {
	return w.message.System == nil && w.isOwnMessage()
}

// canReply: a system event is the channel narrating itself, and nobody is waiting
// to be answered.
func (w *MessageWidget) canReply(permissions domain.Permission) bool {
	return w.message.System == nil && permissions.Has(domain.PermissionSendMessage)
}

// canDelete: your own message, or any where you hold ManageMessages.
func (w *MessageWidget) canDelete(permissions domain.Permission) bool {
	return w.isOwnMessage() || permissions.Has(domain.PermissionManageMessages)
}

// canPin: authorship buys nothing, unlike deleting — a pin is a change to the
// channel, so Revolt asks for ManageMessages even over your own words.
func (w *MessageWidget) canPin(permissions domain.Permission) bool {
	return w.message.System == nil && permissions.Has(domain.PermissionManageMessages)
}

// canBulkSelect: whether this row may join a bulk delete. Not canDelete with a
// count — Revolt asks for ManageMessages over the bulk route even for the
// account's own words, where a single delete takes authorship instead, and it
// refuses the *whole* batch over one message past domain.MaxBulkDeleteAge. So a
// row it would refuse is never offered rather than selected and then dropped.
func (w *MessageWidget) canBulkSelect(permissions domain.Permission) bool {
	return permissions.Has(domain.PermissionManageMessages) && withinBulkWindow(w.message.ID)
}

// withinBulkWindow reports whether a message is young enough for a bulk delete.
// An unreadable ID answers false: the route reads the same ULID and refuses one
// it cannot parse.
func withinBulkWindow(messageID string) bool {
	when, err := util.Timestamp(messageID)
	if err != nil {
		return false
	}

	return time.Since(when) < domain.MaxBulkDeleteAge
}

// canClearReactions: a message carrying none is not asked about, which also
// covers a system message — Revolt refuses a reaction to one.
func (w *MessageWidget) canClearReactions(permissions domain.Permission) bool {
	return len(w.message.Reactions) > 0 && permissions.Has(domain.PermissionManageMessages)
}

// canReact gates whether the chips answer a click.
func (w *MessageWidget) canReact(permissions domain.Permission) bool {
	return w.message.System == nil && permissions.Has(domain.PermissionReact)
}

// canAddReaction is canReact plus the message's own say. One restricting
// reactions to nothing at all is offered no way in: the picker would open empty,
// and every pick from it would be refused by the server.
func (w *MessageWidget) canAddReaction(permissions domain.Permission) bool {
	allowed, restricted := w.message.ReactionsAllowed()

	return w.canReact(permissions) && (!restricted || len(allowed) > 0)
}

// permissions is asked when something needs it — a hover, a right-click, and a
// row that carries reactions, whose chips have to know whether they answer a
// click — never per mounted row. Once per menu, one bitfield answering every
// question it has.
func (w *MessageWidget) permissions() domain.Permission {
	return w.deps.Store.Permissions(w.message.ChannelID)
}

/* Quick actions and context menu */

// actionMark tints an action mark neutral, what an action that only takes you
// somewhere wears. The two that commit something name their own colour at the
// call site, that colour being the warning.
func actionMark(res fyne.Resource) fyne.Resource {
	return tintedIcon(res, theme.Colors.SwiftActionIcon)
}

// buildActions creates the hidden, rounded group of quick-action buttons, each
// gated the same way the matching menu item is.
func (w *MessageWidget) buildActions() *fyne.Container {
	onHover := func(hovering bool) {
		w.overChild = hovering
		w.updateHover()
	}
	act := w.deps.Actions
	permissions := w.permissions()

	var buttons []fyne.CanvasObject
	if w.canAddReaction(permissions) {
		react := NewIconButton(actionMark(assets.ActionAddIcon), nil, onHover)
		react.onTap = func() { w.react(react) }
		buttons = append(buttons, react)
	}
	if w.canReply(permissions) {
		buttons = append(buttons, NewIconButton(actionMark(assets.ActionReplyIcon), func() { act.OnReply(w.message) }, onHover))
	}
	if w.canEdit() {
		buttons = append(buttons, NewIconButton(actionMark(assets.ActionEditIcon), func() { act.OnEdit(w.message) }, onHover))
	}
	if w.canDelete(permissions) {
		buttons = append(buttons, NewIconButton(tintedIcon(assets.ActionDeleteIcon, theme.Colors.SwiftActionDanger), func() { act.OnDelete(w.message) }, onHover))
	}

	// The overflow button is always last and opens the right-click menu beneath itself.
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
	if w.canAddReaction(permissions) {
		items = append(items, fyne.NewMenuItemWithIcon("Add reaction", actionMark(assets.ActionAddIcon), func() {
			w.react(w)
		}))
	}
	if w.canReply(permissions) {
		items = append(items, fyne.NewMenuItemWithIcon("Reply", actionMark(assets.ActionReplyIcon), func() { act.OnReply(w.message) }))
	}
	if w.canEdit() {
		items = append(items, fyne.NewMenuItemWithIcon("Edit", actionMark(assets.ActionEditIcon), func() { act.OnEdit(w.message) }))
	}

	// Menu only, never a hover button: the quick actions are the things done often
	// enough to be worth a click without opening anything.
	if w.canPin(permissions) {
		label, mark := "Pin message", assets.SystemPinnedIcon
		if w.message.Pinned {
			label, mark = "Unpin message", assets.SystemUnpinnedIcon
		}
		pinned := !w.message.Pinned
		items = append(items, fyne.NewMenuItemWithIcon(label, actionMark(mark), func() { act.OnPin(w.message, pinned) }))
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

	// The two that take something away from everybody sit under the last separator,
	// furthest from the copy helpers a misaimed click would land on.
	clearable, deletable := w.canClearReactions(permissions), w.canDelete(permissions)
	if clearable || deletable {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	if clearable {
		items = append(items, fyne.NewMenuItemWithIcon("Clear reactions",
			tintedIcon(assets.ActionEmojiIcon, theme.Colors.SwiftActionDanger), func() { act.OnClearReactions(w.message) }))
	}
	if deletable {
		items = append(items, fyne.NewMenuItemWithIcon("Delete",
			tintedIcon(assets.ActionDeleteIcon, theme.Colors.SwiftActionDanger), func() { act.OnDelete(w.message) }))
	}

	// The way into selecting, and the only one: a bulk delete is a mode, and a mode
	// entered by a stray click is a mode nobody meant to be in. This row joins the
	// set on the way in, the reader having already named a message by opening the
	// menu on it.
	if w.canBulkSelect(permissions) {
		items = append(items, fyne.NewMenuItemWithIcon("Select messages",
			actionMark(assets.ActionSaveIcon), func() { act.OnSelectMessages(w.message) }))
	}

	return items
}

// react adds the picked emoji, for the two entry points that offer one to a
// message carrying none. Picking one already there is a request to have reacted,
// which is already true.
func (w *MessageWidget) react(anchor fyne.CanvasObject) {
	allowed, _ := w.message.ReactionsAllowed()

	w.deps.Actions.OnPickEmoji(anchor, allowed, func(choice EmojiChoice) {
		w.deps.Actions.OnReact(w.message, choice.Value(), true)
	})
}

// TappedSecondary opens the message context menu at the cursor on right-click.
func (w *MessageWidget) TappedSecondary(e *fyne.PointEvent) {
	ShowContextMenu(w, w.menuItems(), e.AbsolutePosition)
}

/* In-place editing */

// StartEdit swaps the body for an in-place editor, save/cancel floating where the
// quick-actions do. Saving unchanged or emptied content counts as a cancel.
// Answers with the entry to focus, or nil when the message is not editable or is
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

	hint := newText("esc to cancel  •  enter to save", theme.Colors.TimestampText, theme.Sizes.MessageTimestampSize)
	w.bodySlot.Objects = []fyne.CanvasObject{container.NewVBox(WithCaret(entry), hint)}
	w.bodySlot.Show()
	w.bodySlot.Refresh()

	// The save/cancel pair replaces the hover quick-actions for the whole edit.
	buttons := container.NewStack(roundedPanel(), HBoxNoSpacing(
		NewIconButton(tintedIcon(assets.ActionSaveIcon, theme.Colors.SwiftActionConfirm), save, nil),
		NewIconButton(actionMark(assets.ActionCancelIcon), cancel, nil),
	))
	w.actionsOverlay.Objects = []fyne.CanvasObject{buttons}
	w.actionsOverlay.Show()
	w.actionsOverlay.Refresh()

	w.setHighlighted(true)
	return entry
}

// HasRelativeTime reports whether this row's body carries a <t:…:R>. The
// controller asks so its clock can re-render the few rows that need it rather
// than every row on screen.
func (w *MessageWidget) HasRelativeTime() bool { return w.relativeTime }

// RefreshRelativeTime re-renders the body, which is what re-reads a relative
// timestamp against the clock: "in 5 minutes" would otherwise say that for as
// long as the row stayed mounted. A no-op where the body carries none, and while
// the message is being edited — the slot holds an entry then, and replacing it
// under somebody typing would lose what they had written.
func (w *MessageWidget) RefreshRelativeTime() {
	if !w.relativeTime || w.editing {
		return
	}

	w.body = newFlushContainer(renderMessageBody(w.deps, w.message.Content, w.TappedSecondary))
	w.bodySlot.Objects = []fyne.CanvasObject{w.body}
	w.bodySlot.Refresh()

	// The reading can change width and so a wrapped body's height — "in 1 minute"
	// becoming "just now" — and the column places rows from measured heights, so
	// the row is re-laid rather than repainted into the space the old text asked
	// for. Relayout, not w.Refresh(): the slot above covers the new body, and the
	// whole-row walk re-uploaded the avatar and every attachment once a minute
	// for every mounted row carrying a span.
	Relayout(w.content)
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
	w.actionsOverlay.Hide()
	if w.actions != nil {
		w.actions.Hide()
		w.actionsOverlay.Objects = []fyne.CanvasObject{w.actions}
		w.actionsOverlay.Show()
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
// over them, hiding them after a grace period otherwise. Suspended while editing,
// which paints its own highlight and overlay, and while selecting, where the row
// offers one thing and the quick-actions would share the corner the tick is in.
// The wash still follows the pointer either way — see setHighlighted.
func (w *MessageWidget) updateHover() {
	if w.editing {
		return
	}

	if w.selecting {
		w.setHighlighted(w.overMessage || w.overChild)
		return
	}

	if w.overMessage || w.overChild {
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
			// Cleared first: a timer firing mid-edit used to return with the field
			// still set, so the guard above refused to arm another and the actions
			// stayed up for good.
			w.hideTimer = nil
			if w.overMessage || w.overChild || w.editing {
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
	w.actionsOverlay.Show()
	w.actionsOverlay.Refresh()
}

/* Selecting */

// SetSelecting puts the row in or out of selection mode, picked saying whether it
// is in the set. Both arrive together because a row scrolling back in is built
// and told once: asking twice would draw it unticked for a frame.
//
// A row the route would refuse (canBulkSelect) wears no tick and takes no click,
// but still enters the mode — greyed out rather than looking ordinary, or a
// column with a week's history in it reads as half broken.
func (w *MessageWidget) SetSelecting(on, picked bool) {
	w.selecting = on
	w.selected = on && picked

	if on {
		w.ensureTick()
		w.selectTick.set(w.selected)
		w.selectTick.Show()
	} else if w.selectTick != nil {
		w.selectTick.Hide()
	}

	// The quick-actions go with the mode: they are built lazily, so a row the
	// pointer never visited has none to hide.
	if w.actions != nil {
		w.actions.Hide()
	}
	w.stopHideTimer()
	w.setHighlighted(w.overMessage || w.overChild)
}

// SetSelected marks or unmarks a row already in the mode.
func (w *MessageWidget) SetSelected(picked bool) {
	if !w.selecting || picked == w.selected {
		return
	}

	w.selected = picked
	w.selectTick.set(picked)
	w.setHighlighted(w.overMessage || w.overChild)
}

// Selected reports whether the row is in the set.
func (w *MessageWidget) Selected() bool { return w.selected }

// ensureTick builds the tick on the first time this row is asked to select, the
// way the quick-actions are built on the first hover.
func (w *MessageWidget) ensureTick() {
	if w.selectTick != nil {
		return
	}

	offered := w.canBulkSelect(w.permissions())
	w.selectTick = newSelectTick(offered, func(extend bool) {
		w.deps.Actions.OnToggleSelected(w.message, extend)
	})
	w.selectTick.onHover = func(hovering bool) {
		w.overChild = hovering
		w.updateHover()
	}

	// A right-click anywhere in the row has to reach the row's own menu, this one
	// included — the driver gives the press to the innermost object that takes one
	// and does not walk back up.
	w.selectTick.onSecondaryTap = w.TappedSecondary

	w.selectOverlay.Objects = []fyne.CanvasObject{w.selectTick}
	w.selectOverlay.Show()
	w.selectOverlay.Refresh()
}

// selectTick is the box a row wears while the column is selecting, and the whole
// of the target: the row itself cannot take the click, its body being a
// selectable Label that answers one already — innermost wins, see
// internal/ui/CLAUDE.md. Shift extends from the last row picked, which is read
// here rather than passed, ui.ShiftHeld being the only thing that can answer it.
type selectTick struct {
	tapBase

	background *canvas.Rectangle
	mark       *canvas.Image
	content    fyne.CanvasObject
	onHover    func(bool)

	// offered is a row this channel's permissions or Revolt's week-long window put
	// out of reach. It keeps its box so the column does not change shape down its
	// edge, and answers nothing.
	offered bool
	on      bool
	hovered bool
}

var (
	_ fyne.Tappable     = (*selectTick)(nil)
	_ desktop.Hoverable = (*selectTick)(nil)
)

func newSelectTick(offered bool, onPick func(extend bool)) *selectTick {
	t := &selectTick{
		background: canvas.NewRectangle(color.Transparent),
		mark:       newScaledIcon(tintedIcon(assets.ActionSaveIcon, theme.Colors.MessageSelectMark), theme.Sizes.MessageSelectMarkSize),
		offered:    offered,
	}
	t.background.CornerRadius = theme.Sizes.MessageSelectTickSize / 2
	t.background.StrokeWidth = theme.Sizes.OutlineWidth

	if offered {
		t.onTap = func() { onPick(ShiftHeld()) }
	}

	t.content = container.NewStack(t.background, container.NewCenter(t.mark))
	t.ExtendBaseWidget(t)
	t.repaint()

	return t
}

func (t *selectTick) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

// MinSize is the circle, whatever the mark inside it measures.
func (t *selectTick) MinSize() fyne.Size {
	side := theme.Sizes.MessageSelectTickSize

	return fyne.NewSize(side, side)
}

// Cursor keeps the arrow over a tick offering nothing — tapBase promises a
// pointer unconditionally, which here is a lie the reader acts on.
func (t *selectTick) Cursor() desktop.Cursor {
	if !t.offered {
		return desktop.DefaultCursor
	}

	return desktop.PointerCursor
}

func (t *selectTick) MouseIn(*desktop.MouseEvent) { t.hovered = true; t.repaint() }
func (t *selectTick) MouseOut()                   { t.hovered = false; t.repaint() }

func (t *selectTick) set(on bool) {
	t.on = on
	t.repaint()
}

// repaint is the tick's whole appearance in one place: filled and marked when it
// is in the set, an empty ring when it is not, and dimmed throughout when the row
// is one the route would refuse.
func (t *selectTick) repaint() {
	edge, fill := theme.Colors.MessageSelectTickEdge, color.Color(color.Transparent)
	if t.on {
		edge, fill = theme.Colors.MessageSelectTickOn, theme.Colors.MessageSelectTickOn
	}
	if t.hovered && t.offered {
		edge = solidColor(theme.Lighten(edge, 0.25))
	}
	if !t.offered {
		edge, fill = theme.Colors.MessageSelectTickOff, color.Transparent
	}

	t.background.StrokeColor = solidColor(edge)
	t.background.FillColor = fill
	t.mark.Hidden = !t.on
	t.background.Refresh()
	t.mark.Refresh()

	reportHover(t.onHover, t.hovered)
}

/* The delete mark */

// SetDeleted marks the row as one whose message has been deleted and which is
// standing for a moment longer. Unlike the jump and edit washes this is a
// *state*: it stands until the row goes, so it goes through fill() and the row
// still lifts under the pointer. Nothing here takes it off — the row is removed
// with the mark still on it.
func (w *MessageWidget) SetDeleted(on bool) {
	if on == w.deleted {
		return
	}

	w.deleted = on
	w.setHighlighted(w.overMessage || w.overChild)
}

// setHighlighted paints (or clears) the row's hover background. Guarded on the
// fill: every child hover crossing re-enters updateHover, so sweeping the
// quick-action group repainted the row once per button.
func (w *MessageWidget) setHighlighted(on bool) {
	w.stopFlash() // the pointer arriving is the reader having found the row
	if fill := w.fill(on); fill != w.background.FillColor {
		w.background.FillColor = fill
		w.background.Refresh()
	}
}

/* The jump mark */

// Flash washes the row a jump landed on and fades it back out. Not a state the
// widget holds: fill() is untouched, so the row goes on answering the pointer and
// the last tick hands the background back to it.
//
// The fade runs between two opaque colours rather than down the wash's alpha. The
// palette writes straight alpha into color.RGBA, which Go composites as
// premultiplied (see theme.Fade), so fading that way darkens the row on the way
// out — hence fading *to* what is behind a row at rest.
func (w *MessageWidget) Flash() {
	// Held, then let go: ease-out would spend most of the second on a colour too
	// faint to have found anything by.
	w.flashWash(theme.Colors.MessageJumpBackground, flashDuration, fyne.AnimationEaseIn,
		func(done float32) float32 { return 1 - done })
}

// FlashEdit washes the row an edit landed on, in and back out again. The same
// wash as a jump's on every count but two: its own colour, and a strength that
// rises before it falls — an edit is not a place the reader was brought to, so
// the row must not already be at full wash on the frame it arrives.
func (w *MessageWidget) FlashEdit() {
	w.flashWash(theme.Colors.MessageEditBackground, editFlashDuration, fyne.AnimationLinear,
		func(done float32) float32 { return float32(math.Sin(float64(done) * math.Pi)) })
}

// flashWash animates the row's background between what it rests against and wash,
// strength saying how far towards wash each tick stands. The last tick hands the
// background back to fill(), so the row goes on answering the pointer.
func (w *MessageWidget) flashWash(wash color.Color, duration time.Duration, curve fyne.AnimationCurve, strength func(done float32) float32) {
	w.stopFlash()

	rest := w.restBackdrop()
	w.flash = fyne.NewAnimation(duration, func(done float32) {
		if done >= 1 {
			w.background.FillColor = w.fill(w.overMessage || w.overChild)
		} else {
			w.background.FillColor = mixColor(rest, wash, strength(done))
		}
		canvas.Refresh(w.background)
	})

	w.flash.Curve = curve
	w.flash.Start()
}

func (w *MessageWidget) stopFlash() {
	if w.flash == nil {
		return
	}

	w.flash.Stop()
	w.flash = nil
}

// restBackdrop is what a row at rest is seen against: its own wash where it has
// one, else the message area behind it — a row's rest fill being transparent.
func (w *MessageWidget) restBackdrop() color.Color {
	switch {
	case w.deleted:
		return theme.Colors.MessageDeletedBackground
	case w.mentioned:
		return theme.Colors.MessageMentionBackground
	}

	return theme.Colors.MessageAreaBackground
}

// mixColor is from at 0 and to at 1, taking both as opaque.
func mixColor(from, to color.Color, at float32) color.Color {
	fr, fg, fb, _ := from.RGBA()
	tr, tg, tb, _ := to.RGBA()

	// RGBA reports 16-bit channels; 257 is what takes one back to 8.
	lerp := func(a, b uint32) uint8 {
		return uint8((float32(a) + (float32(b)-float32(a))*at) / 257)
	}

	return color.RGBA{R: lerp(fr, tr), G: lerp(fg, tg), B: lerp(fb, tb), A: 0xFF}
}

// fill is the row's background at rest and under the pointer. A message naming
// the account keeps its warm wash either way — that is what says they were
// addressed — so hovering lifts the colour rather than replacing it.
func (w *MessageWidget) fill(hovered bool) color.Color {
	switch {
	// A row on its way out outranks everything else it could be: the message has
	// gone, which is the only thing left worth saying about it.
	case w.deleted && hovered:
		return theme.Colors.MessageDeletedHoverBackground
	case w.deleted:
		return theme.Colors.MessageDeletedBackground

	// A picked row outranks a mention wash: the mention is a fact about the
	// message and the tick is a state the reader put it in, and only one of the two
	// is about to be acted on.
	case w.selected && hovered:
		return theme.Colors.MessageSelectedHoverBackground
	case w.selected:
		return theme.Colors.MessageSelectedBackground
	case w.mentioned && hovered:
		return theme.Colors.MessageMentionHoverBackground
	case w.mentioned:
		return theme.Colors.MessageMentionBackground
	case hovered:
		return theme.Colors.MessageHoverBackground
	}

	return color.Transparent
}

// setGutterShown reveals a grouped continuation's gutter timestamp by toggling
// its colour, so the width stays fixed and the body never shifts.
func (w *MessageWidget) setGutterShown(shown bool) {
	if w.gutterTimestamp == nil {
		return
	}

	var tint color.Color = theme.Colors.TimestampText
	if !shown {
		tint = color.Transparent
	}
	if w.gutterTimestamp.Color != tint {
		w.gutterTimestamp.Color = tint
		w.gutterTimestamp.Refresh()
	}
}

/* Content assembly */

// buildAuthoredContent assembles a message somebody wrote: the avatar gutter (or
// the hover timestamp standing in it), the header, the body slot, and any reply
// previews above them.
func (w *MessageWidget) buildAuthoredContent(grouped bool, shortTime, fullTime string) fyne.CanvasObject {
	deps, message := w.deps, w.message

	// Parsed once and read twice: the text below and the invite scan buildExtras
	// makes are the same document, and each parsing for itself is a second pass
	// over the body on every mount.
	doc := markdown.Parse(message.Content)
	w.codes = inviteCodesInParsed(message.Content, doc)
	w.relativeTime = markdown.HasRelativeTimestamp(doc)

	// Every interactive piece of the row takes the same menu, so the pointer never
	// lands somewhere that swallows a right-click.
	w.body = newFlushContainer(renderDocument(deps, doc, w.TappedSecondary))
	w.bodySlot = container.NewStack(w.body)

	// An empty body still renders one line tall, drawing as a gap above whatever the
	// message does carry. Hiding the slot removes it — the layouts around skip
	// hidden children, so no spacing is charged either. StartEdit shows it again.
	w.emptyBody = message.Content == ""
	if w.emptyBody {
		w.bodySlot.Hide()
	}

	var leftColumn, body fyne.CanvasObject
	if grouped {
		// Transparent until hover — see setGutterShown.
		w.gutterTimestamp = newText(shortTime, color.Transparent, theme.Sizes.MessageTimestampSize)

		gutter := &columnLayout{
			width:     theme.Sizes.MessageAvatarColumnWidth,
			topOffset: gutterTimestampTopOffset(),
			collapse:  true,
		}
		leftColumn = container.New(gutter, w.gutterTimestamp)

		// A continuation draws no header, so its edit mark hangs under the body
		// instead — ahead of the attachments, being a note about what was said rather
		// than something else the message carries.
		extras := w.buildExtras()
		if mark := w.buildEditMark(); mark != nil {
			extras = append([]fyne.CanvasObject{mark}, extras...)
		}
		body = stackExtras(w.bodySlot, extras)
	} else {
		author, nameColor := resolveAuthor(deps, message)

		// A webhook's author ID names no account, so its avatar opens nothing: the
		// card would be an empty stranger over a message whose whole identity is the
		// name beside it.
		var onTap func()
		if message.Webhook == nil {
			onTap = func() { deps.Actions.OnUserTapped(message.AuthorID, w.avatar) }
		}

		w.avatar = NewAvatar(deps.Images, author.AvatarURL, onTap)
		w.avatar.onSecondaryTap = w.TappedSecondary
		leftColumn = container.New(&columnLayout{
			width:     theme.Sizes.MessageAvatarColumnWidth,
			topOffset: avatarTopOffset(),
		}, w.avatar)

		w.authorText = NewAccentText(author.Name, nameColor, 0, fyne.TextStyle{Bold: true})
		w.authorMarks = HBoxNoSpacing()
		w.setAuthorMark(author.Mark)

		header := w.buildMessageHeader(fullTime, message.Pinned, w.buildEditMark(), w.bodySlot)
		body = stackExtras(header, w.buildExtras())
	}

	// One row rather than nested Borders: each inserts theme padding between its
	// edges and its centre, which made the gap after the gutter three times
	// MessageContentPadding with nothing saying so.
	row := NewFillRow(2, leftColumn, HorizontalSpacer(theme.Sizes.MessageContentPadding), body)

	// Replies go inside the row's margins, so carrying one leaves the avatar and the
	// name where a message without one puts them.
	if grouped || len(message.Replies) == 0 {
		return row
	}

	block, previews := buildReplyBlock(deps, message, w.TappedSecondary)
	w.replies = previews

	return VBoxNoSpacing(block, row)
}

// resolveAuthor is shared by construction and RefreshAuthor, so a lazily fetched
// author renders identically either way. The colour comes back beside the author
// rather than on it: a role's can be a gradient, and the fallback is the theme's,
// which the store has no business naming.
func resolveAuthor(deps Deps, message *domain.Message) (author domain.Author, nameColor color.Color) {
	author = deps.Store.MessageAuthor(message)

	nameColor = theme.Colors.TextPrimary
	if author.Color != nil {
		nameColor = author.Color
	}

	return author, nameColor
}

// setAuthorMark fills the slot after the name, once. A message keeps the first
// mark it is given: a webhook's and a mask are fixed, and an account resolving
// after the mount can only turn out to be a bot.
func (w *MessageWidget) setAuthorMark(mark domain.AuthorMark) {
	if w.authorMarks == nil || len(w.authorMarks.Objects) > 0 {
		return
	}

	glyph := NewAuthorMark(mark, theme.Sizes.MessageAuthorMarkSize)
	if glyph == nil {
		return
	}

	w.authorMarks.Add(HorizontalSpacer(theme.Sizes.MessageContentPadding))
	w.authorMarks.Add(glyph)

	// The slot was zero-width, so the timestamp beside it has to be re-placed.
	Relayout(w.authorLine)
}

/* System events */

// buildSystemLine renders an event as the one line it is: its mark standing in
// the avatar's gutter, then the sentence and the time.
//
// The time is drawn rather than revealed on hover — there is no name for it to
// follow, and "Someone left" with no *when* is half a sentence. The name is the
// one thing here accepting a pointer, opening that profile; it carries the row's
// menu and is deliberately not hoverable, so the row keeps the hover the whole
// line lights up with.
func (w *MessageWidget) buildSystemLine(timestamp string) fyne.CanvasObject {
	name, rest := w.deps.Store.SystemTextParts(w.message.System)

	w.systemText = newText(rest, theme.Colors.SystemMessageText, theme.Sizes.SystemMessageTextSize)
	time := newText(timestamp, theme.Colors.TimestampText, theme.Sizes.MessageTimestampSize)

	mark, tone := systemMark(w.message.System.Kind)
	icon := newScaledIcon(tintedIcon(mark, tone), theme.Sizes.SystemMessageIconSize)
	gutter := container.New(&columnLayout{
		width:     theme.Sizes.MessageAvatarColumnWidth,
		topOffset: systemIconTopOffset(),
	}, icon)

	// Siblings in one row, so the smaller time centres itself against the line.
	line := make([]fyne.CanvasObject, 0, 4)
	if name != "" {
		subject := w.message.System.Subject()
		w.systemName = newMentionText(name, theme.Sizes.SystemMessageTextSize, fyne.TextStyle{Bold: true},
			func(anchor fyne.CanvasObject) { w.deps.Actions.OnUserTapped(subject, anchor) },
			w.TappedSecondary)
		line = append(line, w.systemName)
	}
	w.systemLine = HBoxNoSpacing(append(line, w.systemText, HorizontalSpacer(theme.Sizes.MessageContentPadding), time)...)

	return NewFillRow(2, gutter, HorizontalSpacer(theme.Sizes.MessageContentPadding), w.systemLine)
}

// systemMark answers with the glyph *and* the colour, being two halves of one
// thing: the tone is the class of event — arrival, departure, removal, a channel
// change — which is what a column of these is skimmed by, and the glyph is which
// event of that class it was. SystemKind is Revolt's vocabulary carried verbatim,
// so one added later falls through to the generic mark in neutral grey.
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

// avatarTopOffset centres the avatar on the block a single-line message occupies:
// the author line plus one line of body. An offset from the top rather than a
// centring of the row, so a longer body grows away from it and every avatar sits
// at the same height whatever the message says.
func avatarTopOffset() float32 {
	return (messageLineHeight()*2 - theme.Sizes.MessageAvatarSize) / 2
}

// gutterTimestampTopOffset centres a grouped continuation's hover timestamp on
// the body line beside it — smaller text, so the shared centre takes an offset.
func gutterTimestampTopOffset() float32 {
	return (messageLineHeight() - lineHeight(theme.Sizes.MessageTimestampSize)) / 2
}

// systemIconTopOffset does the same for an event's mark, a different height again
// from the line of type beside it.
func systemIconTopOffset() float32 {
	return (lineHeight(theme.Sizes.SystemMessageTextSize) - theme.Sizes.SystemMessageIconSize) / 2
}

// verticalPad is this message's top or bottom margin: tight when it abuts a
// same-author continuation, the full gap otherwise. A system line keeps its own
// whatever surrounds it — it can neither open nor continue a group, and a run of
// joins reads as one block.
func (w *MessageWidget) verticalPad(tight bool) float32 {
	switch {
	case w.message.System != nil:
		return theme.Sizes.SystemMessagePadding
	case tight:
		return theme.Sizes.MessageGroupedVerticalPadding
	}

	return theme.Sizes.MessageVerticalPadding
}

// stackExtras hangs extras under head, or answers with head alone when there are
// none — the great majority of messages, which is why the box is not built
// regardless.
func stackExtras(head fyne.CanvasObject, extras []fyne.CanvasObject) fyne.CanvasObject {
	if len(extras) == 0 {
		return head
	}

	return container.NewVBox(append([]fyne.CanvasObject{head}, extras...)...)
}

// buildExtras is what hangs below a message's text, in order: attachments, then
// embeds — what was uploaded before what was unfurled, only the first being
// deliberate — then any invite, the one thing the client composed rather than the
// server, then reactions, what everybody else added to all of it.
//
// The reaction row is drawn only when there is one, so the permission it needs —
// whether the chips answer a click — is paid by the few messages that carry one
// rather than by every row on the page. Adding the first is offered from the
// hover actions, which read permissions when the pointer arrives.
//
// It is also the one part of the row that is repainted in place: the box holding
// the chips is kept, so a reaction arriving does not rebuild everything above it.
func (w *MessageWidget) buildExtras() []fyne.CanvasObject {
	deps, message, onMenu := w.deps, w.message, w.TappedSecondary

	var extras []fyne.CanvasObject

	if len(message.Attachments) > 0 {
		extras = append(extras, buildAttachments(deps, message.Attachments, onMenu))
	}
	if len(message.Embeds) > 0 {
		extras = append(extras, buildEmbeds(deps, message.Embeds, onMenu))
	}
	if len(w.codes) > 0 {
		extras = append(extras, buildInvites(deps, w.codes))
	}
	if len(message.Reactions) > 0 {
		w.reactionRow = container.NewStack(w.newReactions(message))
		extras = append(extras, w.reactionRow)
	}

	return extras
}

// newReactions is the chip row as this widget draws it, the one place the four
// things buildReactions needs are gathered — construction and SetReactions must
// not disagree about any of them.
func (w *MessageWidget) newReactions(message *domain.Message) fyne.CanvasObject {
	return buildReactions(w.deps, message, w.canReact(w.permissions()), w.TappedSecondary, w.setOverChild)
}

// SetReactions redraws the chip row from message, leaving the rest of the row —
// the parsed body, its pictures, an open hover — exactly as it stands. It answers
// false when there is no chip row to redraw: a message gaining its first reaction
// or losing its last changes whether the box under the body exists at all, which
// is a rebuild.
//
// The chips are built from the new message, never the old: each closes over the
// count and the emoji it was drawn with, and a chip toggling against a stale
// count is a chip that undoes somebody else's reaction.
func (w *MessageWidget) SetReactions(message *domain.Message) bool {
	if w.reactionRow == nil || len(message.Reactions) == 0 {
		return false
	}

	w.message = message
	w.reactionRow.Objects = []fyne.CanvasObject{w.newReactions(message)}
	w.reactionRow.Refresh()

	return true
}

// setOverChild is the hook every child that takes hover for itself reports
// through — see overChild.
func (w *MessageWidget) setOverChild(hovering bool) {
	w.overChild = hovering
	w.updateHover()
}

// buildMessageHeader is the author line — bold name in its role colour, then the
// timestamp — above the message text. Keeping the time on the name line stops long
// body text running under it, and both being siblings in the HBox, the smaller one
// centres itself against the name.
//
// The name is followed by whatever says who is posting — nothing for a person,
// the bot mark for an account Revolt marks as one, the webhook mark for a name an
// integration posted under. It sits with the name rather than with the notes
// after the time, being part of who wrote this rather than a note about it.
//
// An edited or pinned message trails the same line — the edit mark first, being
// about when, then the pin event's own mark — both in the timestamp's colour: a
// note about the message rather than part of what was said. A bare image, not a
// widget, so the row's hover and menu pass through it.
func (w *MessageWidget) buildMessageHeader(timestamp string, pinned bool, edited, body fyne.CanvasObject) fyne.CanvasObject {
	ts := newText(timestamp, theme.Colors.TimestampText, theme.Sizes.MessageTimestampSize)

	line := []fyne.CanvasObject{w.authorText, w.authorMarks, HorizontalSpacer(theme.Sizes.MessageContentPadding), ts}
	if edited != nil {
		line = append(line, HorizontalSpacer(theme.Sizes.MessageContentPadding), edited)
	}
	if pinned {
		side := theme.Sizes.MessagePinMarkSize
		mark := newScaledIcon(tintedIcon(assets.SystemPinnedIcon, theme.Colors.TimestampText), side)
		line = append(line, HorizontalSpacer(theme.Sizes.MessageAttachmentSpacing), container.NewCenter(mark))
	}

	w.authorLine = HBoxNoSpacing(line...)

	return VBoxNoSpacing(w.authorLine, body)
}

/* The edit mark */

// buildEditMark is the note an edited message trails: a pencil, then how long ago
// it was changed. Nil when the message has never been edited, which is what keeps
// it off the header line of nearly every row.
func (w *MessageWidget) buildEditMark() fyne.CanvasObject {
	if w.message.Edited == nil {
		return nil
	}

	side := theme.Sizes.MessageEditMarkSize
	mark := newScaledIcon(tintedIcon(assets.ActionEditIcon, theme.Colors.TimestampText), side)
	w.editMark = newText(util.ShortAgo(*w.message.Edited), theme.Colors.TimestampText, theme.Sizes.MessageTimestampSize)

	w.editMarkRow = HBoxNoSpacing(
		container.NewCenter(mark),
		HorizontalSpacer(theme.Sizes.MessageAttachmentSpacing),
		w.editMark,
	)

	return w.editMarkRow
}

// RefreshEditMark rewrites the span on the mark, reporting whether the row
// carries one at all. The clock belongs to the caller: nothing else redraws a
// message that has stopped changing, so a mark written as "just now" would go on
// saying it for as long as the row stays mounted.
func (w *MessageWidget) RefreshEditMark() bool {
	if w.editMark == nil || w.message.Edited == nil {
		return false
	}

	span := util.ShortAgo(*w.message.Edited)
	if span == w.editMark.Text {
		return true
	}

	// The span is a different width, so the row is laid out again rather than only
	// repainted — canvas.Text's own Refresh does not re-run the box around it.
	w.editMark.Text = span
	Relayout(w.editMarkRow)

	return true
}

/* Day separator */

// newDaySeparator is the divider announcing a new day: the day's name, a hairline
// out to the right edge, inset to a message row's horizontal padding.
func newDaySeparator(label string) fyne.CanvasObject {
	text := newBoldText(label, theme.Colors.DaySeparatorText, theme.Sizes.DaySeparatorTextSize)

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

// daySeparatorLayout gives the label its minimum and the rule the rest, both
// centred so the hairline meets the middle of the text. Two children, label first.
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

/* Status line */

// NewMessageStatus is the line the column draws in place of messages. A nil mark
// draws the sentence alone: what earns one is a state the reader is looking at
// rather than waiting on.
func NewMessageStatus(mark fyne.Resource, text string) fyne.CanvasObject {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	if mark == nil {
		return container.NewCenter(label)
	}

	side := theme.Sizes.MessageStatusMarkSize
	icon := newScaledIcon(tintedIcon(mark, theme.Colors.MessageStatusMark), side)

	// Boxed at its own size first: a row hands every child the full height, so an
	// unboxed image draws at the label's padding rather than the size asked for.
	box := container.NewCenter(container.NewGridWrap(fyne.NewSize(side, side), icon))
	row := HBoxNoSpacing(box, HorizontalSpacer(theme.Sizes.MessageStatusGap), label)

	return container.NewCenter(row)
}

/* Channel topic */

// ChannelTopic is what a channel says it is for, drawn after its name in the
// message header behind a rule. Shortened rather than wrapped — the header is one
// line and shares it with the buttons at its other end — so it belongs in a slot
// that hands it real width, which is that header's centre.
//
// Hidden when there is no topic: a rule with nothing after it reads as something
// that failed to load.
type ChannelTopic struct {
	widget.BaseWidget

	text    *canvas.Text
	box     *fyne.Container // the ellipsis box around text, re-labelled by Set
	content fyne.CanvasObject
}

var _ fyne.Widget = (*ChannelTopic)(nil)

// NewChannelTopic builds an empty, hidden topic. Set gives it something to say.
func NewChannelTopic() *ChannelTopic {
	w := &ChannelTopic{text: newText("", theme.Colors.ChannelTopicText, theme.Sizes.ChannelTopicSize)}
	w.box = NewEllipsisText(w.text)

	gap := theme.Sizes.ChannelTopicGap
	rule := container.NewCenter(hairline(theme.Sizes.OutlineWidth, theme.Sizes.ChannelTopicRuleHeight))

	w.content = NewFillRow(3, HorizontalSpacer(gap), rule, HorizontalSpacer(gap), w.box)

	w.Hide()
	w.ExtendBaseWidget(w)

	return w
}

func (w *ChannelTopic) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

// Set says what the channel is for, or hides the strip when it says nothing. The
// topic is flattened to one line however many were typed into it: a header two
// rows tall is what a newline would otherwise cost. Call on the UI thread.
func (w *ChannelTopic) Set(topic string) {
	topic = strings.Join(strings.Fields(topic), " ")
	if topic == "" {
		w.Hide()
		return
	}

	SetEllipsisText(w.box, topic)
	w.Show()
}

/* Reply previews */

// buildReplyBlock stacks the quoted lines above the message answering them,
// ending in the gap between the two. The lines come back as well as being mounted:
// what one quotes may not be cached, and resolving it is a request.
func buildReplyBlock(deps Deps, message *domain.Message, onMenu func(*fyne.PointEvent)) (fyne.CanvasObject, []*replyPreview) {
	previews := make([]*replyPreview, 0, len(message.Replies))
	quotes := make([]fyne.CanvasObject, 0, len(message.Replies)+1)

	for _, replyID := range message.Replies {
		preview := newReplyPreview(deps, message.ChannelID, replyID, onMenu)
		previews = append(previews, preview)
		quotes = append(quotes, preview.row)
	}

	return VBoxNoSpacing(append(quotes, VerticalSpacer(theme.Sizes.MessageReplyBlockGap))...), previews
}

// newReplyLine is the elbow tying a quoted line to the message answering it: a
// leg in the avatar gutter, an arm running right to the quote. Every quoted line
// carries its own, so a stack reads as separate answers rather than one bracket.
func newReplyLine() fyne.CanvasObject {
	leg := canvas.NewRectangle(theme.Colors.ReplyLine)
	arm := canvas.NewRectangle(theme.Colors.ReplyLine)

	return container.New(&replyLineLayout{}, leg, arm)
}

// replyLineLayout spans the avatar gutter and the gap after it, so the quote
// starts where the body below does. It reports no height — the quoted line decides
// the row. Two children, leg first.
type replyLineLayout struct{}

func (l *replyLineLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}
	leg, arm := objects[0], objects[1]

	thickness := theme.Sizes.MessageReplyLineThickness
	x := theme.Sizes.MessageReplyLineInset

	// The arm sits on the quoted line's centre; the leg hangs from the corner to the
	// foot of the row, pointing at the message the quote belongs to.
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

// replyPreview is the quoted line above a message that answers another. A struct
// rather than a built subtree because what it quotes may arrive later: a reply
// reaches as far back as somebody cared to answer, and the cache is only the tail
// of a channel.
type replyPreview struct {
	// row is the whole line, kept so filling one in can re-lay it out — the name
	// sits inside it and everything after moves.
	row *fyne.Container

	channelID string
	messageID string

	avatar  *fyne.Container
	author  *canvas.Text
	content *canvas.Text

	// marks is the author mark's slot, filled once and empty for a person — the
	// header's arrangement, for the same reason: what a quote names can resolve
	// after the line is mounted.
	marks *fyne.Container
}

func newReplyPreview(deps Deps, channelID, messageID string, onMenu func(*fyne.PointEvent)) *replyPreview {
	p := &replyPreview{channelID: channelID, messageID: messageID}

	size := fyne.NewSize(replyPreviewAvatarSize, replyPreviewAvatarSize)
	p.avatar, _ = newAvatarSlot(size)

	p.author = newBoldText("", theme.Colors.TextPrimary, replyPreviewTextSize)
	p.content = newText("", theme.Colors.TimestampText, replyPreviewTextSize)
	p.marks = HBoxNoSpacing()

	// A line that found nothing leads nowhere: everything a mounted reply names has
	// been asked for by the time it is drawn, so one still unresolved was deleted.
	quote := NewTappableContainer(HBoxNoSpacing(
		container.NewCenter(p.avatar),
		HorizontalSpacer(8),
		container.NewCenter(p.author),
		p.marks,
		HorizontalSpacer(5),
		container.NewCenter(p.content),
	), func() {
		if p.Resolved(deps) {
			deps.Actions.OnJumpToMessage(channelID, messageID)
		}
	})
	quote.onSecondaryTap = onMenu

	// The elbow indents the quote to the content column and draws the line down. The
	// row's horizontal margin is already applied around the block.
	p.row = HBoxNoSpacing(newReplyLine(), quote)
	p.set(deps)

	return p
}

// Resolved reports whether the line is quoting a message rather than saying it
// found none. The controller asks so it knows which targets to fetch.
func (p *replyPreview) Resolved(deps Deps) bool {
	return deps.Actions.ResolveMessage(p.channelID, p.messageID) != nil
}

// Refresh re-reads the quoted message and re-lays the line out, for a target
// that resolved after the widget was mounted.
func (p *replyPreview) Refresh(deps Deps) {
	p.set(deps)
	p.author.Refresh()
	p.content.Refresh()
	Relayout(p.row)
}

func (p *replyPreview) set(deps Deps) {
	author, content := resolveReply(deps, p.channelID, p.messageID)

	p.author.Text = author.Name
	p.content.Text = content
	p.setMark(author.Mark)
	loadAvatar(deps.Images, p.avatar, author.AvatarURL, fyne.NewSize(replyPreviewAvatarSize, replyPreviewAvatarSize))
}

// setMark fills the slot after the quoted name, once — MessageWidget's own, at
// the quote's smaller size.
func (p *replyPreview) setMark(mark domain.AuthorMark) {
	if len(p.marks.Objects) > 0 {
		return
	}

	glyph := NewAuthorMark(mark, theme.Sizes.ReplyAuthorMarkSize)
	if glyph == nil {
		return
	}

	p.marks.Add(HorizontalSpacer(4))
	p.marks.Add(glyph)
}

// resolveReply looks up a referenced message: its author, with the name already
// shortened to what a quote has room for, and its flattened content. A missing
// reference yields a nameless author and says so in the content — the caller
// draws the same line either way.
func resolveReply(deps Deps, channelID, messageID string) (author domain.Author, content string) {
	message := deps.Actions.ResolveMessage(channelID, messageID)
	if message == nil {
		return domain.Author{}, "Unknown message reference"
	}

	author = deps.Store.MessageAuthor(message)
	author.Name = util.Truncate(author.Name, maxReplyUsernameLength)

	// Flattened rather than shown raw: a quote is one line, and the asterisks and
	// newlines the source carries would either read literally or break the row.
	// Named, so a reply to a message that is only emoji quotes something.
	flat := markdown.DocumentTextNamed(markdown.Parse(message.Content), deps.Store.EmojiName)

	return author, util.Truncate(flat, maxReplyPreviewLength)
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

	wrap         wrapMeter
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

// MinSize grows the entry as the composer's does.
func (e *EditEntry) MinSize() fyne.Size { return composerMinSize(&e.Entry, &e.wrap) }

// Resize places the caret at the end on the first real size. Set in the
// constructor it lands against zero-width word-wrapped row bounds — one rune per
// visual row — which clamps it a character in from the start.
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
		// No refresh after: the entry's own TypedKey ends in one, and a second
		// is a second re-wrap per keystroke — MessageInput's reasoning.
		e.Entry.TypedKey(key)
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
}
