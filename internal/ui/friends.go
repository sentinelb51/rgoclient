package ui

// The friends list: the row at the top of the home sidebar and the dialog it
// opens. Revolt has no collection of relationships to fetch — each is filed on
// the person it is with — so the controller resolves them and this draws what it
// is handed. The dialog refills in place: accepting a request is an action whose
// whole result is the list moving, and closing to show that would take the rest
// of the answers with it.

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

/* The sidebar row */

// FriendsRow is the way into the friends list, above the conversations in the
// home sidebar. It marks itself as an unread channel does while requests are
// waiting, that being the only part of the list owed an answer.
type FriendsRow struct {
	tapBase

	background *canvas.Rectangle
	pendingBar *canvas.Rectangle
	label      *canvas.Text

	pending bool
}

var (
	_ fyne.Tappable     = (*FriendsRow)(nil)
	_ desktop.Hoverable = (*FriendsRow)(nil)
)

// NewFriendsRow creates the sidebar row.
func NewFriendsRow(onTap func()) *FriendsRow {
	label := newText("Friends", theme.Colors.CategoryText, theme.Sizes.ChannelLabelSize)

	w := &FriendsRow{
		background: canvas.NewRectangle(color.Transparent),
		pendingBar: canvas.NewRectangle(color.Transparent),
		label:      label,
	}
	w.onTap = onTap
	w.ExtendBaseWidget(w)

	return w
}

// SetPending marks the row while friend requests are waiting on an answer. A
// no-op when unchanged, so a sidebar-wide sync costs nothing for a row that held.
func (w *FriendsRow) SetPending(pending bool) {
	if w.pending == pending {
		return
	}

	w.pending = pending
	w.refreshAppearance()
	w.Refresh()
}

func (w *FriendsRow) CreateRenderer() fyne.WidgetRenderer {
	w.pendingBar.SetMinSize(fyne.NewSize(theme.Sizes.UnreadIndicatorWidth, 0))
	w.background.SetMinSize(fyne.NewSize(0, theme.Sizes.ChannelItemHeight))
	w.refreshAppearance()

	// The marker slot, the padding and the glyph are the channel row's, so the two
	// line up despite being different widgets. The slot is as wide as a *selection*
	// marker though nothing here draws one: a channel row stacks both and takes the
	// wider, so sizing to the pending bar alone would shift this row two pixels left.
	leading := container.NewHBox(
		container.NewStack(HorizontalSpacer(theme.Sizes.SelectionMarkerWidth), container.NewHBox(w.pendingBar)),
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		GroupIcon(),
	)
	content := container.NewBorder(nil, nil, leading, nil, NewEllipsisText(w.label))

	return widget.NewSimpleRenderer(container.NewStack(w.background, content))
}

func (w *FriendsRow) refreshAppearance() {
	if w.pending {
		w.pendingBar.FillColor = theme.Colors.UnreadIndicator
		w.label.Color = theme.Colors.TextPrimary
	} else {
		w.pendingBar.FillColor = color.Transparent
		w.label.Color = theme.Colors.CategoryText
	}

	w.background.FillColor = color.Transparent

	w.background.Refresh()
	w.pendingBar.Refresh()
	w.label.Refresh()
}

func (w *FriendsRow) MouseIn(*desktop.MouseEvent) {
	w.background.FillColor = theme.Colors.ChannelHoverBackground
	w.background.Refresh()
}

func (w *FriendsRow) MouseOut() { w.refreshAppearance() }

/* The dialog */

// FriendEntry is one person in the list. Buttons are the ProfileButtons a card
// offers, built by the same controller policy — what applies to somebody is a
// question about the relationship, and asking it twice is how surfaces disagree.
type FriendEntry struct {
	UserID    string
	Name      string
	Handle    string
	AvatarURL string
	Buttons   []ProfileButton
}

// FriendSection is a heading and whoever is under it. An empty one is dropped
// rather than drawn as a heading over nothing.
type FriendSection struct {
	Title   string
	Entries []FriendEntry

	// Awaiting marks a section owed an answer, the only place a row emphasises its
	// first button. Elsewhere the heading has already said what the row is about,
	// and a coloured slab per row would be the loudest thing in a list mostly read.
	Awaiting bool
}

// FriendsDialog lists this account's relationships, grouped. Content goes on the
// modal layer; SetSections refills it.
type FriendsDialog struct {
	Content fyne.CanvasObject

	deps   Deps
	list   *fyne.Container // the sections themselves, replaced wholesale on a refill
	empty  *canvas.Text
	onUser func(userID string, anchor fyne.CanvasObject)
}

