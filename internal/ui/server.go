package ui

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

// serverHoverGrowth is how much a server icon grows when hovered or selected.
const serverHoverGrowth = 1.1

// ServerWidget is a circular server icon that grows and recolours when hovered
// or selected.
type ServerWidget struct {
	widget.BaseWidget
	Server *revoltgo.Server

	images      *cache.ImageCache
	onTap       func()
	background  *canvas.Circle
	iconWrapper *fyne.Container

	selected bool
	hovered  bool
}

var (
	_ fyne.Tappable     = (*ServerWidget)(nil)
	_ desktop.Hoverable = (*ServerWidget)(nil)
)

// NewServerWidget creates a server icon widget.
func NewServerWidget(images *cache.ImageCache, server *revoltgo.Server, onTap func()) *ServerWidget {
	w := &ServerWidget{
		Server:     server,
		images:     images,
		onTap:      onTap,
		background: canvas.NewCircle(theme.Colors.ServerDefaultBg),
	}
	w.ExtendBaseWidget(w)
	return w
}

// SetSelected updates the selection state and appearance.
func (w *ServerWidget) SetSelected(selected bool) {
	w.selected = selected
	w.refreshAppearance()
}

func (w *ServerWidget) refreshAppearance() {
	if w.selected {
		w.background.FillColor = theme.Colors.ServerSelectedBg
	} else {
		w.background.FillColor = theme.Colors.ServerDefaultBg
	}
	w.background.Refresh()

	if w.iconWrapper == nil {
		return
	}
	size := theme.Sizes.ServerIconSize
	if w.selected || w.hovered {
		size *= serverHoverGrowth
	}
	w.iconWrapper.Layout = container.NewGridWrap(fyne.NewSize(size, size)).Layout
	w.iconWrapper.Refresh()
}

func (w *ServerWidget) CreateRenderer() fyne.WidgetRenderer {
	iconSize := fyne.NewSize(theme.Sizes.ServerIconSize, theme.Sizes.ServerIconSize)

	initial := ""
	if len(w.Server.Name) > 0 {
		initial = string(w.Server.Name[0])
	}
	label := canvas.NewText(initial, theme.Colors.TextPrimary)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.Alignment = fyne.TextAlignCenter

	icon := container.NewStack(w.background, container.NewCenter(label))
	if w.Server.Icon != nil {
		w.images.LoadIntoContainer(w.Server.Icon.ID, w.Server.Icon.URL("64"), iconSize, icon, true, w.background)
	}

	w.iconWrapper = container.NewGridWrap(iconSize, icon)
	return widget.NewSimpleRenderer(container.NewCenter(w.iconWrapper))
}

func (w *ServerWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

func (w *ServerWidget) MouseIn(*desktop.MouseEvent) {
	w.hovered = true
	w.refreshAppearance()
}

func (w *ServerWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *ServerWidget) MouseOut() {
	w.hovered = false
	w.refreshAppearance()
}
