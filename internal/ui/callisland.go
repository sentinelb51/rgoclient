package ui

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* The call island */

// CallIsland is the card floating at the top of the window: the call this
// account is in, the voice channel it is looking at, or both — and nothing at
// all when neither.
//
// It is one widget in one place rather than a surface per view. A call outlives
// leaving the channel, the server and the view, so the only slot that can hold
// it is the window's own: it is not a messageIsland (the modal layer's surface
// for the three message lists), not a composer dock badge (everything in that
// stack is about the open channel), and not a strip under the message header,
// which every view without one would have to draw for itself.
//
// It is drawn as the settings page's invite card — same radius, same padding,
// same two lines and same lifted outline — because it is the same shape doing
// the same job somewhere else, and two cards a shade apart read as a mistake.
//
// Two halves, either of which stands alone: the running call, and the voice
// channel on screen the account is *not* in. Each is a channel over the server
// it belongs to, with what can be done about it at its end. Both are up when a
// reader in one call is looking at another channel, with a rule between them.
//
// Under the live half — and only that half — runs the state bar: the
// connection's health as a colour, the word itself being what its tooltip is for.
// It ends at the rule rather than at the card's far edge, because what it reports
// is the running call and the other half is an offer with no state to report.
//
// Floating, so nothing below reserves room for it — see NewCallIslandLayer, and
// note that the card hides itself when it has nothing to say. Every setter
// leaves the card's own size to be settled by Sync, which is what the caller
// finishes with.
type CallIsland struct {
	widget.BaseWidget

	/* The live half */

	live        *fyne.Container // the whole half, hidden when there is no call
	callIcon    *islandIcon     // the picture leading it, hidden where there is none to draw
	callIconKey string          // what that picture was last loaded for
	callName    *canvas.Text
	callDetail  *canvas.Text      // the server, or how many are in the group
	callWhere   fyne.CanvasObject // the slot holding callDetail, hidden where there is nothing to say

	// An OutlinedIconButton bakes its mark, its tint and its disabled state at
	// construction, so a toggle here is a button *replaced* in its slot rather
	// than one recoloured. Two slots, two states each.
	micSlot       *fyne.Container
	headphoneSlot *fyne.Container

	/* The join half */

	join        *fyne.Container // the whole half, hidden when there is nothing to join
	joinIcon    *islandIcon
	joinIconKey string
	joinName    *canvas.Text
	joinDetail  *canvas.Text
	joinWhere   fyne.CanvasObject

	// The offer is a mark in a slot, for the reason the two toggles across the rule
	// are: an OutlinedIconButton bakes its tint and its disabled state at
	// construction, so a state change here replaces the button rather than
	// recolouring it.
	joinSlot *fyne.Container

	// rule stands between the halves and is drawn only when both are up.
	rule fyne.CanvasObject

	/* The state bar */

	// bar is the live half's bottom edge, not the card's, and is never hidden on its
	// own — it goes down with the half it belongs to. joinReserve is the height it
	// takes standing empty under the other half, so the two halves' lines line up.
	bar         *stateBar
	joinReserve fyne.CanvasObject

	content fyne.CanvasObject
	images  *cache.ImageCache

	onMute   func()
	onDeafen func()
	onJoin   func()

	muted    bool
	deafened bool
	joinable bool
}

var _ fyne.Widget = (*CallIsland)(nil)

// CallIslandActions is what the island's targets do. Each is called on the UI
// thread.
type CallIslandActions struct {
	OnMute    func()
	OnDeafen  func()
	OnHangUp  func()
	OnJoin    func() // joins the voice channel the reader is looking at
	OnChannel func() // the call's name is tappable and goes back to its channel

	// OnState reports the pointer entering or leaving the state bar, carrying the
	// word the bar's colour stands for. The tooltip is a layer the controller owns,
	// so the island can only ask for one.
	OnState func(text string, over fyne.CanvasObject, hovering bool)
}

