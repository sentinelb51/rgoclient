package ui

// The emoji picker: one pop-up serving both the composer and a reaction, the two
// picking from the same set and differing only in what they do with the answer.
//
// Nothing here is fetched. Ready carries the emoji of every server the account is
// in and revoltgo files create/delete into State itself, so Store.Emojis is
// already the whole set and already current; the picker asks once as it opens.

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// emojiPickerLimit is how many cells the grid draws at once. A server may define
// hundreds and an account may be in many, so the whole set is a page nobody reads
// and a widget per entry nobody sees — the search field is what reaches past it.
const emojiPickerLimit = 120

// unicodeMark stands in the rail for the group that belongs to no server, the one
// entry with no icon to draw. A character rather than one of the client's own
// marks: what the group holds is characters.
const unicodeMark = "🙂"

/* What can be picked */

// EmojiChoice is one pickable emoji: a character, or a custom one that is a
// picture and an ID. Which it is decides both how it draws and what it is worth
// to a caller, so both are answered here rather than at each call site.
type EmojiChoice struct {
	ID   string // the custom emoji's ULID; "" for a unicode character
	Name string // what the preview line reads, and the first thing search matches
	Char string // the character itself; "" for a custom emoji

	// Keywords are the other words this answers to, searched and never drawn: "no"
	// has to reach 👎 without the preview line reading "thumbs down no".
	Keywords string
}

// Value is what a reaction carries. Revolt takes the ID of a custom emoji and the
// character of anything else, in the same field.
func (c EmojiChoice) Value() string {
	if c.ID != "" {
		return c.ID
	}

	return c.Char
}

// Token is what a message body carries. A custom emoji is written as its ID
// between colons, which is what markdown.Emoji reads back.
func (c EmojiChoice) Token() string {
	if c.ID != "" {
		return ":" + c.ID + ":"
	}

	return c.Char
}

// EmojiGroup is a heading and what is under it — one server's emoji, or the
// unicode set. An empty group is dropped rather than drawn over nothing. The icon
// is what the rail jumps by; a group with no ServerID is the unicode set, which
// has no picture to stand for it.
type EmojiGroup struct {
	ServerID string
	Title    string
	IconID   string
	IconURL  string

	Choices []EmojiChoice
}

// UnicodeEmoji is the set that works in every channel, conversations included,
// where a server's own emoji do not exist. Each carries a name so the search
// field reaches it — otherwise typing would filter one half of the picker only.
var UnicodeEmoji = []EmojiChoice{
	{Char: "👍", Name: "thumbs up", Keywords: "yes ok agree"},
	{Char: "👎", Name: "thumbs down", Keywords: "no disagree"},
	{Char: "❤️", Name: "heart", Keywords: "love red"},
	{Char: "😂", Name: "laugh", Keywords: "joy funny lol"},
	{Char: "😮", Name: "surprised", Keywords: "wow shock"},
	{Char: "😢", Name: "sad", Keywords: "cry tear"},
	{Char: "🎉", Name: "party", Keywords: "celebrate tada"},
	{Char: "🔥", Name: "fire", Keywords: "hot lit"},
	{Char: "🤔", Name: "thinking", Keywords: "hmm think"},
	{Char: "✅", Name: "check", Keywords: "done tick yes"},
	{Char: "❌", Name: "cross", Keywords: "wrong no"},
	{Char: "🙏", Name: "thanks", Keywords: "please pray"},
}

/* The picker */

// ShowEmojiPicker drops the picker beside anchor and calls onPick with whatever
// is chosen. Groups are in the order they are drawn — the open server first, so
// what somebody reaches for most is what they land on.
func ShowEmojiPicker(deps Deps, anchor fyne.CanvasObject, groups []EmojiGroup, onPick func(EmojiChoice)) {
	c := fyne.CurrentApp().Driver().CanvasForObject(anchor)
	if c == nil {
		return
	}

	newEmojiPicker(deps, c, groups, onPick).showBeside(anchor)
}

// emojiEntry is one choice with its search key folded once. A keystroke asks
// every entry whether it matches, so folding at the point of comparison would
// lower the whole set per character typed.
type emojiEntry struct {
	choice EmojiChoice
	fold   string
}

// emojiSection is a group and its folded entries — what the picker was handed,
// prepared once when it opens.
type emojiSection struct {
	group   EmojiGroup
	entries []emojiEntry
}

