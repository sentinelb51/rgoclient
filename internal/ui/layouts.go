package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/ui/theme"
)

/* Spacers and fitting */

// HorizontalSpacer returns a fixed-width transparent gap.
func HorizontalSpacer(width float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(width, 0))

	return r
}

// VerticalSpacer returns a fixed-height transparent gap.
func VerticalSpacer(height float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(0, height))

	return r
}

// fitWithin scales width by height down to fit inside maxW by maxH, preserving
// the aspect ratio; dimensions that already fit come back unchanged. A zero
// input dimension (metadata the server didn't provide) yields a zero size,
// leaving the fallback to the caller.
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

// NewFillRow lays objects left to right with no gaps, the child at fillIndex
// absorbing the leftover width while the rest keep their minimum. Used for the
// flat server | channel | messages | member columns, so the sections sit flush
// against each other and only the message area stretches.
func NewFillRow(fillIndex int, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{horizontal: true, fill: fillIndex}, objects...)
}

// NewFillColumn stacks objects top to bottom with no gaps, the child at
// fillIndex absorbing the leftover height. It backs the message area, where the
// composer's margin has to be exactly what it asks for: a Border charges theme
// padding between its centre and each edge slot, so the dock's gap to the
// messages came out wider than its gap to the window.
func NewFillColumn(fillIndex int, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{fill: fillIndex}, objects...)
}

// Relayout re-runs c's own layout and repaints it, without touching its
// children. Hiding or showing a child does neither on its own, so the vacated
// slot stays reserved until something else forces a layout; Container.Refresh
// would reclaim it, but only by walking every descendant.
func Relayout(c *fyne.Container) {
	if c == nil || c.Layout == nil {
		return
	}

	c.Layout.Layout(c.Objects, c.Size())
	canvas.Refresh(c)
}

// noSpacingLayout arranges visible children edge to edge along one axis, each
// stretched to the container's full extent on the other. The child at fill (-1
// for none) absorbs whatever space the others leave.
type noSpacingLayout struct {
	horizontal bool
	fill       int
}

func (l *noSpacingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var fixed float32
	if l.fill >= 0 {
		for i, child := range objects {
			if child.Visible() && i != l.fill {
				fixed += l.extent(child.MinSize())
			}
		}
	}

	var pos float32
	for i, child := range objects {
		if !child.Visible() {
			continue
		}

		extent := l.extent(child.MinSize())
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
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		m := child.MinSize()
		if l.horizontal {
			w += m.Width
			h = max(h, m.Height)
		} else {
			h += m.Height
			w = max(w, m.Width)
		}
	}

	return fyne.NewSize(w, h)
}

// extent returns the size component along the layout's axis.
func (l *noSpacingLayout) extent(size fyne.Size) float32 {
	if l.horizontal {
		return size.Width
	}

	return size.Height
}

/* Columns */

// columnLayout stacks children in a fixed-width column, each centred
// horizontally and the group pinned to the top. It backs the message avatar
// gutter, so the avatar lines up with the author name rather than drifting to
// the middle of a tall message.
//
// With collapse set it reports zero minimum height, so its child can never make
// the row taller than the content beside it — that is what keeps a grouped
// message's hover timestamp from stretching a one-line row.
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

// overlayLayout positions children relative to the top-right corner, allowing
// them to bleed outside the parent (a negative Y). It reports a zero minimum
// size, so it never affects the parent's layout.
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
// margin on three sides. Body is laid out *past* the card's top edge rather than
// stopping above it: a column that ends above a card ends at a hard cut through
// whatever glyph the viewport landed on, and that cut — not the gap — is what
// reads as the top of a separate bar. Sliding the content under the card instead
// puts the cut behind the card, where the only thing left to see is the shadow
// darkening the content on its way under.
//
// Body stops a corner radius short of the card's bottom edge, since the rounded
// corners would otherwise leave the cut showing in the two notches beside them.
// Pair this with NewDockReserve, or the newest content sits under the card with
// no way to scroll it clear.
func NewFloatingDock(body, card fyne.CanvasObject) *fyne.Container {
	return container.New(&dockLayout{}, body, card)
}

// DockReserve is the room a floating card takes out of the column it hangs over:
// its own height plus the gutter above and below it.
func DockReserve(card fyne.CanvasObject) float32 {
	return card.MinSize().Height + theme.Sizes.ComposerDockMargin*2
}

// NewDockReserve wraps a scroller's content so the bottom of it can still be
// read: it reports its child's height plus DockReserve, so the last message
// comes to rest above the card instead of under it. The card is measured on
// demand rather than pushed in, so one that grows — a reply preview, an
// attachment row, the mention picker — is accounted for without anything having
// to notice that it grew.
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

