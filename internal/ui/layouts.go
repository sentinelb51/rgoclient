package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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

// FillLastRowLayout lays children left to right with no gaps: every child except
// the last takes its minimum width, and the last child fills the remaining
// space. All children span the full height. Used for the flat server | channel |
// message columns so the sections sit flush against each other.
type FillLastRowLayout struct{}

func (l *FillLastRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	last := -1
	for i, child := range objects {
		if child.Visible() {
			last = i
		}
	}

	var x float32
	for i, child := range objects {
		if !child.Visible() {
			continue
		}
		w := child.MinSize().Width
		if i == last {
			w = max(size.Width-x, 0)
		}
		child.Resize(fyne.NewSize(w, size.Height))
		child.Move(fyne.NewPos(x, 0))
		x += w
	}
}

func (l *FillLastRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
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
// horizontally and the group vertically.
type FixedWidthColumnLayout struct{ Width float32 }

func (l *FixedWidthColumnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var total float32
	for _, child := range objects {
		if child.Visible() {
			total += child.MinSize().Height
		}
	}

	y := max((size.Height-total)/2, 0)
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
