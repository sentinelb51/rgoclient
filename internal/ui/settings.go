package ui

// The settings page: an icon rail of sections beside a scrolling pane of rows.
//
// A *layer* rather than a canvas overlay: the modal layer holds one thing at a
// time, and this page has to ask "clear the cache?" over itself. Stacked into the
// window's content it covers the client while leaving that layer free above it —
// at the cost of swallowing pointer events itself, which is what the opaque
// backdrop and the tap sink are for.
//
// Every row is one shape: a label, an optional line of explanation, and one
// control — beside the text, or under it for a slider. Sections assemble rows;
// rows never reach back into a section.

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
	"RGOClient/internal/domain"
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
	SectionPerformance
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
	{SectionPerformance, "Performance", assets.PerformanceIcon},
	{SectionAdvanced, "Advanced", assets.AdvancedIcon},
	{SectionAbout, "About", assets.AboutIcon},
}

/* The page */

// SettingsHooks is everything the page needs from the controller — plain
// functions rather than an extension of Deps, as the join dialog takes its
// callbacks: nothing else in the client needs any of them.
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

	// LogOutEverywhere revokes every session rather than this one, which is why it
	// is separate: the two read alike and one signs the user out of their phone.
	LogOutEverywhere func()

	// SetPresence publishes how the account appears to everybody else. The page
	// never hears back — the change returns as an ordinary user update, and the
	// store answers for it exactly as it would for anybody else.
	SetPresence func(presence domain.Presence)

	// SetStatusText publishes the line beside the account's name, blank clearing it.
	SetStatusText func(text string)

	// SetDisplayName publishes the name shown in place of the username, blank
	// removing it. What is short enough to refuse is Revolt's limit, so the
	// controller holds it and says so itself.
	SetDisplayName func(name string)

	// ChangeUsername asks for the new handle and the password Revolt takes with it —
	// a card on the modal layer rather than a row here, two answers being needed at
	// once and one of them a password that must not sit on a page that stays open.
	ChangeUsername func()

	// ChangeAvatar and ChangeBanner ask for a picture and hang it on the account.
	// The page supplies no path — picking a file needs the window.
	ChangeAvatar func()
	ChangeBanner func()

	RemoveAvatar func()
	RemoveBanner func()

	// SetBio publishes the description on the account's profile, blank removing it.
	SetBio func(text string)

	// LoadProfile hands back the account's own bio and banner, which are a request
	// rather than part of the user record — so the rows drawn from them are built
	// empty and filled through SetProfile. It may answer at once, from what the
	// controller already holds, and it may never answer at all.
	LoadProfile func(onLoaded func(profile domain.UserProfile))

	/* Sounds */

	// Sounds is every sound the client can make, in the order they are listed.
	// Which they are is the controller's to say: the audio package is below app and
	// above nothing this one imports, and what a sound is *for* is a question about
	// events rather than about widgets.
	Sounds func() []SettingsSound

	// ChooseSound asks for a file and points a sound at it; ResetSound puts the
	// built-in back. Both play the result — a sound is chosen by hearing it — and
	// neither reports here, the page reloading to redraw the row.
	ChooseSound func(key string, onPicked func())
	ResetSound  func(key string)

	// PlaySound sounds one whatever its own switch says, for the row's button.
	PlaySound func(key string)

	/* Cache */

	CacheDir func() string
	// ChooseCacheDir asks for a directory and reports the one picked, or nothing at
	// all. The page cannot open the dialog itself: it needs the window.
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

// SettingsSound is one sound as the Notifications section lists it. File is what
// it has been pointed at, empty for the built-in — which is not a missing file
// but a synthesised one, so no row is ever unset.
type SettingsSound struct {
	Key     string
	Title   string
	Summary string
	File    string

	// Typing marks the four the composer fires per keystroke, which are listed
	// under their own caption and answer to their own volume.
	Typing bool
}

