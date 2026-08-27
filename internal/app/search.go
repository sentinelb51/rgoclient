package app

// Channel search: the island in ui/search.go, and the two halves of what it
// asks for.
//
// A question is asked whole. Revolt's search is a request per query, so the
// field reports on submit rather than per keystroke, and searchQuery holds the
// one in flight: an answer to an older query is dropped, not drawn under a newer.
// The *answer* is incremental — it caps at a hundred and searchFound accumulates
// the pages after that.
//
// Beyond the query, the order and the limit, the route takes only a window of
// message IDs — no author, no attachment, no reaction — so the span is sent and
// everything else is held here and applied on the way to the island. That is
// what makes a chip free: toggling one redraws the cards the client already has,
// where moving an end of the span has to ask again. That same window is what a
// page walks along, which is why the two cannot both have their way with it and
// the client takes the tighter (pageFrom).

import (
	"log"
	"strings"
	"time"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

// searchLimit is how many results one request asks for. Revolt caps a search at
// 100, so a full page is the ceiling rather than the end of the answer — the way
// past it is another request beginning where this one stopped, which is what
// loadMoreSearch is.
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

	dialog := ui.NewSearchDialog(a.deps(), a.channelName(), a.store.SelfID(), a.onSearchChanged,
		a.loadMoreSearch, a.closeOverlay)
	dialog.OnResize = a.repositionOverlay

	a.showOverlay(dialog.Content)
	a.search = dialog // after showOverlay, which clears whatever was there
	a.searchChannelID = channelID
	a.searchQuery = ui.SearchQuery{}
	a.searchFound, a.searchAnswered = nil, false
	a.searchMore, a.searchPaging = false, false
	a.searchSeq++ // an answer owed to the last opening is not this one's

	a.loadSearchAuthors(channelID)
	a.window.Canvas().Focus(dialog.Entry)
}

// loadSearchAuthors fills the island's author picker with the people this
// channel can be narrowed to. A conversation names its own participants, so that
// answer is already here; a server's membership is a walk, which belongs off the
// UI thread — the sidebar's last one is taken where it is this server's, one walk
// feeding the member list, the composer's mentions and this. Call on the UI
// thread.
func (a *App) loadSearchAuthors(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok {
		return
	}

	serverID := channel.ServerID
	if serverID == "" {
		a.search.SetAuthors(recipientCandidates(a.store, channel))
		return
	}

	// Read on this thread and used on the worker: a published membership is never
	// written into, which is what makes handing the slice over safe.
	members := a.memberCache
	if a.memberCacheServer != serverID {
		members = nil
	}
	epoch := a.epoch

	go func() {
		if members == nil {
			members = a.store.Members(serverID)
		}
		candidates := memberCandidates(members)

		a.doOnUI(func() {
			if a.stale(epoch) || a.search == nil || a.searchChannelID != channelID {
				return
			}

			a.search.SetAuthors(candidates)
		}, false)
	}()
}

// closeSearch forgets the island. Only closeOverlay calls it — the layer holds
// one thing at a time, so anything else opening takes this one down.
func (a *App) closeSearch() {
	a.search = nil
	a.searchChannelID = ""
	a.searchQuery = ui.SearchQuery{}
	a.searchFound, a.searchAnswered = nil, false
	a.searchMore, a.searchPaging = false, false
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

	// The same request narrowed differently — a chip or a person, neither of them
	// something the route was asked with. An answer still on its way needs nothing
	// done to it: it will be drawn through the narrowing standing when it lands,
	// which is the narrowing just recorded.
	//
	// Something having to differ is what keeps a second Enter on an unchanged
	// query a real request: the same question asked again is a reader asking for
	// it again, not a redraw of what is already on screen.
	if query.SameRequest(a.searchQuery) && narrowedApart(query, a.searchQuery) {
		a.searchQuery = query
		if a.searchAnswered {
			a.refillSearch(a.drawSearchResults)
		}

		return
	}

	a.searchQuery = query
	a.searchFound, a.searchAnswered = nil, false
	a.searchMore, a.searchPaging = false, false

	if query.Text == "" {
		a.refillSearch(a.search.Prompt)
		return
	}

	a.searchMessages(query)
}

