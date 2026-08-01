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

// hoverHideDelay debounces the transition between the message body and the
// floating action buttons so the buttons don't flicker.
const hoverHideDelay = 50 * time.Millisecond

// MessageWidget renders a single chat message with a hover state that reveals
// quick-action buttons.
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle

	// authorText and avatar are retained so a message whose author resolves after
	// the widget is mounted (lazy per-author fetch) can be updated in place via
	// RefreshAuthor, instead of rebuilding the whole channel. Both are nil for a
	// grouped continuation message, which draws neither a name nor an avatar.
	authorText *canvas.Text
	avatar     *Avatar

	// gutterTimestamp is the small left-gutter time shown on a grouped continuation
	// message (in place of the avatar), revealed on hover. nil for a full message.
	gutterTimestamp *canvas.Text

	// bottomSpacer is the message's bottom margin, kept so SetFollowedByGroup can
	// tighten it when a continuation is appended directly beneath this message.
	bottomSpacer *canvas.Rectangle

	// daySeparator is the dated divider drawn above the first message of a
	// calendar day, nil for every other message. It lives on the widget rather
	// than as its own list entry so the mounted window stays one object per
	// message: whoever ends up first on its day carries the separator, and every
	// path that re-evaluates a message against its new predecessor (prepend seams,
	// deletions, edits) re-derives it for free.
	daySeparator fyne.CanvasObject

	// bodySlot holds the rendered message body; StartEdit swaps it for an
	// in-place editor and CancelEdit restores body, leaving the header,
	// attachments, and replies untouched.
	bodySlot *fyne.Container
	body     fyne.CanvasObject
	editing  bool

	// The hover quick-actions are built lazily on first reveal (ensureActions):
	// the ~3 buttons and their icons aren't constructed for messages the pointer
	// never touches. deps/message are retained to build them later;
	// actionsOverlay holds them once built and is empty until then.
	deps           Deps
	message        *revoltgo.Message
	actionsOverlay *fyne.Container
	actions        *fyne.Container

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
// continuation of the previous one from the same author (Discord/Stoat-style):
// it omits the avatar and name header and instead shows a small, hover-revealed
// timestamp in the avatar gutter. Vertical spacing is asymmetric: a head/standalone
// carries the full gap above it while a continuation carries only a tight gap, and
// followedByGroup tightens the bottom margin so a head sits flush against the
// continuations beneath it without changing the gap between separate groups.
//
// A non-empty dayLabel means this message opens a new calendar day, and the named
// day separator is drawn above it.
func NewMessageWidget(deps Deps, message *revoltgo.Message, dayLabel string, grouped, followedByGroup bool) *MessageWidget {
	w := &MessageWidget{
		background: canvas.NewRectangle(color.Transparent),
		deps:       deps,
		message:    message,
	}

	text := message.Content
	if message.System != nil {
		text = util.FormatSystemMessage(deps.Session, message.System)
	}

	var shortTime, fullTime string
	if t, err := util.Timestamp(message.ID); err == nil {
		shortTime, fullTime = util.ShortTime(t), util.NiceTime(t)
	}

	w.body = NewFlushContainer(renderMessageBody(deps, text))
	w.bodySlot = container.NewStack(w.body)

	var leftColumn, body fyne.CanvasObject
	if grouped {
		// Transparent until hover; toggling colour (not visibility) keeps the
		// gutter's width fixed so the body never shifts when the time appears. The
		// gutter reports zero height so it never makes the continuation row taller
		// than its single line of text.
		w.gutterTimestamp = canvas.NewText(shortTime, color.Transparent)
		w.gutterTimestamp.TextSize = theme.Sizes.MessageTimestampSize
		gutter := &GutterLayout{Width: theme.Sizes.MessageAvatarColumnWidth, TopOffset: theme.Sizes.MessageTimestampTopOffset}
		leftColumn = container.New(gutter, w.gutterTimestamp)
		body = buildGroupedContent(deps, message, w.bodySlot)
	} else {
		name, nameColor, avatarID, avatarURL := resolveAuthor(deps, message)
		w.avatar = NewAvatar(deps.Images, avatarID, avatarURL, func() {
			if deps.Actions != nil {
				deps.Actions.OnAvatarTapped(message.Author)
			}
		})
		column := &FixedWidthColumnLayout{Width: theme.Sizes.MessageAvatarColumnWidth, TopAlign: true}
		leftColumn = container.New(column, w.avatar)

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

	w.actionsOverlay = container.New(&OverlayLayout{YOffset: -16, RightOffset: 6})
	messageRow := container.NewStack(inner, w.actionsOverlay)

	if dayLabel != "" {
		w.daySeparator = NewDaySeparator(dayLabel)
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

// Editing reports whether the message is in in-place edit mode.
func (w *MessageWidget) Editing() bool { return w.editing }

// SetFollowedByGroup tightens (or restores) the bottom margin when a same-author
// continuation is appended directly beneath this message after it was mounted.
func (w *MessageWidget) SetFollowedByGroup(followed bool) {
	w.bottomSpacer.SetMinSize(fyne.NewSize(0, verticalPad(followed)))
	w.bottomSpacer.Refresh()
	w.Refresh()
}

// RefreshAuthor re-resolves the author's name, role colour, and avatar and
// applies them in place. Called when a previously-unknown author is fetched, or
// a member updates, after the widget was mounted — avoiding a full re-render of
// the channel. A grouped continuation shows neither name nor avatar, so there's
// nothing to update.
func (w *MessageWidget) RefreshAuthor() {
	if w.authorText == nil {
		return
	}
	name, nameColor, avatarID, avatarURL := resolveAuthor(w.deps, w.message)
	if w.authorText.Text != name || w.authorText.Color != nameColor {
		w.authorText.Text = name
		w.authorText.Color = nameColor
		w.authorText.Refresh()
	}
	w.avatar.SetSource(w.deps.Images, avatarID, avatarURL)
}

// --- permissions -------------------------------------------------------------

// isOwnMessage reports whether the message was authored by the logged-in user.
func (w *MessageWidget) isOwnMessage() bool {
	if w.deps.Session == nil {
		return false
	}
	self := w.deps.Session.State.Self()
	return self != nil && self.ID == w.message.Author
}

// canEdit reports whether the edit action should be offered: only your own
// regular messages (system messages have no editable content).
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

// --- quick actions and context menu ------------------------------------------

// buildActions creates the hidden, rounded group of quick-action buttons. The
// set is dynamic: reply is always offered, edit only on your own (non-system)
// message, and delete on your own message or where you can manage messages.
func (w *MessageWidget) buildActions() *fyne.Container {
	onHover := func(hovering bool) {
		w.overActions = hovering
		w.updateHover()
	}
	act := w.deps.Actions

	buttons := []fyne.CanvasObject{
		NewIconButton(fynetheme.MailReplyIcon(), func() {
			if act != nil {
				act.OnReply(w.message)
			}
		}, onHover),
	}

	if w.canEdit() {
		buttons = append(buttons, NewIconButton(fynetheme.DocumentCreateIcon(), func() {
			if act != nil {
				act.OnEdit(w.message)
			}
		}, onHover))
	}
	if w.canDelete() {
		buttons = append(buttons, NewIconButton(fynetheme.DeleteIcon(), func() {
			if act != nil {
				act.OnDelete(w.message)
			}
		}, onHover))
	}

	// Overflow button: always last, opens the full context menu (the same one
	// right-clicking the message shows) beneath itself.
	more := NewIconButton(fynetheme.MoreVerticalIcon(), nil, onHover)
	more.onTap = func() { ShowContextMenu(more, w.menuItems(), AnchorBelow(more)) }
	buttons = append(buttons, more)

	group := container.NewStack(roundedPanel(), HBoxNoSpacing(buttons...))
	group.Hide()
	return group
}

// menuItems builds the message's context-menu entries, mirroring the hover
// quick-actions (reply/edit/delete, gated the same way) plus copy helpers. Used
// by both the overflow button and the right-click handler.
func (w *MessageWidget) menuItems() []*fyne.MenuItem {
	act := w.deps.Actions

	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Reply", fynetheme.MailReplyIcon(), func() {
			if act != nil {
				act.OnReply(w.message)
			}
		}),
	}
	if w.canEdit() {
		items = append(items, fyne.NewMenuItemWithIcon("Edit", fynetheme.DocumentCreateIcon(), func() {
			if act != nil {
				act.OnEdit(w.message)
			}
		}))
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
			fyne.NewMenuItemWithIcon("Delete", fynetheme.DeleteIcon(), func() {
				if act != nil {
					act.OnDelete(w.message)
				}
			}))
	}
	return items
}

