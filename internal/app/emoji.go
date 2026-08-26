package app

// The emoji picker's contents. Two surfaces open it — the composer's button and a
// message's add-reaction chip — and both ask for the same thing, so what is on
// offer is decided here rather than at either.
//
// Nothing is fetched: Ready carries every server's emoji and revoltgo files
// create/delete into State on the way past, so Store.Emojis is already the whole
// set and already current. What the gateway handler below is left to do is
// re-take the *copy* of it the composer holds — the pop-up walks the store each
// time it opens, the ":" list does not.

import (
	"slices"

	"fyne.io/fyne/v2"

	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

// onEmojisChanged follows an emoji being added to or removed from a server the
// account is in. Only the composer's list needs it: without one an emoji added
// minutes ago is uncompletable, and a deleted one still completes to a token
// that draws as a broken picture.
//
// Queued rather than taken here — uploading a dozen emoji is a dozen events, and
// this is a walk of every emoji the account can reach. Call on the UI thread.
func (a *App) onEmojisChanged() {
	a.queueRefresh(refreshEmojis)
}

// OnPickEmoji opens the picker beside anchor. A non-nil allowed is a message
// restricting what may be reacted to it, and narrows the picker to exactly that
// list — anything else would be refused by the server. Call on the UI thread.
func (a *App) OnPickEmoji(anchor fyne.CanvasObject, allowed []string, onPick func(ui.EmojiChoice)) {
	groups := a.emojiGroups()
	if allowed != nil {
		groups = a.allowedEmojiGroups(allowed)
	}

	ui.ShowEmojiPicker(a.deps(), anchor, groups, onPick)
}

// allowedEmojiGroups is the one group a restricted message offers, in the order
// the message named rather than the picker's own: the list is the author's, and
// short enough that a rail jumping between servers would head nothing.
//
// An entry is a custom emoji's ULID or a character, told apart the way a
// reaction is — Revolt uses one field for both. A custom one the account holds
// no server for still draws, the CDN serving the picture either way; only its
// name is unavailable, and the ID stands in so the cell can still be told from
// its neighbours.
func (a *App) allowedEmojiGroups(allowed []string) []ui.EmojiGroup {
	choices := make([]ui.EmojiChoice, 0, len(allowed))

	for _, value := range allowed {
		if name := a.store.EmojiName(value); name != "" {
			choices = append(choices, ui.EmojiChoice{ID: value, Name: name})
			continue
		}

		if util.IsEmojiID(value) {
			choices = append(choices, ui.EmojiChoice{ID: value, Name: value})
			continue
		}

		choices = append(choices, unicodeChoice(value))
	}

	return []ui.EmojiGroup{{Title: "Allowed here", Choices: choices}}
}

// unicodeChoice names a character out of the built-in set where it is one of
// them, so the search field and the preview line read the same as they do in the
// ordinary picker. Anything else is its own name.
func unicodeChoice(char string) ui.EmojiChoice {
	for _, known := range ui.UnicodeEmoji {
		if known.Char == char {
			return known
		}
	}

	return ui.EmojiChoice{Char: char, Name: char}
}

// refreshEmojiCandidates hands the composer what a typed ":" completes against —
// the same groups the pop-up draws, flattened, so the two cannot offer different
// things in different orders. Called where either changes: the set on ready, on a
// server joined or left and on an emoji added or removed; the order when a server
// is entered. Not from the rail's own rebuild, which is queued for updates that
// change no emoji at all.
//
// Cheap enough for the UI thread: Store.Emojis is a walk of what State already
// holds and no request backs it, which is the same walk opening the picker makes.
func (a *App) refreshEmojiCandidates() {
	groups := a.emojiGroups()

	total := 0
	for _, group := range groups {
		total += len(group.Choices)
	}

	candidates := make([]ui.MentionCandidate, 0, total)
	for _, group := range groups {
		for _, choice := range group.Choices {
			candidates = append(candidates, ui.NewEmojiCandidate(choice))
		}
	}

	a.setMentionCandidates(ui.MentionEmoji, candidates)
}

// emojiGroups is what the picker draws: one group per server that defines any,
// the open server first and the unicode set last. Every server is offered rather
// than only the open one — Revolt lets an emoji be used wherever the account can
// write, and the whole set is already in hand. One walk, bucketed: asking per
// server would be a walk of every emoji per server.
func (a *App) emojiGroups() []ui.EmojiGroup {
	emojis := a.store.Emojis()

	buckets := make(map[string][]ui.EmojiChoice, len(a.serverIDs))
	for _, emoji := range emojis {
		if emoji.ServerID == "" {
			continue // a detached emoji belongs to no server, so no group would name it
		}

		buckets[emoji.ServerID] = append(buckets[emoji.ServerID],
			ui.EmojiChoice{ID: emoji.ID, Name: emoji.Name})
	}

	// The open server first, then the sidebar's own order, so the picker reads the
	// same way the column beside it does.
	order := a.serverIDs
	if a.currentServerID != "" {
		order = slices.Insert(slices.Clone(order), 0, a.currentServerID)
	}

	groups := make([]ui.EmojiGroup, 0, len(buckets)+1)
	seen := make(map[string]bool, len(buckets))

	for _, serverID := range order {
		choices := buckets[serverID]
		if len(choices) == 0 || seen[serverID] {
			continue
		}
		seen[serverID] = true

		groups = append(groups, a.emojiGroup(serverID, choices))
	}

	return append(groups, ui.EmojiGroup{Title: "Emoji", Choices: ui.UnicodeEmoji})
}

// emojiGroup heads one server's emoji with the server: its name for the caption,
// its icon for the rail that jumps between groups. A server the store cannot
// answer for still gets a heading — its emoji are usable either way, and an
// uncaptioned group would read as belonging to the one above.
func (a *App) emojiGroup(serverID string, choices []ui.EmojiChoice) ui.EmojiGroup {
	group := ui.EmojiGroup{ServerID: serverID, Title: "Server", Choices: choices}

	server, ok := a.store.Server(serverID)
	if !ok {
		return group
	}

	if server.Name != "" {
		group.Title = server.Name
	}
	group.IconID, group.IconURL = server.IconID, server.IconURL

	return group
}