// emojiPicker is the pop-up itself: a header naming what the pointer is over, the
// search field, and a rail of the servers on offer beside the grid. It wears its
// own rounded fill, the hairline and a shadow over the plain panel Fyne draws
// behind any pop-up, which is what makes it read as a card floating over the
// client rather than a menu dropping out of the composer. A plain struct rather
// than a widget — everything it draws is a container, and nothing here answers an
// event its children do not.
type emojiPicker struct {
	deps     Deps
	sections []emojiSection
	onPick   func(EmojiChoice)

	search *emojiSearch
	list   *fyne.Container // the sections, replaced wholesale on every query
	scroll *ObservableScroll

	/* The header */

	previewIcon  *fyne.Container
	previewName  *fyne.Container
	previewGroup *fyne.Container

	/* The rail */

	// blocks are the drawn sections and railButtons the icons that jump to them:
	// parallel, and both rebuilt per query, a section a query filtered away being
	// nothing the rail can point at.
	blocks      []*fyne.Container
	rail        *fyne.Container
	railButtons []*emojiRailButton
	active      int

	// tip names a rail icon. The picker's own rather than the app's: the app's is a
	// layer in the window's content, and a pop-up is a canvas overlay over all of
	// it, so a label mounted there would be covered by the rail it is naming.
	tip *Tooltip

	// cells memoises one widget per emoji, so narrowing a query reorders objects
	// that already exist and a picture is asked of the cache once rather than once
	// per keystroke. Lazy, so a set past the limit costs nothing for what is unseen.
	cells map[string]fyne.CanvasObject

	// The same memo for a section's own chrome, indexed by its place in sections —
	// which foldGroups fixes as the picker opens, where blocks and railButtons above
	// are in the order a query drew them. Without it a keystroke rebuilds every
	// caption and grid and asks the image cache for every rail icon again.
	sectionRails  []*emojiRailButton
	sectionBlocks []*fyne.Container
	sectionGrids  []*fyne.Container

	// placed is fill's guard against drawing one emoji twice, held here so the map
	// is allocated once per opening rather than once per keystroke.
	placed map[string]bool

	// top is what Enter picks: the first match of the current query, which is why
	// the field is worth typing into rather than scrolling past. topIn is the group
	// it sits under, the header naming one as well as drawing it.
	top   EmojiChoice
	topIn string
	found bool

	hovered EmojiChoice
	over    bool

	content fyne.CanvasObject
	popUp   *widget.PopUp
	canvas  fyne.Canvas
}

func newEmojiPicker(deps Deps, c fyne.Canvas, groups []EmojiGroup, onPick func(EmojiChoice)) *emojiPicker {
	sections := foldGroups(groups)
	p := &emojiPicker{
		deps:     deps,
		sections: sections,
		onPick:   onPick,
		list:     VBoxNoSpacing(),
		rail:     container.NewVBox(),
		tip:      NewTooltip(),
		cells:    make(map[string]fyne.CanvasObject),

		sectionRails:  make([]*emojiRailButton, len(sections)),
		sectionBlocks: make([]*fyne.Container, len(sections)),
		sectionGrids:  make([]*fyne.Container, len(sections)),
		placed:        make(map[string]bool, emojiPickerLimit),
	}

	p.search = newEmojiSearch(p.fill, p.acceptTop, func() { p.popUp.Hide() })

	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = theme.Sizes.EmojiPickerRadius
	Outline(background)
	Elevate(background)

	gap := theme.Sizes.EmojiPickerGap

	// Held off the right edge by the indicator's own width, so the bar does not land
	// on the last cell of every row.
	gutter := theme.Sizes.ScrollIndicatorWidth + theme.Sizes.ScrollIndicatorInset*2
	scrolled := NewInset(p.list, 0, 0, 0, gutter)

	p.scroll = NewObservableVScroll(scrolled)
	p.scroll.OnScroll = func(offset fyne.Position) { p.markSectionAt(offset.Y) }

	// One group is the unicode set on its own, which a rail of one icon says nothing
	// about. Decided as the picker opens rather than per query: a rail appearing and
	// vanishing as a query narrows would re-wrap the grid under the pointer.
	railColumn := p.buildRail()
	showIf(railColumn, len(p.sections) > 1)

	// The scroller cannot be asked how tall it wants to be — container.Scroll
	// reports its own current height as its minimum — so the list is measured and
	// the ceiling applied here. The rail beside it is handed whatever that comes to.
	viewport := container.New(
		&cappedHeightLayout{content: scrolled, max: theme.Sizes.EmojiPickerMaxHeight},
		NewFillRow(1, railColumn, NewInset(p.scroll, 0, 0, gap, 0)))

	body := VBoxNoSpacing(
		p.buildPreview(), VerticalSpacer(gap),
		viewport, VerticalSpacer(gap),
		p.searchField())

	p.content = container.NewStack(background, NewInset(body, gap, gap, gap, gap), p.tip.Layer)

	p.fill("")

	p.popUp = widget.NewPopUp(NewFixedWidthContainer(theme.Sizes.EmojiPickerWidth, p.content), c)
	p.canvas = c

	return p
}