// CallIslandWhere is where one half's channel is: the channel itself, what to
// say under it, and what to draw beside it. A server channel is its server; a
// group is how many are in it; a direct message is neither and gets a name and a
// face alone.
//
// The three picture fields are tried in that order — a picture of its own, then
// the faces of the people in it, then a letter — so a caller fills in whatever it
// has and the card draws the best of them.
type CallIslandWhere struct {
	Channel string
	Detail  string // the line under it, or "" for a half with nothing to add

	IconURL string
	Faces   []CallIslandFace
	Initial string // the name a missing picture takes its letter from
}

// CallIslandFace is one person in the cluster a group with no picture of its own
// is drawn as.
type CallIslandFace struct {
	Name      string
	AvatarURL string
}

// icon is everything the picture is drawn from, joined. setWhere re-asks for one
// exactly when this moves, which a sync running many times a second otherwise
// would every time — and unlike an ID it also covers a picture that changed
// without its channel doing.
func (at CallIslandWhere) icon() string {
	key := at.IconURL + "\x00" + at.Initial
	for _, face := range at.Faces {
		key += "\x00" + face.AvatarURL
	}

	return key
}

// drawn reports whether there is anything to put in the picture's slot at all.
func (at CallIslandWhere) drawn() bool {
	return at.IconURL != "" || at.Initial != "" || len(at.Faces) > 0
}

// callIslandNameLimit is how much of a name the card carries, channel and server
// alike. The card is as wide as what is in it and hangs over the header row, so
// a name nobody bothered to keep short must not push it across the window.
const callIslandNameLimit = 20

// NewCallIsland builds the card. It starts hidden and with both halves down:
// nothing shows one but a call starting or a voice channel being opened.
func NewCallIsland(images *cache.ImageCache, actions CallIslandActions) *CallIsland {
	w := &CallIsland{
		images:   images,
		onMute:   actions.OnMute,
		onDeafen: actions.OnDeafen,
		onJoin:   actions.OnJoin,
	}

	gap := theme.Sizes.CallIslandGap

	// The bar first: it is the live half's bottom edge, so that half is built
	// around it.
	w.buildBar(actions.OnState)
	w.buildLive(actions, gap)
	w.buildJoin(gap)
	w.rule = w.buildRule()

	background := canvas.NewRectangle(theme.Colors.CallIslandBackground)
	background.CornerRadius = theme.Sizes.CallIslandRadius
	background.SetMinSize(fyne.NewSize(0, theme.Sizes.CallIslandHeight))

	// Lighter than the client's one hairline, like the invite card's and the
	// context menu's: this stands over whatever was being read rather than meeting
	// another surface, and a darker edge would be a groove cut into the header.
	Outline(background)
	background.StrokeColor = theme.Colors.CallIslandOutline
	elevate(background, theme.Sizes.CallIslandShadowBlur)

	body := HBoxNoSpacing(w.live, w.rule, w.join)

	padV, padH := theme.Sizes.CallIslandPaddingV, theme.Sizes.CallIslandPaddingH

	// A sink rather than a bare stack: hit testing finds the deepest object that
	// matches, and a rectangle matches nothing — without this a click on the card
	// lands on the message header it is floating over.
	w.content = newTapSink(container.NewStack(background, NewInset(body, padV, padV, padH, padH)))

	w.ExtendBaseWidget(w)
	w.Hide()

	return w
}

// buildLive assembles the running call's half.
func (w *CallIsland) buildLive(actions CallIslandActions, gap float32) {
	w.callName, w.callDetail, w.callWhere = w.buildLines()
	w.callIcon = newIslandIcon()

	// The lines are the tappable half: the channel is the thing worth going back
	// to, and the buttons beside them are about the call rather than about where
	// it is. The icon is outside the target — it names the server rather than
	// offering anything, and it is not what the pointer lights.
	lines := newIslandLink(w.callLines(), func() {
		if actions.OnChannel != nil {
			actions.OnChannel()
		}
	}, w.lightCall)

	w.micSlot = container.NewStack(w.micButton())
	w.headphoneSlot = container.NewStack(w.headphoneButton())

	// Outlined rather than plain: these are targets, and an outlined destructive
	// button wears the hairline in its own tint, so hanging up needs no other
	// signal. The same three marks the invite card's own actions wear.
	hangUp := NewOutlinedIconButton(
		tintedIcon(assets.CallEndIcon, theme.Colors.CallIslandDanger),
		theme.Colors.CallIslandDanger,
		func() {
			if actions.OnHangUp != nil {
				actions.OnHangUp()
			}
		})

	buttons := NewGapRow(theme.Sizes.CallIslandButtonGap,
		w.micSlot, w.headphoneSlot, hangUp)

	// The bar is the half's own bottom edge rather than the card's: what it reports
	// is this call, and the other half is an offer with no state to report. It ends
	// where the half does, a gap short of the rule — the same margin it starts at.
	iconGap := theme.Sizes.CallIslandIconGap
	row := NewGapRow(gap, NewGapRow(iconGap, w.callIcon, lines), vcenter(buttons))

	w.live = NewFillColumn(0, row, w.bar)
	w.live.Hide()
}