// narrowedApart reports whether the two queries narrow the same answer
// differently — everything applied *here* rather than sent, which is the whole
// of what SameRequest leaves out.
func narrowedApart(query, previous ui.SearchQuery) bool {
	return query.Filters != previous.Filters || query.AuthorID != previous.AuthorID
}

// searchMessages runs one query and fills the island. Whatever authors the
// answer does not carry are resolved in the same worker, for the reason
// loadPinned gives. The query is recorded rather than counted, so an answer to a
// superseded one is dropped — a second Enter mid-flight is the ordinary case,
// and the two can come back in either order.
func (a *App) searchMessages(query ui.SearchQuery) {
	channelID, serverID := a.searchChannelID, a.channelServerID(a.searchChannelID)
	epoch := a.epoch

	a.searchSeq++
	seq := a.searchSeq

	a.refillSearch(a.search.Searching)

	go func() {
		messages, err := a.client.SearchMessages(channelID, query.Text, query.Sort, searchLimit,
			query.After, query.Before, "")
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.search == nil || a.searchChannelID != channelID ||
				a.searchSeq != seq {
				return
			}
			if err != nil {
				log.Printf("search %s: %v", channelID, err)
				a.refillSearch(func() { a.search.Fail("Couldn't search this channel.") })
				return
			}

			a.searchFound, a.searchAnswered = messages, true
			a.searchMore = pageWasFull(len(messages), searchLimit) && pageable(query.Sort)
			a.refillSearch(a.drawSearchResults)
		}, false)
	}()
}

// loadMoreSearch asks for the page after what is held and appends it. The query
// is the one already recorded rather than one passed in: the button is on the
// island, and what the island is showing is the answer to that query — a chip
// toggled since narrowed it here without asking anything.
//
// Guarded on a page not already being out, since the button is disabled rather
// than removed while one is. Call on the UI thread.
func (a *App) loadMoreSearch() {
	if a.search == nil || a.searchPaging || !a.searchMore || len(a.searchFound) == 0 {
		return
	}
	query := a.searchQuery
	channelID, serverID := a.searchChannelID, a.channelServerID(a.searchChannelID)
	cursor := a.searchFound[len(a.searchFound)-1].ID
	epoch, seq := a.epoch, a.searchSeq

	a.searchPaging = true
	a.refillSearch(a.drawSearchResults)

	go func() {
		messages, err := a.client.SearchMessages(channelID, query.Text, query.Sort, searchLimit,
			query.After, query.Before, cursor)
		if err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(serverID, messages))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.search == nil || a.searchChannelID != channelID ||
				a.searchSeq != seq {
				return
			}
			a.searchPaging = false

			if err != nil {
				// The answer already on screen is still the answer, so this is a notice
				// rather than the island's own failure line — replacing a hundred cards with
				// a sentence would cost the reader what they had.
				log.Printf("search %s (next page): %v", channelID, err)
				a.notify(ui.ToneWarning, "Couldn't load more results.")
				a.refillSearch(a.drawSearchResults)

				return
			}

			added := appendUnseen(&a.searchFound, messages)
			a.searchMore = added > 0 && pageWasFull(len(messages), searchLimit)
			a.refillSearch(a.drawSearchResults)
		}, false)
	}()
}

// pageable reports whether an order can be paged through at all. Only the two
// chronological ones can: a relevance ranking is re-computed over whatever window
// the route is given, so the page after it is not a thing that exists.
func pageable(sort domain.MessageSort) bool {
	return sort == domain.SortNewest || sort == domain.SortOldest
}

