package ui

// The GIF picker: the composer's other pop-up, drawn on the emoji picker's own
// island so the two read as one client. What separates them is that nothing here
// is in hand — every list is a request to a service that allows ten of them in
// ten seconds — so the field settles before it asks, an answer is held for as
// long as the picker is open, and a query already in flight is not asked twice.
//
// A tile at rest is a **still** — image.Decode takes the first frame — and
// plays under the pointer where the service named a GIF rendition to play
// (gifAnimator; the video renditions the service prefers still have no
// player). See docs/known-gaps.md.

import (
	"image"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// gifQueryDelay is how long the field stands still before a query is sent. The
// service's bucket is ten requests in ten seconds and a typed word is a dozen
// keystrokes, so this is the whole of what keeps a search inside it.
const gifQueryDelay = 350 * time.Millisecond

// gifTrendingKey is what the opening page is held under — the empty query, which
// is the one thing nobody can type into the field.
const gifTrendingKey = ""

// What the line above the grid says. It names what is being shown rather than
// repeating the query, which the field under it is already carrying, and is set
// in the small caps every other heading in this client wears.
const (
	gifTrending          = "TRENDING"
	gifResults           = "RESULTS"
	gifSearching         = "SEARCHING..."
	gifNothingFound      = "NOTHING FOUND"
	gifUnreachable       = "GIF SERVICE UNREACHABLE"
	gifSearchPlaceholder = "Search GIFs"
)

// gifPickerColumns is how many columns the grid stands in. Two at the picker's
// width leaves a tile wide enough to recognise a GIF from; three is thumbnails.
const gifPickerColumns = 2

// gifDefaultRatio is the shape a tile takes where the service named none — the
// landscape most GIFs are, so a page of unmeasured ones is not a page of
// portraits that all move once their pictures land.
const gifDefaultRatio = 4.0 / 3.0

// A tile is clamped to these, whatever the picture: a panorama draws as a strip
// nothing can be read off, and one tall column of portrait pushes everything
// beside it out of the viewport.
const (
	gifMinRatio = 0.62
	gifMaxRatio = 2.4
)

// gifReshapeSlack is how far a picture may differ from the shape its tile was
// guessed at before the column is re-laid out under whoever is reading it. A
// tenth is about a row of pixels on a tile this size.
const gifReshapeSlack = 0.1

/* What can be picked */

// GIFChoice is one GIF on offer. PageURL is what picking it answers with: the
// page is what a message carries, and Revolt unfurls that into the embed. The
// preview is a still of it, and is drawn rather than sent.
//
// Its dimensions are what the tile is *shaped* by, so a GIF is drawn as the
// picture it is rather than letterboxed inside a cell. The service leaves them
// out often enough that a tile stands at gifDefaultRatio until the picture lands
// and says otherwise.
type GIFChoice struct {
	ID         string
	PageURL    string
	PreviewURL string

	// AnimatedURL is the smallest rendition that actually moves, played while
	// the pointer is on the tile. Empty where the service named none, and the
	// tile stays a still.
	AnimatedURL string

	PreviewWidth  int
	PreviewHeight int
}

// GIFCategory is a heading GIFs are browsable by, searched for by its own Title.
type GIFCategory struct {
	Title    string
	ImageURL string
}

// GIFSource is where the picker's contents come from. Each is a request, so each
// answers through done on the UI thread — this package fetches nothing itself,
// and the controller decides what an error becomes.
type GIFSource struct {
	Trending   func(done func([]GIFChoice, error))
	Search     func(query string, category bool, done func([]GIFChoice, error))
	Categories func(done func([]GIFCategory, error))
}

/* The picker */

// ShowGIFPicker drops the picker beside anchor and calls onPick with whatever is
// chosen.
func ShowGIFPicker(deps Deps, anchor fyne.CanvasObject, source GIFSource, onPick func(GIFChoice)) {
	c := fyne.CurrentApp().Driver().CanvasForObject(anchor)
	if c == nil {
		return
	}

	newGIFPicker(deps, c, source, onPick).showBeside(anchor)
}

// gifPicker is the pop-up: a line saying what is being shown, the categories as
// chips, the grid, and the field. A plain struct rather than a widget, as the
// emoji picker is — everything it draws is a container.
type gifPicker struct {
	deps   Deps
	source GIFSource
	onPick func(GIFChoice)

	search *gifSearch
	status *canvas.Text
	grid   *fyne.Container
	scroll *ObservableScroll

	// chips are the headings and chipRow the strip holding them, hidden until they
	// arrive — a strip standing empty is a gap between the line and the grid.
	chips   *fyne.Container
	chipRow fyne.CanvasObject

	// results are held per query for as long as the picker is open, which is the
	// honest bound on how long an answer nothing announces can be believed, and
	// loading is the single flight over them: re-typing a query whose first answer
	// is still on its way waits for it rather than sending another.
	results    map[string][]GIFChoice
	loading    map[string]bool
	categories []GIFCategory

	// query is what the grid is drawn for, and fromCategory the one query that was
	// picked from a heading rather than typed — the route is told which, and the
	// field carries the same words either way.
	query        string
	fromCategory string

	// timer is the settling window. A wake that has been overtaken does nothing:
	// it compares the field against what it was armed with.
	timer *time.Timer

	content *fyne.Container
	popUp   *widget.PopUp
	canvas  fyne.Canvas
}

func newGIFPicker(deps Deps, c fyne.Canvas, source GIFSource, onPick func(GIFChoice)) *gifPicker {
	p := &gifPicker{
		deps:    deps,
		source:  source,
		onPick:  onPick,
		results: make(map[string][]GIFChoice),
		loading: make(map[string]bool),
		chips:   HBoxNoSpacing(),
		canvas:  c,
	}

	p.search = newGIFSearch(p.onTyped, p.close)
	p.status = newBoldText("", theme.Colors.CategoryText, theme.Sizes.GIFPickerTitleSize)
	p.grid = container.New(&gifColumns{
		columns: gifPickerColumns,
		gap:     theme.Sizes.EmojiPickerGap,
	})

	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = theme.Sizes.EmojiPickerRadius
	Outline(background)
	Elevate(background)

	gap := theme.Sizes.EmojiPickerGap

	// The chips scroll sideways rather than wrapping: a wrapped run of headings is
	// as tall as the grid under it, and the grid is what the picker is for.
	p.chipRow = NewFixedHeightContainer(theme.Sizes.GIFPickerChipHeight, container.NewHScroll(p.chips))
	p.chipRow.Hide()

	// A fixed viewport rather than the emoji picker's ceiling: the columns are as
	// tall as what is in them and that is not known until an answer lands, so a
	// card measured off this one would open at nothing and grow with every page.
	// The grid is always about to be full, and the line above it is what an empty
	// one says.
	p.scroll = NewPlainVScroll(p.grid)
	viewport := NewFixedHeightContainer(theme.Sizes.GIFPickerMaxHeight, p.scroll)

	body := VBoxNoSpacing(
		p.status, VerticalSpacer(gap),
		p.chipRow, VerticalSpacer(gap),
		viewport, VerticalSpacer(gap),
		p.searchField())

	p.content = container.NewStack(background, NewInset(body, gap, gap, gap, gap))

	p.popUp = widget.NewPopUp(NewFixedWidthContainer(theme.Sizes.GIFPickerWidth, p.content), c)

	p.loadCategories()
	p.show(gifTrendingKey)

	return p
}

// searchField is the client's own field surface, as the emoji picker's is: an
// entry under AppTheme draws no box of its own.
func (p *gifPicker) searchField() fyne.CanvasObject {
	padding := theme.Sizes.EmojiPickerGap

	return NewFixedHeightContainer(theme.Sizes.SettingsInputHeight, container.NewStack(
		newFieldBackground(),
		NewInset(WithCaret(p.search), 0, 0, padding, padding),
	))
}

// showBeside puts the picker under anchor, pulled back inside the canvas where it
// would hang off an edge — a PopUp shows wherever it is put.
func (p *gifPicker) showBeside(anchor fyne.CanvasObject) {
	pos := AnchorBelow(anchor)
	size := p.popUp.Content.MinSize()
	_, area := p.canvas.InteractiveArea()

	if pos.X+size.Width > area.Width {
		pos.X = max(area.Width-size.Width, 0)
	}
	if pos.Y+size.Height > area.Height {
		pos.Y = max(pos.Y-anchor.Size().Height-size.Height, 0)
	}

	p.popUp.ShowAtPosition(pos)
	p.canvas.Focus(p.search)
}

func (p *gifPicker) close() {
	p.popUp.Hide()
}

// open reports whether the picker is still up. Every answer is checked against
// it: a request outlives the pop-up a click elsewhere dismissed, and filling a
// grid nobody can see is a picture fetched for nothing.
func (p *gifPicker) open() bool {
	return p.popUp != nil && p.popUp.Visible()
}

/* Asking */

// onTyped arms the settling window. Emptying the field goes back to trending at
// once — that answer is already held, so there is nothing to wait for.
func (p *gifPicker) onTyped(text string) {
	query := strings.TrimSpace(text)

	if p.timer != nil {
		p.timer.Stop()
	}

	if query == "" {
		p.show(gifTrendingKey)
		return
	}

	p.timer = time.AfterFunc(gifQueryDelay, func() {
		DoOnUI(func() {
			if p.open() && strings.TrimSpace(p.search.Text) == query {
				p.show(query)
			}
		})
	})
}

// show draws the answer to a query, asking for it where it is not in hand. It is
// the single writer of the grid, so every path that changes what is on offer ends
// here rather than filling it in for itself.
func (p *gifPicker) show(query string) {
	p.query = query

	if choices, held := p.results[query]; held {
		p.fill(choices)
		return
	}

	p.setStatus(gifSearching)
	p.grid.Objects = nil
	p.grid.Refresh()

	if p.loading[query] {
		return
	}
	p.loading[query] = true

	done := func(choices []GIFChoice, err error) {
		delete(p.loading, query)
		if err != nil {
			if p.open() && p.query == query {
				p.setStatus(gifUnreachable)
			}

			return
		}

		p.results[query] = choices
		if p.open() && p.query == query {
			p.fill(choices)
		}
	}

	if query == gifTrendingKey {
		p.source.Trending(done)
		return
	}

	p.source.Search(query, query == p.fromCategory, done)
}

// loadCategories asks once, when the picker opens. A failure is silent: the
// chips are a way around the field rather than the picker's contents.
func (p *gifPicker) loadCategories() {
	p.source.Categories(func(categories []GIFCategory, err error) {
		if err != nil || !p.open() {
			return
		}

		p.categories = categories
		p.fillChips()
	})
}

func (p *gifPicker) fillChips() {
	cells := make([]fyne.CanvasObject, 0, len(p.categories))

	for _, category := range p.categories {
		title := category.Title
		cells = append(cells, NewInset(
			newGIFChip(title, func() { p.pickCategory(title) }),
			0, theme.Sizes.EmojiPickerGap, 0, 0))
	}

	p.chips.Objects = cells
	p.chips.Refresh()
	showIf(p.chipRow, len(cells) > 0)

	// The strip appearing is the only thing that changes the card's height, and a
	// pop-up takes its size once, as it is shown.
	Relayout(p.content)
	if p.popUp != nil {
		p.popUp.Resize(p.popUp.MinSize())
	}
}

// pickCategory searches a heading. The field is filled with it so the picker says
// what it is showing and a keystroke edits it rather than starting over — which
// arms the settling window, so the search is run from here and the wake it leaves
// behind finds the field already agreeing with the query.
func (p *gifPicker) pickCategory(title string) {
	p.fromCategory = title
	p.search.SetText(title)
	p.show(title)
}

/* Drawing */

func (p *gifPicker) fill(choices []GIFChoice) {
	if len(choices) == 0 {
		p.setStatus(gifNothingFound)
		p.grid.Objects = nil
		p.grid.Refresh()

		return
	}

	p.setStatus(p.heading())

	cells := make([]fyne.CanvasObject, 0, len(choices))
	for _, choice := range choices {
		cells = append(cells, newGIFTile(p.deps, choice, func() { p.accept(choice) }, p.reshape))
	}

	p.grid.Objects = cells
	p.grid.Refresh()

	// The content is a different height now, and the scroll clamps an offset against
	// what it last measured — including the one written here.
	p.scroll.SyncContent()
	p.scroll.ScrollToOffset(fyne.Position{})
}

// heading names what the grid is showing, the tiles being pictures with nothing
// on them that says which question they answered.
func (p *gifPicker) heading() string {
	if p.query == gifTrendingKey {
		return gifTrending
	}

	return gifResults
}

// reshape re-lays the columns out after a tile learned its own shape. It walks
// the placed children and nothing else — no picture is re-asked for and no widget
// is refreshed — so a page of unmeasured GIFs settling is a layout apiece rather
// than a repaint apiece.
func (p *gifPicker) reshape() {
	if !p.open() {
		return
	}

	Relayout(p.grid)
	p.scroll.SyncContent()
}

func (p *gifPicker) setStatus(text string) {
	if p.status.Text == text {
		return
	}

	p.status.Text = text
	p.status.Refresh()
}

func (p *gifPicker) accept(choice GIFChoice) {
	p.close()
	p.onPick(choice)
}

/* The search field */

// gifSearch is the field, a widget of its own for the reason the emoji picker's
// is: an Entry inside a pop-up swallows Escape, and the pop-up never hears the
// key that should close it. Enter takes nothing — a query is answered by the
// service rather than by a first match, so there is nothing to accept.
type gifSearch struct {
	widget.Entry

	onCancel func()
}

func newGIFSearch(onChanged func(string), onCancel func()) *gifSearch {
	s := &gifSearch{onCancel: onCancel}
	s.ExtendBaseWidget(s)
	s.PlaceHolder = gifSearchPlaceholder
	s.OnChanged = onChanged

	return s
}

func (s *gifSearch) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		s.onCancel()
		return
	}

	s.Entry.TypedKey(key)
}