// foldGroups lowers every name and its keywords once as the picker opens — the
// walk the grid is about to make anyway, saving one per keystroke over a set that
// can run to thousands.
func foldGroups(groups []EmojiGroup) []emojiSection {
	sections := make([]emojiSection, 0, len(groups))

	for _, group := range groups {
		entries := make([]emojiEntry, 0, len(group.Choices))
		for _, choice := range group.Choices {
			fold := strings.ToLower(choice.Name)
			if choice.Keywords != "" {
				fold += " " + strings.ToLower(choice.Keywords)
			}

			entries = append(entries, emojiEntry{choice: choice, fold: fold})
		}

		sections = append(sections, emojiSection{group: group, entries: entries})
	}

	return sections
}

// searchField is the client's own field surface rather than a bare Entry: an
// entry under AppTheme draws no box of its own, and a caret blinking on the
// picker's background would not read as somewhere to type.
func (p *emojiPicker) searchField() fyne.CanvasObject {
	padding := theme.Sizes.EmojiPickerGap

	return NewFixedHeightContainer(theme.Sizes.SettingsInputHeight, container.NewStack(
		newFieldBackground(),
		NewInset(WithCaret(p.search), 0, 0, padding, padding),
	))
}

/* The header */

// buildPreview is the strip at the head of the island: the emoji under the
// pointer drawn large, its name, and the group it came from. A cell is a 34-unit square of
// somebody else's artwork, which is most of what there is to recognise one by —
// and with nothing hovered the strip says what Enter would take.
func (p *emojiPicker) buildPreview() fyne.CanvasObject {
	gap := theme.Sizes.EmojiPickerGap
	side := theme.Sizes.EmojiPickerPreviewSize

	p.previewIcon = container.NewGridWrap(fyne.NewSize(side, side))
	p.previewName = NewEllipsisText(
		newBoldText("", theme.Colors.TextPrimary, theme.Sizes.EmojiPickerPreviewNameSize))
	p.previewGroup = NewEllipsisText(
		newText("", theme.Colors.CategoryText, theme.Sizes.EmojiPickerCaptionSize))

	// The island's padding, the card's own and the gap beside the picture: what is
	// left is what a name has to be truncated into. Known here rather than at a
	// layout, the pop-up being a fixed width.
	room := theme.Sizes.EmojiPickerWidth - 5*gap - side
	lines := NewFixedWidthContainer(room, VBoxNoSpacing(p.previewName, p.previewGroup))

	card := canvas.NewRectangle(theme.Colors.SessionCardBg)
	card.CornerRadius = theme.Sizes.ReactionRadius

	row := HBoxNoSpacing(p.previewIcon, HorizontalSpacer(gap), container.NewCenter(lines))

	return container.NewStack(card, NewInset(row, gap, gap, gap, gap))
}

// showPreview draws one emoji in the header, named and filed.
func (p *emojiPicker) showPreview(choice EmojiChoice, in string) {
	p.previewIcon.Objects = []fyne.CanvasObject{newPreviewEmoji(p.deps, choice)}
	p.previewIcon.Refresh()

	SetEllipsisText(p.previewName, previewName(choice))
	SetEllipsisText(p.previewGroup, strings.ToUpper(in))
}

// restPreview is the header with nothing hovered: what Enter would take, or the
// one line saying a query matched nothing — which is the whole of the empty
// state, the grid under it collapsing to nothing of its own accord.
func (p *emojiPicker) restPreview() {
	if p.found {
		p.showPreview(p.top, p.topIn)
		return
	}

	p.previewIcon.Objects = nil
	p.previewIcon.Refresh()

	SetEllipsisText(p.previewName, "No emoji match that.")
	SetEllipsisText(p.previewGroup, "")
}

