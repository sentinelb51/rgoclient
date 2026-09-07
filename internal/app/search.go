package app

// Channel search: the island in ui/search.go, and what it takes to answer it.
//
// Revolt's route filters on **words and a window of message IDs, and nothing
// else** — no author, no attachment, no mention — so every other question a
// query can ask is answered here, by reading messages back and keeping the ones
// that match. That is the whole shape of this file:
//
//   - A query with words is walked over `/search`, which selects by full text.
//   - A query with none — `has:image` on its own — is walked over the channel's
//     history, which is the only complete source there is.
//   - Either way the walk **keeps going until it has a screenful**, because one
//     page of a hundred narrowed by a person who speaks rarely is nothing. The
//     count line says what was read as well as what was found.
//
// The walk is bounded (config.Behaviour.SearchScanPages) and the island offers to
// spend the budget again rather than reading a busy channel's whole history for a
// question nobody said was worth it. A relevance ranking is the one order that
// cannot be walked at all: the route re-ranks whatever window it is given, so a
// narrower one is not the page after a wider one.

import (
	"log"
	"slices"
	"strings"
	"time"

	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

// searchLimit is how many messages one request asks for. Revolt caps both routes
// at 100, so a full page is the ceiling rather than the end of the answer.
const searchLimit = 100

// searchWant is how many matches are enough for one press: the well scrolls, and
// a reader who wanted more has a button for it. It is a floor rather than a cap —
// whatever the page a walk stops on brought is kept.
const searchWant = 25

// searchScanPages is how many requests one press may spend. Read per press
// rather than captured, so the setting applies to the next search.
func searchScanPages() int { return max(1, config.Current().Behaviour.SearchScanPages) }

/* Opening it */

// showChannelSearch opens the island for the channel on screen, with the field
// focused: a search that has to be clicked into first is a click nobody meant to
// spend.
func (a *App) showChannelSearch() {
	channelID, ok := a.searchableChannel()
	if !ok {
		return
	}

	dialog := ui.NewSearchDialog(a.deps(), a.channelName(), a.onSearchChanged,
		a.loadMoreSearch, a.closeOverlay)
	dialog.OnResize = a.repositionOverlay

	a.showOverlay(dialog.Content)
	a.search = dialog // after showOverlay, which clears whatever was there
	a.searchChannelID = channelID
	a.resetSearchAnswer()
	a.searchQuery = ui.SearchQuery{}
	a.searchPeople = searchPeople{}
	a.searchSeq++ // an answer owed to the last opening is not this one's

	a.loadSearchAuthors(channelID)
	a.window.Canvas().Focus(dialog.Entry)
}

// loadSearchAuthors fills the island's people list with the accounts this channel
// can be narrowed to, which is also what `from:` and `mentions:` are resolved
// against. A conversation names its own participants, so that answer is already
// here; a server's membership is a walk, which belongs off the UI thread — the
// sidebar's last one is taken where it is this server's, one walk feeding the
// member list, the composer's mentions and this. Call on the UI thread.
func (a *App) loadSearchAuthors(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok {
		return
	}

	serverID := channel.ServerID
	if serverID == "" {
		a.takeSearchAuthors(recipientCandidates(a.store, channel))
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

			a.takeSearchAuthors(candidates)
		}, false)
	}()
}

// takeSearchAuthors installs the pool and re-reads the query against it. A
// membership is a walk, so a `from:` typed before it landed resolved to nobody
// and answered nothing — which is worth asking again the moment it can be
// answered. Call on the UI thread.
func (a *App) takeSearchAuthors(candidates []ui.MentionCandidate) {
	a.searchAuthors = candidates
	a.search.SetAuthors(candidates)

	if !a.searchQuery.Asks() {
		return
	}
	people := a.resolvePeople(a.searchQuery.Terms)
	if people.same(a.searchPeople) {
		return
	}

	a.searchPeople = people
	a.search.SetNote(a.searchNote())
	a.searchMessages()
}