/* The grid */

// gifColumns is the picker's masonry: fixed columns, each tile as tall as its own
// picture. A GIF is a shape as much as it is a picture — a cell of one size
// letterboxes every portrait one and bands every wide one — and a column that
// takes whatever comes next is what keeps the two edges straight without
// cropping, which Fyne cannot do anyway (it clips nothing).
//
// Each tile is placed in the column standing shortest, so the two ends level out
// on their own rather than being balanced by a pass over the whole page.
type gifColumns struct {
	columns int
	gap     float32

	// width is what the last layout was given. A layout is asked for its minimum
	// with no width at all, and the height of a column depends entirely on one —
	// the same measure-after-layout trap a wrapping grid has.
	width float32
}

// gifShaped is a child that knows what shape it wants to be drawn in. Anything
// else takes the default, so the layout is not a tile's to know about.
type gifShaped interface {
	aspect() float32
}

func (l *gifColumns) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.width = size.Width
	l.place(objects, size.Width, true)
}

func (l *gifColumns) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := l.width
	if width <= 0 {
		width = theme.Sizes.GIFPickerWidth
	}

	return fyne.NewSize(width, l.place(objects, width, false).Height)
}

// place walks the children once, placing them where move says to and reporting
// what the tallest column came to either way.
func (l *gifColumns) place(objects []fyne.CanvasObject, width float32, move bool) fyne.Size {
	if l.columns < 1 || width <= 0 {
		return fyne.Size{}
	}

	cell := (width - l.gap*float32(l.columns-1)) / float32(l.columns)
	heights := make([]float32, l.columns)

	for _, object := range objects {
		if !object.Visible() {
			continue
		}

		column := shortestColumn(heights)
		height := cell / gifAspect(object)

		if move {
			object.Resize(fyne.NewSize(cell, height))
			object.Move(fyne.NewPos(float32(column)*(cell+l.gap), heights[column]))
		}

		heights[column] += height + l.gap
	}

	tallest := float32(0)
	for _, height := range heights {
		tallest = max(tallest, height)
	}

	return fyne.NewSize(width, max(tallest-l.gap, 0))
}

