package ui

import (
	"hash/fnv"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	// glyphButtonSize is the side length of a GlyphButton.
	glyphButtonSize = 24

	// iconRestTranslucency dims an icon button at rest, so hovering lights the icon
	// itself rather than a plate behind it.
	iconRestTranslucency = 0.45
)

/* Shared plumbing */

// tapBase is the tap, right-click and mouse-move plumbing every interactive
// widget here embeds. Embedders supply CreateRenderer and, where they want it,
// MouseIn/MouseOut — deliberately not declared here, since a no-op pair would
// take hover from every parent row.
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

// reportHover passes a hover on to an optional listener — the shape every widget
// here that both draws its own hover and hands it upward repeats.
func reportHover(onHover func(bool), on bool) {
	if onHover != nil {
		onHover(on)
	}
}

func (b *tapBase) Cursor() desktop.Cursor { return desktop.PointerCursor }

// roundedPanel is the surface the floating message controls sit on — the hover
// quick-actions and the edit save/cancel pair.
func roundedPanel() *canvas.Rectangle {
	panel := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	panel.CornerRadius = 4

	return panel
}

/* Text objects */

// newText is how every canvas.Text here is built. The fill goes through
// solidColor, so a role's gradient reaching a text object — which panics the
// painter — is structurally impossible rather than a rule each caller remembers.
// A zero size is the theme's own, as canvas.NewText takes it.
func newText(text string, fill color.Color, size float32) *canvas.Text {
	obj := canvas.NewText(text, solidColor(fill))
	if size > 0 {
		obj.TextSize = size
	}

	return obj
}

// newInitial is the letter a server icon falls back to before its picture lands
// — the same in the rail and on an invite card, so a server reads the same in
// both. Empty where there is nothing to stand for.
func newInitial(name string) *canvas.Text {
	letter := ""
	if name != "" {
		letter = strings.ToUpper(string([]rune(name)[0]))
	}

	initial := newBoldText(letter, theme.Colors.TextPrimary, 0)
	initial.Alignment = fyne.TextAlignCenter

	return initial
}

// newBoldText is the same in the one style anything here asks for.
func newBoldText(text string, fill color.Color, size float32) *canvas.Text {
	obj := newText(text, fill, size)
	obj.TextStyle = fyne.TextStyle{Bold: true}

	return obj
}

/* Edges */

// Outline edges rect with the client's one hairline, darker than anything it is
// laid over. Which rectangle carries it matters: a card sized by its own padding
// wears it on its background, but one whose content reaches its edge — a picture
// — needs it on a rectangle stacked over the content or it is painted over.
func Outline(rect *canvas.Rectangle) {
	rect.StrokeColor = theme.Colors.Outline
	rect.StrokeWidth = theme.Sizes.OutlineWidth
}

// Elevate casts rect's shadow onto what it is laid over — only the composer dock
// carries one, an outline and a margin making a card but only a shadow making one
// that is *above* something. DropShadow follows the corner radius and paints
// nothing under the fill, so a translucent shadow cannot dirty the card, and the
// blur overruns the margin on purpose: the surface behind is what has to darken.
func Elevate(rect *canvas.Rectangle) {
	rect.Shadow = canvas.Shadow{
		Color:      theme.Colors.CardShadow,
		BlurRadius: theme.Sizes.CardShadowBlur,
		Variant:    canvas.DropShadow,
	}
}

// NewColumnDivider is the seam between two columns of the main row. It belongs
// *inside* a column: the row addresses children by position to find the one that
// stretches, so a divider of its own would shift that index and would stay behind
// when the member sidebar is hidden.
func NewColumnDivider() fyne.CanvasObject { return hairline(theme.Sizes.OutlineWidth, 0) }

// NewRowDivider is the same hairline lying across a column.
func NewRowDivider() fyne.CanvasObject { return hairline(0, theme.Sizes.OutlineWidth) }

// hairline is a bar of the outline colour, thick on one axis and stretched by its
// parent on the other.
func hairline(width, height float32) fyne.CanvasObject {
	return sizedRect(theme.Colors.Outline, width, height)
}

/* Drawn glyphs */

// The client's own marks are laid out on a 20-unit grid and scaled to whatever
// square they are drawn in, so a hashtag, a stopwatch and a tinted SVG all line
// up as one set.

// glyphBox centres content in the square every one of those marks shares.
func glyphBox(content fyne.CanvasObject) fyne.CanvasObject {
	side := theme.Sizes.HashtagIconSize

	return container.NewCenter(container.NewGridWrap(fyne.NewSize(side, side), content))
}

// glyphLine is a stroke plotter on that grid, given the square's own scale.
func glyphLine(fill color.Color, scale float32) func(x1, y1, x2, y2 float32) *canvas.Line {
	return func(x1, y1, x2, y2 float32) *canvas.Line {
		line := canvas.NewLine(fill)
		line.Position1 = fyne.NewPos(x1*scale, y1*scale)
		line.Position2 = fyne.NewPos(x2*scale, y2*scale)
		line.StrokeWidth = 2 * scale

		return line
	}
}

/* Chips */

// NewChip is one small rounded label in its own colour — a badge or a count.
func NewChip(text string, tint color.Color) fyne.CanvasObject {
	return newChip(nil, text, tint)
}

// NewTappableChip is a chip that leads somewhere. Lighting under the pointer is
// the whole of what tells it from a plain one beside it — a mutual profile draws
// both, the names it resolved and a "+n" for the ones it could not.
func NewTappableChip(text string, tint color.Color, onTap func()) fyne.CanvasObject {
	c := &tappableChip{}
	c.content, c.background = chipParts(nil, text, tint)
	c.onTap = onTap
	c.ExtendBaseWidget(c)

	return c
}

