package ui

// The surface all three settings pages are drawn on: an icon rail of sections
// beside a scrolling pane of captioned cards.
//
// A *layer* rather than a canvas overlay: the modal layer holds one thing at a
// time, and a page has to ask "lift this ban?" over itself. Stacked into the
// window's content it covers the client while leaving that layer free above it —
// at the cost of swallowing pointer events itself, which is what the opaque
// backdrop and the tap sink are for.
//
// Every row is one shape: a label, an optional line of explanation, and one
// control — beside the text, or under it for a slider. Sections assemble rows;
// rows never reach back into a section. What the sections *are* is the page's:
// the shell is handed a list to draw a rail from and a set of cards to mount, and
// knows nothing about either.

import (
	"cmp"
	"image/color"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* The rail's entries */

// railEntry is one section as a rail draws it. The section is a plain int: the
// shell serves two pages that number their sections separately, and neither's
// constants mean anything to it.
type railEntry struct {
	section int
	title   string
	icon    fyne.Resource
}

// settingsGroup is one captioned card kept beside its caption, so the rail can
// list the section's groups and scroll to one. A group left with no rows —
// everything in it advanced — has a nil object and is dropped before it reaches
// either.
type settingsGroup struct {
	caption string
	object  fyne.CanvasObject

	// card is the surface behind the rows, kept so a jump can wash it — the flash
	// is what says which of several cards the rail just moved to.
	card *canvas.Rectangle
}

/* The shell */

// settingsShell is the rail, the pane and the vocabulary of rows both settings
// pages are built from. Embedded by value, so a page's own sections reach every
// row shape by promotion and never name it.
type settingsShell struct {
	// Layer is the full-window layer to stack over the main row. Hidden until the
	// page is opened.
	Layer *fyne.Container

	// onClose is what the header's button does. The shell offers no other way out —
	// a page reached by covering the client has to be dismissed rather than left.
	onClose func()

	rail  *fyne.Container
	pane  *fyne.Container
	title *canvas.Text

	// backSlot is the line over the title, empty for anything reached from the
	// rail. Every mount fills or empties it, so the way out cannot outlive what it
	// led out of.
	backSlot *fyne.Container

	// paneScroll is what a rail sub-entry moves and what reports the movement back,
	// so the entry marked open follows the reader rather than the last tap.
	paneScroll *ObservableScroll

	// popover is the page's own floating slot, for the colour picker. It sits in
	// Layer rather than on the window's modal layer, which holds one thing at a
	// time — a picker there would be closed by the first confirmation the page
	// raised.
	popover *fyne.Container

	// groups are the open section's cards beside their captions — the rail lists
	// them, and scrolling to one is an offset into pane.
	groups []settingsGroup
	// subButtons are the rail's sub-entries, kept so the selection can be repainted
	// without rebuilding the rail.
	subButtons []*settingsRailButton
	// navGroups indexes groups for each sub-entry. Not every group earns one — a
	// preview is a card with no caption — so the two do not run in step.
	navGroups []int
	// groupOffsets is where each group starts inside the pane, measured once per
	// section: the scroll path must not walk the pane per event.
	groupOffsets []float32
	activeNav    int

	// flash is the wash marking the group a jump landed on. One at a time — a
	// second jump hands the first one's card back before starting its own.
	flash *fyne.Animation

	// gridAllow and gridDeny are what the permission grid is working from, re-seeded
	// on every build of whatever it is aimed at. They live here rather than on a
	// page because three grids are drawn from them — a server role or its default,
	// a role inside one channel or that channel's, and a group's own — and the rows
	// that read them are shell methods for the same reason.
	//
	// A row computes from *these* rather than from the entry it was built with: a
	// second change made before the first has echoed back would otherwise send the
	// first one's absence. deny stays zero for the two scopes that are a plain set
	// rather than an overwrite.
	gridAllow domain.Permission
	gridDeny  domain.Permission

	// islands marks the list on screen as a card per entry rather than rows sharing
	// one, which is what stands between two of its rows. Set as the list is built,
	// since a late answer refills a body it did not build — see spaceRows.
	islands bool

	// indexing marks the throwaway pass that walks the sections for their names: a
	// row answers with what it is called rather than with anything to draw, and a
	// group hands its rows to record instead of building a card. Only the client's
	// own settings is searchable, so a shell with no recorder never sets it.
	indexing bool
	record   func(caption string, rows []fyne.CanvasObject) settingsGroup
}

// initShell builds the layer and the floating slot, hidden. The rail, the pane
// and the title arrive with the first build.
func (p *settingsShell) initShell(onClose func()) {
	p.onClose = onClose

	p.popover = container.NewStack()
	p.popover.Hide()

	// A layer, not a stack: the page is as wide as its widest card, and a narrower
	// window would be grown to fit it the moment the page opened.
	p.Layer = NewLayer()
	p.Layer.Hide()
}

// IsOpen reports whether the page is covering the client.
func (p *settingsShell) IsOpen() bool { return p.Layer.Visible() }

// resetShell drops what the shell built, so nothing it mounted keeps a widget or
// an image alive.
func (p *settingsShell) resetShell() {
	p.closePopover()
	p.stopFlash()
	p.Layer.Hide()
	p.Layer.Objects = nil
	p.groups = nil
	p.subButtons = nil
	p.navGroups = nil
	p.paneScroll = nil
	p.backSlot = nil
}

/* The floating slot */

// showPalette opens the colour picker beside anchor, closing itself once a
// preset is chosen.
func (p *settingsShell) showPalette(anchor fyne.CanvasObject, onPick func(hex string)) {
	p.showPopover(anchor, newPaletteCard(func(hex string) {
		p.closePopover()
		onPick(hex)
	}))
}

// showPopover floats card beside anchor, over a sink that closes it again on the
// next click anywhere else.
func (p *settingsShell) showPopover(anchor, card fyne.CanvasObject) {
	p.popover.Objects = []fyne.CanvasObject{
		newDismissSink(p.closePopover),
		container.New(&popoverLayout{anchor: anchor, host: p.popover}, card),
	}
	p.popover.Show()
	p.popover.Refresh()
}

// closePopover takes the floating slot down. Safe when nothing is in it.
func (p *settingsShell) closePopover() {
	if p.popover == nil || !p.popover.Visible() {
		return
	}

	p.popover.Objects = nil
	p.popover.Hide()
	p.popover.Refresh()
}

/* The surface */

// newSurface creates the rail, the pane and the title, empty. Split from
// buildSurface because mounting a section writes to all three, and the page does
// that in between the two calls.
func (p *settingsShell) newSurface() {
	p.rail = container.NewVBox()
	p.pane = VBoxNoSpacing()
	p.title = newBoldText("", theme.Colors.TextPrimary, theme.Sizes.SettingsHeaderSize)
}

// buildSurface assembles the whole thing around a pane the page has already
// filled: a backdrop that stops the client behind receiving anything, the rail,
// and the pane. caption is the word over the rail; head and foot are what the
// page pins above and below the list of sections, either of which may be nil.
func (p *settingsShell) buildSurface(caption string, head, foot fyne.CanvasObject) fyne.CanvasObject {
	backdrop := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	padding := theme.Sizes.SettingsPagePadding

	p.paneScroll = NewPlainVScroll(p.centred(NewInset(p.pane, 0, padding, padding, padding)))
	p.paneScroll.OnScroll = func(offset fyne.Position) { p.markGroupAt(offset.Y) }

	pane := NewFillColumn(1, p.buildHeader(), p.paneScroll)

	row := NewFillRow(1,
		NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, p.buildRailColumn(caption, head, foot)),
		pane,
	)

	return newTapSink(container.NewStack(backdrop, row))
}