func shortestColumn(heights []float32) int {
	shortest := 0
	for i, height := range heights {
		if height < heights[shortest] {
			shortest = i
		}
	}

	return shortest
}

func gifAspect(object fyne.CanvasObject) float32 {
	if shaped, ok := object.(gifShaped); ok {
		return shaped.aspect()
	}

	return gifDefaultRatio
}

func gifRatioMoved(from, to float32) bool {
	return max(from, to)-min(from, to) > gifReshapeSlack
}

// gifRatio is a picture's shape, clamped to what a tile may be drawn as.
func gifRatio(width, height int) float32 {
	if width <= 0 || height <= 0 {
		return gifDefaultRatio
	}

	return min(max(float32(width)/float32(height), gifMinRatio), gifMaxRatio)
}

/* One heading */

// gifChip is a category, and is the client's chip in everything but its fill:
// ChipBg is the colour this island itself is drawn in, so the pill would be
// invisible on it. It takes the tile's fill instead, which is what makes the
// strip and the grid read as furniture of one card.
type gifChip struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*gifChip)(nil)
	_ desktop.Hoverable = (*gifChip)(nil)
)

func newGIFChip(title string, onTap func()) *gifChip {
	c := &gifChip{background: canvas.NewRectangle(theme.Colors.ComposerBg)}
	c.background.CornerRadius = theme.Sizes.ChipRadius

	label := newBoldText(title, theme.Colors.CategoryText, theme.Sizes.ChipTextSize)
	padV, padH := theme.Sizes.ChipPaddingV, theme.Sizes.ChipPaddingH

	c.content = container.NewStack(c.background,
		NewInset(container.NewCenter(label), padV, padV, padH, padH))
	c.onTap = onTap
	c.ExtendBaseWidget(c)

	return c
}