// tappableChip is NewChip's surface with a click on it — a widget of its own
// rather than a TappableContainer, whose square hover fill behind a rounded label
// reads as a second shape appearing rather than the chip responding.
type tappableChip struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*tappableChip)(nil)
	_ desktop.Hoverable = (*tappableChip)(nil)
)

func (c *tappableChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *tappableChip) MouseIn(*desktop.MouseEvent) {
	c.background.FillColor = theme.Colors.ChipHoverBg
	c.background.Refresh()
}

func (c *tappableChip) MouseOut() {
	c.background.FillColor = theme.Colors.ChipBg
	c.background.Refresh()
}

/* Status lines */

// StatusLine is the one place a screen with no notice layer can report an
// outcome: login and the second factor are both up before NoticeStack is built,
// and a Fyne error dialog is the one surface AppTheme does not reach.
//
// A widget.Label rather than a canvas.Text: a transport error is a long sentence
// and has to wrap, and Importance colours text without holding a colour a restyle
// would leave stale.
type StatusLine struct {
	// Content is the object to mount, kept apart from the label so a caller need
	// not know what the line is made of.
	Content fyne.CanvasObject

	label *widget.Label
}

// NewStatusLine builds an empty line. It is mounted empty rather than added on
// demand, so a message appearing does not move what is under it.
func NewStatusLine() *StatusLine {
	label := widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{})
	label.Wrapping = fyne.TextWrapWord

	return &StatusLine{Content: label, label: label}
}

// Fail reports something that went wrong. Call on the UI thread.
func (s *StatusLine) Fail(message string) { s.set(message, widget.DangerImportance) }

// Notice reports something that did not. Call on the UI thread.
func (s *StatusLine) Notice(message string) { s.set(message, widget.MediumImportance) }

// Clear empties the line without taking its height back.
func (s *StatusLine) Clear() { s.set("", widget.MediumImportance) }

func (s *StatusLine) set(message string, importance widget.Importance) {
	s.label.Importance = importance
	s.label.SetText(message)
}

/* Bot mark */

// NewBotMark is the glyph that says an account is a bot, drawn after its name. A
// glyph rather than a lettered chip: a column of identical words reads as labels
// rather than as a property of the names beside them. The side is the caller's —
// a member row's name and a profile heading are set differently.
func NewBotMark(side float32) fyne.CanvasObject {
	return container.NewCenter(newScaledIcon(tintedIcon(assets.BotIcon, theme.Colors.BotMark), side))
}

// RoleChip is a role drawn as a chip: a dot in its colour beside its name. The
// whole chip answers the right-click — the dot is a few pixels across.
type RoleChip struct {
	tapBase

	content fyne.CanvasObject
}

var _ fyne.SecondaryTappable = (*RoleChip)(nil)

// NewRoleChip draws role as a chip, right-clickable for its name and ID. An
// uncoloured role falls back to the primary text colour, dot included, so the
// shape is the same wherever the chip is used.
func NewRoleChip(role domain.Role) *RoleChip {
	tint := theme.Colors.TextPrimary
	if role.Color != nil {
		tint = role.Color
	}

	w := &RoleChip{content: newChip(newChipDot(tint), role.Name, tint)}
	w.onSecondaryTap = func(e *fyne.PointEvent) { ShowContextMenu(w, roleMenu(role), e.AbsolutePosition) }
	w.ExtendBaseWidget(w)

	return w
}

func (w *RoleChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

// roleMenu is what right-clicking a role offers. One built from a name alone
// carries no ID, and leaves that item out rather than offering an empty copy.
func roleMenu(role domain.Role) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Copy role name", func() { CopyToClipboard(role.Name) }),
	}
	if role.ID != "" {
		items = append(items, fyne.NewMenuItem("Copy role ID", func() { CopyToClipboard(role.ID) }))
	}

	return items
}

// newChip assembles the chip: an optional leading mark, then the text, on a
// rounded surface.
func newChip(mark fyne.CanvasObject, text string, tint color.Color) fyne.CanvasObject {
	chip, _ := chipParts(mark, text, tint)

	return chip
}

// chipParts builds the surface and hands the background back beside it, which a
// tappable chip needs to recolour on hover.
func chipParts(mark fyne.CanvasObject, text string, tint color.Color) (fyne.CanvasObject, *canvas.Rectangle) {
	background := canvas.NewRectangle(theme.Colors.ChipBg)
	background.CornerRadius = theme.Sizes.ChipRadius

	label := newBoldText(text, tint, theme.Sizes.ChipTextSize)

	var content fyne.CanvasObject = container.NewCenter(label)
	if mark != nil {
		content = HBoxNoSpacing(mark, HorizontalSpacer(theme.Sizes.ChipDotGap), content)
	}

	padV, padH := theme.Sizes.ChipPaddingV, theme.Sizes.ChipPaddingH

	return container.NewStack(background, NewInset(content, padV, padV, padH, padH)), background
}

// newChipDot is the leading dot, the one thing in a chip carrying the hairline —
// which is what lifts a saturated colour off the surface behind it. Pinned to its
// own square and centred twice: a row layout stretches, and a stretched circle is
// an ellipse.
func newChipDot(fill color.Color) fyne.CanvasObject {
	size := theme.Sizes.ChipDotSize

	dot := canvas.NewCircle(fill)
	dot.StrokeColor = theme.Colors.Outline
	dot.StrokeWidth = theme.Sizes.OutlineWidth

	return container.NewCenter(container.NewGridWrap(fyne.NewSize(size, size), dot))
}

/* Containers */

