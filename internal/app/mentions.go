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
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

const (
	// inboxLimit is how many mentions the panel resolves. Each is a request — Revolt
	// offers no route taking a list of IDs — so the newest are fetched and the rest
	// stay counted in the sidebar rather than costing a request apiece.
	inboxLimit = 40

	// whereRunes bounds the address on a row. It shares the line with the author,
	// and the author is what the row is *about*: unbounded, one long server name
	// takes the width and leaves the name it was found under as two letters.
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

// mentionCount is how many messages in a channel name the account.
func (a *App) mentionCount(channelID string) int { return len(a.mentions[channelID]) }

// serverMentioned reports whether any of a server's channels holds one. The rail
// says only that, the count being the channel sidebar's to give.
func (a *App) serverMentioned(serverID string) bool {
	server, ok := a.store.Server(serverID)
	if !ok {
		return false
	}

	return slices.ContainsFunc(server.Channels, func(channelID string) bool {
		return len(a.mentions[channelID]) > 0
	})
}

// homeMentioned reports whether a conversation holds one. The home button wears
// the same bar a server icon does, so a mention in a direct message is visible
// from inside a server.
func (a *App) homeMentioned() bool {
	return slices.ContainsFunc(a.dmChannels, func(channelID string) bool {
		return len(a.mentions[channelID]) > 0
	})
}

// syncMentionMarks repaints the rail: every server icon, the home button and the
// inbox button. One walk, because a single mention moves at most one server's
// mark and always the inbox's. Call on the UI thread.
func (a *App) syncMentionMarks() {
	if a.inboxButton != nil {
		a.inboxButton.SetMentioned(len(a.mentions) > 0)
	}
	if a.homeButton != nil {
		a.homeButton.SetMentioned(a.homeMentioned())
	}

	for _, obj := range a.serverList.Objects {
		if w, ok := obj.(*ui.ServerWidget); ok {
			w.SetMentioned(a.serverMentioned(w.Server.ID))
		}
	}
}

/* The inbox */

// showMentions opens the inbox. The panel goes up saying it is loading: the rows
// are messages the client holds only the IDs of, so every one of them is a
// request.
func (a *App) showMentions() {
	dialog := ui.NewMentionsDialog(a.deps(), a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.inbox = dialog // after showOverlay, which clears whatever was there
	a.inboxSeq++

	a.loadMentions()
}

// closeMentions forgets the panel. Only closeOverlay calls it — the layer holds
// one thing at a time, so anything else opening takes this one down.
func (a *App) closeMentions() {
	a.inbox = nil
}

// loadMentions resolves the newest mentions and fills the panel. inboxSeq drops
// an answer that a later opening has already overtaken, the request being long
// enough that a panel closed and reopened would otherwise fill twice.
func (a *App) loadMentions() {
	targets, channels := a.mentionTargets()
	if len(targets) == 0 {
		a.inbox.SetEntries(nil)
		a.repositionOverlay()

		return
	}

	seq := a.inboxSeq
	epoch := a.epoch

	go func() {
		messages := a.client.ResolveMessages(targets)

		// Resolved here rather than through ensureAuthor's queue, as the pins panel
		// does it: a webhook or somebody departed would otherwise be a raw ID the panel
		// mounts and fills in a moment later. Each is paired with the server whose
		// member record names them, the rows spanning as many as the mentions do.
		a.client.ResolveAuthors(a.unknownMentionAuthors(messages, channels))

		a.doOnUI(func() {
			if a.stale(epoch) || a.inbox == nil || a.inboxSeq != seq {
				return
			}
			if len(messages) == 0 {
				log.Print("mentions: nothing resolved")
				a.inbox.Fail("Couldn't load your mentions.")

				return
			}

			a.showMentioned(messages)
		}, false)
	}()
}

// mentionTargets is the newest mentions across every channel, and the server each
// channel belongs to. Bounded by inboxLimit: the set is unbounded — it is
// whatever has gone unread — and each one costs a request. Call on the UI thread.
func (a *App) mentionTargets() ([]client.MessageRef, map[string]string) {
	var (
		refs     []client.MessageRef
		channels = make(map[string]string, len(a.mentions))
	)

	for channelID, messageIDs := range a.mentions {
		channels[channelID] = a.channelServerID(channelID)
		for _, messageID := range messageIDs {
			refs = append(refs, client.MessageRef{ChannelID: channelID, MessageID: messageID})
		}
	}

	// Newest first, which is both the order the panel wants and the half worth
	// paying for when there are more than the limit.
	slices.SortFunc(refs, func(a, b client.MessageRef) int {
		return strings.Compare(b.MessageID, a.MessageID)
	})
	if len(refs) > inboxLimit {
		refs = refs[:inboxLimit]
	}

	return refs, channels
}

// unknownMentionAuthors is who among the resolved messages the store cannot yet
// name, each paired with the server whose member record carries their nickname
// and role colour. Safe off the UI thread — the store's reads are, and the
// channel-to-server map was taken before leaving it.
func (a *App) unknownMentionAuthors(messages []*domain.Message, channels map[string]string) []client.AuthorRef {
	var targets []client.AuthorRef

	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		serverID := channels[message.ChannelID]

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

// showMentioned fills the open panel, newest first. Call on the UI thread.
func (a *App) showMentioned(messages []*domain.Message) {
	slices.SortFunc(messages, func(a, b *domain.Message) int {
		return strings.Compare(b.ID, a.ID)
	})

	// The mention edge is dropped: this panel is the set of messages naming the
	// account, so an amber card would be every card.
	entries := make([]ui.MessageCard, 0, len(messages))
	for _, message := range messages {
		card := a.messageCard(message)
		card.Where = a.mentionWhere(message.ChannelID)
		card.Mentioned = false
		entries = append(entries, card)
	}
	a.inbox.SetEntries(entries)

	// The card is centred and sized from its own minimum, which a row gained or lost
	// changes; neither re-runs on its own.
	a.repositionOverlay()
}

// mentionWhere addresses a row: the channel, prefixed by its server where it has
// one. A direct message is named after the person it is with, who is also the
// author of anything in it that could name this account — so it says what kind of
// place it is instead, the row's own name having said which one.
func (a *App) mentionWhere(channelID string) string {
	channel, ok := a.store.Channel(channelID)
	if !ok {
		return ""
	}

	switch {
	case channel.Kind == domain.ChannelDM:
		return "Direct message"
	case channel.ServerID == "":
		return util.Truncate(channel.Name, whereRunes)
	}

	name := "#" + channel.Name
	if server, ok := a.store.Server(channel.ServerID); ok {
		name = server.Name + " " + name
	}

	return util.Truncate(name, whereRunes)
}
