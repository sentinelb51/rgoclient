package ui

// The two cards a group conversation is made and grown by. One names a group and
// picks who starts in it, the other adds to one that already exists; they are the
// same card with and without a name field, because they ask the same question at
// two moments — which of the people you are friends with.
//
// Friends is not a shortlist here, it is the whole of what Revolt takes: the
// create route refuses a stranger and the whole request with them. So what is on
// offer is the controller's walk of the relationships, and this package draws
// what it is handed.
//
// A row is the friends page's own card at the same sizes — the same island, the
// same avatar and ring, the same name over handle — minus that page's buttons.
// The whole row is one answer, so the only thing at its end is the mark saying
// whether it has been given.

import (
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* What a card is offered */

// GroupCandidate is one person a group card can be answered with. The same
// fields a friends row is drawn from, and for the same reason: it is the same
// person on the same island, so the two must not come to look different.
type GroupCandidate struct {
	UserID    string
	Name      string
	Handle    string
	AvatarURL string
	Presence  domain.Presence
}

// groupCard is everything the two cards differ in. Held as a value rather than
// spelled out twice so the shared builder is the only place either is assembled.
type groupCard struct {
	title   string
	label   string // what the list of people is called
	empty   string // what stands in its place when nobody is on offer
	action  string
	pending string

	// named draws the name field, which only a new group has — Revolt takes the
	// name at creation and an addition names nothing. needsAny withholds the button
	// until somebody is picked: adding nobody is a request that would do nothing,
	// where a group of one is a group somebody can be added to later.
	named    bool
	needsAny bool
}

/* The card */

// GroupDialog is either card. It reports its own outcome the way every other card
// on the modal layer does — the status line under the fields, the button
// re-enabled — so a refusal is corrected where it was typed.
type GroupDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to focus
	// once it is up. Entry is nil on the card that adds to a group: it has no field,
	// and focusing the list would take Escape away from the layer.
	Entry   fyne.Focusable
	Content fyne.CanvasObject

	status dialogStatus
	action *Button
}

// NewGroupDialog builds the card that makes a group. onSubmit is called on the UI
// thread with the name and whoever was picked; onClose dismisses the modal layer.
func NewGroupDialog(deps Deps, people []GroupCandidate,
	onSubmit func(name string, userIDs []string), onClose func()) *GroupDialog {

	return newGroupDialog(deps, groupCard{
		title:   "New group",
		label:   "People",
		empty:   "Add a friend first — a group is made from people you know.",
		action:  "Create",
		pending: "Creating...",
		named:   true,
	}, people, onSubmit, onClose)
}

// NewGroupInviteDialog builds the card that adds to one. It is handed only the
// friends who are not already in the group, so what it lists is what it can do.
func NewGroupInviteDialog(deps Deps, group string, people []GroupCandidate,
	onSubmit func(userIDs []string), onClose func()) *GroupDialog {

	return newGroupDialog(deps, groupCard{
		title:    "Add to " + group,
		label:    "Friends",
		empty:    "Everybody you're friends with is already here.",
		action:   "Add",
		pending:  "Adding...",
		needsAny: true,
	}, people, func(_ string, userIDs []string) { onSubmit(userIDs) }, onClose)
}

func newGroupDialog(deps Deps, card groupCard, people []GroupCandidate,
	onSubmit func(name string, userIDs []string), onClose func()) *GroupDialog {

	d := &GroupDialog{}

	var name *modalEntry
	if card.named {
		name = newModalEntry(onClose)
		name.SetPlaceHolder("Book club")

		// Enter in the name field answers the card, as it does in a prompt. Tap rather
		// than the action itself: it is the only path that reads the disabled state.
		name.OnSubmitted = func(string) { d.action.Tap() }

		d.Entry = name
	}

	picker := newGroupPicker(deps, card, people, func(picked int) {
		if card.needsAny {
			enableIf(d.action, picked > 0)
		}
	})

	d.status = newDialogStatus()
	d.action = NewWeightedButton(card.action, ButtonPrimary, func() {
		d.status.set(card.pending, theme.Colors.TimestampText)
		d.action.Disable()

		var typed string
		if name != nil {
			typed = name.Text
		}

		onSubmit(typed, picker.picked())
	})
	enableIf(d.action, !card.needsAny)

	rows := []fyne.CanvasObject{dialogHeader(card.title, onClose), widget.NewSeparator()}
	if name != nil {
		rows = append(rows, dialogField("Name", fieldSurface(name)))
	}
	rows = append(rows, picker.field, d.status.row(), d.action)

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.GroupDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))

	return d
}