// TappableContainer wraps content, highlighting a background on hover and
// invoking onTap when clicked.
type TappableContainer struct {
	tapBase

	// Menu supplies the items right-clicking offers. The option for rows that are a
	// plain container rather than a widget of their own.
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

// TappedSecondary raises Menu's items when set, else falls back to tapBase's
// handler — a reply preview hands its right-click to the message around it.
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

// NewHoverableStack makes content tappable with an outline and an optional hover
// callback. The rectangle is stacked *over* the content, which is what lets an
// attachment be framed: the picture reaches the card's edge, so a border behind
// it is painted over. Drawn at rest, so hover must lift the stroke, not replace it.
func NewHoverableStack(content fyne.CanvasObject, onTap func(), onHover func(bool)) *HoverableStack {
	h := &HoverableStack{
		background: canvas.NewRectangle(color.Transparent),
		content:    content,
		onHover:    onHover,
	}
	Outline(h.background)
	h.onTap = onTap
	h.ExtendBaseWidget(h)

	return h
}

func (h *HoverableStack) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(h.content, h.background))
}

func (h *HoverableStack) MouseIn(*desktop.MouseEvent) { h.setHovered(true) }
func (h *HoverableStack) MouseOut()                   { h.setHovered(false) }

func (h *HoverableStack) setHovered(on bool) {
	h.background.StrokeColor = theme.Colors.Outline
	if on {
		h.background.StrokeColor = theme.Colors.AttachmentHoverBorder
	}
	h.background.Refresh()

	reportHover(h.onHover, on)
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

func (b *IconButton) MouseIn(*desktop.MouseEvent) { b.setHovered(true) }
func (b *IconButton) MouseOut()                   { b.setHovered(false) }

func (b *IconButton) setHovered(on bool) {
	b.icon.Translucency = iconRestTranslucency
	if on {
		b.icon.Translucency = 0
	}
	b.icon.Refresh()

	reportHover(b.onHover, on)
}

// SidebarButton is the circular icon button bookending the server list as the
// fixed home and settings entries, in the server rows' own colours.
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

// SetSelected marks the button as the active view. A no-op when unchanged, so a
// sidebar-wide sync only repaints what moved.
func (b *SidebarButton) SetSelected(selected bool) {
	if b.selected == selected {
		return
	}

	b.selected = selected
	b.refreshAppearance()
}

// refreshAppearance repaints the circle. Selection outranks hover, so hovering
// the active view does not dim it.
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

// GlyphButton is a square, icon-only button that fills under the pointer: the
// chrome a card wears rather than an action it offers. Closing one is what it
// mostly is, hence the constructor that names no icon.
type GlyphButton struct {
	tapBase
	background *canvas.Rectangle
	icon       *canvas.Image
}

var (
	_ fyne.Tappable     = (*GlyphButton)(nil)
	_ desktop.Hoverable = (*GlyphButton)(nil)
)

// NewCloseButton creates a close button with the given tap handler.
func NewCloseButton(onTap func()) *GlyphButton {
	return NewGlyphButton(fynetheme.CancelIcon(), onTap)
}

// NewGlyphButton is the same button wearing res — the way into a card's menu,
// drawn on its banner beside the way out.
func NewGlyphButton(res fyne.Resource, onTap func()) *GlyphButton {
	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = 4

	b := &GlyphButton{background: background, icon: newScaledIcon(res, 0)}
	b.onTap = onTap
	b.ExtendBaseWidget(b)

	return b
}

func (b *GlyphButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.background, container.NewPadded(b.icon)))
}

func (b *GlyphButton) MinSize() fyne.Size {
	return fyne.NewSize(glyphButtonSize, glyphButtonSize)
}

func (b *GlyphButton) MouseIn(*desktop.MouseEvent) {
	b.background.FillColor = theme.Colors.SwiftActionHoverBg
	b.background.Refresh()
}

func (b *GlyphButton) MouseOut() {
	b.background.FillColor = color.Transparent
	b.background.Refresh()
}

/* Tooltips */

// Tooltip is the floating label an icon-only control shows on hover. Layer goes
// over the whole window so the label can overhang the column that triggered it.
// Not a Fyne pop-up: pushing an overlay routes the whole hit test into it, so the
// hovered widget never sees MouseOut and the tooltip never comes down.
type Tooltip struct {
	Layer *fyne.Container // stack this over the main layout

	card  *fyne.Container
	label *canvas.Text
}

// NewTooltip builds an empty, hidden tooltip.
func NewTooltip() *Tooltip {
	label := newBoldText("", theme.Colors.TextPrimary, theme.Sizes.TooltipTextSize)

	background := canvas.NewRectangle(theme.Colors.TooltipBg)
	background.CornerRadius = theme.Sizes.TooltipRadius

	padV, padH := theme.Sizes.TooltipPaddingV, theme.Sizes.TooltipPaddingH
	card := container.NewStack(background, NewInset(label, padV, padV, padH, padH))
	card.Hide()

	// Nothing in the layer accepts an event, so it takes none from underneath, and
	// NewLayer keeps the name it holds out of the window's minimum size. The card
	// places itself, hence the unlaid-out container the layer can fill freely.
	return &Tooltip{Layer: NewLayer(container.NewWithoutLayout(card)), card: card, label: label}
}

// Show names obj, placing the label just past its right edge and centred on it.
// An empty name hides the tooltip instead.
func (t *Tooltip) Show(text string, obj fyne.CanvasObject) {
	anchor, size, ok := t.prepare(text, obj)
	if !ok {
		return
	}

	t.place(fyne.NewPos(
		anchor.X+obj.Size().Width+theme.Sizes.TooltipGap,
		anchor.Y+(obj.Size().Height-size.Height)/2,
	))
}

