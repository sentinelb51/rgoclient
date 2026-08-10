package ui

// The settings page: an icon rail of sections beside a scrolling pane of rows.
//
// It is a *layer* rather than a canvas overlay or a second window. An overlay
// would be the obvious choice — it is what every other modal here is — but the
// modal layer holds exactly one thing at a time, and a settings page has to be
// able to ask "clear the cache?" over itself. Stacked into the window's content
// instead, it covers the client while leaving the overlay layer free above it.
// The cost is that it has to swallow pointer events itself, which is what the
// opaque backdrop and the tap sink are for.
//
// Every row is the same shape: a label, an optional line of explanation, and one
// control — beside the text, or under it when the control is a slider. Sections
// assemble rows; rows never reach back into a section.

import (
	"image/color"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/ui/theme"
)

/* Sections */

// SettingsSection identifies one entry in the rail.
type SettingsSection int

const (
	SectionAccount SettingsSection = iota
	SectionInterface
	SectionStyles
	SectionBehaviour
	SectionNotifications
	SectionCache
	SectionAdvanced
	SectionAbout
)

// railEntry is one section as the rail draws it.
type railEntry struct {
	section SettingsSection
	title   string
	icon    fyne.Resource
}

var railEntries = []railEntry{
	{SectionAccount, "Account", assets.AccountIcon},
	{SectionInterface, "Interface", assets.InterfaceIcon},
	{SectionStyles, "Styles", assets.StylesIcon},
	{SectionBehaviour, "Behaviour", assets.BehaviourIcon},
	{SectionNotifications, "Notifications", assets.NotifyIcon},
	{SectionCache, "Cache", assets.CacheIcon},
	{SectionAdvanced, "Advanced", assets.AdvancedIcon},
	{SectionAbout, "About", assets.AboutIcon},
}

/* The page */

// SettingsHooks is everything the page needs from the controller. It is a bundle
// of plain functions rather than an extension of Deps, the way the join dialog
// takes its callbacks: nothing else in the client needs any of them.
type SettingsHooks struct {
	Deps Deps

	// Update records a change. The page never writes config directly, so the
	// controller can decide when to persist and what else a change implies.
	Update func(mutate func(*config.Settings))

	// Restyle applies the palette and size tables and repaints the client behind
	// the page. Called after any change to Interface or Styles.
	Restyle func()

	Close   func()
	Confirm func(Confirm)

	Version string
	Build   string

	/* Account */

	Sessions      func() []SettingsSession
	ForgetSession func(userID string)
	LogOut        func()

	/* Cache */

	CacheDir func() string
	// ChooseCacheDir asks the user for a directory and reports the one they
	// picked, or nothing at all if they changed their mind. The page cannot open
	// the dialog itself: it needs the window.
	ChooseCacheDir func(onPicked func(path string))
	CacheStats     func(onDone func(cache.ImageStats))
	ClearCache     func()

	/* About */

	ConfigPath func() string
	OpenPath   func(path string)
}

// SettingsSession is one saved login as the Account section lists it.
type SettingsSession struct {
	UserID    string
	Username  string
	AvatarURL string
}

// SettingsPage is the settings surface and its state. The controller keeps one
// for the life of the process and stacks Layer into the window content, so a
// rebuild of the client behind it leaves the open section and scroll position
// alone.
type SettingsPage struct {
	// Layer is the full-window layer to stack over the main row. Hidden until
	// Open.
	Layer *fyne.Container

	hooks SettingsHooks

	section SettingsSection
	rail    *fyne.Container
	pane    *fyne.Container
	title   *canvas.Text

	// paneScroll is what a rail sub-entry moves and what reports the movement
	// back, so the entry marked open follows the reader rather than the last tap.
	paneScroll *ObservableScroll

	// groups are the open section's cards beside their captions — the rail lists
	// them, and scrolling to one is an offset into pane.
	groups []settingsGroup
	// subButtons are the rail's sub-entries, in the same order as groups, kept so
	// the selection can be repainted without rebuilding the rail.
	subButtons []*settingsRailButton
	// navGroups indexes groups for each sub-entry. Not every group earns one — a
	// preview is a card with no caption — so the two do not run in step.
	navGroups []int
	// groupOffsets is where each group starts inside the pane, measured once when
	// the section is built — the scroll path must not walk the pane per event.
	groupOffsets []float32
	activeNav    int

	// advanced is config's AdvancedMode as the open section was built against.
	// Rows and whole sections are dropped when it is off, so it is read once per
	// build rather than per row.
	advanced bool

	// popover is the page's own floating slot, for the colour picker. It sits in
	// Layer above the page rather than on the window's modal layer, which holds
	// one thing at a time — a picker there would be closed by the first
	// confirmation the page raised.
	popover *fyne.Container

	// previews are the sample rows the Styles section draws, re-run on every
	// change so a dragged slider is answered immediately — the client behind the
	// page is covered, so it is the only thing that can answer.
	previews []settingsPreview
}

