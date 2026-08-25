package ui

// The friends list: the row at the top of the home sidebar and the page it
// opens. Revolt has no collection of relationships to fetch — each is filed on
// the person it is with — so the controller resolves them and this draws what it
// is handed.
//
// A page where the messages go rather than a card on the modal layer. The list
// is four sections deep and every row carries what can be done about somebody,
// which is a surface rather than an answer to a question: a dialog holding it had
// to cap its own height, crowd its rows and put a wall of labelled buttons down
// its right-hand edge. Standing in the message area it is read the way the
// settings page is — one column, centred, captioned sections — and it refills in
// place, accepting a request being an action whose whole result is the list
// moving.
//
// A row is the settings page's invite card: the same island, the same rim, the
// same outlined marks at its end. It is the one island in the client that is also
// a target — the card *is* the row's primary action, writing to somebody where
// there is a conversation to open — so it fills and lifts its rim under the
// pointer where an invite card, the same island, only sits there. What is left at
// its end is the rare and the destructive, which is the whole reason the common
// one is not a button beside them. The picture leading the card is the way to the
// profile, and the only part of the card that does not do what the card does.

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

/* The sidebar row */

// FriendsRow is the way into the friends page, above the conversations in the
// home sidebar. It answers to selection as a channel row does — the page it opens
// stands where a channel's messages would — and marks itself as an unread channel
// does while requests are waiting, that being the only part of the list owed an
// answer.
type FriendsRow struct {
	tapBase

	background         *canvas.Rectangle
	selectionIndicator *canvas.Rectangle
	pendingBar         *canvas.Rectangle
	label              *canvas.Text

	selected bool
	pending  bool
}

var (
	_ fyne.Tappable     = (*FriendsRow)(nil)
	_ desktop.Hoverable = (*FriendsRow)(nil)
)

// NewFriendsRow creates the sidebar row.
func NewFriendsRow(onTap func()) *FriendsRow {
	label := newText("Friends", theme.Colors.CategoryText, theme.Sizes.ChannelLabelSize)

	w := &FriendsRow{
		background:         canvas.NewRectangle(color.Transparent),
		selectionIndicator: canvas.NewRectangle(color.Transparent),
		pendingBar:         canvas.NewRectangle(color.Transparent),
		label:              label,
	}
	w.onTap = onTap
	w.ExtendBaseWidget(w)

	return w
}

// SetState marks the row as the open view and as one owed an answer. The two are
// set together for the reason a channel row's are: they share the marker slot and
// the label's colour, so nothing sets one of them alone. A no-op when unchanged,
// so a sidebar-wide sync costs nothing for a row that held.
func (w *FriendsRow) SetState(selected, pending bool) {
	if w.selected == selected && w.pending == pending {
		return
	}

	w.selected, w.pending = selected, pending
	w.refreshAppearance()
	w.Refresh()
}

func (w *FriendsRow) CreateRenderer() fyne.WidgetRenderer {
	w.selectionIndicator.SetMinSize(fyne.NewSize(theme.Sizes.SelectionMarkerWidth, 0))
	w.pendingBar.SetMinSize(fyne.NewSize(theme.Sizes.UnreadIndicatorWidth, 0))
	w.background.SetMinSize(fyne.NewSize(0, theme.Sizes.ChannelItemHeight))
	w.refreshAppearance()

	// The marker slot, the padding and the glyph are the channel row's, so the two
	// line up despite being different widgets — both indicators sharing the one slot
	// there and here, the narrower wrapped so it stays at its own width.
	indicators := container.NewStack(w.selectionIndicator, container.NewHBox(w.pendingBar))
	leading := container.NewHBox(
		indicators,
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		GroupIcon(),
	)
	content := container.NewBorder(nil, nil, leading, nil, NewEllipsisText(w.label))

	return widget.NewSimpleRenderer(container.NewStack(w.background, content))
}