// ShowAbove centres the label over obj, clamped to the layer's width and dropped
// below where there is no room above. What a cell in a grid needs: it has
// neighbours either side, so a label past its right edge names the wrong one.
func (t *Tooltip) ShowAbove(text string, obj fyne.CanvasObject) {
	anchor, size, ok := t.prepare(text, obj)
	if !ok {
		return
	}

	x := clamp(anchor.X+(obj.Size().Width-size.Width)/2, 0, max(t.Layer.Size().Width-size.Width, 0))

	y := anchor.Y - size.Height - theme.Sizes.TooltipGap
	if y < 0 {
		y = anchor.Y + obj.Size().Height + theme.Sizes.TooltipGap
	}

	t.place(fyne.NewPos(x, y))
}

// prepare labels the card and measures it, reporting where obj sits inside the
// layer — the difference of two canvas-absolute positions, so it holds wherever
// the layer itself sits.
func (t *Tooltip) prepare(text string, obj fyne.CanvasObject) (fyne.Position, fyne.Size, bool) {
	if text == "" {
		t.Hide()
		return fyne.Position{}, fyne.Size{}, false
	}

	t.label.Text = text
	t.label.Refresh()

	driver := fyne.CurrentApp().Driver()
	anchor := driver.AbsolutePositionForObject(obj).Subtract(driver.AbsolutePositionForObject(t.Layer))

	size := t.card.MinSize()
	t.card.Resize(size)

	return anchor, size, true
}

func (t *Tooltip) place(pos fyne.Position) {
	t.card.Move(pos)
	t.card.Show()
	t.card.Refresh() // Show alone neither lays the card out nor repaints it
}

// Hide takes the tooltip down. Safe to call when nothing is showing.
func (t *Tooltip) Hide() { t.card.Hide() }

/* Avatars */

// circularAvatar builds a circular avatar of the given size, loading the image
// from avatarURL when present. It is the one place an avatar image is mounted.
func circularAvatar(images *cache.ImageCache, avatarURL string, size fyne.Size) *fyne.Container {
	avatar, _ := newAvatarSlot(size)
	loadAvatar(images, avatar, avatarURL, size)

	return avatar
}

// newAvatarSlot is the same circle with nothing loaded into it, for the member
// list's recycled rows: a picture asked for at construction has no generation to
// check itself against and lands on whoever the row has moved on to.
//
// The placeholder comes back beside the slot so a row swapping a picture out can
// restore *that* object — Fyne only learns of an object when the container
// holding it is refreshed, so a fresh circle draws nothing at all.
func newAvatarSlot(size fyne.Size) (*fyne.Container, *canvas.Circle) {
	placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)

	return container.NewGridWrap(size, placeholder), placeholder
}

// loadAvatar kicks off the circular image load for an already-built avatar
// container.
func loadAvatar(images *cache.ImageCache, target *fyne.Container, avatarURL string, size fyne.Size) {
	if avatarURL == "" {
		return
	}

	images.LoadIntoContainer(imageCacheID(avatarURL), avatarURL, size, target, true, nil)
}

// imageCacheID is the key a picture is cached under: its Autumn file ID for one
// of Revolt's URLs, a hash otherwise. The ID doubles as the disk cache's
// filename, and an embed preview's URL — scheme and slashes — is no filename.
func imageCacheID(imageURL string) string {
	if imageURL == "" {
		return ""
	}
	if id := util.IDFromAttachmentURL(imageURL); id != "" {
		return id
	}

	sum := fnv.New64a()
	sum.Write([]byte(imageURL))

	return strconv.FormatUint(sum.Sum64(), 16)
}

// Avatar is the circular, tappable avatar beside a message. Deliberately not
// hoverable — innermost wins, so it would take hover from the message row and the
// quick-actions would vanish whenever the pointer crossed it.
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
// Read per event so the setting takes effect without a rebuild.
func scrollAmplify() float32 {
	return float32(config.Current().Behaviour.ScrollSpeed)
}

const (
	// scrollIndicatorLinger is how long the indicator stays up after the last
	// movement, scrollIndicatorFade how long it takes to leave.
	scrollIndicatorLinger = 700 * time.Millisecond
	scrollIndicatorFade   = 350 * time.Millisecond
)

// ObservableScroll is a vertical scroll container that reports offset changes,
// supports middle-mouse panning, and draws its own position indicator.
type ObservableScroll struct {
	container.Scroll
	OnScroll func(offset fyne.Position)

	/* The position indicator */

	indicator  *canvas.Rectangle
	lastOffset fyne.Position
	linger     *time.Timer
	fade       *fyne.Animation

	panning bool
}

var _ fyne.Draggable = (*ObservableScroll)(nil)

// NewObservableVScroll creates an observable vertical scroll container, drawing
// the client's own position indicator down its right edge.
func NewObservableVScroll(content fyne.CanvasObject) *ObservableScroll {
	s := &ObservableScroll{indicator: canvas.NewRectangle(theme.Colors.ScrollIndicator)}
	s.indicator.Hide()

	return s.init(content)
}

// NewPlainVScroll is the same scroll without the indicator, for a column whose
// content reaches the right edge the strip would be drawn over — the settings
// pane centres its cards, so a narrow window puts the indicator on one.
func NewPlainVScroll(content fyne.CanvasObject) *ObservableScroll {
	return new(ObservableScroll).init(content)
}

func (s *ObservableScroll) init(content fyne.CanvasObject) *ObservableScroll {
	s.Direction = container.ScrollVerticalOnly
	s.Content = content
	s.ExtendBaseWidget(s)

	return s
}

