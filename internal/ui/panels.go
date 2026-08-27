package ui

// The island the three message surfaces are drawn on, the card one message is
// drawn as, and two of the three surfaces themselves: what has been pinned in a
// channel, and what has named this account anywhere. The third is channel
// search, which adds a field and a run of chips to the same island — see
// search.go.
//
// The island is three surfaces deep on purpose: the card it floats on, the well
// the cards are sunk into, and the cards that lift back out of the well. Two
// would leave a message reading as a row somebody drew a box around, which is
// what these two panels were before.
//
// A card is a summary and never a second rendering of a body: the controller has
// the store, so the text arrives flattened and the counts arrive counted.

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* One message as a card draws it */

// MessageCard is one message as any of the three surfaces draws it. Everything
// is already resolved, and the badges say what the message carries rather than
// carrying it.
//
// The two properties that are about the surface rather than the message —
// Pinned and Mentioned — are the surface's to leave off: every card in the pins
// panel is pinned and every card in the inbox names this account, and a mark on
// all of them says nothing.
type MessageCard struct {
	Author      string
	AuthorColor color.Color // nil where no coloured role applies
	AvatarURL   string

	// Mark is the glyph after the name, resolved by the store like the rest of the
	// author: a card carries it for the same reason a row does — the name alone
	// does not say a webhook posted it.
	Mark domain.AuthorMark

	Preview string
	When    string

	// Where names the channel the message is in, for a surface whose cards are not
	// all from one — the mention inbox. Empty in the two that are, where the
	// heading already said it and every card repeating it would be a column of one
	// word.
	Where string

	Attachments int
	Images      int
	Reactions   int

	Links     bool
	Pinned    bool
	Edited    bool
	Mentioned bool

	Jump func()

	// Unpin takes the pin off, for the panel that lists them. Nil where the account
	// may not manage the channel's messages, and the button is then not drawn at
	// all — a disabled one on every card says only that the reader is not a
	// moderator.
	Unpin func()

	// Dismiss takes the card off the surface listing it, for the inbox: a mention
	// is something the reader deals with rather than something they own, so the
	// card carries the way to be done with it. It stands where the jump mark
	// otherwise is — the whole card is already the way to the message, and a mark
	// beside it says the same thing twice.
	Dismiss func()
}

/* The island the three surfaces share */

// islandParts is what a surface puts into the island beyond what every island
// has. Controls are the blocks between the header and the count line — the
// search field and its chips, nothing in the two panels — and Trailing is what
// rides at the far end of that line.
type islandParts struct {
	Mark  fyne.Resource
	Title string
	Where string

	Controls []fyne.CanvasObject
	Trailing fyne.CanvasObject

	// OnMore asks for the page after what is drawn. Nil where the surface cannot
	// page at all, which is what leaves the slot out rather than hiding it.
	OnMore func()

	OnClose func()
}

// messageIsland is the shell: a header, a line saying what the cards amount to,
// and the well they sit in. It fills in place rather than closing — unpinning
// and searching again are actions whose whole result is the well moving.
type messageIsland struct {
	deps Deps

	count    *canvas.Text
	countRow fyne.CanvasObject
	status   *canvas.Text
	empty    fyne.CanvasObject // the line and its mark, standing where the cards are not
	list     *fyne.Container

	// more is the way to the next page and moreSlot is that button with the gap
	// above it, hidden together: a well with nothing further to ask for must not pay
	// for the gap either. Both nil where the surface does not page.
	more     *Button
	moreSlot fyne.CanvasObject

	// countAlone is a surface with nothing to put opposite the count, whose line is
	// hidden until there is a number to put in it.
	countAlone bool
}