func (w *FriendsRow) refreshAppearance() {
	w.background.FillColor = color.Transparent
	w.selectionIndicator.FillColor = color.Transparent
	if w.selected {
		w.background.FillColor = theme.Colors.ChannelSelectedBg
		w.selectionIndicator.FillColor = theme.Colors.TextPrimary
	}

	w.pendingBar.FillColor = color.Transparent
	if w.pending {
		w.pendingBar.FillColor = theme.Colors.UnreadIndicator
	}

	w.label.Color = theme.Colors.CategoryText
	if w.selected || w.pending {
		w.label.Color = theme.Colors.TextPrimary
	}

	w.background.Refresh()
	w.selectionIndicator.Refresh()
	w.pendingBar.Refresh()
	w.label.Refresh()
}

func (w *FriendsRow) MouseIn(*desktop.MouseEvent) {
	if w.selected {
		return
	}

	w.background.FillColor = theme.Colors.ChannelHoverBackground
	w.background.Refresh()
}

func (w *FriendsRow) MouseOut() { w.refreshAppearance() }

/* What the page is handed */

// FriendEntry is one person in the list. Buttons are the ProfileButtons a card
// offers, built by the same controller policy — what applies to somebody is a
// question about the relationship, and asking it twice is how surfaces disagree.
type FriendEntry struct {
	UserID    string
	Name      string
	Handle    string
	AvatarURL string
	Presence  domain.Presence
	Buttons   []ProfileButton

	// Open is what tapping the card does — the row's *primary* action, drawn as no
	// button at all: writing to somebody is the one thing done from a friends list
	// often, and a target for it beside two that end a relationship is a hand aiming
	// at the wrong square. Nil where the relationship has no such action — Revolt
	// opens a conversation only between friends — and the card falls back to the
	// profile, which is the whole of what there is to do about a request or a block.
	Open func()
}

// FriendSection is a heading and whoever is under it. An empty one is dropped
// rather than drawn as a heading over nothing.
type FriendSection struct {
	Title   string
	Detail  string // the line under the caption, saying what the section is
	Entries []FriendEntry

	// Folded is the state the section is *first* drawn in — shut, for the one
	// nobody opens this page to read. What the reader does with it after that is
	// the page's to remember, and outranks this.
	Folded bool
}

/* The page */

// FriendsPage is the whole surface, mounted in the message area and hidden until
// the sidebar row is tapped. SetSections refills it.
type FriendsPage struct {
	widget.BaseWidget

	deps    Deps
	list    *fyne.Container // the sections themselves, replaced wholesale on a refill
	content fyne.CanvasObject
	onUser  func(userID string, anchor fyne.CanvasObject)

	// sections is the last answer the controller gave, kept so folding one can
	// redraw from it: what a fold changes is how the page is *drawn*, and asking
	// the controller for the list again to find that out would be a walk of every
	// relationship for a click on a heading.
	sections []FriendSection

	// folded is what the reader has shut, by caption — a fixed set of headings,
	// named in one place. Absent means the section's own Folded still stands, so a
	// default and a decision are told apart rather than the map having to be
	// primed. It outlives a refill and dies with the page, as a collapsed channel
	// category outlives a sidebar rebuild and dies with the session.
	folded map[string]bool

	/* Asking somebody new */

	handle *widget.Entry
	ask    *Button
}

var _ fyne.Widget = (*FriendsPage)(nil)

// NewFriendsPage builds the page. onUser opens somebody's profile from their
// row and onAsk sends a friend request to a typed handle, both called on the UI
// thread; onAsk reports back through done so the field is cleared by what took
// and kept by what did not.
func NewFriendsPage(deps Deps, onUser func(userID string, anchor fyne.CanvasObject),
	onAsk func(handle string, done func(sent bool))) *FriendsPage {

	p := &FriendsPage{
		deps:   deps,
		list:   VBoxNoSpacing(),
		onUser: onUser,
	}

	padding := theme.Sizes.FriendsPagePadding
	body := NewPlainVScroll(
		friendsCentred(NewInset(p.list, 0, padding, padding, padding)))

	// The ask stands between the header and the list rather than in it: the list is
	// replaced wholesale on every refill, and presence alone refills it — a field
	// rebuilt under somebody typing would lose what they had typed.
	p.content = NewFillColumn(2, p.buildHeader(), p.buildAsk(onAsk), body)
	p.ExtendBaseWidget(p)
	p.Hide()

	return p
}

