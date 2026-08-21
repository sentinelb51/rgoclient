package ui

// Invite cards — what an invite code unfurls into: the server it opens and the
// one action that applies to it.
//
// Built from a code rather than an invite, the code being all a message carries.
// Resolving one is a request, so a card mounts loading and fills itself in
// through SetInvite — also the seam a caller already holding a domain.Invite uses
// to skip the request.
//
// Its width is fixed rather than measured, unlike an embed's: an embed arrives
// whole, where this is mounted saying nothing, and a card that resized on arrival
// would shuffle the column under someone already reading it.

import (
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// Captions name the state the card is in. They are what tells someone whether
// the button below is going to join them to something or merely go there.
//
// Neither of the two states without an invite names what the code opens: a
// server and a group come back through the same route, and which arrived is not
// known until it has.
const (
	inviteCaptionLoading = "Invite"
	inviteCaptionFailed  = "Invite"
)

// inviteCaptionFor is the caption a resolved invite wears — what it opens, and
// whether the account is in it already.
func inviteCaptionFor(kind domain.InviteKind, joined bool) string {
	noun := "server"
	if kind == domain.InviteGroup {
		noun = "group"
	}

	if joined {
		return "You're already a member of this " + noun
	}

	return "You've been invited to join a " + noun
}

// InviteCard is a mounted invite. Content is what a caller mounts; SetInvite
// fills in what arrives after the card is up, and Fail replaces it with the
// reason it never will.
type InviteCard struct {
	Content fyne.CanvasObject

	deps Deps
	code string

	// iconURL is held rather than passed down: setBody rebuilds the whole row per
	// state, and the picture outlives the state that brought it. bannerURL is the
	// same, and is drawn only on a preview.
	iconURL   string
	bannerURL string
	caption   *canvas.Text

	// preview marks a card built from an invite already in hand, which differs in
	// both directions. It draws the banner: in a message the card mounts saying
	// nothing and is filled a moment later, so a part only some invites have would
	// shove the messages under it down as the answer landed — a preview has nothing
	// under it to move. And it draws no button: a preview stands beside the
	// caller's own.
	preview bool

	// background is held for its stroke alone — a failed card reddens its edge.
	background *canvas.Rectangle

	// body is everything below the caption, replaced whole per state rather than
	// edited: an ellipsis text fixes the string it shortens when it is built, and
	// the action button exists in one state out of three.
	body *fyne.Container
}

// NewInviteCard mounts a card for a code and asks the controller to resolve it.
// The answer arrives on the UI thread and lands in SetInvite or Fail.
func NewInviteCard(deps Deps, code string) *InviteCard {
	c := newInviteCard(deps, code)

	deps.Actions.ResolveInvite(code, func(invite domain.Invite, err error) {
		if err != nil {
			c.Fail()
			return
		}
		c.SetInvite(invite)
	})

	return c
}

// NewInviteCardFor mounts a card for an invite that is already resolved, for a
// caller holding one — the join dialog, previewing what a pasted code opens
// beside the button that would redeem it. It says what the code opens and
// nothing else: see preview above for the two ways it differs.
func NewInviteCardFor(deps Deps, invite domain.Invite) *InviteCard {
	c := newInviteCard(deps, invite.Code)
	c.preview = true
	c.SetInvite(invite)

	return c
}

func newInviteCard(deps Deps, code string) *InviteCard {
	c := &InviteCard{deps: deps, code: code, body: container.NewStack()}

	c.caption = newBoldText(inviteCaptionLoading, theme.Colors.InviteCaption, theme.Sizes.InviteCaptionSize)

	// The card surface an embed is drawn on, wearing the hairline on its own
	// background — everything inside sits within the padding.
	c.background = canvas.NewRectangle(theme.Colors.EmbedBg)
	c.background.CornerRadius = theme.Sizes.EmbedRadius
	Outline(c.background)

	// Not an ellipsis text, unlike the name below it: one fixes the string it
	// shortens at construction, and this is the one line rewritten in place. The
	// captions are ours and they fit.
	column := VBoxNoSpacing(
		c.caption,
		VerticalSpacer(theme.Sizes.EmbedRowGap),
		c.body,
	)

	padV, padH := theme.Sizes.EmbedPaddingV, theme.Sizes.EmbedPaddingH
	card := container.NewStack(c.background, NewInset(column, padV, padV, padH, padH))

	c.Content = NewFixedWidthContainer(theme.Sizes.InviteCardWidth, card)
	c.setBody(inviteState{caption: inviteCaptionLoading, title: "Resolving invite…"})

	return c
}

// SetInvite fills the card in. What the button offers is decided here rather
// than by the caller: the account is already in the server or it is not, and the
// store is the only thing that knows.
func (c *InviteCard) SetInvite(invite domain.Invite) {
	c.iconURL, c.bannerURL = invite.IconURL, invite.BannerURL

	name := inviteName(invite)
	state := inviteState{
		title:   name,
		initial: name,
		detail:  inviteDetail(invite),
	}

	label, open, joined := c.destination(invite)
	state.caption = inviteCaptionFor(invite.Kind, joined)

	switch {
	case c.preview: // the caller has the only button
	case joined:
		state.action = inviteButton(label, ButtonPlain, open)
	default:
		state.action = inviteButton("Join", ButtonPrimary, func() {
			c.deps.Actions.OnJoinInvite(c.code)
		})
	}

	c.setBody(state)
}

// destination is where the card leads when the account is in what the code opens
// already — the server, or for a group the channel that *is* the group, there
// being no server to be in. A miss is the ordinary case: an invite is
// interesting precisely when it names something never seen.
func (c *InviteCard) destination(invite domain.Invite) (label string, open func(), joined bool) {
	if invite.Kind == domain.InviteGroup {
		if _, in := c.deps.Store.Channel(invite.ChannelID); !in {
			return "", nil, false
		}

		channelID := invite.ChannelID

		return "Go to group", func() { c.deps.Actions.OnChannelTapped(channelID) }, true
	}

	if _, in := c.deps.Store.Server(invite.ServerID); !in {
		return "", nil, false
	}

	serverID := invite.ServerID

	return "Go to server", func() { c.deps.Actions.OnServerTapped(serverID) }, true
}

// inviteName is what the card calls the thing it opens. A group has no server
// name — Revolt describes one with the channel fields alone — so its name is the
// channel's, which is also why the detail line below must not repeat it.
func inviteName(invite domain.Invite) string {
	name, fallback := strings.TrimSpace(invite.ServerName), "Unnamed server"
	if invite.Kind == domain.InviteGroup {
		name, fallback = strings.TrimSpace(invite.ChannelName), "Unnamed group"
	}

	if name == "" {
		return fallback
	}

	return name
}

// Fail marks the invite as one that will never resolve. Revolt answers a code
// that has expired, been revoked or never existed the same way, so the card says
// the one thing true of all three rather than guessing which it was.
func (c *InviteCard) Fail() {
	c.setBody(inviteState{caption: inviteCaptionFailed, title: "Invite expired or invalid", failed: true})
}

// inviteState is everything one of the card's three states says — a value rather
// than four arguments because the states differ in which parts they fill, and
// because initial is not derived from title: a failed card has a sentence where
// the name would be, and no server to stand for.
type inviteState struct {
	caption string
	title   string
	detail  string
	initial string
	action  fyne.CanvasObject

	failed bool
}

// setBody swaps the card into a state. The row's height is the icon's in every
// one of them, so filling a card in never moves the message underneath it.
func (c *InviteCard) setBody(state inviteState) {
	c.caption.Text = state.caption
	c.caption.Refresh()

	// The edge is the half of the failure nobody has to read — a card carrying one
	// line where a server would be is otherwise the same shape as one still
	// resolving.
	c.background.StrokeColor = theme.Colors.Outline
	if state.failed {
		c.background.StrokeColor = theme.Colors.InviteFailedOutline
	}
	c.background.Refresh()

	side := fyne.NewSize(theme.Sizes.InviteIconSize, theme.Sizes.InviteIconSize)

	slot := c.icon(state.initial, side)
	if state.failed {
		slot = inviteFailedMark(side)
	}

	row := []fyne.CanvasObject{
		slot,
		HorizontalSpacer(theme.Sizes.EmbedAccentGap),
		inviteText(state),
	}
	if state.action != nil {
		row = append(row, HorizontalSpacer(theme.Sizes.EmbedAccentGap), container.NewCenter(state.action))
	}

	body := fyne.CanvasObject(NewFillRow(2, row...))
	if strip := c.bannerStrip(state); strip != nil {
		body = VBoxNoSpacing(strip, VerticalSpacer(theme.Sizes.EmbedRowGap), body)
	}

	c.body.Objects = []fyne.CanvasObject{body}
	c.body.Refresh()
}

// bannerStrip is the server's own banner across the card, or nil where there is
// none to draw. It sits inside the card's padding rather than against its edge:
// a canvas.Image has no corner radius, and one drawn to the edge would square off
// the two corners the card rounds.
func (c *InviteCard) bannerStrip(state inviteState) fyne.CanvasObject {
	if !c.preview || c.bannerURL == "" || state.failed {
		return nil
	}

	height := theme.Sizes.InviteBannerHeight
	width := theme.Sizes.InviteCardWidth - 2*theme.Sizes.EmbedPaddingH
	size := fyne.NewSize(width, height)

	frame := container.NewGridWrap(size, canvas.NewRectangle(theme.Colors.ServerDefaultBg))
	c.deps.Images.LoadIntoContainer(imageCacheID(c.bannerURL), c.bannerURL, size, frame, false, nil)

	return frame
}

// icon is the server's picture over the initial it falls back to. A card with no
// server yet passes no initial and keeps the empty circle — a letter taken from
// "Resolving invite" would name a server that does not exist.
func (c *InviteCard) icon(initial string, size fyne.Size) fyne.CanvasObject {
	background := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	slot := container.NewStack(background, container.NewCenter(newInitial(initial)))
	if c.iconURL != "" {
		c.deps.Images.LoadIntoContainer(imageCacheID(c.iconURL), c.iconURL, size, slot, true, background)
	}

	return container.NewGridWrap(size, slot)
}

// inviteFailedMark stands where the picture would on a card that never resolved.
// It keeps the icon's slot, so the row is the same height it was while loading
// and the message under it does not move — but it is drawn small and inside that
// slot: it stands for no server, and the empty circle it replaces reads as one
// still on its way.
func inviteFailedMark(size fyne.Size) fyne.CanvasObject {
	mark := newScaledIcon(tintedIcon(assets.ForbiddenIcon, theme.Colors.InviteFailedText), theme.Sizes.InviteFailedMark)

	return container.NewGridWrap(size, container.NewCenter(mark))
}

// inviteText is the two lines beside the icon: what the server is called, and
// what is known about it. Both shorten rather than wrap — a card is a summary,
// and the name is the only part of it somebody reads twice.
func inviteText(state inviteState) fyne.CanvasObject {
	title := newBoldText(state.title, theme.Colors.TextPrimary, theme.Sizes.InviteNameSize)

	// A failed card has a sentence where the name would be. Bold is the weight a
	// name is read at, and a sentence wearing it reads as an alarm rather than as
	// the one thing that is left to say about the code.
	if state.failed {
		title = newText(state.title, theme.Colors.InviteFailedText, theme.Sizes.InviteNameSize)
	}

	if state.detail == "" {
		return NewFillRow(0, NewEllipsisText(title))
	}

	subtitle := newText(state.detail, theme.Colors.InviteDetail, theme.Sizes.InviteDetailSize)

	return VBoxNoSpacing(
		NewFillRow(0, NewEllipsisText(title)),
		VerticalSpacer(theme.Sizes.InviteTextGap),
		NewFillRow(0, NewEllipsisText(subtitle)),
	)
}

// inviteButton is the card's one action, and the only thing on it taking a
// pointer. Joining is what the card is for, so that one is weighted; a server the
// account is already in offers the plain way into it instead.
func inviteButton(label string, weight ButtonWeight, onTap func()) fyne.CanvasObject {
	return NewWeightedButton(label, weight, onTap)
}

// inviteDetail is the line under the name: how many members, and the channel the
// code lands in when Revolt named one. Either half may be missing, so the line
// is assembled from what actually arrived rather than formatted in one go.
func inviteDetail(invite domain.Invite) string {
	// A group's name is already the title and it comes back with no count, so the
	// line under it is who is asking rather than what it is.
	if invite.Kind == domain.InviteGroup {
		if invite.InviterName == "" {
			return ""
		}

		return "Invited by " + invite.InviterName
	}

	var parts []string

	if invite.MemberCount > 0 {
		unit := " members"
		if invite.MemberCount == 1 {
			unit = " member"
		}
		parts = append(parts, groupDigits(invite.MemberCount)+unit)
	}
	if invite.ChannelName != "" {
		parts = append(parts, "#"+invite.ChannelName)
	}

	return strings.Join(parts, " · ")
}

// groupDigits inserts thousands separators, so a member count reads as a size
// rather than as an identifier.
func groupDigits(n int) string {
	digits := strconv.Itoa(n)
	if len(digits) <= 3 {
		return digits
	}

	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}

	var b strings.Builder
	b.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}

	return b.String()
}