// settingsPreview is a sample of the real widgets, and how to build it again.
type settingsPreview struct {
	host  *fyne.Container
	build func() fyne.CanvasObject
}

// settingsGroup is one captioned card kept beside its caption, so the rail can
// list the open section's groups and scroll to the one that is picked. A group
// left with no rows — everything in it being advanced — has a nil object and is
// dropped before it reaches either.
type settingsGroup struct {
	caption string
	object  fyne.CanvasObject
}

// NewSettingsPage builds the page, hidden.
func NewSettingsPage(hooks SettingsHooks) *SettingsPage {
	p := &SettingsPage{hooks: hooks, section: SectionInterface}

	p.popover = container.NewStack()
	p.popover.Hide()

	// A layer, not a stack: the page is as wide as its widest card, and a window
	// narrower than that would be grown to fit it the moment settings opened.
	p.Layer = NewLayer()
	p.Layer.Hide()

	return p
}

// IsOpen reports whether the page is covering the client.
func (p *SettingsPage) IsOpen() bool { return p.Layer.Visible() }

// Open builds the page and shows it. Call on the UI thread.
func (p *SettingsPage) Open() {
	p.Rebuild()
	p.Layer.Show()
}

// Close hides the page and drops what it built, so nothing it mounted keeps a
// message widget or an image alive. Call on the UI thread.
func (p *SettingsPage) Close() {
	p.closePopover()
	p.Layer.Hide()
	p.Layer.Objects = nil
	p.previews = nil
	p.groups = nil
	p.subButtons = nil
	p.navGroups = nil
	p.paneScroll = nil
}

// Rebuild constructs the page from the theme tables as they now stand. Called on
// open, and by the controller after a style change that the page itself should
// pick up. Call on the UI thread.
func (p *SettingsPage) Rebuild() {
	p.closePopover()
	p.advanced = config.Current().Interface.AdvancedMode
	p.Layer.Objects = []fyne.CanvasObject{p.build(), p.popover}
	p.Layer.Refresh()
}

/* The floating slot */

// showPalette opens the colour picker beside anchor, closing itself once a
// preset is chosen.
func (p *SettingsPage) showPalette(anchor fyne.CanvasObject, onPick func(hex string)) {
	p.showPopover(anchor, newPaletteCard(func(hex string) {
		p.closePopover()
		onPick(hex)
	}))
}

// showPopover floats card beside anchor, over a sink that closes it again on the
// next click anywhere else.
func (p *SettingsPage) showPopover(anchor, card fyne.CanvasObject) {
	p.popover.Objects = []fyne.CanvasObject{
		newDismissSink(p.closePopover),
		container.New(&popoverLayout{anchor: anchor, host: p.popover}, card),
	}
	p.popover.Show()
	p.popover.Refresh()
}

// closePopover takes the floating slot down. Safe when nothing is in it.
func (p *SettingsPage) closePopover() {
	if p.popover == nil || !p.popover.Visible() {
		return
	}

	p.popover.Objects = nil
	p.popover.Hide()
	p.popover.Refresh()
}

// build assembles the whole surface: a backdrop that stops the client behind
// receiving anything, the rail, and the pane.
func (p *SettingsPage) build() fyne.CanvasObject {
	backdrop := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	p.rail = container.NewVBox()
	p.pane = VBoxNoSpacing()
	p.title = canvas.NewText("", theme.Colors.TextPrimary)
	p.title.TextSize = theme.Sizes.SettingsHeaderSize
	p.title.TextStyle = fyne.TextStyle{Bold: true}

	p.showSection(p.section)

	padding := theme.Sizes.SettingsPagePadding

	p.paneScroll = NewPlainVScroll(p.centred(NewInset(p.pane, 0, padding, padding, padding)))
	p.paneScroll.OnScroll = func(offset fyne.Position) { p.markGroupAt(offset.Y) }

	pane := NewFillColumn(1, p.buildHeader(), p.paneScroll)

	row := NewFillRow(1,
		NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, p.buildRailColumn()),
		pane,
	)

	return newTapSink(container.NewStack(backdrop, row))
}