// setHovered records what the pointer is over and names it. A cell reports
// leaving with the choice it entered with, so a stale leave cannot take down the
// header of the cell just entered.
func (p *emojiPicker) setHovered(choice EmojiChoice, in string, over bool) {
	if !over && (!p.over || p.hovered.Value() != choice.Value()) {
		return
	}

	p.hovered, p.over = choice, over

	if !over {
		p.restPreview()
		return
	}

	p.showPreview(choice, in)
}

// previewName is what one emoji is called. A custom one is written the way a
// message carries it, so the label doubles as what typing it by hand looks like.
func previewName(choice EmojiChoice) string {
	if choice.Name == "" {
		return ""
	}
	if choice.ID != "" {
		return ":" + choice.Name + ":"
	}

	return choice.Name
}

/* The rail */

// buildRail is the column of server icons down the picker's leading edge. It is
// a surface of its own rather than a hairline seam because everything drawn on it
// is the same tone as the picker's own fill — a server with no icon yet is a
// ServerDefaultBg disc, and the mark saying which section is open is a
// ChannelSelectedBg fill; on NoticeBg neither exists.
func (p *emojiPicker) buildRail() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SessionCardBg)
	background.CornerRadius = theme.Sizes.ReactionRadius

	column := container.NewStack(background, NewPlainVScroll(p.rail))

	return NewFixedWidthContainer(theme.Sizes.EmojiPickerRailWidth, column)
}

// railButtonFor is the icon that jumps to section i, built the first time that
// section is drawn and kept. Its tap reads where fill last laid the section out
// from the button rather than closing over it: the button outlives the query it
// was first drawn under, and the drawn order moves with the query.
func (p *emojiPicker) railButtonFor(i int) *emojiRailButton {
	if button := p.sectionRails[i]; button != nil {
		return button
	}

	group := p.sections[i].group

	var button *emojiRailButton
	button = newEmojiRailButton(p.deps, group,
		func() { p.jumpTo(button.at) },
		func(over bool) {
			if !over {
				p.tip.Hide()
				return
			}

			p.tip.ShowAbove(group.Title, button)
		})
	p.sectionRails[i] = button

	return button
}

// jumpTo brings one section's caption to the top of the grid. The selection is
// set here rather than left to OnScroll: the scroll clamps at the end of the
// content, so the last section never reaches the top and would never light.
func (p *emojiPicker) jumpTo(index int) {
	if index >= len(p.blocks) {
		return
	}

	p.scroll.ScrollToOffset(fyne.NewPos(0, p.blocks[index].Position().Y))
	p.markSection(index)
}

// markSectionAt follows the reader: the icon marked is the last one whose section
// has started at or above the top of the grid. Read off the laid-out block rather
// than summed from minimums — a wrapping grid answers MinSize with one cell, so
// what a section costs is known only once it has been placed.
func (p *emojiPicker) markSectionAt(offset float32) {
	active := 0
	for i, block := range p.blocks {
		if block.Position().Y <= offset+theme.Sizes.EmojiPickerGap {
			active = i
		}
	}

	p.markSection(active)
}

// markSection repaints the two icons that changed, and nothing else. The rail is
// not rebuilt to move the selection: following the grid's scroll would replace
// every button several times a second, including the one under the pointer —
// which then never hears MouseOut.
func (p *emojiPicker) markSection(index int) {
	if index == p.active || index >= len(p.railButtons) {
		return
	}

	if p.active < len(p.railButtons) {
		p.railButtons[p.active].setSelected(false)
	}
	p.railButtons[index].setSelected(true)
	p.active = index
}

/* Filling the grid */