// SettingsPage is the settings surface and its state. The controller keeps one
// for the life of the process, so a rebuild of the client behind it leaves the
// open section and the scroll position alone.
type SettingsPage struct {
	// Layer is the full-window layer to stack over the main row. Hidden until Open.
	Layer *fyne.Container

	hooks SettingsHooks

	section SettingsSection
	rail    *fyne.Container
	pane    *fyne.Container
	title   *canvas.Text

	/* Search */

	// query is what the field at the head of the rail holds. It outlives the results
	// view — a section reached from a result keeps it, which is what filters the
	// Advanced lists down to what was being looked for.
	query string

	// searching says the pane is showing results rather than a section. Separate
	// from a non-empty query for the same reason: leaving the results view does not
	// empty the field.
	searching bool

	// index is every setting the search can find, built once per open and lazily —
	// most opens never search. See settings_search.go.
	index []settingsHit

	// indexing marks the throwaway pass that walks the sections for their names:
	// a row answers with what it is called rather than with anything to draw.
	indexing bool

	// flash is the wash marking the group a jump landed on. One at a time — a
	// second jump hands the first one's card back before starting its own.
	flash *fyne.Animation

	// paneScroll is what a rail sub-entry moves and what reports the movement back,
	// so the entry marked open follows the reader rather than the last tap.
	paneScroll *ObservableScroll

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

	// account is the Account section's late arrivals, cleared with every section
	// change — see accountRows.
	account accountRows

	// advanced is AdvancedMode as the open section was built against. Rows and whole
	// sections are dropped when it is off, so it is read once per build.
	advanced bool

	// popover is the page's own floating slot, for the colour picker. It sits in
	// Layer rather than on the window's modal layer, which holds one thing at a time
	// — a picker there would be closed by the first confirmation the page raised.
	popover *fyne.Container

	// previews are the samples the Styles section draws, re-run on every change so a
	// dragged slider is answered at once: the client behind the page is covered, so
	// they are the only thing that can answer.
	previews []settingsPreview
}