// closeSearch forgets the island. Only closeOverlay calls it — the layer holds
// one thing at a time, so anything else opening takes this one down.
func (a *App) closeSearch() {
	a.search = nil
	a.searchChannelID = ""
	a.searchQuery = ui.SearchQuery{}
	a.searchPeople = searchPeople{}
	a.searchAuthors = nil
	a.resetSearchAnswer()
}

// resetSearchAnswer drops everything belonging to one question, which is what
// asking another begins with.
func (a *App) resetSearchAnswer() {
	a.searchFound = nil
	a.searchCursor, a.searchScanned = "", 0
	a.searchMore, a.searchPaging = false, false
}

/* Asking */

// onSearchChanged is what the island reports every change through: the field
// submitted, a chip tapped, an order picked.
//
// Every one of them re-asks, which is a deliberate change from when a chip was
// free: the narrowing is applied *inside* the walk now, so what is held is the
// answer rather than a superset of it — taking a filter off cannot widen a set
// that was never gathered. That is the trade for a filter that actually finds
// things. Call on the UI thread.
func (a *App) onSearchChanged(query ui.SearchQuery) {
	if a.search == nil {
		return
	}

	a.searchQuery = query
	a.searchPeople = a.resolvePeople(query.Terms)
	a.search.SetNote(a.searchNote())
	a.resetSearchAnswer()

	if !query.Asks() {
		a.refillSearch(a.search.Prompt)
		return
	}

	a.searchMessages()
}

// searchMessages runs one walk from the top and fills the island. The query is
// recorded rather than counted, so an answer to a superseded one is dropped — a
// second Enter mid-flight is the ordinary case, and the two can come back in
// either order.
func (a *App) searchMessages() {
	a.searchSeq++
	a.refillSearch(a.search.Searching)
	a.runSearch(a.searchJob(""), true)
}

// loadMoreSearch spends the budget again from where the last walk stopped. The
// query is the one already recorded rather than one passed in: the button is on
// the island, and what the island is showing is the answer to that query.
//
// Guarded on a walk not already being out, since the button is disabled rather
// than removed while one is. Call on the UI thread.
func (a *App) loadMoreSearch() {
	if a.search == nil || a.searchPaging || !a.searchMore || a.searchCursor == "" {
		return
	}

	a.searchPaging = true
	a.refillSearch(a.drawSearchResults)
	a.runSearch(a.searchJob(a.searchCursor), false)
}

// searchJob is one walk, taken on the UI thread so nothing off it reads the
// controller. held is what is already on screen — the walk skips those, so a
// route ignoring the window it was given cannot hand back the same page forever.
type searchJob struct {
	channelID string
	serverID  string

	query  ui.SearchQuery
	people searchPeople
	held   []*domain.Message

	sort   domain.MessageSort
	cursor string
	budget int
}

// searchJob builds one from what is recorded. Call on the UI thread.
func (a *App) searchJob(cursor string) searchJob {
	sort := searchSort(a.searchQuery)

	return searchJob{
		channelID: a.searchChannelID,
		serverID:  a.channelServerID(a.searchChannelID),
		query:     a.searchQuery,
		people:    a.searchPeople,
		held:      a.searchFound,
		sort:      sort,
		cursor:    cursor,
		budget:    searchBudget(sort),
	}
}

// runSearch walks on a worker and installs the answer in one hop. fresh says the
// walk began from the top, so what lands replaces rather than appends. Call on
// the UI thread.
func (a *App) runSearch(job searchJob, fresh bool) {
	epoch, seq := a.epoch, a.searchSeq

	go func() {
		answer := a.walkSearch(job)
		if answer.err == nil {
			a.client.ResolveAuthors(a.unknownAuthors(job.serverID, answer.found))
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.search == nil || a.searchChannelID != job.channelID ||
				a.searchSeq != seq {
				return
			}
			a.searchPaging = false

			if answer.err != nil {
				a.failSearch(job.channelID, answer.err, fresh)
				return
			}

			appendUnseen(&a.searchFound, answer.found)
			a.searchScanned += answer.scanned
			a.searchCursor = answer.cursor
			a.searchMore = answer.more && answer.cursor != ""

			a.refillSearch(a.drawSearchResults)
		}, false)
	}()
}