// centred caps content at the page width and centres it horizontally. A row is a
// label at one end and a control at the other, and across a maximised window the
// two lose each other. The header goes through it too, so the title sits over the
// cards' left edge rather than the window's.
//
// Horizontally only: container.NewCenter would centre vertically as well, leaving
// a short section floating in the middle rather than under its own title.
func (p *settingsShell) centred(content fyne.CanvasObject) fyne.CanvasObject {
	body := NewFixedWidthContainer(theme.Sizes.SettingsPageWidth, content)

	return container.NewHBox(layout.NewSpacer(), body, layout.NewSpacer())
}

// buildHeader is the pane's heading and close button. The heading is centred with
// the cards; the button is anchored to the pane's top right through overlayLayout,
// which reports no minimum and so cannot pull the title off centre.
func (p *settingsShell) buildHeader() fyne.CanvasObject {
	padding := theme.Sizes.SettingsPagePadding

	// Hidden rather than absent: mount fills and empties it, and a container the
	// header never laid out is one nothing can fill.
	p.backSlot = VBoxNoSpacing()
	p.backSlot.Hide()

	// The title is capped and centred with the cards; the back row is not, so it
	// stands at the pane's own left edge and the two sit diagonally rather than
	// stacked. The padding above and below is the column's, not the title's — a
	// hidden back row would otherwise take the top of it away with itself.
	heading := NewInset(VBoxNoSpacing(
		p.backSlot,
		p.centred(NewInset(p.title, 0, 0, padding, padding)),
	), padding, theme.Sizes.SettingsGroupGap, 0, 0)

	dismiss := container.New(&overlayLayout{yOffset: padding, rightOffset: padding},
		NewCloseButton(p.onClose))

	return container.NewStack(heading, dismiss)
}

/* The way back */

// backLink is the way out of something mounted inside a section: what is being
// left, and what leaving it does. A zero one is a section — reached from the rail,
// which is already saying where the reader is — so mount takes none and only a
// drilldown passes one.
type backLink struct {
	label string
	onTap func()
}

// showBack fills or empties the line over the title. Only mount calls it: what the
// pane holds and the way out of it are one change, and a page setting either
// alone would leave a way back to somewhere it is no longer standing.
func (p *settingsShell) showBack(back backLink) {
	if p.backSlot == nil {
		return // reset, or a page whose surface has not been built yet
	}

	if back.label == "" || back.onTap == nil {
		p.backSlot.Objects = nil
		p.backSlot.Hide()
		p.backSlot.Refresh()

		return
	}

	// Boxed rather than laid in the column: the column stretches a child to its
	// width, and a button as wide as the pane would answer taps a long way from
	// anything it draws.
	padding := theme.Sizes.SettingsPagePadding

	p.backSlot.Objects = []fyne.CanvasObject{
		NewInset(HBoxNoSpacing(newSettingsBackLink(back.label, back.onTap)),
			0, theme.Sizes.SettingsBackGap, padding, padding),
	}
	p.backSlot.Show()
	p.backSlot.Refresh()
}

