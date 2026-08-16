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

	// maxUncachedMessages bounds what is held outside the message cache: quoted
	// messages older than a channel's cached tail, and the window a jump landed
	// in. Only what reaches further back than the cache lands here, so it is a
	// ceiling rather than a working size — but a session left running is a session
	// that must not grow, and nothing else evicts these.
	maxUncachedMessages = 512
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
// The column reports a fixed minimum rather than what it holds. Its contents are
// somebody else's — a long display name, a wide attachment — and Fyne grows a
// window the frame its content's minimum outgrows it without ever giving the room
// back, so reporting honestly resized the window as messages mounted.
func (a *App) buildMessageArea() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	a.input = ui.NewMessageInput(a.deps(), a.window)
	a.input.SetPlaceHolder(composerPlaceholder)
	a.input.OnSubmit = a.handleSubmit
	a.input.OnEditLast = a.editLastOwnMessage
	a.input.OnRefused = func(reason string) { a.notify(ui.ToneWarning, "%s", reason) }
	a.input.OnTyping = a.noteTyping
	a.input.RegisterDropHandler()

	// Floating composer dock: mention picker, reply and attachment rows and the
	// entry stack in one rounded card. Its fill is the entry's own background, so
	// the entry's box disappears into it and the outline draws the boundary
	// instead — taking the accent on focus, the composer's only "typing here" cue.
	// ui.NewInset rather than NewPadded, which would add theme padding on top.
	//
	// What makes it float is that the messages run *under* it (ui.NewFloatingDock),
	// so there is no cut above the card to read as the top of a bar and Elevate's
	// shadow darkens what disappears beneath it. Which is why AppTheme answers
	// ColorNameShadow with nothing: Fyne's ambient shadow would land in the same
	// gutter and draw the bar straight back on.
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

	// The emoji button rides the entry's last line rather than the middle of it —
	// see ui.NewComposerButtonSlot, which is what decides where in the row it lands.
	entry := ui.NewFillRow(0,
		ui.WithCaret(a.input),
		ui.NewComposerButtonSlot(a.input.EmojiButton),
	)

	inner := ui.VBoxNoSpacing(
		a.input.Mentions,
		a.input.ReplyContainer,
		a.input.AttachmentContainer,
		entry,
	)
	padV, padH := theme.Sizes.ComposerPaddingV, theme.Sizes.ComposerPaddingH
	card := container.NewStack(dockBg, ui.NewInset(inner, padV, padV, padH, padH))

	// The slowmode chip rides above the card, flush with its right edge — the whole
	// stack floats, so NewDockReserve holds room for the chip too. A zero-width
	// spacer takes the row's fill, which pins the chip to the trailing edge at its
	// own minimum, and the typing line hangs at the leading end. The spacer keeps
	// the fill rather than the line taking it: a hidden child is skipped by the
	// layout, so a disappearing fill slot would place the chip from the left in
	// every channel where nobody is typing.
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

	// The way back from a jump goes in the header rather than over the column: the
	// badge row above the composer is bare text with nothing behind it and nothing
	// in it accepting a pointer, and this is a button. It also belongs beside the
	// channel's name, being about which part of that channel is on screen.
	a.jumpChip = ui.NewTappableChip("Jump to present", theme.Colors.TextPrimary, a.returnToPresent)
	a.jumpChip.Hide()

	// The ways into the two message panels sit beside the member toggle: all three
	// are about the channel on screen rather than about anything in the column,
	// and a pin's own mark is what the message row already carries.
	search := ui.NewIconButton(assets.SearchIcon, a.showChannelSearch, nil)
	pins := ui.NewIconButton(assets.SystemPinnedIcon, a.showPinnedMessages, nil)

	members := ui.NewIconButton(assets.MembersIcon, a.toggleMemberList, nil)
	a.messageHeader = container.NewBorder(nil, nil, title, container.NewHBox(a.jumpChip, search, pins, members))
	header := container.NewPadded(a.messageHeader)

	// The note hangs under the header rather than over the column: it is about the
	// channel, not about what is in it, and the column below carries messages the
	// reader is here for. It is built once and shown per channel — there is one
	// note, and a strip rebuilt per selection would be a widget per channel switch.
	// Its visibility comes from the open channel rather than starting hidden: a
	// restyle rebuilds this tree under a standing selection, and a note that came
	// back down would leave a voice channel looking like a text one.
	a.channelNote = ui.NewChannelNote(assets.VoiceIcon, voiceNote)
	if a.channelKind() != domain.ChannelVoice {
		a.channelNote.Hide()
	}

	a.floatingDock = ui.NewFloatingDock(a.messageScroll, a.composerDock)
	a.messageColumn = ui.NewFillColumn(2, header, a.channelNote, a.floatingDock)

	floor := fyne.NewSize(theme.Sizes.MessageAreaMinWidth, theme.Sizes.MessageAreaMinHeight)
	return ui.NewFixedSizeContainer(floor, container.NewStack(background, a.messageColumn))
}

