package app

// The pinned-messages panel — the one surface showing a channel's pins as a set
// rather than one mark on one row.
//
// It is a request: a pin is a flag on the message and Revolt publishes no
// collection of them, so Client.PinnedMessages searches the channel. What comes
// back is held here while the panel is up and never enters the message cache — a
// pin reaches as far back as anybody cared to keep something, and the cache is a
// channel's contiguous tail.

import (
	"log"
	"slices"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

const (
	// pinsLimit is how many pins one request asks for. Revolt caps a search at 100,
	// so a full page is the ceiling rather than the end of the list — the rest is
	// asked for through loadMorePins.
	pinsLimit = 100

	// previewRunes is how much of a message an island card summarises: enough to
	// recognise it, short enough that its line stays one line. All three surfaces
	// draw the same card, hence the shared name.
	previewRunes = 120
)

/* Opening it */

// showPinnedMessages opens the panel for the channel on screen. The list arrives
// afterwards, so the panel goes up saying it is loading rather than the click
// doing nothing until the request lands.
func (a *App) showPinnedMessages() {
	channelID, ok := a.searchableChannel()
	if !ok {
		return
	}

	dialog := ui.NewPinsDialog(a.deps(), a.channelName(), a.loadMorePins, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.pins = dialog // after showOverlay, which clears whatever was there
	a.pinsChannelID = channelID
	a.pinned = nil

	a.loadPinned(channelID) // which resets the paging state

}

// searchableChannel is the open channel, or false when there is none or the
// account may not read its history. Both panels are one search of that history,
// so both open on this: the request would be refused, and a panel that could only
// report the refusal is worse than a notice saying it before anything opens. Call
// on the UI thread.
func (a *App) searchableChannel() (string, bool) {
	channelID := a.currentChannelID
	if channelID == "" {
		return "", false
	}
	if !a.store.Permissions(channelID).Has(domain.PermissionReadMessageHistory) {
		a.notify(ui.ToneWarning, "You can't read this channel's history.")

		return "", false
	}

	return channelID, true
}

// closePins forgets the panel. Only closeOverlay calls it — the layer holds one
// thing at a time, so anything else opening takes this one down.
func (a *App) closePins() {
	a.pins = nil
	a.pinsChannelID = ""
	a.pinned = nil
	a.pinsMore, a.pinsPaging = false, false
}

// loadPinned fetches the list and fills the panel. The search carries its own
// users, so what is left to resolve is usually nothing — but it is resolved in
// the same worker rather than through ensureAuthor's queue: a webhook or somebody
// departed would otherwise be a raw ID the panel mounts and fills in a moment
// later.
//
// It bumps pinsSeq, which is what abandons a page already in flight: this is the
// list from the start, and a page of the one it replaces would append to it. Call
// on the UI thread.
func (a *App) loadPinned(channelID string) {
	serverID := a.channelServerID(channelID)
	epoch := a.epoch

	a.pinsSeq++
	a.pinsMore, a.pinsPaging = false, false
	seq := a.pinsSeq

	go func() {
		messages, err := a.client.PinnedMessages(channelID, pinsLimit, "")
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.pins == nil || a.pinsChannelID != channelID || a.pinsSeq != seq {
				return
			}
			if err != nil {
				log.Printf("pinned messages %s: %v", channelID, err)
				a.pins.Fail("Couldn't load the pinned messages.")
				return
			}

			a.pinned = messages
			a.pinsMore = pageWasFull(len(messages), pinsLimit)
			a.showPinned()
		}, false)
	}()
}

// loadMorePins asks for the pins older than the last one held and appends them.
// The panel is newest-first and only ever walks backwards, a pin being a search
// with no query — so the cursor is the oldest card on screen. Call on the UI
// thread.
func (a *App) loadMorePins() {
	if a.pins == nil || a.pinsPaging || !a.pinsMore || len(a.pinned) == 0 {
		return
	}
	channelID := a.pinsChannelID
	serverID := a.channelServerID(channelID)
	cursor := a.pinned[len(a.pinned)-1].ID
	epoch, seq := a.epoch, a.pinsSeq

	a.pinsPaging = true
	a.showPinned()

	go func() {
		messages, err := a.client.PinnedMessages(channelID, pinsLimit, cursor)
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.pins == nil || a.pinsChannelID != channelID || a.pinsSeq != seq {
				return
			}
			a.pinsPaging = false

			if err != nil {
				// What is on screen is still the list; a failure to extend it is a notice
				// rather than the panel's own failure line, which would take the list away.
				log.Printf("pinned messages %s (next page): %v", channelID, err)
				a.notify(ui.ToneWarning, "Couldn't load more pins.")
				a.showPinned()

				return
			}

			added := appendUnseen(&a.pinned, messages)
			a.pinsMore = added > 0 && pageWasFull(len(messages), pinsLimit)
			a.showPinned()
		}, false)
	}()
}