// settingsBackLink is that button, and the whole of the shell's breadcrumb: there
// is one drilldown and its path is two segments, the second of which is the title
// below. It is a plain ui.Button in every respect that can be seen — the same
// fill, hairline, radius, hover lift and label — and a widget of its own only
// because Button has nowhere to put the mark that says which way it goes.
type settingsBackLink struct {
	tapBase

	content    fyne.CanvasObject
	background *canvas.Rectangle
}

var (
	_ fyne.Tappable     = (*settingsBackLink)(nil)
	_ desktop.Hoverable = (*settingsBackLink)(nil)
)

func newSettingsBackLink(label string, onTap func()) *settingsBackLink {
	side := theme.Sizes.SettingsIconSize
	line := glyphLine(theme.Colors.SettingsBackText, side/20)

	mark := container.NewGridWrap(fyne.NewSize(side, side),
		container.NewWithoutLayout(line(12, 5, 7, 10), line(7, 10, 12, 15)))

	background := canvas.NewRectangle(theme.Colors.ButtonBg)
	background.CornerRadius = theme.Sizes.ButtonRadius
	Outline(background)

	body := HBoxNoSpacing(
		mark,
		HorizontalSpacer(theme.Sizes.SettingsBackMarkGap),
		vcenter(newBoldText(label, theme.Colors.ButtonText, theme.Sizes.ButtonTextSize)),
	)

	l := &settingsBackLink{background: background}
	l.content = NewMinHeightContainer(theme.Sizes.ButtonMinHeight,
		container.NewStack(background,
			NewInset(body, theme.Sizes.ButtonPaddingV, theme.Sizes.ButtonPaddingV,
				theme.Sizes.ButtonPaddingH, theme.Sizes.ButtonPaddingH)))
	l.onTap = onTap
	l.ExtendBaseWidget(l)

	return l
}

func (l *settingsBackLink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.content)
}

func (l *settingsBackLink) MouseIn(*desktop.MouseEvent) { l.fill(theme.Colors.ButtonHoverBg) }

func (l *settingsBackLink) MouseOut() { l.fill(theme.Colors.ButtonBg) }

func (l *settingsBackLink) fill(colour color.Color) {
	l.background.FillColor = colour
	l.background.Refresh()
}

// buildRailColumn is the rail, whatever the page pins at either end of it, and
// the seam separating the column from the pane — every column's hairline, inside
// its own fixed width.
func (p *settingsShell) buildRailColumn(caption string, head, foot fyne.CanvasObject) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	padding := theme.Sizes.SettingsPagePadding
	padH := theme.Sizes.SettingsRowPaddingH

	label := newBoldText(strings.ToUpper(caption), theme.Colors.CategoryText, theme.Sizes.SettingsCaptionSize)

	// Whatever the page pins here is what the rail below is showing — a search box,
	// or the server this page is about — so it sits above the scroll rather than in
	// it: one that scrolls away is one nobody finds.
	headRow := []fyne.CanvasObject{label}
	if head != nil {
		headRow = append(headRow, VerticalSpacer(theme.Sizes.SettingsPreviewGap), head)
	}

	top := NewInset(VBoxNoSpacing(headRow...), padding, theme.Sizes.SettingsPreviewGap, padH, padH)
	content := NewInset(p.rail, 0, padding, padH, padH)

	// A nil is dropped rather than laid out: a layout walking its children calls
	// Visible() on each, which a nil interface answers with a panic.
	column := []fyne.CanvasObject{top, container.NewVScroll(content)}
	if foot != nil {
		column = append(column, foot)
	}

	return NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, background,
		NewFillRow(0, NewFillColumn(1, column...), NewColumnDivider()))
}

// buildRail fills the rail with one button per entry, and — under the open one —
// one per group it holds. open is the section marked; a page showing something
// that is not a section at all (a page of search results) passes one no entry
// carries, and nothing is marked.
func (p *settingsShell) buildRail(entries []railEntry, open int, onSelect func(section int)) {
	p.subButtons = nil
	p.navGroups = nil

	var buttons []fyne.CanvasObject
	for _, entry := range entries {
		selected := entry.section == open

		buttons = append(buttons, newSettingsRailButton(entry, selected, func() {
			onSelect(entry.section)
		}))

		// A section with one place to go has no navigation in it: the entry would
		// repeat the section's own name and scroll to where the pane already is.
		if !selected || p.navigable() < 2 {
			continue
		}

		for i, group := range p.groups {
			if group.caption == "" {
				continue // a preview is a card, but not somewhere to go
			}

			nav := len(p.subButtons)
			button := newSettingsSubButton(group.caption, nav == p.activeNav, func() {
				p.scrollToNav(nav)
			})

			p.subButtons = append(p.subButtons, button)
			p.navGroups = append(p.navGroups, i)
			buttons = append(buttons, button)
		}
	}

	p.rail.Objects = buttons
	p.rail.Refresh() // a container re-lays out only when it is told its children moved
}

// navigable counts the groups the rail could list — the captioned ones, a
// preview being a card with nowhere to go.
func (p *settingsShell) navigable() int {
	var count int
	for _, group := range p.groups {
		if group.caption != "" {
			count++
		}
	}

	return count
}

// mount puts one set of groups in the pane and re-heads it, with no way back —
// what the rail reaches. The rail itself is left to the caller: what it lists is
// the page's, and every caller rebuilds it from what this sets.
func (p *settingsShell) mount(groups []settingsGroup, title string) {
	p.mountUnder(groups, title, backLink{})
}

