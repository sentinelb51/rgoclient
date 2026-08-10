package app

import (
	"errors"
	"log"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	initialPageSize = 30 // messages fetched when first opening a channel

	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"
	remountThreshold  = 200 // px from the bottom before trimmed newer rows re-mount
)

// The mounting budget, all four settings. They are read per use rather than
// captured, so changing one in the settings applies to the next scroll instead
// of the next launch.

// historyPageSize is how many older messages are fetched, or re-mounted from
// cache, per scroll-up.
func historyPageSize() int { return config.Current().Behaviour.HistoryPageSize }

// initialMountCount is how many messages a channel switch mounts. Far fewer than
// the cache holds: only ~20 fit on screen, scrollback re-mounts the rest from
// cache instantly, and every mounted widget is real work — rapid channel
// switching churns widgets the renderer cache then holds for up to a minute, so
// this directly bounds how fast memory can ratchet up.
func initialMountCount() int { return config.Current().Behaviour.InitialMountCount }

// mountedCap is the ceiling on mounted widgets during scrollback: prepends past
// it trim widgets off the bottom and vice versa, so scrolling through any amount
// of history keeps a constant number mounted.
func mountedCap() int { return config.Current().Behaviour.MountedCap }

// renderedCap is the same ceiling for live appends, one page lower so a channel
// that is being talked in does not sit permanently at the trim threshold.
func renderedCap() int { return max(mountedCap()-historyPageSize(), 1) }

// messageGroupWindow is the largest gap a message may follow the previous one by
// and still group under it.
func messageGroupWindow() time.Duration { return config.Current().Behaviour.GroupWindow() }

/* The message area */

