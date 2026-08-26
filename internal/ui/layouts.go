package ui

import (
	"cmp"
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/ui/theme"
)

/* Spacers and fitting */

// HorizontalSpacer is a fixed-width transparent gap.
func HorizontalSpacer(width float32) fyne.CanvasObject {
	return sizedRect(color.Transparent, width, 0)
}

// VerticalSpacer is a fixed-height transparent gap.
func VerticalSpacer(height float32) fyne.CanvasObject {
	return sizedRect(color.Transparent, 0, height)
}

// sizedRect is a rectangle with a floor on one axis and none on the other, which
// is what every spacer and hairline in this package is.
func sizedRect(fill color.Color, width, height float32) *canvas.Rectangle {
	r := canvas.NewRectangle(fill)
	r.SetMinSize(fyne.NewSize(width, height))

	return r
}

// fitWithin scales width by height down into maxW by maxH, preserving the aspect
// ratio; what already fits comes back unchanged. A zero input — metadata the
// server did not provide — yields a zero size, leaving the fallback to the caller.
func fitWithin(width, height int, maxW, maxH float32) fyne.Size {
	w, h := float32(width), float32(height)
	if w > maxW {
		h *= maxW / w
		w = maxW
	}
	if h > maxH {
		w *= maxH / h
		h = maxH
	}

	return fyne.NewSize(w, h)
}

/* Linear */

// VBoxNoSpacing stacks objects top to bottom with no gap between them.
func VBoxNoSpacing(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{fill: -1}, objects...)
}

// HBoxNoSpacing lays objects left to right with no gap between them.
func HBoxNoSpacing(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{horizontal: true, fill: -1}, objects...)
}

// NewGapColumn stacks objects top to bottom with gap between the *visible* ones.
// The composer's reply cards are the caller: they come and go, and a hidden one
// must cost neither its height nor its gap — which a VBox of spacers cannot say.
func NewGapColumn(gap float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{fill: -1, gap: gap}, objects...)
}

// NewGapRow lays objects left to right with gap between the *visible* ones. The
// call island is the caller: its two halves and the parts inside them come and
// go, and a hidden one must cost neither its width nor its gap.
func NewGapRow(gap float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{horizontal: true, fill: -1, gap: gap}, objects...)
}

// NewGapBlock is the same with gap above and below the block as well, and
// nothing at all — not even the gap — while every child is hidden. The composer
// dock's optional rows are the caller: the card's own padding is sized for the
// entry, which brings InnerPadding of its own, where a reply card or an
// attachment brings none and would otherwise sit against the card's top edge.
func NewGapBlock(gap float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{fill: -1, gap: gap, edges: true}, objects...)
}

/* Virtualised lists */

// slot is where one mounted child of a virtualised list goes. Index-aligned with
// the container's objects, not with the model behind them.
type slot struct{ top, height float32 }

// slotLayout places mounted children at the absolute position their slot names
// and reports the whole model's height. Both virtualised lists use it — the
// member sidebar, whose rows are a fixed height, and the message column, whose
// are not and which passes a measure hook.
//
// MinSize is O(1) and **must stay so**: container.Scroll asks its content for a
// minimum on every offset write, so a walk here would put the cost of the list
// back on the scroll path. The width is zero because a vertical scroll takes its
// content's minimum width as its own.
type slotLayout struct {
	// measure runs before placement for a list whose heights are only known once
	// the children exist. Nil for one whose are not.
	measure func(objects []fyne.CanvasObject, width float32)

	slots []slot
	total float32
}

func (l *slotLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if l.measure != nil {
		l.measure(objects, size.Width)
	}

	for i, child := range objects {
		if i >= len(l.slots) {
			return
		}

		child.Resize(fyne.NewSize(size.Width, l.slots[i].height))
		child.Move(fyne.NewPos(0, l.slots[i].top))
	}
}

func (l *slotLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, l.total)
}