// mountUnder is mount for something standing inside a section rather than being
// one — a role's editor, a page of search results — which needs a line saying
// what it is inside of and answering a tap to leave.
func (p *settingsShell) mountUnder(groups []settingsGroup, title string, back backLink) {
	p.closePopover() // its anchor is about to stop existing
	p.stopFlash()    // as is the card it was washing

	p.groups = slices.DeleteFunc(groups, func(g settingsGroup) bool { return g.object == nil })
	p.navGroups = nil
	p.activeNav = 0

	cards := make([]fyne.CanvasObject, len(p.groups))
	for i, group := range p.groups {
		cards[i] = group.object
	}
	p.pane.Objects = cards
	p.pane.Refresh() // a container re-lays out only when it is told its children moved
	p.measureGroups()

	if p.paneScroll != nil {
		// The content is a different height now, and the scroll clamps an offset
		// against what it last measured — including the one a jump is about to write.
		p.paneScroll.SyncContent()
		p.paneScroll.ScrollToOffset(fyne.Position{})
	}

	p.showBack(back)
	p.title.Text = title
	p.title.Refresh()
}

// refill replaces one group's rows in place, for a list that has just been
// fetched or one an entry has been taken out of. Remounting the section would put
// the reader back at the top of a page they had scrolled, and rebuild every other
// card to redraw one.
func (p *settingsShell) refill(body *fyne.Container, rows []fyne.CanvasObject) {
	body.Objects = p.spaceRows(rows)
	body.Refresh()
	p.measureGroups() // the card is a different height, and the rail scrolls by these

	if p.paneScroll != nil {
		p.paneScroll.SyncContent()
	}
}

/* Moving between groups */

// measureGroups records where each group starts inside the pane. Summed from the
// groups above rather than read from Position(), which is not set until the first
// layout — and taken once per section rather than per scroll, a MinSize on a
// group card being a walk of every row in it.
func (p *settingsShell) measureGroups() {
	p.groupOffsets = make([]float32, len(p.groups))

	var offset float32
	for i, group := range p.groups {
		p.groupOffsets[i] = offset
		offset += group.object.MinSize().Height
	}
}

// navOffset is where the group behind one sub-entry starts.
func (p *settingsShell) navOffset(nav int) float32 {
	group := p.navGroups[nav]
	if group >= len(p.groupOffsets) {
		return 0
	}

	return p.groupOffsets[group]
}

// scrollToNav brings one group's caption to the top of the pane. The selection is
// set here rather than left to OnScroll: the scroll clamps at the end of the
// content, so the last group never reaches the top and would never light.
func (p *settingsShell) scrollToNav(nav int) {
	if p.paneScroll == nil || nav >= len(p.navGroups) {
		return
	}

	// The caption sits below the gap every group leads with.
	p.paneScroll.ScrollToOffset(fyne.NewPos(0, p.navOffset(nav)+theme.Sizes.SettingsGroupGap))
	p.markNav(nav)
	p.flashGroup(p.navGroups[nav])
}

// flashGroup washes one group's card and lets go again. A section is several
// cards of similar rows and the scroll lands mid-page, so the entry lighting up
// in the rail says where the reader is but not what they were brought to.
func (p *settingsShell) flashGroup(group int) {
	p.stopFlash()

	if group >= len(p.groups) || p.groups[group].card == nil {
		return
	}

	card := p.groups[group].card
	rest := theme.Colors.SessionCardBg

	p.flash = fyne.NewAnimation(flashDuration, func(done float32) {
		if done >= 1 {
			card.FillColor = rest
		} else {
			card.FillColor = mixColor(rest, theme.Colors.SettingsJumpBackground, 1-done)
		}
		canvas.Refresh(card)
	})

	p.flash.Curve = fyne.AnimationEaseIn
	p.flash.Start()
}

// stopFlash ends the wash where it stands. The card is handed back its rest
// colour here rather than left mid-fade: a rebuild drops the rectangle, but a
// jump to another group in the same section does not.
func (p *settingsShell) stopFlash() {
	if p.flash == nil {
		return
	}

	p.flash.Stop()
	p.flash = nil

	for _, group := range p.groups {
		if group.card != nil {
			group.card.FillColor = theme.Colors.SessionCardBg
			canvas.Refresh(group.card)
		}
	}
}

// markGroupAt follows the reader: the entry marked is the last one whose group
// has started at or above the top of the view.
func (p *settingsShell) markGroupAt(offset float32) {
	nav := 0
	for i := range p.navGroups {
		if p.navOffset(i) <= offset+theme.Sizes.SettingsGroupGap {
			nav = i
		}
	}

	p.markNav(nav)
}

// markNav repaints the two sub-entries that changed, and nothing else.
func (p *settingsShell) markNav(nav int) {
	if nav == p.activeNav || nav >= len(p.subButtons) {
		return
	}

	if p.activeNav < len(p.subButtons) {
		p.subButtons[p.activeNav].setSelected(false)
	}
	p.subButtons[nav].setSelected(true)
	p.activeNav = nav
}

/* Rows and groups */

// group is a captioned card of rows: a hairline between each pair, the caption
// outside the card above it. A card with nothing left in it is no card at all.
func (p *settingsShell) group(caption, detail string, rows ...fyne.CanvasObject) settingsGroup {
	kept := separateRows(rows)
	if len(kept) == 0 {
		return settingsGroup{}
	}

	if p.indexing {
		return p.record(caption, kept)
	}

	return p.groupOf(caption, detail, VBoxNoSpacing(kept...))
}