// fill redraws the grid and the rail for a query. Matching is a case-insensitive
// substring of the folded name, the query folded once here and each candidate
// once at construction.
func (p *emojiPicker) fill(query string) {
	query = strings.ToLower(strings.TrimSpace(query))

	p.found = false
	drawn := 0

	// One widget per emoji, so the same one twice in a pass is one object asked to
	// sit in two cells — which Fyne answers by drawing it in neither. Nothing should
	// offer a duplicate; dropping one keeps that a missing entry, not a hole.
	clear(p.placed)

	var rows, icons []fyne.CanvasObject
	p.blocks, p.railButtons = p.blocks[:0], p.railButtons[:0]

	for i, section := range p.sections {
		cells := make([]fyne.CanvasObject, 0, min(len(section.entries), emojiPickerLimit-drawn))

		for _, entry := range section.entries {
			if drawn == emojiPickerLimit {
				break
			}
			if query != "" && !strings.Contains(entry.fold, query) {
				continue
			}

			value := entry.choice.Value()
			if p.placed[value] {
				continue
			}
			p.placed[value] = true

			if !p.found {
				p.top, p.topIn, p.found = entry.choice, section.group.Title, true
			}
			cells = append(cells, p.cell(entry.choice, section.group.Title))
			drawn++
		}

		if len(cells) == 0 {
			continue
		}

		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.EmojiPickerGap))
		}

		// The button is reused, so where it now points has to be written back: a fresh
		// one knew nothing about a previous query for free.
		button := p.railButtonFor(i)
		button.at = len(p.blocks)

		block := p.blockFor(i, cells)

		p.blocks = append(p.blocks, block)
		p.railButtons = append(p.railButtons, button)
		rows = append(rows, block)
		icons = append(icons, button)
	}

	p.list.Objects = rows
	p.list.Refresh()

	p.rail.Objects = icons
	p.rail.Refresh() // a container re-lays out only when it is told its children moved

	// The top of the grid is what a fill leaves the reader at, so the first icon is
	// marked and every other reused one put back — each a no-op where it was already
	// in that state, which for a narrowing query is nearly all of them.
	p.active = 0
	for i, button := range p.railButtons {
		button.setSelected(i == 0)
	}

	// The content is a different height now, and the scroll clamps an offset against
	// what it last measured — including the one written here.
	p.scroll.SyncContent()
	p.scroll.ScrollToOffset(fyne.Position{})

	// The pointer is in the field, not the grid, so whatever a cell was naming has
	// been reordered out from under it.
	p.over = false
	p.tip.Hide()
	p.restPreview()

	// A pop-up takes its size once, as it is shown, and a narrowed query is a
	// shorter grid: without this the island stands at the height of the set it
	// opened with, a query matching nothing leaving a panel of nothing. It can only
	// shrink — the unfiltered set is what it opened at.
	if p.popUp != nil {
		p.popUp.Resize(p.popUp.MinSize())
	}
}

// blockFor is section i as it is drawn: its caption over its grid, built once and
// pointed at whatever cells the query left it with. The grid wraps at whatever the
// picker's width allows, so the cell size is the only thing deciding how many sit
// on a row. Nothing is refreshed here — the list this block hangs in is, and a
// container's refresh walks its children.
func (p *emojiPicker) blockFor(i int, cells []fyne.CanvasObject) *fyne.Container {
	if p.sectionGrids[i] == nil {
		side := theme.Sizes.EmojiPickerCellSize

		p.sectionGrids[i] = container.NewGridWrap(fyne.NewSize(side, side))
		p.sectionBlocks[i] = VBoxNoSpacing(emojiCaption(p.sections[i].group.Title), p.sectionGrids[i])
	}
	p.sectionGrids[i].Objects = cells

	return p.sectionBlocks[i]
}

// cell returns the widget for one emoji, building it the first time it is shown.
// The group is captured with it: an emoji is drawn under exactly one heading, so
// what the header files it under cannot change between queries.
func (p *emojiPicker) cell(choice EmojiChoice, in string) fyne.CanvasObject {
	value := choice.Value()
	if cell, ok := p.cells[value]; ok {
		return cell
	}

	cell := newEmojiCell(p.deps, choice, func() {
		p.popUp.Hide()
		p.onPick(choice)
	}, func(over bool) { p.setHovered(choice, in, over) })
	p.cells[value] = cell

	return cell
}

// acceptTop takes the first match, which is what Enter in the search field is
// for. Nothing happens on an empty result — there is nothing to have meant.
func (p *emojiPicker) acceptTop() {
	if !p.found {
		return
	}

	choice := p.top
	p.popUp.Hide()
	p.onPick(choice)
}

// showBeside puts the picker under anchor, pulled back inside the canvas where it
// would hang off an edge — a PopUp shows wherever it is put, half off the screen
// included. Both things that open one sit low, so the bottom edge is the usual case.
func (p *emojiPicker) showBeside(anchor fyne.CanvasObject) {
	pos := AnchorBelow(anchor)
	size := p.popUp.Content.MinSize()
	_, area := p.canvas.InteractiveArea()

	if pos.X+size.Width > area.Width {
		pos.X = max(area.Width-size.Width, 0)
	}
	if pos.Y+size.Height > area.Height {
		// Above the anchor rather than merely shoved up, so it does not cover what
		// opened it.
		pos.Y = max(pos.Y-anchor.Size().Height-size.Height, 0)
	}

	p.popUp.ShowAtPosition(pos)

	// The field takes focus, not the pop-up, so the picker can be typed at the moment
	// it opens — the only way past the drawn limit.
	p.canvas.Focus(p.search)
}

