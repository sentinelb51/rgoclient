package ui

// The pinned-messages panel: what a channel has kept, in one place. A pin is a
// flag on the message and Revolt publishes no collection of them, so the list is
// a search the controller makes when the panel opens — nothing keeps it current
// while it is up, and a row is a summary rather than the message itself.
//
// Tapping a row leads to the message, which is the whole point of the panel: the
// pins are what somebody decided was worth coming back to, and they are almost
// always further back than the column is holding.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
)

// PinEntry is one pinned message as the panel draws it. The text is already
// resolved and already flattened: the controller has the store, and a row is a
// line of summary rather than a second rendering of a body.
type PinEntry struct {
	Author    string
	AvatarURL string
	Preview   string
	When      string

	// Jump leads to the message. Unpin takes the pin off and is nil where the
	// account may not manage the channel's messages — which is what decides
	// whether the button is drawn at all, a disabled one on every row saying only
	// that the reader is not a moderator.
	Jump  func()
	Unpin func()
}

// PinsDialog lists a channel's pinned messages on the modal layer. SetEntries
// refills it, as the friends dialog refills: unpinning is an action whose whole
// result is the list changing under it.
type PinsDialog struct {
	Content fyne.CanvasObject

	deps   Deps
	list   *fyne.Container // the rows themselves, replaced wholesale on a refill
	status *canvas.Text    // the one line that speaks when the rows cannot
}

// NewPinsDialog builds the panel for a channel, showing that it is loading.
// channel names it in the heading; onClose dismisses the layer.
func NewPinsDialog(deps Deps, channel string, onClose func()) *PinsDialog {
	pad := theme.Sizes.PinsPadding

	d := &PinsDialog{
		deps:   deps,
		list:   VBoxNoSpacing(),
		status: canvas.NewText("Loading pinned messages...", theme.Colors.TimestampText),
	}
	d.status.TextSize = theme.Sizes.PinsPreviewSize

	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.JoinDialogCornerRadius

	// The scroller cannot be asked how tall it wants to be — container.Scroll
	// reports its own current height as its minimum — so the list is measured and
	// the ceiling applied here, as the friends list does it.
	viewport := container.New(
		&cappedHeightLayout{content: d.list, max: theme.Sizes.PinsListMaxHeight},
		NewPlainVScroll(d.list))

	body := VBoxNoSpacing(
		d.header(channel, onClose),
		NewInset(VBoxNoSpacing(d.status, viewport), 0, pad, pad, pad),
	)

	// Fixed rather than minimum width: every row shortens to what it is given, so
	// no name and no preview can widen the panel.
	d.Content = newTapSink(NewFixedWidthContainer(theme.Sizes.PinsDialogWidth, container.NewStack(card, body)))

	return d
}

// SetEntries replaces the whole list. The panel is centred and sized from its own
// minimum, so the caller repositions the overlay afterwards. Call on the UI
// thread.
func (d *PinsDialog) SetEntries(entries []PinEntry) {
	rows := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, d.row(entry))
	}

	d.list.Objects = rows
	d.list.Refresh()

	d.setStatus("Nothing is pinned here yet.", len(rows) == 0)
}

// Fail replaces the list with a reason it is not there. Call on the UI thread.
func (d *PinsDialog) Fail(reason string) {
	d.list.Objects = nil
	d.list.Refresh()

	d.setStatus(reason, true)
}

// setStatus labels the line above the list and shows it only when it is wanted.
func (d *PinsDialog) setStatus(text string, show bool) {
	d.status.Text = text
	d.status.Refresh()

	if show {
		d.status.Show()
		return
	}
	d.status.Hide()
}

// header is the title row, laid out as the friends dialog's is: the heading
// centred across the whole card with the close button over its right edge, so the
// button does not shift the title off centre.
func (d *PinsDialog) header(channel string, onClose func()) fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Pinned in "+channel, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	closeButton := container.NewBorder(nil, nil, nil, container.NewCenter(NewCloseButton(onClose)))

	return container.NewStack(title, closeButton)
}

// row draws one pin: who wrote it and when above what it said, with the way to
// take the pin off at the far end. The summary is what is tappable — the button
// beside it is a sibling, so the pointer reaches whichever it is over.
func (d *PinsDialog) row(entry PinEntry) fyne.CanvasObject {
	gap := theme.Sizes.PinsGap
	side := theme.Sizes.PinsAvatarSize
	avatar := circularAvatar(d.deps.Images, entry.AvatarURL, fyne.NewSize(side, side))

	name := canvas.NewText(entry.Author, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.PinsNameSize
	name.TextStyle = fyne.TextStyle{Bold: true}

	when := canvas.NewText(entry.When, theme.Colors.TimestampText)
	when.TextSize = theme.Sizes.PinsPreviewSize

	preview := canvas.NewText(entry.Preview, theme.Colors.TimestampText)
	preview.TextSize = theme.Sizes.PinsPreviewSize

	// The name takes the leftover width and the time keeps its own, so a long name
	// is shortened rather than pushing the time out of the row. Spacers above and
	// below rather than a Center: an ellipsis box reports no width of its own, so
	// centring it would hand it nothing and shorten the name away entirely.
	identity := container.NewVBox(
		layout.NewSpacer(),
		NewFillRow(0, NewEllipsisText(name), HorizontalSpacer(gap), when),
		NewEllipsisText(preview),
		layout.NewSpacer(),
	)

	// The avatar is inside what answers the tap, so the hover fill covers the row
	// rather than lighting up beside a picture that leads to the same place.
	summary := NewTappableContainer(NewFillRow(1,
		HBoxNoSpacing(container.NewCenter(avatar), HorizontalSpacer(gap)),
		identity,
	), entry.Jump)

	return NewFixedHeightContainer(theme.Sizes.PinsRowHeight,
		NewFillRow(0, summary, d.unpinSlot(entry.Unpin, gap)))
}

// unpinSlot is the trailing end of a row: the button, or nothing at all where
// the account cannot take a pin off. The empty case is still a child, so the
// row's fill slot is at the same index either way.
func (d *PinsDialog) unpinSlot(unpin func(), gap float32) fyne.CanvasObject {
	if unpin == nil {
		return HorizontalSpacer(0)
	}

	return HBoxNoSpacing(
		HorizontalSpacer(gap),
		container.NewCenter(NewIconButton(assets.SystemUnpinnedIcon, unpin, nil)),
	)
}
