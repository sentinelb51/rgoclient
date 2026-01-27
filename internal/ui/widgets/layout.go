package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// VerticalNoSpacingLayout arranges objects vertically with zero spacing.
type VerticalNoSpacingLayout struct{}

// Layout arranges objects top-to-bottom with no gap.
func (l *VerticalNoSpacingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		childMin := child.MinSize()
		child.Move(fyne.NewPos(0, y))
		child.Resize(fyne.NewSize(size.Width, childMin.Height))
		y += childMin.Height
	}
}

// MinSize calculates the minimum size required.
func (l *VerticalNoSpacingLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		childMin := child.MinSize()
		if childMin.Width > w {
			w = childMin.Width
		}
		h += childMin.Height
	}
	return fyne.NewSize(w, h)
}

// NewVerticalNoSpacingContainer creates a container with zero vertical spacing.
func NewVerticalNoSpacingContainer(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&VerticalNoSpacingLayout{}, objects...)
}

// VBoxNoSpacing is an alias for NewVerticalNoSpacingContainer for convenience.
func VBoxNoSpacing(objects ...fyne.CanvasObject) *fyne.Container {
	return NewVerticalNoSpacingContainer(objects...)
}

// HorizontalNoSpacingLayout arranges objects horizontally with zero spacing.
type HorizontalNoSpacingLayout struct{}

// Layout arranges objects left-to-right with no gap.
func (l *HorizontalNoSpacingLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		childMin := child.MinSize()
		child.Resize(fyne.NewSize(childMin.Width, size.Height))
		child.Move(fyne.NewPos(x, 0))
		x += childMin.Width
	}
}

// MinSize calculates the minimum size required.
func (l *HorizontalNoSpacingLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		childMin := child.MinSize()
		w += childMin.Width
		if childMin.Height > h {
			h = childMin.Height
		}
	}
	return fyne.NewSize(w, h)
}

// NewHorizontalNoSpacingContainer creates a container with zero horizontal spacing.
func NewHorizontalNoSpacingContainer(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&HorizontalNoSpacingLayout{}, objects...)
}

// HBoxNoSpacing is an alias for NewHorizontalNoSpacingContainer for convenience.
func HBoxNoSpacing(objects ...fyne.CanvasObject) *fyne.Container {
	return NewHorizontalNoSpacingContainer(objects...)
}

// VerticalCenterFixedWidthLayout centers objects horizontally within a fixed width container.
// Objects are stacked vertically and centered vertically within the available height.
type VerticalCenterFixedWidthLayout struct {
	Width float32
}

func (l *VerticalCenterFixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	// Calculate total height of visible objects
	totalHeight := float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		totalHeight += child.MinSize().Height
	}

	// Start Y offset to center objects vertically
	y := (size.Height - totalHeight) / 2
	if y < 0 {
		y = 0
	}

	centerX := l.Width / 2
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		childMin := child.MinSize()
		child.Resize(childMin)

		// Horizontal Center
		x := centerX - (childMin.Width / 2)
		child.Move(fyne.NewPos(x, y))
		y += childMin.Height
	}
}

func (l *VerticalCenterFixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		childMin := child.MinSize()
		h += childMin.Height
	}
	return fyne.NewSize(l.Width, h)
}

// OverlayLayout positions content at a specific offset from the top-right corner.
// It allows the content to bleed outside the bounds (e.g., negative Y).
// Useful for floating action buttons, tooltips, or overlays.
type OverlayLayout struct {
	YOffset     float32
	RightOffset float32
}

func (l *OverlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		childSize := child.MinSize()
		x := size.Width - childSize.Width - l.RightOffset
		y := l.YOffset // Use directly (can be negative)
		child.Resize(childSize)
		child.Move(fyne.NewPos(x, y))
	}
}

func (l *OverlayLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, 0) // Does not affect parent size
}

// SwiftActionsLayout is deprecated. Use OverlayLayout instead.
type SwiftActionsLayout = OverlayLayout

// MinHeightLayout forces a minimum height for the container.
type MinHeightLayout struct {
	MinHeight float32
}

func (l *MinHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	// Child fills the size
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.NewPos(0, 0))
	}
}

func (l *MinHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), l.MinHeight
	for _, child := range objects {
		childMin := child.MinSize()
		if childMin.Width > w {
			w = childMin.Width
		}
		if childMin.Height > h {
			h = childMin.Height
		}
	}
	return fyne.NewSize(w, h)
}

// NewMinHeightContainer creates a container with a minimum height.
func NewMinHeightContainer(height float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&MinHeightLayout{MinHeight: height}, objects...)
}