func (p *FriendsPage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.content)
}

// buildHeader is the row over the list, built as the message header is: the same
// padding, the same bold label and the same kind of glyph in front of it, so
// swapping one view for the other moves nothing along the top of the window.
func (p *FriendsPage) buildHeader() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Friends", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	return container.NewPadded(
		container.NewBorder(nil, nil, container.NewHBox(GroupIcon(), title), nil))
}

// buildAsk is the row that reaches somebody the client has never drawn. Every
// other way to a person is a surface they appear on — a message, a member row —
// so without this an account you were simply told the name of cannot be reached
// at all. The placeholder spells the shape Revolt matches on: it looks accounts
// up by the name *and* the discriminator and guesses at neither, so a bare name
// finds nobody.
//
// It is a card in the column the list stands in rather than a control in the
// header: it is one of the things this page is for, not a way to look at it.
func (p *FriendsPage) buildAsk(onAsk func(handle string, done func(sent bool))) fyne.CanvasObject {
	p.handle = widget.NewEntry()
	p.handle.SetPlaceHolder("name#0000")

	// Tap rather than the action: it is the only path that reads the disabled state,
	// so Enter cannot send a second request while the first is out.
	p.handle.OnSubmitted = func(string) { p.ask.Tap() }

	p.ask = NewWeightedButton("Add friend", ButtonPrimary, func() {
		handle := strings.TrimSpace(p.handle.Text)
		if handle == "" {
			return
		}

		p.ask.Disable()
		onAsk(handle, func(sent bool) {
			if sent {
				p.handle.SetText("")
			}
			p.ask.Enable()
		})
	})

	gap := theme.Sizes.FriendsGap
	padV, padH := theme.Sizes.FriendsCardPaddingV, theme.Sizes.FriendsCardPaddingH
	padding := theme.Sizes.FriendsPagePadding

	row := NewFillRow(0, vcenter(fieldSurface(p.handle)), HorizontalSpacer(gap), vcenter(p.ask))
	card := container.NewStack(newIslandCard(),
		NewMinHeightContainer(theme.Sizes.FriendsRowHeight, NewInset(row, padV, padV, padH, padH)))

	return friendsCentred(NewInset(card, padding, 0, padding, padding))
}

// SetSections replaces the whole list, dropping the sections nobody is in. Call
// on the UI thread.
func (p *FriendsPage) SetSections(sections []FriendSection) {
	p.sections = sections
	p.redraw()
}

// redraw builds the list from what the controller last gave and what the reader
// has shut. A folded section's cards are not built at all rather than built and
// hidden — which is the point of folding one: a page carrying tens of stale
// requests should cost what the heading costs, not what the requests do.
func (p *FriendsPage) redraw() {
	// The cards about to be dropped include whichever button the pointer is on, and
	// a discarded widget hears nothing: it will never report the pointer leaving.
	p.hideTip()

	var rows []fyne.CanvasObject

	for _, section := range p.sections {
		if len(section.Entries) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.FriendsGroupGap))
		}

		folded := p.isFolded(section)
		rows = append(rows, p.header(section, folded))
		if folded {
			continue
		}

		for i, entry := range section.Entries {
			if i > 0 {
				rows = append(rows, VerticalSpacer(theme.Sizes.FriendsCardGap))
			}
			rows = append(rows, p.card(entry))
		}
	}

	if len(rows) == 0 {
		rows = []fyne.CanvasObject{p.empty()}
	}

	p.list.Objects = rows
	p.list.Refresh()
}

// isFolded answers for one section: what the reader decided, or the state the
// controller asked it to start in.
func (p *FriendsPage) isFolded(section FriendSection) bool {
	if shut, decided := p.folded[section.Title]; decided {
		return shut
	}

	return section.Folded
}

// fold shuts or opens a section and redraws. The whole list is rebuilt rather
// than the one section's rows shown and hidden: they are not built while it is
// shut, so there is nothing standing there to reveal.
func (p *FriendsPage) fold(title string, shut bool) {
	if p.folded == nil {
		p.folded = make(map[string]bool)
	}
	p.folded[title] = shut

	p.redraw()
}