// failSearch says a walk came to nothing. A first walk replaces the island's
// contents with the reason; a later one must not — the answer already on screen
// is still the answer, and replacing a hundred cards with a sentence would cost
// the reader what they had.
func (a *App) failSearch(channelID string, err error, fresh bool) {
	log.Printf("search %s: %v", channelID, err)

	if fresh {
		a.refillSearch(func() { a.search.Fail("Couldn't search this channel.") })
		return
	}

	a.notify(ui.ToneWarning, "Couldn't search any further.")
	a.refillSearch(a.drawSearchResults)
}

// searchAnswer is what one walk came to.
type searchAnswer struct {
	found   []*domain.Message
	cursor  string
	scanned int

	// more says the walk stopped because its budget ran out rather than because
	// the channel did — the only case where asking again can find anything.
	more bool

	err error
}

// walkSearch is the loop both sources run: ask for a page, keep what matches,
// and go again until there is a screenful or the budget is spent. Off the UI
// thread — every store read it makes is safe there, and nothing here touches the
// controller's own state.
func (a *App) walkSearch(job searchJob) searchAnswer {
	answer := searchAnswer{cursor: job.cursor}
	terms := job.query.Terms

	seen := make(map[string]bool, len(job.held)+searchLimit)
	for _, message := range job.held {
		seen[message.ID] = true
	}

	for range job.budget {
		page, err := a.searchPage(job, answer.cursor)
		if err != nil {
			answer.err = err
			return answer
		}

		fresh := 0
		for _, message := range page {
			if seen[message.ID] {
				continue
			}
			seen[message.ID] = true
			fresh++
			answer.scanned++

			if a.matchesSearch(message, terms, job.people) {
				answer.found = append(answer.found, message)
			}
		}

		// A short page is the end of what there is; a page that brought nothing new
		// is a route ignoring the window it was given, which walking further would
		// only ask the same hundred of forever. Neither is worth another request,
		// and neither leaves anywhere to resume from.
		if fresh == 0 || !pageWasFull(len(page), searchLimit) || !pageable(job.sort) {
			answer.cursor = ""
			return answer
		}
		answer.cursor = page[len(page)-1].ID

		if len(answer.found) >= searchWant {
			answer.more = true
			return answer
		}
	}
	answer.more = true

	return answer
}

// searchPage is one request, from whichever source the query needs. Words go to
// the route that selects on them; a query with none is a question the route
// cannot be asked, so the channel is read instead.
func (a *App) searchPage(job searchJob, cursor string) ([]*domain.Message, error) {
	terms := job.query.Terms
	if terms.Text != "" {
		return a.client.SearchMessages(job.channelID, terms.Text, job.sort, searchLimit,
			terms.After, terms.Before, cursor)
	}

	return a.client.ScanMessages(job.channelID, job.sort, searchLimit,
		terms.After, terms.Before, cursor)
}

// searchSort is the order a walk actually runs in. Relevance ranks against
// words; with none there is nothing to be relevant to, so a filter-only walk runs
// newest-first — which is also the only way it can be walked at all.
func searchSort(query ui.SearchQuery) domain.MessageSort {
	if query.Terms.Text == "" && query.Sort == domain.SortRelevance {
		return domain.SortNewest
	}

	return query.Sort
}

// searchBudget is how many requests one press may spend. An order that cannot be
// paged gets one whatever the setting says — there is no second page to spend it
// on.
func searchBudget(sort domain.MessageSort) int {
	if !pageable(sort) {
		return 1
	}

	return searchScanPages()
}

// pageable reports whether an order can be walked at all. Only the two
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

/* Who a name is */

// searchPeople is what `from:` and `mentions:` named, resolved to accounts.
// Resolved once per query rather than per message, and here rather than in `ui`:
// the island parses the names and only the store can say whose they are, which is
// the seam a device list crosses in the other direction.
type searchPeople struct {
	from     []string
	mentions []string

	// everyone is `mentions:everyone`, which names nobody at all — Revolt stores
	// an @everyone as a flag with an empty mention list.
	everyone bool

	// unknown is every name that resolved to nobody, said by the island: a filter
	// finding nothing because it names nobody is worse than no filter.
	unknown []string
}