// groupOf is the same card around a body the caller keeps a handle on, for a
// list refilled in place rather than rebuilt — see refill.
func (p *settingsShell) groupOf(caption, detail string, body *fyne.Container) settingsGroup {
	if p.indexing {
		return p.record(caption, body.Objects)
	}

	card := newSettingsCard()

	return settingsGroup{
		caption: caption,
		object:  VBoxNoSpacing(append(p.groupHeader(caption, detail), container.NewStack(card, body))...),
		card:    card,
	}
}

// bareGroupOf is the same caption and explanation over a body that carries its
// own surfaces — a list drawn as a card per entry, where one card behind them all
// would be a second surface saying they belong together, which is the opposite of
// what a card each says. It has no rectangle to wash, so a jump to it scrolls and
// does not flash.
func (p *settingsShell) bareGroupOf(caption, detail string, body *fyne.Container) settingsGroup {
	if p.indexing {
		return p.record(caption, body.Objects)
	}

	return settingsGroup{
		caption: caption,
		object:  VBoxNoSpacing(append(p.groupHeader(caption, detail), body)...),
	}
}

// groupHeader is the caption and the explanation above a group, and the gap that
// separates it from the group before.
func (p *settingsShell) groupHeader(caption, detail string) []fyne.CanvasObject {
	header := []fyne.CanvasObject{VerticalSpacer(theme.Sizes.SettingsGroupGap)}
	if caption != "" {
		label := newBoldText(strings.ToUpper(caption), theme.Colors.CategoryText, theme.Sizes.SettingsCaptionSize)
		header = append(header,
			NewInset(label, 0, theme.Sizes.SettingsPreviewGap, theme.Sizes.SettingsRowPaddingH, 0))
	}
	if detail != "" {
		note := rowDetail(detail, cardWidth()-theme.Sizes.SettingsRowPaddingH)
		header = append(header,
			NewInset(note, 0, theme.Sizes.SettingsPreviewGap, theme.Sizes.SettingsRowPaddingH, 0))
	}

	return header
}

// spaceRows puts between two rows of the list on screen whatever stands between
// them: the hairline of a shared card, or the gap between two cards of their own.
// Which it is was decided when the list was built and is read again here, a late
// answer refilling a body it did not build.
func (p *settingsShell) spaceRows(rows []fyne.CanvasObject) []fyne.CanvasObject {
	if !p.islands {
		return separateRows(rows)
	}

	spaced := make([]fyne.CanvasObject, 0, max(len(rows)*2-1, 0))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if len(spaced) > 0 {
			spaced = append(spaced, VerticalSpacer(theme.Sizes.SettingsIslandGap))
		}
		spaced = append(spaced, row)
	}

	return spaced
}

// separateRows drops the rows that were not built — one refused for an unknown
// field, one advanced mode hides — and puts the hairline between each remaining
// pair. Dropping them here covers every path a row reaches a card by; a nil left
// in a container panics the layout that walks it.
func separateRows(rows []fyne.CanvasObject) []fyne.CanvasObject {
	separated := make([]fyne.CanvasObject, 0, max(len(rows)*2-1, 0))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if len(separated) > 0 {
			separated = append(separated, newRowSeparator())
		}
		separated = append(separated, row)
	}

	return separated
}

// newRowSeparator is the hairline between two rows of a group, inset from both
// ends so the card's rounded corners are never cut across.
func newRowSeparator() fyne.CanvasObject {
	inset := theme.Sizes.SettingsRowPaddingH

	return NewInset(NewRowDivider(), 0, 0, inset, inset)
}

// cardWidth is the width a group's card gives its rows. The page is centred at a
// fixed width, so this is known before anything is laid out — which is what lets
// a row's prose be wrapped where it is built rather than by a layout that would
// need the width before it could report a height.
func cardWidth() float32 {
	return theme.Sizes.SettingsPageWidth - 2*theme.Sizes.SettingsPagePadding - 2*theme.Sizes.SettingsRowPaddingH
}

// rowTextWidth is the room a row's label and explanation have: the card less the
// control at the trailing edge and the gutter between them, so wrapped prose
// stops short of the control rather than running under it.
func rowTextWidth(control fyne.CanvasObject) float32 {
	return cardWidth() - theme.Sizes.SettingsRowPaddingH - control.MinSize().Width
}

// row is the shape every setting takes: a label, an optional line of
// explanation, and one control at the trailing edge.
func (p *settingsShell) row(label, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	row, _ := p.markedRow(label, detail, control)

	return row
}

// rowWith is row for an explanation that has to be a widget rather than a line
// of prose — a path shortened to whatever width the row has to give it.
func (p *settingsShell) rowWith(label string, detail, control fyne.CanvasObject) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow(label)
	}

	row, _ := p.rowOf(rowLabel(label, rowTextWidth(control)), detail, control)

	return row
}

// markedRow is row plus the bar down its left edge, handed back for the caller to
// fill. Only a toggle has anything to say with it, and the toggle is built a
// level above, so the rectangle travels rather than the state.
func (p *settingsShell) markedRow(label, detail string, control fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
	if p.indexing {
		return newIndexRow(label), canvas.NewRectangle(color.Transparent)
	}

	width := rowTextWidth(control)

	var note fyne.CanvasObject
	if detail != "" {
		note = rowDetail(detail, width)
	}

	return p.rowOf(rowLabel(label, width), note, control)
}

