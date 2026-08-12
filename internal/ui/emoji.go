package ui

// The emoji picker: one pop-up serving both the composer and a reaction, since
// the two are picking from the same set and only differ in what they do with the
// answer. It is the only surface in the client that draws a server's own emoji
// as a set — everywhere else one is a picture derived from an ID.
//
// Nothing here is fetched. Ready carries the emoji of every server the account is
// in and revoltgo files the create and delete events into State itself, so what
// the controller walks (Store.Emojis) is already the whole set and already
// current; the picker asks once as it opens.

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// emojiPickerLimit is how many cells the grid will draw at once. A server may
// define hundreds and an account may be in many, so the whole set is a page
// nobody reads and a widget per entry nobody sees — the search field is what
// reaches past it.
const emojiPickerLimit = 120

/* What can be picked */

// EmojiChoice is one pickable emoji: a character, or a custom one that is a
// picture and an ID. Which it is decides both how it draws and what it is worth
// to a caller, so the two questions are answered here rather than at each of them.
type EmojiChoice struct {
	ID   string // the custom emoji's ULID; "" for a unicode character
	Name string // what the preview line reads, and the first thing search matches
	Char string // the character itself; "" for a custom emoji

	// Keywords are the other words this answers to, searched and never drawn. A
	// character has no shortcode of its own, so "no" has to reach 👎 without the
	// preview line reading "thumbs down no".
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
// unicode set. An empty group is dropped rather than drawn over nothing.
type EmojiGroup struct {
	Title   string
	Choices []EmojiChoice
}

// UnicodeEmoji is the set that works in every channel a message can be in,
// conversations included, where a server's own emoji do not exist. Each carries a
// name so the search field reaches it: without one the field would filter the
// custom half of the picker and leave this half standing.
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

// emojiSection is a heading and its folded entries — the groups the picker was
// handed, prepared once when it opens.
type emojiSection struct {
	title   string
	entries []emojiEntry
}

// emojiPicker is the pop-up itself, wearing the client's hairline the way
// contextMenu does and for the same reason: nothing in Fyne's own pop-up draws
// one, and a floating surface with no edge reads as part of what is behind it.
//
// It is a plain struct rather than a widget: everything it draws is a container,
// and nothing here answers a pointer or a key that its own children do not.
type emojiPicker struct {
	deps     Deps
	sections []emojiSection
	onPick   func(EmojiChoice)

	search *emojiSearch
	list   *fyne.Container // the sections, replaced wholesale on every query
	empty  *canvas.Text

	// previewBox is the line naming what the pointer is over. The string is kept
	// beside it because the box shortens what it draws to the width it is given, so
	// reading the text object back would compare against whatever last fitted.
	previewBox  *fyne.Container
	previewText string

	// cells memoises one widget per emoji, so narrowing a query reorders objects
	// that already exist rather than rebuilding them — and a custom emoji's picture
	// is asked of the cache once rather than once per keystroke. Built lazily, so a
	// set larger than the limit never costs a widget for what is never shown.
	cells map[string]fyne.CanvasObject

	// top is what Enter picks: the first match of the current query, which is the
	// whole of why the field is worth typing into rather than scrolling past.
	top   EmojiChoice
	found bool

	hovered EmojiChoice
	over    bool

	content fyne.CanvasObject
	popUp   *widget.PopUp
	canvas  fyne.Canvas
}

func newEmojiPicker(deps Deps, c fyne.Canvas, groups []EmojiGroup, onPick func(EmojiChoice)) *emojiPicker {
	p := &emojiPicker{
		deps:     deps,
		sections: foldGroups(groups),
		onPick:   onPick,
		list:     VBoxNoSpacing(),
		empty:    canvas.NewText("No emoji match that.", theme.Colors.TimestampText),
		cells:    make(map[string]fyne.CanvasObject),
	}
	p.empty.TextSize = theme.Sizes.EmojiPickerCaptionSize
	p.empty.Hide()

	preview := canvas.NewText("", theme.Colors.TimestampText)
	preview.TextSize = theme.Sizes.EmojiPickerCaptionSize
	p.previewBox = NewEllipsisText(preview)

	p.search = newEmojiSearch(p.fill, p.acceptTop, func() { p.popUp.Hide() })

	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = fynetheme.Size(fynetheme.SizeNameMenuRadius)
	Outline(background)

	// The scroller cannot be asked how tall it wants to be — container.Scroll
	// reports its own current height as its minimum — so the list is measured and
	// the ceiling applied here.
	viewport := container.New(
		&cappedHeightLayout{content: p.list, max: theme.Sizes.EmojiPickerMaxHeight},
		NewPlainVScroll(p.list))

	gap := theme.Sizes.EmojiPickerGap
	body := VBoxNoSpacing(p.searchField(), VerticalSpacer(gap), p.empty, viewport, p.previewLine())
	p.content = container.NewStack(background, NewInset(body, gap, gap, gap, gap))

	p.fill("")

	p.popUp = widget.NewPopUp(NewFixedWidthContainer(theme.Sizes.EmojiPickerWidth, p.content), c)
	p.canvas = c

	return p
}

// foldGroups lowers every name and its keywords once, when the picker opens. The
// walk is the one the grid is about to make anyway; what it saves is repeating it
// per keystroke over a set that can run to thousands.
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

		sections = append(sections, emojiSection{title: group.Title, entries: entries})
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

// previewLine names whatever the pointer is over, and what Enter would take when
// it is over nothing. It is what gives a cell a name at all: the grid is pictures,
// and one whose picture has not arrived is otherwise an anonymous square.
//
// The ellipsis box is what pins it to one line — it reports the font's height
// whatever it holds, so an empty line is as tall as a full one and a name as long
// as somebody made it cannot widen the pop-up under the pointer.
func (p *emojiPicker) previewLine() fyne.CanvasObject {
	return VBoxNoSpacing(VerticalSpacer(theme.Sizes.EmojiPickerGap), p.previewBox)
}

// setHovered records what the pointer is over. A cell reports leaving with the
// choice it reported entering with, so a stale leave from the cell just left
// cannot blank the name of the one just entered.
func (p *emojiPicker) setHovered(choice EmojiChoice, over bool) {
	if !over && (!p.over || p.hovered.Value() != choice.Value()) {
		return
	}

	p.hovered, p.over = choice, over
	p.showPreview()
}

// showPreview writes the line: what is hovered, or failing that what Enter takes.
func (p *emojiPicker) showPreview() {
	text := ""
	switch {
	case p.over:
		text = previewName(p.hovered)
	case p.found:
		text = previewName(p.top)
	}

	if p.previewText == text {
		return
	}

	p.previewText = text
	SetEllipsisText(p.previewBox, text)
}

// previewName is what one emoji is called. A custom one is written the way a
// message carries it, so the line doubles as what typing it by hand would look
// like; a character has no such form and is named plainly.
func previewName(choice EmojiChoice) string {
	if choice.Name == "" {
		return ""
	}
	if choice.ID != "" {
		return ":" + choice.Name + ":"
	}

	return choice.Name
}

// fill redraws the grid for a query. Matching is a case-insensitive substring of
// the folded name, the query folded once here and each candidate once at
// construction.
func (p *emojiPicker) fill(query string) {
	query = strings.ToLower(strings.TrimSpace(query))

	p.found = false
	drawn := 0

	// One widget stands for one emoji, so the same one twice in a pass would be the
	// same object asked to sit in two cells — which Fyne answers by drawing it in
	// neither. Nothing should offer a duplicate; dropping one is what keeps that
	// from being a hole in the grid rather than a missing entry.
	placed := make(map[string]bool, emojiPickerLimit)

	var rows []fyne.CanvasObject
	for _, section := range p.sections {
		cells := make([]fyne.CanvasObject, 0, min(len(section.entries), emojiPickerLimit-drawn))

		for _, entry := range section.entries {
			if drawn == emojiPickerLimit {
				break
			}
			if query != "" && !strings.Contains(entry.fold, query) {
				continue
			}

			value := entry.choice.Value()
			if placed[value] {
				continue
			}
			placed[value] = true

			if !p.found {
				p.top, p.found = entry.choice, true
			}
			cells = append(cells, p.cell(entry.choice))
			drawn++
		}

		if len(cells) == 0 {
			continue
		}

		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.EmojiPickerGap))
		}
		rows = append(rows, emojiCaption(section.title), p.grid(cells))
	}

	p.list.Objects = rows
	p.list.Refresh()

	if p.found {
		p.empty.Hide()
	} else {
		p.empty.Show()
	}

	// The pointer is in the field, not the grid, so what a query changes is what
	// Enter would take.
	p.over = false
	p.showPreview()
}