// resizeDock re-hangs the floating stack after something in it appeared or
// disappeared. Fyne re-lays out for a growing minimum but leaves a shrinking one
// reserved, and the stack stands on its own height twice over: the card is placed
// from the bottom up, and ui.DockReserve is what it costs the message column. The
// scroll is refreshed because that room is part of the height it scrolls through.
// Call on the UI thread.
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
// badge if it is still the open one. It asks on every visit: revoltgo models
// neither the field nor its ChannelUpdate, so opening the channel is the only
// moment the client can learn the number, or learn that it moved.
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
// when next continues it, and heading it with a day separator on a new day.
//
// Every mount path funnels through here, so this is also where an unresolved
// author is chased down — ensureAuthor is a no-op once State knows the user, so
// it costs two map lookups per widget in the common case.
//
// A system event names whoever it is about rather than an author, so it is the
// *target* that gets chased, and ui.MessageWidget.Author answers with it — which
// lets one refresh pass cover both. Only where the target is somebody: see
// domain.SystemMessage.TargetsUser.
func (a *App) newMessageWidget(prev, curr, next *domain.Message) *ui.MessageWidget {
	switch {
	case curr.System != nil:
		if curr.System.TargetsUser() {
			a.ensureAuthor(a.channelServerID(curr.ChannelID), curr.System.Target)
		}
	case curr.Webhook == nil:
		a.ensureAuthor(a.channelServerID(curr.ChannelID), curr.AuthorID)
	}
	if !continuesGroup(prev, curr) {
		a.ensureReplies(curr)
	}

	return ui.NewMessageWidget(a.deps(), curr, dayLabel(prev, curr),
		continuesGroup(prev, curr), continuesGroup(curr, next))
}

/* Lazy reply resolution */

// ensureReplies makes a message's quoted lines renderable. A reply names a
// message by ID alone and the cache is only a channel's tail, so a quote whose
// target has scrolled out reads as "Unknown message reference" unless asked for.
//
// The fetchedReplies guard is **kept** on failure, unlike ensureAuthor's: the
// usual reason is that the target was deleted, which stays true, and a quote
// remounts on every scroll past it. The cost is that one missed through a dropped
// connection stays unresolved until the channel is reopened.
//
// A grouped continuation draws no quotes, so the caller does not queue for one.
// Call on the UI thread.
func (a *App) ensureReplies(message *domain.Message) {
	for _, replyID := range message.Replies {
		if a.fetchedReplies[replyID] || a.ResolveMessage(message.ChannelID, replyID) != nil {
			continue
		}

		a.fetchedReplies[replyID] = true
		a.pendingReplies = append(a.pendingReplies, client.MessageRef{
			ChannelID: message.ChannelID,
			MessageID: replyID,
		})
	}
	if len(a.pendingReplies) == 0 || a.replyTimer != nil {
		return
	}

	// The same settling window author resolution uses, and deliberately not a knob
	// of its own: both are "what this page needs and does not have", queued by the
	// same mount and answered in the same hop.
	a.replyTimer = time.AfterFunc(authorFetchDelay(), func() {
		a.doOnUI(a.flushReplies, false)
	})
}