// rowOf lays a built text column against a control.
func (p *settingsShell) rowOf(name fyne.CanvasObject, detail, control fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
	text := []fyne.CanvasObject{name}
	if detail != nil {
		text = append(text, VerticalSpacer(theme.Sizes.ChipSpacing), detail)
	}

	body := NewFillRow(0,
		vcenter(VBoxNoSpacing(text...)),
		HorizontalSpacer(theme.Sizes.SettingsRowPaddingH),
		vcenter(control),
	)

	return p.frame(body)
}

// entryRow is the shape a row of a *list* takes rather than a setting: something
// drawn at the leading edge — an avatar, a channel's glyph — the name and a line
// about it, and however many buttons the entry offers. The lead is what separates
// a list of people or channels from a column of switches at a glance.
func (p *settingsShell) entryRow(lead fyne.CanvasObject, label, detail string, controls ...fyne.CanvasObject) fyne.CanvasObject {
	if detail == "" {
		return p.entryRowWith(lead, label, nil, controls...)
	}

	return p.entryRowWith(lead, label,
		func(width float32) fyne.CanvasObject { return rowDetail(detail, width) }, controls...)
}

// entryRowWith is entryRow with the second line built rather than written: an
// invite's names the channel and carries a chip for whoever made it. The line is
// asked for at the width the row has left, which is what the plain one wraps at
// and the only thing a caller cannot work out for itself.
func (p *settingsShell) entryRowWith(lead fyne.CanvasObject, label string,
	detail func(width float32) fyne.CanvasObject, controls ...fyne.CanvasObject) fyne.CanvasObject {

	return p.entryRowIn(cardWidth(), lead, label, detail, controls...)
}

// entryRowIn is entryRowWith in a card of the given width rather than the page's
// own — what a row *paired* with another is built at, each taking half. Every
// width inside the row is derived from it, so a half row is the same shape and
// not a narrower one with the same numbers in it.
func (p *settingsShell) entryRowIn(card float32, lead fyne.CanvasObject, label string,
	detail func(width float32) fyne.CanvasObject, controls ...fyne.CanvasObject) fyne.CanvasObject {

	if p.indexing {
		return newIndexRow(label)
	}

	gap := theme.Sizes.SettingsRowPaddingH
	buttons := HBoxNoSpacing(controls...)

	width := card - gap - buttons.MinSize().Width - gap
	if lead != nil {
		width -= lead.MinSize().Width + gap
	}

	// The name is somebody's or something's rather than the client's own, so it is
	// shortened to the row rather than wrapped: a list is read down its leading
	// edge, and a name that takes two lines moves every row after it.
	text := []fyne.CanvasObject{
		NewEllipsisText(newText(label, theme.Colors.TextPrimary, theme.Sizes.SettingsLabelSize)),
	}
	if detail != nil {
		text = append(text, VerticalSpacer(theme.Sizes.SettingsEntryLineGap), detail(width))
	}

	// The text column is what stretches, so it is the fill slot: second when
	// something leads the row, first when nothing does.
	fill, body := 0, []fyne.CanvasObject{}
	if lead != nil {
		fill = 2
		body = append(body, vcenter(lead), HorizontalSpacer(gap))
	}
	body = append(body,
		vcenter(VBoxNoSpacing(text...)),
		HorizontalSpacer(gap),
		vcenter(buttons),
	)

	row, _ := p.frame(NewFillRow(fill, body...))

	return row
}

// rowLabel names a setting; rowDetail is the sentence under it. Both wrap at the
// width they are given.
func rowLabel(label string, width float32) fyne.CanvasObject {
	return NewWrappedText(label, width, theme.Sizes.SettingsLabelSize, theme.Colors.TextPrimary)
}

func rowDetail(detail string, width float32) fyne.CanvasObject {
	return NewWrappedText(detail, width, theme.Sizes.SettingsDetailSize, theme.Colors.TimestampText)
}

// stackedRow puts the control on a line of its own under the explanation, the
// only way a slider gets width enough to be aimed with. A switch stays on one
// line — it says what it says at any size, and two lines each would double the
// length of every section.
func (p *settingsShell) stackedRow(label, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow(label)
	}

	text := []fyne.CanvasObject{rowLabel(label, cardWidth())}
	if detail != "" {
		text = append(text, VerticalSpacer(theme.Sizes.SettingsEntryLineGap), rowDetail(detail, cardWidth()))
	}
	text = append(text, VerticalSpacer(theme.Sizes.SettingsControlGap), control)

	row, _ := p.frame(VBoxNoSpacing(text...))

	return row
}

// frame is the padding, the row-height floor and the marker every row shares.
// The marker is stacked over the body rather than laid beside it, so it sits at
// the card's own edge inside the row's padding.
func (p *settingsShell) frame(body fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
	marker, markerRow := newSettingsMarker()

	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV

	return NewMinHeightContainer(theme.Sizes.SettingsRowHeight,
		container.NewStack(markerRow, NewInset(body, padV, padV, padH, padH))), marker
}

// block is a row whose content spans its full width rather than sitting in a
// control slot — a usage meter, whose whole point is the proportion it draws.
func (p *settingsShell) block(content fyne.CanvasObject) fyne.CanvasObject {
	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV

	return NewMinHeightContainer(theme.Sizes.SettingsRowHeight,
		NewInset(content, padV, padV, padH, padH))
}

// vcenter centres obj vertically while letting it fill the width it is given.
// container.NewCenter leaves it at its minimum in both directions, which pulls a
// label into the middle of the row and collapses a slider to its thumb.
func vcenter(obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), obj, layout.NewSpacer())
}

