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

/* Edges */

// Outline edges rect with the client's hairline. Every card is drawn with the
// same one — an embed, an attachment, the composer dock — and it is darker than
// anything it is ever laid over, so no fill behind it can close on it.
//
// Which rectangle carries it matters. A card whose content sits inside padding
// can wear it on its own background; one whose content reaches its edge — a
// picture — needs it on a rectangle stacked over the content instead, or it is
// simply painted over.
func Outline(rect *canvas.Rectangle) {
	rect.StrokeColor = theme.Colors.Outline
	rect.StrokeWidth = theme.Sizes.OutlineWidth
}

// Elevate casts rect's shadow onto whatever it is laid over. The composer dock
// is the only thing in the client that carries one: an outline and a margin make
// a card, but only a cast shadow makes a card that is *above* something. Without
// it the gutter reads as a strip the card was dropped into, which is the
// difference between an island and a bar.
//
// DropShadow follows the corner radius and paints nothing beneath the fill, so a
// translucent shadow cannot darken the card itself. The blur deliberately
// overruns the margin — it is the surface behind that has to darken — and Fyne
// grows the object's own quad to fit the shadow, so it draws outside rect's
// bounds rather than being clipped to them.
func Elevate(rect *canvas.Rectangle) {
	rect.Shadow = canvas.Shadow{
		Color:      theme.Colors.CardShadow,
		BlurRadius: theme.Sizes.CardShadowBlur,
		Variant:    canvas.DropShadow,
	}
}

// NewColumnDivider is the seam between two columns of the main row: the same
// hairline, one pixel wide, stretched to the column's height by the row it is
// placed in.
//
// It belongs to a column rather than sitting between two. The main row
// addresses its children by position to find the one that stretches, and a
// divider of its own would both shift that index and stay behind when the
// member sidebar is hidden.
func NewColumnDivider() fyne.CanvasObject {
	divider := canvas.NewRectangle(theme.Colors.Outline)
	divider.SetMinSize(fyne.NewSize(theme.Sizes.OutlineWidth, 0))

	return divider
}

// NewRowDivider is the same hairline lying across a column, which is what marks
// off a group of rows from the rows under it.
func NewRowDivider() fyne.CanvasObject {
	divider := canvas.NewRectangle(theme.Colors.Outline)
	divider.SetMinSize(fyne.NewSize(0, theme.Sizes.OutlineWidth))

	return divider
}

/* Chips */

// NewChip is one small rounded label in its own colour — a badge or a count.
func NewChip(text string, tint color.Color) fyne.CanvasObject {
	return newChip(nil, text, tint)
}

// NewTappableChip is a chip that leads somewhere. It lights under the pointer and
// carries the pointer cursor, which is the whole of what tells it apart from the
// plain one beside it — a mutual profile draws both, the names it resolved and a
// "+n" for the ones it could not.
func NewTappableChip(text string, tint color.Color, onTap func()) fyne.CanvasObject {
	c := &tappableChip{}
	c.content, c.background = chipParts(nil, text, tint)
	c.onTap = onTap
	c.ExtendBaseWidget(c)

	return c
}

