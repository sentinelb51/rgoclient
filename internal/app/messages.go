package app

import (
	"log"
	"os"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	initialPageSize = 30 // messages fetched when first opening a channel
	historyPageSize = 50 // older messages fetched (or re-mounted) per scroll-up

	// initialMountCount is how many messages a channel switch mounts. Far fewer
	// than the cache holds: only ~20 fit on screen, scrollback re-mounts the rest
	// from cache instantly, and every mounted widget is real work — rapid channel
	// switching churns widgets the renderer cache then holds for up to a minute,
	// so this directly bounds how fast memory can ratchet up.
	initialMountCount = 50

	// renderedCap is the ceiling on widgets kept mounted by live appends, and
	// mountedCap the ceiling during scrollback: prepends past it trim widgets off
	// the bottom and vice versa, so scrolling through any amount of history keeps
	// a constant number mounted.
	renderedCap = 200
	mountedCap  = renderedCap + historyPageSize

	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"
	remountThreshold  = 200 // px from the bottom before trimmed newer rows re-mount

	messageGroupWindow = 7 * time.Minute // max gap for a message to group under the previous
)

/* The message area */

// buildMessageArea builds the message list, header, and composer.
func (a *App) buildMessageArea() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	a.messageScroll = ui.NewObservableVScroll(a.messageList)
	a.messageScroll.OnScroll = func(pos fyne.Position) {
		if pos.Y <= 0 {
			a.loadMoreHistory()
			return
		}
		if a.messageList.MinSize().Height-a.messageScroll.Size().Height-pos.Y <= remountThreshold {
			a.mountNewerFromCache()
		}
	}
	a.clearMessages()

	a.input = ui.NewMessageInput(a.deps(), a.window)
	a.input.SetPlaceHolder("Send a message...")
	a.input.OnSubmit = a.handleSubmit
	a.input.OnEditLast = a.editLastOwnMessage
	a.input.RegisterDropHandler()

	// Floating composer dock: the mention picker, reply and attachment rows and the
	// entry stack inside one rounded card. Its fill is the entry's own input
	// background, so the entry's box disappears into it and the outline draws the
	// boundary instead — taking the accent on focus, the composer's only "you are
	// typing here" cue. The padding is thin because everything in the stack already
	// carries its own inset, and it goes through ui.NewInset because NewPadded and
	// Border would each add theme padding on top of what is asked for.
	dockBg := canvas.NewRectangle(theme.Colors.ComposerBg)
	dockBg.CornerRadius = theme.Sizes.ComposerRadius
	dockBg.StrokeColor = theme.Colors.ComposerBorder
	dockBg.StrokeWidth = 1
	a.input.OnFocusChanged = func(focused bool) {
		dockBg.StrokeColor = theme.Colors.ComposerBorder
		if focused {
			dockBg.StrokeColor = theme.Colors.ComposerBorderFocus
		}
		dockBg.Refresh()
	}

	inner := ui.VBoxNoSpacing(
		a.input.Mentions,
		a.input.ReplyContainer,
		a.input.AttachmentContainer,
		ui.WithCaret(a.input),
	)
	padV, padH := theme.Sizes.ComposerPaddingV, theme.Sizes.ComposerPaddingH
	dock := container.NewStack(dockBg, ui.NewInset(inner, padV, padV, padH, padH))

	a.channelHeader = widget.NewLabelWithStyle(a.channelName(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.channelGlyph = container.NewStack(ui.ChannelGlyph(a.currentChannel()))
	header := container.NewPadded(container.NewHBox(a.channelGlyph, a.channelHeader))

	layout := container.NewBorder(header, container.NewPadded(dock), nil, nil, a.messageScroll)
	return container.NewStack(background, layout)
}

/* Composing */

// handleSubmit sends the composed message, its attachments, and its replies. The
// composer is cleared immediately and the send runs in the background: the
// message appears when the gateway echoes it back.
func (a *App) handleSubmit(text string) {
	if (text == "" && len(a.input.Attachments) == 0) || a.currentChannelID == "" || a.session == nil {
		return
	}

	session := a.session
	channelID := a.currentChannelID
	attachments := slices.Clone(a.input.Attachments)
	replies := slices.Clone(a.input.Replies)

	a.input.SetText("")
	a.input.ClearAttachments()
	a.input.ClearReplies()
	a.jumpToLatest()

	go func() {
		send := revoltgo.MessageSend{
			Content:     text,
			Attachments: uploadAttachments(session, attachments),
			Replies:     toMessageReplies(replies),
		}
		if _, err := session.ChannelMessageSend(channelID, send); err != nil {
			log.Printf("send message: %v", err)
		}
	}()
}

// uploadAttachments uploads each local file and returns the resulting IDs. A file
// that fails to open or upload is logged and skipped, so one bad attachment
// doesn't sink the whole message.
func uploadAttachments(session *revoltgo.Session, attachments []ui.Attachment) []string {
	ids := make([]string, 0, len(attachments))

	for _, attachment := range attachments {
		file, err := os.Open(attachment.Path)
		if err != nil {
			log.Printf("open attachment %s: %v", attachment.Path, err)
			continue
		}

		uploaded, err := session.AttachmentUpload(&revoltgo.FileParams{Name: attachment.Name, Reader: file})
		_ = file.Close()
		if err != nil {
			log.Printf("upload attachment %s: %v", attachment.Name, err)
			continue
		}
		ids = append(ids, uploaded.ID)
	}

	return ids
}

// toMessageReplies converts composer replies to the API representation.
func toMessageReplies(replies []ui.Reply) []*revoltgo.MessageReplies {
	out := make([]*revoltgo.MessageReplies, len(replies))
	for i, r := range replies {
		out[i] = &revoltgo.MessageReplies{ID: r.ID, Mention: r.Mention}
	}

	return out
}

/* Widget construction */

// newMessageWidget builds a message widget, drawing curr as a grouped
// continuation of prev when they belong together, tightening its bottom margin
// when next continues it, and heading it with a day separator when it opens a new
// calendar day.
//
// Every mount path funnels through here, so this is also where an unresolved
// author is chased down: whether the message came from the gateway, the initial
// page, or scrollback, the widget can't render a name until State knows the user.
// ensureAuthor is a no-op once it does, so this costs two map lookups per widget
// in the common case.
func (a *App) newMessageWidget(prev, curr, next *revoltgo.Message) *ui.MessageWidget {
	if curr.System == nil && curr.Webhook == nil {
		a.ensureAuthor(a.channelServerID(curr.Channel), curr.Author)
	}

	return ui.NewMessageWidget(a.deps(), curr, dayLabel(prev, curr),
		continuesGroup(prev, curr), continuesGroup(curr, next))
}

// dayLabel returns the day separator label for curr — "" when it belongs to the
// same calendar day as the message above it. A message with no predecessor is
// treated as opening its day, so loaded history always starts with a date;
// prepending older messages rebuilds that row, dropping the label if the day
// continues.
func dayLabel(prev, curr *revoltgo.Message) string {
	ct, err := util.Timestamp(curr.ID)
	if err != nil {
		return ""
	}

	if prev != nil {
		if pt, err := util.Timestamp(prev.ID); err == nil && util.SameDay(pt, ct) {
			return ""
		}
	}

	return util.DayLabel(ct)
}

// continuesGroup reports whether curr should render as a continuation of prev:
// same author, neither a system/webhook/masqueraded message, on the same calendar
// day, and within messageGroupWindow. A reply always starts a fresh group, and so
// does a message on the far side of a day separator — the separator has to break
// the group, or a pair minutes apart across midnight would render as one
// headerless block.
func continuesGroup(prev, curr *revoltgo.Message) bool {
	if prev == nil || curr == nil || curr.Author == "" || prev.Author != curr.Author {
		return false
	}
	if curr.System != nil || prev.System != nil ||
		curr.Webhook != nil || prev.Webhook != nil ||
		curr.Masquerade != nil || prev.Masquerade != nil {
		return false
	}
	if len(curr.Replies) > 0 {
		return false
	}

	pt, errPrev := util.Timestamp(prev.ID)
	ct, errCurr := util.Timestamp(curr.ID)
	if errPrev != nil || errCurr != nil || !util.SameDay(pt, ct) {
		return false
	}

	gap := ct.Sub(pt)
	return gap >= 0 && gap <= messageGroupWindow
}

// setChannelGlyph repoints the message header's prefix mark at the open
// channel's type, so a DM reads "@name" rather than "#name". Call on the UI
// thread; a nil channel falls back to the hashtag.
func (a *App) setChannelGlyph(channel *revoltgo.Channel) {
	if a.channelGlyph == nil {
		return
	}

	a.channelGlyph.Objects = []fyne.CanvasObject{ui.ChannelGlyph(channel)}
	a.channelGlyph.Refresh()
}

/* Loading and rendering */

// loadChannelMessages fetches the newest page of messages for a channel.
func (a *App) loadChannelMessages(channelID string) {
	a.messages.SetDepleted(channelID, false)

	session := a.session
	if session == nil {
		return
	}

	go func() {
		page, err := session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			IncludeUsers: true,
			Limit:        initialPageSize,
		})
		if err != nil {
			a.doOnUI(func() {
				if a.currentChannelID == channelID {
					a.showStatus("Failed to load messages")
				}
			}, true)
			return
		}

		if len(page.Messages) == 0 {
			a.doOnUI(func() {
				if a.currentChannelID == channelID {
					a.showStatus("No messages in this channel")
					a.messages.SetDepleted(channelID, true)
				}
			}, true)
			return
		}

		a.messages.Set(channelID, page.Messages)
		a.doOnUI(func() {
			// Render from the cache, not from the page we just stored: a gateway
			// message can land between the two, and the cache is the one view that
			// already includes it.
			if a.currentChannelID == channelID {
				a.displayCached()
			}
		}, true)
	}()
}