// buildJoin assembles the half offering the voice channel on screen.
func (w *CallIsland) buildJoin(gap float32) {
	w.joinName, w.joinDetail, w.joinWhere = w.buildLines()
	w.joinIcon = newIslandIcon()
	w.joinSlot = container.NewStack(w.joinButton())

	// The reserve is the bar's height standing empty, so both halves' lines sit at
	// the same level: without it the join half would centre over the card's whole
	// height where the live half centres over what is left above its bar. It is
	// shown and hidden with the bar rather than always — a card offering only a
	// join would otherwise carry a strip of nothing along its bottom.
	w.joinReserve = VerticalSpacer(theme.Sizes.CallIslandBarHeight + theme.Sizes.CallIslandBarGap)
	w.joinReserve.Hide()

	iconGap := theme.Sizes.CallIslandIconGap
	row := NewGapRow(gap, NewGapRow(iconGap, w.joinIcon, w.joinLines()), vcenter(w.joinSlot))

	w.join = NewFillColumn(0, row, w.joinReserve)
	w.join.Hide()
}

// lightCall brightens the call's two lines under the pointer and puts them back
// after. A fill behind them is what NewTappableContainer would draw, and on a
// card that *is* the only panel in play it reads as a button nobody drew an edge
// on; the words being the target, lighting the words is what says so.
func (w *CallIsland) lightCall(hovering bool) {
	name, server := theme.Colors.CallIslandText, theme.Colors.CallIslandMuted
	if hovering {
		name, server = theme.Colors.CallIslandTextHover, theme.Colors.CallIslandText
	}

	w.callName.Color = name
	w.callName.Refresh()

	w.callDetail.Color = server
	w.callDetail.Refresh()
}

// buildLines is a half's two lines: the channel, and where it is under it. The
// second line's slot is what is hidden when there is nothing to say — hiding the
// text alone would leave the slot standing, and a gapped column charges a gap for
// every *visible* child, an empty one included.
func (w *CallIsland) buildLines() (name, detail *canvas.Text, where fyne.CanvasObject) {
	name = newText("", theme.Colors.CallIslandText, theme.Sizes.CallIslandNameSize)
	detail = newText("", theme.Colors.CallIslandMuted, theme.Sizes.CallIslandDetailSize)

	where = VBoxNoSpacing(detail)
	where.Hide()

	return name, detail, where
}

// callLines and joinLines stack a half's two lines, centred against whatever
// stands beside them.
func (w *CallIsland) callLines() fyne.CanvasObject {
	return vcenter(NewGapColumn(theme.Sizes.CallIslandLineGap, VBoxNoSpacing(w.callName), w.callWhere))
}

func (w *CallIsland) joinLines() fyne.CanvasObject {
	return vcenter(NewGapColumn(theme.Sizes.CallIslandLineGap, VBoxNoSpacing(w.joinName), w.joinWhere))
}

// buildRule is the seam between the halves: a hairline inset from the card's
// edges, so it divides the two rather than cutting the card in half. Not
// NewColumnDivider — the client's one hairline is darker than every surface it
// meets, and this card is darker than the hairline.
func (w *CallIsland) buildRule() fyne.CanvasObject {
	gap := theme.Sizes.CallIslandGap
	line := sizedRect(theme.Colors.CallIslandOutline, theme.Sizes.OutlineWidth, 0)

	rule := NewInset(line, theme.Sizes.CallIslandPaddingV, theme.Sizes.CallIslandPaddingV, gap, gap)
	rule.Hide()

	return rule
}