/* A section's heading */

// header names a section, counts it and folds it. The count is what makes a
// folded one still worth reading — it is the whole of what the section says once
// its rows are gone — and the caption is upper-cased as the settings page's group
// headers are, a section here and a section there being the same thing.
//
// The explanation goes *inside* the heading rather than under it, so the whole
// block is one target and a shut section costs one line instead of two: what it
// explains is rows that are not on screen.
func (p *FriendsPage) header(section FriendSection, folded bool) fyne.CanvasObject {
	h := &friendsHeader{background: canvas.NewRectangle(color.Transparent)}
	h.background.CornerRadius = theme.Sizes.ButtonRadius
	h.onTap = func() { p.fold(section.Title, !folded) }

	caption := newBoldText(strings.ToUpper(section.Title)+" — "+strconv.Itoa(len(section.Entries)),
		theme.Colors.CategoryText, theme.Sizes.FriendsCaptionSize)

	gap := theme.Sizes.FriendsGap

	// drawIndicator already centres itself in a box of its own, which is what a row
	// that stretches every child needs — a line stretched is a line moved.
	lines := []fyne.CanvasObject{
		NewFillRow(2, drawIndicator(!folded), HorizontalSpacer(gap), vcenter(caption)),
	}

	// Indented past the mark so it starts where the caption does: the mark is about
	// the section, and a sentence beginning under it would read as the mark's own.
	if section.Detail != "" && !folded {
		detail := newText(section.Detail, theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)
		lines = append(lines,
			VerticalSpacer(gap*headerLineGap),
			NewInset(NewEllipsisText(detail), 0, 0, theme.Sizes.CategoryIndicatorSize+gap, 0))
	}

	// The strip is the column's full width, as a channel category's is the sidebar's:
	// a heading that folds is a target, and one the width of its own words leaves
	// most of the line dead. Its text starts where a card's padding does, so the
	// mark sits over the pictures below it.
	padH := theme.Sizes.FriendsCardPaddingH
	h.content = container.NewStack(h.background,
		NewInset(VBoxNoSpacing(lines...), gap, gap, padH, padH))
	h.ExtendBaseWidget(h)

	return NewInset(h, 0, gap, 0, 0)
}

// headerLineGap is the share of the row gap that separates a caption from its own
// explanation — half, the two being one thing where the gap below the block
// separates it from another.
const headerLineGap = 0.5

// friendsHeader is a section's heading: a target the width of the column, which
// is what a heading that folds has to be. It fills under the pointer rather than
// lighting its text, the way a channel category does — this is a strip in a list,
// where the call island's lines are the only thing on their card.
type friendsHeader struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*friendsHeader)(nil)
	_ desktop.Hoverable = (*friendsHeader)(nil)
)

func (h *friendsHeader) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(h.content)
}

// ChannelHoverBackground rather than ButtonHoverBg: this is a heading in a list,
// which is what a channel category is, and the button's own fill is a step
// brighter than the cards under it — a slab the width of the column, lighter than
// anything it names, reads as the thing to look at.
func (h *friendsHeader) MouseIn(*desktop.MouseEvent) { h.fill(theme.Colors.ChannelHoverBackground) }
func (h *friendsHeader) MouseOut()                   { h.fill(color.Transparent) }

func (h *friendsHeader) fill(colour color.Color) {
	h.background.FillColor = colour
	h.background.Refresh()
}

// empty is what stands in for the whole list when there is nobody in any section:
// an island of its own rather than a bare line, so a page with nothing to say is
// still the page rather than a sentence floating on the background.
func (p *FriendsPage) empty() fyne.CanvasObject {
	line := newText("Nobody yet. Open somebody's profile to ask them to be friends.",
		theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)

	padV, padH := theme.Sizes.FriendsCardPaddingV, theme.Sizes.FriendsCardPaddingH

	return container.NewStack(newIslandCard(),
		NewMinHeightContainer(theme.Sizes.FriendsRowHeight,
			NewInset(vcenter(NewEllipsisText(line)), padV, padV, padH, padH)))
}

