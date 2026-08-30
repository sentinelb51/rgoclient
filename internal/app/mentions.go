package app

// Mentions: the messages naming this account, and the inbox listing them.
//
// Both halves are here because they are one set read two ways. The sidebar marks
// are a count per channel — an amber bar and a number — and the inbox is the same
// IDs turned back into messages, so a channel gaining a mention has to move both
// and neither is legible without the other.
//
// Only a *direct* mention is recorded. domain.Message.MentionsUser is broader,
// counting @everyone, which is right for washing a row warm and wrong here:
// Revolt stores named users alone, so counting the flag would leave the sidebar
// disagreeing with itself the moment the client reconnected.

import (
	"log"
	"slices"
	"sort"
	"strings"

	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

const (
	// inboxLimit is how many mentions the panel resolves. Each is a request — Revolt
	// offers no route taking a list of IDs — so the newest are fetched and the rest
	// stay counted in the sidebar rather than costing a request apiece.
	inboxLimit = 40

	// whereRunes bounds the address on a row and the name of the group it is
	// filed under. A row's shares its line with the author, and the author is what
	// the row is *about*: unbounded, one long channel name takes the width and
	// leaves the name it was found under as two letters.
	whereRunes = 30
)

/* The set */

// recordMention files a message that names the account and marks the rail. The
// channel's own row is the caller's to repaint — this is the per-message path,
// and its one caller marks the row unread in the same breath. Call on the UI
// thread.
func (a *App) recordMention(message *domain.Message) {
	if !slices.Contains(message.Mentions, a.store.SelfID()) {
		return
	}

	channelID := message.ChannelID
	if slices.Contains(a.mentions[channelID], message.ID) {
		return
	}

	a.mentions[channelID] = append(a.mentions[channelID], message.ID)
	a.syncMentionMarks()
}

// restoreDismissedMentions takes back what was waved off in an earlier run.
// Ready is where it belongs: the stored set is keyed by account, and this is the
// first moment the account is known.
//
// Merged rather than assigned. A dismissal made this run is already in the map
// and is written to the file behind it, so the two hold the same thing — but a
// reconnect must not narrow the set to what the last debounced write happened to
// have reached. Call on the UI thread.
func (a *App) restoreDismissedMentions() {
	for _, messageID := range config.DismissedMentions(a.store.SelfID()) {
		a.dismissedMentions[messageID] = true
	}
}

// keepDismissed is a set of mentions with what the reader has waved off taken
// back out of it. Ready carries the account's whole read state and Revolt keeps
// no record of a dismissal, so re-reading that set is exactly when a dismissed
// mention would come back. Call on the UI thread.
func (a *App) keepDismissed(mentions map[string][]string) map[string][]string {
	kept := make(map[string][]string, len(mentions))

	for channelID, messageIDs := range mentions {
		ids := slices.DeleteFunc(slices.Clone(messageIDs), func(id string) bool {
			return a.dismissedMentions[id]
		})
		if len(ids) > 0 {
			kept[channelID] = ids
		}
	}

	return kept
}

// clearMentions forgets a channel's mentions outright, for the two acts that
// mean the reader has seen everything in it: opening it, and marking it read.
// Reports whether anything went, the callers repainting for their own reasons.
// Call on the UI thread.
func (a *App) clearMentions(channelID string) bool {
	if len(a.mentions[channelID]) == 0 {
		return false
	}

	delete(a.mentions, channelID)
	a.syncMentionMarks()

	return true
}

// pruneMentions drops the mentions read as far as messageID, which is what
// Revolt does to the account's own record: an ack pulls the mentions up to and
// including the message acknowledged, never the whole array. Both ack routes
// populate it — a server-wide one carries each channel's last message — so an
// empty messageID is a guard against a shape neither sends, not a case.
// Call on the UI thread.
func (a *App) pruneMentions(channelID, messageID string) bool {
	pending := a.mentions[channelID]
	if len(pending) == 0 {
		return false
	}
	if messageID == "" {
		return a.clearMentions(channelID)
	}

	// IDs are ULIDs and the slice is kept in order, so what survives is the suffix
	// past the one acknowledged.
	kept := pending[sort.Search(len(pending), func(i int) bool { return pending[i] > messageID }):]
	if len(kept) == len(pending) {
		return false
	}

	if len(kept) == 0 {
		delete(a.mentions, channelID)
	} else {
		a.mentions[channelID] = slices.Clone(kept)
	}
	a.syncMentionMarks()

	return true
}

// forgetMentions drops named messages from a channel's mentions, for the two
// ways one stops being a mention without being read: the message was deleted,
// and the inbox asked for it only to be told it is not there. Reports whether
// anything went. Call on the UI thread.
func (a *App) forgetMentions(channelID string, messageIDs []string) bool {
	pending := a.mentions[channelID]
	if len(pending) == 0 || len(messageIDs) == 0 {
		return false
	}

	kept := slices.DeleteFunc(slices.Clone(pending), func(id string) bool {
		return slices.Contains(messageIDs, id)
	})
	if len(kept) == len(pending) {
		return false
	}

	if len(kept) == 0 {
		delete(a.mentions, channelID)
	} else {
		a.mentions[channelID] = kept
	}
	a.syncMentionMarks()

	return true
}

// forgetLeftServer drops what a departed server's channels were still marked
// with. Both maps are keyed by channel and nothing else names them again: the
// rail icon and the rows are gone, so a mention left behind would light the
// inbox for a channel that cannot be opened. revoltgo evicts the server from
// State but keeps its channels, which is what still answers which server a
// channel was in. Call on the UI thread.
func (a *App) forgetLeftServer(serverID string) {
	for channelID := range a.mentions {
		if channel, ok := a.store.Channel(channelID); ok && channel.ServerID == serverID {
			delete(a.mentions, channelID)
		}
	}
	for channelID := range a.unreadChannels {
		if channel, ok := a.store.Channel(channelID); ok && channel.ServerID == serverID {
			delete(a.unreadChannels, channelID)
		}
	}

	a.syncMentionMarks()
}

// mentionCount is how many messages in a channel name the account.
func (a *App) mentionCount(channelID string) int { return len(a.mentions[channelID]) }

// mentionedServers files the mention set by where each channel lives: which
// servers hold one, and whether a conversation does — the home button wearing
// the same bar a server icon does, so a mention in a direct message is visible
// from inside a server. One walk of the set, which is a handful of channels,
// where asking per icon resolved every server on every rail repaint.
//
// Asked of the store rather than of App.dmChannels: that list is a request the
// home view makes when it is first opened, so a client that started in a server
// has none, and the one mention the button exists for would be the one it could
// not see. Every channel Ready carries is in the store, and one with no server is
// in the home view by definition.
func (a *App) mentionedServers() (marked map[string]bool, home bool) {
	marked = make(map[string]bool, len(a.mentions))
	for channelID := range a.mentions {
		if len(a.mentions[channelID]) == 0 {
			continue
		}
		if serverID := a.store.ChannelServerID(channelID); serverID != "" {
			marked[serverID] = true
		} else {
			home = true
		}
	}

	return marked, home
}

// syncMentionMarks repaints the rail: every server icon, the home button and the
// inbox button. One walk, because a single mention moves at most one server's
// mark and always the inbox's. Call on the UI thread.
func (a *App) syncMentionMarks() {
	marked, home := a.mentionedServers()

	if a.inboxButton != nil {
		a.inboxButton.SetMentioned(len(a.mentions) > 0)
	}
	if a.homeButton != nil {
		a.homeButton.SetMentioned(home)
	}

	for _, obj := range a.serverList.Objects {
		if w, ok := obj.(*ui.ServerWidget); ok {
			w.SetMentioned(marked[w.Server.ID])
		}
	}
}

/* The inbox */

// showMentions opens the inbox. The panel goes up saying it is loading: the rows
// are messages the client holds only the IDs of, so every one of them is a
// request.
func (a *App) showMentions() {
	dialog := ui.NewMentionsDialog(a.deps(), a.loadMoreMentions, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.inbox = dialog // after showOverlay, which clears whatever was there
	a.inboxSeq++
	a.mentioned = nil
	a.inboxMore, a.inboxPaging = false, false

	a.loadMentions()
}

// closeMentions forgets the panel. Only closeOverlay calls it — the layer holds
// one thing at a time, so anything else opening takes this one down.
func (a *App) closeMentions() {
	a.inbox = nil
	a.mentioned = nil
	a.inboxMore, a.inboxPaging = false, false
}

// loadMentions resolves the newest mentions and fills the panel. inboxSeq drops
// an answer that a later opening has already overtaken, the request being long
// enough that a panel closed and reopened would otherwise fill twice.
func (a *App) loadMentions() {
	targets, channels, more := a.mentionTargets("", inboxLimit)
	if len(targets) == 0 {
		a.inbox.SetGroups(nil)
		a.repositionOverlay()

		return
	}
	a.inboxMore = more

	seq := a.inboxSeq
	epoch := a.epoch

	go func() {
		messages, gone := a.client.ResolveMessages(targets)

		// Resolved here rather than through ensureAuthor's queue, as the pins panel
		// does it: a webhook or somebody departed would otherwise be a raw ID the panel
		// mounts and fills in a moment later. Each is paired with the server whose
		// member record names them, the rows spanning as many as the mentions do.
		a.client.ResolveAuthors(a.unresolvedAuthors(messages, func(channelID string) string {
			return channels[channelID]
		}))

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			// A mention the server says is not there is not a mention, and forgetting it
			// is the set's business rather than the panel's — a reader who closed the
			// inbox before the answer landed must not be left with marks over messages
			// that cannot be listed. Only what the server answered *for* is dropped, so a
			// request that simply failed leaves the set alone and is reported below.
			a.forgetGone(gone)

			if a.inbox == nil || a.inboxSeq != seq {
				return
			}
			if len(messages) == 0 && len(gone) == 0 {
				log.Print("mentions: nothing resolved")
				a.inbox.Fail("Couldn't load your mentions.")

				return
			}

			a.mentioned = messages
			a.showMentioned()
		}, false)
	}()
}