// newMessageIsland builds one and returns what mounts it.
func newMessageIsland(deps Deps, parts islandParts) (*messageIsland, fyne.CanvasObject) {
	gap := theme.Sizes.IslandGap
	pad := theme.Sizes.IslandPadding

	d := &messageIsland{deps: deps, list: VBoxNoSpacing()}
	if parts.OnMore != nil {
		d.buildMore(parts.OnMore)
	}

	blocks := []fyne.CanvasObject{d.buildHeader(parts), VerticalSpacer(gap)}
	for _, control := range parts.Controls {
		blocks = append(blocks, control, VerticalSpacer(gap))
	}
	blocks = append(blocks,
		d.buildCountRow(parts.Trailing),
		VerticalSpacer(gap*countRowStep),
		d.buildWell(parts.Mark),
	)

	island := canvas.NewRectangle(theme.Colors.IslandBg)
	island.CornerRadius = theme.Sizes.IslandRadius
	Outline(island)

	// Fixed rather than minimum width: every card shortens to what it is given, so
	// no name and no preview can widen the island.
	content := newTapSink(NewFixedWidthContainer(theme.Sizes.IslandWidth,
		container.NewStack(island, NewInset(VBoxNoSpacing(blocks...), pad, pad, pad, pad))))

	return d, content
}

// countRowStep is how much of the island's gap stands under the count line. The
// line labels the well rather than dividing two blocks, so it sits closer to
// what it counts than to what is above it.
const countRowStep = 0.6

// islandInnerWidth is the room inside the island's padding — what a wrapping run
// of chips wraps against. A number rather than the width it is laid out at,
// because NewFlow is asked for its height before it has been given one.
func islandInnerWidth() float32 {
	return theme.Sizes.IslandWidth - 2*theme.Sizes.IslandPadding
}

// buildHeader is the island's one line of identity: the mark, what this is, and
// where it is looking. The address takes the leftover width and shortens, so the
// close button keeps its corner.
func (d *messageIsland) buildHeader(parts islandParts) fyne.CanvasObject {
	gap := theme.Sizes.IslandChipGap

	mark := newScaledIcon(tintedIcon(parts.Mark, theme.Colors.TextPrimary), theme.Sizes.SearchFieldGlyph)
	title := newBoldText(parts.Title, theme.Colors.TextPrimary, theme.Sizes.IslandNameSize+1)
	where := newText(parts.Where, theme.Colors.IslandCountText, theme.Sizes.IslandTimeSize)

	return NewFillRow(4,
		container.NewCenter(mark),
		HorizontalSpacer(gap),
		container.NewCenter(title),
		HorizontalSpacer(gap),
		NewEllipsisText(where),
		container.NewCenter(NewCloseButton(parts.OnClose)),
	)
}

// buildCountRow is what the answer amounts to, and whatever the surface puts
// opposite it. One line, because the two are read together: "12 of 87" only
// means something beside the chips that took it there.
func (d *messageIsland) buildCountRow(trailing fyne.CanvasObject) fyne.CanvasObject {
	d.count = newText("", theme.Colors.IslandCountText, theme.Sizes.IslandCountTextSize)

	row := []fyne.CanvasObject{
		container.NewCenter(d.count),
		HorizontalSpacer(theme.Sizes.IslandChipGap),
	}
	if trailing != nil {
		row = append(row, trailing)
	}

	// A row holding nothing but an empty count is a band of nothing over the well,
	// so a surface with nothing to put opposite the count only shows the line once
	// it has a number to put in it.
	d.countRow = NewFillRow(1, row...)
	d.countAlone = trailing == nil
	showIf(d.countRow, !d.countAlone)

	return d.countRow
}

// buildWell is the sunk surface the cards sit in. The scroller cannot be asked
// how tall it wants to be — container.Scroll reports its own current height as
// its minimum — so the list is measured and the ceiling applied here, through
// cappedHeightLayout.
func (d *messageIsland) buildWell(res fyne.Resource) fyne.CanvasObject {
	pad := theme.Sizes.IslandWellPadding

	well := canvas.NewRectangle(theme.Colors.IslandWellBg)
	well.CornerRadius = theme.Sizes.IslandWellRadius
	Outline(well)

	mark := newScaledIcon(tintedIcon(res, theme.Colors.IslandHintText), theme.Sizes.SearchFieldGlyph)
	mark.Translucency = iconRestTranslucency
	d.status = newText("", theme.Colors.IslandHintText, theme.Sizes.IslandPreviewSize)

	// A floor under the empty state, so an island holding one sentence — loading,
	// nothing found, or a request that failed — is still an island rather than a
	// strip with a line in it.
	d.empty = NewMinHeightContainer(theme.Sizes.IslandListMaxHeight*emptyWellShare,
		container.NewCenter(VBoxNoSpacing(
			container.NewCenter(mark),
			VerticalSpacer(theme.Sizes.IslandCardSpacing),
			container.NewCenter(d.status),
		)))

	viewport := container.New(
		&cappedHeightLayout{content: d.list, max: theme.Sizes.IslandListMaxHeight},
		NewPlainVScroll(d.list))

	return container.NewStack(well,
		NewInset(VBoxNoSpacing(d.empty, viewport), pad, pad, pad, pad))
}

