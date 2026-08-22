package ui

// Channel search: the island in panels.go, plus what only this surface has. The
// pins panel and the mention inbox are answers the reader asked one question
// for; a search is a question they refine, so this one carries the controls to
// refine it with — a field, a run of filter chips and the three orders the route
// can answer in.
//
// Nothing here decides what a filter means. The dialog owns which chips are lit
// and reports the whole query on every change; the controller holds the messages
// and knows whether a change has to reach the network — see app/search.go.

import (
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* What a search asks for */

// SearchFilter is one condition a result has to meet. They narrow what came back
// rather than what was asked for: Revolt's search takes a query, an order and a
// limit and nothing else, so every one of these is applied to the answer.
type SearchFilter int

const (
	FilterFromMe SearchFilter = iota
	FilterMentionsMe
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
}

// SameRequest reports whether q and other would be answered by the same request.
// Filters are not in it: they are applied to the answer, so changing one costs
// nothing.
func (q SearchQuery) SameRequest(other SearchQuery) bool {
	return q.Text == other.Text && q.Sort == other.Sort
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

	onChange func(SearchQuery)

	entry *modalEntry
	query SearchQuery

	filters   map[SearchFilter]*searchChip
	sorts     map[domain.MessageSort]*searchChip
	clearSlot fyne.CanvasObject // the Clear chip and the gap before it, hidden together
}

// searchFilterChips are the conditions on offer, in the order they are drawn.
// The three the client already has a mark for borrow it: a message carrying an @
// and the chip that finds one are otherwise the same thing drawn twice.
var searchFilterChips = []struct {
	filter SearchFilter
	icon   fyne.Resource
	label  string
}{
	{FilterFromMe, assets.AccountIcon, "From me"},
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

// NewSearchDialog builds the island for a channel. onChange receives the whole
// query whenever any part of it moves — the field submitted, a filter toggled,
// an order picked — and the controller decides which of those has to reach the
// network. onClose dismisses the layer.
func NewSearchDialog(deps Deps, channel string, onChange func(SearchQuery), onClose func()) *SearchDialog {
	d := &SearchDialog{
		onChange: onChange,
		query:    SearchQuery{Sort: domain.SortRelevance},
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
		Controls: []fyne.CanvasObject{d.buildField(), d.buildFilters()},
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
	pad := theme.Sizes.IslandChipPaddingH

	mark := newScaledIcon(tintedIcon(assets.SearchIcon, theme.Colors.IslandHintText),
		theme.Sizes.SearchFieldGlyph)

	field := canvas.NewRectangle(theme.Colors.ComposerBg)
	field.CornerRadius = theme.Sizes.SearchFieldRadius
	Outline(field)

	row := NewFillRow(2,
		container.NewCenter(mark),
		HorizontalSpacer(theme.Sizes.IslandChipGap),
		WithCaret(d.entry),
	)

	return NewFixedHeightContainer(theme.Sizes.SearchFieldHeight,
		container.NewStack(field, NewInset(row, 0, 0, pad, pad)))
}

// buildFilters is the run of chips, wrapping against the island's inner width.
// It holds nothing else — the run fills the row as it is, and a Clear that only
// appears when something is on would put a second row under it half the time,
// which is why Clear rides in the count row instead.
func (d *SearchDialog) buildFilters() fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(searchFilterChips))
	for _, entry := range searchFilterChips {
		chip := newSearchChip(entry.icon, entry.label, nil)
		chip.onTap = func() { d.toggle(entry.filter) }

		d.filters[entry.filter] = chip
		chips = append(chips, chip)
	}

	return NewFlow(islandInnerWidth(), theme.Sizes.IslandChipGap, chips...)
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

func (d *SearchDialog) clearFilters() {
	d.query.Filters = 0
	d.paintFilters()
	d.onChange(d.query)
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

func (d *SearchDialog) paintFilters() {
	for filter, chip := range d.filters {
		chip.Set(d.query.Filters.Has(filter))
	}

	showIf(d.clearSlot, d.query.Filters.Any())
}

/* Filling it */

// SetResults replaces the cards. found is how many came back before the filters
// were applied, which the line reports alongside: the route caps an answer at
// 100 with no paging, so a reader narrowing one has to be able to see that they
// are narrowing that hundred. Call on the UI thread.
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
		return plural(found, "result")
	}

	return strconv.Itoa(shown) + " of " + plural(found, "result")
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