// grid wraps the cells at whatever the picker's width allows, so the cell size is
// the only thing deciding how many sit on a row.
func (p *emojiPicker) grid(cells []fyne.CanvasObject) fyne.CanvasObject {
	side := theme.Sizes.EmojiPickerCellSize

	return container.NewGridWrap(fyne.NewSize(side, side), cells...)
}

// cell returns the widget for one emoji, building it the first time it is shown.
func (p *emojiPicker) cell(choice EmojiChoice) fyne.CanvasObject {
	value := choice.Value()
	if cell, ok := p.cells[value]; ok {
		return cell
	}

	cell := newEmojiCell(p.deps, choice, func() {
		p.popUp.Hide()
		p.onPick(choice)
	}, func(over bool) { p.setHovered(choice, over) })
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
// would otherwise hang off an edge — a PopUp shows wherever it is put, half off
// the screen included. Both things that open one sit low in the window, so the
// bottom edge is the one this is usually doing something about.
func (p *emojiPicker) showBeside(anchor fyne.CanvasObject) {
	pos := AnchorBelow(anchor)
	size := p.popUp.Content.MinSize()
	_, area := p.canvas.InteractiveArea()

	if pos.X+size.Width > area.Width {
		pos.X = max(area.Width-size.Width, 0)
	}
	if pos.Y+size.Height > area.Height {
		// Above the anchor rather than merely shoved up, so what opened it is not the
		// thing it covers.
		pos.Y = max(pos.Y-anchor.Size().Height-size.Height, 0)
	}

	p.popUp.ShowAtPosition(pos)

	// The field takes focus rather than the pop-up, so the picker can be typed at
	// the moment it opens — which is the only way past the drawn limit.
	p.canvas.Focus(p.search)
}

// emojiCaption names a group, in the small caps the sidebar's categories are set
// in.
func emojiCaption(title string) fyne.CanvasObject {
	text := canvas.NewText(strings.ToUpper(title), theme.Colors.CategoryText)
	text.TextSize = theme.Sizes.EmojiPickerCaptionSize
	text.TextStyle = fyne.TextStyle{Bold: true}

	return NewInset(NewEllipsisText(text), 0, theme.Sizes.EmojiPickerGap, 0, 0)
}

/* The search field */

// emojiSearch is the field at the top of the picker. It is a widget of its own
// only because Enter and Escape have to mean something here: an Entry inside a
// pop-up otherwise swallows both, and the pop-up behind it never hears the key
// that should close it.
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

// newPickerEmoji draws one emoji at picker size: the picture for a custom one,
// the character itself for everything else.
//
// The square is reserved before the request starts, so a picture arriving repaints
// its own cell rather than moving the grid around it. The unicode half is a plain
// canvas.Text — Fyne falls back to the platform's emoji font for glyphs the
// client's own font has none of, which is every emoji.
func newPickerEmoji(deps Deps, choice EmojiChoice) fyne.CanvasObject {
	side := theme.Sizes.EmojiPickerEmojiSize

	if choice.ID == "" {
		text := canvas.NewText(choice.Char, theme.Colors.TextPrimary)
		text.TextSize = side

		return text
	}

	// The square is outlined rather than left clear, unlike a chip's: a chip has a
	// surface of its own behind the emoji, where a grid of transparent squares is
	// an empty panel until the pictures land — and how many there are is the first
	// thing the picker has to say. It is the hairline rather than a fill because a
	// filled tile at rest is what a hovered cell looks like.
	placeholder := canvas.NewRectangle(color.Transparent)
	placeholder.CornerRadius = theme.Sizes.ReactionRadius
	Outline(placeholder)

	size := fyne.NewSize(side, side)
	frame := container.NewGridWrap(size, placeholder)
	deps.Emojis.LoadIntoContainer(choice.ID, deps.Store.EmojiURL(choice.ID), size, frame, false, nil)

	return frame
}
