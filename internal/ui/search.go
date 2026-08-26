package ui

// Channel search: the island in panels.go, plus what only this surface has. The
// pins panel and the mention inbox are answers the reader asked one question
// for; a search is a question they refine, so this one carries the controls to
// refine it with — a field, a run of filter chips, the two drawers that hold
// what a chip cannot, and the three orders the route can answer in.
//
// Nothing here decides what a filter means. The dialog owns which chips are lit
// and reports the whole query on every change; the controller holds the messages
// and knows whether a change has to reach the network — see app/search.go.

import (
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* What a search asks for */

// SearchFilter is one condition a result has to meet, of the kind a single bit
// can hold: a message either carries a picture or it does not. The two that need
// a value of their own — which person, which days — are fields on the query
// beside these, and are asked for in a drawer rather than by a chip alone.
type SearchFilter int

const (
	FilterMentionsMe SearchFilter = iota
	FilterPinned
	FilterFiles
	FilterImages
	FilterLinks
	FilterReactions
)

// SearchFilters is a set of them, one bit each — a value, so the controller can
// tell one query from another with == rather than walking a map.
type SearchFilters uint32

// Has reports whether filter is on.
func (f SearchFilters) Has(filter SearchFilter) bool { return f&(1<<filter) != 0 }

// Any reports whether anything is narrowing the answer.
func (f SearchFilters) Any() bool { return f != 0 }

func (f SearchFilters) with(filter SearchFilter, on bool) SearchFilters {
	if on {
		return f | 1<<filter
	}

	return f &^ (1 << filter)
}

// SearchQuery is the whole state of the island: what was last submitted, in what
// order, narrowed by what. Text moves on submit only — a filter tapped while
// something half-typed sits in the field narrows the results on screen, which is
// what the reader is looking at.
type SearchQuery struct {
	Text    string
	Sort    domain.MessageSort
	Filters SearchFilters

	// AuthorID narrows to one person, and is applied to the answer exactly as the
	// chips are: DataMessageSearch carries no author, so nobody's messages can be
	// *asked* for — only found among the hundred that came back.
	AuthorID string

	// After and Before bound the answer in time, and are the only narrowing here
	// that is genuinely sent. Held as the instants the reader named rather than as
	// the message IDs the route takes at either end, which is the client's own
	// business — see client.SearchMessages.
	//
	// Half-open: After is the first instant kept and Before the first one dropped,
	// so one day is that day's start and the next day's.
	After, Before time.Time
}

// SameRequest reports whether q and other would be answered by the same request.
// Neither the chips nor the author are in it — both are applied to the answer, so
// changing one costs nothing. The span is: it is what the route is bounded with.
func (q SearchQuery) SameRequest(other SearchQuery) bool {
	return q.Text == other.Text && q.Sort == other.Sort &&
		q.After.Equal(other.After) && q.Before.Equal(other.Before)
}

// Narrowed reports whether anything at all is cutting the answer down — a chip,
// an author or a span. What Clear puts back.
func (q SearchQuery) Narrowed() bool {
	return q.Filters.Any() || q.AuthorID != "" || !q.After.IsZero() || !q.Before.IsZero()
}

/* The island */

// SearchDialog searches one channel for what is typed in it. Unlike the pins
// panel it asks for nothing until it is told to: Revolt's search is a request
// per query, so it runs on submit rather than on every keystroke.
type SearchDialog struct {
	*messageIsland

	Content fyne.CanvasObject

	// Entry is the field to focus once the island is up — a search that has to be
	// clicked into before it can be typed into is a click nobody meant to spend.
	Entry fyne.Focusable

	// OnResize fires when the island's height moves under its own steam: a drawer
	// opening, a chip taking a longer name, a row appearing in the author list.
	// The layer centres what it holds from that height and re-measures for nobody.
	OnResize func()

	onChange func(SearchQuery)

	entry  *modalEntry
	query  SearchQuery
	selfID string

	filters map[SearchFilter]*searchChip
	sorts   map[domain.MessageSort]*searchChip

	// The three chips standing for a value rather than a bit. fromMe and author
	// write the same field: one is the shortcut, the other the picker, and which is
	// lit is decided by whose ID the query holds.
	fromMe *searchChip
	author *searchChip
	dates  *searchChip

	authors *authorDrawer
	days    *dateDrawer

	chipRow   *fyne.Container
	block     *fyne.Container   // the chips and both drawers, relaid as one
	clearSlot fyne.CanvasObject // the Clear chip and the gap before it, hidden together
}

// The author and date chips as they read with nothing chosen. The lit forms name
// the person or the span instead, so the chip is the whole of what it is worth.
const (
	fromMeLabel  = "From me"
	anyoneLabel  = "From anyone"
	anyTimeLabel = "Any time"
)

// searchFilterChips are the conditions on offer, in the order they are drawn,
// after the three that carry a value. The three the client already has a mark for
// borrow it: a message carrying an @ and the chip that finds one are otherwise
// the same thing drawn twice.
var searchFilterChips = []struct {
	filter SearchFilter
	icon   fyne.Resource
	label  string
}{
	{FilterMentionsMe, assets.MentionIcon, "Mentions"},
	{FilterPinned, assets.SystemPinnedIcon, "Pinned"},
	{FilterFiles, assets.SearchAttachmentIcon, "Files"},
	{FilterImages, assets.SearchImageIcon, "Images"},
	{FilterLinks, assets.SearchLinkIcon, "Links"},
	{FilterReactions, assets.SearchReactionIcon, "Reactions"},
}

// searchSortChips are the three orders the route answers in: what it thinks
// answers the question best, then the two the reader can predict.
var searchSortChips = []struct {
	sort  domain.MessageSort
	icon  fyne.Resource
	label string
}{
	{domain.SortRelevance, assets.SearchRelevanceIcon, "Best"},
	{domain.SortNewest, assets.SearchNewestIcon, "Newest"},
	{domain.SortOldest, assets.SearchOldestIcon, "Oldest"},
}

// NewSearchDialog builds the island for a channel. selfID is what the From me
// chip writes, and what tells that chip's state from the picker's. onChange
// receives the whole query whenever any part of it moves — the field submitted, a
// filter toggled, a person picked, an order chosen — and the controller decides
// which of those has to reach the network. onClose dismisses the layer.
func NewSearchDialog(deps Deps, channel, selfID string, onChange func(SearchQuery), onClose func()) *SearchDialog {
	d := &SearchDialog{
		onChange: onChange,
		query:    SearchQuery{Sort: domain.SortRelevance},
		selfID:   selfID,
		filters:  make(map[SearchFilter]*searchChip, len(searchFilterChips)),
		sorts:    make(map[domain.MessageSort]*searchChip, len(searchSortChips)),
	}

	// The field handles Escape itself — see modalEntry.
	d.entry = newModalEntry(onClose)
	d.entry.SetPlaceHolder("Search this channel")
	d.entry.OnSubmitted = d.submit
	d.Entry = d.entry

	// The controls are built before the island rather than into it: the island is
	// the shell all three surfaces share, and what a question is refined with is
	// this one's alone.
	island, content := newMessageIsland(deps, islandParts{
		Mark:     assets.SearchIcon,
		Title:    "Search",
		Where:    "in " + channel,
		Controls: []fyne.CanvasObject{d.buildField(), d.buildFilters(deps)},
		Trailing: d.buildSorts(),
		OnClose:  onClose,
	})

	d.messageIsland = island
	d.Content = content

	d.Prompt()

	return d
}

// buildField is the client's own field surface rather than a bare Entry: an
// entry under AppTheme draws no box of its own, and a caret blinking on the
// island's background would not read as somewhere to type.
func (d *SearchDialog) buildField() fyne.CanvasObject {
	return searchField(assets.SearchIcon, WithCaret(d.entry))
}

// searchField is that box, shared with the drawers' own fields so a date typed
// into one looks like a query typed into the other.
func searchField(mark fyne.Resource, content fyne.CanvasObject) fyne.CanvasObject {
	pad := theme.Sizes.IslandChipPaddingH

	glyph := newScaledIcon(tintedIcon(mark, theme.Colors.IslandHintText), theme.Sizes.SearchFieldGlyph)

	field := canvas.NewRectangle(theme.Colors.ComposerBg)
	field.CornerRadius = theme.Sizes.SearchFieldRadius
	Outline(field)

	row := NewFillRow(2,
		container.NewCenter(glyph),
		HorizontalSpacer(theme.Sizes.IslandChipGap),
		content,
	)

	return NewFixedHeightContainer(theme.Sizes.SearchFieldHeight,
		container.NewStack(field, NewInset(row, 0, 0, pad, pad)))
}

// buildFilters is the run of chips and the two drawers under it, wrapping against
// the island's inner width. The drawers hang here rather than beside the field
// because each belongs to the chip that opens it; only one is ever up, so the
// island grows by one panel at most. A Clear that only appeared when something
// was on would put a second row under the run half the time, which is why Clear
// rides in the count row instead.
func (d *SearchDialog) buildFilters(deps Deps) fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(searchFilterChips)+3)

	d.fromMe = newSearchChip(assets.AccountIcon, fromMeLabel, d.toggleFromMe)
	d.author = newSearchChip(assets.MembersIcon, anyoneLabel, d.toggleAuthors)
	d.dates = newSearchChip(assets.SearchDateIcon, anyTimeLabel, d.toggleDates)
	chips = append(chips, d.fromMe, d.author, d.dates)

	for _, entry := range searchFilterChips {
		chip := newSearchChip(entry.icon, entry.label, nil)
		chip.onTap = func() { d.toggle(entry.filter) }

		d.filters[entry.filter] = chip
		chips = append(chips, chip)
	}

	d.chipRow = NewFlow(islandInnerWidth(), theme.Sizes.IslandChipGap, chips...)
	d.authors = newAuthorDrawer(deps, d.pickAuthor, d.closeDrawers, d.resized)
	d.days = newDateDrawer(d.setSpan, d.closeDrawers)

	d.block = VBoxNoSpacing(d.chipRow, d.authors.slot, d.days.slot)

	return d.block
}