// unknownAuthors is who among a page of messages from one server the store
// cannot yet name.
func (a *App) unknownAuthors(serverID string, messages []*domain.Message) []client.AuthorRef {
	return a.unresolvedAuthors(messages, func(string) string { return serverID })
}

// unresolvedAuthors is the walk every surface listing messages resolves its
// authors by. serverOf names the server a message's channel is in, "" for a DM,
// where there is no member record carrying a nickname or a role colour.
//
// Safe off the UI thread — the store's reads are, and unlike ensureAuthor this
// touches none of the controller's guards: a panel is opened by a click and asks
// once.
func (a *App) unresolvedAuthors(messages []*domain.Message, serverOf func(channelID string) string) []client.AuthorRef {
	var targets []client.AuthorRef

	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		serverID := serverOf(message.ChannelID)

		userID := message.AuthorID
		if userID == "" || seen[serverID+":"+userID] {
			continue
		}
		seen[serverID+":"+userID] = true

		if a.store.HasUser(userID) && (serverID == "" || a.store.HasMember(serverID, userID)) {
			continue
		}
		targets = append(targets, client.AuthorRef{ServerID: serverID, UserID: userID})
	}

	return targets
}

/* What goes in it */

// showPinned refills the open panel from what was fetched. Call on the UI thread.
func (a *App) showPinned() {
	if a.pins == nil {
		return
	}

	// Once for the whole list rather than per row: it is a walk of the channel's
	// roles, and every row would get the same answer.
	manage := a.store.Permissions(a.pinsChannelID).Has(domain.PermissionManageMessages)

	entries := make([]ui.MessageCard, 0, len(a.pinned))
	for _, message := range a.pinned {
		entries = append(entries, a.pinCard(message, manage))
	}
	a.pins.SetEntries(entries)
	a.pins.SetMore(pinsMoreLabel(a.pinsMore, a.pinsPaging), a.pinsPaging)

	// The card is centred and sized from its own minimum, which a row gained or lost
	// changes; neither re-runs on its own.
	a.repositionOverlay()
}

// pinsMoreLabel is what the way to the next page reads, "" where there is none.
// The panel only walks backwards, so the word can say so.
func pinsMoreLabel(more, busy bool) string {
	switch {
	case !more:
		return ""
	case busy:
		return moreBusyLabel
	}

	return "Older pins"
}

// pinCard builds one card: the shared summary, plus the way to take the pin off
// where the account may. The pinned badge is dropped — this panel is the set of
// pinned messages, so a mark on every card of it says nothing.
func (a *App) pinCard(message *domain.Message, manage bool) ui.MessageCard {
	card := a.messageCard(message)
	card.Pinned = false

	if manage {
		card.Unpin = func() { a.unpinFromPanel(message) }
	}

	return card
}

// messageCard summarises one message for an island card, shared by all three
// surfaces — each lists messages the column need not be holding, and each draws
// them the same way. The counts are taken here because the widget is handed a
// value rather than a message: the store is the controller's.
//
// The surface closes on the way to one: a jump moves the column underneath, and
// a card left standing would cover the thing the reader asked to see.
func (a *App) messageCard(message *domain.Message) ui.MessageCard {
	author := a.store.MessageAuthor(message)
	channelID, messageID := message.ChannelID, message.ID

	return ui.MessageCard{
		Author:      author.Name,
		AuthorColor: author.Color,
		AvatarURL:   author.AvatarURL,
		Mark:        author.Mark,

		Preview: a.messagePreview(message),
		When:    messageWhen(messageID),

		Attachments: len(message.Attachments),
		Images:      imagesIn(message),
		Reactions:   len(message.Reactions),

		Links:     hasLink(message),
		Pinned:    message.Pinned,
		Edited:    message.Edited != nil,
		Mentioned: message.MentionsUser(a.store.SelfID()),

		Jump: func() {
			a.closeOverlay()
			a.jumpToMessageIn(channelID, messageID)
		},
	}
}

// messagePreview flattens a message onto one line. Through the parser rather
// than off the raw source: a row has space for what the reader would have seen,
// not for the asterisks behind it or for an emoji's ULID. A pin need not be text
// — a picture is among the things most worth keeping — so an empty body says
// what it carries instead of drawing an empty row.
func (a *App) messagePreview(message *domain.Message) string {
	if content := ui.PreviewText(a.store, message.Content); content != "" {
		return util.Truncate(content, previewRunes)
	}

	switch {
	case len(message.Attachments) == 1:
		return message.Attachments[0].Name
	case len(message.Attachments) > 1:
		return "Attachments"
	case len(message.Embeds) > 0:
		return "Embed"
	default:
		return "No text"
	}
}

// messageWhen dates a row off the message ID, ULIDs carrying the instant they
// were made. An ID that cannot be parsed leaves the slot empty rather than
// dating the row to the epoch.
func messageWhen(messageID string) string {
	when, err := util.Timestamp(messageID)
	if err != nil {
		return ""
	}

	return util.NiceTime(when)
}

/* Taking one off */