// emptyWellShare is how much of the list's ceiling the empty state holds open.
// Not a setting: it is a proportion of a size that already is one.
const emptyWellShare = 0.25

// buildMore is the way to the next page: a full-width button at the end of the
// well, which is where a reader who has run out of cards already is. Inside the
// scroll rather than under it — a footer outside would stand there whether or not
// there is anything more, and the well is capped in height.
func (d *messageIsland) buildMore(onMore func()) {
	d.more = NewButton("", onMore)

	slot := VBoxNoSpacing(VerticalSpacer(theme.Sizes.IslandCardSpacing), d.more)
	slot.Hide()

	d.moreSlot = slot
}

/* Filling it */

// setCards replaces the well's contents, one gap between each. Call on the UI
// thread.
func (d *messageIsland) setCards(cards []fyne.CanvasObject) {
	spaced := make([]fyne.CanvasObject, 0, 2*len(cards))
	for _, card := range cards {
		if len(spaced) > 0 {
			spaced = append(spaced, VerticalSpacer(theme.Sizes.IslandCardSpacing))
		}

		spaced = append(spaced, card)
	}

	d.setBlocks(spaced)
}

// setBlocks replaces the well's contents with a column already spaced, for a
// surface whose runs are not all one gap apart. The way to the next page rides
// under whatever is put here, so no filler has to remember to keep it. Call on
// the UI thread.
func (d *messageIsland) setBlocks(blocks []fyne.CanvasObject) {
	if d.moreSlot != nil {
		blocks = append(blocks, d.moreSlot)
	}

	d.list.Objects = blocks
	d.list.Refresh()
}

// SetMore says what the way to the next page reads, or takes it away: an empty
// label is nothing further to ask for. busy draws it as the request it already
// is, a second tap on a page in flight being a second request for the same page.
// Call on the UI thread.
func (d *messageIsland) SetMore(label string, busy bool) {
	if d.moreSlot == nil {
		return
	}
	if label == "" {
		showIf(d.moreSlot, false)

		return
	}

	d.more.SetText(label)
	if busy {
		d.more.Disable()
	} else {
		d.more.Enable()
	}

	showIf(d.moreSlot, true)
}

// reset empties the well and leaves one line standing in it. Call on the UI
// thread.
func (d *messageIsland) reset(reason string) {
	d.setCards(nil)
	d.setCount("")
	d.say(reason)
	d.SetMore("", false)
}

// say fills the well's own line, or hides it where the cards speak for
// themselves.
func (d *messageIsland) say(reason string) {
	d.status.Text = reason
	d.status.Refresh()

	showIf(d.empty, reason != "")
}

// setCount labels the well.
func (d *messageIsland) setCount(text string) {
	d.count.Text = text
	d.count.Refresh()

	if d.countAlone {
		showIf(d.countRow, text != "")
	}
}

/* One card */

