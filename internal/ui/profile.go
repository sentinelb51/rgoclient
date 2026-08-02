package ui

// User profiles. One resolved Profile behind two presentations: the compact card
// a click on an avatar opens beside it, and the full dialog that card expands
// into on the modal layer. Both are assembled from the same header, section and
// chip helpers, so the only thing separating them is how much they say — the
// card names someone, the dialog tells you about them.
//
// Neither reaches for State. The controller resolves a Profile (app/profile.go)
// and hands it over, so a card can be built and measured from a value alone.

import (
	"fmt"
	"image/color"
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

const (
	// profileBioRunes is how much of a bio each presentation shows: the card
	// previews a sentence of it, the dialog shows the text itself. The dialog
	// stops too, because nothing on the modal layer scrolls — an unbounded bio
	// would push its own buttons off the screen.
	profileBioRunes       = 220
	profileDialogBioRunes = 700

	// profileRoleLimit is how many role chips the compact card draws before
	// counting the rest into a final "+n"; the dialog lists them all.
	profileRoleLimit = 4

	// profileHandleShare is how much of the identity line the handle may take
	// before it is the one being shortened. The display name is what a card is
	// for, so it keeps the rest.
	profileHandleShare = 0.5
)

/* Presence */

// presenceColor fills the presence ring. The vocabulary itself is
// domain.Presence — what each state is *called* belongs to the domain, what
// colour it is drawn in belongs here.
func presenceColor(presence domain.Presence) color.Color {
	switch presence {
	case domain.PresenceOnline:
		return theme.Colors.PresenceOnline
	case domain.PresenceIdle:
		return theme.Colors.PresenceIdle
	case domain.PresenceFocus:
		return theme.Colors.PresenceFocus
	case domain.PresenceBusy:
		return theme.Colors.PresenceBusy
	}

	return theme.Colors.PresenceOffline
}

/* Profile data */

// ProfileActions are the buttons a presentation offers. A nil field leaves its
// button out, which is how the card drops "Message" for the account's own user
// and how the dialog — already expanded — drops "Full profile".
type ProfileActions struct {
	OnMessage func() // open a direct message
	OnExpand  func() // swap the card for the dialog
	OnClose   func() // dismiss the layer; drawn on the dialog's banner
}

/* Cards */

// ProfileCard is a mounted profile, compact or full. Content is what goes on the
// overlay layer; SetBio fills the About section in when the bio lands.
type ProfileCard struct {
	Content fyne.CanvasObject

	deps  Deps
	about *fyne.Container // the About slot, empty and hidden until SetBio
	full  bool
}

// NewProfileCard builds the compact card, to be anchored beside whatever was
// clicked.
func NewProfileCard(deps Deps, profile domain.Profile, actions ProfileActions) *ProfileCard {
	return newProfileCard(deps, profile, actions, false)
}

// NewProfileDialog builds the full profile, to be centred on the modal layer.
func NewProfileDialog(deps Deps, profile domain.Profile, actions ProfileActions) *ProfileCard {
	return newProfileCard(deps, profile, actions, true)
}

func newProfileCard(deps Deps, profile domain.Profile, actions ProfileActions, full bool) *ProfileCard {
	c := &ProfileCard{deps: deps, about: container.NewStack(), full: full}
	c.about.Hide()

	width := theme.Sizes.ProfileCardWidth
	if full {
		width = theme.Sizes.ProfileDialogWidth
	}
	pad := theme.Sizes.ProfilePadding

	background := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	background.CornerRadius = theme.Sizes.ProfileCornerRadius

	body := VBoxNoSpacing(
		c.header(profile, actions),
		NewInset(c.details(profile, actions, width-2*pad), 0, pad, pad, pad),
	)

	// Fixed rather than minimum width: every row inside shortens to the width it
	// is given, so a long name or role can never widen the card.
	c.Content = newTapSink(NewFixedWidthContainer(width, container.NewStack(background, body)))

	return c
}

// SetBio fills the About section in once the profile request lands. An empty bio
// leaves the section out altogether rather than showing an empty well. The card
// grows by doing this, so the caller re-places it (Overlay.Reposition). Call on
// the UI thread.
func (c *ProfileCard) SetBio(bio string) {
	bio = strings.TrimSpace(bio)
	if bio == "" {
		return
	}

	c.about.Objects = []fyne.CanvasObject{profileSection("About me", c.bio(bio))}
	c.about.Show()
	c.about.Refresh()
}

// bio renders the profile text. The card previews it as plain text, since chasing
// a bio's formatting is not what a card that only names someone is for; the
// dialog renders the markdown itself, mentions and all.
func (c *ProfileCard) bio(bio string) fyne.CanvasObject {
	if !c.full {
		preview := widget.NewLabel(util.Truncate(markdown.DocumentText(markdown.Parse(bio)), profileBioRunes))
		preview.Wrapping = fyne.TextWrapWord
		preview.Importance = widget.LowImportance

		return newFlushContainer(preview)
	}

	// No menu of its own: a bio is not a message, so a right-click has nothing to
	// offer beyond the selection the flattened body already supports.
	body := renderMessageBody(c.deps, util.Truncate(bio, profileDialogBioRunes), func(*fyne.PointEvent) {})

	return newFlushContainer(body)
}

/* Header */

// header is the colour banner with the avatar overhanging it.
func (c *ProfileCard) header(profile domain.Profile, actions ProfileActions) fyne.CanvasObject {
	side, height := theme.Sizes.ProfileAvatarSize, theme.Sizes.ProfileBannerHeight
	if c.full {
		side, height = theme.Sizes.ProfileDialogAvatarSize, theme.Sizes.ProfileDialogBannerHeight
	}

	banner := fyne.CanvasObject(profileBanner(profile.Accent, height))
	if actions.OnClose != nil {
		// Laid over the banner rather than beside it, so the way out costs the card
		// no height of its own.
		inset := theme.Sizes.ProfileTightGap
		banner = container.NewStack(banner,
			container.New(&overlayLayout{yOffset: inset, rightOffset: inset}, NewCloseButton(actions.OnClose)))
	}

	// Raised by half its own height — ring included, so the picture itself is
	// centred on the banner's bottom edge — by a negative inset, which shortens
	// the row it sits in by the same amount so the name starts directly under it.
	avatar := c.avatar(profile, side)

	return VBoxNoSpacing(banner,
		NewInset(avatar, -avatar.MinSize().Height/2, 0, theme.Sizes.ProfilePadding, 0))
}

// profileBanner is the strip of colour the avatar overhangs, in the user's
// most-senior role colour. Two rectangles: the rounded one gives the card its top
// corners, and the square one covers the strip's lower half so it meets the card
// body flush instead of pinching in at the bottom corners.
func profileBanner(accent color.Color, height float32) fyne.CanvasObject {
	if accent == nil {
		accent = theme.Colors.ProfileBannerBg
	}

	rounded := canvas.NewRectangle(accent)
	rounded.CornerRadius = theme.Sizes.ProfileCornerRadius
	square := canvas.NewRectangle(accent)

	return NewMinHeightContainer(height, rounded, NewInset(square, height/2, 0, 0, 0))
}

// avatar is the picture inside its presence ring, sized to the card. The whole
// block is the same size whatever the presence is — an offline user simply has
// the ring's width in card colour, so nothing around the avatar moves when
// someone goes online.
func (c *ProfileCard) avatar(profile domain.Profile, side float32) fyne.CanvasObject {
	cut, ring := theme.Sizes.ProfileAvatarRing, theme.Sizes.ProfilePresenceRing
	inner := side + 2*ring
	outer := inner + 2*cut

	// Filled in the card's own colour, so the outermost band reads as the avatar
	// being cut out of the banner rather than outlined on top of it — and as the
	// gap that keeps the presence ring off the banner's own colour.
	backdrop := canvas.NewCircle(theme.Colors.ViewerCardBg)
	avatar := circularAvatar(c.deps.Images, profile.AvatarURL, fyne.NewSize(side, side))

	layers := []fyne.CanvasObject{backdrop}
	if band := presenceRing(profile.Presence, inner); band != nil {
		layers = append(layers, band)
	}
	layers = append(layers, container.NewCenter(avatar))

	return container.NewGridWrap(fyne.NewSize(outer, outer), container.NewStack(layers...))
}

// presenceRing is the coloured band the avatar sits in, at diameter side. Offline
// gets nothing at all rather than a grey ring: absence is what "offline" looks
// like, and it is the one presence invisible has to be indistinguishable from.
func presenceRing(presence domain.Presence, side float32) fyne.CanvasObject {
	if presence == domain.PresenceOffline {
		return nil
	}

	band := canvas.NewCircle(presenceColor(presence))

	return container.NewCenter(container.NewGridWrap(fyne.NewSize(side, side), band))
}

/* Body */

// details is everything under the header. width is the room the rows have, which
// the chip flow needs given rather than measured — see NewFlow.
func (c *ProfileCard) details(profile domain.Profile, actions ProfileActions, width float32) fyne.CanvasObject {
	rows := []fyne.CanvasObject{c.identity(profile, width)}

	if profile.Status != "" {
		rows = append(rows, VerticalSpacer(theme.Sizes.ProfileTightGap), profileStatus(profile.Status))
	}

	// Empty and hidden: the bio is a separate request and lands after the card is
	// already up (SetBio).
	rows = append(rows, c.about)

	if len(profile.Roles) > 0 {
		rows = append(rows, profileSection("Roles", profileRoles(profile.Roles, c.full, width)))
	}
	if c.full {
		if len(profile.Badges) > 0 {
			rows = append(rows, profileSection("Badges", profileBadges(profile.Badges, width)))
		}
		if history := profileHistory(profile); history != nil {
			rows = append(rows, profileSection("Member since", history))
		}
	}

	if buttons := c.buttons(profile, actions); buttons != nil {
		rows = append(rows, VerticalSpacer(theme.Sizes.ProfileGap), buttons)
	}

	return VBoxNoSpacing(rows...)
}

// identity is the display name with the account's real handle beside it: the
// name is what someone chose to be called, the handle is who they are, and on one
// line the second reads as qualifying the first. The name carries the role
// colour, as it does on a message, and gives up its width first, so neither can
// widen the card.
func (c *ProfileCard) identity(profile domain.Profile, width float32) fyne.CanvasObject {
	size := theme.Sizes.ProfileNameSize
	if c.full {
		size = theme.Sizes.ProfileDialogNameSize
	}

	nameColor := theme.Colors.TextPrimary
	if profile.Accent != nil {
		nameColor = profile.Accent
	}

	name := canvas.NewText(profile.Name, nameColor)
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.TextSize = size

	// What follows the name, and the room it costs. Each gets its own spacer: one
	// object laid out twice would keep only the last position it was moved to.
	gap := theme.Sizes.ProfileGap
	room := width

	var trailing []fyne.CanvasObject
	if profile.Handle != "" {
		handle := profileHandle(profile.Handle, width*profileHandleShare)
		room -= gap + handle.MinSize().Width
		trailing = append(trailing, HorizontalSpacer(gap), handle)
	}
	if profile.Bot {
		mark := container.NewCenter(profileChip("BOT", theme.Colors.MentionText))
		room -= gap + mark.MinSize().Width
		trailing = append(trailing, HorizontalSpacer(gap), mark)
	}

	// The name takes the width it needs and no more, so the handle reads as
	// following it rather than sitting at the far edge of the card — but never
	// more than what the rest of the line leaves, so it is still the one that
	// shortens. Measured with the same call the truncation uses, so a name that
	// fits is never cut.
	fitted := min(fyne.MeasureText(name.Text, name.TextSize, name.TextStyle).Width, max(room, 0))
	line := HBoxNoSpacing(append([]fyne.CanvasObject{
		NewFixedWidthContainer(fitted, NewEllipsisText(name)),
	}, trailing...)...)
	if !c.full {
		return line
	}

	// The dialog has the room to spell the presence out; on the card the ring
	// around the avatar is the whole of it.
	presence := canvas.NewText(profile.Presence.Label(), presenceColor(profile.Presence))
	presence.TextSize = theme.Sizes.ProfileHandleSize

	return VBoxNoSpacing(line, VerticalSpacer(theme.Sizes.ProfileTightGap), NewEllipsisText(presence))
}

// profileHandle is the "@username#0001" beside the display name, kept at its own
// size rather than the name's. It is given a width of its own, capped at a share
// of the row: a zero-minimum ellipsis in a fill row would be handed no width at
// all, and an unbounded one would leave the name none.
func profileHandle(handle string, limit float32) fyne.CanvasObject {
	text := canvas.NewText(handle, theme.Colors.TimestampText)
	text.TextSize = theme.Sizes.ProfileHandleSize

	width := min(fyne.MeasureText(handle, text.TextSize, text.TextStyle).Width, limit)

	return NewFixedWidthContainer(width, NewEllipsisText(text))
}

// profileStatus is the user's own status line.
func profileStatus(status string) fyne.CanvasObject {
	text := canvas.NewText(status, theme.Colors.TextPrimary)
	text.TextSize = theme.Sizes.ProfileStatusSize
	text.TextStyle = fyne.TextStyle{Italic: true}

	return NewEllipsisText(text)
}

// profileSection titles a block of the card, in the small caps the channel
// sidebar's category headers are set in.
func profileSection(title string, content fyne.CanvasObject) fyne.CanvasObject {
	label := canvas.NewText(strings.ToUpper(title), theme.Colors.CategoryText)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.TextSize = theme.Sizes.ProfileSectionSize

	return VBoxNoSpacing(
		VerticalSpacer(theme.Sizes.ProfileGap),
		label,
		VerticalSpacer(theme.Sizes.ProfileTightGap),
		content,
	)
}

// profileDetail is one muted line of fact, as the dates are set.
func profileDetail(text string) fyne.CanvasObject {
	line := canvas.NewText(text, theme.Colors.TimestampText)
	line.TextSize = theme.Sizes.ProfileDetailSize

	return NewEllipsisText(line)
}

// profileHistory is the dates block: when they joined the open server, and when
// the account itself was made. Nil when neither is known — a conversation has no
// server to have been joined, and an ID that isn't a ULID carries no date.
func profileHistory(profile domain.Profile) fyne.CanvasObject {
	var lines []fyne.CanvasObject

	if !profile.Joined.IsZero() {
		where := "this server"
		if profile.ServerName != "" {
			where = profile.ServerName
		}
		lines = append(lines, profileDetail(fmt.Sprintf("Joined %s on %s", where, util.FullDate(profile.Joined))))
	}
	if !profile.Created.IsZero() {
		lines = append(lines, profileDetail("Account created "+util.FullDate(profile.Created)))
	}

	if len(lines) == 0 {
		return nil
	}

	return VBoxNoSpacing(lines...)
}

/* Chips */

// profileChip is one small rounded label: a role in its own colour, a badge, or
// the bot mark.
func profileChip(text string, tint color.Color) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ProfileChipBg)
	background.CornerRadius = theme.Sizes.ProfileChipRadius

	label := canvas.NewText(text, tint)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.TextSize = theme.Sizes.ProfileChipTextSize

	padV, padH := theme.Sizes.ProfileChipPaddingV, theme.Sizes.ProfileChipPaddingH

	return container.NewStack(background, NewInset(label, padV, padV, padH, padH))
}

