package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
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

// minSizeLayout stretches every child to fill the container and reports a
// minimum size that is at least min on each axis.
type minSizeLayout struct{ min fyne.Size }

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

	return m
}

/* Padding */

// newFlushContainer wraps a single widget that carries Fyne's built-in inner
// padding (notably widget.RichText) so its content sits flush against the
// container's top-left origin instead of being inset. That lets the message body
// line up with the author name, a plain canvas.Text with no padding of its own.
func newFlushContainer(obj fyne.CanvasObject) *fyne.Container {
	return container.New(&stripPaddingLayout{inset: fynetheme.InnerPadding()}, obj)
}

// stripPaddingLayout neutralises a uniform inset on its single child by
// over-sizing the child and offsetting it by the inset, so the child's content
// aligns with the container origin. What the child draws outside the container
// bounds is its transparent padding, so nothing visible spills onto neighbours.
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
