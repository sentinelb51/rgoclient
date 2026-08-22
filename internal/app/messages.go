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
	"RGOClient/internal/audio"
	"RGOClient/internal/cache"
	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

const (
	initialPageSize = 30 // messages fetched when first opening a channel

	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"
	remountThreshold  = 200 // px from the bottom before trimmed newer rows re-mount

	// settleDelay is how long after mounting a channel's tail the scroll to the
	// bottom is re-issued. The column keeps its bottom as rows are measured, but
	// nothing is measured until the window has given it a width, which on a first
	// layout is a frame away.
	settleDelay = 120 * time.Millisecond

	// editMarkTick is how often a mounted message's "edited N ago" span is
	// rewritten. A minute, because that is the finest span the mark ever names.
	editMarkTick = time.Minute

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

// initialMountCount is how many messages a channel switch puts in the window.
// The column builds widgets only for what is on screen, so this bounds what is
// indexed rather than the work of a switch; scrollback takes the rest from cache.
func initialMountCount() int { return config.Current().Behaviour.InitialMountCount }

// mountedCap is the ceiling on the window during scrollback: prepends past it
// trim rows off the bottom and vice versa, so scrolling through any amount of
// history keeps a constant number held.
func mountedCap() int { return config.Current().Behaviour.MountedCap }

// renderedCap is the same ceiling for live appends, one page lower so a channel
// that is being talked in does not sit permanently at the trim threshold.
func renderedCap() int { return max(mountedCap()-historyPageSize(), 1) }

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
	a.input.OnEscape = a.escapeToPresent
	a.input.OnRefused = func(reason string) { a.notify(ui.ToneWarning, "%s", reason) }
	a.input.OnTyping = a.noteTyping
	a.input.OnKeystroke = a.noteKeystroke
	a.input.OnResize = a.resizeDock
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

	// The two buttons ride the entry's last line rather than the middle of it — see
	// ui.NewComposerButtonSlot, which is what decides where in the row they land.
	a.composerEntry = ui.NewFillRow(0,
		ui.WithCaret(a.input),
		ui.NewComposerButtonSlot(a.input.EmojiButton),
		ui.NewComposerButtonSlot(a.input.AttachButton),
	)

	// The notice replaces the row rather than joining it — see ui.ComposerNotice.
	a.composerNotice = ui.NewComposerNotice()

	// Everything above the entry is one block with a gap around it and between its
	// rows — see ui.NewGapBlock. ComposerPaddingV is what the card is worth around
	// the *entry*, which brings InnerPadding of its own; a reply card brings none
	// and sat against the card's top edge with the text hard under it. The block
	// costs nothing at all while all three rows are hidden, so an empty composer is
	// the height it always was.
	inner := ui.VBoxNoSpacing(
		ui.NewGapBlock(theme.Sizes.ComposerRowGap,
			a.input.Mentions,
			a.input.ReplyContainer,
			a.input.AttachmentContainer,
		),
		a.composerEntry,
		a.composerNotice,
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

	// The way back to the live tail is a bar of its own between the two, spanning
	// the card's width: what it reports is where the whole column is standing,
	// where the badges beside it report something about the channel.
	a.jumpBar = ui.NewJumpBar(a.backToPresent)
	a.jumpBar.OnResize = a.resizeDock
	a.composerDock = ui.VBoxNoSpacing(badgeRow, a.jumpBar, card)

	// The column is virtualised — see ui.MessageList: the window is data, and only
	// what is on screen has a widget. OnMount is where a row's lazy lookups go.
	a.messages = ui.NewMessageList(a.deps(), a.composerDock)
	a.messages.OnMount = a.onMessageMounted
	a.messages.OnScroll = func() {
		a.syncJumpBar()
		if a.messages.AtTop() {
			a.loadMoreHistory()
			return
		}
		if a.messages.FromBottom() <= remountThreshold {
			a.mountNewerFromCache()
		}
	}
	a.clearMessages()

	a.channelHeader = widget.NewLabelWithStyle(a.channelName(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.channelGlyph = container.NewStack(ui.ChannelGlyph(a.channelKind()))
	title := container.NewHBox(a.channelGlyph, a.channelHeader)

	// The ways into the two message panels sit beside the member toggle: all three
	// are about the channel on screen rather than about anything in the column,
	// and a pin's own mark is what the message row already carries.
	search := ui.NewIconButton(assets.SearchIcon, a.showChannelSearch, nil)
	pins := ui.NewIconButton(assets.SystemPinnedIcon, a.showPinnedMessages, nil)

	members := ui.NewIconButton(assets.MembersIcon, a.toggleMemberList, nil)

	// The topic takes the row's centre, which is the only slot with width to give:
	// it is somebody else's prose and shortens to whatever is left between the name
	// and the buttons.
	a.channelTopic = ui.NewChannelTopic()
	a.messageHeader = container.NewBorder(nil, nil, title,
		container.NewHBox(search, pins, members), a.channelTopic)
	a.syncChannelTopic() // a restyle rebuilds this row under a standing selection
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

	a.floatingDock = ui.NewFloatingDock(a.messages, a.composerDock)
	a.messageColumn = ui.NewFillColumn(2, header, a.channelNote, a.floatingDock)

	floor := fyne.NewSize(theme.Sizes.MessageAreaMinWidth, theme.Sizes.MessageAreaMinHeight)
	return ui.NewFixedSizeContainer(floor, container.NewStack(background, a.messageColumn))
}

// resizeDock re-hangs the floating stack after something in it appeared or
// disappeared. Fyne re-lays out for a growing minimum but leaves a shrinking one
// reserved, and the stack stands on its own height twice over: the card is placed
// from the bottom up, and ui.DockReserve is what it costs the message column. The
// column re-reads that room, it being part of the height it scrolls through.
// Call on the UI thread.
func (a *App) resizeDock() {
	ui.Relayout(a.floatingDock)
	a.messages.Relayout()
}

/* Composing */

// The composer's placeholder, and the two reasons a channel gives for taking no
// message. Without SendMessage the entry is hidden outright and ui.ComposerNotice
// carries the reason in its place.
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

	reason := ""
	switch {
	case !known || permissions.Has(domain.PermissionSendMessage):
		a.input.SetPlaceHolder(composerPlaceholder)
	case !a.canViewChannel(channel):
		reason = composerNoAccess
	default:
		reason = composerNoSending
	}

	if a.composerNotice == nil || a.composerEntry == nil {
		return
	}

	a.composerNotice.Set(reason)
	if reason == "" {
		a.composerEntry.Show()
	} else {
		a.composerEntry.Hide()
	}
	a.resizeDock()
}

// OnAttachFile asks for a file to hang on the next message. No filter: what a
// channel takes is the server's call, and a picture is only one of the kinds.
// Call on the UI thread.
func (a *App) OnAttachFile(onPicked func(path string)) {
	a.chooseFile("Choose a file to attach", ui.FileFilter{}, func(path, _ string) { onPicked(path) })
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

	// Sounded here rather than on the echo: this is feedback for the key that was
	// just pressed, and a round trip later it would land on whatever the user had
	// moved on to. A refusal is announced by its own notice.
	a.playSound(audio.Send)

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

// toReplies drops the composer's own bookkeeping — which channel each quoted
// message lives in, needed only to draw its preview — leaving what is sent.
func toReplies(pending []ui.Reply) []domain.Reply {
	replies := make([]domain.Reply, len(pending))
	for i, reply := range pending {
		replies[i] = domain.Reply{ID: reply.ID, Mention: reply.Mention}
	}

	return replies
}

/* Mounting a row */

// onMessageMounted runs as the column builds a row's widget — every time, a row
// scrolled out of the overscan being rebuilt on its way back. It is where an
// unresolved author is chased down: ensureAuthor is a no-op once State knows the
// user, so it costs two map lookups per row in the common case.
//
// A system event names whoever it is about rather than an author, so it is the
// *target* that gets chased, and ui.MessageWidget.Author answers with it — which
// lets one refresh pass cover both. Only where the target is somebody: see
// domain.SystemMessage.TargetsUser. A grouped continuation draws no quotes, so
// nothing is queued for one; an edited row needs the clock that rewrites its mark.
func (a *App) onMessageMounted(message *domain.Message, grouped bool) {
	switch {
	case message.System != nil:
		if message.System.TargetsUser() {
			a.ensureAuthor(a.channelServerID(message.ChannelID), message.System.Target)
		}
	case message.Webhook == nil:
		a.ensureAuthor(a.channelServerID(message.ChannelID), message.AuthorID)
	}

	if !grouped {
		a.ensureReplies(message)
	}
	if message.Edited != nil {
		a.armEditMarks()
	}
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
		fetched, _ := a.client.ResolveMessages(pending)

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

			if a.messages != nil {
				a.messages.EachMounted(func(w *ui.MessageWidget) { w.RefreshReplies(resolved) })
			}
		}, false)
	}()
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

// syncChannelTopic labels the header with what the open channel says it is for.
// The header is relaid out rather than refreshed: a topic appearing or going
// takes room off the name beside it, which a repaint alone would not give back.
// Call on the UI thread.
func (a *App) syncChannelTopic() {
	if a.channelTopic == nil {
		return
	}

	channel, _ := a.currentChannel()
	a.channelTopic.Set(channel.Description)
	ui.Relayout(a.messageHeader)
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

	a.messages.SetMessages(messages)
	a.settleAtBottom()
}

// settleAtBottom scrolls to the newest message and again once the column has
// settled, so a switch into a channel lands at the live tail rather than a
// screen short of it. Call on the UI thread.
func (a *App) settleAtBottom() {
	a.scrollToBottom()

	channelID, epoch := a.currentChannelID, a.epoch
	if a.settleTimer != nil {
		a.settleTimer.Stop()
	}

	a.settleTimer = time.AfterFunc(settleDelay, func() {
		a.doOnUI(func() {
			// Not if the reader has moved in the meantime: a jump or a scroll up
			// within the window is a position they chose.
			if a.stale(epoch) || a.currentChannelID != channelID || a.jumped {
				return
			}

			a.scrollToBottom()
		}, false)
	})
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
	if a.messages != nil {
		a.messages.ShowStatus(ui.NewMessageStatus(mark, text))
	}
	a.syncJumpBar()
}

// clearMessages empties the message list. Any tooltip goes with it, as the
// server sidebar's does on a rebuild: a reaction chip naming who is in it is
// about to be dropped, so it will never report the pointer leaving.
func (a *App) clearMessages() {
	a.cancelActiveEdit()
	a.setJumped(false)
	a.tooltip.Hide()
	if a.messages != nil {
		a.messages.Clear()
	}
	a.syncJumpBar()
}

// jumpToLatest brings the view back to the newest message: a plain scroll when
// the live tail is mounted, a re-render when scrollback has trimmed it away.
func (a *App) jumpToLatest() {
	cached := a.client.Messages().Get(a.currentChannelID)
	bottom := a.messages.Message(a.messages.Len() - 1)

	if len(cached) == 0 || (bottom != nil && bottom.ID == cached[len(cached)-1].ID) {
		a.scrollToBottom()
		return
	}

	a.displayCached()
}

/* Jumping to a message */

// OnJumpToMessage brings a quoted message into view. Three answers, cheapest
// first: it may already be in the window, it may be in the channel's cached tail,
// and otherwise it is somewhere further back and the page around it is a request.
//
// Only the open channel is ever asked about. A reply names a message in the
// channel it was written in, so the two are the same by construction — and a
// jump that could also change channel would have to wait on that channel's own
// first page before it knew where to land.
func (a *App) OnJumpToMessage(channelID, messageID string) {
	if channelID != a.currentChannelID || messageID == "" {
		return
	}
	if a.revealMessage(messageID) || a.jumpWithinCache(messageID) {
		return
	}

	a.loadJumpWindow(channelID, messageID)
}

// revealMessage centres a message the window already holds and flashes it,
// reporting whether it was there. Call on the UI thread.
func (a *App) revealMessage(messageID string) bool {
	if !a.messages.Reveal(messageID) {
		return false
	}

	// The row is on screen now, so it has a widget to wash. Hovering stops it, the
	// pointer arriving being the reader having found the row.
	if w := a.messages.Mounted(messageID); w != nil {
		w.Flash()
	}

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

	a.messages.SetMessages(messages)
	a.revealMessage(targetID)
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

// setJumped records whether the column is showing a window a jump landed in.
// That is one of the two ways of not being at the live tail, so the bar offering
// the way back is asked again rather than told. Call on the UI thread.
func (a *App) setJumped(jumped bool) {
	a.atOldest = false
	if a.jumped == jumped {
		return
	}
	a.jumped = jumped

	a.syncJumpBar()
}

// viewingOlder reports whether the column is showing anything but the live tail:
// the window a jump landed in, or scrollback far enough up that a message
// arriving would land off screen. The same tolerance an append counts as being
// at the bottom, so the two cannot disagree about where the reader is.
func (a *App) viewingOlder() bool {
	if a.jumped {
		return true
	}
	if a.messages == nil {
		return false
	}

	return a.messages.FromBottom() > atBottomTolerance
}

// syncJumpBar matches the bar over the composer to that answer. Every path that
// can move it calls this — the scroll, a jump, a message arriving, a column
// replaced outright — because the answer is a position and no one of them owns
// it. Call on the UI thread.
func (a *App) syncJumpBar() {
	if a.jumpBar == nil {
		return
	}

	a.jumpBar.Set(a.viewingOlder())
}

// backToPresent is what tapping that bar does. A jump window is left through the
// cache; plain scrollback is a scroll, or a re-render when it has trimmed the
// live tail away.
func (a *App) backToPresent() {
	if a.jumped {
		a.returnToPresent()
		return
	}

	a.jumpToLatest()
}

// escapeToPresent is Escape doing what tapping the bar does, and nothing at all
// when the bar is down — Escape in a column already at the tail should not move
// it, and it still has to reach the entry that pressed it.
func (a *App) escapeToPresent() {
	if !a.viewingOlder() {
		return
	}

	a.backToPresent()
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

// removeMessages takes deleted messages out of the window in one pass. The
// column re-derives the grouping at every seam a removal leaves, so a
// continuation whose group head was deleted regains its header. A moderation
// sweep deletes a whole run at once, and one ID at a time would relayout the
// column once per message. Call on the UI thread.
//
// The mention set is answered first and for *any* channel: a deleted message
// naming the account is nothing the inbox can ever resolve, and one deleted in a
// channel nobody is looking at is exactly the one that would go on being counted.
func (a *App) removeMessages(channelID string, messageIDs []string) {
	if len(messageIDs) == 0 {
		return
	}
	if a.forgetMentions(channelID, messageIDs) {
		a.refreshChannelRow(channelID)
	}

	if channelID != a.currentChannelID {
		return
	}

	doomed := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		doomed[id] = true
	}

	if a.editing != nil && doomed[a.editing.Message().ID] {
		a.editing = nil // the editor goes with its row
	}
	a.messages.Remove(doomed)
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

	i := a.messages.Index(messageID)
	if i == -1 {
		return
	}

	message := a.client.Messages().Find(channelID, messageID)
	if message == nil {
		return
	}

	// Read off the window's own copy before it is replaced: an update is delivered
	// as "this message changed", and only the stamp says which of the things that
	// can change did. A reaction or an unfurled embed must not flash the row.
	edited := newlyEdited(a.messages.Message(i), message)

	a.messages.Replace(message)

	if edited && a.messages.InView(messageID) {
		if w := a.messages.Mounted(messageID); w != nil {
			w.FlashEdit()
		}
	}
}

// newlyEdited reports whether next carries an edit the mounted copy did not. A
// message the widget was built before the account joined has no previous copy to
// compare, which counts as no news.
func newlyEdited(previous, next *domain.Message) bool {
	if next.Edited == nil || previous == nil {
		return false
	}

	return previous.Edited == nil || !previous.Edited.Equal(*next.Edited)
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
	for _, message := range slices.Backward(cached) {
		if message.AuthorID == self && message.System == nil && message.Content != "" {
			a.OnEdit(message)
			return
		}
	}
}

/* The window */

// Which slice of a channel's cached messages the column holds, and how that
// window slides as the user scrolls. displayMessages opens it at the live tail;
// everything below moves it. Holding is not drawing: ui.MessageList builds
// widgets only for the rows on screen, so the cap bounds what is indexed, and a
// dirty frame costs the viewport whatever the cap is.
//
// Invariants:
//   - The window is oldest-first, matching the cache's own order.
//   - The window never exceeds mountedCap messages, in either scroll direction.
//   - Scrolling down never needs the network: trimming only ever drops a
//     channel's oldest messages, so everything below the window is still cached.

// appendMessage adds a freshly received message, trimming the oldest widget when
// over the render cap and keeping the scroll position stable. prev is the
// message's predecessor in its channel, captured when the message was cached.
func (a *App) appendMessage(message, prev *domain.Message) {
	if a.currentChannelID == "" {
		return
	}

	// When scrollback has trimmed the newest rows the view is detached from the
	// live tail: don't mount, since the predecessor isn't in the window and the
	// row would render against the wrong neighbour. The message is cached and
	// mounts via mountNewerFromCache on the way back down. A status line may be
	// showing instead, which the first real message replaces.
	bottom := a.messages.Message(a.messages.Len() - 1)
	if bottom != nil && prev != nil && bottom.ID != prev.ID {
		return
	}

	atBottom := a.messages.AtBottom(atBottomTolerance)

	a.messages.Append([]*domain.Message{message})
	if over := a.messages.Len() - renderedCap(); over > 0 {
		a.messages.TrimTop(over)
	}

	if atBottom {
		a.scrollToBottom()
		return
	}

	a.syncJumpBar() // the column grew under a reader who is not at the bottom of it
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

	top := a.messages.Message(0)
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
			if a.currentChannelID == channelID && a.messages.Message(0) == top {
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
			if a.currentChannelID != channelID || a.messages.Message(0) != top {
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

	a.messages.Prepend(older)
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

	bottom := a.messages.Message(a.messages.Len() - 1)
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

	a.appendMessages(cached[i+1 : min(i+1+historyPageSize(), len(cached))])
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
			if a.currentChannelID != channelID || a.messages.Message(a.messages.Len()-1) != bottom {
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
			a.appendMessages(newer)
		}, true)
	}()
}

// appendMessages mounts newer messages below the current view, preserving the
// scroll position and trimming the top past mountedCap.
func (a *App) appendMessages(page []*domain.Message) {
	if len(page) == 0 {
		return
	}

	a.messages.Append(page)
	if over := a.messages.Len() - mountedCap(); over > 0 {
		a.messages.TrimTop(over)
	}
}

// trimMountedBottom drops rows far below the viewport after a prepend; they
// re-mount via mountNewerFromCache on the way back down. It stops when the
// would-be bottom row is no longer cached: the cache window ends at the live tail,
// so once scrollback runs past its cap the downward remount could not re-serve
// trimmed rows, and the window is allowed to grow instead.
func (a *App) trimMountedBottom() {
	over := a.messages.Len() - mountedCap()
	if over <= 0 {
		return
	}

	newBottom := a.messages.Message(a.messages.Len() - over - 1)
	if newBottom == nil || a.client.Messages().Find(a.currentChannelID, newBottom.ID) == nil {
		return
	}

	a.messages.TrimBottom(over)
}

// scrollToBottom scrolls the message view to the newest message.
func (a *App) scrollToBottom() {
	if a.messages != nil {
		a.messages.ScrollToBottom()
	}
	a.syncJumpBar()
}

/* The edit marks' clock */

// armEditMarks starts the clock rewriting the mounted rows' "edited N ago" spans
// if it is not already running. Called as a row carrying a mark is mounted, so
// one can never be on screen without it. Call on the UI thread.
func (a *App) armEditMarks() {
	if a.editMarkTimer == nil {
		a.refreshEditMarks()
	}
}

// refreshEditMarks rewrites those spans and re-arms while any row still carries
// one, so a channel with none costs nothing. Nothing else redraws a message that
// has stopped changing: without this a mark written as "just now" would still say
// it an hour later. Call on the UI thread.
func (a *App) refreshEditMarks() {
	a.editMarkTimer = nil

	var marked bool
	if a.messages != nil {
		a.messages.EachMounted(func(w *ui.MessageWidget) {
			if w.RefreshEditMark() {
				marked = true
			}
		})
	}

	if !marked {
		return
	}

	a.editMarkTimer = time.AfterFunc(editMarkTick, func() {
		a.doOnUI(a.refreshEditMarks, false)
	})
}