// TappedSecondary opens the message context menu at the cursor on right-click.
func (w *MessageWidget) TappedSecondary(e *fyne.PointEvent) {
	ShowContextMenu(w, w.menuItems(), e.AbsolutePosition)
}

// --- in-place editing --------------------------------------------------------

// StartEdit swaps the message body for an in-place editor, with save/cancel
// buttons floating where the hover quick-actions normally appear. Enter
// (without shift) or the save button submits the new content through onSave;
// Escape or the cancel button calls onCancel. Saving unchanged or emptied
// content counts as a cancel. Returns the entry for the caller to focus, or
// nil when the message isn't editable or is already being edited.
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

	// The save/cancel pair replaces the hover quick-actions and stays visible
	// for the whole edit.
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

// --- hover -------------------------------------------------------------------

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
// the buttons, hiding them after a short grace period otherwise. Suspended
// while editing, which paints its own highlight and overlay buttons.
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
// them into the (until now empty) overlay. Subsequent reveals reuse them.
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

// setGutterShown reveals or hides the grouped continuation's gutter timestamp by
// toggling its colour (kept at a fixed width, so the body never shifts). A no-op
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

// --- content assembly --------------------------------------------------------

// resolveAuthor resolves the display name, role colour, and avatar for a
// message's author. Shared by widget construction and RefreshAuthor so a lazily
// fetched author renders identically whether it was known up front or filled in
// later.
func resolveAuthor(deps Deps, message *revoltgo.Message) (name string, nameColor color.Color, avatarID, avatarURL string) {
	author := util.MessageAuthor(deps.Session, message)
	nameColor = theme.Colors.TextPrimary
	if author.Color != nil {
		nameColor = author.Color
	}
	return author.Name, nameColor, util.IDFromAttachmentURL(author.AvatarURL), author.AvatarURL
}

