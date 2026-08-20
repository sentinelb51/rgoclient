package app

// Channel search: the pins panel with a query — the same request, rows and
// refusal to cache — differing only in asking again on every Enter.
//
// Nothing here is incremental. Revolt's search is a request per query, so the
// field reports on submit rather than per keystroke, and searchQuery holds the
// one in flight: an answer to an older query is dropped, not drawn under a newer.

import (
	"log"
	"strings"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// searchLimit is how many results are asked for. Revolt caps a search at 100 and
// offers no paging, so this is the whole answer or the newest hundred of it.
const searchLimit = 100

/* Opening it */

// showChannelSearch opens the panel for the channel on screen, with the field
// focused: a search that has to be clicked into first is a click nobody meant to
// spend.
func (a *App) showChannelSearch() {
	channelID, ok := a.searchableChannel()
	if !ok {
		return
	}

	dialog := ui.NewSearchDialog(a.deps(), a.channelName(), a.searchMessages, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.search = dialog // after showOverlay, which clears whatever was there
	a.searchChannelID = channelID
	a.window.Canvas().Focus(dialog.Entry)
}

// closeSearch forgets the panel. Only closeOverlay calls it — the layer holds one
// thing at a time, so anything else opening takes this one down.
func (a *App) closeSearch() {
	a.search = nil
	a.searchChannelID = ""
	a.searchQuery = ""
}

/* Asking */

// searchMessages runs one query and fills the panel. Authors are resolved in the
// same worker for the reason loadPinned gives: the search route cannot be asked
// for the users, so every query would draw a column of raw IDs filling in a
// moment later. The query is recorded rather than counted, so an answer to a
// superseded one is dropped — a second Enter mid-flight is the ordinary case, and
// the two can come back in either order.
func (a *App) searchMessages(query string) {
	if a.search == nil {
		return
	}

	// Trimmed here as well as in the client: a query of spaces comes back empty,
	// which reads as "nothing matched" rather than as nothing having been asked.
	query = strings.TrimSpace(query)
	if query == "" {
		a.refillSearch(func() { a.search.Fail("Type something and press Enter.") })
		return
	}

	channelID, serverID := a.searchChannelID, a.channelServerID(a.searchChannelID)
	epoch := a.epoch

	a.searchQuery = query
	a.refillSearch(a.search.Searching)

	go func() {
		messages, err := a.client.SearchMessages(channelID, query, searchLimit)
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.search == nil || a.searchChannelID != channelID || a.searchQuery != query {
				return
			}
			if err != nil {
				log.Printf("search %s: %v", channelID, err)
				a.refillSearch(func() { a.search.Fail("Couldn't search this channel.") })
				return
			}

			a.showResults(messages)
		}, false)
	}()
}

// showResults refills the open panel. Call on the UI thread.
func (a *App) showResults(messages []*domain.Message) {
	entries := make([]ui.MessageEntry, 0, len(messages))
	for _, message := range messages {
		entries = append(entries, a.messageEntry(message))
	}

	a.refillSearch(func() { a.search.SetEntries(entries) })
}

// refillSearch changes the panel and re-places it. Every change is a change of
// height — a query in flight replaces the rows with a line — and the card is
// centred and sized from its own minimum, neither of which re-runs on its own.
// Call on the UI thread.
func (a *App) refillSearch(change func()) {
	change()
	a.repositionOverlay()
}