// centred caps content at the page width and centres it horizontally. A row is a
// label at one end and a control at the other; across a maximised window the two
// lose each other entirely. The header goes through it too, so the title sits
// over the left edge of the cards rather than out at the window's.
//
// Horizontally only: container.NewCenter would centre it vertically too, which
// leaves a short section — Account is one — floating in the middle of the window
// rather than starting under its own title.
func (p *SettingsPage) centred(content fyne.CanvasObject) fyne.CanvasObject {
	body := NewFixedWidthContainer(theme.Sizes.SettingsPageWidth, content)

	return container.NewHBox(layout.NewSpacer(), body, layout.NewSpacer())
}

// buildHeader is the pane's title and its close button. The title is centred with
// the cards; the button is not, and is anchored to the pane's own top right —
// overlayLayout reports no minimum, so it cannot pull the title off centre.
func (p *SettingsPage) buildHeader() fyne.CanvasObject {
	padding := theme.Sizes.SettingsPagePadding

	title := p.centred(NewInset(p.title, padding, theme.Sizes.SettingsGroupGap, padding, padding))
	dismiss := container.New(&overlayLayout{yOffset: padding, rightOffset: padding},
		NewCloseButton(p.hooks.Close))

	return container.NewStack(title, dismiss)
}

// buildRailColumn is the rail, the advanced-mode switch pinned under it, and the
// seam that separates the column from the pane — the same hairline every other
// column carries, inside its own fixed width.
func (p *SettingsPage) buildRailColumn() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	padding := theme.Sizes.SettingsPagePadding

	caption := canvas.NewText("SETTINGS", theme.Colors.CategoryText)
	caption.TextSize = theme.Sizes.SettingsCaptionSize
	caption.TextStyle = fyne.TextStyle{Bold: true}

	content := NewInset(
		container.NewVBox(caption, p.rail),
		padding, padding, theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingH,
	)

	column := NewFillColumn(0, container.NewVScroll(content), p.buildRailFooter())

	return NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, background,
		NewFillRow(0, column, NewColumnDivider()))
}

// buildRailFooter is the advanced-mode switch. It sits at the foot of the rail
// rather than among the settings because it decides which of them there are —
// and the rail is where the reader looks when something they remember is missing.
func (p *SettingsPage) buildRailFooter() fyne.CanvasObject {
	label := canvas.NewText("Advanced mode", theme.Colors.CategoryText)
	label.TextSize = theme.Sizes.SettingsRailTextSize

	toggle := NewToggle(p.advanced, func(on bool) {
		p.change(func(s *config.Settings) { s.Interface.AdvancedMode = on })
		p.reload()
	})

	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV
	row := NewFillRow(0, vcenter(label), HorizontalSpacer(padH), vcenter(toggle))

	return VBoxNoSpacing(
		newRowSeparator(),
		NewMinHeightContainer(theme.Sizes.SettingsRowHeight, NewInset(row, padV, padV, padH, padH)),
	)
}

// buildRail fills the rail with one button per section, and — under the open one
// — one per group it holds.
func (p *SettingsPage) buildRail() {
	p.rail.Objects = nil
	p.subButtons = nil

	for _, entry := range visibleRailEntries(p.advanced) {
		p.rail.Add(newSettingsRailButton(entry, entry.section == p.section, func() {
			p.showSection(entry.section)
			p.pane.Refresh()
		}))

		if entry.section != p.section {
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
			p.rail.Add(button)
		}
	}
}

// visibleRailEntries drops the sections advanced mode is hiding. Advanced is the
// raw size and colour tables, which are the whole reason the mode exists.
func visibleRailEntries(advanced bool) []railEntry {
	if advanced {
		return railEntries
	}

	visible := make([]railEntry, 0, len(railEntries))
	for _, entry := range railEntries {
		if entry.section != SectionAdvanced {
			visible = append(visible, entry)
		}
	}

	return visible
}