// verticalPad returns a message's top or bottom margin: tight when it abuts a
// same-author continuation, the full gap otherwise (which separates groups).
func verticalPad(tight bool) float32 {
	if tight {
		return theme.Sizes.MessageGroupedVerticalPadding
	}
	return theme.Sizes.MessageVerticalPadding
}

// buildMessageContent assembles the author/text header plus any attachments.
func buildMessageContent(deps Deps, message *revoltgo.Message, author *canvas.Text, timestamp string, body fyne.CanvasObject) fyne.CanvasObject {
	header := buildMessageHeader(author, timestamp, body)
	if len(message.Attachments) == 0 {
		return header
	}
	return container.NewVBox(header, buildAttachments(deps, message.Attachments))
}

// buildGroupedContent renders a grouped continuation message: just the body
// (and any attachments), with no author/timestamp header.
func buildGroupedContent(deps Deps, message *revoltgo.Message, body fyne.CanvasObject) fyne.CanvasObject {
	if len(message.Attachments) == 0 {
		return body
	}
	return container.NewVBox(body, buildAttachments(deps, message.Attachments))
}

// buildMessageHeader renders the author line (the bold name in its role colour
// followed by a baseline-aligned timestamp) above the message text. Keeping the
// timestamp inline on the name line — rather than overlaid on the whole body —
// aligns it with the username and stops long body text from running under it.
func buildMessageHeader(author *canvas.Text, timestamp string, body fyne.CanvasObject) fyne.CanvasObject {
	ts := canvas.NewText(timestamp, theme.Colors.TimestampText)
	ts.TextSize = theme.Sizes.MessageTimestampSize
	// Drop the smaller timestamp so its baseline lines up with the bold name.
	tsAligned := VBoxNoSpacing(VerticalSpacer(theme.Sizes.MessageTimestampTopOffset), ts)

	nameLine := container.NewHBox(author, HorizontalSpacer(theme.Sizes.MessageContentPadding), tsAligned)
	return VBoxNoSpacing(nameLine, body)
}