func (c *gifChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *gifChip) MouseIn(*desktop.MouseEvent) {
	c.background.FillColor = theme.Colors.ReactionHoverBg
	c.background.Refresh()
}

func (c *gifChip) MouseOut() {
	c.background.FillColor = theme.Colors.ComposerBg
	c.background.Refresh()
}

/* One tile */

// gifTile is one GIF in the grid: its still, drawn at the picture's own shape in
// a well that lights under the pointer. The picture is *contained* and the tile
// is sized from the same ratio, so the two agree and there is no letterbox to
// see — except where the clamp moved the tile off the picture's shape, which is
// exactly where a band is the honest drawing. Where the service named no
// dimensions the tile stands at the default and re-shapes once the picture lands,
// which is the one thing here that moves the column under the reader.
type gifTile struct {
	tapBase

	background *canvas.Rectangle

	// rim is stacked *over* the picture: the picture reaches the tile's edge, so an
	// edge drawn underneath it would be covered — the arrangement HoverableStack
	// already has for an attachment.
	rim     *canvas.Rectangle
	content fyne.CanvasObject

	// anim plays the animated rendition while the pointer is on the tile; nil
	// where the service named none.
	anim *gifAnimator

	// ratio is width over height, read by the layout on every pass.
	ratio float32
}