// newMessageCard draws one message: who wrote it and when, what it said, and
// what it carries. The badge strip is left off entirely where there is nothing
// to say, so a plain message is a shorter card rather than one with an empty
// band.
func newMessageCard(deps Deps, entry MessageCard) fyne.CanvasObject {
	gap := theme.Sizes.IslandCardGap
	pad := theme.Sizes.IslandCardPadding
	side := theme.Sizes.IslandAvatarSize

	card := &messageCard{
		background: canvas.NewRectangle(theme.Colors.IslandCardBg),
		mentioned:  entry.Mentioned,
	}
	card.onTap = entry.Jump
	card.background.CornerRadius = theme.Sizes.IslandCardRadius

	name := newBoldText(entry.Author, authorFill(entry.AuthorColor), theme.Sizes.IslandNameSize)
	when := newText(entry.When, theme.Colors.TimestampText, theme.Sizes.IslandTimeSize)

	// The name takes the leftover width and the rest of the heading keeps its own,
	// so a long name shortens rather than pushing the date out of the card. Where
	// rides at the fixed end: it is as much of the card's address as the name is,
	// and a channel that had to shorten would be the half saying which of a dozen
	// alike this one is.
	heading := []fyne.CanvasObject{NewEllipsisText(name)}
	if mark := NewAuthorMark(entry.Mark, theme.Sizes.IslandAuthorMarkSize); mark != nil {
		heading = append(heading, HorizontalSpacer(gap*halfStep), mark)
	}
	heading = append(heading, HorizontalSpacer(gap))
	if entry.Where != "" {
		heading = append(heading,
			container.NewCenter(newText(entry.Where, theme.Colors.MentionText, theme.Sizes.IslandTimeSize)),
			HorizontalSpacer(gap))
	}
	heading = append(heading, container.NewCenter(when))

	preview := newText(entry.Preview, theme.Colors.TimestampText, theme.Sizes.IslandPreviewSize)

	column := []fyne.CanvasObject{
		NewFillRow(0, heading...),
		VerticalSpacer(theme.Sizes.IslandCardSpacing * halfStep),
		NewEllipsisText(preview),
	}
	if badges := messageBadges(entry); badges != nil {
		column = append(column, VerticalSpacer(theme.Sizes.IslandCardSpacing), badges)
	}

	row := NewFillRow(2,
		container.NewCenter(circularAvatar(deps.Images, entry.AvatarURL, fyne.NewSize(side, side))),
		HorizontalSpacer(gap),
		VBoxNoSpacing(column...),
		HorizontalSpacer(gap),
		card.trailing(entry),
	)

	card.content = container.NewStack(card.background, NewInset(row, pad, pad, pad, pad))
	card.ExtendBaseWidget(card)
	card.paint()

	return card
}

// halfStep is the tighter of the two gaps inside a card: the heading and the
// line under it are one thing, where the badges below are another.
const halfStep = 0.5

// messageCard is the tappable surface one message is drawn on. It fills under
// the pointer and its jump mark comes up with it, so the whole card reads as the
// one thing it is — the way to the message.
type messageCard struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject

	// jump is the mark at the far end, nil on a card that puts an action there
	// instead. The card is the way to the message either way, so nothing else
	// depends on it being drawn.
	jump *canvas.Image

	mentioned bool
}

var (
	_ fyne.Tappable     = (*messageCard)(nil)
	_ desktop.Hoverable = (*messageCard)(nil)
)

// trailing is the far end of the card: what it can be acted on with, and the
// jump mark where nothing has taken its place.
//
// Dismissing is the exception that takes it: the mark and the button would be
// two ends of the same card offering the two things the reader can do with a
// mention, and only one of them needs a target — the card itself is the other.
func (c *messageCard) trailing(entry MessageCard) fyne.CanvasObject {
	switch {
	case entry.Dismiss != nil:
		return container.NewCenter(c.action(assets.ActionSaveIcon, theme.Colors.SwiftActionConfirm, entry.Dismiss))

	case entry.Unpin != nil:
		return HBoxNoSpacing(
			container.NewCenter(c.action(assets.SystemUnpinnedIcon, theme.Colors.SwiftActionDanger, entry.Unpin)),
			HorizontalSpacer(theme.Sizes.IslandCardGap),
			container.NewCenter(c.jumpMark()),
		)

	default:
		return container.NewCenter(c.jumpMark())
	}
}

// action is one button at the far end of a card. An outlined button rather than
// a bare mark, as the invite list's revoke is: the mark is the only thing
// offering the action, and an icon with nothing round it reads as decoration. It
// hands its hover back to the card, which is what keeps the card lit while the
// pointer is on it.
func (c *messageCard) action(res fyne.Resource, tint color.Color, do func()) fyne.CanvasObject {
	return NewOutlinedIconButton(tintedIcon(res, tint), tint, do).reporting(c.setHovered)
}

// jumpMark is the arrow the card lifts under the pointer, built here rather than
// with the card so that one putting an action in its place never makes it.
func (c *messageCard) jumpMark() fyne.CanvasObject {
	c.jump = newScaledIcon(tintedIcon(assets.SearchJumpIcon, theme.Colors.IslandHintText),
		theme.Sizes.IslandJumpGlyph)

	return c.jump
}