// buildBar is the strip along the live half's bottom edge.
func (w *CallIsland) buildBar(onState func(string, fyne.CanvasObject, bool)) {
	w.bar = newStateBar(func(hovering bool) {
		if onState != nil {
			onState(w.bar.state, w.bar, hovering)
		}
	})
}

func (w *CallIsland) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

/* The live half */

// SetCall shows the running call, named by its channel and the server it is in.
// An empty server drops that line — a call in a conversation is in none. Call
// Sync after.
func (w *CallIsland) SetCall(where CallIslandWhere) {
	w.callIconKey = w.setWhere(w.callName, w.callDetail, w.callWhere, w.callIcon, w.callIconKey, where)
	w.live.Show()
	w.joinReserve.Show()
}

// ClearCall takes the running call's half down, the state bar going with it: an
// offer has no connection to report on, and the other half no longer has one to
// line up against.
func (w *CallIsland) ClearCall() {
	w.live.Hide()
	w.joinReserve.Hide()
}

// SetState says how the call is doing. good is false for anything the reader
// would want to know about. The word is not drawn — it is what the bar's tooltip
// answers with — so a card in a call that is fine says nothing it would have to
// be reread.
func (w *CallIsland) SetState(text string, good bool) {
	tint := theme.Colors.CallIslandStatePoor
	if good {
		tint = theme.Colors.CallIslandStateGood
	}

	w.bar.set(text, tint)
}

// SetMuted and SetDeafened redraw the two toggles. Both are no-ops on an
// unchanged value — a call reports its state more often than its state changes.
func (w *CallIsland) SetMuted(muted bool) {
	if w.muted == muted {
		return
	}
	w.muted = muted

	w.micSlot.Objects = []fyne.CanvasObject{w.micButton()}
	w.micSlot.Refresh()
}

func (w *CallIsland) SetDeafened(deafened bool) {
	if w.deafened == deafened {
		return
	}
	w.deafened = deafened

	w.headphoneSlot.Objects = []fyne.CanvasObject{w.headphoneButton()}
	w.headphoneSlot.Refresh()

	// A deafened call is a muted one, and the mic button says so by being drawn
	// disabled rather than by pretending it can be pressed.
	w.micSlot.Objects = []fyne.CanvasObject{w.micButton()}
	w.micSlot.Refresh()
}

/* The join half */

// SetJoin offers the named voice channel, greying the button where the account
// may not connect: the offer is still drawn, a voice channel that cannot be
// entered being worth saying so, and a button that simply went missing reads as a
// client that forgot. Call Sync after.
func (w *CallIsland) SetJoin(where CallIslandWhere, joinable bool) {
	w.joinIconKey = w.setWhere(w.joinName, w.joinDetail, w.joinWhere, w.joinIcon, w.joinIconKey, where)

	w.joinable = joinable
	w.joinSlot.Objects = []fyne.CanvasObject{w.joinButton()}
	w.joinSlot.Refresh()

	w.join.Show()
}

// ClearJoin takes that half down.
func (w *CallIsland) ClearJoin() {
	w.join.Hide()
}

/* Settling */

// Sync shows the card when either half has something to say and hides it when
// neither does, and draws the rule only between two halves that are both up.
// Whatever the halves did, the card's own size has changed, and a widget does
// not re-measure the layer it floats on: the caller relayouts that layer after.
// Call on the UI thread.
func (w *CallIsland) Sync() {
	if w.live.Visible() && w.join.Visible() {
		w.rule.Show()
	} else {
		w.rule.Hide()
	}

	if w.live.Visible() || w.join.Visible() {
		w.Show()
	} else {
		w.Hide()
	}

	w.Refresh()
}

/* Parts */