// buildSorts is what rides opposite the island's count: the way out of the
// filters, and the order the answer came back in. Both belong on that line
// because both are read against the number on it.
func (d *SearchDialog) buildSorts() fyne.CanvasObject {
	gap := theme.Sizes.IslandChipGap

	// The gap goes with the chip rather than beside it: a hidden child is skipped
	// by the row, a spacer left standing on its own is not.
	d.clearSlot = HBoxNoSpacing(HorizontalSpacer(gap),
		newSearchChip(assets.ActionCancelIcon, "Clear", d.clearFilters))
	d.clearSlot.Hide()

	sorts := make([]fyne.CanvasObject, 0, 2*len(searchSortChips)-1)
	for index, entry := range searchSortChips {
		if index > 0 {
			sorts = append(sorts, HorizontalSpacer(gap))
		}

		chip := newSearchChip(entry.icon, entry.label, nil)
		chip.onTap = func() { d.pickSort(entry.sort) }

		d.sorts[entry.sort] = chip
		sorts = append(sorts, chip)
	}
	d.sorts[d.query.Sort].Set(true)

	return HBoxNoSpacing(d.clearSlot, HorizontalSpacer(gap), HBoxNoSpacing(sorts...))
}

/* Who wrote it */

// SetAuthors hands the picker the people this channel can be narrowed to,
// resolved by the controller off the UI thread. Late is the ordinary case — a
// server's membership is a walk — so the drawer can already be open, which is why
// the picker re-runs its own query on being refilled. Call on the UI thread.
func (d *SearchDialog) SetAuthors(candidates []MentionCandidate) {
	d.authors.setCandidates(candidates)
	if d.authors.slot.Visible() {
		d.authors.refilter()
		d.resized()
	}
}