// emojiCaption names a group, in the small caps the sidebar's categories are set
// in.
func emojiCaption(title string) fyne.CanvasObject {
	text := newBoldText(strings.ToUpper(title), theme.Colors.CategoryText, theme.Sizes.EmojiPickerCaptionSize)

	return NewInset(NewEllipsisText(text), 0, theme.Sizes.EmojiPickerGap, 0, 0)
}

/* The search field */

// emojiSearch is the field at the foot of the picker, a widget of its own only
// because Enter and Escape have to mean something here: an Entry inside a pop-up
// swallows both, and the pop-up never hears the key that should close it.
type emojiSearch struct {
	widget.Entry

	onAccept func()
	onCancel func()
}

func newEmojiSearch(onChanged func(string), onAccept, onCancel func()) *emojiSearch {
	s := &emojiSearch{onAccept: onAccept, onCancel: onCancel}
	s.ExtendBaseWidget(s)
	s.PlaceHolder = "Search emoji"
	s.OnChanged = onChanged

	return s
}

func (s *emojiSearch) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		s.onAccept()
	case fyne.KeyEscape:
		s.onCancel()
	default:
		s.Entry.TypedKey(key)
	}
}

/* One cell */

// emojiCell is one emoji in the grid: the picture or the character on a square
// that lights under the pointer.
type emojiCell struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject
	onHover    func(bool)
}

var (
	_ fyne.Tappable     = (*emojiCell)(nil)
	_ desktop.Hoverable = (*emojiCell)(nil)
)

func newEmojiCell(deps Deps, choice EmojiChoice, onTap func(), onHover func(bool)) *emojiCell {
	c := &emojiCell{background: canvas.NewRectangle(color.Transparent), onHover: onHover}
	c.background.CornerRadius = theme.Sizes.ReactionRadius

	padding := theme.Sizes.ReactionPaddingV
	c.content = container.NewStack(c.background,
		NewInset(container.NewCenter(newPickerEmoji(deps, choice)), padding, padding, padding, padding))

	c.onTap = onTap
	c.ExtendBaseWidget(c)

	return c
}

func (c *emojiCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *emojiCell) MouseIn(*desktop.MouseEvent) {
	c.background.FillColor = theme.Colors.ReactionHoverBg
	c.background.Refresh()
	c.onHover(true)
}

func (c *emojiCell) MouseOut() {
	c.background.FillColor = color.Transparent
	c.background.Refresh()
	c.onHover(false)
}

/* One rail icon */

// emojiRailButton is one group in the rail: the server's icon, the fill saying
// the grid is showing it, and the bar the settings rail marks an open section
// with. A widget rather than a TappableContainer because the selection has to
// survive the pointer leaving.
type emojiRailButton struct {
	tapBase

	background *canvas.Rectangle
	marker     *canvas.Rectangle
	content    fyne.CanvasObject
	onHover    func(bool)

	// at is where fill last drew this button's section, which is what its tap jumps
	// to. A field rather than a captured value: the button survives the query and
	// the drawn order does not.
	at       int
	selected bool
}

var (
	_ fyne.Tappable     = (*emojiRailButton)(nil)
	_ desktop.Hoverable = (*emojiRailButton)(nil)
)

func newEmojiRailButton(deps Deps, group EmojiGroup, onTap func(), onHover func(bool)) *emojiRailButton {
	b := &emojiRailButton{background: canvas.NewRectangle(color.Transparent), onHover: onHover}
	b.background.CornerRadius = theme.Sizes.SettingsGroupRadius
	b.onTap = onTap

	var markerRow fyne.CanvasObject
	b.marker, markerRow = newSettingsMarker()

	b.content = NewMinHeightContainer(theme.Sizes.EmojiPickerRailRowHeight, container.NewStack(
		b.background, markerRow, container.NewCenter(newRailIcon(deps, group))))
	b.ExtendBaseWidget(b)

	return b
}

