package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
)

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

// NewMinHeightContainer wraps objects in a container with a minimum height. Its
// children are stretched to fill the available space.
func NewMinHeightContainer(height float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&minHeightLayout{height: height}, objects...)
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
