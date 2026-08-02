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
	"RGOClient/internal/util"
)

const (
	// closeButtonSize is the side length of a CloseButton.
	closeButtonSize = 24

	// iconRestTranslucency dims an icon button while the pointer is elsewhere.
	// Hovering clears it, so what lights up is the icon itself rather than a plate
	// drawn behind it.
	iconRestTranslucency = 0.45
)

/* Shared plumbing */

// tapBase provides the tap, right-click, and (no-op) mouse-move plumbing shared
// by the interactive widgets here. Embedders implement CreateRenderer and, where
// they react to hover, MouseIn/MouseOut. Setting onSecondaryTap opts the widget
// into a context menu; leaving it nil makes right-clicks a no-op.
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

// Cursor shows the pointer over every tappable widget, so clickable elements
// read as clickable.
func (b *tapBase) Cursor() desktop.Cursor { return desktop.PointerCursor }

// roundedPanel is the small rounded surface the floating message controls — the
// hover quick-actions and the edit save/cancel pair — sit on.
func roundedPanel() *canvas.Rectangle {
	panel := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	panel.CornerRadius = 4

	return panel
}

/* Containers */

// TappableContainer wraps content, highlighting a background on hover and
// invoking onTap when clicked.
type TappableContainer struct {
	tapBase

	// Menu supplies the items right-clicking the row offers, as on the sidebar's
	// server and channel rows. It is the option for rows that are a plain
	// container rather than a widget of their own — the member list's.
	Menu func() []*fyne.MenuItem

	background *canvas.Rectangle
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable          = (*TappableContainer)(nil)
	_ fyne.SecondaryTappable = (*TappableContainer)(nil)
	_ desktop.Hoverable      = (*TappableContainer)(nil)
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

// TappedSecondary raises Menu's items when one is set, falling back to the
// handler tapBase carries — the reply preview inside a message hands its
// right-click to the message rather than opening a menu of its own.
func (t *TappableContainer) TappedSecondary(event *fyne.PointEvent) {
	if t.Menu != nil {
		showMenuHook(t, t.Menu, event)
		return
	}

	t.tapBase.TappedSecondary(event)
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

// NewHoverableStack makes content tappable with a hover border and an optional
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

/* Buttons */

// IconButton is a flat, icon-only button used for the per-message quick actions
// and the attachment viewer's header. It draws no background of its own: the
// icon rests dimmed and brightens under the pointer.
type IconButton struct {
	tapBase
	icon    *canvas.Image
	onHover func(bool)
}

var (
	_ fyne.Tappable     = (*IconButton)(nil)
	_ desktop.Hoverable = (*IconButton)(nil)
)

func NewIconButton(res fyne.Resource, onTap func(), onHover func(bool)) *IconButton {
	b := &IconButton{icon: newScaledIcon(res, theme.Sizes.SwiftActionSize*0.7), onHover: onHover}
	b.icon.Translucency = iconRestTranslucency
	b.onTap = onTap
	b.ExtendBaseWidget(b)

	return b
}

func (b *IconButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewCenter(b.icon))
}

// MinSize gives every icon button the same box whatever its icon measures, so a
// row of them is evenly spaced.
func (b *IconButton) MinSize() fyne.Size {
	size := theme.Sizes.SwiftActionSize

	return fyne.NewSize(size, size*0.8)
}

func (b *IconButton) MouseIn(*desktop.MouseEvent) {
	b.setLit(true)

	if b.onHover != nil {
		b.onHover(true)
	}
}

func (b *IconButton) MouseOut() {
	b.setLit(false)

	if b.onHover != nil {
		b.onHover(false)
	}
}

// setLit brightens or dims the icon.
func (b *IconButton) setLit(lit bool) {
	translucency := float64(iconRestTranslucency)
	if lit {
		translucency = 0
	}

	b.icon.Translucency = translucency
	b.icon.Refresh()
}

// SidebarButton is a circular, icon-only button matching the server-icon look.
// It bookends the server list as the fixed home and settings entries, so it
// reuses the server background, hover, and selected colours — the last of which
// the home button carries while the direct-message view is open.
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

// NewSidebarButton creates a sidebar button rendering the given icon.
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

// SetSelected marks the button as the active view. Unchanged state is a no-op,
// so a sidebar-wide sync only repaints what actually changed.
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

// NewSidebarSeparator returns the short horizontal bar dividing the fixed
// home/settings buttons from the scrolling server icons.
func NewSidebarSeparator() fyne.CanvasObject {
	bar := canvas.NewRectangle(theme.Colors.ServerListSeparator)
	bar.CornerRadius = 1
	bar.SetMinSize(fyne.NewSize(theme.Sizes.ServerIconSize*0.6, 2))

	return container.NewCenter(bar)
}

// CloseButton is an icon-only "cancel" button for removing items.
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

func (b *CloseButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.background, container.NewPadded(b.icon)))
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

