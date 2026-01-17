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

// Ensure ServerWidget implements necessary interfaces at compile time.
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

	serverWidget := &ServerWidget{
		Server:     server,
		onTap:      onTap,
		background: canvas.NewCircle(theme.Colors.ServerDefaultBg),
		baseSize:   baseSize,
		grownSize:  grownSize,
	}
	serverWidget.ExtendBaseWidget(serverWidget)
	return serverWidget
}

// SetSelected updates the selection state and refreshes appearance.
func (serverWidget *ServerWidget) SetSelected(selected bool) {
	serverWidget.selected = selected
	serverWidget.updateAppearance()
}

func (serverWidget *ServerWidget) updateAppearance() {
	// Update background colour
	if serverWidget.selected {
		serverWidget.background.FillColor = theme.Colors.ServerSelectedBg
	} else {
		serverWidget.background.FillColor = theme.Colors.ServerDefaultBg
	}
	serverWidget.background.Refresh()

	// Update size based on hover/selected state
	serverWidget.updateSize()
}

func (serverWidget *ServerWidget) updateSize() {
	if serverWidget.iconWrapper == nil {
		return
	}

	var newSize float32
	if serverWidget.selected || serverWidget.hovered {
		newSize = serverWidget.grownSize
	} else {
		newSize = serverWidget.baseSize
	}

	size := fyne.NewSize(newSize, newSize)
	serverWidget.iconWrapper.Layout = container.NewGridWrap(size).Layout
	serverWidget.iconWrapper.Refresh()
}

// CreateRenderer returns the renderer for this widget.
func (serverWidget *ServerWidget) CreateRenderer() fyne.WidgetRenderer {
	iconSize := fyne.NewSize(serverWidget.baseSize, serverWidget.baseSize)

	// Server initial as fallback placeholder
	initial := ""
	if len(serverWidget.Server.Name) > 0 {
		initial = string(serverWidget.Server.Name[0])
	}
	initialLabel := canvas.NewText(initial, theme.Colors.TextPrimary)
	initialLabel.TextStyle = fyne.TextStyle{Bold: true}
	initialLabel.Alignment = fyne.TextAlignCenter

	// Create icon content container - background is always present for hover effects
	serverWidget.iconContainer = container.NewStack(serverWidget.background, container.NewCenter(initialLabel))

	// Load server icon asynchronously if available
	iconID, iconURL := GetServerIconInfo(serverWidget.Server)
	if iconURL != "" {
		cache.GetImageCache().LoadImageToContainer(iconID, iconURL, iconSize, serverWidget.iconContainer, true, serverWidget.background)
	}

	serverWidget.iconWrapper = container.NewGridWrap(iconSize, serverWidget.iconContainer)

	// Centre the icon wrapper for consistent positioning
	centered := container.NewCenter(serverWidget.iconWrapper)

	return widget.NewSimpleRenderer(centered)
}

// Tapped handles tap events on the widget.
func (serverWidget *ServerWidget) Tapped(*fyne.PointEvent) {
	if serverWidget.onTap != nil {
		serverWidget.onTap()
	}
}

// MouseIn handles mouse entering the widget.
func (serverWidget *ServerWidget) MouseIn(*desktop.MouseEvent) {
	serverWidget.hovered = true
	serverWidget.updateAppearance()
}

// MouseMoved handles mouse movement within the widget.
func (serverWidget *ServerWidget) MouseMoved(*desktop.MouseEvent) {}

// MouseOut handles mouse leaving the widget.
func (serverWidget *ServerWidget) MouseOut() {
	serverWidget.hovered = false
	serverWidget.updateAppearance()
}