// forgetGone drops the mentions the inbox was told are not there and repaints
// what counted them. Call on the UI thread.
func (a *App) forgetGone(gone []client.MessageRef) {
	byChannel := make(map[string][]string, len(gone))
	for _, ref := range gone {
		byChannel[ref.ChannelID] = append(byChannel[ref.ChannelID], ref.MessageID)
	}

	for channelID, messageIDs := range byChannel {
		if a.forgetMentions(channelID, messageIDs) {
			a.refreshChannelRow(channelID)
		}
	}
}

// mentionTargets is a page of the newest mentions across every channel, the
// server each channel belongs to, and whether the set holds more past the page.
// Bounded because the set is unbounded — it is whatever has gone unread — and
// each one costs a request.
//
// cursor is the oldest mention already listed, and the page begins strictly
// older than it. Paging by **ID** rather than by an offset into the sorted set:
// the set moves under the panel — forgetGone drops what the server denied, an
// arriving mention is filed at the other end — and an index into a list that has
// shifted is a page with a hole or a repeat in it. Call on the UI thread.
func (a *App) mentionTargets(cursor string, limit int) ([]client.MessageRef, map[string]string, bool) {
	var (
		refs     []client.MessageRef
		channels = make(map[string]string, len(a.mentions))
	)

	for channelID, messageIDs := range a.mentions {
		channels[channelID] = a.channelServerID(channelID)
		for _, messageID := range messageIDs {
			if cursor != "" && messageID >= cursor {
				continue
			}

			refs = append(refs, client.MessageRef{ChannelID: channelID, MessageID: messageID})
		}
	}

	// Newest first, which is both the order the panel wants and the half worth
	// paying for when there are more than the limit.
	slices.SortFunc(refs, func(a, b client.MessageRef) int {
		return strings.Compare(b.MessageID, a.MessageID)
	})

	more := len(refs) > limit
	if more {
		refs = refs[:limit]
	}

	return refs, channels, more
}