// buildMessageArea builds the message list, header, and composer.
//
// The column reports a fixed minimum rather than what it is holding. It is the
// one section whose contents are somebody else's — a long display name, a wide
// attachment, a mention picker over a tall composer — and Fyne grows a window the
// frame its content's minimum outgrows it, without ever giving the room back. Left
// to report honestly, the window resized itself as messages mounted.
func (a *App) buildMessageArea() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	a.input = ui.NewMessageInput(a.deps(), a.window)
	a.input.SetPlaceHolder(composerPlaceholder)
	a.input.OnSubmit = a.handleSubmit
	a.input.OnEditLast = a.editLastOwnMessage
	a.input.OnRefused = func(reason string) { a.notify(ui.ToneWarning, "%s", reason) }
	a.input.OnTyping = a.noteTyping
	a.input.RegisterDropHandler()

	// Floating composer dock: the mention picker, reply and attachment rows and the
	// entry stack inside one rounded card. Its fill is the entry's own input
	// background, so the entry's box disappears into it and the outline draws the
	// boundary instead — taking the accent on focus, the composer's only "you are
	// typing here" cue. The padding is thin because everything in the stack already
	// carries its own inset, and it goes through ui.NewInset because NewPadded and
	// Border would each add theme padding on top of what is asked for.
	//
	// What makes it float is that the messages run *under* it: ui.NewFloatingDock
	// hangs it over a column taller than itself, so there is no cut above the card
	// to read as the top of a bar, and ui.Elevate's shadow darkens the content
	// disappearing beneath it. The gap around it is only a gutter.
	//
	// Which is why AppTheme answers ColorNameShadow with nothing: Fyne's ambient
	// shadow is a scroll-edge gradient that would land in this same gap and draw
	// the bar straight back on. One deliberate cast shadow, no ambient ones.
	dockBg := canvas.NewRectangle(theme.Colors.ComposerBg)
	dockBg.CornerRadius = theme.Sizes.ComposerRadius
	ui.Outline(dockBg)
	ui.Elevate(dockBg)
	a.input.OnFocusChanged = func(focused bool) {
		dockBg.StrokeColor = theme.Colors.Outline
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
	card := container.NewStack(dockBg, ui.NewInset(inner, padV, padV, padH, padH))

	// The slowmode chip rides above the card rather than inside it, flush with its
	// right edge: what floats over the message column is the whole stack, so
	// NewDockReserve holds room for the chip too and the newest message still comes
	// to rest clear of it. A zero-width spacer takes the row's fill, which is what
	// pins the chip to the trailing edge at its own minimum — and that row's layout
	// is the one it re-runs each time it is relabelled.
	// The typing line hangs at the leading end of that same row. The spacer stays
	// the fill rather than the line taking it: a hidden child is skipped by the
	// row's layout, so a fill slot that can disappear would leave the chip placed
	// from the left in every channel where nobody is typing.
	a.slowmodeBadge = ui.NewSlowmodeBadge()
	a.typingIndicator = ui.NewTypingIndicator(a.images)
	badgeRow := ui.NewFillRow(1, a.typingIndicator, ui.HorizontalSpacer(0), a.slowmodeBadge)
	a.slowmodeBadge.OnResize = func() { ui.Relayout(badgeRow) }
	a.typingIndicator.OnResize = func() { ui.Relayout(badgeRow) }
	a.composerDock = ui.VBoxNoSpacing(badgeRow, card)

	a.messageScroll = ui.NewObservableVScroll(ui.NewDockReserve(a.messageList, a.composerDock))
	a.messageScroll.OnScroll = func(pos fyne.Position) {
		if pos.Y <= 0 {
			a.loadMoreHistory()
			return
		}
		if a.contentHeight()-a.messageScroll.Size().Height-pos.Y <= remountThreshold {
			a.mountNewerFromCache()
		}
	}
	a.clearMessages()

	a.channelHeader = widget.NewLabelWithStyle(a.channelName(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.channelGlyph = container.NewStack(ui.ChannelGlyph(a.channelKind()))
	title := container.NewHBox(a.channelGlyph, a.channelHeader)
	members := ui.NewIconButton(assets.MembersIcon, a.toggleMemberList, nil)
	header := container.NewPadded(container.NewBorder(nil, nil, title, members))

	a.floatingDock = ui.NewFloatingDock(a.messageScroll, a.composerDock)
	layout := ui.NewFillColumn(1, header, a.floatingDock)

	floor := fyne.NewSize(theme.Sizes.MessageAreaMinWidth, theme.Sizes.MessageAreaMinHeight)
	return ui.NewFixedSizeContainer(floor, container.NewStack(background, layout))
}

// resizeDock re-hangs the floating stack after something in it appeared or
// disappeared — the slowmode chip, the typing line. Fyne re-lays out for a growing
// minimum on its own but leaves a shrinking one reserved, and here the stack
// stands on its own height twice over: the card is placed from the bottom up, and
// ui.DockReserve is how much of the message column it costs. Laying out the
// floating dock resizes the stack, which re-runs the stack's own layout in turn;
// the scroll is refreshed because the room it holds for the dock is part of the
// height it scrolls through. Call on the UI thread.
func (a *App) resizeDock() {
	ui.Relayout(a.floatingDock)
	a.messageScroll.Refresh()
}

/* Composing */

// The composer's placeholder, which is where a channel that will not take a
// message says so: without SendMessage the entry is disabled, so the placeholder
// is the only thing left in the card to carry the reason.
const (
	composerPlaceholder = "Send a message..."
	composerNoAccess    = "You don't have access to this channel"
	composerNoSending   = "You can't send messages in this channel"
)

// syncComposer matches the composer to what the account may do in the open
// channel. Called from every path that changes which channel that is, and from
// the gateway event that can change the answer without the channel moving — so
// it reads the open channel rather than being told, and there is one answer
// however it was reached.
func (a *App) syncComposer() {
	if a.input == nil {
		return
	}

	channel, known := a.store.Channel(a.currentChannelID)
	permissions := a.store.Permissions(a.currentChannelID)
	a.input.SetPermissions(permissions)

	switch {
	case !known || permissions.Has(domain.PermissionSendMessage):
		a.input.SetPlaceHolder(composerPlaceholder)
	case !a.canViewChannel(channel):
		a.input.SetPlaceHolder(composerNoAccess)
	default:
		a.input.SetPlaceHolder(composerNoSending)
	}
}

// handleSubmit sends the composed message, its attachments, and its replies. The
// composer is cleared immediately and the send runs in the background: the
// message appears when the gateway echoes it back.
func (a *App) handleSubmit(text string) {
	if (text == "" && len(a.input.Attachments) == 0) || a.currentChannelID == "" {
		return
	}

	channelID := a.currentChannelID

	// The entry is disabled where this is missing, so reaching here means the
	// permission went away while the message was being typed. Unlike slowmode this
	// one is said out loud: nothing on screen has changed to explain it.
	if !a.store.Permissions(channelID).Has(domain.PermissionSendMessage) {
		a.syncComposer()
		a.notify(ui.ToneWarning, "%s.", composerNoSending)
		return
	}

	// A channel in slowmode refuses the send and keeps what was typed. Nothing is
	// said about it: the badge counting down beside the caret is already the
	// answer, and a notice per keypress would bury it.
	if a.slowmodeRemaining(channelID) > 0 {
		return
	}

	attachments := slices.Clone(a.input.Attachments)
	replies := toReplies(a.input.Replies)

	a.input.SetText("")
	a.input.ClearAttachments()
	a.input.ClearReplies()
	a.jumpToLatest()
	a.startSlowmode(channelID)
	a.stopTyping(channelID) // emptying the entry from here raises no keystroke to notice

	// The cooldown starts optimistically, so a second Enter cannot outrun the
	// request — and is given back when the message never landed.
	onFail := a.notifyFailure("send message", "Could not send that message.")
	a.background(
		func() error { return a.client.SendMessage(channelID, text, attachments, replies) },
		func(err error) {
			a.clearSlowmode(channelID)
			onFail(err)
		},
	)
}

/* Slowmode */

// slowmodeTick is how often the badge re-reads a running cooldown. It only ever
// draws whole seconds, so anything finer would repaint the same text.
const slowmodeTick = time.Second

// slowmodeOf is the cooldown a channel imposes *on this account* — zero when the
// channel has none, and zero for anyone holding BypassSlowmode, for whom the
// rule exists but does not apply.
func (a *App) slowmodeOf(channelID string) time.Duration {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Slowmode == 0 {
		return 0
	}
	if a.store.Permissions(channelID).Has(domain.PermissionBypassSlowmode) {
		return 0
	}

	return channel.Slowmode
}

// slowmodeRemaining is how much of a channel's cooldown is left to wait.
func (a *App) slowmodeRemaining(channelID string) time.Duration {
	if a.slowmodeOf(channelID) == 0 {
		return 0
	}

	return max(time.Until(a.slowmodeUntil[channelID]), 0)
}

// startSlowmode begins a channel's cooldown, unless one is already running: the
// gateway echo of a message this client just sent must not extend the wait the
// send itself started. That the echo calls this at all is what covers a message
// the same account sent from somewhere else. Call on the UI thread.
func (a *App) startSlowmode(channelID string) {
	cooldown := a.slowmodeOf(channelID)
	if cooldown == 0 || time.Now().Before(a.slowmodeUntil[channelID]) {
		return
	}

	a.slowmodeUntil[channelID] = time.Now().Add(cooldown)
	a.refreshSlowmode()
}

// clearSlowmode gives a cooldown back, for a send that never landed. Call on the
// UI thread.
func (a *App) clearSlowmode(channelID string) {
	delete(a.slowmodeUntil, channelID)
	a.refreshSlowmode()
}

// refreshSlowmode repaints the badge for the open channel and keeps a running
// cooldown ticking. The tick is one timer re-armed a second at a time rather
// than a ticker for the life of the app: outside a channel that is counting down
// there is nothing to count. Call on the UI thread.
func (a *App) refreshSlowmode() {
	if a.slowmodeBadge == nil {
		return
	}
	if a.slowmodeTimer != nil {
		a.slowmodeTimer.Stop()
		a.slowmodeTimer = nil
	}

	channelID := a.currentChannelID
	remaining := a.slowmodeRemaining(channelID)

	shown := a.slowmodeBadge.Visible()
	a.slowmodeBadge.Set(a.slowmodeOf(channelID), remaining)
	if a.slowmodeBadge.Visible() != shown {
		a.resizeDock()
	}

	if remaining == 0 {
		return
	}

	epoch := a.epoch
	a.slowmodeTimer = time.AfterFunc(slowmodeTick, func() {
		a.doOnUI(func() {
			if !a.stale(epoch) {
				a.refreshSlowmode()
			}
		}, false)
	})
}

// loadSlowmode re-reads a channel's cooldown in the background and repaints the
// badge if it is still the open one.
//
// It asks on every visit rather than once. Revolt announces a changed slowmode
// through ChannelUpdate and revoltgo models neither that field nor the one on
// the channel itself, so opening the channel is the only moment the client can
// learn the number — or learn that it moved. It is one small request alongside
// the message page the same switch already fetches.
func (a *App) loadSlowmode(channelID string) {
	epoch := a.epoch

	go func() {
		if _, err := a.client.FetchSlowmode(channelID); err != nil {
			if !errors.Is(err, client.ErrNoSession) {
				log.Printf("fetch slowmode for %s: %v", channelID, err)
			}
			return
		}

		a.doOnUI(func() {
			if !a.stale(epoch) && a.currentChannelID == channelID {
				a.refreshSlowmode()
			}
		}, false)
	}()
}

// toReplies drops the composer's own bookkeeping — which channel each quoted
// message lives in, needed only to draw its preview — leaving what is sent.
func toReplies(pending []ui.Reply) []domain.Reply {
	replies := make([]domain.Reply, len(pending))
	for i, reply := range pending {
		replies[i] = domain.Reply{ID: reply.ID, Mention: reply.Mention}
	}

	return replies
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
//
// A system event names whoever it is about rather than an author, and reads
// "Someone joined" until that user is known — so it is the target that gets
// chased. ui.MessageWidget.Author answers with it for the same reason, which is
// what lets one refresh pass cover both.
func (a *App) newMessageWidget(prev, curr, next *domain.Message) *ui.MessageWidget {
	switch {
	case curr.System != nil:
		a.ensureAuthor(a.channelServerID(curr.ChannelID), curr.System.Target)
	case curr.Webhook == nil:
		a.ensureAuthor(a.channelServerID(curr.ChannelID), curr.AuthorID)
	}

	return ui.NewMessageWidget(a.deps(), curr, dayLabel(prev, curr),
		continuesGroup(prev, curr), continuesGroup(curr, next))
}

// dayLabel returns the day separator label for curr — "" when it belongs to the
// same calendar day as the message above it. A message with no predecessor is
// treated as opening its day, so loaded history always starts with a date;
// prepending older messages rebuilds that row, dropping the label if the day
// continues.
func dayLabel(prev, curr *domain.Message) string {
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
func continuesGroup(prev, curr *domain.Message) bool {
	if !config.Current().Interface.GroupMessages {
		return false
	}
	if prev == nil || curr == nil || curr.AuthorID == "" || prev.AuthorID != curr.AuthorID {
		return false
	}
	if curr.System != nil || prev.System != nil ||
		curr.Webhook != nil || prev.Webhook != nil ||
		curr.Masquerade || prev.Masquerade {
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
	return gap >= 0 && gap <= messageGroupWindow()
}

// channelKind is the open channel's type, or the zero value — which draws the
// hashtag — when nothing is selected.
func (a *App) channelKind() domain.ChannelKind {
	channel, _ := a.currentChannel()

	return channel.Kind
}

// setChannelGlyph repoints the message header's prefix mark at the open
// channel's type, so a DM reads "@name" rather than "#name". Call on the UI
// thread.
func (a *App) setChannelGlyph() {
	if a.channelGlyph == nil {
		return
	}

	a.channelGlyph.Objects = []fyne.CanvasObject{ui.ChannelGlyph(a.channelKind())}
	a.channelGlyph.Refresh()
}

/* Loading and rendering */

// loadChannelMessages fetches the newest page of messages for a channel. The
// client deduplicates concurrent requests per channel, so switching back and
// forth no longer fans out one fetch per switch — the superseded switch finds the
// page already on its way and leaves it to finish.
func (a *App) loadChannelMessages(channelID string) {
	go func() {
		count, err := a.client.LatestMessages(channelID, initialPageSize)
		if err != nil {
			if errors.Is(err, client.ErrBusy) {
				return
			}
			log.Printf("load channel %s: %v", channelID, err)
			a.doOnUI(func() {
				if a.currentChannelID == channelID {
					a.showStatus("Failed to load messages")
				}
			}, true)
			return
		}

		a.doOnUI(func() {
			if a.currentChannelID != channelID {
				return
			}
			if count == 0 {
				a.showStatus("No messages in this channel")
				return
			}

			// Render from the cache, not from the page just stored: a gateway message
			// can land between the two, and the cache is the one view that already
			// includes it.
			a.displayCached()
		}, true)
	}()
}

// displayCached re-renders the open channel from its cached messages.
func (a *App) displayCached() {
	a.displayMessages(a.client.Messages().Get(a.currentChannelID))
}

// displayMessages renders the newest initialMountCount messages, oldest first,
// and scrolls to the bottom. Older cached messages are re-mounted on scrollback
// by loadMoreHistory's cache tier. Call on the UI thread.
func (a *App) displayMessages(messages []*domain.Message) {
	a.cancelActiveEdit()
	if mount := initialMountCount(); len(messages) > mount {
		messages = messages[len(messages)-mount:]
	}

	widgets := make([]fyne.CanvasObject, len(messages))
	for i, message := range messages {
		var prev, next *domain.Message
		if i > 0 {
			prev = messages[i-1]
		}
		if i+1 < len(messages) {
			next = messages[i+1]
		}
		widgets[i] = a.newMessageWidget(prev, message, next)
	}

	a.messageList.Objects = widgets
	a.remountMessages()
	a.scrollToBottom()
}

// showStatus replaces the message list with a single centred line.
func (a *App) showStatus(text string) {
	a.cancelActiveEdit()
	label := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Centred on what can be seen, so the room held for the dock doesn't push the
	// line low and leave the column scrollable by exactly that much.
	height := float32(400)
	if a.messageScroll != nil {
		if h := a.messageScroll.Size().Height - ui.DockReserve(a.composerDock) - 5; h > 100 {
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
	cached := a.client.Messages().Get(a.currentChannelID)
	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)

	if len(cached) == 0 || (bottom != nil && bottom.ID == cached[len(cached)-1].ID) {
		a.scrollToBottom()
		return
	}

	a.displayCached()
}

// removeMessages unmounts deleted messages in one pass, re-evaluating grouping at
// every seam a removal leaves behind — a continuation whose group head was
// deleted regains its header. A moderation sweep deletes a whole run at once, and
// doing that one ID at a time would rescan the mounted list and relayout the
// column once per message. Call on the UI thread.
func (a *App) removeMessages(channelID string, messageIDs []string) {
	if channelID != a.currentChannelID || len(messageIDs) == 0 {
		return
	}

	doomed := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		doomed[id] = true
	}

	// seams records, in the surviving list, where each removal happened: the row
	// that moves up into that slot has a new predecessor above it.
	objects := a.messageList.Objects
	kept, seams := objects[:0], []int(nil)
	for _, obj := range objects {
		w, ok := obj.(*ui.MessageWidget)
		if !ok || !doomed[w.Message().ID] {
			kept = append(kept, obj)
			continue
		}

		if a.editing != nil && a.editing.Message().ID == w.Message().ID {
			a.editing = nil // the editor unmounts with its widget
		}
		seams = append(seams, len(kept))
	}
	if len(seams) == 0 {
		return
	}

	clear(objects[len(kept):]) // the unmounted widgets still sit in the shared array
	a.messageList.Objects = kept

	a.rebuildSeams(seams)
	a.remountMessages()
}

// rebuildSeams re-derives the grouping either side of each index a removal left
// behind: the row that moved up sees a new message above it, and the row above it
// a new one below. Indices are into the surviving list, ascending, and repeat
// where a run was deleted — each is applied once.
func (a *App) rebuildSeams(seams []int) {
	last := -1
	for _, i := range seams {
		if i == last {
			continue
		}
		last = i

		prev, next := a.mountedMessage(i-1), a.mountedMessage(i)
		if next != nil {
			a.messageList.Objects[i] = a.newMessageWidget(prev, next, a.mountedMessage(i+1))
		}
		if i > 0 {
			if w, ok := a.messageList.Objects[i-1].(*ui.MessageWidget); ok {
				w.SetFollowedByGroup(continuesGroup(prev, next))
			}
		}
	}
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

	message := a.client.Messages().Find(channelID, messageID)
	if message == nil {
		return
	}

	a.messageList.Objects[i] = a.newMessageWidget(a.mountedMessage(i-1), message, a.mountedMessage(i+1))
	a.remountMessages()
}

// editLastOwnMessage opens the in-place editor on the user's newest editable
// message in the open channel, triggered by Up in an empty composer. It scans the
// cache rather than tracking "last sent" state: the cache only ever gains own
// messages through the gateway echo, so the scan can't race the send path.
func (a *App) editLastOwnMessage() {
	self := a.store.SelfID()
	if self == "" || a.currentChannelID == "" {
		return
	}

	cached := a.client.Messages().Get(a.currentChannelID)
	for i := len(cached) - 1; i >= 0; i-- {
		message := cached[i]
		if message.AuthorID == self && message.System == nil && message.Content != "" {
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
func (a *App) mountedMessage(i int) *domain.Message {
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
func (a *App) appendMessage(message, prev *domain.Message) {
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

	contentHeight := a.contentHeight()
	viewHeight := a.messageScroll.Size().Height
	atBottom := contentHeight-viewHeight-a.messageScroll.Offset.Y < atBottomTolerance

	// When this message continues the one above it, tighten that message's bottom
	// margin so the group reads as a block.
	if continuesGroup(prev, message) {
		a.tightenBottomWidget()
	}
	a.messageList.Add(a.newMessageWidget(prev, message, nil))

	if over := len(a.messageList.Objects) - renderedCap(); over > 0 {
		a.trimMountedTop(over, atBottom)
	}

	a.remountMessages()
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

	messages := a.client.Messages()
	cached := messages.Get(channelID)
	if i, ok := slices.BinarySearchFunc(cached, top.ID, cache.CompareMessageID); ok && i > 0 {
		a.prependMessages(cached[max(0, i-historyPageSize()):i])
		return
	}

	if messages.IsDepleted(channelID) {
		return
	}
	a.loadingHistory = true

	go func() {
		defer a.doOnUI(func() { a.loadingHistory = false }, true)

		older, err := a.client.HistoryBefore(channelID, top.ID, historyPageSize())
		if err != nil {
			if !errors.Is(err, client.ErrBusy) {
				log.Printf("load history for %s: %v", channelID, err)
			}
			return
		}

		a.doOnUI(func() {
			if a.currentChannelID == channelID && a.mountedMessage(0) == top {
				a.prependMessages(older)
			}
		}, true)
	}()
}

// prependMessages mounts older messages, oldest first, above the current view,
// preserving the scroll position and trimming the bottom past mountedCap.
func (a *App) prependMessages(older []*domain.Message) {
	if len(older) == 0 {
		return
	}

	// The one place that genuinely needs the *minimum* rather than the laid-out
	// height: the offset correction below is measured across the mutation, and the
	// scroller has not re-laid the content out by then. It is two measurements per
	// scrolled-to page, not per frame.
	oldHeight := a.messageList.MinSize().Height

	// The newest prepended message lands directly above the previously topmost
	// message, so that existing row is each one's neighbour at the seam.
	topMessage, topNext := a.mountedMessage(0), a.mountedMessage(1)

	// The oldest message has no loaded predecessor, so it renders as a full
	// message; every other one sees its true neighbours for grouping.
	widgets := make([]fyne.CanvasObject, len(older))
	for i, msg := range older {
		var prev, next *domain.Message
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
	a.remountMessages()

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

	cached := a.client.Messages().Get(a.currentChannelID)
	i, ok := slices.BinarySearchFunc(cached, bottom.ID, cache.CompareMessageID)
	if !ok || i+1 == len(cached) {
		return // bottom is the live tail, or no longer cached
	}

	a.appendMessages(cached[i+1:min(i+1+historyPageSize(), len(cached))], bottom)
}

// appendMessages mounts newer messages below the current view, preserving the
// scroll position and trimming the top past mountedCap.
func (a *App) appendMessages(page []*domain.Message, bottom *domain.Message) {
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
		var next *domain.Message
		if i+1 < len(page) {
			next = page[i+1]
		}
		a.messageList.Add(a.newMessageWidget(prev, msg, next))
		prev = msg
	}

	if over := len(a.messageList.Objects) - mountedCap(); over > 0 {
		a.trimMountedTop(over, false)
	}

	a.remountMessages()
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
	over := len(a.messageList.Objects) - mountedCap()
	if over <= 0 {
		return
	}

	objects := a.messageList.Objects
	keep := len(objects) - over

	newBottom := a.mountedMessage(keep - 1)
	if newBottom == nil || a.client.Messages().Find(a.currentChannelID, newBottom.ID) == nil {
		return
	}

	a.messageList.Objects = objects[:keep]
	clear(objects[keep:])
	a.remountMessages()
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

// remountMessages repaints the column after the *set* of mounted widgets changed
// — an append, a prepend, a trim, a widget swapped in place.
//
// Container.Refresh would also call Refresh on every child, and widget.RichText's
// Refresh drops its cached minimum size and re-runs updateRowBounds, i.e. re-wraps
// its text. So telling the list that one widget arrived re-flowed every mounted
// message body. Only the container's own geometry changed, and ui.Relayout is
// exactly that: re-run this layout, repaint, don't walk the children. Widgets
// built during the mutation are new and already carry their content.
func (a *App) remountMessages() {
	ui.Relayout(a.messageList)
}

// contentHeight is the laid-out height of the mounted column. It reads the size
// the scroller already gave the content rather than asking the list for its
// minimum: fyne's BaseWidget.MinSize is not memoised, so MinSize here walks every
// mounted widget's renderer — which on the scroll path means once per wheel tick
// and once per pan frame.
func (a *App) contentHeight() float32 {
	if a.messageScroll == nil || a.messageScroll.Content == nil {
		return 0
	}

	return a.messageScroll.Content.Size().Height
}