// CreateRenderer adds the indicator to Fyne's own scroll renderer. A plain
// rectangle rather than Fyne's bar, which comes wrapped in a hover-accepting area
// over the content's right edge and — innermost — takes the message row's hover.
// AppTheme zeroes both of its sizes; this draws what they used to.
func (s *ObservableScroll) CreateRenderer() fyne.WidgetRenderer {
	base := s.Scroll.CreateRenderer()

	objects := base.Objects()
	if s.indicator != nil {
		objects = slices.Concat(objects, []fyne.CanvasObject{s.indicator})
	}

	return &scrollRenderer{WidgetRenderer: base, scroll: s, objects: objects}
}

// scrollRenderer is Fyne's scroll renderer with the indicator drawn last, over
// the content. Composing the object list once holds because the renderer
// underneath sets its own when it is built and never replaces it.
type scrollRenderer struct {
	fyne.WidgetRenderer

	scroll  *ObservableScroll
	objects []fyne.CanvasObject
}

func (r *scrollRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *scrollRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	r.scroll.placeIndicator()
}

// Refresh reveals the indicator rather than Scrolled doing it: every offset
// change ends here whoever asked for it. The offset is compared rather than
// trusted, so a repaint for any other reason does not flash the bar.
func (r *scrollRenderer) Refresh() {
	r.WidgetRenderer.Refresh()

	moved := r.scroll.Offset != r.scroll.lastOffset
	r.scroll.lastOffset = r.scroll.Offset

	if r.scroll.placeIndicator() && moved {
		r.scroll.revealIndicator()
	}
}

// Destroy stops the fade with the widget: a restyle rebuilds the tree, dropping
// the scroll while an animation could still be running against it.
func (r *scrollRenderer) Destroy() {
	r.scroll.stopFade()
	if r.scroll.linger != nil {
		r.scroll.linger.Stop()
	}

	r.WidgetRenderer.Destroy()
}

// SyncContent resizes the content to what it now measures. Fyne does this from
// its renderer's Layout, which runs on a Refresh — and refreshing a mounted
// message column re-wraps every body, which is why nothing here refreshes after a
// mount. Without it ScrollToOffset clamps against the pre-mount size, so a column
// grown to a page of history scrolls as though it still fitted the viewport. Only
// a caller that mounts and scrolls in one pass needs it.
func (s *ObservableScroll) SyncContent() {
	if s.Content == nil {
		return
	}

	s.Content.Resize(s.Content.MinSize().Max(s.Size()))
}

// placeIndicator sizes the bar to the fraction of content in view and moves it to
// where that fraction sits, reporting whether there is anything to indicate. The
// extent comes from Content.Size(), never MinSize — that is a walk of every
// mounted row, and this runs on the scroll path.
func (s *ObservableScroll) placeIndicator() bool {
	if s.indicator == nil || s.Content == nil {
		return false
	}

	view := s.Size()
	content := s.Content.Size().Height
	if view.Height <= 0 || content <= view.Height {
		s.indicator.Hide()
		return false
	}

	width := theme.Sizes.ScrollIndicatorWidth
	inset := theme.Sizes.ScrollIndicatorInset
	track := view.Height - inset*2

	// The floor is applied first, so a viewport shorter than the minimum still gets
	// a bar no taller than its track.
	height := min(max(track*view.Height/content, theme.Sizes.ScrollIndicatorMinHeight), track)
	progress := clamp(s.Offset.Y/(content-view.Height), 0, 1)

	s.indicator.CornerRadius = width / 2
	s.indicator.Resize(fyne.NewSize(width, height))
	s.indicator.Move(fyne.NewPos(view.Width-width-inset, inset+progress*(track-height)))

	return true
}

// revealIndicator brings the bar back to full strength and re-arms its exit.
func (s *ObservableScroll) revealIndicator() {
	s.stopFade()

	s.indicator.FillColor = theme.Colors.ScrollIndicator
	s.indicator.Show()
	canvas.Refresh(s.indicator)

	if s.linger == nil {
		s.linger = time.AfterFunc(scrollIndicatorLinger, func() { DoOnUI(s.fadeIndicator) })
		return
	}
	s.linger.Reset(scrollIndicatorLinger)
}

// fadeIndicator takes the bar out. Only the linger timer calls it, so a movement
// arriving mid-fade stops it through revealIndicator.
func (s *ObservableScroll) fadeIndicator() {
	s.stopFade()

	s.fade = fyne.NewAnimation(scrollIndicatorFade, func(done float32) {
		s.indicator.FillColor = theme.Fade(theme.Colors.ScrollIndicator, 1-done)
		canvas.Refresh(s.indicator)
	})
	s.fade.Start()
}

func (s *ObservableScroll) stopFade() {
	if s.fade == nil {
		return
	}

	s.fade.Stop()
	s.fade = nil
}

// Scrolled amplifies the wheel delta and notifies listeners.
func (s *ObservableScroll) Scrolled(ev *fyne.ScrollEvent) {
	amplified := *ev
	speed := scrollAmplify()
	amplified.Scrolled.DX *= speed
	amplified.Scrolled.DY *= speed

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

	// ScrollToOffset rather than writing Offset and refreshing: Scroll.Refresh
	// repaints every descendant, which for a pan is the whole column per frame.
	s.ScrollToOffset(fyne.NewPos(s.Offset.X-ev.Dragged.DX, s.Offset.Y-ev.Dragged.DY))
	s.notify()
}

// DragEnd completes fyne.Draggable. Without it the driver never recognises the
// scroll as draggable and Dragged is never called at all.
func (s *ObservableScroll) DragEnd() { s.panning = false }

func (s *ObservableScroll) notify() {
	if s.OnScroll != nil {
		s.OnScroll(s.Offset)
	}
}

/* Text metrics */

// textAscentRatio is how much of a line sits above its baseline. Fyne exposes no
// font metrics, so it is taken as fixed — one font throughout, and only the
// difference between two line heights is ever scaled by it.
const textAscentRatio = 0.8