var (
	_ fyne.Tappable     = (*gifTile)(nil)
	_ desktop.Hoverable = (*gifTile)(nil)
	_ gifShaped         = (*gifTile)(nil)
)

func newGIFTile(deps Deps, choice GIFChoice, onTap func(), onReshaped func()) *gifTile {
	t := &gifTile{
		background: canvas.NewRectangle(theme.Colors.ComposerBg),
		rim:        canvas.NewRectangle(color.Transparent),
		ratio:      gifRatio(choice.PreviewWidth, choice.PreviewHeight),
	}
	t.background.CornerRadius = theme.Sizes.ReactionRadius
	t.rim.CornerRadius = theme.Sizes.ReactionRadius
	Outline(t.rim)

	measured := choice.PreviewWidth > 0 && choice.PreviewHeight > 0

	frame := container.NewStack()
	if choice.PreviewURL != "" {
		deps.Images.LoadAsync(imageCacheID(choice.PreviewURL), choice.PreviewURL, false, func(img image.Image) {
			pixels := img.Bounds()

			picture := canvas.NewImageFromImage(img)
			picture.FillMode = canvas.ImageFillContain

			frame.Objects = []fyne.CanvasObject{picture}
			frame.Refresh()

			if measured {
				return
			}

			// Only a tile nothing had measured re-shapes, and only where the picture
			// disagrees with the guess by enough to see.
			if ratio := gifRatio(pixels.Dx(), pixels.Dy()); gifRatioMoved(t.ratio, ratio) {
				t.ratio = ratio
				onReshaped()
			}
		})
	}

	if choice.AnimatedURL != "" {
		t.anim = newGIFAnimator(deps.Images, imageCacheID(choice.AnimatedURL), choice.AnimatedURL, frame)
	}

	padding := theme.Sizes.GIFPickerTilePad
	t.content = container.NewStack(t.background, NewInset(frame, padding, padding, padding, padding), t.rim)
	t.onTap = onTap
	t.ExtendBaseWidget(t)

	return t
}

func (t *gifTile) aspect() float32 { return t.ratio }

func (t *gifTile) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

func (t *gifTile) MouseIn(*desktop.MouseEvent) { t.setHovered(true) }
func (t *gifTile) MouseOut()                   { t.setHovered(false) }

// setHovered lifts the rim and the frame under the picture together: the picture
// covers the well, so a fill alone is three pixels of colour around the edge.
// It is also the tile's play control — hover is the one gesture a GIF moves on.
func (t *gifTile) setHovered(on bool) {
	t.background.FillColor, t.rim.StrokeColor = theme.Colors.ComposerBg, theme.Colors.Outline
	if on {
		t.background.FillColor, t.rim.StrokeColor = theme.Colors.ReactionHoverBg, theme.Colors.AttachmentHoverBorder
	}

	t.background.Refresh()
	t.rim.Refresh()

	t.anim.SetPlaying(on)
}
