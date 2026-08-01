package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* Server icons */

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

	// baseLayout and grownLayout size the icon at rest and when hovered or
	// selected; built once so hovering doesn't allocate a layout per event.
	baseLayout  fyne.Layout
	grownLayout fyne.Layout

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

// SetSelected updates the selection state. Unchanged state is a no-op, so a
// sidebar-wide sync only repaints the icons that actually changed.
func (w *ServerWidget) SetSelected(selected bool) {
	if w.selected == selected {
		return
	}

	w.selected = selected
	w.refreshAppearance()
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

	grown := theme.Sizes.ServerIconSize * serverHoverGrowth
	w.baseLayout = layout.NewGridWrapLayout(iconSize)
	w.grownLayout = layout.NewGridWrapLayout(fyne.NewSize(grown, grown))
	w.iconWrapper = container.New(w.baseLayout, icon)

	return widget.NewSimpleRenderer(container.NewCenter(w.iconWrapper))
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

	wrap := w.baseLayout
	if w.selected || w.hovered {
		wrap = w.grownLayout
	}
	if w.iconWrapper.Layout != wrap {
		w.iconWrapper.Layout = wrap
		w.iconWrapper.Refresh()
	}
}

func (w *ServerWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

func (w *ServerWidget) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (w *ServerWidget) MouseIn(*desktop.MouseEvent) {
	w.hovered = true
	w.refreshAppearance()
}

func (w *ServerWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *ServerWidget) MouseOut() {
	w.hovered = false
	w.refreshAppearance()
}

/* Channel rows */

// ChannelWidget is a selectable channel row carrying selection and unread state.
type ChannelWidget struct {
	widget.BaseWidget
	Channel *revoltgo.Channel
	onTap   func()

	background         *canvas.Rectangle
	selectionIndicator *canvas.Rectangle
	unreadIndicator    *canvas.Rectangle
	label              *canvas.Text

	selected bool
	unread   bool
}

var (
	_ fyne.Tappable     = (*ChannelWidget)(nil)
	_ desktop.Hoverable = (*ChannelWidget)(nil)
)

// NewChannelWidget creates a channel row.
func NewChannelWidget(channel *revoltgo.Channel, onTap func()) *ChannelWidget {
	label := canvas.NewText(channel.Name, theme.Colors.CategoryText)
	label.TextSize = theme.Sizes.ChannelLabelSize
	label.Alignment = fyne.TextAlignLeading

	w := &ChannelWidget{
		Channel:            channel,
		onTap:              onTap,
		background:         canvas.NewRectangle(color.Transparent),
		selectionIndicator: canvas.NewRectangle(color.Transparent),
		unreadIndicator:    canvas.NewRectangle(color.Transparent),
		label:              label,
	}
	w.ExtendBaseWidget(w)

	return w
}

// SetState updates selection and unread together. Unchanged state is a no-op, so
// a sidebar-wide sync only repaints the rows that actually changed.
func (w *ChannelWidget) SetState(selected, unread bool) {
	if w.selected == selected && w.unread == unread {
		return
	}

	w.selected = selected
	w.unread = unread
	w.refreshAppearance()
	w.Refresh()
}

func (w *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	w.selectionIndicator.SetMinSize(fyne.NewSize(3, 0))
	w.unreadIndicator.SetMinSize(fyne.NewSize(theme.Sizes.UnreadIndicatorWidth, 0))

	// Both indicators share the same 3px slot; the unread bar is wrapped in an
	// HBox so it keeps its 1px width and stays left-aligned.
	indicators := container.NewStack(w.selectionIndicator, container.NewHBox(w.unreadIndicator))
	content := container.NewHBox(
		indicators,
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		HashtagIcon(),
		w.label,
	)

	w.background.SetMinSize(fyne.NewSize(0, theme.Sizes.ChannelItemHeight))
	w.refreshAppearance()

	return widget.NewSimpleRenderer(container.NewStack(w.background, content))
}

func (w *ChannelWidget) refreshAppearance() {
	if w.selected {
		w.background.FillColor = theme.Colors.ChannelSelectedBg
		w.selectionIndicator.FillColor = theme.Colors.TextPrimary
	} else {
		w.background.FillColor = color.Transparent
		w.selectionIndicator.FillColor = color.Transparent
	}

	if w.unread {
		w.unreadIndicator.FillColor = theme.Colors.UnreadIndicator
	} else {
		w.unreadIndicator.FillColor = color.Transparent
	}

	if w.selected || w.unread {
		w.label.Color = theme.Colors.TextPrimary
	} else {
		w.label.Color = theme.Colors.CategoryText
	}

	w.background.Refresh()
	w.selectionIndicator.Refresh()
	w.unreadIndicator.Refresh()
	w.label.Refresh()
}

func (w *ChannelWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

func (w *ChannelWidget) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (w *ChannelWidget) MouseIn(*desktop.MouseEvent) {
	if !w.selected {
		w.background.FillColor = theme.Colors.ChannelHoverBackground
		w.background.Refresh()
	}
}

func (w *ChannelWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *ChannelWidget) MouseOut() { w.refreshAppearance() }

/* Channel categories */

// CategoryWidget is a collapsible category header. Toggling it shows or hides
// the channel widgets registered through SetChannels.
type CategoryWidget struct {
	widget.BaseWidget
	title    string
	onToggle func(collapsed bool)

	indicator   *fyne.Container
	background  *canvas.Rectangle
	channels    []fyne.CanvasObject
	channelHost *fyne.Container

	collapsed bool
	first     bool
}

var (
	_ fyne.Tappable     = (*CategoryWidget)(nil)
	_ desktop.Hoverable = (*CategoryWidget)(nil)
)

// NewCategoryWidget creates a category header. onToggle reports the new
// collapsed state whenever the user clicks it.
func NewCategoryWidget(title string, onToggle func(collapsed bool)) *CategoryWidget {
	w := &CategoryWidget{
		title:      title,
		onToggle:   onToggle,
		indicator:  container.NewCenter(drawIndicator(true)),
		background: canvas.NewRectangle(color.Transparent),
	}
	w.ExtendBaseWidget(w)

	return w
}

// SetFirst marks this as the first category, which removes its top margin.
func (w *CategoryWidget) SetFirst(first bool) { w.first = first }

// SetChannels registers the channel widgets this category controls, along with
// the host container refreshed when their visibility changes.
func (w *CategoryWidget) SetChannels(channels []fyne.CanvasObject, host *fyne.Container) {
	w.channels = channels
	w.channelHost = host
}

// SetCollapsed sets the collapsed state and updates visibility.
func (w *CategoryWidget) SetCollapsed(collapsed bool) {
	w.collapsed = collapsed
	w.applyCollapsed()
}

func (w *CategoryWidget) applyCollapsed() {
	w.indicator.Objects = []fyne.CanvasObject{drawIndicator(!w.collapsed)}
	w.indicator.Refresh()

	for _, ch := range w.channels {
		if w.collapsed {
			ch.Hide()
		} else {
			ch.Show()
		}
	}

	if w.channelHost != nil {
		w.channelHost.Refresh()
	}
}

func (w *CategoryWidget) MinSize() fyne.Size {
	height := theme.Sizes.CategoryHeight
	if !w.first {
		height += theme.Sizes.CategorySpacing
	}

	return fyne.NewSize(0, height)
}

func (w *CategoryWidget) CreateRenderer() fyne.WidgetRenderer {
	title := canvas.NewText(w.title, theme.Colors.CategoryText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.TextSize = 13

	indicatorRow := container.NewHBox(w.indicator, HorizontalSpacer(8))
	content := container.NewBorder(nil, nil, title, indicatorRow, nil)
	inner := container.NewStack(w.background, container.NewPadded(content))

	return &categoryRenderer{widget: w, inner: inner}
}

func (w *CategoryWidget) Tapped(*fyne.PointEvent) {
	w.collapsed = !w.collapsed
	w.applyCollapsed()

	if w.onToggle != nil {
		w.onToggle(w.collapsed)
	}
}

func (w *CategoryWidget) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (w *CategoryWidget) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.ChannelHoverBackground
	w.background.Refresh()
}

func (w *CategoryWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *CategoryWidget) MouseOut() {
	w.background.FillColor = color.Transparent
	w.background.Refresh()
}

// categoryRenderer adds a top margin to every category except the first.
type categoryRenderer struct {
	widget *CategoryWidget
	inner  *fyne.Container
}

func (r *categoryRenderer) Layout(size fyne.Size) {
	var margin float32
	if !r.widget.first {
		margin = theme.Sizes.CategorySpacing
	}

	r.inner.Move(fyne.NewPos(0, margin))
	r.inner.Resize(fyne.NewSize(size.Width, size.Height-margin))
}

func (r *categoryRenderer) MinSize() fyne.Size           { return r.widget.MinSize() }
func (r *categoryRenderer) Refresh()                     { r.inner.Refresh() }
func (r *categoryRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.inner} }
func (r *categoryRenderer) Destroy()                     {}

/* Drawn glyphs */

// drawIndicator renders a category's expand/collapse glyph: a minus when
// expanded, a plus when collapsed.
func drawIndicator(expanded bool) fyne.CanvasObject {
	const pad = 3

	size := theme.Sizes.CategoryIndicatorSize
	stroke := theme.Sizes.CategoryIndicatorStroke
	col := theme.Colors.CategoryIndicator

	horizontal := canvas.NewLine(col)
	horizontal.Position1 = fyne.NewPos(pad, size/2)
	horizontal.Position2 = fyne.NewPos(size-pad, size/2)
	horizontal.StrokeWidth = stroke

	lines := []fyne.CanvasObject{horizontal}
	if !expanded {
		vertical := canvas.NewLine(col)
		vertical.Position1 = fyne.NewPos(size/2, pad)
		vertical.Position2 = fyne.NewPos(size/2, size-pad)
		vertical.StrokeWidth = stroke
		lines = append(lines, vertical)
	}

	return container.NewCenter(container.NewGridWrap(fyne.NewSize(size, size), container.NewWithoutLayout(lines...)))
}

// HashtagIcon returns a drawn "#" glyph used to prefix channel names.
func HashtagIcon() fyne.CanvasObject {
	col := theme.Colors.HashtagIcon
	size := theme.Sizes.HashtagIconSize
	scale := size / 20

	line := func(x1, y1, x2, y2 float32) *canvas.Line {
		l := canvas.NewLine(col)
		l.Position1 = fyne.NewPos(x1*scale, y1*scale)
		l.Position2 = fyne.NewPos(x2*scale, y2*scale)
		l.StrokeWidth = 2 * scale
		return l
	}

	glyph := container.NewWithoutLayout(
		line(7, 2, 7, 18),
		line(13, 2, 13, 18),
		line(2, 7, 18, 7),
		line(2, 13, 18, 13),
	)

	return container.NewCenter(container.NewGridWrap(fyne.NewSize(size, size), glyph))
}

/* Member rows */

// NewMemberWidget builds a member row: a small circular avatar and the display
// name, the whole row tappable. Offline members get dimmed name text.
func NewMemberWidget(deps Deps, member *revoltgo.ServerMember, online bool) fyne.CanvasObject {
	textColor := theme.Colors.TextPrimary
	if !online {
		textColor = theme.Colors.CategoryText
	}

	label := canvas.NewText(util.MemberName(deps.Session, member), textColor)
	label.TextSize = theme.Sizes.MemberNameSize

	avatarSize := fyne.NewSize(theme.Sizes.MemberAvatarSize, theme.Sizes.MemberAvatarSize)
	avatar := circularAvatar(deps.Images, util.MemberAvatarURL(deps.Session, member), avatarSize)

	row := container.NewHBox(
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		container.NewCenter(avatar),
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		container.NewCenter(label),
	)

	userID := member.ID.User
	content := NewMinHeightContainer(theme.Sizes.MemberRowHeight, row)

	return NewTappableContainer(content, func() { deps.Actions.OnAvatarTapped(userID) })
}

// NewMemberSection is the small bold header grouping members, e.g. "Online — 5".
func NewMemberSection(title string) fyne.CanvasObject {
	text := canvas.NewText(title, theme.Colors.CategoryText)
	text.TextStyle = fyne.TextStyle{Bold: true}
	text.TextSize = 12

	return container.NewHBox(HorizontalSpacer(theme.Sizes.ChannelLeftPadding), container.NewPadded(text))
}

/* Saved sessions */

// SessionCard is a clickable card for a saved login, with a remove button.
type SessionCard struct {
	widget.BaseWidget
	background *canvas.Rectangle
	avatar     *fyne.Container
	username   string
	onTap      func()
	onRemove   func()
}

// NewSessionCard creates a saved-session card, loading the avatar if available.
func NewSessionCard(images *cache.ImageCache, username, avatarID string, onTap, onRemove func()) *SessionCard {
	background := canvas.NewRectangle(theme.Colors.SessionCardBg)
	background.CornerRadius = 4

	side := theme.Sizes.SessionCardAvatarSize
	var url string
	if avatarID != "" {
		url = revoltgo.EndpointAutumnFile("avatars", avatarID, "64")
	}

	c := &SessionCard{
		background: background,
		avatar:     circularAvatar(images, url, fyne.NewSize(side, side)),
		username:   username,
		onTap:      onTap,
		onRemove:   onRemove,
	}
	c.ExtendBaseWidget(c)

	return c
}

func (c *SessionCard) CreateRenderer() fyne.WidgetRenderer {
	label := widget.NewLabel(c.username)
	label.TextStyle.Bold = true

	remove := container.NewCenter(NewCloseButton(c.onRemove))
	content := container.NewBorder(nil, nil, c.avatar, remove, label)
	tappable := NewTappableContainer(content, c.onTap)

	return widget.NewSimpleRenderer(container.NewStack(c.background, container.NewPadded(tappable)))
}