// baselineOffset is how far down text at size small must start to share a
// baseline with text at large beside it. Two sizes handed the same height centre
// against each other instead, leaving the smaller one riding high.
func baselineOffset(large, small float32) float32 {
	return textAscentRatio * (lineHeight(large) - lineHeight(small))
}

/* Accented text */

// AccentText is a name drawn in a role's colour. A text object takes one colour,
// so a gradient across a word is a gradient across its letters: a flat fill
// mounts one canvas.Text, a gradient one per rune. That split is why it is a
// widget — a name with no gradient, nearly every one, still mounts a single
// object, so the message list pays nothing for the ones that do.
type AccentText struct {
	widget.BaseWidget

	content *fyne.Container
	layout  *accentLayout

	text  string
	fill  color.Color
	size  float32
	style fyne.TextStyle
}

// NewAccentText draws text in fill, which may be a domain.Gradient. A zero size
// is the theme's own, as canvas.NewText takes it.
func NewAccentText(text string, fill color.Color, size float32, style fyne.TextStyle) *AccentText {
	if size == 0 {
		size = fynetheme.TextSize()
	}

	t := &AccentText{layout: &accentLayout{}, text: text, fill: fill, size: size, style: style}
	t.content = container.New(t.layout)
	t.build()
	t.ExtendBaseWidget(t)

	return t
}

func (t *AccentText) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

// Text is what the widget currently draws, which is what Fit left of it.
func (t *AccentText) Text() string { return t.text }

// Set re-labels the text, for an author who resolved after the widget was
// mounted. Nothing is rebuilt when neither the name nor the colour has moved.
func (t *AccentText) Set(text string, fill color.Color) {
	if t.text == text && sameColor(t.fill, fill) {
		return
	}

	t.text, t.fill = text, fill
	t.build()
	t.content.Refresh()
	t.Refresh()
}

// Fit shortens the text to width, ending it in an ellipsis, so a long name
// cannot widen the line it shares. Call before mounting: it shortens the string
// itself rather than re-fitting to whatever width it is later given.
func (t *AccentText) Fit(width float32) {
	fitted := TruncateToWidth(t.text, width, t.size, t.style)
	if fitted == t.text {
		return
	}

	t.text = fitted
	t.build()
}

// build mounts the text objects the current name and fill need: one, or one per
// rune, each filled where its glyph falls along the run.
//
// Offsets are measured off the *whole* name up to each rune rather than summed
// from the runes, so the split run lands exactly where the unsplit one would —
// summing single glyphs drifts enough to move the timestamp beside a name. The
// centres are then stretched over the whole gradient, or the outermost letters
// stop short of the stops they exist to show.
func (t *AccentText) build() {
	t.layout.size = fyne.MeasureText(t.text, t.size, t.style)

	stops, gradient := t.fill.(domain.Gradient)
	if !gradient || len(stops) < 2 || t.text == "" {
		t.layout.offsets = []float32{0}
		t.content.Objects = []fyne.CanvasObject{t.newText(t.text, t.fill)}

		return
	}

	runes := []rune(t.text)
	t.layout.offsets = make([]float32, len(runes))
	for i := range runes {
		t.layout.offsets[i] = fyne.MeasureText(string(runes[:i]), t.size, t.style).Width
	}

	centre := func(i int) float32 {
		end := t.layout.size.Width
		if i+1 < len(runes) {
			end = t.layout.offsets[i+1]
		}

		return (t.layout.offsets[i] + end) / 2
	}
	span := centre(len(runes)-1) - centre(0)

	objects := make([]fyne.CanvasObject, len(runes))
	for i, r := range runes {
		at := 0.5 // one glyph has no run to spread over, so it takes the middle
		if span > 0 {
			at = float64((centre(i) - centre(0)) / span)
		}
		objects[i] = t.newText(string(r), stops.At(at))
	}

	t.content.Objects = objects
}

// accentLayout places each glyph at the offset the name measures up to it, and
// reports the name's own measurement whatever it was split into.
type accentLayout struct {
	offsets []float32
	size    fyne.Size
}

func (l *accentLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for i, child := range objects {
		if i >= len(l.offsets) {
			return
		}

		end := l.size.Width
		if i+1 < len(l.offsets) {
			end = l.offsets[i+1]
		}

		child.Resize(fyne.NewSize(end-l.offsets[i], size.Height))
		child.Move(fyne.NewPos(l.offsets[i], 0))
	}
}

func (l *accentLayout) MinSize([]fyne.CanvasObject) fyne.Size { return l.size }

func (t *AccentText) newText(text string, fill color.Color) *canvas.Text {
	if fill == nil {
		fill = theme.Colors.TextPrimary
	}

	obj := newText(text, fill, t.size)
	obj.TextStyle = t.style

	return obj
}

// solidColor flattens fill to something a canvas.Text can be drawn in. Fyne keys
// its glyph cache on the text object's fields, colour included, so a fill that
// cannot be a map key — domain.Gradient is a slice — panics the painter mid-frame.
// Shapes are safe: their texture is keyed by the object, not by the fill.
//
// What comes back is the gradient's own mean, as anywhere it is used flat. Only
// AccentText spreads one across text, and it gives each rune a stop of its own.
func solidColor(fill color.Color) color.Color {
	if _, gradient := fill.(domain.Gradient); !gradient {
		return fill
	}

	// RGBA64 holds RGBA's premultiplied result exactly; NRGBA would have to divide
	// the alpha back out.
	r, g, b, a := fill.RGBA()

	return color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(b), A: uint16(a)}
}

