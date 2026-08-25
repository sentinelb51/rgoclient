package ui

// The settings search: the field at the head of the rail, the index it matches
// against, and the page of results it puts in the pane.
//
// Nothing here knows what any setting *is*. The index is taken by walking the
// sections themselves with the rows answering with their names instead of
// building anything, so a section that gains a row gains a result for free and
// there is no second list of names to keep in step.

import (
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// settingsResultLimit is how many results are drawn before the rest are counted
// instead. A short query matches most of the palette, and a page nobody can read
// to the end of is not worth the hundreds of controls it costs to build.
const settingsResultLimit = 40

/* What the search matches against */

// settingsHit is one place a search can lead: a row, or the group heading it
// sits under.
type settingsHit struct {
	section SettingsSection
	group   string
	label   string

	// field is the theme entry an Advanced result draws its own control from.
	// Those rows are built from a name and nothing else, so the search *is* the
	// filter the Advanced lists used to carry a field of their own for: a size or
	// a colour is edited in the results rather than jumped to. Empty everywhere
	// else, which is what makes a result a link.
	field string
	color bool

	// advanced marks a hit only advanced mode reveals, so a basic-mode search
	// never offers somewhere that will not be there on arrival.
	advanced bool
}

// hitKey is a hit's identity across the two index passes.
type hitKey struct {
	section      SettingsSection
	group, label string
}

func (h settingsHit) at() hitKey {
	return hitKey{section: h.section, group: h.group, label: h.label}
}

// indexRow stands in for a row while the page is being indexed: it carries the
// name the search matches against and is never drawn. adv drops one exactly as it
// drops a real row, which is what lets the basic pass record what basic mode
// shows without a flag of its own.
type indexRow struct {
	*canvas.Rectangle
	label string
}

func newIndexRow(label string) *indexRow {
	return &indexRow{Rectangle: canvas.NewRectangle(color.Transparent), label: label}
}

// recordGroup files one group's caption and the rows that survived it. Called in
// place of building the card, so an index pass measures no text and mounts
// nothing.
func (p *SettingsPage) recordGroup(caption string, rows []fyne.CanvasObject) settingsGroup {
	if caption != "" {
		p.index = append(p.index, settingsHit{section: p.section, group: caption, label: caption})
	}

	for _, row := range rows {
		stub, ok := row.(*indexRow)
		if !ok {
			continue // a separator, or a note: nothing anybody searches for
		}

		p.index = append(p.index, settingsHit{section: p.section, group: caption, label: stub.label})
	}

	return settingsGroup{caption: caption}
}

// recordFields files one of the Advanced lists. Those rows are a line of table
// each, so they are indexed from the table rather than built and walked.
func (p *SettingsPage) recordFields(caption string, fields []string, colour bool) settingsGroup {
	p.index = append(p.index, settingsHit{section: p.section, group: caption, label: caption})

	for _, field := range fields {
		p.index = append(p.index, settingsHit{
			section: p.section,
			group:   caption,
			label:   field,
			field:   field,
			color:   colour,
		})
	}

	return settingsGroup{caption: caption}
}

// searchHits is the index, built on the first keystroke. Most opens never search,
// and the walk is two builds of every section.
func (p *SettingsPage) searchHits() []settingsHit {
	if p.index == nil {
		p.index = buildSettingsIndex(p.hooks)
	}

	return p.index
}

// buildSettingsIndex walks the sections twice: once as basic mode shows them and
// once as advanced mode does. What only the second pass saw is what the mode
// reveals — read off the difference rather than declared, because a row is
// withheld in more than one way (adv on the row, advanced on a whole style group)
// and only one of them is visible from the row itself.
func buildSettingsIndex(hooks SettingsHooks) []settingsHit {
	// The two hooks that answer from somewhere else entirely — a request for the
	// profile, a walk of the cache directory. Nothing built here is ever shown, so
	// neither would have anywhere to land.
	hooks.LoadProfile = func(func(domain.UserProfile)) {}
	hooks.CacheStats = func(func(cache.ImageStats)) {}
	hooks.LoadSecurity = func(func(SecurityState, error)) {}

	// The same rule with a device behind it: enumerating is a walk of the audio
	// backend, and StartInputMonitor *opens a microphone*. Doing either on the
	// first keystroke in the search box, for a page nobody is looking at, is the
	// bug. A nil device list is harmless — optionRow records only its label.
	hooks.InputDevices = nil
	hooks.OutputDevices = nil
	hooks.StartInputMonitor = nil
	hooks.StopInputMonitor = nil

	basic := indexPass(hooks, false)

	shown := make(map[hitKey]bool, len(basic))
	for _, hit := range basic {
		shown[hit.at()] = true
	}

	full := indexPass(hooks, true)
	for i := range full {
		full[i].advanced = !shown[full[i].at()]
	}

	return full
}

// indexPass builds every section a mode lists, onto a page that exists for the
// walk alone.
func indexPass(hooks SettingsHooks, advanced bool) []settingsHit {
	p := &SettingsPage{hooks: hooks, advanced: advanced}
	p.indexing = true
	p.record = p.recordGroup

	for _, entry := range visibleRailEntries(advanced) {
		p.section = SettingsSection(entry.section)
		p.sectionGroups(p.section)
	}

	return p.index
}

/* The field */

// buildSearchField is the box at the head of the rail. Its own surface rather
// than textField's, that one being a control sized to a row's trailing slot.
func (p *SettingsPage) buildSearchField() fyne.CanvasObject {
	entry := &searchEntry{}
	entry.ExtendBaseWidget(entry)
	entry.PlaceHolder = "Search settings"
	entry.Text = p.query
	entry.OnChanged = p.onQuery
	p.field = entry

	mark := newScaledIcon(tintedIcon(assets.SearchIcon, theme.Colors.CategoryText),
		theme.Sizes.SettingsIconSize)

	row := NewFillRow(2,
		container.NewCenter(mark),
		HorizontalSpacer(theme.Sizes.ChipDotGap),
		WithCaret(entry),
	)

	return NewFixedHeightContainer(theme.Sizes.SettingsInputHeight,
		container.NewStack(newFieldBackground(),
			NewInset(row, 0, 0, theme.Sizes.ChipPaddingH, theme.Sizes.ChipPaddingH)))
}

// searchEntry is the query field. Escape empties it rather than reaching the page
// as "close", but only while there is something to empty: a search is what the
// reader wants out of first, and once it is gone the key means what it means
// everywhere else in the client.
type searchEntry struct {
	widget.Entry
}

func (e *searchEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape && e.Text != "" {
		e.SetText("")
		return
	}

	e.Entry.TypedKey(key)
}

// onQuery answers a keystroke. Emptying the field puts the open section back
// rather than leaving an empty page of results.
func (p *SettingsPage) onQuery(text string) {
	query := strings.TrimSpace(text)
	if query == p.query {
		return
	}
	p.query = query

	if query == "" {
		p.showSection(p.section)
	} else {
		p.showResults()
	}
}

/* The results */

// showResults swaps the pane to what the query matches. Results stand inside no
// section — the rail marks nothing while they show — so the back line is the only
// way out of them that does not need the field found and emptied by hand.
func (p *SettingsPage) showResults() {
	p.searching = true
	p.account = accountRows{} // a profile landing after this has nothing left to fill
	p.previews = nil
	p.mountUnder(p.resultGroups(), "Search",
		backLink{label: "All settings", onTap: p.clearQuery})
	p.rebuildRail()
}

// clearQuery empties the field, which puts the open section back through onQuery.
// Emptying p.query alone would leave the box holding a search nothing is showing,
// and onQuery ignores a keystroke that types it again.
func (p *SettingsPage) clearQuery() {
	if p.field == nil {
		return
	}

	p.field.SetText("")
}

// matches is the index filtered by the query, and how many more the limit
// dropped. Declaration order throughout, which is rail order: the friendly
// sections come before the raw tables, so a short query spends the limit on the
// settings that have names rather than on the palette.
func (p *SettingsPage) matches() ([]settingsHit, int) {
	query := strings.ToLower(p.query)

	var found []settingsHit
	for _, hit := range p.searchHits() {
		if hit.advanced && !p.advanced {
			continue
		}
		if !strings.Contains(strings.ToLower(hit.label), query) {
			continue
		}

		found = append(found, hit)
	}

	return capResults(dropRepeats(found))
}

// dropRepeats takes out what a reader would read twice: a group heading whose own
// rows answered as well — it leads to the same card those rows are on — and a
// second entry with the name of one already listed in the same section.
func dropRepeats(found []settingsHit) []settingsHit {
	answered := make(map[hitKey]bool, len(found))
	for _, hit := range found {
		if hit.label != hit.group {
			answered[hitKey{section: hit.section, group: hit.group}] = true
		}
	}

	kept := make([]settingsHit, 0, len(found))
	listed := make(map[hitKey]bool, len(found))

	for _, hit := range found {
		if hit.label == hit.group && answered[hitKey{section: hit.section, group: hit.group}] {
			continue
		}

		name := hitKey{section: hit.section, label: hit.label}
		if listed[name] {
			continue
		}
		listed[name] = true

		kept = append(kept, hit)
	}

	return kept
}

// capResults is the limit, and what it left behind.
func capResults(kept []settingsHit) ([]settingsHit, int) {
	if len(kept) <= settingsResultLimit {
		return kept, 0
	}

	return kept[:settingsResultLimit], len(kept) - settingsResultLimit
}

// resultGroups is one card per section, in rail order.
func (p *SettingsPage) resultGroups() []settingsGroup {
	hits, more := p.matches()
	if len(hits) == 0 {
		return []settingsGroup{p.group("", "", p.note("Nothing on these pages is called that."))}
	}

	var groups []settingsGroup
	for start := 0; start < len(hits); {
		end := start
		for end < len(hits) && hits[end].section == hits[start].section {
			end++
		}

		rows := make([]fyne.CanvasObject, 0, end-start)
		for _, hit := range hits[start:end] {
			rows = append(rows, p.resultRow(hit))
		}

		groups = append(groups, p.group(railTitle(hits[start].section), "", rows...))
		start = end
	}

	if more > 0 {
		groups = append(groups, p.group("", "", p.note(matchesLeft(more))))
	}

	return groups
}

// matchesLeft is the line under a capped list.
func matchesLeft(more int) string {
	if more == 1 {
		return "1 more match. Type a little more to reach it."
	}

	return strconv.Itoa(more) + " more matches. Type a little more to narrow them down."
}

// resultRow is a result as the card draws it: a size or a colour brings its own
// control, everything else is the way to where it lives.
func (p *SettingsPage) resultRow(hit settingsHit) fyne.CanvasObject {
	if hit.field != "" {
		if hit.color {
			return p.colorRow(hit.field, hit.field)
		}

		return p.sizeRow(hit.field, hit.field)
	}

	text := []fyne.CanvasObject{rowLabel(hit.label, resultTextWidth())}
	if hit.group != hit.label {
		text = append(text,
			VerticalSpacer(theme.Sizes.ChipSpacing),
			rowDetail(hit.group, resultTextWidth()))
	}

	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV
	body := NewFillRow(0,
		vcenter(VBoxNoSpacing(text...)),
		HorizontalSpacer(padH),
		vcenter(newChevron(theme.Colors.TimestampText)),
	)

	return NewMinHeightContainer(theme.Sizes.SettingsRowHeight,
		NewTappableContainer(NewInset(body, padV, padV, padH, padH), func() { p.jumpTo(hit) }))
}

// resultTextWidth is the room a result's two lines have: the card less the mark
// saying the row leads somewhere, and the gutter before it.
func resultTextWidth() float32 {
	return cardWidth() - theme.Sizes.SettingsRowPaddingH - theme.Sizes.SettingsIconSize
}

// jumpTo opens the section a result names and brings its group to the top.
func (p *SettingsPage) jumpTo(hit settingsHit) {
	p.showSection(hit.section)

	for nav, group := range p.navGroups {
		if p.groups[group].caption == hit.group {
			p.scrollToNav(nav)
			break
		}
	}
}

// newChevron is the mark saying a row leads somewhere, plotted on the same
// 20-unit grid the client's other drawn marks share.
func newChevron(tint color.Color) fyne.CanvasObject {
	side := theme.Sizes.SettingsIconSize
	line := glyphLine(tint, side/20)

	return container.NewGridWrap(fyne.NewSize(side, side),
		container.NewWithoutLayout(line(8, 5, 13, 10), line(13, 10, 8, 15)))
}