// toggleFromMe is the shortcut past the picker, the one person a reader looks for
// often enough to be worth a chip of their own. Tapping it while it is lit is the
// way back to anyone, which is what makes it read as a filter rather than a menu.
func (d *SearchDialog) toggleFromMe() {
	if d.selfID == "" {
		return
	}
	if d.query.AuthorID == d.selfID {
		d.setAuthor("", "")
		return
	}

	d.setAuthor(d.selfID, "")
}

// toggleAuthors opens the picker, or clears the person it chose. A chip standing
// for somebody is put out by tapping it, like every other chip in the run; only a
// chip standing for nobody opens the drawer.
func (d *SearchDialog) toggleAuthors() {
	if d.query.AuthorID != "" && d.query.AuthorID != d.selfID {
		d.setAuthor("", "")
		return
	}
	if d.authors.slot.Visible() {
		d.closeDrawers()
		return
	}

	d.days.slot.Hide()
	d.authors.open()
	d.resized()
	d.focus(d.authors.entry)
}

// pickAuthor takes the drawer's choice and closes it: one person is the whole of
// what it was open to ask.
func (d *SearchDialog) pickAuthor(candidate MentionCandidate) {
	d.closeDrawers()
	d.setAuthor(candidate.ID, candidate.Name)
	d.focus(d.entry)
}