// Fail reports a refused request and re-enables the button, so what it came from
// can be corrected and sent again. Call on the UI thread.
func (d *GroupDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.action.Enable()
}

/* The list of people */

// groupPicker is the list a card is answered with: who is on offer, and the line
// counting how many of them have been picked. The answer is derived from the rows
// rather than kept beside them — a row owns whether it is picked, because it is
// the thing that draws it — which is also what keeps the answer in the order the
// list was offered in.
type groupPicker struct {
	rows []*pickRow

	count *canvas.Text
	field fyne.CanvasObject // the caption and the list, built as one of the card's fields
}

func newGroupPicker(deps Deps, card groupCard, people []GroupCandidate, onPick func(picked int)) *groupPicker {
	p := &groupPicker{
		count: newText("", theme.Colors.TimestampText, theme.Sizes.JoinDialogTextSize),
	}

	// The caption is a field's label with the count held at the far end, so the two
	// are read as one line: what this is, and how far it has been answered.
	label := newBoldText(strings.ToUpper(card.label), theme.Colors.CategoryText, theme.Sizes.DialogLabelSize)
	caption := NewFillRow(0, vcenter(NewEllipsisText(label)), vcenter(p.count))

	body := p.buildBody(deps, card, people, onPick)

	// Bound to its caption by the gap every other field on the card uses, rather
	// than by the wider one between fields.
	p.field = VBoxNoSpacing(caption, VerticalSpacer(theme.Sizes.DialogLabelGap), body)

	return p
}

// buildBody is the list itself, or what stands in its place when nobody is on
// offer.
func (p *groupPicker) buildBody(deps Deps, card groupCard,
	people []GroupCandidate, onPick func(picked int)) fyne.CanvasObject {

	if len(people) == 0 {
		return groupPickerEmpty(card.empty)
	}

	rows := make([]fyne.CanvasObject, 0, len(people))
	for _, candidate := range people {
		row := newPickRow(deps, candidate, func() { onPick(p.recount()) })

		p.rows = append(p.rows, row)
		rows = append(rows, row)
	}

	// The list is measured rather than the scroller: a scroller has no opinion about
	// its own height, so a card of forty friends would be taller than the window.
	list := NewGapColumn(theme.Sizes.FriendsCardGap, rows...)

	return container.New(
		&cappedHeightLayout{content: list, max: theme.Sizes.GroupPickerHeight},
		NewPlainVScroll(list))
}

// picked is who the card is answered with, in the order they were offered.
func (p *groupPicker) picked() []string {
	ids := make([]string, 0, len(p.rows))
	for _, row := range p.rows {
		if row.chosen {
			ids = append(ids, row.userID)
		}
	}

	return ids
}

// recount repaints the line beside the caption and reports what it now says.
// Nobody picked says nothing rather than "0 selected": the list underneath
// already says so.
func (p *groupPicker) recount() int {
	n := len(p.picked())

	text := ""
	if n > 0 {
		text = strconv.Itoa(n) + " selected"
	}
	if p.count.Text != text {
		p.count.Text = text
		p.count.Refresh()
	}

	return n
}

// groupPickerEmpty stands in for the whole list when there is nobody to offer: an
// island of its own rather than a bare line, as the friends page's empty state is,
// so a card with nothing to list is still a card.
func groupPickerEmpty(line string) fyne.CanvasObject {
	text := newText(line, theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)
	padV, padH := theme.Sizes.FriendsCardPaddingV, theme.Sizes.FriendsCardPaddingH

	return container.NewStack(newIslandCard(),
		NewMinHeightContainer(theme.Sizes.FriendsRowHeight,
			NewInset(vcenter(NewEllipsisText(text)), padV, padV, padH, padH)))
}

/* One person */

// pickRow is one person as an answer that can be given and taken back. It is the
// friends page's card — the same island, the same fill under the pointer — with
// one difference the whole row turns on: it also has a *chosen* fill, which hover
// must not overwrite, an answer already given outranking a pointer passing over.
type pickRow struct {
	tapBase

	background *canvas.Rectangle
	mark       *pickMark
	content    fyne.CanvasObject

	userID  string
	hovered bool
	chosen  bool
}