// stackSpaced stacks rows with one gap between them and none at the ends. A
// NewGapColumn charges its gap around the block as well, and a VBox's spacing is
// the theme's; this is the arrangement a message's attachments and embeds want.
func stackSpaced(gap float32, rows ...fyne.CanvasObject) *fyne.Container {
	spaced := make([]fyne.CanvasObject, 0, max(len(rows)*2-1, 0))
	for i, row := range rows {
		if i > 0 {
			spaced = append(spaced, VerticalSpacer(gap))
		}
		spaced = append(spaced, row)
	}

	return container.NewVBox(spaced...)
}

// NewWrapColumn stacks objects top to bottom, each given the column's full width
// *before* it is measured. A wrapping widget answers MinSize with whatever width
// it was last laid out at, so a column that measured first — VBoxNoSpacing does —
// sizes a text row to the many lines it wraps into at no width at all. The body of
// a message carrying a code block is that column.
func NewWrapColumn(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&wrapColumnLayout{}, objects...)
}

// wrapColumnLayout measures each child at the width it will be drawn at, which
// takes a resize per child before the heights are known.
type wrapColumnLayout struct{}

func (l *wrapColumnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var y float32
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		child.Resize(fyne.NewSize(size.Width, child.MinSize().Height))

		height := child.MinSize().Height
		child.Resize(fyne.NewSize(size.Width, height))
		child.Move(fyne.NewPos(0, y))

		y += height
	}
}

func (l *wrapColumnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var min fyne.Size
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		size := child.MinSize()
		min.Width = max(min.Width, size.Width)
		min.Height += size.Height
	}

	return min
}

// NewFillRow lays objects left to right with no gaps, the child at fillIndex
// absorbing the leftover width while the rest keep their minimum. It backs the
// flat server | channel | messages | member row.
func NewFillRow(fillIndex int, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{horizontal: true, fill: fillIndex}, objects...)
}

// NewFillColumn is the same top to bottom. It backs the message area, where the
// composer's margin has to be exactly what it asks for: a Border charges theme
// padding between its centre and each edge slot, so the dock's gap to the
// messages came out wider than its gap to the window.
func NewFillColumn(fillIndex int, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{fill: fillIndex}, objects...)
}

// Relayout re-runs c's own layout and repaints it without touching its children.
// Hiding or showing a child does neither, so the vacated slot stays reserved
// until something forces a layout — Container.Refresh reclaims it, but only by
// walking every descendant.
func Relayout(c *fyne.Container) {
	if c == nil || c.Layout == nil {
		return
	}

	c.Layout.Layout(c.Objects, c.Size())
	canvas.Refresh(c)
}

// noSpacingLayout arranges visible children edge to edge along one axis, each
// stretched to the container's full extent on the other. The child at fill (-1
// for none) absorbs whatever space the others leave. A non-zero gap goes between
// the *visible* children only, so a row that hides itself takes its gap with it.
type noSpacingLayout struct {
	horizontal bool
	fill       int
	gap        float32
	edges      bool

	// extents holds each child's measurement for the length of one pass, so a fill
	// layout measures each child once rather than twice. Reused, UI-thread only.
	extents []float32
}

func (l *noSpacingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.extents = slices.Grow(l.extents[:0], len(objects))

	var fixed float32
	var shown int
	for i, child := range objects {
		var extent float32
		if child.Visible() {
			shown++
			extent = l.extent(child.MinSize())
			if i != l.fill {
				fixed += extent
			}
		}

		l.extents = append(l.extents, extent)
	}
	fixed += l.gaps(shown)

	var pos float32
	first := true
	for i, child := range objects {
		if !child.Visible() {
			continue
		}

		if !first || l.edges {
			pos += l.gap
		}
		first = false

		extent := l.extents[i]
		if i == l.fill {
			extent = max(l.extent(size)-fixed, 0)
		}

		if l.horizontal {
			child.Resize(fyne.NewSize(extent, size.Height))
			child.Move(fyne.NewPos(pos, 0))
		} else {
			child.Resize(fyne.NewSize(size.Width, extent))
			child.Move(fyne.NewPos(0, pos))
		}
		pos += extent
	}
}