// showSection swaps the pane to one section's groups.
func (p *SettingsPage) showSection(section SettingsSection) {
	p.closePopover() // its anchor is about to stop existing
	p.previews = nil

	// Advanced mode can be switched off while its own section is open, and
	// "Reset every setting" switches it off from another one entirely.
	if section == SectionAdvanced && !p.advanced {
		section = SectionInterface
	}
	p.section = section

	switch section {
	case SectionAccount:
		p.groups = p.accountSection()
	case SectionInterface:
		p.groups = p.interfaceSection()
	case SectionStyles:
		p.groups = p.stylesSection()
	case SectionBehaviour:
		p.groups = p.behaviourSection()
	case SectionNotifications:
		p.groups = p.notificationsSection()
	case SectionCache:
		p.groups = p.cacheSection()
	case SectionAdvanced:
		p.groups = p.advancedSection()
	case SectionAbout:
		p.groups = p.aboutSection()
	}

	p.groups = slices.DeleteFunc(p.groups, func(g settingsGroup) bool { return g.object == nil })
	p.navGroups = nil
	p.activeNav = 0

	p.pane.Objects = nil
	for _, group := range p.groups {
		p.pane.Add(group.object)
	}
	p.measureGroups()

	if p.paneScroll != nil {
		p.paneScroll.ScrollToOffset(fyne.Position{})
	}

	p.title.Text = railTitle(section)
	p.title.Refresh()
	p.buildRail()
}

/* Moving between groups */

// measureGroups records where each group starts inside the pane. The offsets are
// summed from the groups above rather than read from Position(), which is right
// only while the pane's own top inset stays zero and is not set at all until the
// first layout — and they are taken once per section rather than per scroll,
// since a MinSize on a group card is a walk of every row in it.
func (p *SettingsPage) measureGroups() {
	p.groupOffsets = make([]float32, len(p.groups))

	var offset float32
	for i, group := range p.groups {
		p.groupOffsets[i] = offset
		offset += group.object.MinSize().Height
	}
}

// navOffset is where the group behind one sub-entry starts.
func (p *SettingsPage) navOffset(nav int) float32 {
	group := p.navGroups[nav]
	if group >= len(p.groupOffsets) {
		return 0
	}

	return p.groupOffsets[group]
}

// scrollToNav brings one group's caption to the top of the pane. The selection is
// set here rather than left to OnScroll: the scroll clamps at the end of the
// content, so the last group never reaches the top and would never light up.
func (p *SettingsPage) scrollToNav(nav int) {
	if p.paneScroll == nil || nav >= len(p.navGroups) {
		return
	}

	// The caption sits below the gap every group leads with.
	p.paneScroll.ScrollToOffset(fyne.NewPos(0, p.navOffset(nav)+theme.Sizes.SettingsGroupGap))
	p.markNav(nav)
}

// markGroupAt follows the reader: the entry marked is the last one whose group
// has started at or above the top of the view.
func (p *SettingsPage) markGroupAt(offset float32) {
	nav := 0
	for i := range p.navGroups {
		if p.navOffset(i) <= offset+theme.Sizes.SettingsGroupGap {
			nav = i
		}
	}

	p.markNav(nav)
}

// markNav repaints the two sub-entries that changed, and nothing else.
func (p *SettingsPage) markNav(nav int) {
	if nav == p.activeNav || nav >= len(p.subButtons) {
		return
	}

	if p.activeNav < len(p.subButtons) {
		p.subButtons[p.activeNav].setSelected(false)
	}
	p.subButtons[nav].setSelected(true)
	p.activeNav = nav
}

func railTitle(section SettingsSection) string {
	for _, entry := range railEntries {
		if entry.section == section {
			return entry.title
		}
	}

	return ""
}

/* Applying changes */

// change records a setting that takes effect where it is read.
func (p *SettingsPage) change(mutate func(*config.Settings)) {
	p.hooks.Update(mutate)
}

// restyle records a setting the theme tables are built from, applies them, and
// re-runs the previews. The client behind the page is repainted by the
// controller; the previews are what the user can actually see change.
func (p *SettingsPage) restyle(mutate func(*config.Settings)) {
	p.hooks.Update(mutate)
	p.hooks.Restyle()

	for _, preview := range p.previews {
		preview.host.Objects = []fyne.CanvasObject{preview.build()}
		preview.host.Refresh()
	}
}

