package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// Compile-time interface assertions.
var (
	_ fyne.Widget       = (*CategoryWidget)(nil)
	_ fyne.Tappable     = (*CategoryWidget)(nil)
	_ desktop.Hoverable = (*CategoryWidget)(nil)
)

// CategoryWidget displays a collapsible category header in the channel sidebar.
type CategoryWidget struct {
	widget.BaseWidget
	title              string
	collapsed          bool
	indicatorContainer *fyne.Container
	background         *canvas.Rectangle
	onToggle           func(collapsed bool)
	channelWidgets     []fyne.CanvasObject
	channelContainer   *fyne.Container
	isFirstCategory    bool
}

// NewCategoryWidget creates a new category widget with the given title.
func NewCategoryWidget(title string, onToggle func(collapsed bool)) *CategoryWidget {
	w := &CategoryWidget{
		title:              title,
		collapsed:          false,
		indicatorContainer: container.NewCenter(getExpandedIndicator()),
		background:         canvas.NewRectangle(color.Transparent),
		onToggle:           onToggle,
		isFirstCategory:    false,
	}
	w.ExtendBaseWidget(w)
	return w
}

// MinSize returns the minimum size for the category widget.
func (w *CategoryWidget) MinSize() fyne.Size {
	height := theme.Sizes.CategoryHeight
	if !w.isFirstCategory {
		height += theme.Sizes.CategorySpacing
	}
	return fyne.NewSize(0, height)
}

// SetIsFirstCategory sets whether this is the first category (no top spacing).
func (w *CategoryWidget) SetIsFirstCategory(isFirst bool) {
	w.isFirstCategory = isFirst
}

// SetCollapsed updates the collapsed state.
func (w *CategoryWidget) SetCollapsed(collapsed bool) {
	w.collapsed = collapsed
	w.updateIndicator()
	w.updateChannelVisibility()
}

// IsCollapsed returns the current collapsed state.
func (w *CategoryWidget) IsCollapsed() bool {
	return w.collapsed
}

// SetChannelWidgets sets the channel widgets that belong to this category.
func (w *CategoryWidget) SetChannelWidgets(widgets []fyne.CanvasObject, container *fyne.Container) {
	w.channelWidgets = widgets
	w.channelContainer = container
}

func (w *CategoryWidget) updateIndicator() {
	w.indicatorContainer.RemoveAll()
	if w.collapsed {
		w.indicatorContainer.Add(getCollapsedIndicator())
	} else {
		w.indicatorContainer.Add(getExpandedIndicator())
	}
	w.indicatorContainer.Refresh()
}

func (w *CategoryWidget) updateChannelVisibility() {
	for _, ch := range w.channelWidgets {
		if w.collapsed {
			ch.Hide()
		} else {
			ch.Show()
		}
	}
	if w.channelContainer != nil {
		w.channelContainer.Refresh()
	}
}

// CreateRenderer returns the renderer for this widget.
func (w *CategoryWidget) CreateRenderer() fyne.WidgetRenderer {
	titleLabel := canvas.NewText(w.title, theme.Colors.CategoryText)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	titleLabel.TextSize = 13

	rightSpacer := canvas.NewRectangle(color.Transparent)
	rightSpacer.SetMinSize(fyne.NewSize(8, 0))
	indicatorWithSpacer := container.NewHBox(w.indicatorContainer, rightSpacer)

	content := container.NewBorder(nil, nil, titleLabel, indicatorWithSpacer, nil)
	padded := container.NewPadded(content)
	inner := container.NewStack(w.background, padded)

	return &categoryRenderer{
		widget:  w,
		inner:   inner,
		objects: []fyne.CanvasObject{inner},
	}
}

// Tapped handles tap events on the widget.
func (w *CategoryWidget) Tapped(*fyne.PointEvent) {
	w.collapsed = !w.collapsed
	w.updateIndicator()
	w.updateChannelVisibility()
	if w.onToggle != nil {
		w.onToggle(w.collapsed)
	}
}

// MouseIn handles mouse entering the widget.
func (w *CategoryWidget) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.ChannelHoverBackground
	w.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (w *CategoryWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (w *CategoryWidget) MouseOut() {
	w.background.FillColor = color.Transparent
	w.background.Refresh()
}

// getExpandedIndicator returns a minus sign (-) for expanded state.
func getExpandedIndicator() fyne.CanvasObject {
	size := theme.Sizes.CategoryIndicatorSize
	stroke := theme.Sizes.CategoryIndicatorStroke
	col := theme.Colors.CategoryIndicator
	pad := float32(3)

	line := canvas.NewLine(col)
	line.Position1 = fyne.NewPos(pad, size/2)
	line.Position2 = fyne.NewPos(size-pad, size/2)
	line.StrokeWidth = stroke

	icon := container.NewWithoutLayout(line)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// getCollapsedIndicator returns a plus sign (+) for collapsed state.
func getCollapsedIndicator() fyne.CanvasObject {
	size := theme.Sizes.CategoryIndicatorSize
	stroke := theme.Sizes.CategoryIndicatorStroke
	col := theme.Colors.CategoryIndicator
	pad := float32(3)

	hLine := canvas.NewLine(col)
	hLine.Position1 = fyne.NewPos(pad, size/2)
	hLine.Position2 = fyne.NewPos(size-pad, size/2)
	hLine.StrokeWidth = stroke

	vLine := canvas.NewLine(col)
	vLine.Position1 = fyne.NewPos(size/2, pad)
	vLine.Position2 = fyne.NewPos(size/2, size-pad)
	vLine.StrokeWidth = stroke

	icon := container.NewWithoutLayout(hLine, vLine)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// categoryRenderer handles layout for CategoryWidget with optional top margin.
type categoryRenderer struct {
	widget  *CategoryWidget
	inner   *fyne.Container
	objects []fyne.CanvasObject
}

func (r *categoryRenderer) Layout(size fyne.Size) {
	topMargin := float32(0)
	if !r.widget.isFirstCategory {
		topMargin = theme.Sizes.CategorySpacing
	}
	innerHeight := size.Height - topMargin
	r.inner.Move(fyne.NewPos(0, topMargin))
	r.inner.Resize(fyne.NewSize(size.Width, innerHeight))
}

func (r *categoryRenderer) MinSize() fyne.Size {
	return r.widget.MinSize()
}

func (r *categoryRenderer) Refresh() {
	r.inner.Refresh()
}

func (r *categoryRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *categoryRenderer) Destroy() {}