// displayCached re-renders the open channel from its cached messages.
func (a *App) displayCached() {
	a.displayMessages(a.messages.Get(a.currentChannelID))
}

// displayMessages renders the newest initialMountCount messages, oldest first,
// and scrolls to the bottom. Older cached messages are re-mounted on scrollback
// by loadMoreHistory's cache tier. Call on the UI thread.
func (a *App) displayMessages(messages []*revoltgo.Message) {
	a.cancelActiveEdit()
	if len(messages) > initialMountCount {
		messages = messages[len(messages)-initialMountCount:]
	}

	widgets := make([]fyne.CanvasObject, len(messages))
	for i, message := range messages {
		var prev, next *revoltgo.Message
		if i > 0 {
			prev = messages[i-1]
		}
		if i+1 < len(messages) {
			next = messages[i+1]
		}
		widgets[i] = a.newMessageWidget(prev, message, next)
	}

	a.messageList.Objects = widgets
	a.messageList.Refresh()
	a.scrollToBottom()
}

// showStatus replaces the message list with a single centred line.
func (a *App) showStatus(text string) {
	a.cancelActiveEdit()
	label := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	height := float32(400)
	if a.messageScroll != nil {
		if h := a.messageScroll.Size().Height - 5; h > 100 {
			height = h
		}
	}

	a.messageList.Objects = []fyne.CanvasObject{ui.NewMinHeightContainer(height, container.NewCenter(label))}
	a.messageList.Refresh()

	// The inner list refresh alone doesn't repaint the scroll viewport until an
	// event forces relayout, so the status would only appear after a scroll.
	if a.messageScroll != nil {
		a.messageScroll.ScrollToTop()
		a.messageScroll.Refresh()
	}
}