// entryColumn is the slot the first field of an entry row's second line sits in,
// so whatever follows it starts in the same place down the whole list. The slot
// is reserved whether or not the field is there: a row the store could not name
// that half of must not pull the rest of its line left past the rows around it.
//
// Pass a field that shortens rather than wraps — the slot is a ceiling as well as
// a floor, and a wrapped one would take a second line and move the row.
func entryColumn(field fyne.CanvasObject, width float32) fyne.CanvasObject {
	column := entryColumnWidth(width)
	if field == nil {
		return HorizontalSpacer(column)
	}

	return NewFixedWidthContainer(column, field)
}

// entryColumnWidth is that slot's width, for a caller laying out what follows it
// as well. A share of the row rather than the theme's width outright, so what
// comes after the slot keeps its room in a row half the card wide: the theme's
// width is what a full row can afford and a ceiling everywhere else.
func entryColumnWidth(width float32) float32 {
	return min(theme.Sizes.SettingsEntryColumnWidth, width*entryColumnShare)
}

// entryColumnShare is how much of a row's text width the slot may take. Just
// under half: the field in it is the *lesser* of the two things on the line —
// where an invite lands, against who made it — and a slot past half would read as
// the line's subject.
const entryColumnShare = 0.45

// pairedRows lays cells out two to a row, which is what a list of short entries
// is worth: an invite is a code and two buttons, and one per row spends the whole
// card on it and scrolls twice as far. Each cell is built at halfCardWidth, so the
// two share their geometry and every column in the card lines up across both.
//
// An odd cell keeps its half rather than stretching: a last row twice as wide as
// the ones above it reads as a different kind of entry.
func pairedRows(cells []fyne.CanvasObject) []fyne.CanvasObject {
	half := halfCardWidth() + 2*theme.Sizes.SettingsRowPaddingH

	rows := make([]fyne.CanvasObject, 0, (len(cells)+1)/2)
	for i := 0; i < len(cells); i += 2 {
		pair := []fyne.CanvasObject{NewFixedWidthContainer(half, cells[i])}
		if i+1 < len(cells) {
			pair = append(pair,
				HorizontalSpacer(theme.Sizes.SettingsPairGutter),
				NewFixedWidthContainer(half, cells[i+1]))
		}

		rows = append(rows, HBoxNoSpacing(pair...))
	}

	return rows
}

// newIsland is one entry standing on a surface of its own, for a list where each
// is its own thing rather than a line of a table — an invite, which is a code
// somebody hands out and revokes on its own.
func newIsland(body fyne.CanvasObject) fyne.CanvasObject {
	return container.NewStack(newIslandCard(), body)
}

// newIslandCard is that surface on its own, for a caller that has to keep the
// rectangle — the friends page, whose islands are also targets and repaint under
// the pointer. Built here so an island in the settings and an island where the
// messages go cannot drift a shade apart.
func newIslandCard() *canvas.Rectangle {
	card := newSettingsCard()
	card.StrokeColor = theme.Colors.SettingsIslandOutline
	Elevate(card)

	return card
}

// halfCardWidth is the card one cell of a paired row is built in: half of what is
// left once the gutter is taken out, less the padding the cell's own frame adds.
func halfCardWidth() float32 {
	return (cardWidth()-theme.Sizes.SettingsPairGutter)/2 - theme.Sizes.SettingsRowPaddingH
}

// note is a row of prose on its own — the line that says a change waits for a
// restart, that a list is empty, or that a feature has not been built.
func (p *settingsShell) note(text string) fyne.CanvasObject {
	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsPreviewGap
	gap := theme.Sizes.SettingsNoteMarkGap

	mark := newNoteMark()
	label := rowDetail(text, cardWidth()-2*padH-theme.Sizes.SettingsNoteMarkSize-gap)

	return NewInset(HBoxNoSpacing(mark, HorizontalSpacer(gap), label), padV, padV, padH, padH)
}

// newNoteMark is the badge a note carries: an "i" plotted on the same 20-unit
// grid the client's other drawn marks share, inside a box of its own. Drawn
// rather than set — the bundled font subset has no ⓘ, so the glyph fell to
// whatever the platform could substitute and landed as tofu on macOS, and a
// letter centred by its own metrics sits high in a box this small, the line box
// reserving descent the "i" never uses.
func newNoteMark() fyne.CanvasObject {
	side := theme.Sizes.SettingsNoteMarkSize
	scale := side / 20
	tint := theme.Colors.TimestampText

	box := canvas.NewRectangle(color.Transparent)
	box.StrokeColor = tint
	box.StrokeWidth = theme.Sizes.OutlineWidth
	box.CornerRadius = theme.Sizes.SettingsNoteMarkRadius

	part := func(y, height float32) *canvas.Rectangle {
		r := canvas.NewRectangle(tint)
		r.Move(fyne.NewPos(9*scale, y*scale))
		r.Resize(fyne.NewSize(2*scale, height*scale))

		return r
	}

	glyph := container.NewWithoutLayout(part(4.5, 2), part(8.5, 7))

	return container.NewGridWrap(fyne.NewSize(side, side), container.NewStack(box, glyph))
}

/* Shared controls */

// boolRow is a switch and the bar that follows it, so a column of settings can be
// read for what is on without reading every switch in it.
func (p *settingsShell) boolRow(label, detail string, value bool, onChanged func(bool)) fyne.CanvasObject {
	var marker *canvas.Rectangle

	toggle := NewToggle(value, func(on bool) {
		markRow(marker, on)
		onChanged(on)
	})

	row, marker := p.markedRow(label, detail, toggle)
	markRow(marker, value)

	return row
}

