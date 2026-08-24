package ui

// The client's own settings: which sections there are, what each holds, and the
// controls that write to config. The surface they are drawn on — the rail, the
// pane, and the vocabulary of rows — is settings_shell.go, which this page and a
// server's settings share.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

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
	SectionVoice
	SectionCache
	SectionPerformance
	SectionAdvanced
	SectionAbout
)

var railEntries = []railEntry{
	{int(SectionAccount), "Account", assets.AccountIcon},
	{int(SectionInterface), "Interface", assets.InterfaceIcon},
	{int(SectionStyles), "Styles", assets.StylesIcon},
	{int(SectionBehaviour), "Behaviour", assets.BehaviourIcon},
	{int(SectionNotifications), "Notifications", assets.NotifyIcon},
	{int(SectionVoice), "Voice", assets.MicIcon},
	{int(SectionCache), "Cache", assets.CacheIcon},
	{int(SectionPerformance), "Performance", assets.PerformanceIcon},
	{int(SectionAdvanced), "Advanced", assets.AdvancedIcon},
	{int(SectionAbout), "About", assets.AboutIcon},
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

	/* Voice */

	// InputDevices and OutputDevices enumerate what the machine offers. Both walk
	// the audio backend, so both are stubbed out for the index pass.
	InputDevices  func() []AudioDevice
	OutputDevices func() []AudioDevice

	// StartInputMonitor opens the microphone and reports its level until
	// StopInputMonitor. The controller samples — a level arrives off the audio
	// thread and each repaint is the whole window — so this is called at a rate a
	// meter can be drawn at rather than at the device's.
	//
	// Opening a device is why it must never run during the index pass, and why
	// stopping it belongs to both showSection and Close.
	StartInputMonitor func(report func(level float32))
	StopInputMonitor  func()

	// GateRatio is where a sensitivity setting falls on the meter's scale, 0-1.
	// The meter draws a level and a threshold on one bar and the two have to agree
	// about decibels; the mapping belongs to the audio package, which ui does not
	// import, so it crosses as a plain ratio like the level itself.
	GateRatio func(sensitivity int) float32

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

	/* Performance */

	// CPUCores describes the machine's cores. `ui` must not import `cpu`, so the
	// counts cross as a value the way an AudioDevice does, and the controller keeps
	// what a kind of core is *for* on its own side.
	CPUCores func() CPUCores

	/* About */

	ConfigPath func() string
	OpenPath   func(path string)
}

// CPUCores is what the settings page is told about the machine's cores.
type CPUCores struct {
	Performance int
	Efficiency  int

	// Hybrid is which split this is, and the two read nothing alike to somebody
	// choosing between them: true is Intel's performance and efficiency cores,
	// where one side is slower on purpose, and false is AMD's chiplets, where one
	// side clocks higher because it carries less cache.
	Hybrid bool
}

// Split reports whether there is anything to choose between. Without it the group
// is dropped from the page rather than drawn as a control whose four values would
// all pick the same processors — the same rule the taskbar-flash group follows.
func (c CPUCores) Split() bool {

	return c.Performance > 0 && c.Efficiency > 0
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
	settingsShell

	hooks SettingsHooks

	section SettingsSection

	/* Search */

	// query is what the field at the head of the rail holds. It outlives the results
	// view — a section reached from a result keeps it, which is what filters the
	// Advanced lists down to what was being looked for.
	query string

	// searching says the pane is showing results rather than a section. Separate
	// from a non-empty query for the same reason: leaving the results view does not
	// empty the field.
	searching bool

	// field is the box itself, kept so the results page's back line can empty it.
	// Leaving results is the one exit that has to move the field as well as the
	// pane, the query being what put them there.
	field *searchEntry

	// index is every setting the search can find, built once per open and lazily —
	// most opens never search. See settings_search.go.
	index []settingsHit

	// account is the Account section's late arrivals, cleared with every section
	// change — see accountRows.
	account accountRows

	// advanced is AdvancedMode as the open section was built against. Rows and whole
	// sections are dropped when it is off, so it is read once per build.
	advanced bool

	// previews are the samples the Styles section draws, re-run on every change so a
	// dragged slider is answered at once: the client behind the page is covered, so
	// they are the only thing that can answer.
	previews []settingsPreview

	// meter is the Voice section's input level bar, and the one control on this
	// page that owns a *device*. It is stopped by both showSection and Close — the
	// page has no unmount hook, and a discarded widget hears nothing.
	meter *voiceLevelMeter
}

// settingsPreview is a sample of the real widgets, and how to build it again.
type settingsPreview struct {
	host  *fyne.Container
	build func() fyne.CanvasObject
}

// NewSettingsPage builds the page, hidden.
func NewSettingsPage(hooks SettingsHooks) *SettingsPage {
	p := &SettingsPage{hooks: hooks, section: SectionInterface}

	p.initShell(hooks.Close)
	p.record = p.recordGroup

	return p
}

// Open builds the page and shows it. Call on the UI thread.
func (p *SettingsPage) Open() {
	p.Rebuild()
	p.Layer.Show()
}

// Close hides the page and drops what it built, so nothing it mounted keeps a
// widget or an image alive. Call on the UI thread.
func (p *SettingsPage) Close() {
	p.stopMeter()
	p.resetShell()
	p.previews = nil
	p.account = accountRows{}
	p.query = ""
	p.searching = false
	p.field = nil
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

// build assembles the whole surface: the shell, around whichever section — or
// page of results — was showing.
func (p *SettingsPage) build() fyne.CanvasObject {
	p.newSurface()

	if p.searching {
		p.showResults()
	} else {
		p.showSection(p.section)
	}

	// The field is pinned above the rail rather than listed in it: it is what the
	// rail below is showing, and a search box that scrolls away is one nobody finds.
	return p.buildSurface("Settings", p.buildSearchField(), p.buildRailFooter())
}

// rebuildRail re-lists the sections and the open one's groups. Nothing is marked
// while results are showing: the rail is the way out of them rather than a record
// of where they came from, so it is handed a section no entry carries.
func (p *SettingsPage) rebuildRail() {
	open := -1
	if !p.searching {
		open = int(p.section)
	}

	p.buildRail(visibleRailEntries(p.advanced), open, func(section int) {
		p.showSection(SettingsSection(section))
	})
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

// visibleRailEntries drops the sections advanced mode hides — the raw size and
// colour tables, which are the whole reason the mode exists.
func visibleRailEntries(advanced bool) []railEntry {
	if advanced {
		return railEntries
	}

	visible := make([]railEntry, 0, len(railEntries))
	for _, entry := range railEntries {
		if entry.section != int(SectionAdvanced) {
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

	p.stopMeter() // the Voice section holds a microphone open; leaving it must not
	p.section = section
	p.searching = false
	p.account = accountRows{} // a profile landing after this has nothing left to fill
	p.previews = nil
	p.mount(p.sectionGroups(section), railTitle(section))
	p.rebuildRail()
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
	case SectionVoice:
		return p.voiceSection()
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

func railTitle(section SettingsSection) string {
	for _, entry := range railEntries {
		if entry.section == int(section) {
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

	control := p.colorControl(theme.Hex(current), nil, func(hex string) {
		p.restyle(func(s *config.Settings) { setColorOverride(s, field, hex) })
	})

	return p.row(label, "", control)
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
