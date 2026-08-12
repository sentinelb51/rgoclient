package app

// The emoji picker's contents. Two surfaces open it — the composer's button and a
// message's add-reaction chip — and both ask for the same thing, so what is on
// offer is decided here rather than at either of them.
//
// Nothing is fetched. Ready carries the emoji of every server the account is in
// and revoltgo files the create and delete events into State on the way past, so
// Store.Emojis is already the whole set and already current; a request would only
// be a second copy to keep. That is also why no gateway handler here follows one.

import (
	"fyne.io/fyne/v2"

	"RGOClient/internal/ui"
)

// OnPickEmoji opens the picker beside anchor. Call on the UI thread.
func (a *App) OnPickEmoji(anchor fyne.CanvasObject, onPick func(ui.EmojiChoice)) {
	ui.ShowEmojiPicker(a.deps(), anchor, a.emojiGroups(), onPick)
}

// emojiGroups is what the picker draws, one group per server that defines any,
// the open server first and the unicode set last.
//
// Every server is offered rather than only the open one: Revolt lets an emoji be
// used wherever the account can write, a conversation included, and the whole set
// is already in hand — filtering it to the open server would hide most of it to
// save a walk that has already happened.
//
// One walk, bucketed. Asking per server would be a walk of every emoji per
// server, and the picker is opened from a click.
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

	// The open server first, then the rest in the order the sidebar draws them, so
	// the picker reads the same way the column beside it does.
	order := a.serverIDs
	if a.currentServerID != "" {
		order = append([]string{a.currentServerID}, order...)
	}

	groups := make([]ui.EmojiGroup, 0, len(buckets)+1)
	seen := make(map[string]bool, len(buckets))

	for _, serverID := range order {
		choices := buckets[serverID]
		if len(choices) == 0 || seen[serverID] {
			continue
		}
		seen[serverID] = true

		groups = append(groups, ui.EmojiGroup{Title: a.emojiGroupTitle(serverID), Choices: choices})
	}

	return append(groups, ui.EmojiGroup{Title: "Emoji", Choices: ui.UnicodeEmoji})
}

// emojiGroupTitle names a group. A server the store cannot answer for still gets
// a heading — its emoji are usable either way, and a group with no caption would
// read as belonging to the one above it.
func (a *App) emojiGroupTitle(serverID string) string {
	if server, ok := a.store.Server(serverID); ok && server.Name != "" {
		return server.Name
	}

	return "Server"
}
