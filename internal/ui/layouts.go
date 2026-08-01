package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
)

// FitWithin scales width×height down to fit inside maxW×maxH, preserving the
// aspect ratio; dimensions that already fit come back unchanged. A zero input
// dimension (metadata the server didn't provide) yields a zero size, leaving the
// fallback to the caller.
func FitWithin(width, height int, maxW, maxH float32) fyne.Size {
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

// VBoxNoSpacing stacks objects top to bottom with no gap between them.
func VBoxNoSpacing(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{horizontal: false}, objects...)
}

// HBoxNoSpacing lays objects left to right with no gap between them.
func HBoxNoSpacing(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&noSpacingLayout{horizontal: true}, objects...)
}

// noSpacingLayout arranges visible children edge to edge along one axis.
type noSpacingLayout struct{ horizontal bool }

func (l *noSpacingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var pos float32
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		m := child.MinSize()
		if l.horizontal {
			child.Resize(fyne.NewSize(m.Width, size.Height))
			child.Move(fyne.NewPos(pos, 0))
			pos += m.Width
		} else {
			child.Resize(fyne.NewSize(size.Width, m.Height))
			child.Move(fyne.NewPos(0, pos))
			pos += m.Height
		}
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

// RowLayout lays children left to right with no gaps: the child at FillIndex
// expands to fill the leftover width while every other child keeps its minimum
// width. All children span the full height. Used for the flat
// server | channel | messages | members columns so the sections sit flush
// against each other and only the message area stretches.
type RowLayout struct{ FillIndex int }

func (l *RowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var fixed float32
	for i, child := range objects {
		if !child.Visible() || i == l.FillIndex {
			continue
		}
		fixed += child.MinSize().Width
	}

	var x float32
	for i, child := range objects {
		if !child.Visible() {
			continue
		}
		w := child.MinSize().Width
		if i == l.FillIndex {
			w = max(size.Width-fixed, 0)
		}
		child.Resize(fyne.NewSize(w, size.Height))
		child.Move(fyne.NewPos(x, 0))
		x += w
	}
}

func (l *RowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		m := child.MinSize()
		w += m.Width
		h = max(h, m.Height)
	}
	return fyne.NewSize(w, h)
}

// FixedWidthColumnLayout stacks children in a fixed-width column, centering each
// horizontally. The group is vertically centered by default, or pinned to the
// top when TopAlign is set (used for the message avatar gutter so the avatar
// lines up with the author name rather than drifting to the middle of tall
// messages).
type FixedWidthColumnLayout struct {
	Width    float32
	TopAlign bool
}

func (l *FixedWidthColumnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var total float32
	for _, child := range objects {
		if child.Visible() {
			total += child.MinSize().Height
		}
	}

	y := max((size.Height-total)/2, 0)
	if l.TopAlign {
		y = 0
	}
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		m := child.MinSize()
		child.Resize(m)
		child.Move(fyne.NewPos(l.Width/2-m.Width/2, y))
		y += m.Height
	}
}

func (l *FixedWidthColumnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, child := range objects {
		if child.Visible() {
			h += child.MinSize().Height
		}
	}
	return fyne.NewSize(l.Width, h)
}

// GutterLayout positions a single child at the top of a fixed-width column,
// centered horizontally, while reporting zero minimum height. Unlike
// FixedWidthColumnLayout it never lets its child make the row taller than the
// content beside it — used for the grouped message's hover timestamp so a
// continuation row is exactly as tall as its one line of text.
type GutterLayout struct {
	Width     float32
	TopOffset float32
}

func (l *GutterLayout) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		m := child.MinSize()
		child.Resize(m)
		child.Move(fyne.NewPos(l.Width/2-m.Width/2, l.TopOffset))
	}
}

func (l *GutterLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(l.Width, 0)
}

// OverlayLayout positions children relative to the top-right corner, allowing
// them to bleed outside the parent (e.g. negative Y). It reports a zero minimum
// size so it never affects the parent's layout.
type OverlayLayout struct {
	YOffset     float32
	RightOffset float32
}