// unpinFromPanel unpins from the list rather than from the message. Nothing is
// dropped before the server agrees, and the row goes when it has — the panel is a
// snapshot, so nothing else would take it away. Not confirmed: every other
// destructive action here cannot be undone by repeating it, and this one can.
func (a *App) unpinFromPanel(message *domain.Message) {
	messageID := message.ID

	a.setPinned(message, false, func() { a.dropPinned(messageID) })
}

// dropPinned takes a row out of the open panel. Call on the UI thread.
func (a *App) dropPinned(messageID string) {
	if a.pins == nil {
		return
	}

	a.pinned = slices.DeleteFunc(a.pinned, func(message *domain.Message) bool {
		return message.ID == messageID
	})
	a.showPinned()
}

/* Keeping the three panels current */

// All three surfaces listing messages are *fetched* snapshots — a pin, a search
// result and a mention alike reach further back than the message cache — so none
// is drawn from it or written by the handlers keeping the column right. What
// follows is the little that can be kept current without asking again.
//
// A **deletion** is free, a removal from a slice, and is the staleness worth
// fixing first: a card for a message that no longer exists leads nowhere. A
// **pin** is the pins panel re-asking, which is what that panel is.
//
// An **edit** is bound to the cache, the new content being knowable only for a
// message the cache holds — so a card inside the channel's cached tail follows
// one and an older card keeps the line it was fetched with. Re-asking would be a
// request per keystroke somebody else makes.

// dropPanelMessages takes deleted messages out of whichever panels are holding
// them and redraws. Called for *any* channel, like the mention set it sits
// beside: the inbox lists messages from as many channels as the account is in.
// Call on the UI thread.
func (a *App) dropPanelMessages(channelID string, messageIDs []string) {
	if len(messageIDs) == 0 {
		return
	}

	doomed := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		doomed[id] = true
	}

	gone := func(message *domain.Message) bool {
		return message.ChannelID == channelID && doomed[message.ID]
	}

	if a.pins != nil {
		if kept := slices.DeleteFunc(slices.Clone(a.pinned), gone); len(kept) != len(a.pinned) {
			a.pinned = kept
			a.showPinned()
		}
	}
	if a.search != nil {
		if kept := slices.DeleteFunc(slices.Clone(a.searchFound), gone); len(kept) != len(a.searchFound) {
			a.searchFound = kept
			a.drawSearchResults()
		}
	}
	if a.inbox != nil {
		if kept := slices.DeleteFunc(slices.Clone(a.mentioned), gone); len(kept) != len(a.mentioned) {
			a.mentioned = kept
			a.showMentioned()
		}
	}
}

// refreshPanelMessage re-reads one edited message in whichever panels hold it,
// from the cache and only from the cache — see above. Silently does nothing for a
// message older than the channel's cached tail, which is most pins.
// Call on the UI thread.
func (a *App) refreshPanelMessage(channelID, messageID string) {
	if a.pins == nil && a.search == nil && a.inbox == nil {
		return
	}

	fresh := a.client.Messages().Find(channelID, messageID)
	if fresh == nil {
		return
	}

	// The slices are replaced rather than written into: a card was built from the
	// value at the index, and the widget holding it outlives this call.
	replace := func(held []*domain.Message) ([]*domain.Message, bool) {
		i := slices.IndexFunc(held, func(m *domain.Message) bool {
			return m.ChannelID == channelID && m.ID == messageID
		})
		if i == -1 {
			return held, false
		}

		out := slices.Clone(held)
		out[i] = fresh

		return out, true
	}

	if a.pins != nil {
		if out, moved := replace(a.pinned); moved {
			a.pinned = out
			a.showPinned()
		}
	}
	if a.search != nil {
		if out, moved := replace(a.searchFound); moved {
			a.searchFound = out
			a.drawSearchResults()
		}
	}
	if a.inbox != nil {
		if out, moved := replace(a.mentioned); moved {
			a.mentioned = out
			a.showMentioned()
		}
	}
}

// reloadPins re-asks for the channel's pins, which is the whole of how that panel
// follows a pin made anywhere — it is one search, so there is nothing finer to
// do. Queued rather than called: Revolt sends an event per pin and a moderator
// clearing a channel's pins would otherwise be a request each.
//
// It also re-asks after an unpin made *here*, which dropPinned has already drawn:
// the echo says a pin moved and nothing distinguishes it from somebody else's.
// The cost is one redundant search per settling window, and the alternative is a
// marker for "our own action" that has to be right every time — where being
// wrong leaves the panel stale, which is the thing this exists to fix.
//
// It re-asks from the *first* page, dropping whatever was paged in: a pin has
// moved, so which pin is the hundredth has moved with it, and a page fetched past
// the old boundary belongs to a list that no longer exists. loadPinned bumping
// pinsSeq is what makes one still in flight land on nothing.
// Call on the UI thread.
func (a *App) reloadPins() {
	if a.pins == nil || a.pinsChannelID == "" {
		return
	}

	a.loadPinned(a.pinsChannelID)
}