// showMentioned fills the open panel from what was resolved, newest first and
// gathered by the server the mention is in. A reader with an unread mention in
// four servers is being told four things, not one list of forty, and the server
// then only has to be said once rather than on every card. Call on the UI thread.
func (a *App) showMentioned() {
	if a.inbox == nil {
		return
	}

	slices.SortFunc(a.mentioned, func(a, b *domain.Message) int {
		return strings.Compare(b.ID, a.ID)
	})

	// A server takes its place the first time one of its channels appears, so the
	// groups are ordered by the newest mention in each as the cards inside them are.
	var (
		order = make([]string, 0, len(a.mentioned))
		cards = make(map[string][]ui.MessageCard, len(a.mentioned))
	)
	for _, message := range a.mentioned {
		serverID := a.channelServerID(message.ChannelID)
		if _, seen := cards[serverID]; !seen {
			order = append(order, serverID)
		}

		cards[serverID] = append(cards[serverID], a.mentionCard(message))
	}

	groups := make([]ui.MentionGroup, 0, len(order))
	for _, serverID := range order {
		groups = append(groups, ui.MentionGroup{Where: a.mentionSource(serverID), Entries: cards[serverID]})
	}
	a.inbox.SetGroups(groups)

	// After SetGroups, which resets the panel outright when nothing resolved — and
	// would take the way to the next page with it.
	a.inbox.SetMore(inboxMoreLabel(a.inboxMore, a.inboxPaging), a.inboxPaging)

	// The card is centred and sized from its own minimum, which a row gained or lost
	// changes; neither re-runs on its own.
	a.repositionOverlay()
}