func (l *OverlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		m := child.MinSize()
		child.Resize(m)
		child.Move(fyne.NewPos(size.Width-m.Width-l.RightOffset, l.YOffset))
	}
}

func (l *OverlayLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, 0)
}

// NewFlushContainer wraps a single widget that carries Fyne's built-in inner
// padding (notably widget.RichText) so its content sits flush against the
// container's top-left origin instead of being inset. This lets the message body
// text line up with the author name — a plain canvas.Text that has no padding —
// both horizontally and as tightly as a single newline vertically.
func NewFlushContainer(obj fyne.CanvasObject) *fyne.Container {
	return container.New(&stripPaddingLayout{inset: fynetheme.InnerPadding()}, obj)
}

// stripPaddingLayout neutralises a uniform inset on its single child by
// over-sizing the child and offsetting it by the inset, so the child's content
// (drawn inside that inset) aligns with the container origin. The over-hanging
// region the child draws outside the container bounds is its transparent padding,
// so nothing visible spills onto neighbouring widgets.
type stripPaddingLayout struct{ inset float32 }

func (l *stripPaddingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		child.Resize(fyne.NewSize(size.Width+2*l.inset, size.Height+2*l.inset))
		child.Move(fyne.NewPos(-l.inset, -l.inset))
	}
}

func (l *stripPaddingLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		m := child.MinSize()
		w = max(w, m.Width)
		h = max(h, m.Height)
	}
	return fyne.NewSize(max(w-2*l.inset, 0), max(h-2*l.inset, 0))
}

// NewInset wraps a single object in exactly the padding it is given. Neither of
// Fyne's ready-made options does that: container.NewPadded applies the uniform
// theme padding, and container.NewBorder inserts theme padding of its own
// between the edge slots and the centre. The composer needs exact insets,
// because its card padding has to compose predictably with the padding the
// entry already draws inside itself.
func NewInset(obj fyne.CanvasObject, top, bottom, left, right float32) *fyne.Container {
	return container.New(&insetLayout{top: top, bottom: bottom, left: left, right: right}, obj)
}

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
	return fyne.NewSize(w+l.left+l.right, h+l.top+l.bottom)
}

// NewMinHeightContainer wraps objects in a container with a minimum height. Its
// children are stretched to fill the available space.
func NewMinHeightContainer(height float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minHeightLayout{height: height}, objects...)
}

// NewMinWidthContainer wraps objects in a container with a minimum width, its
// children stretched to fill the available space. The horizontal counterpart of
// NewMinHeightContainer: it gives a modal card a stable width without pinning
// its height, so the card still grows to fit its content.
func NewMinWidthContainer(width float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minWidthLayout{width: width}, objects...)
}

// NewFixedWidthContainer pins a column to exactly width whatever its contents
// ask for, stretching children to fill it. The sidebars use it because a
// vertical scroller reports its content's minimum *width* as its own: without
// this, one long channel or member name widens the column and shoves the
// message area sideways. Contrast NewMinWidthContainer, which treats width as a
// floor and still grows. Content wider than the slot is clipped by the scroller,
// so pair it with NewEllipsisText on anything holding user-supplied text.
func NewFixedWidthContainer(width float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&fixedWidthLayout{width: width}, objects...)
}

type fixedWidthLayout struct{ width float32 }

func (l *fixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.NewPos(0, 0))
	}
}

func (l *fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, child := range objects {
		h = max(h, child.MinSize().Height)
	}
	return fyne.NewSize(l.width, h)
}

type minWidthLayout struct{ width float32 }

func (l *minWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.NewPos(0, 0))
	}
}

func (l *minWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := l.width, float32(0)
	for _, child := range objects {
		m := child.MinSize()
		w = max(w, m.Width)
		h = max(h, m.Height)
	}
	return fyne.NewSize(w, h)
}

type minHeightLayout struct{ height float32 }

func (l *minHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.NewPos(0, 0))
	}
}

func (l *minHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), l.height
	for _, child := range objects {
		m := child.MinSize()
		w = max(w, m.Width)
		h = max(h, m.Height)
	}
	return fyne.NewSize(w, h)
}
