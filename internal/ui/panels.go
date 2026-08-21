package ui

// The two panels listing a channel's messages as summaries: what has been
// pinned, and what a search found. A row is one flattened line leading to the
// message, never a second rendering of a body. They share the row, the card and
// the sizes, differing only in what fills them — a pin list is one request as the
// panel opens, a search one per query typed.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
)

/* One message as a row */

// MessageEntry is one message as a panel draws it. The text is already resolved
// and already flattened: the controller has the store, and a row is a line of
// summary rather than a second rendering of a body. Jump leads to the message.
type MessageEntry struct {
	Author    string
	AvatarURL string
	Preview   string
	When      string

	// Where names the channel the message is in, for a panel whose rows are not all
	// from one — the mention inbox. Empty in the two that are, where the heading
	// already said it and every row repeating it would be a column of one word.
	Where string

	Jump func()
}

// PinEntry is a MessageEntry the account may also unpin. Unpin is nil where it
// may not manage the channel's messages, and the button is then not drawn at all
// — a disabled one on every row says only that the reader is not a moderator.
type PinEntry struct {
	MessageEntry

	Unpin func()
}

// messageRow draws one summary: who wrote it and when above what it said, with
// trailing at the far end — a button, or a spacer where there is nothing to
// offer. The summary is what is tappable; trailing is a sibling, so the pointer
// reaches whichever it is over.
func messageRow(deps Deps, entry MessageEntry, trailing fyne.CanvasObject) fyne.CanvasObject {
	gap := theme.Sizes.PanelGap
	side := theme.Sizes.PanelAvatarSize
	avatar := circularAvatar(deps.Images, entry.AvatarURL, fyne.NewSize(side, side))

	name := newBoldText(entry.Author, theme.Colors.TextPrimary, theme.Sizes.PanelNameSize)

	when := newText(entry.When, theme.Colors.TimestampText, theme.Sizes.PanelPreviewSize)
	preview := newText(entry.Preview, theme.Colors.TimestampText, theme.Sizes.PanelPreviewSize)

	// The name takes the leftover width and the time keeps its own, so a long name
	// shortens rather than pushing the time out. Spacers rather than a Center: an
	// ellipsis box reports no width, so centring hands it nothing at all.
	//
	// Where rides between them, at the fixed end: it is as much of the row's
	// address as the name is, and a channel that had to shorten would be the half
	// that says which of a dozen alike this one is.
	heading := []fyne.CanvasObject{NewEllipsisText(name), HorizontalSpacer(gap)}
	if entry.Where != "" {
		heading = append(heading,
			newText(entry.Where, theme.Colors.MentionText, theme.Sizes.PanelPreviewSize),
			HorizontalSpacer(gap))
	}
	heading = append(heading, when)

	identity := container.NewVBox(
		layout.NewSpacer(),
		NewFillRow(0, heading...),
		NewEllipsisText(preview),
		layout.NewSpacer(),
	)

	// The avatar is inside what answers the tap, so the hover fill covers the row
	// rather than lighting up beside a picture leading to the same place.
	summary := NewTappableContainer(NewFillRow(1,
		HBoxNoSpacing(container.NewCenter(avatar), HorizontalSpacer(gap)),
		identity,
	), entry.Jump)

	return NewFixedHeightContainer(theme.Sizes.PanelRowHeight, NewFillRow(0, summary, trailing))
}

/* The card both panels are */

// messagePanel is the shared body: a heading, one line that speaks when the rows
// cannot, and the list. Both panels refill in place rather than closing —
// unpinning and searching again are actions whose whole result is the list moving.
type messagePanel struct {
	deps   Deps
	list   *fyne.Container // the rows themselves, replaced wholesale on a refill
	status *canvas.Text    // the one line that speaks when the rows cannot
}

// newMessagePanel builds the card. title heads it, onClose dismisses the layer,
// and extra is what one panel has that the other does not — the search field,
// or nothing.
func newMessagePanel(deps Deps, title, loading string, extra fyne.CanvasObject, onClose func()) (*messagePanel, fyne.CanvasObject) {
	pad := theme.Sizes.PanelPadding

	p := &messagePanel{
		deps:   deps,
		list:   VBoxNoSpacing(),
		status: newText(loading, theme.Colors.TimestampText, theme.Sizes.PanelPreviewSize),
	}

	// The scroller cannot be asked how tall it wants to be — container.Scroll reports
	// its own current height as its minimum — so the list is measured and the ceiling
	// applied here, as the friends list does it.
	viewport := container.New(
		&cappedHeightLayout{content: p.list, max: theme.Sizes.PanelListMaxHeight},
		NewPlainVScroll(p.list))

	inner := VBoxNoSpacing(p.status, viewport)
	if extra != nil {
		inner = VBoxNoSpacing(extra, VerticalSpacer(pad), p.status, viewport)
	}

	body := VBoxNoSpacing(dialogHeader(title, onClose), NewInset(inner, 0, pad, pad, pad))

	// Fixed rather than minimum width: every row shortens to what it is given, so no
	// name and no preview can widen the panel.
	content := newTapSink(NewFixedWidthContainer(theme.Sizes.PanelDialogWidth,
		container.NewStack(newDialogCard(), body)))

	return p, content
}