// inboxMoreLabel is what the way to the next page reads, "" where there is none.
// The inbox is newest-first and only walks backwards, as the pins panel does.
func inboxMoreLabel(more, busy bool) string {
	switch {
	case !more:
		return ""
	case busy:
		return moreBusyLabel
	}

	return "Older mentions"
}

// loadMoreMentions resolves the next page of the set and appends it. Unlike the
// other two panels this pages a list the client already holds — the mentions are
// IDs here and the request is what turns them into messages — so the page is
// taken off the set rather than asked for from a route, and only the new refs
// are resolved: the ones already drawn cost nothing to keep.
//
// Call on the UI thread.
func (a *App) loadMoreMentions() {
	if a.inbox == nil || a.inboxPaging || !a.inboxMore || len(a.mentioned) == 0 {
		return
	}

	// mentioned is sorted newest first by showMentioned, so the last of it is the
	// oldest already listed and the page begins under it.
	cursor := a.mentioned[len(a.mentioned)-1].ID

	targets, channels, more := a.mentionTargets(cursor, inboxLimit)
	if len(targets) == 0 {
		a.inboxMore = false
		a.showMentioned()

		return
	}

	seq := a.inboxSeq
	epoch := a.epoch

	a.inboxPaging = true
	a.showMentioned()

	go func() {
		messages, gone := a.client.ResolveMessages(targets)

		a.client.ResolveAuthors(a.unresolvedAuthors(messages, func(channelID string) string {
			return channels[channelID]
		}))

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			a.forgetGone(gone)

			if a.inbox == nil || a.inboxSeq != seq {
				return
			}
			a.inboxPaging = false

			// Nothing came back and nothing was denied either, so the request failed
			// rather than the page being empty. The cards already up are still the answer,
			// hence a notice instead of the panel's own failure line.
			if len(messages) == 0 && len(gone) == 0 {
				log.Print("mentions: nothing resolved for the next page")
				a.notify(ui.ToneWarning, "Couldn't load more mentions.")
				a.showMentioned()

				return
			}

			a.mentioned = append(a.mentioned, messages...)
			a.inboxMore = more
			a.showMentioned()
		}, false)
	}()
}

// mentionCard is one message as the inbox draws it. The mention edge is dropped:
// this panel is the set of messages naming the account, so an amber card would be
// every card.
func (a *App) mentionCard(message *domain.Message) ui.MessageCard {
	channelID, messageID := message.ChannelID, message.ID

	card := a.messageCard(message)
	card.Where = a.mentionWhere(channelID)
	card.Mentioned = false
	card.Dismiss = func() { a.dismissMention(channelID, messageID) }

	return card
}

// mentionSource names a group: the server its cards are from, or the home view,
// which is one place to the reader however many conversations are in it.
func (a *App) mentionSource(serverID string) string {
	if serverID == "" {
		return "your direct messages"
	}

	server, ok := a.store.Server(serverID)
	if !ok {
		return "another server"
	}

	return util.Truncate(server.Name, whereRunes)
}

// mentionWhere addresses a row inside its group: the channel alone, the group's
// own line having named the server. A direct message says nothing — the group
// said it is one, and the author is the person it is with.
func (a *App) mentionWhere(channelID string) string {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Kind == domain.ChannelDM {
		return ""
	}

	if channel.ServerID == "" {
		return util.Truncate(channel.Name, whereRunes)
	}

	return util.Truncate("#"+channel.Name, whereRunes)
}

/* Being done with one */

// dismissMention takes one mention off: the mark it put in the sidebar, and the
// card in the panel. Nothing is sent — Revolt has no route dropping a single
// mention, an account's record being cleared by acknowledging a message, which
// would mark everything before it read as well. The ID is remembered instead —
// in the map a reconnect's Ready is filtered through, and in the settings file
// behind it, so a restart before the channel is ever opened does not hand the
// mention back either. Opening the channel acknowledges it for real.
// Call on the UI thread.
func (a *App) dismissMention(channelID, messageID string) {
	a.dismissedMentions[messageID] = true
	config.RememberDismissedMention(a.store.SelfID(), messageID)

	if a.forgetMentions(channelID, []string{messageID}) {
		a.refreshChannelRow(channelID)
	}

	a.mentioned = slices.DeleteFunc(a.mentioned, func(message *domain.Message) bool {
		return message.ID == messageID
	})
	a.showMentioned()
}