// sameColor compares two fills without ==, which panics on a domain.Gradient.
func sameColor(first, second color.Color) bool {
	firstStops, firstGradient := first.(domain.Gradient)
	secondStops, secondGradient := second.(domain.Gradient)
	if firstGradient || secondGradient {
		return firstGradient && secondGradient && slices.EqualFunc(firstStops, secondStops, sameColor)
	}
	if first == nil || second == nil {
		return first == nil && second == nil
	}

	firstR, firstG, firstB, firstA := first.RGBA()
	secondR, secondG, secondB, secondA := second.RGBA()

	return firstR == secondR && firstG == secondG && firstB == secondB && firstA == secondA
}

/* Shortened text */

// ellipsis marks a shortened label. One glyph rather than three dots, so the
// text it replaces stays as long as possible.
const ellipsis = "…"

// NewEllipsisText shortens a single-line canvas.Text to whatever width it is
// given. Its minimum width is zero, which is the point: a sidebar row must not
// widen its column because someone has a long name. Only works in a slot that
// hands its child real width — a Border's centre, not an HBox.
func NewEllipsisText(text *canvas.Text) *fyne.Container {
	return container.New(&ellipsisLayout{text: text, full: text.Text}, text)
}

// SetEllipsisText re-labels a box built by NewEllipsisText, whose full text is
// otherwise fixed at construction — right for a row built per person, wrong for a
// recycled one. Reading the name back off the text object would take whatever
// last fitted the column for the real one.
func SetEllipsisText(box *fyne.Container, text string) {
	layout, ok := box.Layout.(*ellipsisLayout)
	if !ok {
		return
	}

	layout.full = text
	layout.sized = false // the fit is for the previous name, whatever the width
	Relayout(box)
}

// ellipsisLayout re-fits its text to the width it is handed and centres it.
// Rewriting text during Layout is safe because the reported minimum does not
// depend on the content — zero wide, one line tall — so it cannot trigger another
// pass. Both derived values are cached: Layout and MinSize run per pass over every
// row of both sidebars, while the answers change only on a resize.
type ellipsisLayout struct {
	text *canvas.Text
	full string

	width  float32 // the width fitted was derived for
	fitted string
	sized  bool // fitted is meaningful, rather than the zero value for a zero width
	height float32
}