// clearMessages empties the message list.
func (a *App) clearMessages() {
	a.cancelActiveEdit()
	a.messageList.Objects = nil
	a.messageList.Refresh()
	a.scrollToBottom()
}

// jumpToLatest brings the view back to the newest message: a plain scroll when
// the live tail is mounted, a re-render when scrollback has trimmed it away.
func (a *App) jumpToLatest() {
	cached := a.messages.Get(a.currentChannelID)
	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)

	if len(cached) == 0 || (bottom != nil && bottom.ID == cached[len(cached)-1].ID) {
		a.scrollToBottom()
		return
	}

	a.displayCached()
}

// removeMessage unmounts a deleted message, re-evaluating its neighbours'
// grouping — a continuation whose group head was deleted regains its header.
// Call on the UI thread.
func (a *App) removeMessage(channelID, messageID string) {
	if channelID != a.currentChannelID {
		return
	}

	i := a.messageWidgetIndex(messageID)
	if i == -1 {
		return
	}
	if a.editing != nil && a.editing.Message().ID == messageID {
		a.editing = nil // the editor unmounts with its widget
	}

	// slices.Delete zeroes the vacated tail slot, so the unmounted widget isn't
	// kept alive by the list's backing array.
	a.messageList.Objects = slices.Delete(a.messageList.Objects, i, i+1)

	prev, next := a.mountedMessage(i-1), a.mountedMessage(i)
	if next != nil {
		a.messageList.Objects[i] = a.newMessageWidget(prev, next, a.mountedMessage(i+1))
	}
	if i > 0 {
		if w, ok := a.messageList.Objects[i-1].(*ui.MessageWidget); ok {
			w.SetFollowedByGroup(continuesGroup(prev, next))
		}
	}

	a.messageList.Refresh()
}