// setAuthor records the person and repaints both chips from that one field. name
// is what the chip should read; empty means the person is this account, which the
// From me chip says instead.
func (d *SearchDialog) setAuthor(userID, name string) {
	if d.applyAuthor(userID, name) {
		d.report()
	}
}

// applyAuthor is that without the report, so a change made alongside others is
// reported once. Answers whether anything moved.
func (d *SearchDialog) applyAuthor(userID, name string) bool {
	if d.query.AuthorID == userID {
		return false
	}
	d.query.AuthorID = userID

	self := userID != "" && userID == d.selfID
	d.fromMe.Set(self)

	label := anyoneLabel
	if userID != "" && !self {
		label = "From " + name
	}
	d.author.SetLabel(label)
	d.author.Set(userID != "" && !self)

	return true
}

/* When it was written */

// toggleDates opens the span drawer, or clears the span it holds — the author
// chip's rule, for the same reason.
func (d *SearchDialog) toggleDates() {
	if !d.days.span.empty() {
		d.days.clear()
		return
	}
	if d.days.slot.Visible() {
		d.closeDrawers()
		return
	}

	d.authors.close()
	d.days.slot.Show()
	d.resized()
	d.focus(d.days.after.entry)
}

// setSpan takes the days the drawer now holds and turns them into the instants
// the query is bounded by. Called for a preset, for a date typed in full, and for
// a field emptied — the drawer reports nothing in between, a half-typed date
// being a date nobody has finished naming.
func (d *SearchDialog) setSpan(s span) {
	if d.applySpan(s) {
		d.report()
	}
}

// applySpan is setSpan without the report, for the same reason applyAuthor is.
func (d *SearchDialog) applySpan(s span) bool {
	after, before := s.instants()
	if d.query.After.Equal(after) && d.query.Before.Equal(before) {
		return false
	}
	d.query.After, d.query.Before = after, before

	d.dates.SetLabel(s.label())
	d.dates.Set(!s.empty())

	return true
}

/* Reporting what changed */

func (d *SearchDialog) submit(text string) {
	d.query.Text = text
	d.onChange(d.query)
}

func (d *SearchDialog) toggle(filter SearchFilter) {
	d.query.Filters = d.query.Filters.with(filter, !d.query.Filters.Has(filter))
	d.paintFilters()
	d.onChange(d.query)
}

// clearFilters puts the whole of the narrowing back, drawers included: Clear
// names what the count line lost, and a person or a span took as much of it as a
// chip did.
// Each half is put back silently and the whole change reported once: three
// reports for one tap would be a request the reader never asked for, the
// controller reading an unchanged query as the same question asked again.
func (d *SearchDialog) clearFilters() {
	d.closeDrawers()

	d.query.Filters = 0
	d.days.reset()
	d.applySpan(span{})
	d.applyAuthor("", "")

	d.report()
}

// pickSort is exclusive — three chips standing for one value, so the one that
// was lit goes out here rather than the reader having to put it out.
func (d *SearchDialog) pickSort(sort domain.MessageSort) {
	if d.query.Sort == sort {
		return
	}

	d.sorts[d.query.Sort].Set(false)
	d.query.Sort = sort
	d.sorts[sort].Set(true)

	d.onChange(d.query)
}

// report is what the value chips end on: a relabelled chip is a differently wide
// chip, so the run has to be rewrapped before the query goes anywhere.
func (d *SearchDialog) report() {
	d.paintFilters()
	d.resized()
	d.onChange(d.query)
}

func (d *SearchDialog) paintFilters() {
	for filter, chip := range d.filters {
		chip.Set(d.query.Filters.Has(filter))
	}

	showIf(d.clearSlot, d.query.Narrowed())
}

// closeDrawers puts both panels away without touching what they chose — Escape
// out of one, and the way every other opening closes the one before it.
func (d *SearchDialog) closeDrawers() {
	if !d.authors.slot.Visible() && !d.days.slot.Visible() {
		return
	}

	d.authors.close()
	d.days.slot.Hide()
	d.resized()
}

// resized rewraps the chips and re-places the island. A container skips its
// layout when its size has not changed, so a chip that merely took a longer name
// would keep the width it was last given; the flow has to be told outright.
func (d *SearchDialog) resized() {
	Relayout(d.chipRow)
	Relayout(d.block)

	if d.OnResize != nil {
		d.OnResize()
	}
}

// focus hands the keyboard to one of the island's fields, the canvas being
// reachable only through a widget already on it.
func (d *SearchDialog) focus(target fyne.Focusable) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(d.Content); c != nil {
		c.Focus(target)
	}
}