func (l *ellipsisLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	if !l.sized || l.width != size.Width {
		l.fitted = TruncateToWidth(l.full, size.Width, l.text.TextSize, l.text.TextStyle)
		l.width, l.sized = size.Width, true
	}
	if l.fitted != l.text.Text {
		l.text.Text = l.fitted
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
	if l.height == 0 {
		l.height = fyne.MeasureText("W", l.text.TextSize, l.text.TextStyle).Height
	}

	return l.height
}

// TruncateToWidth shortens text to fit width when rendered, appending an ellipsis
// when anything was dropped. Unlike util.Truncate it measures rather than counts
// runes — in a proportional font the same count is a different width. Binary
// search because this runs on every layout pass.
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

// WrapToWidth breaks text into lines no wider than width, breaking mid-word where
// a word does not fit a line of its own — the alternative is a line overhanging
// what sits beside it. A width of zero or less returns the text whole.
func WrapToWidth(text string, width, size float32, style fyne.TextStyle) []string {
	words := strings.Fields(text)
	if width <= 0 || len(words) == 0 {
		return []string{text}
	}

	space := spaceWidth(size, style)
	lines := make([]string, 0, 2)

	line, extent := "", float32(0)
	for _, word := range words {
		measured := fyne.MeasureText(word, size, style).Width

		switch {
		case line == "":
			line, extent = word, measured
		case extent+space+measured <= width:
			line, extent = line+" "+word, extent+space+measured
		default:
			lines = append(lines, line)
			line, extent = word, measured
		}

		// A word wider than the column is cut where it stops fitting and carries on
		// over as many lines as it takes.
		for extent > width {
			head, tail := splitToWidth(line, width, size, style)
			if tail == "" {
				break
			}
			lines = append(lines, head)
			line, extent = tail, fyne.MeasureText(tail, size, style).Width
		}
	}

	return append(lines, line)
}

// splitToWidth cuts text at the last rune that still fits inside width, keeping
// at least one so a column narrower than a single glyph still terminates.
func splitToWidth(text string, width, size float32, style fyne.TextStyle) (head, tail string) {
	runes := []rune(text)
	for i := 1; i < len(runes); i++ {
		if fyne.MeasureText(string(runes[:i+1]), size, style).Width > width {
			return string(runes[:i]), string(runes[i:])
		}
	}

	return text, ""
}

// NewWrappedText is a block of prose wrapped at width — a canvas.Text per line
// rather than a wrapping widget.Label, which cannot be asked how tall it wants to
// be before it has a width and answers with whatever it was last laid out at.
// Every caller knows its width, so the block's minimum is true from the first pass.
func NewWrappedText(text string, width, size float32, colour color.Color) fyne.CanvasObject {
	lines := WrapToWidth(text, width, size, fyne.TextStyle{})

	objects := make([]fyne.CanvasObject, 0, len(lines))
	for _, line := range lines {
		objects = append(objects, newText(line, colour, size))
	}

	return VBoxNoSpacing(objects...)
}

/* Typing mark */

const (
	// typingSweepPeriod is one full there-and-back of the line.
	typingSweepPeriod = time.Second

	// typingSweepSegments is the head and its trail, typingSweepLag how far behind
	// — in fractions of a cycle — each following segment runs. Lagging in *time*
	// rather than in space is what puts the trail on the head's own path: it bunches
	// at each turn and draws out across the middle, with nothing to clamp.
	typingSweepSegments = 4
	typingSweepLag      = 0.075

	// typingSweepFade is how much of the colour ahead of it each segment keeps.
	typingSweepFade = 0.55
)

// TypingMark is the sweeping line that says somebody is composing, mounted above
// the composer card and on a sidebar channel row.
//
// A widget rather than a container of rectangles because the sweep must stop when
// the tree holding it goes away, and only a renderer hears that — refreshChannelList
// rebuilds every row, so an animation left running would tick for the life of the
// process. It accepts no pointer event, so hover and right-click reach what it is
// drawn over.
type TypingMark struct {
	widget.BaseWidget

	segments []*canvas.Rectangle
	content  *fyne.Container

	travel float32 // how far the head's left edge moves, end to end

	sweep *fyne.Animation
}

// NewTypingMark creates the mark at a given width, hidden and still. The width is
// the caller's because the two surfaces are set differently: the composer's line
// is body-sized, a sidebar row's mark is smaller than its name.
func NewTypingMark(width float32, tint color.Color) *TypingMark {
	m := &TypingMark{}

	// Hand-laid on the 20-unit grid the client's glyphs use, so proportions hold at
	// any width and the sweep moves a rectangle with no layout pass behind it. Nine
	// units by three leaves eleven to travel, which is what has to read at a glance.
	// The box is only as tall as the line — both callers centre it in their own row.
	scale := width / 20
	barWidth, barHeight := 9*scale, 3*scale

	m.travel = width - barWidth

	alpha := float32(1)
	m.segments = make([]*canvas.Rectangle, typingSweepSegments)
	for i := range m.segments {
		bar := canvas.NewRectangle(typingTrailTint(tint, alpha))
		bar.CornerRadius = barHeight / 2
		bar.Resize(fyne.NewSize(barWidth, barHeight))
		bar.Move(fyne.NewPos(m.travel/2, 0))

		m.segments[i] = bar
		alpha *= typingSweepFade
	}

	// Farthest behind first: a container paints in order, and the opaque head has to
	// land over its own trail.
	stacked := make([]fyne.CanvasObject, len(m.segments))
	for i, bar := range m.segments {
		stacked[len(m.segments)-1-i] = bar
	}

	m.content = container.NewGridWrap(fyne.NewSize(width, barHeight),
		container.NewWithoutLayout(stacked...))

	m.Hide()
	m.ExtendBaseWidget(m)

	return m
}

func (m *TypingMark) CreateRenderer() fyne.WidgetRenderer {
	return &typingMarkRenderer{WidgetRenderer: widget.NewSimpleRenderer(m.content), mark: m}
}

// SetActive shows the mark and starts the sweep, or hides and stops it. A no-op
// when unchanged, so a sidebar repaint costs nothing per row that did not move.
//
// A hidden widget must never keep an animation — it would repaint nothing sixty
// times a second — and neither may a still one: animate off rests the line in the
// middle of its travel and starts nothing.
func (m *TypingMark) SetActive(active, animate bool) {
	if !active {
		if m.Visible() || m.sweep != nil {
			m.stop()
			m.Hide()
		}
		return
	}

	// animate only counts while the mark is up: hidden, still and stopped are one
	// state, which is why it is not compared on the way out.
	if m.Visible() && animate == (m.sweep != nil) {
		return
	}

	m.stop()
	m.Show()

	if animate {
		m.start()
	}
}

// start runs the sweep. Nothing refreshes: canvas.Rectangle.Move repaints for
// itself, and moving is all this does — the trail's colours are fixed at
// construction. Linear because the shaping is in typingSweepAt; an eased curve
// would also stutter at every repeat, where the line is travelling fastest.
func (m *TypingMark) start() {
	m.sweep = fyne.NewAnimation(typingSweepPeriod, func(done float32) {
		for i, bar := range m.segments {
			at := typingSweepAt(done - float32(i)*typingSweepLag)
			bar.Move(fyne.NewPos(m.travel*at, 0))
		}
	})

	m.sweep.Curve = fyne.AnimationLinear
	m.sweep.RepeatCount = fyne.AnimationRepeatForever
	m.sweep.Start()
}

// stop halts the sweep and rests the line mid-travel, the trail collapsed under
// the opaque head. Nil-safe and idempotent: every start goes through it.
func (m *TypingMark) stop() {
	if m.sweep == nil {
		return
	}

	m.sweep.Stop()
	m.sweep = nil

	for _, bar := range m.segments {
		bar.Move(fyne.NewPos(m.travel/2, 0))
	}
}

// typingTrailTint dims the mark's colour for a segment behind the head. Not
// theme.Fade, which scales a color.RGBA's alpha and leaves channels Go defines as
// *already multiplied by* it — faded that way a colour composites brighter, so
// the tail came out lighter than the line casting it.
func typingTrailTint(c color.Color, alpha float32) color.Color {
	r, g, b, a := c.RGBA()
	scale := func(channel uint32) uint8 { return uint8(float32(channel>>8) * alpha) }

	return color.RGBA{R: scale(r), G: scale(g), B: scale(b), A: scale(a)}
}

// typingSweepAt is where a segment sits in the cycle, 0 at the left of the travel
// to 1 at the right and back. A cosine rather than a triangle: the line eases into
// each turn instead of striking the end, and that slowing is what gathers the
// trail behind the head. The phase arrives negative for every segment but the
// head, so it wraps by flooring rather than truncating towards zero.
func typingSweepAt(phase float32) float32 {
	phase -= float32(math.Floor(float64(phase)))

	return float32(1-math.Cos(2*math.Pi*float64(phase))) / 2
}

// typingMarkRenderer exists for Destroy alone: a restyle and every channel-list
// refresh rebuild the tree, and the animation has to go with it.
type typingMarkRenderer struct {
	fyne.WidgetRenderer
	mark *TypingMark
}

func (r *typingMarkRenderer) Destroy() {
	r.mark.stop()
	r.WidgetRenderer.Destroy()
}