// settingsPreview is a sample of the real widgets, and how to build it again.
type settingsPreview struct {
	host  *fyne.Container
	build func() fyne.CanvasObject
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

// NewSettingsPage builds the page, hidden.
func NewSettingsPage(hooks SettingsHooks) *SettingsPage {
	p := &SettingsPage{hooks: hooks, section: SectionInterface}

	p.popover = container.NewStack()
	p.popover.Hide()

	// A layer, not a stack: the page is as wide as its widest card, and a narrower
	// window would be grown to fit it the moment settings opened.
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
// widget or an image alive. Call on the UI thread.
func (p *SettingsPage) Close() {
	p.closePopover()
	p.stopFlash()
	p.Layer.Hide()
	p.Layer.Objects = nil
	p.previews = nil
	p.account = accountRows{}
	p.groups = nil
	p.subButtons = nil
	p.navGroups = nil
	p.paneScroll = nil
	p.query = ""
	p.searching = false
	p.index = nil
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

// RefreshAccount rebuilds the Account section when what it says has moved under
// it — a new picture, a new handle. A rebuild rather than a setter, those two
// being drawn by three rows between them. Deliberately *not* called for a display
// name: that field is on this section and Enter leaves the cursor in it, so an
// echo would rebuild the page under whoever was still typing. UI thread; a no-op
// while another section is open.
func (p *SettingsPage) RefreshAccount() {
	if !p.IsOpen() || p.section != SectionAccount {
		return
	}

	p.reload()
}

// SetProfile fills in the two rows the Account section cannot build from what
// the store holds: the description, and whether there is a banner to remove.
// Call on the UI thread; safe when another section is open, or none.
func (p *SettingsPage) SetProfile(profile domain.UserProfile) {
	if p.account.bio != nil {
		p.account.bio.Fill(profile.Bio)
	}

	if p.account.removeBanner != nil {
		enableIf(p.account.removeBanner, profile.BackgroundURL != "")
	}
}

// enableIf enables or disables a button from a condition.
func enableIf(button *Button, enabled bool) {
	if enabled {
		button.Enable()
		return
	}

	button.Disable()
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
	p.title = newBoldText("", theme.Colors.TextPrimary, theme.Sizes.SettingsHeaderSize)

	if p.searching {
		p.showResults()
	} else {
		p.showSection(p.section)
	}

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
// label at one end and a control at the other, and across a maximised window the
// two lose each other. The header goes through it too, so the title sits over the
// cards' left edge rather than the window's.
//
// Horizontally only: container.NewCenter would centre vertically as well, leaving
// a short section floating in the middle rather than under its own title.
func (p *SettingsPage) centred(content fyne.CanvasObject) fyne.CanvasObject {
	body := NewFixedWidthContainer(theme.Sizes.SettingsPageWidth, content)

	return container.NewHBox(layout.NewSpacer(), body, layout.NewSpacer())
}

// buildHeader is the pane's title and close button. The title is centred with the
// cards; the button is anchored to the pane's top right through overlayLayout,
// which reports no minimum and so cannot pull the title off centre.
func (p *SettingsPage) buildHeader() fyne.CanvasObject {
	padding := theme.Sizes.SettingsPagePadding

	title := p.centred(NewInset(p.title, padding, theme.Sizes.SettingsGroupGap, padding, padding))
	dismiss := container.New(&overlayLayout{yOffset: padding, rightOffset: padding},
		NewCloseButton(p.hooks.Close))

	return container.NewStack(title, dismiss)
}

// buildRailColumn is the rail, the advanced-mode switch under it, and the seam
// separating the column from the pane — every column's hairline, inside its own
// fixed width.
func (p *SettingsPage) buildRailColumn() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	padding := theme.Sizes.SettingsPagePadding

	caption := newBoldText("SETTINGS", theme.Colors.CategoryText, theme.Sizes.SettingsCaptionSize)
	padH := theme.Sizes.SettingsRowPaddingH

	// The field is pinned above the scroll rather than listed in it: it is what the
	// rail below is showing, and a search box that scrolls away is one nobody finds.
	head := NewInset(
		VBoxNoSpacing(caption, VerticalSpacer(theme.Sizes.SettingsPreviewGap), p.buildSearchField()),
		padding, theme.Sizes.SettingsPreviewGap, padH, padH,
	)

	content := NewInset(p.rail, 0, padding, padH, padH)

	column := NewFillColumn(1, head, container.NewVScroll(content), p.buildRailFooter())

	return NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, background,
		NewFillRow(0, column, NewColumnDivider()))
}

// buildRailFooter is the advanced-mode switch, at the foot of the rail rather
// than among the settings because it decides which of them there are — and the
// rail is where the reader looks when something they remember is missing.
func (p *SettingsPage) buildRailFooter() fyne.CanvasObject {
	label := newText("Advanced mode", theme.Colors.CategoryText, theme.Sizes.SettingsRailTextSize)

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
	p.subButtons = nil

	var buttons []fyne.CanvasObject
	for _, entry := range visibleRailEntries(p.advanced) {
		// Nothing is open while results are showing, so nothing is marked: the rail
		// is the way out of them rather than a record of where they came from.
		open := !p.searching && entry.section == p.section

		buttons = append(buttons, newSettingsRailButton(entry, open, func() {
			p.showSection(entry.section)
		}))

		if !open {
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

// visibleRailEntries drops the sections advanced mode hides — the raw size and
// colour tables, which are the whole reason the mode exists.
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

// showSection swaps the pane to one section's groups, leaving the results view if
// that is what was showing.
func (p *SettingsPage) showSection(section SettingsSection) {
	// Advanced mode can be switched off while its own section is open, and
	// "Reset every setting" switches it off from another one entirely.
	if section == SectionAdvanced && !p.advanced {
		section = SectionInterface
	}

	p.section = section
	p.searching = false
	p.mount(p.sectionGroups(section), railTitle(section))
}

// sectionGroups builds one section. Split from showSection because the index pass
// walks every section without mounting any of them.
func (p *SettingsPage) sectionGroups(section SettingsSection) []settingsGroup {
	switch section {
	case SectionAccount:
		return p.accountSection()
	case SectionInterface:
		return p.interfaceSection()
	case SectionStyles:
		return p.stylesSection()
	case SectionBehaviour:
		return p.behaviourSection()
	case SectionNotifications:
		return p.notificationsSection()
	case SectionCache:
		return p.cacheSection()
	case SectionPerformance:
		return p.performanceSection()
	case SectionAdvanced:
		return p.advancedSection()
	case SectionAbout:
		return p.aboutSection()
	}

	return nil
}

// mount puts one set of groups in the pane and re-heads the rail from it. The one
// path a section and a page of results both arrive by.
func (p *SettingsPage) mount(groups []settingsGroup, title string) {
	p.closePopover() // its anchor is about to stop existing
	p.stopFlash()    // as is the card it was washing
	p.previews = nil
	p.account = accountRows{} // a profile landing after this has nothing left to fill

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

	p.title.Text = title
	p.title.Refresh()
	p.buildRail()
}

/* Moving between groups */

// measureGroups records where each group starts inside the pane. Summed from the
// groups above rather than read from Position(), which is not set until the first
// layout — and taken once per section rather than per scroll, a MinSize on a
// group card being a walk of every row in it.
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
// content, so the last group never reaches the top and would never light.
func (p *SettingsPage) scrollToNav(nav int) {
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
func (p *SettingsPage) flashGroup(group int) {
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
func (p *SettingsPage) stopFlash() {
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

// restyle records a setting the theme tables are built from, applies them and
// re-runs the previews. The controller repaints the client behind the page; the
// previews are what can actually be seen change.
func (p *SettingsPage) restyle(mutate func(*config.Settings)) {
	p.hooks.Update(mutate)
	p.hooks.Restyle()

	for _, preview := range p.previews {
		preview.host.Objects = []fyne.CanvasObject{preview.build()}
		preview.host.Refresh()
	}
}

// reload rebuilds the current section, for a change that alters which rows it
// shows rather than only what one says. Advanced mode is re-read here: a rail tap
// cannot change it, and both things that can come through this.
func (p *SettingsPage) reload() {
	p.advanced = config.Current().Interface.AdvancedMode
	p.index = nil // what a section holds has just moved under it

	if p.searching {
		p.showResults()
	} else {
		p.showSection(p.section)
	}
}

/* Rows and groups */

// adv marks a row advanced mode reveals: a timing, a cap, a budget — something
// tuning the client rather than describing what it does. In basic mode it answers
// nil, which separateRows drops and group drops the whole card for.
func (p *SettingsPage) adv(row fyne.CanvasObject) fyne.CanvasObject {
	if !p.advanced {
		return nil
	}

	return row
}

// group is a captioned card of rows: a hairline between each pair, the caption
// outside the card above it. A card with nothing left in it is no card at all.
func (p *SettingsPage) group(caption, detail string, rows ...fyne.CanvasObject) settingsGroup {
	kept := separateRows(rows)
	if len(kept) == 0 {
		return settingsGroup{}
	}

	if p.indexing {
		return p.recordGroup(caption, kept)
	}

	return p.groupOf(caption, detail, VBoxNoSpacing(kept...))
}

// groupOf is the same card around a body the caller keeps a handle on, for the
// one section refilled in place rather than rebuilt — the Advanced filter, which
// must not take the field it is being typed into with it.
func (p *SettingsPage) groupOf(caption, detail string, body *fyne.Container) settingsGroup {
	if p.indexing {
		return p.recordGroup(caption, body.Objects)
	}

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

	card := newSettingsCard()
	header = append(header, container.NewStack(card, body))

	return settingsGroup{caption: caption, object: VBoxNoSpacing(header...), card: card}
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
func (p *SettingsPage) row(label, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	row, _ := p.markedRow(label, detail, control)

	return row
}

// rowWith is row for an explanation that has to be a widget rather than a line
// of prose — a path shortened to whatever width the row has to give it.
func (p *SettingsPage) rowWith(label string, detail, control fyne.CanvasObject) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow(label)
	}

	row, _ := p.rowOf(rowLabel(label, rowTextWidth(control)), detail, control)

	return row
}

// markedRow is row plus the bar down its left edge, handed back for the caller to
// fill. Only a toggle has anything to say with it, and the toggle is built a
// level above, so the rectangle travels rather than the state.
func (p *SettingsPage) markedRow(label, detail string, control fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
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
func (p *SettingsPage) rowOf(name fyne.CanvasObject, detail, control fyne.CanvasObject) (fyne.CanvasObject, *canvas.Rectangle) {
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
func (p *SettingsPage) stackedRow(label, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	if p.indexing {
		return newIndexRow(label)
	}

	text := []fyne.CanvasObject{rowLabel(label, cardWidth())}
	if detail != "" {
		text = append(text, VerticalSpacer(theme.Sizes.ChipSpacing), rowDetail(detail, cardWidth()))
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
// container.NewCenter leaves it at its minimum in both directions, which pulls a
// label into the middle of the row and collapses a slider to its thumb.
func vcenter(obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), obj, layout.NewSpacer())
}

// note is a row of prose on its own — the line that says a change waits for a
// restart, or that a feature has not been built.
func (p *SettingsPage) note(text string) fyne.CanvasObject {
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

// optionRow is a choice from a short list, shown as the current value and opened
// as a menu — rather than widget.Select, which the client has never mounted and
// which AppTheme flattens in ways nobody has looked at.
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

// colorControl is a swatch opening the palette and a field taking a hex, in one
// box. Neither alone is enough: nobody knows a hex by heart, and no set of
// presets is every colour someone might want.
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

// actionRow is a row whose control does something rather than holding a value.
func (p *SettingsPage) actionRow(label, detail, action string, tone Tone, onTap func()) fyne.CanvasObject {
	return p.row(label, detail, newRowButton(action, tone, onTap))
}

// newRowButton is one button of a row offering more than one thing. A single
// button reaches this through actionRow; two have to be centred by the caller, an
// HBox handing its children its own height.
func newRowButton(label string, tone Tone, onTap func()) *Button {
	return NewWeightedButton(label, tone.weight(), onTap)
}

// readOnlyRow states something the client knows and the user cannot change.
func (p *SettingsPage) readOnlyRow(label, value string) fyne.CanvasObject {
	text := newText(value, theme.Colors.TimestampText, theme.Sizes.SettingsLabelSize)

	return p.row(label, "", text)
}

// preview mounts a sample of the real widgets under a group and registers it, so
// every later change re-runs build. No caption: it belongs to the group above,
// and the rail lists somewhere to go rather than everything in the pane.
func (p *SettingsPage) preview(build func() fyne.CanvasObject) settingsGroup {
	if p.indexing {
		return settingsGroup{} // nothing named, and mounting real widgets to find that out
	}

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

// sizeBounds is the range a size may be dragged through, from its default:
// nothing in the table means anything at three times its size, and a small one
// needs headroom a multiplier alone would not give it.
func sizeBounds(def float32) (low, high float32) {
	high = max(def*3, def+24)

	return 0, high
}