/* Tooltips */

// Tooltip is the floating label an icon-only control shows on hover, naming
// what the icon stands for.
//
// Layer is mounted over the whole window rather than inside the column that
// triggers it, because the label has to be able to overhang that column. A Fyne
// pop-up would do the same job but cannot be used: pushing an overlay routes the
// entire hit test into it, so the widget being hovered would never receive
// MouseOut and the tooltip would never come back down.
type Tooltip struct {
	Layer *fyne.Container // stack this over the main layout

	card  *fyne.Container
	label *canvas.Text
}

// NewTooltip builds an empty, hidden tooltip.
func NewTooltip() *Tooltip {
	label := canvas.NewText("", theme.Colors.TextPrimary)
	label.TextSize = theme.Sizes.TooltipTextSize
	label.TextStyle = fyne.TextStyle{Bold: true}

	background := canvas.NewRectangle(theme.Colors.TooltipBg)
	background.CornerRadius = theme.Sizes.TooltipRadius

	padV, padH := theme.Sizes.TooltipPaddingV, theme.Sizes.TooltipPaddingH
	card := container.NewStack(background, NewInset(label, padV, padV, padH, padH))
	card.Hide()

	// Nothing in the layer is tappable or hoverable, so it never takes an event
	// from the widgets underneath it.
	return &Tooltip{Layer: container.NewWithoutLayout(card), card: card, label: label}
}

// Show names obj, placing the label just past its right edge and centred on it.
// An empty name hides the tooltip instead.
func (t *Tooltip) Show(text string, obj fyne.CanvasObject) {
	if text == "" {
		t.Hide()
		return
	}

	t.label.Text = text
	t.label.Refresh()

	// Both positions are canvas-absolute; the difference is the offset inside the
	// layer, wherever the layer itself happens to sit.
	driver := fyne.CurrentApp().Driver()
	anchor := driver.AbsolutePositionForObject(obj).Subtract(driver.AbsolutePositionForObject(t.Layer))

	size := t.card.MinSize()
	t.card.Resize(size)
	t.card.Move(fyne.NewPos(
		anchor.X+obj.Size().Width+theme.Sizes.TooltipGap,
		anchor.Y+(obj.Size().Height-size.Height)/2,
	))

	t.card.Show()
	t.card.Refresh() // Show alone neither lays the card out nor repaints it
}

// Hide takes the tooltip down. Safe to call when nothing is showing.
func (t *Tooltip) Hide() { t.card.Hide() }

/* Avatars */

// circularAvatar builds a circular avatar of the given size, loading the image
// from avatarURL when present. It is the one place an avatar image is mounted.
func circularAvatar(images *cache.ImageCache, avatarURL string, size fyne.Size) *fyne.Container {
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
	avatar := container.NewGridWrap(size, placeholder)
	loadAvatar(images, avatar, avatarURL, size)

	return avatar
}

// loadAvatar kicks off the circular image load for an already-built avatar
// container.
func loadAvatar(images *cache.ImageCache, target *fyne.Container, avatarURL string, size fyne.Size) {
	if avatarURL == "" {
		return
	}

	images.LoadIntoContainer(avatarCacheID(avatarURL), avatarURL, size, target, true, nil)
}

// avatarCacheID is the key an avatar URL is cached under: its Autumn file ID,
// falling back to the URL itself when it isn't shaped like an Autumn one.
func avatarCacheID(avatarURL string) string {
	if id := util.IDFromAttachmentURL(avatarURL); id != "" {
		return id
	}

	return avatarURL
}

// Avatar is the circular, tappable avatar shown beside a message.
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

// NewAvatar creates a circular avatar of the standard message size.
func NewAvatar(images *cache.ImageCache, avatarURL string, onTap func()) *Avatar {
	a := &Avatar{content: circularAvatar(images, avatarURL, avatarSize())}
	a.onTap = onTap
	a.ExtendBaseWidget(a)

	return a
}

// SetSource reloads the avatar image in place, for when the source resolves
// after the avatar was first mounted with only its placeholder.
func (a *Avatar) SetSource(images *cache.ImageCache, avatarURL string) {
	loadAvatar(images, a.content, avatarURL, avatarSize())
}

func (a *Avatar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(a.content)
}

func (a *Avatar) MinSize() fyne.Size { return avatarSize() }

func avatarSize() fyne.Size {
	return fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
}

