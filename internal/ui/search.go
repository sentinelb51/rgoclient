package ui

// Channel search: the island in panels.go, plus what only this surface has. The
// pins panel and the mention inbox are answers the reader asked one question
// for; a search is a question they refine, so this one carries the controls to
// refine it with — a field, a run of chips, one drawer that completes whatever
// is being typed, and the three orders the route can answer in.
//
// **The field is the query and the chips write into it.** A chip is a shortcut
// for a term and a reading of one, never a state of its own: tapping Images puts
// `has:image` in the field, typing it lights the chip, and there is one string
// either way. The grammar itself is searchquery.go's; nothing here decides what
// a term means, and nothing here resolves a name to an account — the controller
// holds the messages and the store. See app/search.go.

import (
	"strconv"
	"strings"
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

/* The island */

// SearchDialog searches one channel for what is typed in it. Unlike the pins
// panel it asks for nothing until it is told to: a search is a request per query,
// so it runs on submit rather than on every keystroke.
type SearchDialog struct {
	*messageIsland

	Content fyne.CanvasObject

	// Entry is the field to focus once the island is up — a search that has to be
	// clicked into before it can be typed into is a click nobody meant to spend.
	Entry fyne.Focusable

	// OnResize fires when the island's height moves under its own steam: the
	// drawer opening, a chip taking a longer name, a row appearing in the picker.
	// The layer centres what it holds from that height and re-measures for nobody.
	OnResize func()

	onChange func(SearchQuery)

	entry *pickerEntry
	query SearchQuery

	chips  []*termChip
	sorts  map[domain.MessageSort]*searchChip
	drawer *searchDrawer

	note     *canvas.Text
	noteSlot fyne.CanvasObject

	chipRow   *fyne.Container
	block     *fyne.Container   // the chips, the note and the drawer, relaid as one
	clearSlot fyne.CanvasObject // the Clear chip and the gap before it, hidden together
}

// The two chips standing for a value rather than a condition, as they read with
// nothing chosen. Their lit forms name the person or the span instead, so the
// chip is the whole of what it is worth.
const (
	anyoneLabel  = "From anyone"
	anyTimeLabel = "Any time"
)

// searchTermChips are the conditions on offer, in the order they are drawn.
// Each is one term the tap writes into the field, so what a chip means is said
// in searchquery.go and not twice. The three the client already has a mark for
// borrow it: a message carrying an @ and the chip that finds one are otherwise
// the same thing drawn twice.
//
// Not every term is here — `has:embed`, `has:reply` and the four `is:` values
// past pinned are typed or picked out of the drawer. A run of eighteen chips is
// a wall rather than a set of shortcuts.
var searchTermChips = []struct {
	key   SearchKey
	value string
	icon  fyne.Resource
	label string
}{
	{KeyFrom, "me", assets.AccountIcon, "From me"},
	{KeyMentions, "me", assets.MentionIcon, "Mentions me"},
	{KeyIs, "pinned", assets.SystemPinnedIcon, "Pinned"},
	{KeyHas, "file", assets.SearchAttachmentIcon, "Files"},
	{KeyHas, "image", assets.SearchImageIcon, "Images"},
	{KeyHas, "video", assets.SearchVideoIcon, "Video"},
	{KeyHas, "link", assets.SearchLinkIcon, "Links"},
	{KeyHas, "reaction", assets.SearchReactionIcon, "Reactions"},
}

// searchKeyMark is the mark a key is offered under. What a *value* is offered
// under is the chip that writes it where there is one (searchValueMark), so a
// picture means the same shape in the run and in the drawer; the rest fall back
// to their key's, a run of identical magnifiers saying nothing at all.
func searchKeyMark(key SearchKey) fyne.Resource {
	switch key {
	case KeyFrom:
		return assets.MembersIcon
	case KeyMentions:
		return assets.MentionIcon
	case KeyHas:
		return assets.SearchAttachmentIcon
	case KeyIs:
		return assets.SystemPinnedIcon
	case KeyAfter, KeyBefore, KeyDuring:
		return assets.SearchDateIcon
	}

	return assets.SearchIcon
}

func searchValueMark(key SearchKey, value string) fyne.Resource {
	for _, entry := range searchTermChips {
		if entry.key == key && entry.value == value {
			return entry.icon
		}
	}

	return searchKeyMark(key)
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

// NewSearchDialog builds the island for a channel. onChange receives the whole
// query whenever any part of it moves — the field submitted, a chip tapped, a
// person picked, an order chosen — and the controller decides which of those has
// to reach the network. onClose dismisses the layer.
func NewSearchDialog(deps Deps, channel string, onChange func(SearchQuery),
	onMore, onClose func()) *SearchDialog {

	d := &SearchDialog{
		onChange: onChange,
		query:    SearchQuery{Sort: domain.SortRelevance},
		sorts:    make(map[domain.MessageSort]*searchChip, len(searchSortChips)),
	}

	// The field handles Escape itself — see modalEntry — and hands the drawer's
	// list first refusal on the keys that move through it.
	d.entry = newPickerEntry(onClose, d.key)
	d.entry.SetPlaceHolder("Search, or type a filter like has:image")
	d.entry.OnSubmitted = d.submit
	d.entry.OnChanged = d.typed
	d.Entry = d.entry

	// The controls are built before the island rather than into it: the island is
	// the shell all three surfaces share, and what a question is refined with is
	// this one's alone.
	island, content := newMessageIsland(deps, islandParts{
		Mark:     assets.SearchIcon,
		Title:    "Search",
		Where:    "in " + channel,
		Controls: []fyne.CanvasObject{d.buildField(), d.buildChips(deps)},
		Trailing: d.buildSorts(),
		OnMore:   onMore,
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

// searchField is that box, shared with the drawer's own date fields so a day
// typed into one looks like a query typed into the other.
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

// buildChips is the run of chips, the line under it and the drawer, wrapping
// against the island's inner width. All three hang together because all three
// are about the field above them; only one drawer is ever up, so the island
// grows by one panel at most. A Clear that only appeared when something was on
// would put a second row under the run half the time, which is why Clear rides
// in the count row instead.
func (d *SearchDialog) buildChips(deps Deps) fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(searchTermChips)+2)

	for _, entry := range searchTermChips {
		chip := newTermChip(entry.icon, entry.label, entry.key, entry.value)
		chip.onTap = func() { d.toggleTerm(entry.key, entry.value) }

		d.chips = append(d.chips, chip)
		chips = append(chips, chip)
	}

	// The two that stand for a value rather than a condition, so a tap opens the
	// drawer where the others simply light.
	people := newTermChip(assets.MembersIcon, anyoneLabel, KeyFrom, "")
	people.onTap = func() { d.reachFor(KeyFrom) }

	days := newTermChip(assets.SearchDateIcon, anyTimeLabel, KeyAfter, "")
	days.onTap = func() { d.reachFor(KeyAfter) }

	d.chips = append(d.chips, people, days)
	chips = append(chips, people, days)

	d.chipRow = NewFlow(islandInnerWidth(), theme.Sizes.IslandChipGap, chips...)
	d.drawer = newSearchDrawer(deps, d.complete, d.setSpan, d.closeDrawer, d.resized)

	// The note is what the island says about the *query* rather than about the
	// answer — a filter that named nothing, a person nobody here is called — so it
	// stands with the field rather than in the well.
	d.note = newText("", theme.Colors.IslandHintText, theme.Sizes.IslandPreviewSize)
	d.noteSlot = VBoxNoSpacing(
		VerticalSpacer(theme.Sizes.IslandChipGap),
		NewInset(d.note, 0, 0, theme.Sizes.IslandChipPaddingH, 0),
	)
	d.noteSlot.Hide()

	d.block = VBoxNoSpacing(d.chipRow, d.noteSlot, d.drawer.slot)

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

/* What the field says */

// SetAuthors hands the drawer the people this channel can be narrowed to,
// resolved by the controller off the UI thread. Late is the ordinary case — a
// server's membership is a walk — so the drawer can already be open, which is why
// it re-runs its own query on being refilled. Call on the UI thread.
func (d *SearchDialog) SetAuthors(candidates []MentionCandidate) {
	d.drawer.setCandidates(candidates)
	if d.drawer.mode == drawerPeople {
		d.syncDrawer()
	}
}

// SetNote is what the island says about the query itself, "" for nothing to say.
// Call on the UI thread.
func (d *SearchDialog) SetNote(note string) {
	if d.note.Text != note {
		d.note.Text = note
		d.note.Refresh()
	}

	showIf(d.noteSlot, note != "")
	d.resized()
}

// typed runs on every keystroke and reaches the network for nothing: what it
// moves is the drawer, which completes whatever is at the end of the field.
func (d *SearchDialog) typed(string) {
	d.syncDrawer()
}

// submit is Enter: the field as it stands, parsed, reported.
func (d *SearchDialog) submit(string) {
	d.closeDrawer()
	d.report()
}

// toggleTerm is a chip tap — the term goes into the field or comes out of it,
// and the query is re-asked. The field is rewritten rather than a flag being
// set: there is one query and it is the text.
func (d *SearchDialog) toggleTerm(key SearchKey, value string) {
	d.closeDrawer()
	d.setRaw(ToggleTerm(d.entry.Text, key, value))
	d.report()
}

// reachFor opens the drawer on a key the reader tapped rather than typed, and is
// what the two value chips do. A chip standing for something already chosen puts
// it out instead, the rule every other chip in the run follows.
func (d *SearchDialog) reachFor(key SearchKey) {
	if d.dropChosen(key) {
		return
	}
	if d.drawer.showing(key) {
		d.closeDrawer()
		return
	}

	// Typed rather than opened directly, so the drawer's own reading of the field
	// is the only thing that decides what it shows. Only where the field is not
	// already sitting at this key: a chip tapped shut and open again would
	// otherwise leave `after: after:` behind it.
	if completionIn(d.entry.Text).key != key {
		d.setRaw(appendKey(d.entry.Text, key))
	}

	d.syncDrawer()
}

// dropChosen clears the value a chip stands for, answering whether there was
// one. A span is both its ends; a person is however many were named.
func (d *SearchDialog) dropChosen(key SearchKey) bool {
	terms := ParseSearchTerms(d.entry.Text)

	if key == KeyFrom {
		if len(terms.From) == 0 {
			return false
		}

		d.setRaw(SetTerm(d.entry.Text, KeyFrom, ""))
		d.report()

		return true
	}

	if terms.After.IsZero() && terms.Before.IsZero() {
		return false
	}
	d.setRaw(clearSpan(d.entry.Text))
	d.report()

	return true
}

// complete is a pick out of the drawer: the part being typed is replaced by what
// was chosen. A key completes to itself and a colon and leaves the drawer open on
// its values — one pick is half an answer there — where anything else finishes
// the term and re-asks.
func (d *SearchDialog) complete(text string, done bool) {
	at := completionIn(d.entry.Text)
	d.setRaw(completeWith(d.entry.Text, at, text))

	if !done {
		d.syncDrawer()
		d.focus(d.entry)

		return
	}

	d.closeDrawer()
	d.report()
	d.focus(d.entry)
}

// setSpan is the date panel's answer, written as the two terms it stands for.
// `during:` is dropped on the way: the panel names two ends, and a window said
// twice over is one the reader cannot correct from here.
func (d *SearchDialog) setSpan(s span) {
	raw := SetTerm(d.entry.Text, KeyDuring, "")
	raw = SetTerm(raw, KeyAfter, dayValue(s.after))
	raw = SetTerm(raw, KeyBefore, dayValue(s.before))

	d.setRaw(raw)
	d.report()
}

// clearFilters puts the whole of the narrowing back and leaves the words: Clear
// names what the count line lost, and a person or a span took as much of it as a
// chip did. What the reader was searching *for* is not a filter.
func (d *SearchDialog) clearFilters() {
	d.closeDrawer()
	d.setRaw(ParseSearchTerms(d.entry.Text).Text)
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

	d.report()
}

// setRaw writes the field without the change coming back as a keystroke: every
// caller here is already deciding what happens next, and re-entering the drawer
// from inside a chip tap would reopen it under the pointer.
func (d *SearchDialog) setRaw(raw string) {
	if d.entry.Text == raw {
		return
	}

	changed := d.entry.OnChanged
	d.entry.OnChanged = nil
	d.entry.SetText(raw)
	d.entry.OnChanged = changed

	// SetText leaves the caret where it stood, so what is typed next would carry on
	// from the middle of a term nobody typed. The picker's own accept path is the
	// same two lines for the same reason.
	d.entry.CursorRow, d.entry.CursorColumn = cursorPosition(raw, len(raw))
	d.entry.Refresh()
}

// report parses the field, repaints what reads it and hands the query over. A
// relabelled chip is a differently wide chip, so the run is rewrapped before the
// query goes anywhere.
func (d *SearchDialog) report() {
	d.query = NewSearchQuery(d.entry.Text, d.query.Sort)

	d.paintChips()
	d.resized()
	d.onChange(d.query)
}

// paintChips lights each chip from the field. A chip standing for a value says
// what the value is, so the run is what the query reads as.
func (d *SearchDialog) paintChips() {
	terms := d.query.Terms

	for _, chip := range d.chips {
		switch {
		case chip.value != "":
			chip.Set(terms.holds(chip.key, chip.value))
		case chip.key == KeyFrom:
			chip.SetLabel(fromLabel(terms.From))
			chip.Set(len(terms.From) > 0)
		default:
			chip.SetLabel(spanLabel(terms.After, terms.Before))
			chip.Set(!terms.After.IsZero() || !terms.Before.IsZero())
		}
	}

	showIf(d.clearSlot, d.query.Narrowed())
}

// fromLabel names who the answer is narrowed to. One person is named; more than
// one is counted, a run of handles being wider than the island.
func fromLabel(from []string) string {
	switch len(from) {
	case 0:
		return anyoneLabel
	case 1:
		return "From " + from[0]
	}

	return "From " + strconv.Itoa(len(from)) + " people"
}

// key lets the drawer's list consume the keys that move through it, exactly as
// the composer's own does. Enter belongs to the list while one is up: a candidate
// under the cursor is what the reader is answering, not the query behind it.
func (d *SearchDialog) key(event *fyne.KeyEvent) bool {
	return d.drawer.key(event)
}

// syncDrawer re-reads the field and shows whatever completes the end of it.
func (d *SearchDialog) syncDrawer() {
	d.drawer.show(completionIn(d.entry.Text), ParseSearchTerms(d.entry.Text))
	d.resized()
}

func (d *SearchDialog) closeDrawer() {
	if d.drawer.mode == drawerNone {
		return
	}

	d.drawer.close()
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

// SearchOutcome is what one search came to. Scanned is how many messages were
// read to find them, which is the honest denominator now that a narrow question
// is answered by walking rather than by one request: the route filters on words
// alone, so everything else is found by reading messages back.
type SearchOutcome struct {
	Results []MessageCard
	Scanned int

	// More is what another press reads, "" where there is nothing further to ask
	// for, and Busy draws it as the request it already is.
	More string
	Busy bool
}

// SetResults replaces the cards. Call on the UI thread.
func (d *SearchDialog) SetResults(outcome SearchOutcome) {
	cards := make([]fyne.CanvasObject, 0, len(outcome.Results))
	for _, result := range outcome.Results {
		cards = append(cards, newMessageCard(d.deps, result))
	}

	d.setCards(cards)

	switch {
	case len(outcome.Results) > 0:
		d.setCount(countLine(len(outcome.Results), outcome.Scanned))
		d.say("")
	case outcome.Scanned > 0:
		d.setCount(countLine(0, outcome.Scanned))
		d.say("Nothing here matches that.")
	default:
		d.setCount("")
		d.say("Nothing matched that.")
	}

	d.SetMore(outcome.More, outcome.Busy)
}

// Prompt is the island as it opens: nothing asked, nothing to count.
func (d *SearchDialog) Prompt() {
	d.reset("Type something and press Enter, or pick a filter.")
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

// countLine says how much of the answer is on screen and how much was read to
// find it. The second number appears only where it differs: a search that
// answered every message it looked at is one whose denominator says nothing.
func countLine(shown, scanned int) string {
	if shown == scanned {
		return util.Quantity(shown, "result")
	}

	return util.Quantity(shown, "result") + " in " + strconv.Itoa(scanned) + " messages"
}

/* The drawer */

// drawerMode is which of the drawer's four bodies is up. What decides it is the
// end of the field: a bare word is a key to name, a key and a colon is a value to
// pick, and the two that take an answer rather than a condition have a panel
// each.
type drawerMode uint8

const (
	drawerNone drawerMode = iota
	drawerKeys
	drawerValues
	drawerPeople
	drawerDays
)

// searchDrawer is the one panel under the field, and is what makes a syntax
// discoverable: it completes whatever is being typed rather than the reader
// having to know the words. One drawer rather than one per kind of answer,
// because only one thing is ever being typed.
type searchDrawer struct {
	slot *fyne.Container
	mode drawerMode

	keys   *fyne.Container
	values *fyne.Container
	picker *MentionPicker
	days   *dateDrawer

	hint *canvas.Text

	onComplete func(text string, done bool)
	onResize   func()

	empty bool // no candidates at all, which is a different sentence from no match
}

func newSearchDrawer(deps Deps, onComplete func(string, bool), onSpan func(span),
	onCancel, onResize func()) *searchDrawer {

	w := &searchDrawer{onComplete: onComplete, onResize: onResize, empty: true}

	w.keys = NewFlow(searchDrawerWidth(), theme.Sizes.IslandChipGap)
	w.values = NewFlow(searchDrawerWidth(), theme.Sizes.IslandChipGap)
	w.picker = NewMentionPicker(deps, w.accept)
	w.days = newDateDrawer(onSpan, onCancel)
	w.hint = newText("", theme.Colors.IslandHintText, theme.Sizes.IslandPreviewSize)

	w.slot = drawerSlot(VBoxNoSpacing(
		w.keys,
		w.values,
		w.picker,
		w.days.body,
		NewInset(w.hint, 0, 0, theme.Sizes.IslandChipPaddingH, 0),
	))

	return w
}

// searchDrawerWidth is what a run inside the drawer wraps against: the island's
// own room, less the well's padding on both sides.
func searchDrawerWidth() float32 {
	return islandInnerWidth() - 2*theme.Sizes.SearchDrawerPadding
}

// setCandidates replaces the pool the people body draws from.
func (w *searchDrawer) setCandidates(candidates []MentionCandidate) {
	w.empty = len(candidates) == 0
	w.picker.SetCandidates(MentionUser, candidates)
}

// show reads the end of the field and puts up whatever completes it. terms is
// the field already parsed, so the value body can mark what is already on.
func (w *searchDrawer) show(at searchCompletion, terms SearchTerms) {
	switch {
	case at.key == KeyNone && at.prefix == "" && w.mode == drawerNone:
		// Nothing typed and nothing open: a drawer that appeared on its own the
		// moment the island did would cover the answer before there was one.
		return
	case at.key == KeyNone:
		w.showKeys(at.prefix)
	case at.key == KeyFrom || at.key == KeyMentions:
		w.showPeople(at.prefix)
	case at.key == KeyHas || at.key == KeyIs:
		w.showValues(at.key, at.prefix, terms)
	default:
		w.showDays(terms)
	}
}

// showKeys lists the filters, narrowed by what has been typed of one. A word
// that matches no key closes the drawer rather than saying so: most words are
// what the reader is searching for.
func (w *searchDrawer) showKeys(prefix string) {
	chips := make([]fyne.CanvasObject, 0, len(searchKeys))
	for _, entry := range searchKeys {
		if !strings.HasPrefix(entry.name, prefix) {
			continue
		}

		chip := newSearchChip(searchKeyMark(entry.key), entry.name+": "+entry.hint, nil)
		chip.onTap = func() { w.onComplete(entry.name+":", false) }
		chips = append(chips, chip)
	}
	if len(chips) == 0 {
		w.close()
		return
	}

	w.keys.Objects = chips
	w.wear(drawerKeys, "")
}

// showValues lists what one key takes, marking what the field already holds so
// the run reads as the state as well as the offer.
func (w *searchDrawer) showValues(key SearchKey, prefix string, terms SearchTerms) {
	chips := make([]fyne.CanvasObject, 0, len(searchFlagValues))
	for _, entry := range searchFlagValues {
		if entry.key != key || !strings.HasPrefix(entry.value, prefix) {
			continue
		}

		chip := newSearchChip(searchValueMark(key, entry.value), entry.value, nil)
		chip.Set(terms.holds(key, entry.value))
		chip.onTap = func() { w.onComplete(entry.value, true) }
		chips = append(chips, chip)
	}
	if len(chips) == 0 {
		w.values.Objects = nil
		w.wear(drawerValues, searchKeyName(key)+" does not take that. Comma-separate for either.")

		return
	}

	w.values.Objects = chips
	w.wear(drawerValues, "Comma-separate values for either of them.")
}

// showPeople is the composer's own mention picker, which is what makes a
// 2000-member server cheap to filter — it ranks into fixed scratch and allocates
// nothing per keystroke — and makes these rows look like the rows an @ opens,
// which is where the reader last saw this list.
func (w *searchDrawer) showPeople(prefix string) {
	if w.picker.Update(MentionUser, prefix) {
		w.wear(drawerPeople, "")
		return
	}

	hint := "Nobody by that name."
	if w.empty {
		hint = "Nobody here to narrow by yet."
	}
	w.wear(drawerPeople, hint)
}

// showDays is the two ends typed out and the runs worth not typing. Unlike the
// others it stays put after a choice — a range is two answers, and a preset is as
// often the start of narrowing one as the end of it.
func (w *searchDrawer) showDays(terms SearchTerms) {
	w.days.fill(terms)
	w.wear(drawerDays, "")
}

// wear shows one body and puts the rest away. A hidden child is skipped by the
// column, so the drawer is as tall as whichever is up.
func (w *searchDrawer) wear(mode drawerMode, hint string) {
	w.mode = mode

	showIf(w.keys, mode == drawerKeys)
	showIf(w.values, mode == drawerValues)
	showIf(w.picker, mode == drawerPeople && hint == "")
	showIf(w.days.body, mode == drawerDays)

	if w.hint.Text != hint {
		w.hint.Text = hint
		w.hint.Refresh()
	}
	showIf(w.hint, hint != "")

	w.slot.Show()
	Relayout(w.slot)
}

// showing reports whether the body a key opens is the one already up, which is
// what makes its chip a toggle rather than a switch between two panels: tapping
// the date chip while the people list is open moves to the dates, and tapping it
// again puts them away.
func (w *searchDrawer) showing(key SearchKey) bool {
	switch key {
	case KeyFrom, KeyMentions:
		return w.mode == drawerPeople
	case KeyAfter, KeyBefore, KeyDuring:
		return w.mode == drawerDays
	}

	return false
}

func (w *searchDrawer) close() {
	w.mode = drawerNone
	w.picker.Reset()
	w.picker.Hide()
	w.slot.Hide()
}

// accept reports the chosen person as the handle a term is written with — a
// handle has no spaces where a display name routinely does, and both find the
// same account. Guarded because the picker offers whatever it last matched, and
// an empty list matches a candidate with no ID at all.
func (w *searchDrawer) accept(candidate MentionCandidate) {
	if candidate.ID == "" {
		return
	}

	name := candidate.Username
	if name == "" {
		name = candidate.Name
	}

	w.onComplete(quoteValue(name), true)
}

// key lets the people list consume the keys that move through it. The other
// bodies are chips, which are tapped: a run of shortcuts does not need a cursor.
func (w *searchDrawer) key(event *fyne.KeyEvent) bool {
	if w.mode != drawerPeople || !w.picker.Visible() {
		return false
	}

	switch event.Name {
	case fyne.KeyUp:
		w.picker.Step(-1)
	case fyne.KeyDown:
		w.picker.Step(1)
	case fyne.KeyTab, fyne.KeyReturn, fyne.KeyEnter:
		w.picker.Accept()
	default:
		return false
	}

	return true
}

/* When it was written */

// span is the two *days* a date panel has been told, either of them zero for an
// end left open. Days rather than instants, because a day is what was typed and
// what the chip has to be able to say back.
type span struct {
	after, before time.Time
}

func (s span) empty() bool { return s.after.IsZero() && s.before.IsZero() }

// same compares two spans by the days they name. == would compare the time.Time
// structs, which a parsed day and a computed one need not share.
func (s span) same(other span) bool {
	return s.after.Equal(other.after) && s.before.Equal(other.before)
}

// spanLabel is what the date chip reads for the window the query holds. Before
// is the first instant *dropped*, so the last day inside the span is the one
// before it — which is the day the reader named.
func spanLabel(after, before time.Time) string {
	last := time.Time{}
	if !before.IsZero() {
		last = before.AddDate(0, 0, -1)
	}

	switch {
	case after.IsZero() && last.IsZero():
		return anyTimeLabel
	case last.IsZero():
		return "Since " + shortDay(after)
	case after.IsZero():
		return "Until " + shortDay(last)
	case after.Equal(last):
		return shortDay(after)
	}

	return shortDay(after) + " to " + shortDay(last)
}

// The layouts a day is read and written in: typed as the unambiguous ordering,
// shown on a chip as the short one, with the year only when it is not this one.
// A month and a year are the shorter forms `during:` takes, a reader naming
// August rather than its thirty-one days.
//
// dayEntryHint is that first layout said in letters rather than in Go's
// reference date, which as a placeholder reads as a date somebody already typed.
const (
	dayEntryLayout   = "2006-01-02"
	monthEntryLayout = "2006-01"
	yearEntryLayout  = "2006"
	dayEntryHint     = "YYYY-MM-DD"
	shortDayLayout   = "Jan 2"
	longDayLayout    = "Jan 2, 2006"
)

// shortDay names a day in as little as still says it.
func shortDay(t time.Time) string {
	if t.Year() != time.Now().Year() {
		return t.Format(longDayLayout)
	}

	return t.Format(shortDayLayout)
}

// dayValue is a day as a term is written with it, "" for an end left open.
func dayValue(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.Format(dayEntryLayout)
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

// dateDrawer is the drawer's date body: the two ends typed out, and the runs
// worth not typing. It reports a span rather than writing the field itself — the
// dialog owns the text, and two writers of one string is two readings of it.
type dateDrawer struct {
	body *fyne.Container

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
	w.body = VBoxNoSpacing(
		HBoxNoSpacing(w.after.content, HorizontalSpacer(gap), w.before.content),
		VerticalSpacer(gap),
		NewFlow(searchDrawerWidth(), gap, chips...),
	)

	return w
}

// fill seeds the panel from the field, so a span typed as terms and one picked
// here are the same two boxes. Before is the first instant dropped, so the box
// says the day before it — which is the day `before:` named.
func (w *dateDrawer) fill(terms SearchTerms) {
	held := span{after: terms.After}
	if !terms.Before.IsZero() {
		held.before = terms.Before.AddDate(0, 0, -1)
	}

	w.after.setDay(held.after)
	w.before.setDay(held.before)
	w.mark(held)
}

// commit reports the span the fields now hold. Driven from the fields' own
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

// setDay writes the box from a preset or from the field. Silent: the drawer is
// reporting the whole span itself and must not be re-entered once per box.
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

	if c.background.FillColor != fill {
		c.background.FillColor = fill
		c.background.Refresh()
	}

	// The tint follows on, not hover, so a pointer sweeping the run repaints
	// backgrounds alone rather than re-tinting an icon per crossing.
	tinted := solidColor(text)
	if c.label.Color != tinted {
		c.icon.Resource = tintedIcon(c.resource, text)
		c.icon.Refresh()

		c.label.Color = tinted
		c.label.Refresh()
	}
}

// termChip is that chip carrying the term it writes, for the run above the
// field. A chip with no value stands for a *kind* of answer rather than a
// condition — which person, which days — and opens the drawer instead of
// toggling.
type termChip struct {
	*searchChip

	key   SearchKey
	value string
}

func newTermChip(res fyne.Resource, label string, key SearchKey, value string) *termChip {
	return &termChip{searchChip: newSearchChip(res, label, nil), key: key, value: value}
}

// pickChip is that chip carrying the number it stands for, for a run where one
// is the answer rather than each being a bit of its own. A filter chip is a
// condition; this is one of a set, which is the whole difference — and the tap
// is what enforces it, not the chip. The screenshare picker and the crop card
// are both made of these.
type pickChip struct {
	*searchChip

	value int
}

func newPickChip(res fyne.Resource, label string, value int) *pickChip {
	return &pickChip{searchChip: newSearchChip(res, label, nil), value: value}
}

// markPickChips lights the one chip standing for value and puts out the rest.
func markPickChips(chips []*pickChip, value int) {
	for _, chip := range chips {
		chip.Set(chip.value == value)
	}
}