// tappableChip is NewChip's surface with a click on it. It is a widget rather
// than a TappableContainer around a chip because that one hovers a square behind
// whatever it wraps, and a square lighting up behind a rounded label is a second
// shape appearing rather than the chip responding.
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
// outcome. The login screen and the second-factor screen are both up before the
// client — and therefore before NoticeStack — is built, so a failure there had
// nowhere to go but a Fyne error dialog, which is the one surface in the app
// that AppTheme does not reach.
//
// It is a widget.Label rather than a canvas.Text for two reasons a login screen
// runs into immediately: a transport error is a long sentence and has to wrap,
// and Importance is the one way to colour text without holding a colour that a
// restyle would leave stale.
type StatusLine struct {
	// Content is the object to mount. The label is kept apart from it so a caller
	// can put the line where it wants without knowing what it is made of.
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

// NewBotMark is the glyph that says an account is a bot, drawn after its name.
//
// A glyph rather than a lettered chip: the word is the same on every row that
// carries one, so a column of them reads as a column of identical labels rather
// than as a property of the names beside them — and the mark is legible at a
// size a three-letter chip is not.
//
// The side is the caller's, as a TypingMark's width is, because the two names it
// follows are set differently: a member row's and a profile's heading.
func NewBotMark(side float32) fyne.CanvasObject {
	return container.NewCenter(newScaledIcon(tintedIcon(assets.BotIcon, theme.Colors.BotMark), side))
}

// RoleChip is a role drawn as a chip: a dot in the role's own colour beside its
// name. The chip is what answers the right-click rather than the dot alone —
// the dot is a few pixels across, and a menu nothing can reliably hit is not one.
type RoleChip struct {
	tapBase

	content fyne.CanvasObject
}

var _ fyne.SecondaryTappable = (*RoleChip)(nil)

// NewRoleChip draws role as a chip, right-clickable for its name and ID. Roles
// without a colour fall back to the primary text colour, dot included, so the
// shape stays the same wherever the chip is used.
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

// roleMenu is what right-clicking a role offers. A role resolved from a server
// always carries an ID; one built from a name alone leaves that item out rather
// than offering an empty copy.
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
// rounded surface. The mark is centred rather than stretched — a row layout
// would otherwise hand a circle the full height of the text beside it.
func newChip(mark fyne.CanvasObject, text string, tint color.Color) fyne.CanvasObject {
	chip, _ := chipParts(mark, text, tint)

	return chip
}

// chipParts builds the surface and hands its background back alongside it, which
// is what a tappable one needs to recolour on hover.
func chipParts(mark fyne.CanvasObject, text string, tint color.Color) (fyne.CanvasObject, *canvas.Rectangle) {
	background := canvas.NewRectangle(theme.Colors.ChipBg)
	background.CornerRadius = theme.Sizes.ChipRadius

	label := canvas.NewText(text, solidColor(tint))
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.TextSize = theme.Sizes.ChipTextSize

	var content fyne.CanvasObject = container.NewCenter(label)
	if mark != nil {
		content = HBoxNoSpacing(mark, HorizontalSpacer(theme.Sizes.ChipDotGap), content)
	}

	padV, padH := theme.Sizes.ChipPaddingV, theme.Sizes.ChipPaddingH

	return container.NewStack(background, NewInset(content, padV, padV, padH, padH)), background
}

// newChipDot is the leading dot: the one thing in a chip carrying the shared
// hairline, which is what lifts a saturated colour off the surface behind it.
// The circle is pinned to its own square and centred twice over — a row layout
// stretches what it is given, and a stretched circle is an ellipse.
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

// NewHoverableStack makes content tappable with an outline and an optional hover
// callback.
//
// Its rectangle is stacked *over* the content, which is what lets an attachment
// be framed at all: the picture is drawn to the card's own edge, so a border
// behind it would simply be painted over. The stroke is the shared hairline at
// rest and lifts to a lighter slate under the pointer.
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

func (h *HoverableStack) MouseIn(*desktop.MouseEvent) {
	h.background.StrokeColor = theme.Colors.AttachmentHoverBorder
	h.background.Refresh()

	if h.onHover != nil {
		h.onHover(true)
	}
}

func (h *HoverableStack) MouseOut() {
	h.background.StrokeColor = theme.Colors.Outline
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

// NewGlyphButton creates the same button wearing res — the way into a card's own
// menu, drawn on its banner beside the way out of it.
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
	// from the widgets underneath it, and NewLayer keeps the name it is holding out
	// of the window's minimum size — the card places itself, so it goes in a
	// container of its own that the layer can fill without resizing it.
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

// ShowAbove names obj with the label centred over it rather than beside it, kept
// inside the layer's own width and dropped below obj where there is no room over
// it. That is what a cell in a grid needs: it has neighbours either side, so a
// label past its right edge names the wrong one.
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
// layer. Both positions it reads are canvas-absolute; the difference is the
// offset inside the layer, wherever the layer itself happens to sit.
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

// newAvatarSlot is the same circle with nothing loaded into it. It exists apart
// from circularAvatar for the member list, whose rows are recycled: a picture
// asked for at construction has no generation to check itself against when it
// arrives, and would be painted into whoever the row has since moved on to.
//
// The placeholder is handed back alongside the slot so a row swapping a picture
// back out can restore *that* object. A fresh circle is one the canvas has never
// seen, and a row that quietly put one back drew nothing at all.
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

// imageCacheID is the key a picture is cached under: its Autumn file ID where
// the URL is one of Revolt's, and a hash of the URL where it isn't.
//
// The hash is not decoration. The ID doubles as the picture's filename in the
// disk cache, and an embed's preview or a site's mark comes from wherever the
// page it was unfurled from serves it — a URL with a scheme and slashes in it,
// which no file can be called. Hashing is what lets those be cached at all.
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
// Read per event rather than at construction: it is one map lookup against a
// wheel notch, and it means the setting takes effect without a rebuild.
func scrollAmplify() float32 {
	return float32(config.Current().Behaviour.ScrollSpeed)
}

const (
	// scrollIndicatorLinger is how long the position indicator stays up after the
	// last movement, and scrollIndicatorFade how long it takes to leave. Long
	// enough to say where the view landed; gone before anything is read against it.
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

// NewPlainVScroll is the same scroll without the position indicator, for a column
// whose own content reaches the right edge the strip would be drawn over. The
// settings pane centres its cards, so an indicator lands on top of one whenever
// the window is narrow enough for the two to meet.
func NewPlainVScroll(content fyne.CanvasObject) *ObservableScroll {
	return new(ObservableScroll).init(content)
}

func (s *ObservableScroll) init(content fyne.CanvasObject) *ObservableScroll {
	s.Direction = container.ScrollVerticalOnly
	s.Content = content
	s.ExtendBaseWidget(s)

	return s
}

// CreateRenderer adds the indicator to Fyne's own scroll renderer. It is a
// canvas.Rectangle rather than Fyne's scroll bar because that bar comes wrapped
// in a hover-accepting area over the right edge of the content, which — being
// innermost — takes the hover the message row under it needs, and swells under
// the pointer over the text it is meant to sit beside. AppTheme zeroes both of
// its sizes; this draws what they used to.
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

// Refresh is where the indicator is revealed rather than in Scrolled: every
// offset change ends here whoever asked for it, so a middle-button pan and the
// jump to the newest message are covered by the same line. The offset is compared
// rather than trusted, because a refresh for any other reason — a mounted widget
// repainting, a theme change — must not flash the bar.
func (r *scrollRenderer) Refresh() {
	r.WidgetRenderer.Refresh()

	moved := r.scroll.Offset != r.scroll.lastOffset
	r.scroll.lastOffset = r.scroll.Offset

	if r.scroll.placeIndicator() && moved {
		r.scroll.revealIndicator()
	}
}

// Destroy stops the fade with the widget. A rebuild of the tree — restyling does
// one — drops the scroll while an animation could still be running against it.
func (r *scrollRenderer) Destroy() {
	r.scroll.stopFade()
	if r.scroll.linger != nil {
		r.scroll.linger.Stop()
	}

	r.WidgetRenderer.Destroy()
}

// SyncContent resizes the content to what it now measures. Fyne's scroller does
// this from its renderer's Layout, which runs on a Refresh — and refreshing a
// mounted message column re-wraps every body in it, which is the whole reason
// nothing here calls Scroll.Refresh after a mount.
//
// Without it ScrollToOffset clamps against the size the content was laid out at
// *before* the mount: a column that has just grown from a screenful to a page of
// history is scrolled as though it still fitted the viewport, which zeroes the
// offset. Only a caller that mounts and then scrolls in the same pass needs it.
func (s *ObservableScroll) SyncContent() {
	if s.Content == nil {
		return
	}

	s.Content.Resize(s.Content.MinSize().Max(s.Size()))
}

// placeIndicator sizes the bar to the fraction of the content in view and moves
// it to where that fraction sits, reporting whether there is anything to
// indicate. The extent comes from Content.Size(), which the scroll's own layout
// has already resized to it — MinSize on the message list is a walk of every
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

	height := fyne.Min(fyne.Max(track*view.Height/content, theme.Sizes.ScrollIndicatorMinHeight), track)
	progress := fyne.Min(fyne.Max(s.Offset.Y/(content-view.Height), 0), 1)

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

// fadeIndicator takes the bar out over scrollIndicatorFade. Only the linger timer
// calls it, so a movement arriving mid-fade stops it through revealIndicator.
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
	// walks and repaints every descendant, which for a long pane is the whole
	// column once per frame of the pan.
	s.ScrollToOffset(fyne.NewPos(s.Offset.X-ev.Dragged.DX, s.Offset.Y-ev.Dragged.DY))
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

/* Text metrics */

// textAscentRatio is how much of a line of text sits above its baseline. Fyne
// measures a line but exposes none of the metrics behind it, so the split is
// taken as fixed — the client draws one font throughout, and only the difference
// between two line heights is ever scaled by it.
const textAscentRatio = 0.8

// baselineOffset is how far down text at size small must start to share a
// baseline with text at size large beside it. Two sizes handed the same height
// centre against each other instead, which leaves the smaller one riding high.
func baselineOffset(large, small float32) float32 {
	return textAscentRatio * (lineHeight(large) - lineHeight(small))
}

/* Accented text */

// AccentText is a name drawn in a role's colour. Fyne fills a text object with
// one colour, so a gradient across a word can only be a gradient across its
// letters: a flat colour mounts a single canvas.Text, a gradient one per rune,
// each filled where that rune sits along the run.
//
// The split is the whole reason it is a widget rather than a bare canvas.Text.
// A name whose role carries no gradient — nearly every one — still mounts exactly
// one text object, so the message list pays nothing for the ones that do.
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

// build lays out the text objects the current name and fill need: one, or one
// per rune, each filled where its own glyph falls along the run.
//
// Every offset is measured off the *whole* name up to that rune rather than
// accumulated from the runes themselves, so the split run is laid out exactly
// where the unsplit one would be — summing single glyphs drifts by a fraction of
// a pixel each, which is enough to move the timestamp beside a name. The centres
// are then stretched over the whole gradient: a text object takes one colour, so
// the outermost letters would otherwise stop short of the stops they exist to
// show.
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

// accentLayout places each of AccentText's glyphs at the offset the name
// measures up to it, and reports the name's own measurement whatever it was split
// into.
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

	obj := canvas.NewText(text, solidColor(fill))
	obj.TextSize = t.size
	obj.TextStyle = t.style

	return obj
}

// solidColor flattens fill to something a canvas.Text can be drawn in. Fyne
// caches a rendered glyph run in a map keyed by the text object's own fields,
// colour included, so a fill that cannot be a map key — a domain.Gradient is a
// slice — panics the painter mid-frame rather than drawing. A shape is safe: its
// texture is keyed by the object, not by what fills it.
//
// The flattening is the gradient's own mean, which is what it answers as
// anywhere it is used as a plain colour. Only ui.AccentText spreads one across
// text, and it does that by giving each rune a stop of its own — so every fill
// reaching a text object here is already meant to be flat.
func solidColor(fill color.Color) color.Color {
	if _, gradient := fill.(domain.Gradient); !gradient {
		return fill
	}

	// Premultiplied, as RGBA reports: RGBA64 holds it exactly, where NRGBA would
	// have to divide the alpha back out.
	r, g, b, a := fill.RGBA()

	return color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(b), A: uint16(a)}
}

// sameColor compares two fills without assuming either can be compared with ==,
// which a domain.Gradient — a slice — panics on.
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

// SetEllipsisText re-labels a box built by NewEllipsisText. The full text is
// fixed at construction there, which is right for a row built per person and
// wrong for one the member list recycles — and reading the name back off the
// text object instead would take it to be whatever last fitted the column.
func SetEllipsisText(box *fyne.Container, text string) {
	layout, ok := box.Layout.(*ellipsisLayout)
	if !ok {
		return
	}

	layout.full = text
	layout.sized = false // the fit is for the previous name, whatever the width
	Relayout(box)
}

// ellipsisLayout re-fits its text to the width it is handed and centres it
// vertically. Rewriting the text during Layout is safe because the reported
// minimum size doesn't depend on the content — the width is fixed at zero and
// the height is the font's — so a shortened string can't trigger another layout.
//
// Both derived values are held onto because Layout and MinSize run on every
// pass, over every row of both sidebars, while the answers change only when the
// column is resized: full is fixed at construction, and so are the text's size
// and style.
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

// WrapToWidth breaks text into lines that each measure no wider than width.
// A word too long for a line of its own is broken mid-word: the alternative is
// one line overhanging whatever sits beside it, which is the thing wrapping is
// for. A width of zero or less is no room to decide anything in, so the text
// comes back whole.
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

		// A word wider than the column itself is cut where it stops fitting and
		// carries on filling the next line, however many that takes.
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

// NewWrappedText is a block of prose that wraps at width. It is canvas.Text per
// line rather than a wrapping widget.Label because a wrapping widget cannot be
// asked how tall it wants to be until after it has been given a width — it
// answers with whatever it was last laid out at — so a row holding one reports
// the wrong height for the frame it is already in. Every caller here knows its
// width beforehand, so the break points are decided at construction and the
// block's minimum is true from the first pass.
func NewWrappedText(text string, width, size float32, colour color.Color) fyne.CanvasObject {
	lines := WrapToWidth(text, width, size, fyne.TextStyle{})

	objects := make([]fyne.CanvasObject, 0, len(lines))
	for _, line := range lines {
		object := canvas.NewText(line, colour)
		object.TextSize = size
		objects = append(objects, object)
	}

	return VBoxNoSpacing(objects...)
}

/* Typing mark */

const (
	// typingSweepPeriod is one full there-and-back of the line.
	typingSweepPeriod = time.Second

	// typingSweepSegments is the head and everything trailing it, and
	// typingSweepLag how far behind the head — in fractions of a cycle — each
	// following segment runs. Lagging in *time* rather than in space is what makes
	// the trail take the same path: it bunches up behind the head at each turn and
	// draws out again across the middle, with nothing to clamp at either end.
	typingSweepSegments = 4
	typingSweepLag      = 0.075

	// typingSweepFade is how much of the colour ahead of it each segment keeps.
	typingSweepFade = 0.55
)

// TypingMark is the sweeping line that says somebody is composing. Both places
// that need one mount it: the line above the composer card, and a channel row in
// the sidebar.
//
// It is a widget of its own rather than a container of rectangles because the
// sweep has to be stopped when the tree holding it goes away, and only a renderer
// is told about that. Every channel row is rebuilt from scratch by
// refreshChannelList, so an animation left running against a discarded row would
// tick for the life of the process.
//
// It accepts no pointer event, so hover and right-click reach whatever it is
// drawn over: the message passing under the composer, or the channel row it
// marks.
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

	// Laid out by hand on the 20-unit grid the client's other glyphs use, so the
	// line keeps its proportions at any width and the sweep can move a rectangle
	// with no layout pass behind it. Nine units by three: long enough to read as a
	// line rather than a dash, shallow enough not to read as a bar — and leaving
	// eleven for it to travel, which is what has to be legible at a glance.
	//
	// The box is exactly as tall as the line, unlike the square the client's glyphs
	// are drawn in: there is nothing above or below it, and both callers centre it
	// in a row of their own.
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

	// Farthest behind first: the head is opaque and has to be drawn over its own
	// trail, and a container paints its objects in the order it holds them.
	glyph := container.NewWithoutLayout()
	for i := len(m.segments) - 1; i >= 0; i-- {
		glyph.Add(m.segments[i])
	}

	m.content = container.NewGridWrap(fyne.NewSize(width, barHeight), glyph)

	m.Hide()
	m.ExtendBaseWidget(m)

	return m
}