/* Filling it */

// SetResults replaces the cards. found is how many came back before the filters
// were applied, which the line reports alongside: the route caps an answer at
// 100 and pages only by being asked for a narrower span, so a reader narrowing
// one has to be able to see that they are narrowing that hundred. Call on the UI
// thread.
func (d *SearchDialog) SetResults(results []MessageCard, found int) {
	cards := make([]fyne.CanvasObject, 0, len(results))
	for _, result := range results {
		cards = append(cards, newMessageCard(d.deps, result))
	}

	d.setCards(cards)

	switch {
	case len(results) > 0:
		d.setCount(countLine(len(results), found))
		d.say("")
	case found > 0:
		d.setCount(countLine(0, found))
		d.say("Nothing here matches those filters.")
	default:
		d.setCount("")
		d.say("Nothing matched that.")
	}
}

// Prompt is the island as it opens: nothing asked, nothing to count.
func (d *SearchDialog) Prompt() {
	d.reset("Type something and press Enter.")
}

// Searching says a request is out, replacing whatever the last one found: a list
// left standing under a new query reads as a result for it. Call on the UI thread.
func (d *SearchDialog) Searching() {
	d.reset("Searching...")
}

// Fail replaces the results with a reason there are none. Call on the UI thread.
func (d *SearchDialog) Fail(reason string) {
	d.reset(reason)
}

// countLine says how much of the answer is on screen. Both numbers appear only
// when they differ: "24 of 24 results" is a sum nobody asked for.
func countLine(shown, found int) string {
	if shown == found {
		return util.Quantity(found, "result")
	}

	return strconv.Itoa(shown) + " of " + util.Quantity(found, "result")
}

/* The drawer that picks a person */

// authorDrawer is what the author chip opens: a field, and the composer's own
// mention picker under it. Reusing that widget is what makes a 2000-member server
// cheap to filter — it ranks into fixed scratch and allocates nothing per
// keystroke — and it makes these rows look like the rows an @ opens, which is
// where the reader last saw this list.
type authorDrawer struct {
	slot *fyne.Container // the gap above and the body, shown and hidden as one

	entry  *pickerEntry
	picker *MentionPicker
	hint   *canvas.Text

	onPick   func(MentionCandidate)
	onResize func()

	empty bool // no candidates at all, which is a different sentence from no match
}

func newAuthorDrawer(deps Deps, onPick func(MentionCandidate), onCancel, onResize func()) *authorDrawer {
	w := &authorDrawer{onPick: onPick, onResize: onResize, empty: true}

	w.picker = NewMentionPicker(deps, w.accept)
	w.hint = newText("", theme.Colors.IslandHintText, theme.Sizes.IslandPreviewSize)
	w.hint.Hide()

	w.entry = newPickerEntry(onCancel, w.key)
	w.entry.SetPlaceHolder("Type a name")
	w.entry.OnChanged = func(string) { w.refilter(); w.resize() }
	w.entry.OnSubmitted = func(string) { w.picker.Accept() }

	w.slot = drawerSlot(VBoxNoSpacing(
		searchField(assets.AccountIcon, WithCaret(w.entry)),
		VerticalSpacer(theme.Sizes.IslandChipGap),
		w.picker,
		NewInset(w.hint, 0, 0, theme.Sizes.IslandChipPaddingH, 0),
	))

	return w
}

// setCandidates replaces the pool. The picker keeps its own copy, so this is the
// one place the drawer holds anything about who exists.
func (w *authorDrawer) setCandidates(candidates []MentionCandidate) {
	w.empty = len(candidates) == 0
	w.picker.SetCandidates(MentionUser, candidates)
}

// open shows the drawer with the head of the pool already listed — the bare-@
// case, which is the picker's own way of saying "everyone".
func (w *authorDrawer) open() {
	w.entry.SetText("")
	w.picker.Reset()
	w.refilter()
	w.slot.Show()
}

func (w *authorDrawer) close() {
	w.slot.Hide()
	w.picker.Reset()
	w.picker.Hide()
}

// refilter re-runs the typed query and says what the rows cannot: that there is
// nobody to pick from, or nobody by that name.
func (w *authorDrawer) refilter() {
	if w.picker.Update(MentionUser, w.entry.Text) {
		w.picker.Show()
		w.hint.Hide()

		return
	}

	w.picker.Hide()
	w.hint.Text = "Nobody by that name."
	if w.empty {
		w.hint.Text = "Nobody here to narrow by yet."
	}
	w.hint.Refresh()
	w.hint.Show()
}