var (
	_ fyne.Tappable     = (*pickRow)(nil)
	_ desktop.Hoverable = (*pickRow)(nil)
)

func newPickRow(deps Deps, candidate GroupCandidate, onPick func()) *pickRow {
	w := &pickRow{
		background: newIslandCard(),
		mark:       newPickMark(),
		userID:     candidate.UserID,
	}
	w.onTap = func() {
		w.chosen = !w.chosen
		w.mark.set(w.chosen)
		w.repaint()
		onPick()
	}

	side := theme.Sizes.FriendsAvatarSize
	avatar := circularAvatar(deps.Images, candidate.AvatarURL, fyne.NewSize(side, side))
	ring := canvas.NewCircle(presenceColor(candidate.Presence))
	ring.Hidden = !candidate.Presence.IsOnline()

	name := newBoldText(candidate.Name, theme.Colors.TextPrimary, theme.Sizes.FriendsNameSize)
	handle := newText(candidate.Handle, theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)
	lines := VBoxNoSpacing(NewEllipsisText(name), NewEllipsisText(handle))

	gap := theme.Sizes.FriendsGap
	padV, padH := theme.Sizes.FriendsCardPaddingV, theme.Sizes.FriendsCardPaddingH

	// The name column takes the leftover width, so a long one shortens rather than
	// pushing the mark off the card.
	row := NewFillRow(2,
		container.NewCenter(container.New(
			&memberRingLayout{band: theme.Sizes.MemberPresenceRing}, ring, avatar)),
		HorizontalSpacer(gap),
		vcenter(lines),
		HorizontalSpacer(gap),
		vcenter(w.mark),
	)

	w.content = container.NewStack(w.background,
		NewMinHeightContainer(theme.Sizes.FriendsRowHeight, NewInset(row, padV, padV, padH, padH)))
	w.repaint()
	w.ExtendBaseWidget(w)

	return w
}

func (w *pickRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

func (w *pickRow) MouseIn(*desktop.MouseEvent) {
	w.hovered = true
	w.repaint()
}

func (w *pickRow) MouseOut() {
	w.hovered = false
	w.repaint()
}

// repaint fills the card for what it now is. Chosen outranks hovered: the pointer
// is passing over, and the fill has an answer to report.
func (w *pickRow) repaint() {
	fill := theme.Colors.SessionCardBg
	switch {
	case w.chosen:
		fill = theme.Colors.GroupPickChosenBg
	case w.hovered:
		fill = theme.Colors.FriendsCardHoverBg
	}
	if w.background.FillColor == fill {
		return
	}

	w.background.FillColor = fill
	w.background.Refresh()
}

/* The mark at the end of a row */

// pickMark says whether a row has been answered for: an empty rim while it has
// not, the same rim in the confirming tint with a tick inside once it has.
//
// Not a button, and not hoverable. The row is the target — the whole of it is one
// answer — and Fyne gives an event to the deepest object that accepts it, so
// either would put a hole in the middle of the row's own hover.
type pickMark struct {
	widget.BaseWidget

	ring *canvas.Circle
	tick *canvas.Image
}

var _ fyne.Widget = (*pickMark)(nil)

func newPickMark() *pickMark {
	side := theme.Sizes.GroupPickMarkSize

	ring := canvas.NewCircle(color.Transparent)
	ring.StrokeWidth = theme.Sizes.OutlineWidth

	m := &pickMark{
		ring: ring,
		tick: newScaledIcon(tintedIcon(assets.ActionSaveIcon, theme.Colors.SwiftActionConfirm), side*0.6),
	}
	m.set(false)
	m.ExtendBaseWidget(m)

	return m
}

func (m *pickMark) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(m.ring, container.NewCenter(m.tick)))
}

// MinSize keeps the mark square whatever the tick inside it measures, so a column
// of rows ends on one line.
func (m *pickMark) MinSize() fyne.Size {
	side := theme.Sizes.GroupPickMarkSize

	return fyne.NewSize(side, side)
}

func (m *pickMark) set(chosen bool) {
	m.ring.StrokeColor = theme.Colors.Outline
	if chosen {
		m.ring.StrokeColor = theme.Colors.SwiftActionConfirm
	}

	m.tick.Hidden = !chosen
	m.ring.Refresh()
	m.tick.Refresh()
}