// reload rebuilds the current section, for a change that alters which rows the
// section shows rather than only what one of them says. Advanced mode is re-read
// here rather than per section, since a rail tap cannot change it while the two
// things that can — the rail's own switch and About's reset — both come through.
func (p *SettingsPage) reload() {
	p.advanced = config.Current().Interface.AdvancedMode
	p.showSection(p.section)
	p.pane.Refresh()
}

/* Rows and groups */

// adv marks a row as one advanced mode reveals: a timing, a cap, a budget —
// something that tunes the client rather than describing what it does. In basic
// mode it returns nil, which separateRows drops, and group drops the whole card
// if nothing else was in it.
func (p *SettingsPage) adv(row fyne.CanvasObject) fyne.CanvasObject {
	if !p.advanced {
		return nil
	}

	return row
}

// group is a captioned card of rows: the inset-list shape, one hairline between
// each pair of rows and the caption sitting outside the card above it. A card
// with nothing left in it is no card at all.
func (p *SettingsPage) group(caption, detail string, rows ...fyne.CanvasObject) settingsGroup {
	kept := separateRows(rows)
	if len(kept) == 0 {
		return settingsGroup{}
	}

	return p.groupOf(caption, detail, VBoxNoSpacing(kept...))
}

// groupOf is the same card around a body the caller keeps a handle on, for the
// one section whose rows are refilled in place rather than rebuilt — the
// Advanced filter, which must not take the field it is being typed into with it.
func (p *SettingsPage) groupOf(caption, detail string, body *fyne.Container) settingsGroup {
	card := canvas.NewRectangle(theme.Colors.SessionCardBg)
	card.CornerRadius = theme.Sizes.SettingsGroupRadius
	Outline(card)

	header := []fyne.CanvasObject{VerticalSpacer(theme.Sizes.SettingsGroupGap)}
	if caption != "" {
		label := canvas.NewText(strings.ToUpper(caption), theme.Colors.CategoryText)
		label.TextSize = theme.Sizes.SettingsCaptionSize
		label.TextStyle = fyne.TextStyle{Bold: true}
		header = append(header,
			NewInset(label, 0, theme.Sizes.SettingsPreviewGap, theme.Sizes.SettingsRowPaddingH, 0))
	}
	if detail != "" {
		note := canvas.NewText(detail, theme.Colors.TimestampText)
		note.TextSize = theme.Sizes.SettingsDetailSize
		header = append(header,
			NewInset(note, 0, theme.Sizes.SettingsPreviewGap, theme.Sizes.SettingsRowPaddingH, 0))
	}

	header = append(header, container.NewStack(card, body))

	return settingsGroup{caption: caption, object: VBoxNoSpacing(header...)}
}

// separateRows drops the rows that were not built — one a section refused for an
// unknown field, one advanced mode is hiding — and puts the hairline between each
// remaining pair. Dropping them here covers every path a row reaches a card by;
// a nil left in a container panics the layout that walks it.
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

// newRowSeparator is the hairline between two rows of a group. It is inset from
// both ends, so the card's rounded corners are never cut across.
func newRowSeparator() fyne.CanvasObject {
	line := canvas.NewRectangle(theme.Colors.Outline)
	line.SetMinSize(fyne.NewSize(0, theme.Sizes.OutlineWidth))

	inset := theme.Sizes.SettingsRowPaddingH

	return NewInset(line, 0, 0, inset, inset)
}

// row is the shape every setting takes: a label, an optional line of
// explanation, and one control at the trailing edge.
func (p *SettingsPage) row(label, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	var note fyne.CanvasObject
	if detail != "" {
		text := canvas.NewText(detail, theme.Colors.TimestampText)
		text.TextSize = theme.Sizes.SettingsDetailSize
		note = text
	}

	return p.rowWith(label, note, control)
}

// rowWith is row for an explanation that has to be a widget rather than a line
// of prose — a path shortened to whatever width the row has to give it.
func (p *SettingsPage) rowWith(label string, detail, control fyne.CanvasObject) fyne.CanvasObject {
	row, _ := p.markedRow(label, detail, control)

	return row
}