// NewFriendsDialog builds the dialog. onUser opens somebody's profile from their
// row; onClose dismisses the layer. Both are called on the UI thread.
func NewFriendsDialog(deps Deps, onUser func(userID string, anchor fyne.CanvasObject), onClose func()) *FriendsDialog {
	pad := theme.Sizes.FriendsPadding
	width := theme.Sizes.FriendsDialogWidth

	d := &FriendsDialog{
		deps:   deps,
		list:   VBoxNoSpacing(),
		empty:  newText("Nobody yet. Add somebody from their profile.", theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize),
		onUser: onUser,
	}
	d.empty.Hide()

	// The scroller cannot be asked how tall it wants to be — container.Scroll reports
	// its own current height as its minimum — so the list is measured and the ceiling
	// applied here.
	viewport := container.New(
		&cappedHeightLayout{content: d.list, max: theme.Sizes.FriendsListMaxHeight},
		NewPlainVScroll(d.list))

	body := VBoxNoSpacing(
		dialogHeader("Friends", onClose),
		NewInset(VBoxNoSpacing(d.empty, viewport), 0, pad, pad, pad),
	)

	// Fixed rather than minimum width, as a profile card is: every row shortens to
	// what it is given, so no name can widen the dialog.
	d.Content = newTapSink(NewFixedWidthContainer(width, container.NewStack(newDialogCard(), body)))

	return d
}

// SetSections replaces the whole list. A section gained or lost changes the
// card's height, so the caller repositions the overlay. Call on the UI thread.
func (d *FriendsDialog) SetSections(sections []FriendSection) {
	rows := make([]fyne.CanvasObject, 0, len(sections)*2)
	for _, section := range sections {
		if len(section.Entries) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.FriendsGap))
		}

		rows = append(rows, d.caption(section))
		for _, entry := range section.Entries {
			rows = append(rows, d.row(entry, section.Awaiting))
		}
	}

	d.list.Objects = rows
	d.list.Refresh()

	showIf(d.empty, len(rows) == 0)
}

// caption names a section and counts it — the count says whether the heading is
// worth reading before the rows under it are.
func (d *FriendsDialog) caption(section FriendSection) fyne.CanvasObject {
	text := newBoldText(section.Title+" — "+strconv.Itoa(len(section.Entries)),
		theme.Colors.TimestampText, theme.Sizes.FriendsSectionSize)

	return NewInset(text, theme.Sizes.FriendsGap, theme.Sizes.FriendsGap, 0, 0)
}

// row draws one person: avatar and name on the left, what can be done about them
// on the right. The name opens their profile, which is where everything this
// dialog does not offer lives.
func (d *FriendsDialog) row(entry FriendEntry, awaiting bool) fyne.CanvasObject {
	side := theme.Sizes.FriendsAvatarSize
	avatar := circularAvatar(d.deps.Images, entry.AvatarURL, fyne.NewSize(side, side))

	name := newBoldText(entry.Name, theme.Colors.TextPrimary, theme.Sizes.FriendsNameSize)
	handle := newText(entry.Handle, theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)

	// Spacers rather than a Center: an ellipsis box reports no width of its own, so
	// centring would hand it nothing and truncate the name away entirely.
	identity := container.NewVBox(
		layout.NewSpacer(),
		NewEllipsisText(name),
		NewEllipsisText(handle),
		layout.NewSpacer(),
	)

	buttons := make([]fyne.CanvasObject, 0, len(entry.Buttons))
	for i, action := range entry.Buttons {
		buttons = append(buttons, newProfileButton(action, awaiting && i == 0))
	}

	userID := entry.UserID
	tappable := NewTappableContainer(identity, func() {
		if d.onUser != nil {
			d.onUser(userID, identity)
		}
	})

	row := container.NewBorder(nil, nil,
		HBoxNoSpacing(avatar, HorizontalSpacer(theme.Sizes.FriendsGap)),
		container.NewCenter(container.NewHBox(buttons...)),
		tappable,
	)

	return NewFixedHeightContainer(theme.Sizes.FriendsRowHeight, row)
}

// cappedHeightLayout hands its children the room it is given and reports the
// content's minimum height up to a ceiling, past which the scroller takes over.
// The *content* is measured rather than the child, the child being that scroller
// and a scroller having no opinion about its height. The ceiling is a field
// rather than a theme lookup: three surfaces mount one of these.
type cappedHeightLayout struct {
	content fyne.CanvasObject
	max     float32
}

func (l *cappedHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
		child.Move(fyne.Position{})
	}
}

func (l *cappedHeightLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	wanted := l.content.MinSize()

	return fyne.NewSize(wanted.Width, min(wanted.Height, l.max))
}
