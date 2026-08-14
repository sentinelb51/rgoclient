package app

// The pinned-messages panel — the one surface that shows a channel's pins as a
// set rather than one mark on one row. Pinning has been built for some time and
// there was nowhere to see the result of it.
//
// Unlike the friends list this is a request: a pin is a flag on the message, and
// Revolt publishes no collection of them, so Client.PinnedMessages searches the
// channel for them. What comes back is held here for as long as the panel is up
// and is not put in the message cache — a pin reaches as far back as anybody
// cared to keep something, and the cache is a channel's contiguous tail.

import (
	"log"
	"slices"
	"strings"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

const (
	// pinsLimit is how many pins are asked for. Revolt caps a search at 100 and
	// there is no paging through them, so this is the whole list or the newest
	// hundred of it.
	pinsLimit = 100

	// pinPreviewRunes is how much of a message a row summarises. Long enough to
	// recognise which pin it is, short enough that the row stays one line.
	pinPreviewRunes = 120
)

/* Opening it */

// showPinnedMessages opens the panel for the channel on screen. The list arrives
// afterwards, so the panel goes up saying it is loading rather than the click
// doing nothing until the request lands.
func (a *App) showPinnedMessages() {
	channelID := a.currentChannelID
	if channelID == "" {
		return
	}

	// The search is refused without this, and a panel that could only report that
	// refusal is worse than the notice saying it before anything opens.
	if !a.store.Permissions(channelID).Has(domain.PermissionReadMessageHistory) {
		a.notify(ui.ToneWarning, "You can't read this channel's history.")
		return
	}

	dialog := ui.NewPinsDialog(a.deps(), a.channelName(), a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.pins = dialog // after showOverlay, which clears whatever was there
	a.pinsChannelID = channelID
	a.pinned = nil

	a.loadPinned(channelID)
}

// closePins forgets the panel. Only closeOverlay calls it — the layer holds one
// thing at a time, so anything else opening takes this one down.
func (a *App) closePins() {
	a.pins = nil
	a.pinsChannelID = ""
	a.pinned = nil
}

// loadPinned fetches the list and fills the panel with it. The authors are
// resolved in the same worker rather than through ensureAuthor's queue: the
// search route cannot be asked for the users (see Client.PinnedMessages), and a
// panel that mounted a column of raw IDs and filled them in a moment later would
// be doing that on every open.
func (a *App) loadPinned(channelID string) {
	serverID := a.channelServerID(channelID)
	epoch := a.epoch

	go func() {
		messages, err := a.client.PinnedMessages(channelID, pinsLimit)
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.pins == nil || a.pinsChannelID != channelID {
				return
			}
			if err != nil {
				log.Printf("pinned messages %s: %v", channelID, err)
				a.pins.Fail("Couldn't load the pinned messages.")
				return
			}

			a.pinned = messages
			a.showPinned()
		}, false)
	}()
}

// unknownAuthors is who among a page of messages the store cannot yet name. Safe
// off the UI thread: the store's reads are, and unlike ensureAuthor this touches
// none of the controller's own guards — the panel is opened by a click and asks
// once, so a second resolution of the same person is not worth a map for.
func (a *App) unknownAuthors(serverID string, messages []*domain.Message) []client.AuthorRef {
	var targets []client.AuthorRef

	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		userID := message.AuthorID
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true

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

	// Asked once for the whole list rather than per row: it is one walk of the
	// channel's roles, and every row would get the same answer.
	manage := a.store.Permissions(a.pinsChannelID).Has(domain.PermissionManageMessages)

	entries := make([]ui.PinEntry, 0, len(a.pinned))
	for _, message := range a.pinned {
		entries = append(entries, a.pinEntry(message, manage))
	}
	a.pins.SetEntries(entries)

	// The card is centred and sized from its own minimum, and a row gained or lost
	// changes that minimum; neither re-runs on its own.
	a.repositionOverlay()
}

// pinEntry builds one row. The panel closes on the way to a message: a jump moves
// the column underneath it, and a panel left over what it just led to would be
// covering the thing the reader asked to see.
func (a *App) pinEntry(message *domain.Message, manage bool) ui.PinEntry {
	author := a.store.MessageAuthor(message)
	channelID, messageID := message.ChannelID, message.ID

	entry := ui.PinEntry{
		Author:    author.Name,
		AvatarURL: author.AvatarURL,
		Preview:   pinPreview(message),
		When:      pinWhen(messageID),

		Jump: func() {
			a.closeOverlay()
			a.OnJumpToMessage(channelID, messageID)
		},
	}

	if manage {
		entry.Unpin = func() { a.unpinFromPanel(message) }
	}

	return entry
}

// pinPreview flattens a message onto one line. A pin need not be text at all —
// a picture is one of the things most worth keeping — so a body with nothing in
// it says what it carries instead of drawing an empty row.
func pinPreview(message *domain.Message) string {
	if content := strings.Join(strings.Fields(message.Content), " "); content != "" {
		return util.Truncate(content, pinPreviewRunes)
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

// pinWhen dates a row off the message ID, ULIDs carrying the instant they were
// made. An ID that cannot be parsed leaves the slot empty rather than dating the
// row to the epoch.
func pinWhen(messageID string) string {
	when, err := util.Timestamp(messageID)
	if err != nil {
		return ""
	}

	return util.NiceTime(when)
}

/* Taking one off */

// unpinFromPanel unpins from the list rather than from the message. Nothing is
// dropped before the server agrees, as everywhere else, and the row goes when it
// has: the panel is a snapshot, so nothing else would take it away.
//
// It is not confirmed. Every other destructive action here cannot be undone by
// repeating it, and this one is a click away from being put back.
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