/* One person */

// card draws one person: their picture and name at one end, what can be done
// about them at the other. The card *is* the row's primary action — a
// conversation where there is one to open — and the picture leading it is the way
// to their profile, which is where everything neither offers lives.
func (p *FriendsPage) card(entry FriendEntry) fyne.CanvasObject {
	c := &friendCard{background: newIslandCard(), userID: entry.UserID}

	profile := func() {
		if p.onUser != nil {
			p.onUser(c.userID, c)
		}
	}

	c.onTap = entry.Open
	if c.onTap == nil {
		c.onTap = profile
	}

	name := newBoldText(entry.Name, theme.Colors.TextPrimary, theme.Sizes.FriendsNameSize)
	handle := newText(entry.Handle, theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)

	lines := VBoxNoSpacing(NewEllipsisText(name), NewEllipsisText(handle))

	gap := theme.Sizes.FriendsGap
	padV, padH := theme.Sizes.FriendsCardPaddingV, theme.Sizes.FriendsCardPaddingH

	// The name column takes the leftover width, so a long one shortens rather than
	// pushing the buttons off the card.
	row := NewFillRow(2,
		p.face(entry, c, profile),
		HorizontalSpacer(gap),
		vcenter(lines),
		HorizontalSpacer(gap),
		vcenter(p.buttons(entry, c)),
	)

	c.content = container.NewStack(c.background,
		NewMinHeightContainer(theme.Sizes.FriendsRowHeight, NewInset(row, padV, padV, padH, padH)))
	c.ExtendBaseWidget(c)
	c.setHovered(false)

	return c
}

// face is the picture leading a card, in the presence ring the member sidebar
// draws — and the one part of the card that does not do what the card does. It
// is an islandLink rather than a TappableContainer for the reason the call
// island's lines are: a fill of its own inside a card that already fills under
// the pointer is a second shape appearing. The hover goes back to the card, so
// the card stays lit while the hand is on the picture, and the tooltip is what
// says the picture leads somewhere else — an avatar opening a profile is this
// client's rule everywhere, but a rule is not a label.
func (p *FriendsPage) face(entry FriendEntry, card *friendCard, onTap func()) fyne.CanvasObject {
	side := theme.Sizes.FriendsAvatarSize
	avatar := circularAvatar(p.deps.Images, entry.AvatarURL, fyne.NewSize(side, side))

	ring := canvas.NewCircle(presenceColor(entry.Presence))
	ring.Hidden = !entry.Presence.IsOnline()

	face := container.New(&memberRingLayout{band: theme.Sizes.MemberPresenceRing}, ring, avatar)

	var link *islandLink
	link = newIslandLink(face, onTap, func(hovering bool) {
		card.setHovered(hovering)
		p.tip("Profile", link, hovering)
	})

	return container.NewCenter(link)
}

// buttons is what the row offers, each an outlined mark in its own tint — the
// invite list's shape, and for its reason: the row's own text is a person, and
// three labelled buttons beside one are more to read than the row itself. What a
// mark means is the tooltip's to say. An action with no mark of its own keeps its
// label rather than going missing.
func (p *FriendsPage) buttons(entry FriendEntry, card *friendCard) fyne.CanvasObject {
	offered := make([]fyne.CanvasObject, 0, len(entry.Buttons))

	for _, action := range entry.Buttons {
		mark, tint := friendMark(action)
		if mark == nil {
			offered = append(offered, newProfileButton(action, false))
			continue
		}

		// The hover is handed back to the card, or the card goes out from under the
		// hand reaching for the button: the deepest object gets the event.
		button := NewOutlinedIconButton(tintedIcon(mark, tint), tint, action.Do)
		button.reporting(func(hovering bool) {
			card.setHovered(hovering)
			p.tip(action.Label, button, hovering)
		})

		offered = append(offered, button)
	}

	return NewGapRow(theme.Sizes.IconButtonGap, offered...)
}