func (l *noSpacingLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	var shown int
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		shown++
		m := child.MinSize()
		if l.horizontal {
			w += m.Width
			h = max(h, m.Height)
		} else {
			h += m.Height
			w = max(w, m.Width)
		}
	}

	if l.horizontal {
		w += l.gaps(shown)
	} else {
		h += l.gaps(shown)
	}

	return fyne.NewSize(w, h)
}

// gaps is what shown children cost in gaps: between them, and around the block
// as well when the layout pads its edges. Nothing is shown, nothing is charged.
func (l *noSpacingLayout) gaps(shown int) float32 {
	if shown == 0 {
		return 0
	}
	if l.edges {
		return l.gap * float32(shown+1)
	}

	return l.gap * float32(shown-1)
}

// extent returns the size component along the layout's axis.
func (l *noSpacingLayout) extent(size fyne.Size) float32 {
	if l.horizontal {
		return size.Width
	}

	return size.Height
}

/* Columns */

// columnLayout stacks children in a fixed-width column, centred and pinned to the
// top. It backs the avatar gutter, so the avatar lines up with the author name
// rather than drifting to the middle of a tall message. With collapse it reports
// zero minimum height, so a grouped message's hover timestamp cannot stretch a
// one-line row.
type columnLayout struct {
	width     float32
	topOffset float32
	collapse  bool
}

func (l *columnLayout) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	y := l.topOffset
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		m := child.MinSize()
		child.Resize(m)
		child.Move(fyne.NewPos(l.width/2-m.Width/2, y))
		y += m.Height
	}
}

func (l *columnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if l.collapse {
		return fyne.NewSize(l.width, 0)
	}

	var h float32
	for _, child := range objects {
		if child.Visible() {
			h += child.MinSize().Height
		}
	}

	return fyne.NewSize(l.width, h)
}

/* Overlays */

// NewLayer wraps what is stacked over the main row — the tooltip, the notice
// stack, the settings page — so it fills the window and asks nothing of it. Fyne
// grows a window to its content's minimum the frame it outgrows it and never
// gives the room back, so a layer reporting what it holds would resize the window
// whenever a long name was hovered or a notice pushed.
func NewLayer(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&layerLayout{}, objects...)
}

// layerLayout fills the layer with each child and reports no minimum at all.
type layerLayout struct{}

func (l *layerLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		child.Resize(size)
		child.Move(fyne.Position{})
	}
}

func (l *layerLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

// overlayLayout positions children against the top-right corner, letting them
// bleed outside the parent (a negative Y). Zero minimum, so it never affects the
// parent's layout.
type overlayLayout struct {
	yOffset     float32
	rightOffset float32
}

func (l *overlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		m := child.MinSize()
		child.Resize(m)
		child.Move(fyne.NewPos(size.Width-m.Width-l.rightOffset, l.yOffset))
	}
}