func (c *messageCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *messageCard) MouseIn(*desktop.MouseEvent) { c.setHovered(true) }
func (c *messageCard) MouseOut()                   { c.setHovered(false) }

func (c *messageCard) setHovered(on bool) {
	c.background.FillColor = theme.Colors.IslandCardBg
	if on {
		c.background.FillColor = theme.Colors.IslandCardHoverBg
	}
	c.background.Refresh()

	if c.jump == nil {
		return
	}

	c.jump.Translucency = iconRestTranslucency
	if on {
		c.jump.Translucency = 0
	}
	c.jump.Refresh()
}

// paint sets what the pointer does not change. A card whose message names this
// account wears the mention amber as its edge rather than a badge: it is the one
// property of a message that is about the reader, and an edge says so without
// taking a slot from what the message actually carries.
func (c *messageCard) paint() {
	Outline(c.background)
	if c.mentioned {
		c.background.StrokeColor = solidColor(theme.Colors.IslandCardMentioned)
	}

	c.setHovered(false)
}

// messageBadges is the strip under a card saying what the message carries.
// Counts are drawn only past one: "1" beside a paperclip is the paperclip twice.
func messageBadges(entry MessageCard) fyne.CanvasObject {
	var badges []fyne.CanvasObject

	add := func(res fyne.Resource, label string) {
		if len(badges) > 0 {
			badges = append(badges, HorizontalSpacer(theme.Sizes.IslandCardGap))
		}

		badges = append(badges, messageBadge(res, label))
	}

	if files := entry.Attachments - entry.Images; files > 0 {
		add(assets.SearchAttachmentIcon, badgeCount(files))
	}
	if entry.Images > 0 {
		add(assets.SearchImageIcon, badgeCount(entry.Images))
	}
	if entry.Links {
		add(assets.SearchLinkIcon, "")
	}
	if entry.Reactions > 0 {
		add(assets.SearchReactionIcon, badgeCount(entry.Reactions))
	}
	if entry.Pinned {
		add(assets.SystemPinnedIcon, "Pinned")
	}
	if entry.Edited {
		add(assets.ActionEditIcon, "Edited")
	}

	if badges == nil {
		return nil
	}

	return HBoxNoSpacing(badges...)
}

func badgeCount(count int) string {
	if count < 2 {
		return ""
	}

	return strconv.Itoa(count)
}

// messageBadge is one mark and what it counts, dimmed: the badges describe the
// message, and reading as brightly as its own line would put them in competition
// with it.
func messageBadge(res fyne.Resource, label string) fyne.CanvasObject {
	mark := newScaledIcon(tintedIcon(res, theme.Colors.IslandBadgeText), theme.Sizes.IslandBadgeGlyph)
	mark.Translucency = iconRestTranslucency

	if label == "" {
		return container.NewCenter(mark)
	}

	return HBoxNoSpacing(
		container.NewCenter(mark),
		HorizontalSpacer(theme.Sizes.IslandBadgeGap),
		container.NewCenter(newText(label, theme.Colors.IslandBadgeText, theme.Sizes.IslandBadgeTextSize)),
	)
}

// authorFill is the colour a name is drawn in, falling back to the card's own
// text where no coloured role applies.
func authorFill(fill color.Color) color.Color {
	if fill == nil {
		return theme.Colors.TextPrimary
	}

	return fill
}

/* The pinned-messages panel */

// PinsDialog lists a channel's pinned messages. A pin is a flag on the message
// and Revolt publishes no collection of them, so the list is a search made as the
// panel opens — nothing keeps it current while it is up.
type PinsDialog struct {
	Content fyne.CanvasObject

	island *messageIsland
}

// NewPinsDialog builds the panel for a channel, showing that it is loading.
// channel names it in the heading; onMore asks for the page after what is drawn;
// onClose dismisses the layer.
func NewPinsDialog(deps Deps, channel string, onMore, onClose func()) *PinsDialog {
	island, content := newMessageIsland(deps, islandParts{
		Mark:    assets.SystemPinnedIcon,
		Title:   "Pinned",
		Where:   "in " + channel,
		OnMore:  onMore,
		OnClose: onClose,
	})
	island.reset("Loading what is pinned here...")

	return &PinsDialog{Content: content, island: island}
}

