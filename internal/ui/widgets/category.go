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

// Ensure CategoryWidget implements necessary interfaces at compile time.
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

// MinSize returns the minimum size for the category widget, including top spacing.
func (categoryWidget *CategoryWidget) MinSize() fyne.Size {
	height := theme.Sizes.CategoryHeight
	if !categoryWidget.isFirstCategory {
		height += theme.Sizes.CategorySpacing
	}
	return fyne.NewSize(0, height)
}

// SetIsFirstCategory sets whether this is the first category (no top spacing).
func (categoryWidget *CategoryWidget) SetIsFirstCategory(isFirst bool) {
	categoryWidget.isFirstCategory = isFirst
}

// getExpandedIndicator returns a drawn minus sign (-) for expanded state.
// The minus is drawn centered vertically to align with the plus sign.
func getExpandedIndicator() fyne.CanvasObject {
	size := theme.Sizes.CategoryIndicatorSize
	strokeWidth := theme.Sizes.CategoryIndicatorStroke
	indicatorColor := theme.Colors.CategoryIndicator
	padding := float32(3)

	// Horizontal line (minus) - centered both horizontally and vertically
	horizontalLine := canvas.NewLine(indicatorColor)
	horizontalLine.Position1 = fyne.NewPos(padding, size/2)
	horizontalLine.Position2 = fyne.NewPos(size-padding, size/2)
	horizontalLine.StrokeWidth = strokeWidth

	icon := container.NewWithoutLayout(horizontalLine)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// getCollapsedIndicator returns a drawn plus sign (+) for collapsed state.
func getCollapsedIndicator() fyne.CanvasObject {
	size := theme.Sizes.CategoryIndicatorSize
	strokeWidth := theme.Sizes.CategoryIndicatorStroke
	indicatorColor := theme.Colors.CategoryIndicator
	padding := float32(3)

	// Horizontal line
	horizontalLine := canvas.NewLine(indicatorColor)
	horizontalLine.Position1 = fyne.NewPos(padding, size/2)
	horizontalLine.Position2 = fyne.NewPos(size-padding, size/2)
	horizontalLine.StrokeWidth = strokeWidth

	// Vertical line
	verticalLine := canvas.NewLine(indicatorColor)
	verticalLine.Position1 = fyne.NewPos(size/2, padding)
	verticalLine.Position2 = fyne.NewPos(size/2, size-padding)
	verticalLine.StrokeWidth = strokeWidth

	icon := container.NewWithoutLayout(horizontalLine, verticalLine)
	wrapper := container.NewGridWrap(fyne.NewSize(size, size), icon)
	return container.NewCenter(wrapper)
}

// NewCategoryWidget creates a new category widget with the given title.
func NewCategoryWidget(title string, onToggle func(collapsed bool)) *CategoryWidget {
	categoryWidget := &CategoryWidget{
		title:              title,
		collapsed:          false,
		indicatorContainer: container.NewCenter(getExpandedIndicator()),
		background:         canvas.NewRectangle(color.Transparent),
		onToggle:           onToggle,
		isFirstCategory:    false,
	}
	categoryWidget.ExtendBaseWidget(categoryWidget)
	return categoryWidget
}

// SetCollapsed updates the collapsed state.
func (categoryWidget *CategoryWidget) SetCollapsed(collapsed bool) {
	categoryWidget.collapsed = collapsed
	categoryWidget.updateIndicator()
	categoryWidget.updateChannelVisibility()
}

// IsCollapsed returns the current collapsed state.
func (categoryWidget *CategoryWidget) IsCollapsed() bool {
	return categoryWidget.collapsed
}

// SetChannelWidgets sets the channel widgets that belong to this category.
func (categoryWidget *CategoryWidget) SetChannelWidgets(widgets []fyne.CanvasObject, channelContainer *fyne.Container) {
	categoryWidget.channelWidgets = widgets
	categoryWidget.channelContainer = channelContainer
}

func (categoryWidget *CategoryWidget) updateIndicator() {
	categoryWidget.indicatorContainer.RemoveAll()
	if categoryWidget.collapsed {
		categoryWidget.indicatorContainer.Add(getCollapsedIndicator())
	} else {
		categoryWidget.indicatorContainer.Add(getExpandedIndicator())
	}
	categoryWidget.indicatorContainer.Refresh()
}

func (categoryWidget *CategoryWidget) updateChannelVisibility() {
	for _, channelWidget := range categoryWidget.channelWidgets {
		if categoryWidget.collapsed {
			channelWidget.Hide()
		} else {
			channelWidget.Show()
		}
	}
	if categoryWidget.channelContainer != nil {
		categoryWidget.channelContainer.Refresh()
	}
}

// CreateRenderer returns the renderer for this widget.
func (categoryWidget *CategoryWidget) CreateRenderer() fyne.WidgetRenderer {
	titleLabel := canvas.NewText(categoryWidget.title, theme.Colors.CategoryText)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	titleLabel.TextSize = 13

	// Add a small spacer to the right of the indicator to push it slightly left from the edge
	rightSpacer := canvas.NewRectangle(color.Transparent)
	rightSpacer.SetMinSize(fyne.NewSize(8, 0))
	indicatorWithSpacer := container.NewHBox(categoryWidget.indicatorContainer, rightSpacer)

	content := container.NewBorder(nil, nil, titleLabel, indicatorWithSpacer, nil)
	padded := container.NewPadded(content)
	inner := container.NewStack(categoryWidget.background, padded)

	return &categoryRenderer{
		categoryWidget: categoryWidget,
		inner:          inner,
		objects:        []fyne.CanvasObject{inner},
	}
}

// Tapped handles tap events on the widget.
func (categoryWidget *CategoryWidget) Tapped(*fyne.PointEvent) {
	categoryWidget.collapsed = !categoryWidget.collapsed
	categoryWidget.updateIndicator()
	categoryWidget.updateChannelVisibility()
	if categoryWidget.onToggle != nil {
		categoryWidget.onToggle(categoryWidget.collapsed)
	}
}

// MouseIn handles mouse entering the widget.
func (categoryWidget *CategoryWidget) MouseIn(*desktop.MouseEvent) {
	categoryWidget.background.FillColor = theme.Colors.ChannelHoverBackground
	categoryWidget.background.Refresh()
}

// MouseMoved handles mouse movement within the widget.
func (categoryWidget *CategoryWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (categoryWidget *CategoryWidget) MouseOut() {
	categoryWidget.background.FillColor = color.Transparent
	categoryWidget.background.Refresh()
}

// categoryRenderer handles layout for CategoryWidget with optional top margin.
type categoryRenderer struct {
	categoryWidget *CategoryWidget
	inner          *fyne.Container
	objects        []fyne.CanvasObject
}

// Layout positions the inner content with appropriate margins.
func (renderer *categoryRenderer) Layout(size fyne.Size) {
	topMargin := float32(0)
	if !renderer.categoryWidget.isFirstCategory {
		topMargin = theme.Sizes.CategorySpacing
	}
	innerHeight := size.Height - topMargin
	renderer.inner.Move(fyne.NewPos(0, topMargin))
	renderer.inner.Resize(fyne.NewSize(size.Width, innerHeight))
}

// MinSize returns the minimum size for this renderer.
func (renderer *categoryRenderer) MinSize() fyne.Size {
	return renderer.categoryWidget.MinSize()
}

// Refresh refreshes the renderer.
func (renderer *categoryRenderer) Refresh() {
	renderer.inner.Refresh()
}

// Objects returns the objects in this renderer.
func (renderer *categoryRenderer) Objects() []fyne.CanvasObject {
	return renderer.objects
}

// Destroy cleans up the renderer.
func (renderer *categoryRenderer) Destroy() {}