// setWhere relabels a half and points its picture at where the channel is,
// dropping either where there is nothing to say. It returns the icon the slot now
// holds, for the caller to keep: the load is re-issued only when that moves, or
// every sync would ask the cache for a picture already on screen.
//
// The texts are no-ops on unchanged text for the same reason — every setter here
// is called from a sync that runs far more often than anything it says changes.
func (w *CallIsland) setWhere(name, detail *canvas.Text, where fyne.CanvasObject,
	icon *islandIcon, loaded string, at CallIslandWhere) string {
	setIslandText(name, util.Truncate(at.Channel, callIslandNameLimit))

	if at.Detail == "" {
		where.Hide()
	} else {
		setIslandText(detail, util.Truncate(at.Detail, callIslandNameLimit))
		where.Show()
	}

	if !at.drawn() {
		icon.Hide()
		return ""
	}

	key := at.icon()
	if loaded != key {
		w.loadIcon(icon, at)
	}
	icon.Show()

	return key
}

// loadIcon fills a slot with the best picture the half has: its own, the faces of
// the people in it, or the letter both fall back to. The slot is *refilled*
// rather than re-pointed: a load already in flight lands in the container it was
// handed, so one reused for another channel would take the picture of the one
// before it.
func (w *CallIsland) loadIcon(slot *islandIcon, at CallIslandWhere) {
	switch {
	case at.IconURL != "":
		slot.setPicture(w.images, at.IconURL, at.Initial)

	// One face is a picture rather than a cluster: a stack of one is a small
	// circle where the slot has room for a whole one, and reads as a group whose
	// other members failed to load.
	case len(at.Faces) == 1:
		slot.setPicture(w.images, at.Faces[0].AvatarURL, at.Faces[0].Name)

	case len(at.Faces) > 1:
		slot.setFaces(w.images, at.Faces)

	default:
		slot.setPicture(w.images, "", at.Initial)
	}
}

// islandIcon is the slot a half's picture is drawn in, empty until a half has
// one. A widget rather than a bare container because the row it sits in stretches
// every child to its full height, and neither a grid nor a stack centres its
// contents in what it is given.
//
// Its width is whatever it currently holds — one circle, or a cluster wider than
// one — so the row beside it reserves the right room.
type islandIcon struct {
	widget.BaseWidget

	body  *fyne.Container // one child: a picture's slot, or the cluster
	width float32
}

var _ fyne.Widget = (*islandIcon)(nil)

func newIslandIcon() *islandIcon {
	i := &islandIcon{body: container.NewStack(), width: theme.Sizes.CallIslandIconSize}
	i.ExtendBaseWidget(i)
	i.Hide()

	return i
}

func (i *islandIcon) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewCenter(i.body))
}

func (i *islandIcon) MinSize() fyne.Size {
	return fyne.NewSize(i.width, theme.Sizes.CallIslandIconSize)
}

// setPicture draws one circular picture over the letter it falls back to, the way
// an invite card's icon does.
func (i *islandIcon) setPicture(images *cache.ImageCache, iconURL, initial string) {
	side := theme.Sizes.CallIslandIconSize

	background := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	slot := container.NewStack(background, container.NewCenter(newInitial(initial)))

	i.width = side
	i.body.Objects = []fyne.CanvasObject{container.NewGridWrap(fyne.NewSize(side, side), slot)}
	i.body.Refresh()

	if iconURL == "" || images == nil {
		return
	}

	images.LoadIntoContainer(imageCacheID(iconURL), iconURL, fyne.NewSize(side, side), slot, true, background)
}

// setFaces draws the people in a group instead, overlapping. Each wears a band of
// the card's own colour, which is what cuts it out of the one behind rather than
// letting two circles smudge into one shape.
//
// The objects are reversed because Fyne paints in order and the *first* face is
// the one that should be on top: it is leftmost, and a stack fanning out to the
// right is what every other client draws.
func (i *islandIcon) setFaces(images *cache.ImageCache, faces []CallIslandFace) {
	side, ring := theme.Sizes.CallIslandFaceSize, theme.Sizes.CallIslandFaceRing

	objects := make([]fyne.CanvasObject, 0, len(faces))
	for _, face := range faces {
		// The letter at the detail line's size rather than the theme's own: a face is
		// two thirds of the slot a single picture gets, and one drawn at full size
		// runs under the face in front of it.
		initial := newInitial(face.Name)
		initial.TextSize = theme.Sizes.CallIslandDetailSize

		placeholder := canvas.NewCircle(theme.Colors.AvatarPlaceholder)
		slot := container.NewStack(placeholder, container.NewCenter(initial))

		if face.AvatarURL != "" && images != nil {
			images.LoadIntoContainer(imageCacheID(face.AvatarURL), face.AvatarURL,
				fyne.NewSize(side, side), slot, true, placeholder)
		}

		halo := canvas.NewCircle(theme.Colors.CallIslandBackground)
		objects = append(objects, container.NewStack(halo, NewInset(slot, ring, ring, ring, ring)))
	}
	slices.Reverse(objects)

	cluster := container.New(&facesLayout{}, objects...)

	i.width = cluster.MinSize().Width
	i.body.Objects = []fyne.CanvasObject{cluster}
	i.body.Refresh()
}

