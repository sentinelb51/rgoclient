package ui

import (
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* Server icons */

const (
	// serverHoverGrowth is how much a server icon grows when hovered or selected.
	serverHoverGrowth = 1.1

	// categoryTitleSize is the caption over a group of channels.
	categoryTitleSize = 13
)

// ServerWidget is a circular server icon that grows and recolours when hovered
// or selected, and wears the same selection bar on its left edge as the channel
// row does when it is the open server.
type ServerWidget struct {
	tapBase
	Server domain.Server

	// OnHover reports the pointer entering and leaving, so the sidebar can name
	// the server in a Tooltip. Menu supplies the right-click items.
	OnHover func(hovering bool)
	Menu    func() []*fyne.MenuItem

	images      *cache.ImageCache
	background  *canvas.Circle
	marker      *canvas.Rectangle
	iconWrapper *fyne.Container
	iconStack   *fyne.Container // the picture, or the initial standing in for one

	// The icon's size at rest and when hovered or selected, built once so hovering
	// allocates no layout per event.
	baseLayout  fyne.Layout
	grownLayout fyne.Layout

	// generation counts the servers this widget has been given, so a picture that
	// arrives after SetServer moved it on is dropped rather than drawn.
	generation uint64

	selected  bool
	mentioned bool
	hovered   bool
}

var (
	_ fyne.Tappable          = (*ServerWidget)(nil)
	_ fyne.SecondaryTappable = (*ServerWidget)(nil)
	_ desktop.Hoverable      = (*ServerWidget)(nil)
)

// NewServerWidget creates a server icon widget.
func NewServerWidget(images *cache.ImageCache, server domain.Server, onTap func()) *ServerWidget {
	w := &ServerWidget{
		Server:     server,
		images:     images,
		background: canvas.NewCircle(theme.Colors.ServerDefaultBg),
		marker:     canvas.NewRectangle(color.Transparent),
	}

	// Menu is assigned by the sidebar after construction, so it is read when the
	// click arrives rather than captured here.
	w.onTap = onTap
	w.onSecondaryTap = func(event *fyne.PointEvent) { showMenuHook(w, w.Menu, event) }
	w.ExtendBaseWidget(w)

	return w
}

// SetServer re-points the icon at the server as it now is. A no-op when neither
// the name nor the picture moved: the rail is rebuilt for an update about any
// server the account is in, so all but one of its icons are drawing what they
// already drew — and asking the cache for those pictures again is the cost this
// spares.
func (w *ServerWidget) SetServer(server domain.Server) {
	if w.Server.Name == server.Name && w.Server.IconID == server.IconID &&
		w.Server.IconURL == server.IconURL {
		w.Server = server

		return
	}

	w.Server = server
	w.generation++

	// Nothing has been drawn yet, and CreateRenderer reads the server when it runs.
	if w.iconStack == nil {
		return
	}

	w.iconStack.Objects = []fyne.CanvasObject{w.background, container.NewCenter(newInitial(server.Name))}
	w.iconStack.Refresh()
	w.loadIcon()
}

// loadIcon fills the icon slot with the server's picture. Not
// ImageCache.LoadIntoContainer, which has no way to be told the widget has since
// been given another server: this one drops an answer against a stale generation.
func (w *ServerWidget) loadIcon() {
	if w.iconStack == nil || w.Server.IconURL == "" {
		return
	}

	size := fyne.NewSize(theme.Sizes.ServerIconSize, theme.Sizes.ServerIconSize)
	generation := w.generation

	w.images.LoadAsync(w.Server.IconID, w.Server.IconURL, true, func(img image.Image) {
		if w.generation != generation {
			return
		}

		picture := canvas.NewImageFromImage(img)
		picture.FillMode = canvas.ImageFillContain
		picture.SetMinSize(size)

		w.iconStack.Objects = []fyne.CanvasObject{w.background, picture}
		w.iconStack.Refresh()
	})
}

// SetSelected updates the selection. A no-op when unchanged, so a sidebar-wide
// sync only repaints what moved.
func (w *ServerWidget) SetSelected(selected bool) {
	if w.selected == selected {
		return
	}

	w.selected = selected
	w.refreshAppearance()
}

// SetMentioned marks the server as holding a message that names the account. The
// rail says only that there is one — which channel and how many is what the
// channel sidebar answers once the server is open.
func (w *ServerWidget) SetMentioned(mentioned bool) {
	if w.mentioned == mentioned {
		return
	}

	w.mentioned = mentioned
	w.refreshAppearance()
}

func (w *ServerWidget) CreateRenderer() fyne.WidgetRenderer {
	iconSize := fyne.NewSize(theme.Sizes.ServerIconSize, theme.Sizes.ServerIconSize)

	w.iconStack = container.NewStack(w.background, container.NewCenter(newInitial(w.Server.Name)))
	w.loadIcon()

	grown := theme.Sizes.ServerIconSize * serverHoverGrowth
	w.baseLayout = layout.NewGridWrapLayout(iconSize)
	w.grownLayout = layout.NewGridWrapLayout(fyne.NewSize(grown, grown))
	w.iconWrapper = container.New(w.baseLayout, w.iconStack)

	w.marker.SetMinSize(fyne.NewSize(theme.Sizes.SelectionMarkerWidth, theme.Sizes.ServerMarkerHeight))
	w.refreshAppearance()

	// The bar is flush with the rail's left edge, so it is laid out apart from the
	// icon: the HBox pins it left, the Center inside gives it back its own height.
	markerRow := container.NewHBox(container.NewCenter(w.marker))

	return widget.NewSimpleRenderer(container.NewStack(markerRow, container.NewCenter(w.iconWrapper)))
}

func (w *ServerWidget) refreshAppearance() {
	if w.selected {
		w.background.FillColor = theme.Colors.ServerSelectedBg
	} else {
		w.background.FillColor = theme.Colors.ServerDefaultBg
	}

	// One bar, so selection outranks the mention: the server is open and its channel
	// sidebar is already carrying the amber row the rail would be pointing at.
	switch {
	case w.selected:
		w.marker.FillColor = theme.Colors.TextPrimary
	case w.mentioned:
		w.marker.FillColor = theme.Colors.MentionIndicator
	default:
		w.marker.FillColor = color.Transparent
	}

	w.background.Refresh()
	w.marker.Refresh()

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

func (w *ServerWidget) MouseIn(*desktop.MouseEvent) {
	w.hovered = true
	w.refreshAppearance()
	w.notifyHover(true)
}

func (w *ServerWidget) MouseOut() {
	w.hovered = false
	w.refreshAppearance()
	w.notifyHover(false)
}

func (w *ServerWidget) notifyHover(hovering bool) {
	if w.OnHover != nil {
		w.OnHover(hovering)
	}
}

/* Channel rows */

// ChannelWidget is a selectable channel row carrying selection and unread state.
type ChannelWidget struct {
	tapBase
	Channel domain.Channel

	// Menu supplies the items right-clicking the row offers.
	Menu func() []*fyne.MenuItem

	deps Deps // kept, the row outliving the channel it was built from

	background         *canvas.Rectangle
	selectionIndicator *canvas.Rectangle
	unreadIndicator    *canvas.Rectangle
	mentionBadge       *MentionBadge   // the count at the trailing end, hidden at zero
	typingMark         *TypingMark     // the trailing mark, hidden unless somebody is composing
	leading            *fyne.Container // holds the type glyph, or a conversation's avatar
	label              *canvas.Text
	labelBox           *fyne.Container // label fitted to its slot; see NewEllipsisText

	height float32 // a conversation card is taller than a channel row

	selected bool
	unread   bool
	mentions int  // how many messages here name the account
	typing   bool // what SetTyping was last told, so a collapsed row can resume it
	animate  bool
}

var (
	_ fyne.Tappable          = (*ChannelWidget)(nil)
	_ fyne.SecondaryTappable = (*ChannelWidget)(nil)
	_ desktop.Hoverable      = (*ChannelWidget)(nil)
)

// NewChannelWidget creates a channel row. The name is already resolved on the
// channel — a DM has none of its own, and the store is what turned it into the
// other participant.
func NewChannelWidget(deps Deps, channel domain.Channel, onTap func()) *ChannelWidget {
	label := newText(channel.Name, theme.Colors.CategoryText, theme.Sizes.ChannelLabelSize)
	label.Alignment = fyne.TextAlignLeading

	w := &ChannelWidget{
		Channel:            channel,
		deps:               deps,
		background:         canvas.NewRectangle(color.Transparent),
		selectionIndicator: canvas.NewRectangle(color.Transparent),
		unreadIndicator:    canvas.NewRectangle(color.Transparent),
		mentionBadge:       NewMentionBadge(),
		typingMark:         NewTypingMark(theme.Sizes.ChannelTypingSize, theme.Colors.TypingMark),
		// A slot rather than the object itself, so a row given another channel can
		// put a different lead in without the renderer being rebuilt around it.
		leading: container.NewStack(channelLeading(deps, channel)),
		height:  channelRowHeight(channel.Kind),
		label:   label,
		// Wrapped here rather than in CreateRenderer, which Fyne may run again after
		// a renderer is dropped: by then the label holds the shortened text.
		labelBox: NewEllipsisText(label),
	}

	// As on ServerWidget, Menu is assigned after construction and so is read when
	// the click arrives.
	w.onTap = onTap
	w.onSecondaryTap = func(event *fyne.PointEvent) { showMenuHook(w, w.Menu, event) }
	w.ExtendBaseWidget(w)

	return w
}

// SetChannel re-points the row at its channel as it now is, a no-op when nothing
// it draws has moved. The sidebar is rebuilt whole for an event about one channel
// — a call joined, a permission overwrite — so most rows in a rebuild are drawing
// exactly what they drew before, avatars included.
func (w *ChannelWidget) SetChannel(channel domain.Channel) {
	relabel := w.Channel.Name != channel.Name
	relead := w.Channel.Kind != channel.Kind || w.Channel.AvatarURL != channel.AvatarURL

	w.Channel = channel
	if !relabel && !relead {
		return
	}

	// Through the box, never the text object: ellipsisLayout rewrites that during
	// layout, so what it holds is whatever last fitted the column.
	if relabel {
		SetEllipsisText(w.labelBox, channel.Name)
	}
	if !relead {
		return
	}

	// Replaced rather than re-pointed: a picture already on its way lands in the
	// container it was handed, so the slot it fills is dropped whole with it.
	w.leading.Objects = []fyne.CanvasObject{channelLeading(w.deps, channel)}
	w.leading.Refresh()

	if height := channelRowHeight(channel.Kind); height != w.height {
		w.height = height
		w.background.SetMinSize(fyne.NewSize(0, height))
	}
}

// SetState updates selection, unread and the mention count together, a no-op
// when unchanged. The three are drawn against each other — the marker says which
// of unread and mentioned the row is — so nothing sets one of them alone.
func (w *ChannelWidget) SetState(selected, unread bool, mentions int) {
	if w.selected == selected && w.unread == unread && w.mentions == mentions {
		return
	}

	w.selected = selected
	w.unread = unread
	w.mentions = mentions
	w.mentionBadge.SetCount(mentions)
	w.refreshAppearance()
	w.Refresh()
}

// SetTyping marks the row as one somebody is composing in. Separate from SetState
// because every caller of that pair depends on its no-op guard, and typing
// arrives on its own schedule.
func (w *ChannelWidget) SetTyping(typing, animate bool) {
	w.typing, w.animate = typing, animate
	w.applyTyping()
}

// Hide and Show carry the mark's animation with the row. A collapsed category
// hides its channels, and Visible() is per object rather than per tree, so a mark
// left running would repaint something nobody can see sixty times a second. What
// the row was told is kept, so re-opening puts the sweep back at once.
func (w *ChannelWidget) Hide() {
	w.typingMark.SetActive(false, false)
	w.BaseWidget.Hide()
}

func (w *ChannelWidget) Show() {
	w.BaseWidget.Show()
	w.applyTyping()
}

func (w *ChannelWidget) applyTyping() {
	w.typingMark.SetActive(w.typing && w.Visible(), w.animate)
}

func (w *ChannelWidget) CreateRenderer() fyne.WidgetRenderer {
	w.selectionIndicator.SetMinSize(fyne.NewSize(theme.Sizes.SelectionMarkerWidth, 0))
	w.unreadIndicator.SetMinSize(fyne.NewSize(theme.Sizes.UnreadIndicatorWidth, 0))

	// Both indicators share the one marker slot; the unread bar is wrapped in an
	// HBox so it keeps its own narrower width and stays left-aligned.
	indicators := container.NewStack(w.selectionIndicator, container.NewHBox(w.unreadIndicator))

	// The name takes the leftover width, or a long DM title widens the column. The
	// typing mark rides in the right gutter, which held nothing but that gap: hidden
	// it costs no width, and it cannot be read as the unread bar at the other edge.
	// The count sits outside the typing mark, at the very end of the row: it is what
	// the row still says once nobody is composing, where the mark comes and goes.
	// Hidden it costs no width, so a row with neither is the row as it was.
	content := container.NewBorder(nil, nil,
		container.NewHBox(indicators, HorizontalSpacer(theme.Sizes.ChannelLeftPadding), w.leading),
		HBoxNoSpacing(
			container.NewCenter(w.typingMark),
			HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
			container.NewCenter(w.mentionBadge),
			HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		),
		w.labelBox,
	)

	w.background.SetMinSize(fyne.NewSize(0, w.height))
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

	// A mention takes the marker over rather than adding to it: every mention is
	// already unread, so the two can only ever be one bar saying which it is.
	switch {
	case w.mentions > 0:
		w.unreadIndicator.FillColor = theme.Colors.MentionIndicator
	case w.unread:
		w.unreadIndicator.FillColor = theme.Colors.UnreadIndicator
	default:
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

func (w *ChannelWidget) MouseIn(*desktop.MouseEvent) {
	if !w.selected {
		w.background.FillColor = theme.Colors.ChannelHoverBackground
		w.background.Refresh()
	}
}

func (w *ChannelWidget) MouseOut() { w.refreshAppearance() }

/* Voice participants */

// VoiceParticipantRow is one person in a voice channel's call, drawn under that
// channel's row in the sidebar: their avatar, their name in whatever colour their
// most senior role gives it, and a mark for a camera or a screen share. Tapping
// one opens their profile, as tapping them in the member sidebar does.
//
// Built per rebuild rather than recycled the way a MemberRow is: the channel
// column is replaced wholesale on every refresh, and a call holds a handful of
// people where the member list scrolls hundreds.
type VoiceParticipantRow struct {
	tapBase

	deps   Deps
	userID string

	// Menu is what a right-click offers: per-person volume and, where the
	// permissions allow it, the voice moderation. Read at the moment of the click
	// rather than captured, the way ChannelWidget and ServerWidget read theirs.
	Menu func() []*fyne.MenuItem

	background *canvas.Rectangle
	ring       *canvas.Circle
	speaking   bool
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*VoiceParticipantRow)(nil)
	_ desktop.Hoverable = (*VoiceParticipantRow)(nil)
)

// NewVoiceParticipantRow draws one participant. The name is already resolved —
// the store hands back the nickname and role colour the member sidebar would
// draw them with.
func NewVoiceParticipantRow(deps Deps, participant domain.VoiceParticipant) *VoiceParticipantRow {
	w := &VoiceParticipantRow{
		deps:       deps,
		userID:     participant.UserID,
		background: canvas.NewRectangle(color.Transparent),

		// The ring exists from construction and only its fill moves. Adding a
		// circle to a container when somebody starts talking would be a layout per
		// syllable, where a colour change is a repaint.
		ring: canvas.NewCircle(color.Transparent),
	}

	side := theme.Sizes.VoiceAvatarSize
	avatar := circularAvatar(deps.Images, participant.AvatarURL, fyne.NewSize(side, side))

	name := newText(participant.Name, voiceNameColor(participant), theme.Sizes.VoiceNameSize)
	name.Alignment = fyne.TextAlignLeading

	leading := HBoxNoSpacing(
		HorizontalSpacer(theme.Sizes.VoiceRowIndent),
		container.NewCenter(container.New(
			&memberRingLayout{band: theme.Sizes.VoiceSpeakingRing}, w.ring, avatar)),
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
	)

	// The name takes the leftover width in a Border's centre, as it does on a
	// member row: an HBox would hand the ellipsis box, which reports zero, zero.
	w.content = container.NewStack(w.background, container.NewBorder(nil, nil, leading,
		voiceMarks(participant), NewEllipsisText(name)))

	// Tapping opens the profile, as it does in the member sidebar. Joining a call
	// is deliberately not on this row: it is how you look somebody up, and a tap
	// that opens a microphone is not a tap anybody expects.
	w.onTap = func() { deps.Actions.OnUserTapped(participant.UserID, w) }
	w.onSecondaryTap = func(event *fyne.PointEvent) {
		if w.Menu == nil {
			return
		}

		showMenuHook(w, w.Menu, event)
	}
	w.ExtendBaseWidget(w)

	return w
}

// UserID is who the row is drawing, so the controller can find the one to mark
// without capturing anything per rebuild.
func (w *VoiceParticipantRow) UserID() string { return w.userID }

// SetSpeaking rings the avatar, or takes the ring away. A no-op on an unchanged
// value: this is called for every participant on every speaking change, and
// Canvas.dirty is one bool — any Refresh repaints the whole window, so the
// cheapest of these is the one that does not happen.
func (w *VoiceParticipantRow) SetSpeaking(speaking bool) {
	if w.speaking == speaking {
		return
	}
	w.speaking = speaking

	w.ring.FillColor = color.Transparent
	if speaking {
		w.ring.FillColor = theme.Colors.VoiceSpeaking
	}
	w.ring.Refresh()
}

func (w *VoiceParticipantRow) CreateRenderer() fyne.WidgetRenderer {
	w.background.SetMinSize(fyne.NewSize(0, theme.Sizes.VoiceRowHeight))

	return widget.NewSimpleRenderer(w.content)
}

func (w *VoiceParticipantRow) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.ChannelHoverBackground
	w.background.Refresh()
}

func (w *VoiceParticipantRow) MouseOut() {
	w.background.FillColor = color.Transparent
	w.background.Refresh()
}

// voiceMarks is what a participant is sharing, at the trailing end of their row.
// A bot mark rides with them: the same account is marked as one wherever it is
// drawn, and a call is one more place a bot turns up.
func voiceMarks(participant domain.VoiceParticipant) fyne.CanvasObject {
	marks := HBoxNoSpacing()

	if participant.Bot {
		marks.Add(container.NewCenter(NewBotMark(theme.Sizes.VoiceMarkSize)))
		marks.Add(HorizontalSpacer(theme.Sizes.VoiceMarkGap))
	}
	if participant.Screensharing {
		marks.Add(container.NewCenter(voiceMark(assets.ScreenshareIcon)))
		marks.Add(HorizontalSpacer(theme.Sizes.VoiceMarkGap))
	}
	if participant.Camera {
		marks.Add(container.NewCenter(voiceMark(assets.CameraIcon)))
		marks.Add(HorizontalSpacer(theme.Sizes.VoiceMarkGap))
	}
	marks.Add(HorizontalSpacer(theme.Sizes.ChannelLeftPadding))

	return marks
}

// voiceMark is one of those marks, tinted the way the sidebar's other glyphs are.
func voiceMark(res fyne.Resource) fyne.CanvasObject {
	side := theme.Sizes.VoiceMarkSize

	return newScaledIcon(tintedIcon(res, theme.Colors.VoiceParticipantMark), side)
}

// voiceNameColor colours a participant's name: their role's, or the row's own
// quieter default. Presence is deliberately not part of it — somebody in a call
// is in it whatever their account says it is doing.
func voiceNameColor(participant domain.VoiceParticipant) color.Color {
	if participant.Color != nil {
		return participant.Color
	}

	return theme.Colors.VoiceParticipantName
}

/* Channel categories */

// CategoryWidget is a collapsible category header, toggling the channel widgets
// registered through SetChannels. Through tapBase it accepts a right-click and,
// having no menu, does nothing with it — the same outcome as refusing one, but
// worth knowing the event stops here.
type CategoryWidget struct {
	tapBase
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
	w.onTap = w.toggle
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

	for _, channel := range w.channels {
		showIf(channel, !w.collapsed)
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
	title := newBoldText(w.title, theme.Colors.CategoryText, categoryTitleSize)

	indicatorRow := container.NewHBox(w.indicator, HorizontalSpacer(8))
	content := container.NewBorder(nil, nil, title, indicatorRow, nil)
	inner := container.NewStack(w.background, container.NewPadded(content))

	return &categoryRenderer{widget: w, inner: inner}
}

// toggle flips the category open or shut, which is what tapping it does.
func (w *CategoryWidget) toggle() {
	w.collapsed = !w.collapsed
	w.applyCollapsed()

	if w.onToggle != nil {
		w.onToggle(w.collapsed)
	}
}

func (w *CategoryWidget) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.ChannelHoverBackground
	w.background.Refresh()
}

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

// drawIndicator is a category's expand/collapse glyph: a minus when expanded, a
// plus when collapsed.
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

// avatarLed reports whether a row is led by a picture rather than a glyph, which
// is also what makes it the taller card. Every conversation but Saved Notes,
// whose picture would be this account's own avatar standing in for a notepad.
func avatarLed(kind domain.ChannelKind) bool {
	return kind.IsConversation() && kind != domain.ChannelSavedMessages
}

// channelRowHeight is how tall a row is drawn, which is decided by the same thing
// that decides what leads it: a conversation is a card led by a picture, and a
// channel a line led by a glyph.
func channelRowHeight(kind domain.ChannelKind) float32 {
	if avatarLed(kind) {
		return theme.Sizes.ConversationItemHeight
	}

	return theme.Sizes.ChannelItemHeight
}

// channelLeading is what precedes a channel's name in its row: a conversation's
// avatar, or the type glyph. Centred, the row being taller than either.
func channelLeading(deps Deps, channel domain.Channel) fyne.CanvasObject {
	if !avatarLed(channel.Kind) {
		return ChannelGlyph(channel.Kind)
	}

	side := theme.Sizes.ConversationAvatarSize
	avatar := circularAvatar(deps.Images, channel.AvatarURL, fyne.NewSize(side, side))

	return container.NewCenter(avatar)
}

// ChannelGlyph prefixes a channel's name in the sidebar row and the message
// header. Anything unrecognised — the zero value included, meaning nothing is
// selected yet — falls back to the hashtag.
func ChannelGlyph(kind domain.ChannelKind) fyne.CanvasObject {
	switch kind {
	case domain.ChannelVoice:
		return VoiceIcon()
	case domain.ChannelDM:
		return AtIcon()
	case domain.ChannelGroup:
		return GroupIcon()
	case domain.ChannelSavedMessages:
		return NotesIcon()
	}

	return HashtagIcon()
}

// glyphScale is the factor taking the 20-unit grid to the square a channel mark
// is drawn in.
func glyphScale() float32 { return theme.Sizes.HashtagIconSize / 20 }

// tintedGlyph is one of the client's own marks standing in for a drawn one, in
// the drawn glyphs' colour so the set reads as one. Used where the shape is more
// strokes than it is worth in canvas objects.
func tintedGlyph(res fyne.Resource) fyne.CanvasObject {
	return glyphBox(newScaledIcon(tintedIcon(res, theme.Colors.HashtagIcon), theme.Sizes.HashtagIconSize))
}

// HashtagIcon is the drawn "#" prefixing channel names.
func HashtagIcon() fyne.CanvasObject {
	line := glyphLine(theme.Colors.HashtagIcon, glyphScale())

	return glyphBox(container.NewWithoutLayout(
		line(7, 2, 7, 18),
		line(13, 2, 13, 18),
		line(2, 7, 18, 7),
		line(2, 13, 18, 13),
	))
}

// AtIcon is the "@" prefixing direct messages — set as text rather than drawn,
// the shape being a spiral no small set of straight lines renders convincingly.
func AtIcon() fyne.CanvasObject {
	glyph := newText("@", theme.Colors.HashtagIcon, theme.Sizes.HashtagIconSize*0.9)
	glyph.Alignment = fyne.TextAlignCenter

	return glyphBox(container.NewCenter(glyph))
}

// VoiceIcon is the speaker prefixing a voice channel. That channel is still one
// this client can only type in, so its row is a text channel's row and the mark
// is the whole of what tells them apart.
func VoiceIcon() fyne.CanvasObject { return tintedGlyph(assets.VoiceIcon) }

// NotesIcon is the notepad prefixing Saved Notes.
func NotesIcon() fyne.CanvasObject { return tintedGlyph(assets.NotesIcon) }

// GroupIcon is the two-head mark prefixing group channels: a pair of overlapping
// outlined circles.
func GroupIcon() fyne.CanvasObject {
	scale := glyphScale()

	head := func(cx, cy, r float32) *canvas.Circle {
		c := canvas.NewCircle(color.Transparent)
		c.StrokeColor = theme.Colors.HashtagIcon
		c.StrokeWidth = 2 * scale
		c.Move(fyne.NewPos((cx-r)*scale, (cy-r)*scale))
		c.Resize(fyne.NewSize(2*r*scale, 2*r*scale))

		return c
	}

	return glyphBox(container.NewWithoutLayout(head(13, 10, 5.5), head(7, 10, 5.5)))
}

/* Saved sessions */

// SessionCard is a clickable card for a saved login, with a remove button. It is
// a row on the sign-in card and wears what every card here wears: the client's
// one hairline, a theme radius and a name in the client's own type — a Fyne
// label would be the only widget-drawn text on the first surface anybody sees.
type SessionCard struct {
	widget.BaseWidget
	background *canvas.Rectangle
	avatar     *fyne.Container
	username   string
	onTap      func()
	onRemove   func()
}

// NewSessionCard creates a saved-session card, loading the avatar if available.
func NewSessionCard(images *cache.ImageCache, username, avatarURL string, onTap, onRemove func()) *SessionCard {
	background := canvas.NewRectangle(theme.Colors.SessionCardBg)
	background.CornerRadius = theme.Sizes.SessionCardRadius
	Outline(background)

	side := theme.Sizes.SessionCardAvatarSize

	c := &SessionCard{
		background: background,
		avatar:     circularAvatar(images, avatarURL, fyne.NewSize(side, side)),
		username:   username,
		onTap:      onTap,
		onRemove:   onRemove,
	}
	c.ExtendBaseWidget(c)

	return c
}

func (c *SessionCard) CreateRenderer() fyne.WidgetRenderer {
	// Ellipsised rather than clipped: a canvas.Text draws its whole width whatever
	// the row is resized to, and a handle is as long as somebody made it.
	name := NewEllipsisText(newBoldText(c.username,
		theme.Colors.TextPrimary, theme.Sizes.SessionCardNameSize))

	// Index 2 is the name: NewFillRow addresses the stretching child by position,
	// and the spacer before it is index 1.
	gap := theme.Sizes.SessionCardGap
	row := NewFillRow(2,
		container.NewCenter(c.avatar),
		HorizontalSpacer(gap),
		name,
		HorizontalSpacer(gap),
		container.NewCenter(NewCloseButton(c.onRemove)),
	)

	padding := theme.Sizes.SessionCardPadding
	tappable := NewTappableContainer(NewInset(row, padding, padding, padding, padding), c.onTap)

	return widget.NewSimpleRenderer(container.NewStack(c.background, tappable))
}