// pageWasFull reports whether a page came back at its ceiling, which is the only
// thing that says there may be another — the route counts nothing for the caller,
// so a short page is the end and a full one is a maybe.
func pageWasFull(got, limit int) bool { return got >= limit }

// appendUnseen adds what a page brought that is not already held and reports how
// many that was. Each page comes back in the order it is held in and begins past
// the last of it, so appending keeps the whole answer ordered.
//
// The repeat check is not belt and braces: nothing in this repo has verified that
// /search honours before/after the way the history route does (see
// internal/client/CLAUDE.md), so a build that ignores them answers with the same
// page forever. Dropping the repeats is what stops it being drawn twice, and a
// page that is *entirely* repeats is what tells the caller the paging is not
// working and to stop offering it.
func appendUnseen(held *[]*domain.Message, page []*domain.Message) int {
	seen := make(map[string]bool, len(*held))
	for _, message := range *held {
		seen[message.ID] = true
	}

	var added int
	for _, message := range page {
		if seen[message.ID] {
			continue
		}
		seen[message.ID] = true

		*held = append(*held, message)
		added++
	}

	return added
}

/* What comes back */

// drawSearchResults narrows the held answer and hands it over as cards. The
// island is told how many came back as well as how many survived, so the line
// above the well can say what the chips took away.
//
// It is also the one writer of the way to the next page: every path that can move
// whether there is one — a fresh query, a page landing, a page failing — ends
// here, so the button cannot be left saying something the state has stopped
// agreeing with. Call on the UI thread.
func (a *App) drawSearchResults() {
	query := a.searchQuery

	results := make([]ui.MessageCard, 0, len(a.searchFound))
	for _, message := range a.searchFound {
		if !a.matchesSearch(message, query) {
			continue
		}

		results = append(results, a.messageCard(message))
	}

	a.search.SetResults(results, len(a.searchFound))
	a.search.SetMore(searchMoreLabel(query.Sort, a.searchMore, a.searchPaging), a.searchPaging)
}

// searchMoreLabel is what the way to the next page reads, "" where there is
// none. The word names the direction the page walks rather than saying "more":
// asking an oldest-first answer for more walks forward in time, which "older"
// would have the reader believe it does not.
func searchMoreLabel(sort domain.MessageSort, more, busy bool) string {
	switch {
	case !more:
		return ""
	case busy:
		return moreBusyLabel
	case sort == domain.SortOldest:
		return "Newer results"
	}

	return "Older results"
}

// moreBusyLabel is what every one of the three panels' next-page buttons says
// while its request is out — the same wait said the same way, whichever surface
// is waiting.
const moreBusyLabel = "Loading..."

// matchesSearch reports whether a message survives what is narrowing the answer.
// Every one of these is a property of the message the route cannot be asked
// about, which is why they are answered against the hundred that came back
// rather than sent — the author included, which is why the chip and the picker
// both land here rather than on the request.
func (a *App) matchesSearch(message *domain.Message, query ui.SearchQuery) bool {
	if !query.Narrowed() {
		return true
	}

	filters, self := query.Filters, a.store.SelfID()
	switch {
	case query.AuthorID != "" && message.AuthorID != query.AuthorID:
		return false
	case !withinSpan(message.ID, query.After, query.Before):
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

// withinSpan re-asks the bound the request already carried. The window *is*
// sent, so this changes nothing where the route honours it — and it is what
// stops the chip lying if a Revolt build ever ignores the field on a search, a
// filter that quietly does nothing being worse than one costing a comparison per
// card. A message ID is a ULID, so the instant it begins with can simply be read
// back out of it.
//
// after is the first instant kept and before the first one dropped, matching the
// half-open span the island reports.
func withinSpan(messageID string, after, before time.Time) bool {
	if after.IsZero() && before.IsZero() {
		return true
	}

	when, err := util.Timestamp(messageID)
	if err != nil {
		return true // not a ULID, so nothing here can say when it was written
	}

	return (after.IsZero() || !when.Before(after)) && (before.IsZero() || when.Before(before))
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