// tip names the mark under the pointer. The label is the one the card draws in
// words, so the two surfaces cannot come to call the same action different things.
//
// ShowAbove rather than Show: these sit at the *right* end of a card that already
// reaches the column's edge, and Show places the label past that edge without
// clamping — where the rail's tooltips, which is what Show is for, have a whole
// window to their right.
func (p *FriendsPage) tip(label string, over fyne.CanvasObject, hovering bool) {
	if !hovering {
		p.hideTip()
		return
	}

	if p.deps.Tooltip != nil {
		p.deps.Tooltip.ShowAbove(label, over)
	}
}

// Hide takes the page down, and any label it left standing with it: the button
// under the pointer is about to stop being drawn and will never report the
// pointer leaving.
func (p *FriendsPage) Hide() {
	p.hideTip()
	p.BaseWidget.Hide()
}

func (p *FriendsPage) hideTip() {
	if p.deps.Tooltip != nil {
		p.deps.Tooltip.Hide()
	}
}

// friendMark is the glyph and the tint one action is drawn in, or a nil resource
// for one with no mark of its own. The tint is the whole of what says which sort
// of action it is: green makes a relationship, amber takes back something that
// can be asked for again, and red ends one.
//
// ProfileActionMessage has no mark here on purpose — it is the *card's* tap
// rather than a button, so the buttons a card carries are the rare ones and two
// of them are destructive. A target for writing to somebody beside those is a
// hand aiming at the wrong square.
func friendMark(action ProfileButton) (fyne.Resource, color.Color) {
	switch action.Action {
	case ProfileActionAccept:
		return assets.ActionSaveIcon, theme.Colors.SwiftActionConfirm
	case ProfileActionDecline:
		return assets.ActionCancelIcon, theme.Colors.SwiftActionCaution
	case ProfileActionAdd:
		return assets.SystemAddedIcon, theme.Colors.SwiftActionConfirm
	case ProfileActionRemove:
		return assets.SystemRemovedIcon, theme.Colors.SwiftActionDanger
	case ProfileActionBlock:
		return assets.ForbiddenIcon, theme.Colors.SwiftActionDanger
	case ProfileActionUnblock:
		return assets.ActionUnblockIcon, theme.Colors.SwiftActionConfirm
	}

	return nil, nil
}

// friendCard is the island one person is drawn on, and the way to whatever the
// row is for. It fills under the pointer *and* lifts its rim — the outline is
// drawn at rest here as everywhere in this client, so hover has to brighten it
// rather than add one — which is what says a card is a target where an invite
// card, the same island, is only read. The buttons and the picture on it report
// their own hover back rather than letting the card go dark under the hand
// reaching for one.
type friendCard struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject
	userID     string
}

var (
	_ fyne.Tappable     = (*friendCard)(nil)
	_ desktop.Hoverable = (*friendCard)(nil)
)

// cardHoverLift is how far towards white the card's rim goes under the pointer.
// Twice buttonHoverLift and short of the outlined button's own quarter: a
// hairline round a whole row carries a lift further than one round a square, and
// the fill has already answered.
const cardHoverLift = 0.25

func (c *friendCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *friendCard) MouseIn(*desktop.MouseEvent) { c.setHovered(true) }
func (c *friendCard) MouseOut()                   { c.setHovered(false) }

func (c *friendCard) setHovered(on bool) {
	fill, rim := theme.Colors.SessionCardBg, theme.Colors.SettingsIslandOutline
	if on {
		fill = theme.Colors.FriendsCardHoverBg
		rim = theme.Lighten(theme.Colors.SettingsIslandOutline, cardHoverLift)
	}
	if c.background.FillColor == fill {
		return
	}

	c.background.FillColor = fill
	c.background.StrokeColor = solidColor(rim)
	c.background.Refresh()
}

/* The column it stands in */

// friendsCentred caps the list at FriendsPageWidth and centres it. The settings
// page does the same with a fixed width, which it can afford being a layer over
// the whole window; this stands in the message area, which is as narrow as
// MessageAreaMinWidth, so the width is a ceiling the column shrinks under.
func friendsCentred(content fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&cappedWidthLayout{max: theme.Sizes.FriendsPageWidth}, content)
}
