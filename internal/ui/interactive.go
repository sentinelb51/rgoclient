package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// tapBase provides the tap, right-click, and (no-op) mouse-move plumbing shared
// by the interactive widgets in this package. Embedders implement CreateRenderer
// and, where they react to hover, MouseIn/MouseOut. Setting onSecondaryTap opts
// the widget into a context menu (servers, channels, members, avatars); leaving
// it nil makes right-clicks a no-op.
type tapBase struct {
	widget.BaseWidget
	onTap          func()
	onSecondaryTap func(*fyne.PointEvent)
}

func (b *tapBase) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *tapBase) TappedSecondary(e *fyne.PointEvent) {
	if b.onSecondaryTap != nil {
		b.onSecondaryTap(e)
	}
}

func (b *tapBase) MouseMoved(*desktop.MouseEvent) {}

// Cursor shows the pointer cursor over every tappable widget, so clickable
// elements read as clickable.
func (b *tapBase) Cursor() desktop.Cursor { return desktop.PointerCursor }

// TappableContainer wraps content, highlighting a background on hover and
// invoking onTap when clicked.
type TappableContainer struct {
	tapBase
	background *canvas.Rectangle
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*TappableContainer)(nil)
	_ desktop.Hoverable = (*TappableContainer)(nil)
)

// NewTappableContainer makes content tappable with a hover highlight.
func NewTappableContainer(content fyne.CanvasObject, onTap func()) *TappableContainer {
	t := &TappableContainer{
		background: canvas.NewRectangle(color.Transparent),
		content:    content,
	}
	t.onTap = onTap
	t.ExtendBaseWidget(t)
	return t
}

func (t *TappableContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(t.background, t.content))
}

func (t *TappableContainer) MouseIn(*desktop.MouseEvent) {
	t.background.FillColor = theme.Colors.TappableHoverBg
	t.background.Refresh()
}

func (t *TappableContainer) MouseOut() {
	t.background.FillColor = color.Transparent
	t.background.Refresh()
}

// HoverableStack wraps content, drawing a thin border on hover and reporting
// hover state. Used for message attachments.
type HoverableStack struct {
	tapBase
	background *canvas.Rectangle
	content    fyne.CanvasObject
	onHover    func(bool)
}

var (
	_ fyne.Tappable     = (*HoverableStack)(nil)
	_ desktop.Hoverable = (*HoverableStack)(nil)
)

// NewHoverableStack makes content tappable with a hover border and optional
// hover callback.
func NewHoverableStack(content fyne.CanvasObject, onTap func(), onHover func(bool)) *HoverableStack {
	h := &HoverableStack{
		background: canvas.NewRectangle(color.Transparent),
		content:    content,
		onHover:    onHover,
	}
	h.onTap = onTap
	h.ExtendBaseWidget(h)
	return h
}

func (h *HoverableStack) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(h.content, h.background))
}

func (h *HoverableStack) MouseIn(*desktop.MouseEvent) {
	h.background.StrokeColor = theme.Colors.AttachmentHoverBorder
	h.background.StrokeWidth = 1
	h.background.Refresh()
	if h.onHover != nil {
		h.onHover(true)
	}
}

func (h *HoverableStack) MouseOut() {
	h.background.StrokeColor = color.Transparent
	h.background.StrokeWidth = 0
	h.background.Refresh()
	if h.onHover != nil {
		h.onHover(false)
	}
}

// IconButton is a flat, icon-only button used for the per-message quick actions
// and the attachment viewer's header.
type IconButton struct {
	tapBase
	background *canvas.Rectangle
	icon       *canvas.Image
	onHover    func(bool)
}

var (
	_ fyne.Tappable     = (*IconButton)(nil)
	_ desktop.Hoverable = (*IconButton)(nil)
)

// roundedPanel is the small rounded surface the floating message controls (the
// hover quick-actions and the edit save/cancel pair) sit on.
func roundedPanel() *canvas.Rectangle {
	panel := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	panel.CornerRadius = 4
	return panel
}

func NewIconButton(res fyne.Resource, onTap func(), onHover func(bool)) *IconButton {
	size := theme.Sizes.SwiftActionSize

	background := canvas.NewRectangle(color.Transparent)
	background.SetMinSize(fyne.NewSize(size, size*0.8))

	b := &IconButton{background: background, icon: newScaledIcon(res, size*0.7), onHover: onHover}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	return b
}

func (b *IconButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.background, container.NewCenter(b.icon)))
}

func (b *IconButton) MouseIn(*desktop.MouseEvent) {
	b.background.FillColor = theme.Colors.SwiftActionHoverBg
	b.background.Refresh()
	if b.onHover != nil {
		b.onHover(true)
	}
}

func (b *IconButton) MouseOut() {
	b.background.FillColor = color.Transparent
	b.background.Refresh()
	if b.onHover != nil {
		b.onHover(false)
	}
}

// SidebarButton is a circular, icon-only button matching the server-icon
// aesthetic. It bookends the server list as the fixed home and settings
// entries, so it reuses the server background/hover colours for consistency —
// including the accent fill of a selected server, which the home button carries
// while the direct-message view is open.
type SidebarButton struct {
	tapBase
	background *canvas.Circle
	icon       *canvas.Image

	selected bool
	hovered  bool
}