// setSelected repaints in place, and not at all when it is already in that state:
// a button is reused across queries, so most of what fill resets has not moved.
func (b *emojiRailButton) setSelected(selected bool) {
	if selected == b.selected {
		return
	}
	b.selected = selected

	b.background.FillColor = color.Transparent
	b.marker.FillColor = color.Transparent

	if selected {
		b.background.FillColor = theme.Colors.ChannelSelectedBg
		b.marker.FillColor = theme.Colors.TextPrimary
	}

	b.background.Refresh()
	b.marker.Refresh()
}

func (b *emojiRailButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

func (b *emojiRailButton) MouseIn(*desktop.MouseEvent) {
	if !b.selected {
		b.background.FillColor = theme.Colors.TappableHoverBg
		b.background.Refresh()
	}

	b.onHover(true)
}

func (b *emojiRailButton) MouseOut() {
	if !b.selected {
		b.background.FillColor = color.Transparent
		b.background.Refresh()
	}

	b.onHover(false)
}

/* Drawing one emoji */

// newRailIcon is what stands for a group in the rail: the server's icon, its
// initial until the picture lands, and a character for the group no server owns.
func newRailIcon(deps Deps, group EmojiGroup) fyne.CanvasObject {
	if group.ServerID == "" {
		return newText(unicodeMark, theme.Colors.TextPrimary, theme.Sizes.EmojiPickerEmojiSize)
	}

	side := theme.Sizes.EmojiPickerRailIconSize
	size := fyne.NewSize(side, side)

	background := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	icon := container.NewStack(background, container.NewCenter(newInitial(group.Title)))
	if group.IconURL != "" {
		deps.Images.LoadIntoContainer(group.IconID, group.IconURL, size, icon, true, background)
	}

	return container.NewGridWrap(size, icon)
}

// newPreviewEmoji draws one emoji at the size the header names it at — the
// rendition a 34-unit cell cannot show, which is what the strip is for.
func newPreviewEmoji(deps Deps, choice EmojiChoice) fyne.CanvasObject {
	side := theme.Sizes.EmojiPickerPreviewSize

	if choice.ID == "" {
		return container.NewCenter(newText(choice.Char, theme.Colors.TextPrimary, side))
	}

	size := fyne.NewSize(side, side)
	frame := container.NewGridWrap(size, canvas.NewRectangle(color.Transparent))
	deps.Emojis.LoadIntoContainer(choice.ID, deps.Store.EmojiURL(choice.ID), size, frame, false, nil)

	return frame
}

// newPickerEmoji draws one emoji at picker size: a picture for a custom one, the
// character itself otherwise. The square is reserved before the request starts,
// so one arriving repaints its own cell rather than moving the grid.
func newPickerEmoji(deps Deps, choice EmojiChoice) fyne.CanvasObject {
	side := theme.Sizes.EmojiPickerEmojiSize

	if choice.ID == "" {
		return newText(choice.Char, theme.Colors.TextPrimary, side)
	}

	// Outlined rather than clear, unlike a chip's: a chip has its own surface behind
	// the emoji, where a grid of transparent squares is an empty panel until the
	// pictures land. The hairline rather than a fill — a filled tile at rest is what
	// a hovered cell looks like.
	placeholder := canvas.NewRectangle(color.Transparent)
	placeholder.CornerRadius = theme.Sizes.ReactionRadius
	Outline(placeholder)

	size := fyne.NewSize(side, side)
	frame := container.NewGridWrap(size, placeholder)
	deps.Emojis.LoadIntoContainer(choice.ID, deps.Store.EmojiURL(choice.ID), size, frame, false, nil)

	return frame
}

// newMentionEmoji draws one emoji as the lead of an autocomplete row, at the size
// the other kinds' avatars and glyphs are drawn at. No outlined placeholder,
// unlike the grid's: the name beside it already says the row is filled.
func newMentionEmoji(deps Deps, choice EmojiChoice) fyne.CanvasObject {
	if choice.ID == "" {
		return newText(choice.Char, theme.Colors.TextPrimary, theme.Sizes.MentionEmojiSize)
	}

	side := theme.Sizes.MentionAvatarSize
	size := fyne.NewSize(side, side)
	frame := container.NewGridWrap(size, canvas.NewRectangle(color.Transparent))
	deps.Emojis.LoadIntoContainer(choice.ID, deps.Store.EmojiURL(choice.ID), size, frame, false, nil)

	return frame
}