func (l *overlayLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

/* Docking */

// NewFloatingDock hangs card over the bottom of body, inset by the composer's
// margin on three sides. Body is laid out *past* the card's top edge: a column
// ending above a card ends at a hard cut through whatever glyph the viewport
// landed on, and that cut — not the gap — is what reads as the top of a separate
// bar. Sliding the content under puts the cut behind the card.
//
// Body stops a corner radius short of the card's bottom edge, or the rounded
// corners leave the cut showing in the two notches beside them. Pair with
// NewDockReserve, or the newest content sits under the card unreachable.
func NewFloatingDock(body, card fyne.CanvasObject) *fyne.Container {
	return container.New(&dockLayout{}, body, card)
}

// DockReserve is the room a floating card takes out of the column it hangs over:
// the part standing above where the column already stops, plus the gutter the
// last message rests in. The column runs to a corner radius short of the card's
// *bottom* edge, so the card obscures its own height less that radius — reserving
// the full height and a gutter at each end counted the overlap twice. Whatever
// rides on top of the card is part of the height measured here.
func DockReserve(card fyne.CanvasObject) float32 {
	margin, radius := theme.Sizes.ComposerDockMargin, theme.Sizes.ComposerRadius

	return max(card.MinSize().Height+margin-radius, 0)
}

// NewDockReserve wraps a scroller's content so its bottom stays readable: it
// reports the child's height plus DockReserve. The card is measured on demand, so
// one that grows — a reply preview, an attachment row, the mention picker — is
// accounted for without anything having to notice.
func NewDockReserve(content, card fyne.CanvasObject) *fyne.Container {
	return container.New(&dockReserveLayout{card: card}, content)
}

// dockLayout places the card of NewFloatingDock; objects are body then card.
type dockLayout struct{}

func (l *dockLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	body, card := objects[0], objects[1]
	margin := theme.Sizes.ComposerDockMargin

	height := card.MinSize().Height
	card.Resize(fyne.NewSize(max(size.Width-margin*2, 0), height))
	card.Move(fyne.NewPos(margin, size.Height-margin-height))

	body.Resize(fyne.NewSize(size.Width, max(size.Height-margin-theme.Sizes.ComposerRadius, 0)))
	body.Move(fyne.Position{})
}

func (l *dockLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	margin := theme.Sizes.ComposerDockMargin

	m := objects[0].MinSize()
	card := objects[1].MinSize()

	return m.Max(fyne.NewSize(card.Width+margin*2, card.Height+margin*2))
}

// dockReserveLayout shrinks its child by the card's height while reporting the
// full one: the container is what the scroller measures, so the reserve has to be
// part of the height it reports.
type dockReserveLayout struct{ card fyne.CanvasObject }

func (l *dockReserveLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	reserve := DockReserve(l.card)
	for _, child := range objects {
		child.Resize(fyne.NewSize(size.Width, max(size.Height-reserve, 0)))
		child.Move(fyne.Position{})
	}
}

func (l *dockReserveLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, child := range objects {
		m = m.Max(child.MinSize())
	}
	m.Height += DockReserve(l.card)

	return m
}

/* Ceilings */

// cappedHeightLayout hands its children the room it is given and reports the
// content's minimum height up to a ceiling, past which the scroller takes over.
// The *content* is measured rather than the child, the child being that scroller
// and a scroller having no opinion about its height. The ceiling is a field
// rather than a theme lookup: two surfaces mount one of these.
type cappedHeightLayout struct {
	content fyne.CanvasObject
	max     float32
}

func (l *cappedHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.Position{})
	}
}

func (l *cappedHeightLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	wanted := l.content.MinSize()

	return fyne.NewSize(wanted.Width, min(wanted.Height, l.max))
}

// cappedWidthLayout centres its child at up to max and hands it everything it has
// below that, reporting no width of its own. It is what stands in for the
// settings page's fixed-width column on a surface that has to shrink: a row is a
// name at one end and its buttons at the other, and across a maximised window the
// two lose each other — but the message area is also narrower than that ceiling
// on a small window, where a fixed width would be clipped instead.
type cappedWidthLayout struct{ max float32 }

func (l *cappedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := min(size.Width, l.max)

	for _, child := range objects {
		child.Resize(fyne.NewSize(width, size.Height))
		child.Move(fyne.NewPos((size.Width-width)/2, 0))
	}
}

// MinSize reports the content's height and no width at all: the ceiling is what
// the column is *given*, not what it demands, and a minimum here would put the
// whole page into the window's own.
func (l *cappedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	for _, child := range objects {
		height = max(height, child.MinSize().Height)
	}

	return fyne.NewSize(0, height)
}

/* Minimum size */

