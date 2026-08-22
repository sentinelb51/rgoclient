package app

// Channel search: the island in ui/search.go, and the two halves of what it
// asks for.
//
// Nothing here is incremental. Revolt's search is a request per query, so the
// field reports on submit rather than per keystroke, and searchQuery holds the
// one in flight: an answer to an older query is dropped, not drawn under a newer.
//
// The route takes a query, an order and a limit and nothing else, so a filter is
// not part of the request — the answer is held here and narrowed on the way to
// the island. That is what makes a chip free: toggling one redraws a hundred
// cards the client already has, where changing the order has to ask again.

import (
	"log"
	"strings"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// searchLimit is how many results are asked for. Revolt caps a search at 100 and
// offers no paging, so this is the whole answer or the hundred the chosen order
// puts first.
const searchLimit = 100

/* Opening it */

// showChannelSearch opens the island for the channel on screen, with the field
// focused: a search that has to be clicked into first is a click nobody meant to
// spend.
func (a *App) showChannelSearch() {
	channelID, ok := a.searchableChannel()
	if !ok {
		return
	}

	dialog := ui.NewSearchDialog(a.deps(), a.channelName(), a.onSearchChanged, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.search = dialog // after showOverlay, which clears whatever was there
	a.searchChannelID = channelID
	a.searchQuery = ui.SearchQuery{}
	a.searchFound, a.searchAnswered = nil, false

	a.window.Canvas().Focus(dialog.Entry)
}

// closeSearch forgets the island. Only closeOverlay calls it — the layer holds
// one thing at a time, so anything else opening takes this one down.
func (a *App) closeSearch() {
	a.search = nil
	a.searchChannelID = ""
	a.searchQuery = ui.SearchQuery{}
	a.searchFound, a.searchAnswered = nil, false
}

/* Asking */

// onSearchChanged is what the island reports every change through: the field
// submitted, a filter toggled, an order picked. Which of those has to reach the
// network is decided here rather than there — the answer being held here is the
// only reason a filter does not. Call on the UI thread.
func (a *App) onSearchChanged(query ui.SearchQuery) {
	if a.search == nil {
		return
	}

	// Trimmed here as well as in the client: a query of spaces comes back empty,
	// which reads as "nothing matched" rather than as nothing having been asked.
	query.Text = strings.TrimSpace(query.Text)

	// The same request narrowed differently — a chip, and nothing the route was
	// asked with. An answer still on its way needs nothing done to it: it will be
	// drawn through the filters standing when it lands, which are the ones just
	// recorded.
	//
	// The filters having to differ is what keeps a second Enter on an unchanged
	// query a real request: the same question asked again is a reader asking for
	// it again, not a redraw of what is already on screen.
	if query.SameRequest(a.searchQuery) && query.Filters != a.searchQuery.Filters {
		a.searchQuery = query
		if a.searchAnswered {
			a.refillSearch(a.drawSearchResults)
		}

		return
	}

	a.searchQuery = query
	a.searchFound, a.searchAnswered = nil, false

	if query.Text == "" {
		a.refillSearch(a.search.Prompt)
		return
	}

	a.searchMessages(query)
}

// searchMessages runs one query and fills the island. Whatever authors the
// answer does not carry are resolved in the same worker, for the reason
// loadPinned gives. The query is recorded rather than counted, so an answer to a
// superseded one is dropped — a second Enter mid-flight is the ordinary case,
// and the two can come back in either order.
func (a *App) searchMessages(query ui.SearchQuery) {
	channelID, serverID := a.searchChannelID, a.channelServerID(a.searchChannelID)
	epoch := a.epoch

	a.refillSearch(a.search.Searching)

	go func() {
		messages, err := a.client.SearchMessages(channelID, query.Text, query.Sort, searchLimit)
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.search == nil || a.searchChannelID != channelID ||
				!a.searchQuery.SameRequest(query) {
				return
			}
			if err != nil {
				log.Printf("search %s: %v", channelID, err)
				a.refillSearch(func() { a.search.Fail("Couldn't search this channel.") })
				return
			}

			a.searchFound, a.searchAnswered = messages, true
			a.refillSearch(a.drawSearchResults)
		}, false)
	}()
}

/* What comes back */

// drawSearchResults narrows the held answer and hands it over as cards. The
// island is told how many came back as well as how many survived, so the line
// above the well can say what the chips took away. Call on the UI thread.
func (a *App) drawSearchResults() {
	filters := a.searchQuery.Filters

	results := make([]ui.MessageCard, 0, len(a.searchFound))
	for _, message := range a.searchFound {
		if !a.matchesSearch(message, filters) {
			continue
		}

		results = append(results, a.messageCard(message))
	}

	a.search.SetResults(results, len(a.searchFound))
}

// matchesSearch reports whether a message survives the chips. Every one of them
// is a property of the message the route cannot be asked about, which is why
// they are answered against the hundred that came back rather than sent.
func (a *App) matchesSearch(message *domain.Message, filters ui.SearchFilters) bool {
	if !filters.Any() {
		return true
	}

	self := a.store.SelfID()
	switch {
	case filters.Has(ui.FilterFromMe) && (self == "" || message.AuthorID != self):
		return false
	case filters.Has(ui.FilterMentionsMe) && !message.MentionsUser(self):
		return false
	case filters.Has(ui.FilterPinned) && !message.Pinned:
		return false
	case filters.Has(ui.FilterFiles) && len(message.Attachments) == 0:
		return false
	case filters.Has(ui.FilterImages) && imagesIn(message) == 0:
		return false
	case filters.Has(ui.FilterLinks) && !hasLink(message):
		return false
	case filters.Has(ui.FilterReactions) && len(message.Reactions) == 0:
		return false
	}

	return true
}

// imagesIn counts the attachments that are pictures, the kind arriving with the
// file rather than being guessed from its name.
func imagesIn(message *domain.Message) int {
	var images int
	for _, file := range message.Attachments {
		if file.Kind == domain.FileImage {
			images++
		}
	}

	return images
}

// hasLink reports whether the message points anywhere. An embed is the answer
// where Revolt could resolve what was posted; the scan of the body catches the
// rest, embeds being off for some accounts and absent for a link nothing could
// be read from.
func hasLink(message *domain.Message) bool {
	for _, embed := range message.Embeds {
		if embed.URL != "" {
			return true
		}
	}

	return strings.Contains(message.Content, "https://") ||
		strings.Contains(message.Content, "http://")
}

// refillSearch changes the island and re-places it. Every change is a change of
// height — a query in flight replaces the cards with a line — and the island is
// centred and sized from its own minimum, neither of which re-runs on its own.
// Call on the UI thread.
func (a *App) refillSearch(change func()) {
	change()
	a.repositionOverlay()
}