// markedRow is rowWith plus the bar down its left edge, handed back for the
// caller to fill. Only a toggle has anything to say with it — a row is marked
// when its setting is on — and the toggle is built a level above this, so the
// rectangle has to travel rather than the state.
func (p *SettingsPage) markedRow(label string, detail, control fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
	name := canvas.NewText(label, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.SettingsLabelSize

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

// stackedRow puts the control on a line of its own under the explanation, which
// is the only way a slider gets width enough to be aimed with. A row whose
// control is a switch stays one line — the switch says what it says at any size,
// and two lines each would double the length of every section.
func (p *SettingsPage) stackedRow(label, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	name := canvas.NewText(label, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.SettingsLabelSize

	text := []fyne.CanvasObject{name}
	if detail != "" {
		note := canvas.NewText(detail, theme.Colors.TimestampText)
		note.TextSize = theme.Sizes.SettingsDetailSize
		text = append(text, VerticalSpacer(theme.Sizes.ChipSpacing), note)
	}
	text = append(text, VerticalSpacer(theme.Sizes.SettingsControlGap), control)

	row, _ := p.frame(VBoxNoSpacing(text...))

	return row
}

// frame is the padding, the row-height floor and the marker every row shares.
// The marker is stacked over the body rather than laid beside it, so it sits at
// the card's own edge inside the row's padding.
func (p *SettingsPage) frame(body fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
	marker, markerRow := newSettingsMarker()

	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV

	return NewMinHeightContainer(theme.Sizes.SettingsRowHeight,
		container.NewStack(markerRow, NewInset(body, padV, padV, padH, padH))), marker
}

// block is a row whose content spans its full width rather than sitting in a
// control slot — a usage meter, whose whole point is the proportion it draws.
func (p *SettingsPage) block(content fyne.CanvasObject) fyne.CanvasObject {
	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV

	return NewMinHeightContainer(theme.Sizes.SettingsRowHeight,
		NewInset(content, padV, padV, padH, padH))
}

// vcenter centres obj vertically while letting it fill the width it is given.
// container.NewCenter would leave it at its minimum in both directions, which
// pulls a label into the middle of the row and collapses a slider to its thumb.
func vcenter(obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), obj, layout.NewSpacer())
}

// note is a row of prose on its own — the line that says a change waits for a
// restart, or that a feature has not been built.
func (p *SettingsPage) note(text string) fyne.CanvasObject {
	label := canvas.NewText("ⓘ  "+text, theme.Colors.TimestampText)
	label.TextSize = theme.Sizes.SettingsDetailSize

	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsPreviewGap

	return NewInset(label, padV, padV, padH, padH)
}

/* Controls */

// toggleRow is a boolean.
func (p *SettingsPage) toggleRow(label, detail string, value bool, set func(*config.Settings, bool)) fyne.CanvasObject {
	return p.boolRow(label, detail, value, func(on bool) {
		p.change(func(s *config.Settings) { set(s, on) })
	})
}

// styleToggleRow is a boolean the theme tables are built from.
func (p *SettingsPage) styleToggleRow(label, detail string, value bool, set func(*config.Settings, bool)) fyne.CanvasObject {
	return p.boolRow(label, detail, value, func(on bool) {
		p.restyle(func(s *config.Settings) { set(s, on) })
	})
}

// boolRow is a switch and the bar that follows it, so a column of settings can be
// read for what is on without reading every switch in it.
func (p *SettingsPage) boolRow(label, detail string, value bool, onChanged func(bool)) fyne.CanvasObject {
	var marker *canvas.Rectangle

	toggle := NewToggle(value, func(on bool) {
		markRow(marker, on)
		onChanged(on)
	})

	var note fyne.CanvasObject
	if detail != "" {
		text := canvas.NewText(detail, theme.Colors.TimestampText)
		text.TextSize = theme.Sizes.SettingsDetailSize
		note = text
	}

	row, marker := p.markedRow(label, note, toggle)
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

// optionRow is a choice from a short list, shown as the current value and opened
// as a menu. A menu rather than widget.Select: the client has never mounted one,
// and AppTheme flattens inputs in ways nobody has looked at.
func (p *SettingsPage) optionRow(label, detail, value string, options []settingsOption, set func(*config.Settings, string)) fyne.CanvasObject {
	var control *optionControl
	control = newOptionControl(value, options, func(picked string) {
		p.change(func(s *config.Settings) { set(s, picked) })
		control.set(picked)
	})

	return p.row(label, detail, control)
}

// numberRow is an integer within bounds: a slider for the feel of it and the
// exact value beside it, on a line of its own beneath the explanation.
func (p *SettingsPage) numberRow(label, detail string, value, low, high int, unit string, set func(*config.Settings, int)) fyne.CanvasObject {
	control := newWideNumberControl(float64(value), float64(low), float64(high), 1, unit, func(v float64) {
		p.change(func(s *config.Settings) { set(s, int(v)) })
	})

	return p.stackedRow(label, detail, control)
}

// sizeRow edits one named entry of the size table. The bounds come from the
// compiled-in default, so a row is a line of table and nothing else.
func (p *SettingsPage) sizeRow(label, field string) fyne.CanvasObject {
	def, ok := theme.DefaultSize(field)
	if !ok {
		return nil
	}

	value, _ := theme.Size(field)
	low, high := sizeBounds(def)

	control := newNumberControl(float64(value), float64(low), float64(high), 1, "px", func(v float64) {
		p.restyle(func(s *config.Settings) { setSizeOverride(s, field, float32(v), def) })
	})

	return p.row(label, "", control)
}

// colorRow edits one named entry of the palette.
func (p *SettingsPage) colorRow(label, field string) fyne.CanvasObject {
	current, ok := theme.Color(field)
	if !ok {
		return nil
	}

	control := p.colorControl(theme.Hex(current), func(hex string) {
		p.restyle(func(s *config.Settings) { setColorOverride(s, field, hex) })
	})

	return p.row(label, "", control)
}

// colorControl is a swatch that opens the palette and a field that takes a hex,
// in one box. Neither alone is enough: nobody knows a hex by heart, and no set
// of presets is every colour someone might want.
func (p *SettingsPage) colorControl(value string, set func(hex string)) fyne.CanvasObject {
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
	swatch = newSwatchButton(mustColor(value), nil)
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

// textField is an entry drawn as a control: the same box a dropdown and a colour
// row sit in, so a row that takes typing reads as one.
func textField(entry *widget.Entry) fyne.CanvasObject {
	padding := theme.Sizes.SettingsRowPaddingH

	return fixedControl(theme.Sizes.SettingsControlWidth, container.NewStack(
		newFieldBackground(),
		NewInset(WithCaret(entry), 0, 0, padding, padding),
	))
}

// actionRow is a row whose control does something rather than holding a value.
func (p *SettingsPage) actionRow(label, detail, action string, tone Tone, onTap func()) fyne.CanvasObject {
	button := widget.NewButton(action, onTap)
	button.Importance = tone.importance()

	return p.row(label, detail, button)
}

// readOnlyRow states something the client knows and the user cannot change.
func (p *SettingsPage) readOnlyRow(label, value string) fyne.CanvasObject {
	text := canvas.NewText(value, theme.Colors.TimestampText)
	text.TextSize = theme.Sizes.SettingsLabelSize

	return p.row(label, "", text)
}

// preview mounts a sample of the real widgets under a group and registers it, so
// every later change re-runs build. It carries no caption: it belongs to the
// group above it, and the rail lists somewhere to go rather than everything in
// the pane.
func (p *SettingsPage) preview(build func() fyne.CanvasObject) settingsGroup {
	host := container.NewStack(build())
	p.previews = append(p.previews, settingsPreview{host: host, build: build})

	gap := theme.Sizes.SettingsPreviewGap

	return settingsGroup{object: NewInset(host, gap, gap, 0, 0)}
}

/* Overrides */

// setSizeOverride records a size, or drops the override when it is back at the
// default — the file holds what was changed, not what the client is running.
func setSizeOverride(s *config.Settings, field string, value, def float32) {
	if value == def {
		delete(s.Styles.Sizes, field)
		return
	}

	if s.Styles.Sizes == nil {
		s.Styles.Sizes = make(map[string]float32)
	}
	s.Styles.Sizes[field] = value
}

// setColorOverride records a colour, dropping it when it matches the default.
func setColorOverride(s *config.Settings, field, hex string) {
	if def, ok := theme.DefaultColor(field); ok && theme.Hex(def) == hex {
		delete(s.Styles.Colors, field)
		return
	}

	if s.Styles.Colors == nil {
		s.Styles.Colors = make(map[string]string)
	}
	s.Styles.Colors[field] = hex
}

// sizeBounds are the range a size may be dragged through, derived from its
// default: nothing in the table means anything at three times its size, and a
// small one needs headroom a multiplier alone would not give it.
func sizeBounds(def float32) (low, high float32) {
	high = max(def*3, def+24)

	return 0, high
}
