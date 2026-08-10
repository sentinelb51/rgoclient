package ui

// Invite cards — what an invite code unfurls into: the server it opens, and the
// one action that applies to it.
//
// The card is built from a code rather than from an invite, because the code is
// all a message carries. Resolving one is a request, so a card mounts in its
// loading state and fills itself in through SetInvite — which is also the seam a
// caller that already holds a domain.Invite uses to skip the request entirely.
//
// Its width is fixed rather than measured from its contents, unlike an embed's.
// An embed arrives whole and is sized to what it says; this one is mounted
// saying nothing and is filled in a moment later, and a card that resized on
// arrival would shuffle the message column under someone already reading it.

import (
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/domain"
	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// Captions name the state the card is in. They are what tells someone whether
// the button below is going to join them to something or merely go there.
const (
	inviteCaptionLoading = "Server invite"
	inviteCaptionJoin    = "You've been invited to join a server"
	inviteCaptionJoined  = "You're already a member of this server"
	inviteCaptionFailed  = "Server invite"
)

// InviteCard is a mounted invite. Content is what a caller mounts; SetInvite
// fills in what arrives after the card is up, and Fail replaces it with the
// reason it never will.
type InviteCard struct {
	Content fyne.CanvasObject

	deps Deps
	code string

	// iconURL is held rather than passed down because setBody rebuilds the whole
	// row for every state, and the picture outlives the state that brought it.
	iconURL string
	caption *canvas.Text

	// body is everything below the caption, replaced whole on each state rather
	// than edited in place: an ellipsis text fixes the string it shortens when it
	// is built, and the action button exists in one state out of three.
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
// caller holding one — a join dialog previewing what a pasted code opens.
func NewInviteCardFor(deps Deps, invite domain.Invite) *InviteCard {
	c := newInviteCard(deps, invite.Code)
	c.SetInvite(invite)

	return c
}

func newInviteCard(deps Deps, code string) *InviteCard {
	c := &InviteCard{deps: deps, code: code, body: container.NewStack()}

	c.caption = canvas.NewText(inviteCaptionLoading, theme.Colors.InviteCaption)
	c.caption.TextSize = theme.Sizes.InviteCaptionSize
	c.caption.TextStyle = fyne.TextStyle{Bold: true}

	// The same card surface an embed is drawn on, wearing the client's one
	// hairline on its own background — everything inside sits within the padding,
	// so nothing ever paints over the edge.
	background := canvas.NewRectangle(theme.Colors.EmbedBg)
	background.CornerRadius = theme.Sizes.EmbedRadius
	Outline(background)

	// The caption is not wrapped in an ellipsis text, unlike the name below it: one
	// fixes the string it shortens when it is built, and this is the one line on
	// the card that is rewritten in place. The captions are ours and they fit.
	column := VBoxNoSpacing(
		c.caption,
		VerticalSpacer(theme.Sizes.EmbedRowGap),
		c.body,
	)

	padV, padH := theme.Sizes.EmbedPaddingV, theme.Sizes.EmbedPaddingH
	card := container.NewStack(background, NewInset(column, padV, padV, padH, padH))

	c.Content = NewFixedWidthContainer(theme.Sizes.InviteCardWidth, card)
	c.setBody(inviteState{caption: inviteCaptionLoading, title: "Resolving invite…"})

	return c
}

// SetInvite fills the card in. What the button offers is decided here rather
// than by the caller: the account is already in the server or it is not, and the
// store is the only thing that knows.
func (c *InviteCard) SetInvite(invite domain.Invite) {
	c.iconURL = invite.IconURL

	name := strings.TrimSpace(invite.ServerName)
	if name == "" {
		name = "Unnamed server"
	}

	state := inviteState{
		title:   name,
		initial: name,
		detail:  inviteDetail(invite),
	}

	if _, joined := c.deps.Store.Server(invite.ServerID); joined {
		serverID := invite.ServerID
		state.caption = inviteCaptionJoined
		state.action = inviteButton("Go to server", widget.MediumImportance, func() {
			c.deps.Actions.OnServerTapped(serverID)
		})
	} else {
		state.caption = inviteCaptionJoin
		state.action = inviteButton("Join", widget.HighImportance, func() {
			c.deps.Actions.OnJoinInvite(c.code)
		})
	}

	c.setBody(state)
}

// Fail marks the invite as one that will never resolve. Revolt answers a code
// that has expired, been revoked or never existed the same way, so the card says
// the one thing true of all three rather than guessing which it was.
func (c *InviteCard) Fail() {
	c.setBody(inviteState{caption: inviteCaptionFailed, title: "Invite expired or invalid"})
}

// inviteState is everything one of the card's three states says. It is a value
// rather than four arguments because the states differ in which parts they fill
// — and because initial is not derived from title: a card that failed has a
// sentence where the name would be, and no server to stand for.
type inviteState struct {
	caption string
	title   string
	detail  string
	initial string
	action  fyne.CanvasObject
}

// setBody swaps the card into a state. The row's height is the icon's in every
// one of them, so filling a card in never moves the message underneath it.
func (c *InviteCard) setBody(state inviteState) {
	c.caption.Text = state.caption
	c.caption.Refresh()

	side := theme.Sizes.InviteIconSize
	row := []fyne.CanvasObject{
		c.icon(state.initial, fyne.NewSize(side, side)),
		HorizontalSpacer(theme.Sizes.EmbedAccentGap),
		inviteText(state.title, state.detail),
	}
	if state.action != nil {
		row = append(row, HorizontalSpacer(theme.Sizes.EmbedAccentGap), container.NewCenter(state.action))
	}

	c.body.Objects = []fyne.CanvasObject{NewFillRow(2, row...)}
	c.body.Refresh()
}

// icon is the server's picture over the initial it falls back to — the same pair
// the server rail draws, so a server reads the same in both places. A card with
// no server yet, or one whose invite never resolved, passes no initial and keeps
// the empty circle: it is a placeholder either way, and a letter taken from
// "Invite expired" would be a server that does not exist.
func (c *InviteCard) icon(initial string, size fyne.Size) fyne.CanvasObject {
	background := canvas.NewCircle(theme.Colors.ServerDefaultBg)

	letter := canvas.NewText("", theme.Colors.TextPrimary)
	if initial != "" {
		letter.Text = strings.ToUpper(string([]rune(initial)[0]))
	}
	letter.TextStyle = fyne.TextStyle{Bold: true}
	letter.Alignment = fyne.TextAlignCenter

	slot := container.NewStack(background, container.NewCenter(letter))
	if c.iconURL != "" {
		c.deps.Images.LoadIntoContainer(imageCacheID(c.iconURL), c.iconURL, size, slot, true, background)
	}

	return container.NewGridWrap(size, slot)
}

// inviteText is the two lines beside the icon: what the server is called, and
// what is known about it. Both shorten rather than wrap — a card is a summary,
// and the name is the only part of it somebody reads twice.
func inviteText(name, detail string) fyne.CanvasObject {
	title := canvas.NewText(name, theme.Colors.TextPrimary)
	title.TextSize = theme.Sizes.InviteNameSize
	title.TextStyle = fyne.TextStyle{Bold: true}

	if detail == "" {
		return NewFillRow(0, NewEllipsisText(title))
	}

	subtitle := canvas.NewText(detail, theme.Colors.InviteDetail)
	subtitle.TextSize = theme.Sizes.InviteDetailSize

	return VBoxNoSpacing(
		NewFillRow(0, NewEllipsisText(title)),
		VerticalSpacer(theme.Sizes.InviteTextGap),
		NewFillRow(0, NewEllipsisText(subtitle)),
	)
}

// inviteButton is the card's one action. A Fyne button survives here where the
// settings page's would not: nothing about it is derived from SizeNameInputBorder
// or ColorNameInputBackground, which are the two AppTheme zeroes that made a
// slider unusable. It is the only thing on the card that takes a pointer, so it
// is also the only place the message row's hover is given up.
func inviteButton(label string, importance widget.Importance, onTap func()) fyne.CanvasObject {
	action := widget.NewButton(label, onTap)
	action.Importance = importance

	return action
}

// inviteDetail is the line under the name: how many members, and the channel the
// code lands in when Revolt named one. Either half may be missing, so the line
// is assembled from what actually arrived rather than formatted in one go.
func inviteDetail(invite domain.Invite) string {
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

// inviteCodesIn reports the distinct invite codes a message body links to, in
// reading order.
//
// The parse is the accurate way to do this — a URL inside a code span is not a
// link, and only the AST knows that — but it runs per mounted message, so it is
// guarded by a substring scan first. Almost every message fails that in a few
// bytes and is never parsed at all.
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

// buildInvites stacks a card for each distinct invite a message links to,
// separated by the gap that separates its embeds. It is given codes rather than
// URLs: the same server is routinely linked twice in one message, once as a bare
// link and once masked, and two identical cards under it would be a bug.
func buildInvites(deps Deps, codes []string) *fyne.Container {
	box := container.NewVBox()

	for i, code := range codes {
		if i > 0 {
			box.Add(VerticalSpacer(theme.Sizes.EmbedSpacing))
		}
		box.Add(HBoxNoSpacing(NewInviteCard(deps, code).Content))
	}

	return box
}