// dockReserveLayout pads its single child by the height of the card hanging over
// it. The padding is on the far side of the child from the card, in the sense
// that it is the child that shrinks: the container itself is what the scroller
// measures, so the reserve has to be part of the height it reports.
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

// NewFixedWidthContainer pins a column to exactly width whatever its contents ask
// for. The sidebars use it because a vertical scroller reports its content's
// minimum *width* as its own: without this, one long channel or member name
// widens the column and shoves the message area sideways. Contrast
// NewMinWidthContainer, which treats width as a floor and still grows. Content
// wider than the slot is clipped by the scroller, so pair it with NewEllipsisText
// on anything holding user-supplied text.
func NewFixedWidthContainer(width float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: fyne.NewSize(width, 0), pinWidth: true}, objects...)
}

// NewFixedHeightContainer pins a slot to exactly height whatever its contents ask
// for. It is what stops a control that grows on interaction from moving the row
// it sits in: the settings page swaps a slider's value for a text field when it
// is clicked, and an entry is half again as tall as the number it replaces.
func NewFixedHeightContainer(height float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minSizeLayout{min: fyne.NewSize(0, height), pinHeight: true}, objects...)
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

// NewFlow lays children left to right, wrapping onto a new row once the next one
// would pass width, with spacing between them on both axes. It is what a run of
// chips — a profile's roles and badges — is laid out with.
//
// The width is given rather than measured because MinSize is asked for before
// the container is handed a width: a row that wrapped only once it was laid out
// would draw outside the height its parent had already reserved. Callers pass
// the width they are about to give it, which for a card of fixed width is known.
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

// arrange walks the visible children in row order, reporting where each one goes
// and how big it is. Laying out and measuring are the same walk, so the two can
// never disagree about how many rows the children take.
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

// popoverLayout places a single card beside an anchor widget — the profile card
// a click on an avatar opens next to it. The card takes its minimum size and is
// kept wholly on screen: it flips to the anchor's other side rather than run off
// the right edge, and slides along the anchor rather than off the top or bottom.
//
// host is the layer the card is positioned within, which is what the anchor's
// canvas-absolute position is measured against — the same conversion Tooltip
// makes for the same reason.
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

// placeBeside returns where a card of size card goes next to an anchor of size
// anchorSize at anchor, inside a layer of size bounds. It prefers the anchor's
// right, centred on it vertically, and gives way to the edges of the layer: a
// card that cannot fit on the right goes on the left, and one that would hang
// off the top or bottom is pulled back inside.
func placeBeside(anchor fyne.Position, anchorSize, card, bounds fyne.Size) fyne.Position {
	gap, margin := theme.Sizes.PopoverGap, theme.Sizes.PopoverMargin

	x := anchor.X + anchorSize.Width + gap
	if x+card.Width > bounds.Width-margin {
		x = anchor.X - card.Width - gap
	}
	y := anchor.Y + anchorSize.Height/2 - card.Height/2

	return fyne.NewPos(
		clampWithin(x, margin, bounds.Width-card.Width-margin),
		clampWithin(y, margin, bounds.Height-card.Height-margin),
	)
}

// clampWithin holds v between low and high, preferring low when the two cross —
// a card taller than the layer starts at the top rather than being pushed off it.
func clampWithin(v, low, high float32) float32 {
	return max(low, min(v, high))
}

/* Padding */

// NewInset wraps a single object in exactly the padding it is given. Neither of
// Fyne's ready-made options does that: container.NewPadded applies the uniform
// theme padding, and container.NewBorder inserts theme padding of its own between
// the edge slots and the centre. The composer needs exact insets, because its
// card padding has to compose predictably with the padding the entry already
// draws inside itself.
func NewInset(obj fyne.CanvasObject, top, bottom, left, right float32) *fyne.Container {
	return container.New(&insetLayout{top: top, bottom: bottom, left: left, right: right}, obj)
}

// newFlushContainer wraps a single widget that carries Fyne's built-in inner
// padding (notably widget.RichText) so its content sits flush against the
// container's top-left origin instead of being inset. That lets the message body
// line up with the author name, a plain canvas.Text with no padding of its own.
func newFlushContainer(obj fyne.CanvasObject) *fyne.Container {
	inset := -fynetheme.InnerPadding()
	return NewInset(obj, inset, inset, inset, inset)
}

// insetLayout pads its single child by a per-side amount. A negative inset
// over-sizes the child and offsets it instead, so the child's content aligns with
// the container origin; what it then draws outside the container bounds is its
// own transparent padding, so nothing visible spills onto neighbours.
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