// refreshMessage rebuilds an edited message's widget in place from its updated
// cache entry. A message the user is editing is left alone — the rebuild would
// discard their open editor, and the cache already holds the remote update, so
// the next rebuild renders it. Call on the UI thread.
func (a *App) refreshMessage(channelID, messageID string) {
	if channelID != a.currentChannelID {
		return
	}
	if a.editing != nil && a.editing.Message().ID == messageID {
		return
	}

	i := a.messageWidgetIndex(messageID)
	if i == -1 {
		return
	}

	message := a.messages.Find(channelID, messageID)
	if message == nil {
		return
	}

	a.messageList.Objects[i] = a.newMessageWidget(a.mountedMessage(i-1), message, a.mountedMessage(i+1))
	a.messageList.Refresh()
}

// editLastOwnMessage opens the in-place editor on the user's newest editable
// message in the open channel, triggered by Up in an empty composer. It scans the
// cache rather than tracking "last sent" state: the cache only ever gains own
// messages through the gateway echo, so the scan can't race the send path.
func (a *App) editLastOwnMessage() {
	if a.session == nil || a.currentChannelID == "" {
		return
	}

	self := a.session.State.Self()
	if self == nil {
		return
	}

	cached := a.messages.Get(a.currentChannelID)
	for i := len(cached) - 1; i >= 0; i-- {
		message := cached[i]
		if message.Author == self.ID && message.System == nil && message.Content != "" {
			a.OnEdit(message)
			return
		}
	}
}

/* The mounted window */

// Which slice of a channel's cached messages currently has live widgets, and how
// that window slides as the user scrolls. displayMessages opens it at the live
// tail; everything below moves it.
//
// Invariants:
//   - Widgets are mounted oldest-first, matching the cache's own order.
//   - The window never exceeds mountedCap widgets, in either scroll direction.
//   - Scrolling down never needs the network: trimming only ever drops a
//     channel's oldest messages, so everything below the window is still cached.

// mountedMessage returns the message rendered by the widget at index i, or nil
// when i is out of range or not a message widget (the list also holds status
// lines).
func (a *App) mountedMessage(i int) *revoltgo.Message {
	if i < 0 || i >= len(a.messageList.Objects) {
		return nil
	}
	if w, ok := a.messageList.Objects[i].(*ui.MessageWidget); ok {
		return w.Message()
	}

	return nil
}

// messageWidgetIndex returns the index of the mounted widget rendering messageID,
// or -1.
func (a *App) messageWidgetIndex(messageID string) int {
	for i, obj := range a.messageList.Objects {
		if w, ok := obj.(*ui.MessageWidget); ok && w.Message().ID == messageID {
			return i
		}
	}

	return -1
}