// flushReplies fetches everything ensureReplies has queued and repaints the
// quotes once for the whole batch. The authors behind them are queued in the same
// pass — a quoted message names its author by ID like any other, and somebody who
// only ever spoke that far back is nobody the page has resolved. Call on the UI
// thread.
func (a *App) flushReplies() {
	a.replyTimer = nil

	pending := a.pendingReplies
	a.pendingReplies = nil
	if len(pending) == 0 {
		return
	}
	epoch := a.epoch

	go func() {
		fetched := a.client.ResolveMessages(pending)

		a.doOnUI(func() {
			if a.stale(epoch) || len(fetched) == 0 {
				return
			}

			// holdUncached drops the store and its guard together, so nothing is
			// left believing a target was already asked for. A quote still on
			// screen is simply asked for again the next time it mounts.
			a.holdUncached(fetched)

			resolved := make(map[string]bool, len(fetched))
			for _, message := range fetched {
				resolved[message.ID] = true

				if message.System == nil && message.Webhook == nil {
					a.ensureAuthor(a.channelServerID(message.ChannelID), message.AuthorID)
				}
			}

			for _, obj := range a.messageList.Objects {
				if w, ok := obj.(*ui.MessageWidget); ok {
					w.RefreshReplies(resolved)
				}
			}
		}, false)
	}()
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
// same author, neither system, webhook nor masqueraded, same calendar day, and
// within messageGroupWindow. A reply starts a fresh group, and so does a message
// across a day separator — a pair minutes apart over midnight must not render as
// one headerless block.
//
// A pinned message starts one too: its mark rides the name line, the one thing a
// continuation does not draw, so grouping it would be the way to pin a message
// and see nothing happen.
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
	if len(curr.Replies) > 0 || curr.Pinned {
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

// voiceNote is what the strip under the header says in a voice channel. Revolt's
// voice channels carry messages like any other, so what is missing is the call —
// which is the half the sentence has to name, a mark saying "voice" over an empty
// composer being how a channel that refuses messages looks.
const voiceNote = "Voice chat isn't supported yet. You can still send messages here."

// syncChannelKind matches the message header to the open channel's type: the
// prefix mark, so a DM reads "@name" rather than "#name", and the note under it,
// which only a voice channel draws. Hiding a child reclaims nothing on its own,
// so the column is relaid out either way. Call on the UI thread.
func (a *App) syncChannelKind() {
	if a.channelGlyph == nil {
		return
	}

	kind := a.channelKind()
	a.channelGlyph.Objects = []fyne.CanvasObject{ui.ChannelGlyph(kind)}
	a.channelGlyph.Refresh()

	if kind == domain.ChannelVoice {
		a.channelNote.Show()
	} else {
		a.channelNote.Hide()
	}
	ui.Relayout(a.messageColumn)
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
				a.showStatusMark(assets.EmptyChannelIcon, "No messages in this channel")
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
	a.setJumped(false) // the tail is what this mounts, so there is nowhere to go back to
	if mount := initialMountCount(); len(messages) > mount {
		messages = messages[len(messages)-mount:]
	}

	a.messageList.Objects = a.buildRun(messages, nil, nil)
	a.remountMessages()
	a.scrollToBottom()
}

// buildRun builds the widgets for a contiguous run of messages, oldest first,
// each seeing its true neighbours. before and after are what the column already
// holds either side of the run, so the seams group correctly; nil makes an end
// render as one nothing joins.
func (a *App) buildRun(messages []*domain.Message, before, after *domain.Message) []fyne.CanvasObject {
	widgets := make([]fyne.CanvasObject, len(messages))
	for i, message := range messages {
		prev, next := before, after
		if i > 0 {
			prev = messages[i-1]
		}
		if i+1 < len(messages) {
			next = messages[i+1]
		}
		widgets[i] = a.newMessageWidget(prev, message, next)
	}

	return widgets
}

// setFollowedByGroup tightens or releases the bottom margin of the widget at i.
// That margin belongs to the message above a seam rather than below it, so every
// re-grouping touches the predecessor through here.
func (a *App) setFollowedByGroup(i int, grouped bool) {
	if i < 0 || i >= len(a.messageList.Objects) {
		return
	}

	if w, ok := a.messageList.Objects[i].(*ui.MessageWidget); ok {
		w.SetFollowedByGroup(grouped)
	}
}

// rebuildAt replaces the widget at i with one built from message, and re-derives
// the grouping of the row above. The row below needs nothing — what decides its
// grouping is read off itself. Call on the UI thread.
func (a *App) rebuildAt(i int, message *domain.Message) {
	prev := a.mountedMessage(i - 1)
	a.messageList.Objects[i] = a.newMessageWidget(prev, message, a.mountedMessage(i+1))

	a.setFollowedByGroup(i-1, continuesGroup(prev, message))
}

// logPageError reports a page request's failure, ignoring the one that only says
// a request for the same channel was already out.
func logPageError(what string, err error) {
	if !errors.Is(err, client.ErrBusy) {
		log.Printf("%s: %v", what, err)
	}
}

// showStatus replaces the message list with a single centred line.
func (a *App) showStatus(text string) { a.showStatusMark(nil, text) }

// showStatusMark is the same line led by one of the client's own marks. Only a
// channel that is simply empty carries one: it is the one status that is not a
// wait or a refusal, so the mark is what the column is showing rather than a
// decoration on an apology.
func (a *App) showStatusMark(mark fyne.Resource, text string) {
	a.cancelActiveEdit()
	a.setJumped(false)
	line := ui.NewMessageStatus(mark, text)

	// Centred on what can be seen, so the room held for the dock doesn't push the
	// line low and leave the column scrollable by exactly that much.
	height := float32(400)
	if a.messageScroll != nil {
		if h := a.messageScroll.Size().Height - ui.DockReserve(a.composerDock) - 5; h > 100 {
			height = h
		}
	}

	a.messageList.Objects = []fyne.CanvasObject{ui.NewMinHeightContainer(height, line)}
	a.messageList.Refresh()

	// The inner list refresh alone doesn't repaint the scroll viewport until an
	// event forces relayout, so the status would only appear after a scroll.
	if a.messageScroll != nil {
		a.messageScroll.ScrollToTop()
		a.messageScroll.Refresh()
	}
}

// clearMessages empties the message list. Any tooltip goes with it, as the
// server sidebar's does on a rebuild: a reaction chip naming who is in it is
// about to be dropped, so it will never report the pointer leaving.
func (a *App) clearMessages() {
	a.cancelActiveEdit()
	a.setJumped(false)
	a.tooltip.Hide()
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

/* Jumping to a message */

// OnJumpToMessage brings a quoted message into view. Three answers, cheapest
// first: it may already be mounted, it may be in the channel's cached tail, and
// otherwise it is somewhere further back and the page around it is a request.
//
// Only the open channel is ever asked about. A reply names a message in the
// channel it was written in, so the two are the same by construction — and a
// jump that could also change channel would have to wait on that channel's own
// first page before it knew where to land.
func (a *App) OnJumpToMessage(channelID, messageID string) {
	if channelID != a.currentChannelID || messageID == "" {
		return
	}
	if a.scrollToMounted(messageID) || a.jumpWithinCache(messageID) {
		return
	}

	a.loadJumpWindow(channelID, messageID)
}

// scrollToMounted reveals a message that already has a widget, reporting whether
// it had one. Call on the UI thread.
func (a *App) scrollToMounted(messageID string) bool {
	i := a.messageWidgetIndex(messageID)
	if i == -1 {
		return false
	}

	a.revealMounted(i)
	return true
}

// jumpWithinCache mounts a window around a message the channel cache still
// holds, reporting whether it did. Nothing is detached here — the window is part
// of the cached tail, so scrolling either way is served from the cache exactly as
// ordinary scrollback is. Call on the UI thread.
func (a *App) jumpWithinCache(messageID string) bool {
	cached := a.client.Messages().Get(a.currentChannelID)
	i, ok := slices.BinarySearchFunc(cached, messageID, cache.CompareMessageID)
	if !ok {
		return false
	}

	from, to := windowAround(len(cached), i, historyPageSize()/2)
	a.mountJumpWindow(cached[from:to], messageID)

	return true
}

// windowAround is the slice of n messages to mount around index i, span either
// side. It is a function of its own because the clamping is the whole of it: a
// window running off either end must still contain the message the jump was
// for, that being the one thing a jump cannot get wrong.
func windowAround(n, i, span int) (from, to int) {
	return max(i-span, 0), min(i+span+1, n)
}

// loadJumpWindow fetches the page around a message the cache cannot answer for
// and mounts it. What comes back is deliberately not cached — see
// Client.MessagesAround — and is held in a.uncached instead, which is what lets
// the quotes inside the window resolve without a request each.
//
// The column is left alone until the page lands. Blanking it for a status line
// would throw away wherever the reader had scrolled to, which is the one thing a
// failed jump must not cost them: a message that cannot be fetched was almost
// always deleted, so the notice is the whole of what happens.
func (a *App) loadJumpWindow(channelID, messageID string) {
	if a.loadingPage {
		return
	}
	a.loadingPage = true
	epoch := a.epoch

	go func() {
		page, err := a.client.MessagesAround(channelID, messageID, historyPageSize())

		a.doOnUI(func() {
			a.loadingPage = false
			if a.stale(epoch) || a.currentChannelID != channelID {
				return
			}
			if err != nil || len(page) == 0 {
				if err != nil {
					logPageError("jump to message "+messageID, err)
				}
				a.notify(ui.ToneWarning, "Couldn't find that message.")
				return
			}

			a.holdUncached(page)
			a.mountJumpWindow(page, messageID)
		}, false)
	}()
}

// mountJumpWindow replaces the column with a window and reveals the message it
// was fetched for. Call on the UI thread.
func (a *App) mountJumpWindow(messages []*domain.Message, targetID string) {
	a.cancelActiveEdit()

	// The way back goes up first: it is in the header, and a header that changed
	// height after the column was measured would move everything measured against
	// it.
	a.setJumped(true)

	a.messageList.Objects = a.buildRun(messages, nil, nil)
	a.remountMessages()

	// The column has grown under a scroller that has not been laid out since, and
	// an offset is clamped against the size the content was last given.
	a.messageScroll.SyncContent()
	a.revealMounted(a.messageWidgetIndex(targetID))
}

// revealMounted scrolls the widget at i to the middle of the viewport and
// flashes it. The offset is a walk of what is above it — affordable here because
// it is once per jump, where the same measurement on the scroll path would be
// once per frame.
func (a *App) revealMounted(i int) {
	objects := a.messageList.Objects
	if i < 0 || i >= len(objects) || a.messageScroll == nil {
		return
	}

	var top float32
	for _, obj := range objects[:i] {
		top += obj.MinSize().Height
	}

	// Centred rather than pinned to the top: a message is read with what was said
	// around it, and what was said before it is half of that. The room held for
	// the composer dock is not viewport, so it does not count towards the middle.
	view := a.messageScroll.Size().Height - ui.DockReserve(a.composerDock)
	top -= max(view-objects[i].MinSize().Height, 0) / 2

	a.messageScroll.ScrollToOffset(fyne.NewPos(0, max(top, 0)))

	if w, ok := objects[i].(*ui.MessageWidget); ok {
		w.Flash()
	}
}

// returnToPresent leaves a jump window for the live tail. The cache is where it
// comes back from, that being what the tail is; a channel whose cache has been
// evicted underneath the window asks for it again.
func (a *App) returnToPresent() {
	channelID := a.currentChannelID
	if channelID == "" {
		return
	}

	if len(a.client.Messages().Get(channelID)) > 0 {
		a.displayCached()
		a.focusInput()
		return
	}

	a.showStatus("Loading messages...")
	a.loadChannelMessages(channelID)
}

// setJumped records whether the column is showing a window a jump landed in, and
// shows or hides the way back. The header is relaid out rather than refreshed:
// the chip appearing changes the width the row's trailing slot asks for, which
// only its own layout can give it. Call on the UI thread.
func (a *App) setJumped(jumped bool) {
	a.atOldest = false
	if a.jumped == jumped {
		return
	}
	a.jumped = jumped

	if a.jumpChip == nil {
		return
	}
	if jumped {
		a.jumpChip.Show()
	} else {
		a.jumpChip.Hide()
	}
	ui.Relayout(a.messageHeader)
}

// holdUncached files a fetched window where ResolveMessage can find it, beside
// the quoted messages already held there. It is what lets a reply inside the
// window draw its quote when the quoted message is in the window too, rather
// than asking for each of them again one at a time.
func (a *App) holdUncached(messages []*domain.Message) {
	if len(a.uncached)+len(messages) > maxUncachedMessages {
		a.uncached = make(map[string]*domain.Message, len(messages))
		a.fetchedReplies = make(map[string]bool)
	}

	for _, message := range messages {
		a.uncached[message.ID] = message
	}
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

		if moved := a.mountedMessage(i); moved != nil {
			a.rebuildAt(i, moved)
			continue
		}

		// The run reached the end of the list, so nothing moved up into the seam and
		// the row above it simply stops continuing.
		a.setFollowedByGroup(i-1, false)
	}
}

// refreshMessage rebuilds an updated message's widget in place from its cache
// entry. A message the user is editing is left alone — the rebuild would discard
// their open editor, and the cache already holds the remote update, so the next
// rebuild renders it. Call on the UI thread.
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

	a.rebuildAt(i, message)
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
	// Appended rather than added: Container.Add refreshes the whole column, and
	// remountMessages below is what actually lays the new row out.
	a.messageList.Objects = append(a.messageList.Objects, a.newMessageWidget(prev, message, nil))

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
// three-tier: messages already cached but not mounted prepend synchronously;
// past that an older page comes from the network; and a window standing outside
// the cache altogether asks for its page without writing one.
//
// Which of the last two applies is read off the cache rather than off a flag,
// because both ways of getting there are the same situation. A jump mounts a
// window from wherever somebody was quoted, and deep scrollback runs off the end
// of a cache bounded per channel — in both, the top mounted message is not in
// the cache, and prepending a page to it would leave a hole this same path would
// later mount as though it were history.
//
// Every tier anchors on the oldest mounted message, so cache trimming can never
// cause a refetch loop.
func (a *App) loadMoreHistory() {
	channelID := a.currentChannelID
	if a.loadingPage || channelID == "" {
		return
	}

	top := a.mountedMessage(0)
	if top == nil {
		return
	}

	messages := a.client.Messages()
	cached := messages.Get(channelID)
	i, inCache := slices.BinarySearchFunc(cached, top.ID, cache.CompareMessageID)

	switch {
	case inCache && i > 0:
		a.prependMessages(cached[max(0, i-historyPageSize()):i])
		return
	case !inCache:
		a.loadOlderPage(channelID, top)
		return
	case messages.IsDepleted(channelID):
		return
	}
	a.loadingPage = true

	go func() {
		defer a.doOnUI(func() { a.loadingPage = false }, true)

		older, err := a.client.HistoryBefore(channelID, top.ID, historyPageSize())
		if err != nil {
			logPageError("load history for "+channelID, err)
			return
		}

		a.doOnUI(func() {
			if a.currentChannelID == channelID && a.mountedMessage(0) == top {
				a.prependMessages(older)
			}
		}, true)
	}()
}

// loadOlderPage extends a window that stands outside the cache. atOldest is what
// the cache's own depleted flag is for a window that is not in it: without it,
// resting at the top of a channel's first page would re-ask on every scroll
// event for a page that cannot exist.
func (a *App) loadOlderPage(channelID string, top *domain.Message) {
	if a.atOldest {
		return
	}
	a.loadingPage = true

	go func() {
		defer a.doOnUI(func() { a.loadingPage = false }, true)

		older, err := a.client.MessagesBefore(channelID, top.ID, historyPageSize())
		if err != nil {
			logPageError("load page before "+top.ID, err)
			return
		}

		a.doOnUI(func() {
			if a.currentChannelID != channelID || a.mountedMessage(0) != top {
				return
			}
			if len(older) == 0 {
				a.atOldest = true
				return
			}

			a.holdUncached(older)
			a.prependMessages(older)
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
	// message, so that existing row is the run's neighbour at the seam.
	topMessage, topNext := a.mountedMessage(0), a.mountedMessage(1)
	widgets := a.buildRun(older, nil, topMessage)

	// That row now has a predecessor above it, so re-evaluate its grouping; its
	// successor is unchanged.
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

// mountNewerFromCache re-mounts messages below the bottom-most mounted one — the
// downward counterpart of loadMoreHistory. Scrolling down out of the cache's own
// window never needs the network: trimming only ever drops a channel's oldest
// messages, so everything below a cached row is cached too. A window that is not
// in the cache at all is the case that does, and it is the same one the upward
// path answers for.
func (a *App) mountNewerFromCache() {
	if a.currentChannelID == "" || a.loadingPage {
		return
	}

	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)
	if bottom == nil {
		return
	}

	cached := a.client.Messages().Get(a.currentChannelID)
	i, inCache := slices.BinarySearchFunc(cached, bottom.ID, cache.CompareMessageID)
	if !inCache {
		a.loadNewerPage(a.currentChannelID, bottom)
		return
	}
	if i+1 == len(cached) {
		// The column has caught up with the tail, so live messages mount again and
		// there is nothing left to jump back to.
		a.setJumped(false)
		return
	}

	a.appendMessages(cached[i+1:min(i+1+historyPageSize(), len(cached))], bottom)
}

// loadNewerPage extends a window that stands outside the cache downwards. It
// needs no counterpart to atOldest: a page that comes back empty means the
// bottom row is the newest message there is, which is the live tail, so the
// column goes back to the cache and mounts it.
func (a *App) loadNewerPage(channelID string, bottom *domain.Message) {
	a.loadingPage = true

	go func() {
		defer a.doOnUI(func() { a.loadingPage = false }, true)

		newer, err := a.client.MessagesAfter(channelID, bottom.ID, historyPageSize())
		if err != nil {
			logPageError("load page after "+bottom.ID, err)
			return
		}

		a.doOnUI(func() {
			last := len(a.messageList.Objects) - 1
			if a.currentChannelID != channelID || a.mountedMessage(last) != bottom {
				return
			}
			if len(newer) == 0 {
				a.displayCached()
				return
			}

			// Nothing re-attaches explicitly: a page that reaches into the cached
			// tail leaves the bottom row inside it, and the tier above serves every
			// scroll after that.
			a.holdUncached(newer)
			a.appendMessages(newer, bottom)
		}, true)
	}()
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

	// Appended in one go rather than through Container.Add, which refreshes the
	// whole column per child.
	a.messageList.Objects = append(a.messageList.Objects, a.buildRun(page, bottom, nil)...)

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
	a.setFollowedByGroup(len(a.messageList.Objects)-1, true)
}

// scrollToBottom scrolls the message view to the newest message.
func (a *App) scrollToBottom() {
	if a.messageScroll != nil {
		a.messageScroll.ScrollToBottom()
	}
}

// remountMessages repaints the column after the *set* of mounted widgets changed.
//
// Container.Refresh would refresh every child, and widget.RichText's Refresh
// re-wraps its text — so announcing one arrival re-flowed every mounted body.
// Only the container's geometry changed, which is what ui.Relayout does.
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