// NewMinWidthContainer pins a minimum width, letting height follow the content —
// it gives a modal card a stable width without stopping it growing to fit.
func NewMinWidthContainer(width float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: fyne.NewSize(width, 0)}, objects...)
}

// NewMinHeightContainer pins a minimum height, letting width follow the content.
func NewMinHeightContainer(height float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: fyne.NewSize(0, height)}, objects...)
}

// NewFixedWidthContainer pins a column to exactly width. The sidebars use it
// because a vertical scroller reports its content's minimum *width* as its own,
// so one long name would shove the message area sideways. Contrast
// NewMinWidthContainer, whose width is a floor. Content wider than the slot is
// clipped, so pair it with NewEllipsisText on anything user-supplied.
func NewFixedWidthContainer(width float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: fyne.NewSize(width, 0), pinWidth: true}, objects...)
}

// NewFixedHeightContainer pins a slot to exactly height, which is what stops a
// control that grows on interaction from moving its row: the settings page swaps
// a slider's value for an entry half again as tall.
func NewFixedHeightContainer(height float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: fyne.NewSize(0, height), pinHeight: true}, objects...)
}

// NewFixedSizeContainer pins both axes while still handing its children whatever
// room it is given. It keeps the message column out of the window's minimum — see
// NewLayer for what that costs otherwise.
func NewFixedSizeContainer(size fyne.Size, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: size, pinWidth: true, pinHeight: true}, objects...)
}

// minSizeLayout stretches every child to fill the container and reports a
// minimum size that is at least min on each axis. pinWidth and pinHeight make
// that axis a ceiling too, so the container reports exactly the given extent.
type minSizeLayout struct {
	min       fyne.Size
	pinWidth  bool
	pinHeight bool
}

func (l *minSizeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.Position{})
	}
}

func (l *minSizeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	m := l.min
	for _, child := range objects {
		c := child.MinSize()
		m.Width = max(m.Width, c.Width)
		m.Height = max(m.Height, c.Height)
	}
	if l.pinWidth {
		m.Width = l.min.Width
	}
	if l.pinHeight {
		m.Height = l.min.Height
	}

	return m
}

/* Flow */

// NewFlow lays children left to right, wrapping once the next would pass width —
// what a run of chips is laid out with. The width is given rather than measured
// because MinSize is asked before the container has one: a row that wrapped only
// on layout would draw outside the height its parent had already reserved.
func NewFlow(width, spacing float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&flowLayout{width: width, spacing: spacing}, objects...)
}

type flowLayout struct {
	width   float32
	spacing float32
}

func (l *flowLayout) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	l.arrange(objects, func(child fyne.CanvasObject, pos fyne.Position, size fyne.Size) {
		child.Resize(size)
		child.Move(pos)
	})
}

func (l *flowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var width, height float32
	l.arrange(objects, func(_ fyne.CanvasObject, pos fyne.Position, size fyne.Size) {
		width = max(width, pos.X+size.Width)
		height = max(height, pos.Y+size.Height)
	})

	return fyne.NewSize(width, height)
}

// arrange walks the visible children in row order, reporting where each goes.
// Laying out and measuring are the same walk, so the two cannot disagree about
// how many rows the children take.
func (l *flowLayout) arrange(objects []fyne.CanvasObject, place func(fyne.CanvasObject, fyne.Position, fyne.Size)) {
	var x, y, rowHeight float32

	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		size := child.MinSize()
		if x > 0 && x+size.Width > l.width {
			x, y = 0, y+rowHeight+l.spacing
			rowHeight = 0
		}

		place(child, fyne.NewPos(x, y), size)
		x += size.Width + l.spacing
		rowHeight = max(rowHeight, size.Height)
	}
}

/* Anchored placement */

// popoverLayout places a card beside an anchor widget and keeps it wholly on
// screen — see placeBeside. host is the layer the card is positioned within,
// which the anchor's canvas-absolute position is measured against, the same
// conversion Tooltip makes.
type popoverLayout struct {
	anchor fyne.CanvasObject
	host   fyne.CanvasObject
}