// accept reports the chosen person. Guarded because the picker offers whatever it
// last matched, and an empty list matches a candidate with no ID at all.
func (w *authorDrawer) accept(candidate MentionCandidate) {
	if candidate.ID == "" {
		return
	}

	w.onPick(candidate)
}

// key lets the list consume the keys that move through it, exactly as the
// composer's own does.
func (w *authorDrawer) key(event *fyne.KeyEvent) bool {
	if !w.picker.Visible() {
		return false
	}

	switch event.Name {
	case fyne.KeyUp:
		w.picker.Step(-1)
	case fyne.KeyDown:
		w.picker.Step(1)
	case fyne.KeyTab:
		w.picker.Accept()
	default:
		return false
	}

	return true
}

func (w *authorDrawer) resize() {
	if w.onResize != nil {
		w.onResize()
	}
}

/* The drawer that picks a span */

// span is what the date drawer has been told: the two *days* the reader named,
// either of them zero for an end left open. Days rather than instants, because a
// day is what was typed and what the chip has to be able to say back.
type span struct {
	after, before time.Time
}

func (s span) empty() bool { return s.after.IsZero() && s.before.IsZero() }

// same compares two spans by the instants they name. == would compare the
// time.Time structs, which a parsed day and a computed one need not share.
func (s span) same(other span) bool {
	return s.after.Equal(other.after) && s.before.Equal(other.before)
}

// instants turns the pair into the bounds the query carries. Before is the day
// *after* the one named, the range being half-open: a search bounded at a day's
// first instant would lose the whole of the day it named.
func (s span) instants() (after, before time.Time) {
	after = s.after
	if !s.before.IsZero() {
		before = s.before.AddDate(0, 0, 1)
	}

	return after, before
}

// label is the chip's whole text. A span with one end open says which end it is,
// there being no second date to read the direction from.
func (s span) label() string {
	switch {
	case s.after.IsZero() && s.before.IsZero():
		return anyTimeLabel
	case s.before.IsZero():
		return "Since " + shortDay(s.after)
	case s.after.IsZero():
		return "Until " + shortDay(s.before)
	case s.after.Equal(s.before):
		return shortDay(s.after)
	}

	return shortDay(s.after) + " to " + shortDay(s.before)
}

// The layouts a day is read and written in: typed as the unambiguous ordering,
// shown on a chip as the short one, with the year only when it is not this one.
// dayEntryHint is that first layout said in letters rather than in Go's
// reference date, which as a placeholder reads as a date somebody already typed.
const (
	dayEntryLayout = "2006-01-02"
	dayEntryHint   = "YYYY-MM-DD"
	shortDayLayout = "Jan 2"
	longDayLayout  = "Jan 2, 2006"
)

// shortDay names a day in as little as still says it.
func shortDay(t time.Time) string {
	if t.Year() != time.Now().Year() {
		return t.Format(longDayLayout)
	}

	return t.Format(shortDayLayout)
}

// searchSpanPresets are the runs of days worth a chip: what a reader means by a
// week without wanting to work out which Monday that was. Each ends today, so all
// of them leave the far end open.
var searchSpanPresets = []struct {
	days  int
	label string
}{
	{1, "Today"},
	{7, "Past week"},
	{30, "Past month"},
	{365, "Past year"},
}

// presetSpan is that run as days. Inclusive of today, so a week is today and the
// six before it rather than today and seven.
func presetSpan(days int) span {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	return span{after: today.AddDate(0, 0, -(days - 1))}
}

// dateDrawer is what the date chip opens: the two ends typed out, and the runs
// worth not typing. Unlike the author drawer it stays open after a choice — a
// range is two answers, and a preset is as often the start of narrowing one as
// the end of it.
type dateDrawer struct {
	slot *fyne.Container

	after, before *dateField
	presets       []*searchChip

	onChange func(span)
	span     span
}