func (m *TypingMark) CreateRenderer() fyne.WidgetRenderer {
	return &typingMarkRenderer{WidgetRenderer: widget.NewSimpleRenderer(m.content), mark: m}
}

// SetActive shows the mark and starts the sweep, or hides it and stops it.
// Unchanged state is a no-op, so a repaint of a whole sidebar costs nothing per
// row that did not move.
//
// A hidden widget must never keep an animation: it would repaint nothing sixty
// times a second. Neither may a still one, which is what animate off asks for —
// the line rests in the middle of its travel and no animation is started at all.
func (m *TypingMark) SetActive(active, animate bool) {
	if !active {
		if m.Visible() || m.sweep != nil {
			m.stop()
			m.Hide()
		}
		return
	}

	// animate is only part of the state while the mark is up, which is why it is
	// not compared on the way out: a still mark and a stopped one are the same
	// thing hidden.
	if m.Visible() && animate == (m.sweep != nil) {
		return
	}

	m.stop()
	m.Show()

	if animate {
		m.start()
	}
}

// start runs the sweep. Nothing here refreshes anything: a written fill marks
// nothing dirty, but canvas.Rectangle.Move repaints for itself — and moving is
// all this does, the trail's colours being fixed at construction.
//
// The curve is linear because the shaping is in typingSweepAt. An eased one would
// also stutter at every repeat, easing out of a cycle and back into the next at
// the point the line is travelling fastest.
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

