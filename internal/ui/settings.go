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
// control. Sections assemble rows; rows never reach back into a section.

import (
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

// notImplemented marks a row whose setting is recorded and honoured, but whose
// feature does not exist yet.
const notImplemented = "Not implemented yet."

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

// NewSettingsPage builds the page, hidden.
func NewSettingsPage(hooks SettingsHooks) *SettingsPage {
	p := &SettingsPage{hooks: hooks, section: SectionInterface}

	p.popover = container.NewStack()
	p.popover.Hide()

	p.Layer = container.NewStack()
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
}

// Rebuild constructs the page from the theme tables as they now stand. Called on
// open, and by the controller after a style change that the page itself should
// pick up. Call on the UI thread.
func (p *SettingsPage) Rebuild() {
	p.closePopover()
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

	p.buildRail()
	p.showSection(p.section)

	pane := NewFillColumn(1, p.buildHeader(), container.NewVScroll(p.paneBody()))

	row := NewFillRow(1,
		NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, p.buildRailColumn()),
		pane,
	)

	return newTapSink(container.NewStack(backdrop, row))
}

// paneBody centres the pane's content horizontally and caps its width. A row is
// a label at one end and a control at the other; across a maximised window the
// two lose each other entirely.
//
// Horizontally only: container.NewCenter would centre it vertically too, which
// leaves a short section — Account is one — floating in the middle of the window
// rather than starting under its own title.
func (p *SettingsPage) paneBody() fyne.CanvasObject {
	padding := theme.Sizes.SettingsPagePadding

	body := NewFixedWidthContainer(theme.Sizes.SettingsPageWidth,
		NewInset(p.pane, 0, padding, padding, padding))

	return container.NewHBox(layout.NewSpacer(), body, layout.NewSpacer())
}

// buildHeader is the pane's title and its close button.
func (p *SettingsPage) buildHeader() fyne.CanvasObject {
	padding := theme.Sizes.SettingsPagePadding

	return NewInset(
		container.NewBorder(nil, nil, nil, NewCloseButton(p.hooks.Close), p.title),
		padding, theme.Sizes.SettingsGroupGap, padding, padding,
	)
}

// buildRailColumn is the rail plus the seam that separates it from the pane —
// the same hairline every other column carries, inside its own fixed width.
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

	return NewFixedWidthContainer(theme.Sizes.SettingsRailWidth, background,
		NewFillRow(0, container.NewVScroll(content), NewColumnDivider()))
}

// buildRail fills the rail with one button per section.
func (p *SettingsPage) buildRail() {
	p.rail.Objects = nil
	for _, entry := range railEntries {
		p.rail.Add(newSettingsRailButton(entry, entry.section == p.section, func() {
			p.showSection(entry.section)
			p.pane.Refresh()
		}))
	}
}

// showSection swaps the pane to one section's rows.
func (p *SettingsPage) showSection(section SettingsSection) {
	p.closePopover() // its anchor is about to stop existing
	p.section = section
	p.previews = nil

	var groups []fyne.CanvasObject
	switch section {
	case SectionAccount:
		groups = p.accountSection()
	case SectionInterface:
		groups = p.interfaceSection()
	case SectionStyles:
		groups = p.stylesSection()
	case SectionBehaviour:
		groups = p.behaviourSection()
	case SectionNotifications:
		groups = p.notificationsSection()
	case SectionCache:
		groups = p.cacheSection()
	case SectionAdvanced:
		groups = p.advancedSection()
	case SectionAbout:
		groups = p.aboutSection()
	}

	p.pane.Objects = groups
	p.title.Text = railTitle(section)
	p.title.Refresh()
	p.buildRail()
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
// section shows rather than only what one of them says.
func (p *SettingsPage) reload() {
	p.showSection(p.section)
	p.pane.Refresh()
}

/* Rows and groups */

// group is a captioned card of rows: the inset-list shape, one hairline between
// each pair of rows and the caption sitting outside the card above it.
func (p *SettingsPage) group(caption, detail string, rows ...fyne.CanvasObject) fyne.CanvasObject {
	return p.groupOf(caption, detail, VBoxNoSpacing(separateRows(rows)...))
}

// groupOf is the same card around a body the caller keeps a handle on, for the
// one section whose rows are refilled in place rather than rebuilt — the
// Advanced filter, which must not take the field it is being typed into with it.
func (p *SettingsPage) groupOf(caption, detail string, body *fyne.Container) fyne.CanvasObject {
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

	return VBoxNoSpacing(header...)
}

// separateRows puts the hairline between each pair.
func separateRows(rows []fyne.CanvasObject) []fyne.CanvasObject {
	separated := make([]fyne.CanvasObject, 0, max(len(rows)*2-1, 0))
	for i, row := range rows {
		if i > 0 {
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

	padH, padV := theme.Sizes.SettingsRowPaddingH, theme.Sizes.SettingsRowPaddingV

	return NewMinHeightContainer(theme.Sizes.SettingsRowHeight,
		NewInset(body, padV, padV, padH, padH))
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
	toggle := NewToggle(value, func(on bool) { p.change(func(s *config.Settings) { set(s, on) }) })

	return p.row(label, detail, toggle)
}

// styleToggleRow is a boolean the theme tables are built from.
func (p *SettingsPage) styleToggleRow(label, detail string, value bool, set func(*config.Settings, bool)) fyne.CanvasObject {
	toggle := NewToggle(value, func(on bool) { p.restyle(func(s *config.Settings) { set(s, on) }) })

	return p.row(label, detail, toggle)
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
// exact value beside it.
func (p *SettingsPage) numberRow(label, detail string, value, low, high int, unit string, set func(*config.Settings, int)) fyne.CanvasObject {
	control := newNumberControl(float64(value), float64(low), float64(high), 1, unit, func(v float64) {
		p.change(func(s *config.Settings) { set(s, int(v)) })
	})

	return p.row(label, detail, control)
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
// every later change re-runs build.
func (p *SettingsPage) preview(build func() fyne.CanvasObject) fyne.CanvasObject {
	host := container.NewStack(build())
	p.previews = append(p.previews, settingsPreview{host: host, build: build})

	gap := theme.Sizes.SettingsPreviewGap

	return NewInset(host, gap, gap, 0, 0)
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