// same reports whether two resolutions are the same answer, which is what says a
// membership landing late changed nothing.
func (p searchPeople) same(other searchPeople) bool {
	return p.everyone == other.everyone &&
		slices.Equal(p.from, other.from) && slices.Equal(p.mentions, other.mentions)
}

// resolvePeople looks each name up among the people this channel can be narrowed
// to. Call on the UI thread.
func (a *App) resolvePeople(terms ui.SearchTerms) searchPeople {
	var people searchPeople
	if len(terms.From) == 0 && len(terms.Mentions) == 0 {
		return people
	}

	index := make(map[string]string, 2*len(a.searchAuthors))
	for _, candidate := range a.searchAuthors {
		if candidate.Username != "" {
			index[strings.ToLower(candidate.Username)] = candidate.ID
		}
		if candidate.Name != "" {
			index[strings.ToLower(candidate.Name)] = candidate.ID
		}
	}
	self := a.store.SelfID()

	people.from = resolveNames(terms.From, index, self, &people)
	for _, name := range terms.Mentions {
		if strings.EqualFold(name, "everyone") {
			people.everyone = true
			continue
		}

		people.mentions = append(people.mentions, resolveNames([]string{name}, index, self, &people)...)
	}

	return people
}

// resolveNames turns what a key named into accounts. "me" is this account and an
// ID is taken as one — a webhook or somebody who has left is in no membership and
// could otherwise not be named at all — and anything else is a name to look up.
func resolveNames(names []string, index map[string]string, self string, people *searchPeople) []string {
	var found []string

	for _, name := range names {
		switch {
		case strings.EqualFold(name, "me"):
			if self != "" {
				found = append(found, self)
			}
		case util.IsID(name):
			found = append(found, name)
		default:
			if userID, ok := index[strings.ToLower(name)]; ok {
				found = append(found, userID)
				continue
			}

			people.unknown = append(people.unknown, name)
		}
	}

	return found
}

/* What comes back */

// drawSearchResults hands the held answer over as cards. Nothing is filtered
// here any more — the walk kept only what matched — so this is the count, the
// cards and the way to spend the budget again.
//
// It is the one writer of that button: every path that can move whether there is
// more to find ends here, so it cannot be left saying something the state has
// stopped agreeing with. Call on the UI thread.
func (a *App) drawSearchResults() {
	results := make([]ui.MessageCard, 0, len(a.searchFound))
	for _, message := range a.searchFound {
		results = append(results, a.messageCard(message))
	}

	a.search.SetResults(ui.SearchOutcome{
		Results: results,
		Scanned: a.searchScanned,
		More:    a.searchMoreLabel(),
		Busy:    a.searchPaging,
	})
}

// searchMoreLabel is what the way to more reads, "" where there is none. A
// narrowed query says what the button *does* — the walk stopped on its budget
// rather than on the channel — where a plain one can name the direction it walks,
// which "more" would have the reader guess at.
func (a *App) searchMoreLabel() string {
	switch {
	case !a.searchMore:
		return ""
	case a.searchPaging:
		return moreBusyLabel
	case a.searchQuery.Narrowed():
		return "Keep searching"
	case a.searchQuery.Sort == domain.SortOldest:
		return "Newer results"
	}

	return "Older results"
}

// moreBusyLabel is what every one of the three panels' next-page buttons says
// while its request is out — the same wait said the same way, whichever surface
// is waiting.
const moreBusyLabel = "Loading..."

// searchNote is what the island says about the query itself rather than about the
// answer: a filter that named nothing, and a person nobody here is called. Both
// are silent failures otherwise — the search simply matches nothing, and the
// reader is left to work out which word did it.
func (a *App) searchNote() string {
	var says []string

	if unknown := a.searchQuery.Terms.Unknown; len(unknown) > 0 {
		says = append(says, "Not a filter: "+strings.Join(unknown, ", ")+".")
	}
	if unknown := a.searchPeople.unknown; len(unknown) > 0 {
		says = append(says, "Nobody here is called "+strings.Join(unknown, " or ")+".")
	}

	return strings.Join(says, " ")
}