/* Scrolling */

// scrollAmplify multiplies wheel deltas so scrolling feels message-by-message.
const scrollAmplify = 4

// ObservableScroll is a vertical scroll container that reports offset changes
// and supports middle-mouse panning.
type ObservableScroll struct {
	container.Scroll
	OnScroll func(offset fyne.Position)
	panning  bool
}

var _ fyne.Draggable = (*ObservableScroll)(nil)

// NewObservableVScroll creates an observable vertical scroll container.
func NewObservableVScroll(content fyne.CanvasObject) *ObservableScroll {
	s := &ObservableScroll{}
	s.Direction = container.ScrollVerticalOnly
	s.Content = content
	s.ExtendBaseWidget(s)

	return s
}

// Scrolled amplifies the wheel delta and notifies listeners.
func (s *ObservableScroll) Scrolled(ev *fyne.ScrollEvent) {
	amplified := *ev
	amplified.Scrolled.DX *= scrollAmplify
	amplified.Scrolled.DY *= scrollAmplify

	s.Scroll.Scrolled(&amplified)
	s.notify()
}

// MouseDown begins panning on a middle-button press.
func (s *ObservableScroll) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		s.panning = true
	}
}

// MouseUp ends panning on a middle-button release.
func (s *ObservableScroll) MouseUp(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		s.panning = false
	}
}

// Dragged pans the view while the middle button is held.
func (s *ObservableScroll) Dragged(ev *fyne.DragEvent) {
	if !s.panning {
		return
	}

	s.Offset.X -= ev.Dragged.DX
	s.Offset.Y -= ev.Dragged.DY
	s.Refresh()
	s.notify()
}

// DragEnd completes fyne.Draggable. Without it the driver never recognises the
// scroll as draggable, so Dragged is never called and panning silently dies.
func (s *ObservableScroll) DragEnd() { s.panning = false }

func (s *ObservableScroll) notify() {
	if s.OnScroll != nil {
		s.OnScroll(s.Offset)
	}
}

/* Shortened text */

// ellipsis marks a shortened label. One glyph rather than three dots, so the
// text it replaces stays as long as possible.
const ellipsis = "…"

// NewEllipsisText wraps a single-line canvas.Text in a container that shortens
// the text to whatever width it is given, ending it in an ellipsis. Its minimum
// width is zero, which is the whole point: a sidebar row must not be able to
// widen its column just because someone has a long name.
//
// It only works in a slot that hands its child real width — a Border's centre,
// not an HBox, which would give a zero-minimum child zero width.
func NewEllipsisText(text *canvas.Text) *fyne.Container {
	return container.New(&ellipsisLayout{text: text, full: text.Text}, text)
}

// ellipsisLayout re-fits its text to the width it is handed and centres it
// vertically. Rewriting the text during Layout is safe because the reported
// minimum size doesn't depend on the content — the width is fixed at zero and
// the height is the font's — so a shortened string can't trigger another layout.
type ellipsisLayout struct {
	text *canvas.Text
	full string
}

func (l *ellipsisLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	if fitted := TruncateToWidth(l.full, size.Width, l.text.TextSize, l.text.TextStyle); fitted != l.text.Text {
		l.text.Text = fitted
		l.text.Refresh()
	}

	height := l.lineHeight()
	l.text.Resize(fyne.NewSize(size.Width, height))
	l.text.Move(fyne.NewPos(0, (size.Height-height)/2))
}

func (l *ellipsisLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, l.lineHeight())
}

// lineHeight measures a sample glyph rather than the text itself, so a row is
// the same height whether its name is long, short, or not yet resolved.
func (l *ellipsisLayout) lineHeight() float32 {
	return fyne.MeasureText("W", l.text.TextSize, l.text.TextStyle).Height
}

// TruncateToWidth shortens text until it fits inside width when rendered at the
// given size and style, appending an ellipsis when anything was dropped. Unlike
// util.Truncate, which counts runes, this measures the rendered result — in a
// proportional font the same rune count is a different width. The binary search
// keeps it to a handful of measurements, which matters because it runs on every
// layout pass.
func TruncateToWidth(text string, width, size float32, style fyne.TextStyle) string {
	if width <= 0 {
		return ""
	}
	if fyne.MeasureText(text, size, style).Width <= width {
		return text
	}

	// Longest prefix that still fits once the ellipsis is added.
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if fyne.MeasureText(string(runes[:mid])+ellipsis, size, style).Width <= width {
			low = mid
		} else {
			high = mid - 1
		}
	}

	if low == 0 {
		return ""
	}
	return string(runes[:low]) + ellipsis
}