/* In a message */

// inviteCardsPerMessage caps how many cards one message may unfurl. A body that
// lists twenty servers is an advertisement, and twenty cards would bury every
// message around it.
const inviteCardsPerMessage = 3

// inviteCodesIn is the distinct invite codes a body links to, in reading order.
// The parse is the accurate way — a URL inside a code span is not a link, and
// only the AST knows that — but it runs per mounted message, so a substring scan
// guards it: almost every message fails that in a few bytes and is never parsed.
func inviteCodesIn(content string) []string {
	if !util.MayContainInvite(content) {
		return nil
	}

	var codes []string
	for _, link := range markdown.Links(markdown.Parse(content)) {
		code := util.InviteLinkCode(link)
		if code == "" || slices.Contains(codes, code) {
			continue
		}

		codes = append(codes, code)
		if len(codes) == inviteCardsPerMessage {
			break
		}
	}

	return codes
}

// buildInvites stacks a card per distinct invite, separated by the gap that
// separates embeds. Codes rather than URLs: the same server is routinely linked
// twice in one message, bare and masked, and two identical cards would be a bug.
func buildInvites(deps Deps, codes []string) *fyne.Container {
	rows := make([]fyne.CanvasObject, 0, max(len(codes)*2-1, 0))

	for i, code := range codes {
		if i > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.EmbedSpacing))
		}
		rows = append(rows, HBoxNoSpacing(NewInviteCard(deps, code).Content))
	}

	return container.NewVBox(rows...)
}