func (l *popoverLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	driver := fyne.CurrentApp().Driver()
	origin := driver.AbsolutePositionForObject(l.host)
	anchor := driver.AbsolutePositionForObject(l.anchor).Subtract(origin)

	for _, child := range objects {
		card := child.MinSize()
		child.Resize(card)
		child.Move(placeBeside(anchor, l.anchor.Size(), card, size))
	}
}

func (l *popoverLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, child := range objects {
		m = m.Max(child.MinSize())
	}

	return m
}

// placeBeside is where a card goes next to an anchor inside a layer of size
// bounds. It prefers the anchor's right, centred vertically, and gives way to the
// layer's edges: no room on the right puts it on the left, and an overhang at top
// or bottom is pulled back inside.
func placeBeside(anchor fyne.Position, anchorSize, card, bounds fyne.Size) fyne.Position {
	gap, margin := theme.Sizes.PopoverGap, theme.Sizes.PopoverMargin

	x := anchor.X + anchorSize.Width + gap
	if x+card.Width > bounds.Width-margin {
		x = anchor.X - card.Width - gap
	}
	y := anchor.Y + anchorSize.Height/2 - card.Height/2

	return fyne.NewPos(
		clamp(x, margin, bounds.Width-card.Width-margin),
		clamp(y, margin, bounds.Height-card.Height-margin),
	)
}

// keepInside pulls a pop-up back inside the canvas where it would otherwise hang
// off the right or bottom edge. Both pop-ups the client draws itself carry it —
// widget.PopUp does nothing of the kind, and only NewPopUpMenu did.
func keepInside(pos fyne.Position, size fyne.Size, c fyne.Canvas) fyne.Position {
	_, area := c.InteractiveArea()

	if pos.X+size.Width > area.Width {
		pos.X = max(area.Width-size.Width, 0)
	}
	if pos.Y+size.Height > area.Height {
		pos.Y = max(area.Height-size.Height, 0)
	}

	return pos
}

// clamp holds v between low and high, preferring low when the two cross — a card
// taller than the layer starts at the top rather than being pushed off it.
// Generic because a layout works in pixels and a settings control in float64s.
func clamp[T cmp.Ordered](v, low, high T) T {
	return max(low, min(v, high))
}

/* Padding */

// NewInset wraps one object in exactly the padding it is given. Neither Fyne
// option does: NewPadded applies the uniform theme padding, NewBorder inserts its
// own between edge slots and centre. The composer needs exact insets, its card
// padding having to compose with what the entry already draws inside itself.
func NewInset(obj fyne.CanvasObject, top, bottom, left, right float32) *fyne.Container {
	return container.New(&insetLayout{top: top, bottom: bottom, left: left, right: right}, obj)
}

// newFlushContainer cancels Fyne's built-in inner padding — widget.RichText's,
// notably — so the content sits flush against the origin. That is what lines the
// message body up with the author name, a canvas.Text with no padding at all.
func newFlushContainer(obj fyne.CanvasObject) *fyne.Container {
	inset := -fynetheme.InnerPadding()
	return NewInset(obj, inset, inset, inset, inset)
}

// insetLayout pads its single child per side. A negative inset over-sizes and
// offsets the child instead, so its content aligns with the container origin;
// what spills outside is the child's own transparent padding.
type insetLayout struct{ top, bottom, left, right float32 }

func (l *insetLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		child.Resize(fyne.NewSize(
			max(size.Width-l.left-l.right, 0),
			max(size.Height-l.top-l.bottom, 0),
		))
		child.Move(fyne.NewPos(l.left, l.top))
	}
}

func (l *insetLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		m := child.MinSize()
		w = max(w, m.Width)
		h = max(h, m.Height)
	}

	return fyne.NewSize(max(w+l.left+l.right, 0), max(h+l.top+l.bottom, 0))
}