var (
	_ fyne.Tappable     = (*SidebarButton)(nil)
	_ desktop.Hoverable = (*SidebarButton)(nil)
)

// NewSidebarButton creates a sidebar button rendering the given icon (a Fyne
// theme icon such as theme.HomeIcon()).
func NewSidebarButton(res fyne.Resource, onTap func()) *SidebarButton {
	b := &SidebarButton{
		background: canvas.NewCircle(theme.Colors.ServerDefaultBg),
		icon:       newScaledIcon(res, theme.Sizes.ServerIconSize*0.5),
	}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	return b
}

func (b *SidebarButton) CreateRenderer() fyne.WidgetRenderer {
	size := theme.Sizes.ServerIconSize
	wrap := container.NewGridWrap(fyne.NewSize(size, size),
		container.NewStack(b.background, container.NewCenter(b.icon)))
	return widget.NewSimpleRenderer(container.NewCenter(wrap))
}

// SetSelected marks the button as the active view, tinting it with the accent a
// selected server icon uses. Unchanged state is a no-op so selection syncs only
// repaint when something actually changed.
func (b *SidebarButton) SetSelected(selected bool) {
	if b.selected == selected {
		return
	}
	b.selected = selected
	b.refreshAppearance()
}

// refreshAppearance repaints the circle for the current selected/hovered state.
// Selection outranks hover, so hovering the active view doesn't dim it.
func (b *SidebarButton) refreshAppearance() {
	switch {
	case b.selected:
		b.background.FillColor = theme.Colors.ServerSelectedBg
	case b.hovered:
		b.background.FillColor = theme.Colors.ServerHoverBg
	default:
		b.background.FillColor = theme.Colors.ServerDefaultBg
	}
	b.background.Refresh()
}

func (b *SidebarButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshAppearance()
}

func (b *SidebarButton) MouseOut() {
	b.hovered = false
	b.refreshAppearance()
}

// NewSidebarSeparator returns the short horizontal bar that visually divides the
// fixed home/settings buttons from the scrolling server icons.
func NewSidebarSeparator() fyne.CanvasObject {
	bar := canvas.NewRectangle(theme.Colors.ServerListSeparator)
	bar.CornerRadius = 1
	bar.SetMinSize(fyne.NewSize(theme.Sizes.ServerIconSize*0.6, 2))
	return container.NewCenter(bar)
}

// closeButtonSize is the side length of a CloseButton.
const closeButtonSize = 24

// CloseButton is an icon-only "cancel" button for removing items, using the
// Fyne theme cancel icon with a hover background.
type CloseButton struct {
	tapBase
	background *canvas.Rectangle
	icon       *canvas.Image
}

var (
	_ fyne.Tappable     = (*CloseButton)(nil)
	_ desktop.Hoverable = (*CloseButton)(nil)
)

// NewCloseButton creates a close button with the given tap handler.
func NewCloseButton(onTap func()) *CloseButton {
	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = 4

	b := &CloseButton{background: background, icon: newScaledIcon(fynetheme.CancelIcon(), 0)}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	return b
}

func (b *CloseButton) MinSize() fyne.Size {
	return fyne.NewSize(closeButtonSize, closeButtonSize)
}

func (b *CloseButton) MouseIn(*desktop.MouseEvent) {
	b.background.FillColor = theme.Colors.SwiftActionHoverBg
	b.background.Refresh()
}

func (b *CloseButton) MouseOut() {
	b.background.FillColor = color.Transparent
	b.background.Refresh()
}

func (b *CloseButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.background, container.NewPadded(b.icon)))
}

// Avatar is a circular, tappable avatar that loads its image asynchronously.
//
// It deliberately does not implement desktop.Hoverable: Fyne delivers hover to
// the innermost hoverable object, so an avatar that accepted hover would pull it
// away from the message row and make the row's quick-actions vanish whenever the
// pointer crossed the avatar.
type Avatar struct {
	tapBase
	content *fyne.Container
}

var _ fyne.Tappable = (*Avatar)(nil)

// NewAvatar creates a circular avatar of the standard message size. If both
// avatarID and avatarURL are set, the image loads in the background.
func NewAvatar(images *cache.ImageCache, avatarID, avatarURL string, onTap func()) *Avatar {
	size := fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	content := container.NewGridWrap(size, placeholder)

	if avatarURL != "" && avatarID != "" {
		images.LoadIntoContainer(avatarID, avatarURL, size, content, true, nil)
	}

	a := &Avatar{content: content}
	a.onTap = onTap
	a.ExtendBaseWidget(a)
	return a
}

// SetSource reloads the avatar image in place, used when the source resolves
// after the avatar was first mounted with only its placeholder.
func (a *Avatar) SetSource(images *cache.ImageCache, avatarID, avatarURL string) {
	if avatarURL == "" || avatarID == "" {
		return
	}
	size := fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
	images.LoadIntoContainer(avatarID, avatarURL, size, a.content, true, nil)
}

func (a *Avatar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(a.content)
}

func (a *Avatar) MinSize() fyne.Size {
	return fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
}