// appendMessage adds a freshly received message, trimming the oldest widget when
// over the render cap and keeping the scroll position stable. prev is the
// message's predecessor in its channel, captured when the message was cached.
func (a *App) appendMessage(message, prev *revoltgo.Message) {
	if a.currentChannelID == "" {
		return
	}

	// A status line may be showing; the first real message replaces it.
	if len(a.messageList.Objects) == 1 && a.mountedMessage(0) == nil {
		a.messageList.Objects = nil
	}

	// When scrollback has trimmed the newest widgets the view is detached from the
	// live tail: don't mount, since the predecessor isn't on screen and the row
	// would render against the wrong neighbour. The message is cached and mounts
	// via mountNewerFromCache on the way back down.
	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)
	if bottom != nil && prev != nil && bottom.ID != prev.ID {
		return
	}

	contentHeight := a.messageList.MinSize().Height
	viewHeight := a.messageScroll.Size().Height
	atBottom := contentHeight-viewHeight-a.messageScroll.Offset.Y < atBottomTolerance

	// When this message continues the one above it, tighten that message's bottom
	// margin so the group reads as a block.
	if continuesGroup(prev, message) {
		a.tightenBottomWidget()
	}
	a.messageList.Add(a.newMessageWidget(prev, message, nil))

	if over := len(a.messageList.Objects) - renderedCap; over > 0 {
		a.trimMountedTop(over, atBottom)
	}

	a.messageList.Refresh()
	if atBottom {
		a.scrollToBottom()
	} else {
		a.messageScroll.Refresh()
	}
}

// loadMoreHistory mounts older messages when the user scrolls to the top. It is
// two-tier: messages already cached but not mounted prepend synchronously; past
// that, an older page comes from the network. Both tiers anchor on the oldest
// mounted message, so cache trimming can never cause a refetch loop.
func (a *App) loadMoreHistory() {
	channelID := a.currentChannelID
	if a.loadingHistory || channelID == "" {
		return
	}

	top := a.mountedMessage(0)
	if top == nil {
		return
	}

	cached := a.messages.Get(channelID)
	if i, ok := slices.BinarySearchFunc(cached, top.ID, cache.CompareMessageID); ok && i > 0 {
		a.prependMessages(cached[max(0, i-historyPageSize):i])
		return
	}

	session := a.session
	if session == nil || a.messages.IsDepleted(channelID) {
		return
	}
	a.loadingHistory = true

	go func() {
		defer a.doOnUI(func() { a.loadingHistory = false }, true)

		page, err := session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			Before:       top.ID,
			Limit:        historyPageSize,
			IncludeUsers: true,
		})
		if err != nil || len(page.Messages) == 0 {
			if err == nil {
				a.messages.SetDepleted(channelID, true)
			}
			return
		}

		older := a.messages.Prepend(channelID, page.Messages)
		a.doOnUI(func() {
			if a.currentChannelID == channelID && a.mountedMessage(0) == top {
				a.prependMessages(older)
			}
		}, true)
	}()
}

// prependMessages mounts older messages, oldest first, above the current view,
// preserving the scroll position and trimming the bottom past mountedCap.
func (a *App) prependMessages(older []*revoltgo.Message) {
	if len(older) == 0 {
		return
	}
	oldHeight := a.messageList.MinSize().Height

	// The newest prepended message lands directly above the previously topmost
	// message, so that existing row is each one's neighbour at the seam.
	topMessage, topNext := a.mountedMessage(0), a.mountedMessage(1)

	// The oldest message has no loaded predecessor, so it renders as a full
	// message; every other one sees its true neighbours for grouping.
	widgets := make([]fyne.CanvasObject, len(older))
	for i, msg := range older {
		var prev, next *revoltgo.Message
		if i > 0 {
			prev = older[i-1]
		}
		if i+1 < len(older) {
			next = older[i+1]
		} else {
			next = topMessage
		}
		widgets[i] = a.newMessageWidget(prev, msg, next)
	}

	// The previously-topmost message now has a predecessor above it, so re-evaluate
	// its grouping; its successor is unchanged.
	if topMessage != nil {
		a.messageList.Objects[0] = a.newMessageWidget(older[len(older)-1], topMessage, topNext)
	}

	a.messageList.Objects = append(widgets, a.messageList.Objects...)
	a.messageList.Refresh()

	if diff := a.messageList.MinSize().Height - oldHeight; diff > 0 {
		a.messageScroll.Offset.Y += diff
		a.messageScroll.Refresh()
	}
	a.trimMountedBottom()
}