// SetEntries replaces the whole list. Call on the UI thread.
func (d *PinsDialog) SetEntries(entries []MessageCard) {
	d.island.fill(entries, "pinned message", "Nothing is pinned here yet.")
}

// SetMore is the island's own, forwarded: a panel holds the shell rather than
// being one. Call on the UI thread.
func (d *PinsDialog) SetMore(label string, busy bool) { d.island.SetMore(label, busy) }

// Fail replaces the list with a reason it is not there. Call on the UI thread.
func (d *PinsDialog) Fail(reason string) { d.island.reset(reason) }

/* The mention inbox */

// MentionGroup is the mentions from one place, drawn under a line naming it.
// Where is what that line ends with — a server, or the reader's own
// conversations — and the count in front of it is how many cards follow.
type MentionGroup struct {
	Where   string
	Entries []MessageCard
}

// MentionsDialog lists every message naming the account, wherever it is. It is
// the one surface that is not about a channel: the cards come from as many as
// the account is in, so they arrive gathered by where they are from and each
// says only which channel of that place it was in.
type MentionsDialog struct {
	Content fyne.CanvasObject

	island *messageIsland
}

// NewMentionsDialog builds the panel, showing that it is loading. onMore asks
// for the page after what is drawn; onClose dismisses the layer.
func NewMentionsDialog(deps Deps, onMore, onClose func()) *MentionsDialog {
	island, content := newMessageIsland(deps, islandParts{
		Mark:    assets.MentionIcon,
		Title:   "Mentions",
		Where:   "everywhere you are",
		OnMore:  onMore,
		OnClose: onClose,
	})
	island.reset("Looking for what named you...")

	return &MentionsDialog{Content: content, island: island}
}

// SetGroups replaces the whole list. Call on the UI thread.
func (d *MentionsDialog) SetGroups(groups []MentionGroup) {
	total := 0
	blocks := make([]fyne.CanvasObject, 0, 2*len(groups))
	for _, group := range groups {
		if len(group.Entries) == 0 {
			continue
		}
		if len(blocks) > 0 {
			blocks = append(blocks, VerticalSpacer(theme.Sizes.IslandGap))
		}

		total += len(group.Entries)
		blocks = append(blocks, newMentionGroup(d.island.deps, group))
	}

	if total == 0 {
		d.island.reset("Nobody has mentioned you.")

		return
	}

	d.island.setBlocks(blocks)
	d.island.setCount(util.Quantity(total, "mention"))
	d.island.say("")
}

// newMentionGroup is one place's mentions: the line saying which, and the cards
// under it. The line is drawn as the island's own count is — this is the same
// answer given per place, and a heading in the card's own weight would compete
// with the names in them.
func newMentionGroup(deps Deps, group MentionGroup) fyne.CanvasObject {
	heading := newText(util.Quantity(len(group.Entries), "mention")+" in "+group.Where,
		theme.Colors.IslandCountText, theme.Sizes.IslandCountTextSize)

	column := []fyne.CanvasObject{
		NewEllipsisText(heading),
		VerticalSpacer(theme.Sizes.IslandCardSpacing),
	}
	for i, entry := range group.Entries {
		if i > 0 {
			column = append(column, VerticalSpacer(theme.Sizes.IslandCardSpacing))
		}

		column = append(column, newMessageCard(deps, entry))
	}

	return VBoxNoSpacing(column...)
}

// SetMore is the island's own, forwarded, as the pins panel's is. Call on the UI
// thread.
func (d *MentionsDialog) SetMore(label string, busy bool) { d.island.SetMore(label, busy) }

// Fail replaces the list with a reason it is not there. Call on the UI thread.
func (d *MentionsDialog) Fail(reason string) { d.island.reset(reason) }

// fill is what a flat surface does with an answer: the cards, the count, and the
// line that speaks where there are none. Channel search does not use it — its
// count says what the filters took away as well as what came back — and neither
// does the inbox, whose cards come in runs under a line apiece.
func (d *messageIsland) fill(entries []MessageCard, noun, empty string) {
	cards := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		cards = append(cards, newMessageCard(d.deps, entry))
	}

	d.setCards(cards)

	if len(entries) == 0 {
		d.setCount("")
		d.say(empty)

		return
	}

	d.setCount(util.Quantity(len(entries), noun))
	d.say("")
}
