package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// Compile-time interface assertions.
var (
	_ fyne.Widget       = (*ServerWidget)(nil)
	_ fyne.Tappable     = (*ServerWidget)(nil)
	_ desktop.Hoverable = (*ServerWidget)(nil)
)

// ServerWidget displays a server icon with selection and hover states.
type ServerWidget struct {
	widget.BaseWidget
	Server        *revoltgo.Server
	onTap         func()
	background    *canvas.Circle
	iconContainer *fyne.Container
	iconWrapper   *fyne.Container
	selected      bool
	hovered       bool
	baseSize      float32
	grownSize     float32
}

// NewServerWidget creates a new server widget.
func NewServerWidget(server *revoltgo.Server, onTap func()) *ServerWidget {
	baseSize := theme.Sizes.ServerIconSize
	grownSize := baseSize * 1.1 // 10% larger on hover/select

	w := &ServerWidget{
		Server:     server,
		onTap:      onTap,
		background: canvas.NewCircle(theme.Colors.ServerDefaultBg),
		baseSize:   baseSize,
		grownSize:  grownSize,
	}
	w.ExtendBaseWidget(w)
	return w
}

// SetSelected updates the selection state and refreshes appearance.
func (w *ServerWidget) SetSelected(selected bool) {
	w.selected = selected
	w.updateAppearance()
}

func (w *ServerWidget) updateAppearance() {
	if w.selected {
		w.background.FillColor = theme.Colors.ServerSelectedBg
	} else {
		w.background.FillColor = theme.Colors.ServerDefaultBg
	}
	w.background.Refresh()
	w.updateSize()
}

func (w *ServerWidget) updateSize() {
	if w.iconWrapper == nil {
		return
	}

	var newSize float32
	if w.selected || w.hovered {
		newSize = w.grownSize
	} else {
		newSize = w.baseSize
	}

	size := fyne.NewSize(newSize, newSize)
	w.iconWrapper.Layout = container.NewGridWrap(size).Layout
	w.iconWrapper.Refresh()
}

// CreateRenderer returns the renderer for this widget.
func (w *ServerWidget) CreateRenderer() fyne.WidgetRenderer {
	iconSize := fyne.NewSize(w.baseSize, w.baseSize)

	initial := ""
	if len(w.Server.Name) > 0 {
		initial = string(w.Server.Name[0])
	}
	initialLabel := canvas.NewText(initial, theme.Colors.TextPrimary)
	initialLabel.TextStyle = fyne.TextStyle{Bold: true}
	initialLabel.Alignment = fyne.TextAlignCenter

	w.iconContainer = container.NewStack(w.background, container.NewCenter(initialLabel))

	iconID, iconURL := GetServerIconInfo(w.Server)
	if iconURL != "" {
		cache.GetImageCache().LoadImageToContainer(iconID, iconURL, iconSize, w.iconContainer, true, w.background)
	}

	w.iconWrapper = container.NewGridWrap(iconSize, w.iconContainer)
	centered := container.NewCenter(w.iconWrapper)

	return widget.NewSimpleRenderer(centered)
}

// Tapped handles tap events on the widget.
func (w *ServerWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (w *ServerWidget) MouseIn(*desktop.MouseEvent) {
	w.hovered = true
	w.updateAppearance()
}

// MouseMoved handles mouse movement within the widget.
func (w *ServerWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (w *ServerWidget) MouseOut() {
	w.hovered = false
	w.updateAppearance()
}
