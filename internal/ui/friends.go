package ui

// The friends list: the row at the top of the home sidebar, and the dialog it
// opens. Revolt has no collection of relationships to fetch — each one is filed
// on the person it is with — so the controller resolves them and this draws what
// it is handed.
//
// The dialog refills in place rather than being rebuilt. Accepting a request is
// the one action here whose whole result is the list changing under it, and a
// dialog that closed to show that would take the rest of the answers with it.

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

// FriendsRow is the way into the friends list, drawn above the conversations in
// the home sidebar. It marks itself the way an unread channel does when requests
// are waiting, that being the only part of the list somebody is owed an answer
// on.
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
	label := canvas.NewText("Friends", theme.Colors.CategoryText)
	label.TextSize = theme.Sizes.ChannelLabelSize

	w := &FriendsRow{
		background: canvas.NewRectangle(color.Transparent),
		pendingBar: canvas.NewRectangle(color.Transparent),
		label:      label,
	}
	w.onTap = onTap
	w.ExtendBaseWidget(w)

	return w
}

// SetPending marks the row while friend requests are waiting on an answer.
// Unchanged state is a no-op, so a sidebar-wide sync costs nothing for a row that
// did not move.
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

	// The marker slot, the left padding and the glyph are the channel row's, so the
	// two line up down the column despite being different widgets. The slot is as
	// wide as a *selection* marker even though nothing here draws one: a channel row
	// stacks the two markers and takes the wider, so a slot sized to the pending bar
	// alone would stand every row above the conversations two pixels to the left.
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

// FriendEntry is one person in the list. Buttons are the same ProfileButton a
// card offers, built by the same controller policy — what applies to somebody is
// a question about the relationship, and asking it twice is how two surfaces
// come to disagree.
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

	// Awaiting marks a section somebody is owed an answer on, which is the only
	// place a row draws its first button emphasised. Everywhere else the heading
	// has already said what the row is about, and a coloured slab per row would
	// be the loudest thing in a list that is mostly read rather than acted on.
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
		empty:  canvas.NewText("Nobody yet. Add somebody from their profile.", theme.Colors.TimestampText),
		onUser: onUser,
	}
	d.empty.TextSize = theme.Sizes.FriendsHandleSize
	d.empty.Hide()

	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.JoinDialogCornerRadius

	// The scroller cannot be asked how tall it wants to be — container.Scroll
	// reports its own current height as its minimum — so the list is measured and
	// the ceiling applied here.
	viewport := container.New(
		&cappedHeightLayout{content: d.list, max: theme.Sizes.FriendsListMaxHeight},
		NewPlainVScroll(d.list))

	body := VBoxNoSpacing(
		d.header(onClose),
		NewInset(VBoxNoSpacing(d.empty, viewport), 0, pad, pad, pad),
	)

	// Fixed rather than minimum width, as a profile card is: every row shortens to
	// the width it is given, so no name can widen the dialog.
	d.Content = newTapSink(NewFixedWidthContainer(width, container.NewStack(card, body)))

	return d
}

// SetSections replaces the whole list. The dialog is on a centred layer sized
// from its own minimum, so the caller repositions the overlay afterwards — a
// section gained or lost changes the card's height. Call on the UI thread.
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

	if len(rows) == 0 {
		d.empty.Show()
	} else {
		d.empty.Hide()
	}
}

// header is the title row, laid out as the join dialog's is: the heading centred
// across the whole card with the close button over its right edge, so the button
// does not shift the title off centre.
func (d *FriendsDialog) header(onClose func()) fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Friends", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	closeButton := container.NewBorder(nil, nil, nil, container.NewCenter(NewCloseButton(onClose)))

	return container.NewStack(title, closeButton)
}

// caption names a section and counts it. The count is what says whether the
// heading is worth reading before the rows under it are.
func (d *FriendsDialog) caption(section FriendSection) fyne.CanvasObject {
	text := canvas.NewText(section.Title+" — "+strconv.Itoa(len(section.Entries)), theme.Colors.TimestampText)
	text.TextSize = theme.Sizes.FriendsSectionSize
	text.TextStyle = fyne.TextStyle{Bold: true}

	return NewInset(text, theme.Sizes.FriendsGap, theme.Sizes.FriendsGap, 0, 0)
}

// row draws one person: avatar and name on the left, what can be done about them
// on the right. The name opens their profile, which is where everything this
// dialog does not offer lives.
func (d *FriendsDialog) row(entry FriendEntry, awaiting bool) fyne.CanvasObject {
	side := theme.Sizes.FriendsAvatarSize
	avatar := circularAvatar(d.deps.Images, entry.AvatarURL, fyne.NewSize(side, side))

	name := canvas.NewText(entry.Name, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.FriendsNameSize
	name.TextStyle = fyne.TextStyle{Bold: true}

	handle := canvas.NewText(entry.Handle, theme.Colors.TimestampText)
	handle.TextSize = theme.Sizes.FriendsHandleSize

	// Spacers rather than a Center: an ellipsis box reports no width of its own —
	// it shortens to whatever it is given — so centring it would hand it nothing
	// and the name would be truncated away entirely.
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
// content's own minimum height up to a ceiling, past which the scroller wrapping
// it takes over. The content is measured rather than the child, because the child
// is that scroller and a scroller has no opinion about its height.
//
// The ceiling is a field rather than a theme lookup: the emoji picker mounts one
// of these too, and a shared layout reading one surface's size would cap the
// other at it.
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