// refillSearch changes the island and re-places it. Every change is a change of
// height — a query in flight replaces the cards with a line — and the island is
// centred and sized from its own minimum, neither of which re-runs on its own.
// Call on the UI thread.
func (a *App) refillSearch(change func()) {
	change()
	a.repositionOverlay()
}

/* What a message has to be */

// matchesSearch reports whether a message survives the query's terms. Every one
// of these is a property the route cannot be asked about, which is why the walk
// asks them here — the author and the attachments included, and the span too,
// which is sent but re-checked for the reason withinSpan gives.
//
// Safe off the UI thread: the store's reads are.
func (a *App) matchesSearch(message *domain.Message, terms ui.SearchTerms, people searchPeople) bool {
	if !terms.Narrowed() {
		return true
	}

	switch {
	case len(terms.From) > 0 && !slices.Contains(people.from, message.AuthorID):
		return false
	case !withinSpan(message.ID, terms.After, terms.Before):
		return false
	case len(terms.Mentions) > 0 && !mentionedIn(message, people):
		return false
	case terms.Has.Any() && !messageCarries(message, terms.Has):
		return false
	case terms.Is.Any() && !a.messageIs(message, terms.Is):
		return false
	}

	return true
}

// mentionedIn reports whether the message pings any of the people named. A name
// that resolved to nobody leaves the list short, which matches nothing — the
// island says which name it was.
func mentionedIn(message *domain.Message, people searchPeople) bool {
	if people.everyone && message.MentionsEveryone {
		return true
	}

	return slices.ContainsFunc(people.mentions, message.MentionsUser)
}

// messageCarries answers `has:`, which is a set of things any one of which will
// do — a comma widens, which is the language's one rule.
func messageCarries(message *domain.Message, has ui.SearchFilters) bool {
	switch {
	case has.Has(ui.FilterFile) && len(message.Attachments) > 0:
		return true
	case has.Has(ui.FilterImage) && filesOfKind(message, domain.FileImage) > 0:
		return true
	case has.Has(ui.FilterVideo) && filesOfKind(message, domain.FileVideo) > 0:
		return true
	case has.Has(ui.FilterAudio) && filesOfKind(message, domain.FileAudio) > 0:
		return true
	case has.Has(ui.FilterLink) && hasLink(message):
		return true
	case has.Has(ui.FilterEmbed) && len(message.Embeds) > 0:
		return true
	case has.Has(ui.FilterReaction) && len(message.Reactions) > 0:
		return true
	case has.Has(ui.FilterReply) && len(message.Replies) > 0:
		return true
	}

	return false
}

// messageIs answers `is:`, the same way. A bot is the account or the integration
// behind the post — a webhook is nobody's account at all, and both are what a
// reader means by "not a person".
func (a *App) messageIs(message *domain.Message, is ui.SearchFilters) bool {
	switch {
	case is.Has(ui.FilterPinned) && message.Pinned:
		return true
	case is.Has(ui.FilterEdited) && message.Edited != nil:
		return true
	case is.Has(ui.FilterSystem) && message.System != nil:
		return true
	case is.Has(ui.FilterBot) && a.postedByBot(message):
		return true
	}

	return false
}

func (a *App) postedByBot(message *domain.Message) bool {
	if message.Webhook != nil {
		return true
	}
	user, ok := a.store.User(message.AuthorID)

	return ok && user.Bot
}

// withinSpan re-asks the bound the request already carried. The window *is*
// sent, so this changes nothing where the route honours it — and it is what
// stops the bound lying if a Revolt build ever ignores the field on `/search`, a
// filter that quietly does nothing being worse than one costing a comparison per
// message. A message ID is a ULID, so the instant it begins with can simply be
// read back out of it.
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

// filesOfKind counts the attachments of one kind, which arrives with the file
// rather than being guessed from its name.
func filesOfKind(message *domain.Message, kind domain.FileKind) int {
	var count int
	for _, file := range message.Attachments {
		if file.Kind == kind {
			count++
		}
	}

	return count
}

// imagesIn is that for pictures, which an island card counts as well.
func imagesIn(message *domain.Message) int { return filesOfKind(message, domain.FileImage) }

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
