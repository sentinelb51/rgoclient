package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// tapBase provides the tap and (no-op) mouse-move plumbing shared by the
// interactive widgets in this package. Embedders implement CreateRenderer and,
// where they react to hover, MouseIn/MouseOut.
type tapBase struct {
	widget.BaseWidget
	onTap func()
}

func (b *tapBase) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *tapBase) MouseMoved(*desktop.MouseEvent) {}

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
	h.background.StrokeColor = color.Black
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

// iconButton is a flat, icon-only button used for the per-message quick actions.
type iconButton struct {
	tapBase
	background *canvas.Rectangle
	icon       *canvas.Image
	onHover    func(bool)
}

var (
	_ fyne.Tappable     = (*iconButton)(nil)
	_ desktop.Hoverable = (*iconButton)(nil)
)

func newIconButton(iconPath string, onTap func(), onHover func(bool)) *iconButton {
	size := theme.Sizes.SwiftActionSize

	background := canvas.NewRectangle(color.Transparent)
	background.SetMinSize(fyne.NewSize(size, size*0.8))

	icon := canvas.NewImageFromFile(iconPath)
	icon.FillMode = canvas.ImageFillContain
	icon.ScaleMode = canvas.ImageScaleSmooth
	icon.SetMinSize(fyne.NewSize(size*0.7, size*0.7))

	b := &iconButton{background: background, icon: icon, onHover: onHover}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	return b
}

func (b *iconButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.background, container.NewCenter(b.icon)))
}

func (b *iconButton) MouseIn(*desktop.MouseEvent) {
	b.background.FillColor = theme.Colors.SwiftActionHoverBg
	b.background.Refresh()
	if b.onHover != nil {
		b.onHover(true)
	}
}

func (b *iconButton) MouseOut() {
	b.background.FillColor = color.Transparent
	b.background.Refresh()
	if b.onHover != nil {
		b.onHover(false)
	}
}

// closeButtonSize is the side length of a CloseButton.
const closeButtonSize = 24

// CloseButton is a drawn "×" button for removing items.
type CloseButton struct {
	tapBase
	hovered bool
}

var (
	_ fyne.Tappable     = (*CloseButton)(nil)
	_ desktop.Hoverable = (*CloseButton)(nil)
)

// NewCloseButton creates a close button with the given tap handler.
func NewCloseButton(onTap func()) *CloseButton {
	b := &CloseButton{}
	b.onTap = onTap
	b.ExtendBaseWidget(b)
	return b
}

func (b *CloseButton) MinSize() fyne.Size {
	return fyne.NewSize(closeButtonSize, closeButtonSize)
}

func (b *CloseButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}

func (b *CloseButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}

func (b *CloseButton) CreateRenderer() fyne.WidgetRenderer {
	line1 := canvas.NewLine(theme.Colors.XButtonNormal)
	line1.StrokeWidth = 2
	line2 := canvas.NewLine(theme.Colors.XButtonNormal)
	line2.StrokeWidth = 2
	return &closeButtonRenderer{button: b, line1: line1, line2: line2}
}

type closeButtonRenderer struct {
	button       *CloseButton
	line1, line2 *canvas.Line
}

func (r *closeButtonRenderer) Layout(size fyne.Size) {
	const padding = 6
	cx, cy := size.Width/2, size.Height/2
	half := (size.Width - 2*padding) / 2

	r.line1.Position1 = fyne.NewPos(cx-half, cy-half)
	r.line1.Position2 = fyne.NewPos(cx+half, cy+half)
	r.line2.Position1 = fyne.NewPos(cx+half, cy-half)
	r.line2.Position2 = fyne.NewPos(cx-half, cy+half)
}

func (r *closeButtonRenderer) MinSize() fyne.Size { return r.button.MinSize() }

func (r *closeButtonRenderer) Refresh() {
	col := theme.Colors.XButtonNormal
	if r.button.hovered {
		col = theme.Colors.XButtonHover
	}
	r.line1.StrokeColor = col
	r.line2.StrokeColor = col
	canvas.Refresh(r.line1)
	canvas.Refresh(r.line2)
}

func (r *closeButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.line1, r.line2}
}

func (r *closeButtonRenderer) Destroy() {}

// Avatar is a circular, tappable avatar that loads its image asynchronously.
type Avatar struct {
	tapBase
	content *fyne.Container
}

var (
	_ fyne.Tappable     = (*Avatar)(nil)
	_ desktop.Hoverable = (*Avatar)(nil)
)

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

func (a *Avatar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(a.content)
}

func (a *Avatar) MinSize() fyne.Size {
	return fyne.NewSize(theme.Sizes.MessageAvatarSize, theme.Sizes.MessageAvatarSize)
}

func (a *Avatar) MouseIn(*desktop.MouseEvent) {}
func (a *Avatar) MouseOut()                   {}