// facesLayout places a cluster's faces from the right, so the one given first
// ends up leftmost and painted last. Every face is the same square — the band
// included — and each stands one step from the last.
type facesLayout struct{}

func (l *facesLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	outer := faceOuter()
	step := theme.Sizes.CallIslandFaceStep
	last := len(objects) - 1
	top := (size.Height - outer) / 2

	for i, face := range objects {
		face.Resize(fyne.NewSize(outer, outer))
		face.Move(fyne.NewPos(float32(last-i)*step, top))
	}
}

func (l *facesLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.Size{}
	}

	outer := faceOuter()

	return fyne.NewSize(outer+float32(len(objects)-1)*theme.Sizes.CallIslandFaceStep, outer)
}

// faceOuter is a face and the band around it, which is the square one occupies.
func faceOuter() float32 {
	return theme.Sizes.CallIslandFaceSize + 2*theme.Sizes.CallIslandFaceRing
}

// micButton builds the microphone toggle in whatever state the island is in. It
// is rebuilt rather than recoloured because an OutlinedIconButton bakes its tint
// at construction, and a disabled one is a different button rather than a
// different colour.
func (w *CallIsland) micButton() *OutlinedIconButton {
	res, tint := assets.MicIcon, theme.Colors.CallIslandText
	if w.muted || w.deafened {
		res, tint = assets.MicOffIcon, theme.Colors.CallIslandDanger
	}

	button := NewOutlinedIconButton(tintedIcon(res, tint), tint, func() {
		if w.onMute != nil {
			w.onMute()
		}
	})

	// Deafened holds the microphone whatever the mute button says, so pressing it
	// would be a press with no effect.
	if w.deafened {
		return button.disabled()
	}

	return button
}

// joinButton builds the offer in whatever state the island is in, rebuilt for the
// same reason micButton is. A mark rather than the word: it stands in line with
// the three across the rule with nothing but the rule between them, and a
// labelled button among four squares reads as a different kind of control.
func (w *CallIsland) joinButton() *OutlinedIconButton {
	if !w.joinable {
		tint := theme.Colors.ButtonDisabledText

		return NewOutlinedIconButton(tintedIcon(assets.CallJoinIcon, tint), tint, nil).disabled()
	}

	tint := theme.Colors.CallIslandJoin

	return NewOutlinedIconButton(tintedIcon(assets.CallJoinIcon, tint), tint, func() {
		if w.onJoin != nil {
			w.onJoin()
		}
	})
}

// headphoneButton is the same for the speakers, which have no third state.
func (w *CallIsland) headphoneButton() *OutlinedIconButton {
	res, tint := assets.HeadphonesIcon, theme.Colors.CallIslandText
	if w.deafened {
		res, tint = assets.HeadphonesOffIcon, theme.Colors.CallIslandDanger
	}

	return NewOutlinedIconButton(tintedIcon(res, tint), tint, func() {
		if w.onDeafen != nil {
			w.onDeafen()
		}
	})
}

/* The link */

// islandLink is a tappable block that lights its *text* on hover rather than
// filling a rectangle behind it. A fill the height of two lines reads as a button
// nobody drew an edge on, and on a card that is the only panel in play there is
// nothing for it to read as part of; brightening the words is what says they are
// the target. What lighting means is the caller's — this only reports the hover.
type islandLink struct {
	tapBase

	content fyne.CanvasObject
	onHover func(bool)
}

