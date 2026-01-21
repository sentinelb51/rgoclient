package widgets

import (
	"fyne.io/fyne/v2"
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

		minSize := child.MinSize()
		child.Move(fyne.NewPos(0, y))
		child.Resize(fyne.NewSize(size.Width, minSize.Height))
		y += minSize.Height
	}
}

// MinSize calculates the minimum size required.
func (l *VerticalNoSpacingLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		min := child.MinSize()
		if min.Width > w {
			w = min.Width
		}
		h += min.Height
	}
	return fyne.NewSize(w, h)
}

// NewVerticalNoSpacingContainer creates a container with zero vertical spacing.
func NewVerticalNoSpacingContainer(objects ...fyne.CanvasObject) *fyne.Container {
	return fyne.NewContainerWithLayout(&VerticalNoSpacingLayout{}, objects...)
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

		minSize := child.MinSize()
		child.Move(fyne.NewPos(x, 0))
		child.Resize(fyne.NewSize(minSize.Width, size.Height)) // Stretch height to fill
		x += minSize.Width
	}
}

// MinSize calculates the minimum size required.
func (l *HorizontalNoSpacingLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}
		min := child.MinSize()
		w += min.Width
		if min.Height > h {
			h = min.Height
		}
	}
	return fyne.NewSize(w, h)
}

// NewHorizontalNoSpacingContainer creates a container with zero horizontal spacing.
func NewHorizontalNoSpacingContainer(objects ...fyne.CanvasObject) *fyne.Container {
	return fyne.NewContainerWithLayout(&HorizontalNoSpacingLayout{}, objects...)
}

// SwiftActionsLayout positions the content at the top right with an offset.
// It allows the content to bleed outside the bounds (e.g., negative Y).
type SwiftActionsLayout struct {
	YOffset     float32
	RightOffset float32
}

func (l *SwiftActionsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		childSize := child.MinSize()
		x := size.Width - childSize.Width - l.RightOffset
		y := l.YOffset // Use directly (can be negative)

		child.Move(fyne.NewPos(x, y))
		child.Resize(childSize)
	}
}

func (l *SwiftActionsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	// Returns zero size so it doesn't affect parent layout flow for "overlay" behavior
	// But usually overlay needs to match stack.
	// Actually for Stack, minSize is the max of children.
	// If we return 0, the stack will be sized by the OTHER children (main content), which is exactly what we want.
	return fyne.NewSize(0, 0)
}