// stop halts the sweep and rests the line in the middle of its travel, which is
// where a still mark is drawn — the trail collapsed under the head, where an
// opaque head hides it. Nil-safe and idempotent: every start goes through it.
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

// typingTrailTint dims the mark's colour for a segment behind the head.
//
// It is not theme.Fade, which scales the alpha of a color.RGBA and leaves the
// channels alone — and Go defines those channels as *already multiplied by* that
// alpha. Faded that way, a colour composites brighter than the one it came from,
// which for a trail meant a tail lighter than the line casting it. Everything has
// to come down together.
func typingTrailTint(c color.Color, alpha float32) color.Color {
	r, g, b, a := c.RGBA()
	scale := func(channel uint32) uint8 { return uint8(float32(channel>>8) * alpha) }

	return color.RGBA{R: scale(r), G: scale(g), B: scale(b), A: scale(a)}
}

// typingSweepAt is where a segment sits at a point in the cycle, from 0 at the
// left of the travel to 1 at the right and back. A cosine rather than a triangle:
// the line eases into each turn instead of striking the end and reversing, and it
// is that slowing which gathers the trail up behind the head there.
//
// The phase arrives negative for every segment but the head, so it is wrapped by
// flooring rather than by truncating towards zero.
func typingSweepAt(phase float32) float32 {
	phase -= float32(math.Floor(float64(phase)))

	return float32(1-math.Cos(2*math.Pi*float64(phase))) / 2
}

// typingMarkRenderer exists for Destroy alone: the tree holding the mark is
// rebuilt by a restyle and by every refresh of the channel list, and the
// animation has to go with it.
type typingMarkRenderer struct {
	fyne.WidgetRenderer
	mark *TypingMark
}

func (r *typingMarkRenderer) Destroy() {
	r.mark.stop()
	r.WidgetRenderer.Destroy()
}