var (
	_ fyne.Tappable     = (*islandLink)(nil)
	_ desktop.Hoverable = (*islandLink)(nil)
)

func newIslandLink(content fyne.CanvasObject, onTap func(), onHover func(bool)) *islandLink {
	l := &islandLink{content: content, onHover: onHover}
	l.onTap = onTap
	l.ExtendBaseWidget(l)

	return l
}

func (l *islandLink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.content)
}

func (l *islandLink) MouseIn(*desktop.MouseEvent)    { reportHover(l.onHover, true) }
func (l *islandLink) MouseOut()                      { reportHover(l.onHover, false) }
func (l *islandLink) MouseMoved(*desktop.MouseEvent) {}

/* The state bar */

// stateBar is the strip along the card's bottom edge saying how the call is
// doing. A widget rather than a rectangle for one reason: a canvas object cannot
// be hovered, and the word the colour stands for is only ever read by asking for
// it. Nothing above it is hoverable, so this steals no hover from a parent.
type stateBar struct {
	widget.BaseWidget

	fill    *canvas.Rectangle
	content fyne.CanvasObject
	state   string
	onHover func(bool)
}

var (
	_ fyne.Widget       = (*stateBar)(nil)
	_ desktop.Hoverable = (*stateBar)(nil)
)

func newStateBar(onHover func(bool)) *stateBar {
	b := &stateBar{
		fill:    canvas.NewRectangle(theme.Colors.CallIslandStateGood),
		onHover: onHover,
	}
	b.fill.CornerRadius = theme.Sizes.CallIslandBarRadius

	// The gap above the bar belongs to the bar rather than standing between it and
	// the lines: three pixels is not something anybody can point at, and a hover
	// target has to be reachable by a hand aiming at what it can see.
	b.content = NewFillColumn(0, VerticalSpacer(0),
		NewFixedHeightContainer(theme.Sizes.CallIslandBarHeight, b.fill))

	b.ExtendBaseWidget(b)

	return b
}

func (b *stateBar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

// MinSize is the bar plus the room above it that is hovered as if it were the
// bar; the width is whatever the card gives it.
func (b *stateBar) MinSize() fyne.Size {
	return fyne.NewSize(0, theme.Sizes.CallIslandBarHeight+theme.Sizes.CallIslandBarGap)
}

// set records the word the bar stands for and paints the colour it stands for.
// A no-op on an unchanged colour: a call reports its state far more often than
// the state moves, and the word is only ever read on hover.
func (b *stateBar) set(state string, tint color.Color) {
	b.state = state

	if b.fill.FillColor == tint {
		return
	}

	b.fill.FillColor = tint
	b.fill.Refresh()
}

func (b *stateBar) MouseIn(*desktop.MouseEvent)    { reportHover(b.onHover, true) }
func (b *stateBar) MouseOut()                      { reportHover(b.onHover, false) }
func (b *stateBar) MouseMoved(*desktop.MouseEvent) {}

/* Text */

// setIslandText relabels a text object in place, a no-op on unchanged text.
func setIslandText(text *canvas.Text, value string) {
	if text.Text == value {
		return
	}

	text.Text = value
	text.Refresh()
}

/* The layer it floats on */

// NewCallIslandLayer is the slot the island hangs in: the top of the window,
// centred, inset by its own margin. A layer rather than a row of the main
// layout — Fyne grows a window to its content's minimum the frame it outgrows it
// and never gives the room back, so a card that came and went would resize the
// window every time a call started.
//
// Stack it under the notice and settings layers: a notice is an answer to
// something the reader just did, and the settings page is a surface of its own
// that nothing should float over. The tooltip layer has to be *above* it, the
// state bar's word being drawn there.
func NewCallIslandLayer(island fyne.CanvasObject) *fyne.Container {
	margin := theme.Sizes.CallIslandMargin

	// A VBox is what pins the card to the top: it hands its child the width and
	// its own minimum height, leaving the rest of the window untouched, where a
	// Border would charge theme padding around it.
	return NewLayer(container.NewVBox(
		NewInset(container.NewCenter(island), margin, 0, margin, margin)))
}