// setRows replaces the whole list and says empty when it is. The panel is centred
// and sized from its own minimum, so the caller repositions the overlay
// afterwards. Call on the UI thread.
func (p *messagePanel) setRows(rows []fyne.CanvasObject, empty string) {
	p.list.Objects = rows
	p.list.Refresh()

	p.setStatus(empty, len(rows) == 0)
}

// say replaces the list with one line — a reason there is nothing, or a request
// in flight. Call on the UI thread.
func (p *messagePanel) say(reason string) {
	p.list.Objects = nil
	p.list.Refresh()

	p.setStatus(reason, true)
}

// setStatus labels the line above the list and shows it only when it is wanted.
func (p *messagePanel) setStatus(text string, show bool) {
	p.status.Text = text
	p.status.Refresh()

	showIf(p.status, show)
}

/* The pinned-messages panel */

// PinsDialog lists a channel's pinned messages. A pin is a flag on the message
// and Revolt publishes no collection of them, so the list is a search made as the
// panel opens — nothing keeps it current while it is up.
type PinsDialog struct {
	Content fyne.CanvasObject

	panel *messagePanel
}

// NewPinsDialog builds the panel for a channel, showing that it is loading.
// channel names it in the heading; onClose dismisses the layer.
func NewPinsDialog(deps Deps, channel string, onClose func()) *PinsDialog {
	panel, content := newMessagePanel(deps, "Pinned in "+channel, "Loading pinned messages...", nil, onClose)

	return &PinsDialog{Content: content, panel: panel}
}

// SetEntries replaces the whole list. Call on the UI thread.
func (d *PinsDialog) SetEntries(entries []PinEntry) {
	rows := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, messageRow(d.panel.deps, entry.MessageEntry, unpinSlot(entry.Unpin)))
	}

	d.panel.setRows(rows, "Nothing is pinned here yet.")
}

// Fail replaces the list with a reason it is not there. Call on the UI thread.
func (d *PinsDialog) Fail(reason string) { d.panel.say(reason) }

// unpinSlot is the trailing end of a row: the button, or nothing where the
// account cannot take a pin off. The empty case is still a child, so the row's
// fill slot is at the same index either way.
func unpinSlot(unpin func()) fyne.CanvasObject {
	if unpin == nil {
		return HorizontalSpacer(0)
	}

	return HBoxNoSpacing(
		HorizontalSpacer(theme.Sizes.PanelGap),
		container.NewCenter(NewIconButton(assets.SystemUnpinnedIcon, unpin, nil)),
	)
}

/* Channel search */

// SearchDialog searches one channel for what is typed in it. Unlike the pins
// panel it asks for nothing until it is told to: Revolt's search is a request
// per query, so it runs on submit rather than on every keystroke.
type SearchDialog struct {
	Content fyne.CanvasObject

	// Entry is the field to focus once the panel is up — a search that has to be
	// clicked into before it can be typed into is a click nobody meant to spend.
	Entry fyne.Focusable

	panel *messagePanel
}

// NewSearchDialog builds the panel for a channel. onSearch receives the query as
// typed, the controller deciding what an empty one means; onClose dismisses the
// layer.
func NewSearchDialog(deps Deps, channel string, onSearch func(query string), onClose func()) *SearchDialog {
	d := &SearchDialog{}

	// The field handles Escape itself — see modalEntry.
	entry := newModalEntry(onClose)
	entry.SetPlaceHolder("Search messages")
	entry.OnSubmitted = onSearch
	d.Entry = entry

	d.panel, d.Content = newMessagePanel(deps, "Search in "+channel,
		"Type something and press Enter.", WithCaret(entry), onClose)

	return d
}

// SetEntries replaces the results. Call on the UI thread.
func (d *SearchDialog) SetEntries(entries []MessageEntry) {
	rows := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, messageRow(d.panel.deps, entry, HorizontalSpacer(0)))
	}

	d.panel.setRows(rows, "Nothing matched that.")
}

// Searching says a request is out, replacing whatever the last one found: a list
// left standing under a new query reads as a result for it.
func (d *SearchDialog) Searching() { d.panel.say("Searching...") }

/* The mention inbox */

// MentionsDialog lists every message naming the account, wherever it is. It is
// the third panel and the only one not about a channel: the rows come from as
// many as the account is in, so each carries where it is from and there is no
// heading that could say it for them.
type MentionsDialog struct {
	Content fyne.CanvasObject

	panel *messagePanel
}

// NewMentionsDialog builds the panel, showing that it is loading. onClose
// dismisses the layer.
func NewMentionsDialog(deps Deps, onClose func()) *MentionsDialog {
	panel, content := newMessagePanel(deps, "Mentions", "Loading mentions...", nil, onClose)

	return &MentionsDialog{Content: content, panel: panel}
}

// SetEntries replaces the whole list. Call on the UI thread.
func (d *MentionsDialog) SetEntries(entries []MessageEntry) {
	rows := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, messageRow(d.panel.deps, entry, HorizontalSpacer(0)))
	}

	d.panel.setRows(rows, "Nobody has mentioned you.")
}

// Fail replaces the list with a reason it is not there. Call on the UI thread.
func (d *MentionsDialog) Fail(reason string) { d.panel.say(reason) }

// Fail replaces the results with a reason there are none. Call on the UI thread.
func (d *SearchDialog) Fail(reason string) { d.panel.say(reason) }