func markRow(marker *canvas.Rectangle, on bool) {
	marker.FillColor = color.Transparent
	if on {
		marker.FillColor = theme.Colors.ServerSelectedBg
	}
	marker.Refresh()
}

// actionRow is a row whose control does something rather than holding a value.
func (p *settingsShell) actionRow(label, detail, action string, tone Tone, onTap func()) fyne.CanvasObject {
	return p.row(label, detail, newRowButton(action, tone, onTap))
}

// newRowButton is one button of a row offering more than one thing. A single
// button reaches this through actionRow; two have to be centred by the caller, an
// HBox handing its children its own height.
func newRowButton(label string, tone Tone, onTap func()) *Button {
	return NewWeightedButton(label, tone.weight(), onTap)
}

// colorControl is a swatch opening the palette and a field taking a hex, in one
// box. Neither alone is enough: nobody knows a hex by heart, and no set of
// presets is every colour someone might want.
// empty is what the swatch shows for a field with no colour in it, the caller
// having nothing to sample: a theme override always has one, a role need not.
func (p *settingsShell) colorControl(value string, empty color.Color, set func(hex string)) fyne.CanvasObject {
	entry := widget.NewEntry()
	entry.Text = value

	var swatch *swatchButton
	entry.OnChanged = func(text string) {
		parsed, ok := theme.ParseHex(text)
		if !ok {
			return // half a hex is what typing one looks like, not a colour
		}

		swatch.SetColor(parsed)
		set(theme.Hex(parsed))
	}

	// The picker writes through the field rather than around it, so the two can
	// never disagree about what colour this is.
	start := mustColor(value)
	if _, ok := theme.ParseHex(value); !ok && empty != nil {
		start = empty
	}

	swatch = newSwatchButton(start, nil)
	swatch.onTap = func() { p.showPalette(swatch, entry.SetText) }

	padding := theme.Sizes.SettingsRowPaddingH
	field := NewFillRow(2,
		container.NewCenter(swatch),
		HorizontalSpacer(theme.Sizes.ChipDotGap),
		WithCaret(entry),
	)

	return fixedControl(theme.Sizes.SettingsControlWidth,
		container.NewStack(newFieldBackground(), NewInset(field, 0, 0, padding, padding)))
}

// readOnlyRow states something the client knows and the user cannot change.
func (p *settingsShell) readOnlyRow(label, value string) fyne.CanvasObject {
	text := newText(value, theme.Colors.TimestampText, theme.Sizes.SettingsLabelSize)

	return p.row(label, "", text)
}

// identityStrip is the subject's picture and name, pinned above the rail on the
// two pages that are about one thing rather than about the client.
func (p *settingsShell) identityStrip(icon fyne.CanvasObject, name string) fyne.CanvasObject {
	title := newBoldText(name, theme.Colors.TextPrimary, theme.Sizes.SettingsRailTextSize)

	return NewFillRow(2,
		icon,
		HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
		vcenter(NewEllipsisText(title)),
	)
}

// descriptionRowOf is the free-text field a server and a group each carry, stated
// read-only where the account may not change it. Nothing writes back what it
// sent: the edit returns as an update the store answers for.
func (p *settingsShell) descriptionRowOf(value, placeholder, detail string, editable bool, onCommit func(string)) fyne.CanvasObject {
	if !editable {
		return p.readOnlyRow("Description", cmp.Or(value, "None"))
	}

	entry := newCommitArea(value, onCommit)
	entry.PlaceHolder = placeholder

	return p.stackedRow("Description", detail, wideField(entry))
}

// pictureRow offers to change or take off one picture, with the current one
// beside the buttons where there is something to preview. Remove is passed in
// rather than built here — a page holds on to it where what it can do is only
// known once a fetch lands — and is drawn disabled rather than left out, so the
// row does not change shape the moment a picture arrives.
func (p *settingsShell) pictureRow(label, detail string, preview fyne.CanvasObject,
	remove *Button, onChange func()) fyne.CanvasObject {

	controls := make([]fyne.CanvasObject, 0, 5)
	if preview != nil {
		controls = append(controls,
			container.NewCenter(preview),
			HorizontalSpacer(theme.Sizes.SettingsPreviewGap))
	}
	controls = append(controls,
		container.NewCenter(newRowButton("Change", ToneInfo, onChange)),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(remove))

	return p.row(label, detail, HBoxNoSpacing(controls...))
}

// textField is an entry drawn as a control: the box a dropdown and a colour row
// sit in, so a row that takes typing reads as one. It takes the object rather
// than a *widget.Entry so an extended one — commitEntry — keeps its overrides.
func textField(entry fyne.CanvasObject) fyne.CanvasObject {
	return fixedControl(theme.Sizes.SettingsControlWidth, fieldSurface(entry))
}

// wideField is textField for a field on a line of its own, sized by the row
// rather than by the control slot — prose typed into 190 pixels is read one word
// at a time.
func wideField(entry fyne.CanvasObject) fyne.CanvasObject {
	return fieldSurface(entry)
}

func fieldSurface(entry fyne.CanvasObject) fyne.CanvasObject {
	padding := theme.Sizes.SettingsRowPaddingH

	return container.NewStack(
		newFieldBackground(),
		NewInset(WithCaret(entry), 0, 0, padding, padding),
	)
}

// enableIf enables or disables a button from a condition.
func enableIf(button *Button, enabled bool) {
	if enabled {
		button.Enable()
		return
	}

	button.Disable()
}