func newDateDrawer(onChange func(span), onCancel func()) *dateDrawer {
	w := &dateDrawer{onChange: onChange}

	w.after = newDateField("On or after", onCancel, w.commit)
	w.before = newDateField("On or before", onCancel, w.commit)

	chips := make([]fyne.CanvasObject, 0, len(searchSpanPresets))
	for _, preset := range searchSpanPresets {
		chip := newSearchChip(assets.SearchDateIcon, preset.label, nil)
		chip.onTap = func() { w.pickPreset(preset.days) }

		w.presets = append(w.presets, chip)
		chips = append(chips, chip)
	}

	gap := theme.Sizes.IslandChipGap
	w.slot = drawerSlot(VBoxNoSpacing(
		HBoxNoSpacing(w.after.content, HorizontalSpacer(gap), w.before.content),
		VerticalSpacer(gap),
		NewFlow(islandInnerWidth(), gap, chips...),
	))

	return w
}

// commit reports the span the fields now hold. Driven from the fields own
// parsing, which stays silent while a date is half-typed: a request per keystroke
// of a full date would be nine requests for days nobody named.
func (w *dateDrawer) commit() {
	w.set(span{after: w.after.day, before: w.before.day})
}

// pickPreset fills both fields from a run of days, so the chip and the boxes
// cannot disagree about what is being asked. Tapping the lit one is the way out,
// the same rule the filter chips follow.
func (w *dateDrawer) pickPreset(days int) {
	chosen := presetSpan(days)
	if chosen.same(w.span) {
		chosen = span{}
	}

	w.after.setDay(chosen.after)
	w.before.setDay(chosen.before)
	w.set(chosen)
}

// clear empties both ends and says so, which is what the chip's own way out is.
func (w *dateDrawer) clear() {
	w.reset()
	w.onChange(w.span)
}

// reset is that silently, for a caller putting several things back at once.
func (w *dateDrawer) reset() {
	w.after.setDay(time.Time{})
	w.before.setDay(time.Time{})
	w.mark(span{})
}

func (w *dateDrawer) set(chosen span) {
	w.mark(chosen)
	w.onChange(chosen)
}

// mark records the span and lights whichever preset it happens to be. Compared
// through Equal rather than ==: a day typed into a field and the same day
// computed from today are the same instant, and == on a time.Time answers about
// the struct.
func (w *dateDrawer) mark(chosen span) {
	w.span = chosen
	for index, chip := range w.presets {
		chip.Set(!chosen.empty() && chosen.same(presetSpan(searchSpanPresets[index].days)))
	}
}

// dateField is one end of the span: a labelled box holding a day, or nothing, or
// something that is not a day yet. That third state is why it parses rather than
// binds — a field mid-edit must not be read as an answer.
type dateField struct {
	content fyne.CanvasObject

	entry *modalEntry
	day   time.Time
}

func newDateField(label string, onCancel, onChange func()) *dateField {
	f := &dateField{}

	f.entry = newModalEntry(onCancel)
	f.entry.SetPlaceHolder(dayEntryHint)
	f.entry.OnChanged = func(text string) {
		if f.parse(text) {
			onChange()
		}
	}

	caption := newText(label, theme.Colors.IslandCountText, theme.Sizes.SearchLabelSize)
	f.content = NewFixedWidthContainer(theme.Sizes.SearchDateWidth, VBoxNoSpacing(
		NewInset(caption, 0, theme.Sizes.IslandBadgeGap, theme.Sizes.IslandChipPaddingH, 0),
		searchField(assets.SearchDateIcon, WithCaret(f.entry)),
	))

	return f
}

// parse reads the box and reports whether the day it stands for moved. Text that
// is not a date leaves the last one standing: the reader is still typing it, and
// dropping the bound on every intermediate keystroke would re-ask the route for
// the unbounded answer each time.
func (f *dateField) parse(text string) bool {
	if text == "" {
		return f.take(time.Time{})
	}

	day, err := time.ParseInLocation(dayEntryLayout, text, time.Local)
	if err != nil {
		return false
	}

	return f.take(day)
}

func (f *dateField) take(day time.Time) bool {
	if f.day.Equal(day) {
		return false
	}
	f.day = day

	return true
}

// setDay writes the box from a preset. Silent: the drawer is reporting the whole
// span itself and must not be re-entered once per field.
func (f *dateField) setDay(day time.Time) {
	f.day = day

	text := ""
	if !day.IsZero() {
		text = day.Format(dayEntryLayout)
	}
	if f.entry.Text == text {
		return
	}

	changed := f.entry.OnChanged
	f.entry.OnChanged = nil
	f.entry.SetText(text)
	f.entry.OnChanged = changed
}