// mountNewerFromCache re-mounts cached messages below the bottom-most mounted one
// — the downward counterpart of loadMoreHistory's cache tier. It never needs the
// network: trimming only ever drops a channel's oldest messages, so everything
// below the mounted window is always cached.
func (a *App) mountNewerFromCache() {
	if a.currentChannelID == "" {
		return
	}

	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)
	if bottom == nil {
		return
	}

	cached := a.messages.Get(a.currentChannelID)
	i, ok := slices.BinarySearchFunc(cached, bottom.ID, cache.CompareMessageID)
	if !ok || i+1 == len(cached) {
		return // bottom is the live tail, or no longer cached
	}

	a.appendMessages(cached[i+1:min(i+1+historyPageSize, len(cached))], bottom)
}

// appendMessages mounts newer messages below the current view, preserving the
// scroll position and trimming the top past mountedCap.
func (a *App) appendMessages(page []*revoltgo.Message, bottom *revoltgo.Message) {
	if len(page) == 0 {
		return
	}

	// Tighten the old bottom widget's margin when the first new message continues
	// its group.
	if continuesGroup(bottom, page[0]) {
		a.tightenBottomWidget()
	}

	prev := bottom
	for i, msg := range page {
		var next *revoltgo.Message
		if i+1 < len(page) {
			next = page[i+1]
		}
		a.messageList.Add(a.newMessageWidget(prev, msg, next))
		prev = msg
	}

	if over := len(a.messageList.Objects) - mountedCap; over > 0 {
		a.trimMountedTop(over, false)
	}

	a.messageList.Refresh()
	a.messageScroll.Refresh()
}

// trimMountedTop unmounts the oldest n widgets, keeping the scroll position
// stable unless the view is pinned to the bottom anyway, where the caller's
// scrollToBottom makes the adjustment moot.
func (a *App) trimMountedTop(n int, atBottom bool) {
	objects := a.messageList.Objects

	var removed float32
	if !atBottom {
		for _, obj := range objects[:n] {
			removed += obj.MinSize().Height
		}
	}

	a.messageList.Objects = objects[n:]
	clear(objects[:n]) // release the trimmed widgets; the retained slice shares this array

	if !atBottom {
		a.messageScroll.Offset.Y = max(a.messageScroll.Offset.Y-removed, 0)
	}
}

// trimMountedBottom unmounts widgets far below the viewport after a prepend; they
// re-mount via mountNewerFromCache on the way back down. It stops when the
// would-be bottom row is no longer cached: the cache window ends at the live tail,
// so once scrollback runs past its cap the downward remount could not re-serve
// trimmed rows, and the window is allowed to grow instead.
func (a *App) trimMountedBottom() {
	over := len(a.messageList.Objects) - mountedCap
	if over <= 0 {
		return
	}

	objects := a.messageList.Objects
	keep := len(objects) - over

	newBottom := a.mountedMessage(keep - 1)
	if newBottom == nil || a.messages.Find(a.currentChannelID, newBottom.ID) == nil {
		return
	}

	a.messageList.Objects = objects[:keep]
	clear(objects[keep:])
	a.messageList.Refresh()
}

// tightenBottomWidget closes the gap under the bottom-most mounted message,
// marking it as the head of a group that continues into the row about to be
// appended.
func (a *App) tightenBottomWidget() {
	n := len(a.messageList.Objects)
	if n == 0 {
		return
	}

	if w, ok := a.messageList.Objects[n-1].(*ui.MessageWidget); ok {
		w.SetFollowedByGroup(true)
	}
}

// scrollToBottom scrolls the message view to the newest message.
func (a *App) scrollToBottom() {
	if a.messageScroll != nil {
		a.messageScroll.ScrollToBottom()
	}
}