// profileRoles draws the role chips, most senior first. The card shows the first
// few and counts the rest into a final chip: something that only names a person
// should not turn into a list of their roles.
func profileRoles(roles []domain.Role, full bool, width float32) fyne.CanvasObject {
	shown, overflow := roles, 0
	if !full && len(roles) > profileRoleLimit {
		shown, overflow = roles[:profileRoleLimit], len(roles)-profileRoleLimit
	}

	chips := make([]fyne.CanvasObject, 0, len(shown)+1)
	for _, role := range shown {
		tint := theme.Colors.TextPrimary
		if role.Color != nil {
			tint = role.Color
		}
		chips = append(chips, profileChip(role.Name, tint))
	}
	if overflow > 0 {
		chips = append(chips, profileChip(fmt.Sprintf("+%d", overflow), theme.Colors.TimestampText))
	}

	return NewFlow(width, theme.Sizes.ProfileChipSpacing, chips...)
}

// profileBadges draws the platform badges an account carries.
func profileBadges(names []string, width float32) fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, len(names))
	for i, name := range names {
		chips[i] = profileChip(name, theme.Colors.MentionText)
	}

	return NewFlow(width, theme.Sizes.ProfileChipSpacing, chips...)
}

/* Buttons */

// buttons is the row along the bottom. They share the width evenly, so the pair
// reads as one control however many there are; nil when there is nothing to
// offer, so the card doesn't reserve the gap above a row it never draws.
func (c *ProfileCard) buttons(profile domain.Profile, actions ProfileActions) fyne.CanvasObject {
	var buttons []fyne.CanvasObject

	if actions.OnMessage != nil {
		message := widget.NewButton("Message", actions.OnMessage)
		message.Importance = widget.HighImportance
		buttons = append(buttons, message)
	}

	switch {
	case actions.OnExpand != nil:
		buttons = append(buttons, widget.NewButton("Full profile", actions.OnExpand))
	case c.full:
		buttons = append(buttons, widget.NewButton("Copy user ID", func() { CopyToClipboard(profile.UserID) }))
	}

	if len(buttons) == 0 {
		return nil
	}

	return container.NewGridWithColumns(len(buttons), buttons...)
}