// drawerSlot wraps a panel with the gap above it so the two hide together — a
// hidden child is skipped by the column, a spacer left standing on its own is
// not — and sinks it into the island the way the message well is sunk.
func drawerSlot(body fyne.CanvasObject) *fyne.Container {
	pad := theme.Sizes.SearchDrawerPadding

	well := canvas.NewRectangle(theme.Colors.IslandWellBg)
	well.CornerRadius = theme.Sizes.IslandWellRadius
	Outline(well)

	slot := VBoxNoSpacing(
		VerticalSpacer(theme.Sizes.IslandChipGap),
		container.NewStack(well, NewInset(body, pad, pad, pad, pad)),
	)
	slot.Hide()

	return slot
}

// pickerEntry is a field whose list gets first refusal on the keys that move
// through it. Escape still belongs to modalEntry, which is what closes the panel
// the list is in.
type pickerEntry struct {
	modalEntry
	onKey func(*fyne.KeyEvent) bool
}

func newPickerEntry(onCancel func(), onKey func(*fyne.KeyEvent) bool) *pickerEntry {
	e := &pickerEntry{onKey: onKey}
	e.onCancel = onCancel
	e.ExtendBaseWidget(e)

	return e
}

func (e *pickerEntry) TypedKey(key *fyne.KeyEvent) {
	if e.onKey != nil && e.onKey(key) {
		return
	}

	e.modalEntry.TypedKey(key)
}

/* The chip both rows are made of */

// searchChip is a pill that is on or off: a mark, a word, and a wash of the
// accent while it is lit. Not a Button — a run of these is a state read at a
// glance, where a row of buttons says only that seven things can be pressed. Not
// a Toggle either: a switch per filter would be a settings page.
type searchChip struct {
	tapBase

	background *canvas.Rectangle
	icon       *canvas.Image
	label      *canvas.Text
	resource   fyne.Resource
	content    fyne.CanvasObject

	on      bool
	hovered bool
}

var (
	_ fyne.Tappable     = (*searchChip)(nil)
	_ desktop.Hoverable = (*searchChip)(nil)
)

func newSearchChip(res fyne.Resource, label string, onTap func()) *searchChip {
	c := &searchChip{
		background: canvas.NewRectangle(theme.Colors.IslandChipBg),
		icon:       newScaledIcon(res, theme.Sizes.IslandChipGlyph),
		label:      newText(label, theme.Colors.IslandChipText, theme.Sizes.IslandChipTextSize),
		resource:   res,
	}
	c.onTap = onTap
	c.background.CornerRadius = theme.Sizes.IslandChipRadius
	Outline(c.background)

	pad := theme.Sizes.IslandChipPaddingH
	row := HBoxNoSpacing(
		container.NewCenter(c.icon),
		HorizontalSpacer(theme.Sizes.IslandBadgeGap),
		container.NewCenter(c.label),
	)

	c.content = container.NewStack(c.background, NewInset(row, 0, 0, pad, pad))
	c.ExtendBaseWidget(c)
	c.paint()

	return c
}

func (c *searchChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

// MinSize keeps every chip the same height whatever its mark measures, so a
// wrapped run of them lines up.
func (c *searchChip) MinSize() fyne.Size {
	return fyne.NewSize(c.content.MinSize().Width, theme.Sizes.IslandChipHeight)
}

// Set lights the chip or puts it out. Silent — the caller is the one that
// changed the state this is reporting.
func (c *searchChip) Set(on bool) {
	if c.on == on {
		return
	}

	c.on = on
	c.paint()
}

// SetLabel renames the chip, for the ones standing for a value rather than a
// condition. The caller rewraps the run: a longer word is a wider chip.
func (c *searchChip) SetLabel(label string) {
	if c.label.Text == label {
		return
	}

	c.label.Text = label
	c.label.Refresh()
}

func (c *searchChip) MouseIn(*desktop.MouseEvent) { c.setHovered(true) }
func (c *searchChip) MouseOut()                   { c.setHovered(false) }

func (c *searchChip) setHovered(on bool) {
	c.hovered = on
	c.paint()
}

func (c *searchChip) paint() {
	fill, text := theme.Colors.IslandChipBg, theme.Colors.IslandChipText
	switch {
	case c.on:
		fill, text = theme.Colors.IslandChipOnBg, theme.Colors.IslandChipOnText
	case c.hovered:
		fill = theme.Colors.IslandChipHoverBg
	}

	c.background.FillColor = fill
	c.background.Refresh()

	c.icon.Resource = tintedIcon(c.resource, text)
	c.icon.Refresh()

	c.label.Color = solidColor(text)
	c.label.Refresh()
}
