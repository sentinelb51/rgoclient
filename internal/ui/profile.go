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
	"image"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	// profileDialogBioRunes is a backstop rather than the fit — past
	// ProfileBioMaxHeight the section scrolls, so what bounds the dialog's height
	// is the section, not this.
	profileDialogBioRunes = 2000

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
// overlay layer; SetProfile fills in the half of a profile that arrives after
// the card is up.
type ProfileCard struct {
	Content fyne.CanvasObject

	deps   Deps
	about  *fyne.Container // the About slot, empty and hidden until SetProfile
	banner *fyne.Container // the accent strip, until a background lands over it
	inner  float32         // the width a row inside the card is given
	strip  fyne.Size       // the banner's own size, which a background is cropped to
	full   bool
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
	c.inner = width - 2*pad

	background := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	background.CornerRadius = theme.Sizes.ProfileCornerRadius

	body := VBoxNoSpacing(
		c.header(profile, actions),
		NewInset(c.details(profile, actions, c.inner), 0, pad, pad, pad),
	)

	// Fixed rather than minimum width: every row inside shortens to the width it
	// is given, so a long name or role can never widen the card.
	c.Content = newTapSink(NewFixedWidthContainer(width, container.NewStack(background, body)))

	return c
}

// SetProfile fills in the half of a profile that only the profile request
// carries: the bio, and the background that replaces the accent banner. An empty
// bio leaves the About section out altogether rather than showing an empty well.
// The dialog grows by filling one in, so the caller re-places it
// (Overlay.Reposition). Call on the UI thread.
func (c *ProfileCard) SetProfile(profile domain.UserProfile) {
	c.setBackground(profile.BackgroundURL)
	c.setBio(profile.Bio)
}

// setBio fills the About section, which only the dialog carries — the compact
// card names someone rather than telling you about them, so it never mounts the
// slot at all.
func (c *ProfileCard) setBio(bio string) {
	bio = strings.TrimSpace(bio)
	if !c.full || bio == "" {
		return
	}

	c.about.Objects = []fyne.CanvasObject{profileSection("About me", c.bio(bio))}
	c.about.Show()
	c.about.Refresh()
}

// setBackground draws the user's own banner over the accent strip, cropped to
// cover it. It replaces the strip rather than sitting on it, because what has to
// show through the picture's rounded corners differs top and bottom: the card's
// own corners at the top, and the card body at the bottom.
//
// A canvas.Image takes one radius for all four corners, so the bottom band is
// laid over itself again squared off — the strip meets the body flush, as it does
// without a background, rather than notching the card's colour into it.
func (c *ProfileCard) setBackground(backgroundURL string) {
	if backgroundURL == "" {
		return
	}

	c.deps.Images.LoadAsync(imageCacheID(backgroundURL), backgroundURL, false, func(img image.Image) {
		radius := theme.Sizes.ProfileCornerRadius
		cropped := coverCrop(img, c.strip)

		picture := newStretchedImage(cropped)
		picture.CornerRadius = radius

		skirt := newStretchedImage(bottomBand(cropped, radius/c.strip.Height))
		backdrop := canvas.NewRectangle(theme.Colors.ViewerCardBg)

		c.banner.Objects = []fyne.CanvasObject{
			NewInset(backdrop, c.strip.Height/2, 0, 0, 0),
			picture,
			NewInset(skirt, c.strip.Height-radius, 0, 0, 0),
		}
		c.banner.Refresh()
	})
}

// newStretchedImage draws a picture at exactly the rect it is given, which is
// only not a distortion because everything here is cropped to that rect's
// proportions first.
func newStretchedImage(img image.Image) *canvas.Image {
	picture := canvas.NewImageFromImage(img)
	picture.FillMode = canvas.ImageFillStretch

	return picture
}

// bottomBand is the bottom fraction of an image.
func bottomBand(img image.Image, fraction float32) image.Image {
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})

	bounds := img.Bounds()
	if !ok || fraction <= 0 || fraction >= 1 {
		return img
	}

	band := bounds
	band.Min.Y = bounds.Max.Y - max(int(float32(bounds.Dy())*fraction), 1)

	return sub.SubImage(band)
}

// coverCrop is the middle of img in the proportions of size, so a background
// fills the banner without being stretched out of shape. It is a sub-image where
// the decoder hands one back — no pixels are copied — and the whole picture
// where it does not.
func coverCrop(img image.Image, size fyne.Size) image.Image {
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})

	bounds := img.Bounds()
	if !ok || size.Width <= 0 || size.Height <= 0 || bounds.Dx() == 0 || bounds.Dy() == 0 {
		return img
	}

	want := float64(size.Width) / float64(size.Height)
	crop := bounds

	if float64(bounds.Dx())/float64(bounds.Dy()) > want {
		width := max(int(float64(bounds.Dy())*want), 1)
		crop.Min.X += (bounds.Dx() - width) / 2
		crop.Max.X = crop.Min.X + width
	} else {
		height := max(int(float64(bounds.Dx())/want), 1)
		crop.Min.Y += (bounds.Dy() - height) / 2
		crop.Max.Y = crop.Min.Y + height
	}

	return sub.SubImage(crop)
}

// bio renders the profile text inside a well of its own, markdown and mentions
// and all. It is the one block of the dialog written by the person rather than
// about them, and at any length it runs into the sections under it — the hairline
// is what says where it stops.
func (c *ProfileCard) bio(bio string) fyne.CanvasObject {
	pad := theme.Sizes.ProfileBioPadding
	inner := c.inner - 2*pad

	// No menu of its own: a bio is not a message, so a right-click has nothing to
	// offer beyond the selection the flattened body already supports.
	body := renderMessageBody(c.deps, util.Truncate(bio, profileDialogBioRunes), func(*fyne.PointEvent) {})

	// Laid out at the width it will be given before it is measured: a wrapping
	// widget answers MinSize with whatever it was last sized to, so asking first
	// would get back the height of one unwrapped line. The flush container
	// over-sizes it by the padding it cancels, which is the width it wraps inside —
	// the well's own padding comes off it first.
	padding := 2 * fynetheme.InnerPadding()
	body.Resize(fyne.NewSize(inner+padding, body.MinSize().Height))

	content := newFlushContainer(body)
	if body.MinSize().Height-padding <= theme.Sizes.ProfileBioMaxHeight {
		return profileWell(content, pad)
	}

	// Past that, the section scrolls rather than growing: the dialog is centred on
	// the modal layer with its buttons below, so a long bio would otherwise push
	// them off the screen. The flush container's negative inset survives the move
	// inside — the scroll clips to its own viewport, so the padding it cancels is
	// cut off rather than drawn.
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(0, theme.Sizes.ProfileBioMaxHeight))

	return profileWell(scroll, pad)
}

// profileWell frames content in the client's hairline with pad inside it. The
// rectangle is unfilled: the card is already a surface, and a second fill on it
// would read as a panel rather than as the edge of one block.
func profileWell(content fyne.CanvasObject, pad float32) fyne.CanvasObject {
	frame := canvas.NewRectangle(color.Transparent)
	frame.CornerRadius = theme.Sizes.ProfileBioRadius
	Outline(frame)

	return container.NewStack(frame, NewInset(content, pad, pad, pad, pad))
}

/* Header */

// header is the colour banner with the avatar overhanging it.
func (c *ProfileCard) header(profile domain.Profile, actions ProfileActions) fyne.CanvasObject {
	side, height := theme.Sizes.ProfileAvatarSize, theme.Sizes.ProfileBannerHeight
	if c.full {
		side, height = theme.Sizes.ProfileDialogAvatarSize, theme.Sizes.ProfileDialogBannerHeight
	}

	c.banner = profileBanner(profile.Accent, height)
	c.strip = fyne.NewSize(c.inner+2*theme.Sizes.ProfilePadding, height)

	banner := fyne.CanvasObject(c.banner)
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
// most-senior role colour — what shows until a background lands over it
// (setBackground), and what a user without one keeps. Two rectangles: the
// rounded one gives the card its top corners, and the square one covers the
// strip's lower half so it meets the card body flush instead of pinching in at
// the bottom corners.
func profileBanner(accent color.Color, height float32) *fyne.Container {
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

	// Empty and hidden: the bio is a separate request and lands after the dialog is
	// already up (SetProfile). The compact card never carries one — it names
	// someone, and what they wrote about themselves is what expanding it is for.
	if c.full {
		rows = append(rows, c.about)
	}

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

	name := NewAccentText(profile.Name, nameColor, size, fyne.TextStyle{Bold: true})

	// What follows the name, and the room it costs. Each gets its own spacer: one
	// object laid out twice would keep only the last position it was moved to.
	gap := theme.Sizes.ProfileGap
	room := width

	var trailing []fyne.CanvasObject
	if profile.Handle != "" {
		handle := profileHandle(profile.Handle, width*profileHandleShare, size)
		room -= gap + handle.MinSize().Width
		trailing = append(trailing, HorizontalSpacer(gap), handle)
	}
	if profile.Bot {
		mark := container.NewCenter(NewChip("BOT", theme.Colors.MentionText))
		room -= gap + mark.MinSize().Width
		trailing = append(trailing, HorizontalSpacer(gap), mark)
	}

	// The name takes the width it needs and no more, so the handle reads as
	// following it rather than sitting at the far edge of the card — and gives up
	// its own text before the line can grow, so it is still the one that shortens.
	name.Fit(max(room, 0))

	return HBoxNoSpacing(append([]fyne.CanvasObject{name}, trailing...)...)
}

// profileHandle is the "@username#0001" beside the display name: the name is
// chosen and the handle is issued, and the second reads as qualifying the first
// by following it, so it is set bare and muted rather than framed — a second
// outlined thing on the identity line competed with the name for the eye. It is
// kept at its own text size rather than the name's, and given a width of its own
// capped at a share of the row — a zero-minimum ellipsis in a fill row would be
// handed no width at all, and an unbounded one would leave the name none.
//
// nameSize is what it sits beside. The row stretches every child to its own
// height, which would centre the handle against the name rather than seat it on
// the same line, so the tag takes exactly its own height and is pushed down onto
// the name's baseline instead — less the padding above the text inside it.
func profileHandle(handle string, limit, nameSize float32) fyne.CanvasObject {
	tag := newHandleTag(handle, limit)

	return VBoxNoSpacing(
		VerticalSpacer(max(baselineOffset(nameSize, tag.text.TextSize)-theme.Sizes.ProfileHandlePaddingV, 0)),
		tag,
	)
}

// handleTag is that handle, and a click on it copies it. A widget rather than a
// text object because only a widget is offered hover and taps, and the pill it
// fills under the pointer is the whole affordance: with no border at rest,
// nothing else says the handle answers a click.
type handleTag struct {
	tapBase

	background *canvas.Rectangle
	text       *canvas.Text
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*handleTag)(nil)
	_ desktop.Hoverable = (*handleTag)(nil)
)

func newHandleTag(handle string, limit float32) *handleTag {
	text := canvas.NewText(handle, theme.Colors.TimestampText)
	text.TextSize = theme.Sizes.ProfileHandleSize

	padV, padH := theme.Sizes.ProfileHandlePaddingV, theme.Sizes.ProfileHandlePaddingH
	width := min(fyne.MeasureText(handle, text.TextSize, text.TextStyle).Width, max(limit-2*padH, 0))

	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = theme.Sizes.ProfileHandleRadius

	t := &handleTag{
		background: background,
		text:       text,
		content:    NewInset(NewFixedWidthContainer(width, NewEllipsisText(text)), padV, padV, padH, padH),
	}
	t.onTap = func() { CopyToClipboard(handle) }
	t.ExtendBaseWidget(t)

	return t
}

func (t *handleTag) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(t.background, t.content))
}

func (t *handleTag) MouseIn(*desktop.MouseEvent) { t.hover(true) }

func (t *handleTag) MouseOut() { t.hover(false) }

// hover lifts the text as well as filling behind it: the fill alone is faint
// against the card, and the handle is muted enough at rest that brightening it
// is what reads as the thing under the pointer.
func (t *handleTag) hover(over bool) {
	t.background.FillColor = color.Transparent
	t.text.Color = theme.Colors.TimestampText

	if over {
		t.background.FillColor = theme.Colors.TappableHoverBg
		t.text.Color = theme.Colors.TextPrimary
	}

	t.background.Refresh()
	t.text.Refresh()
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
		chips = append(chips, NewRoleChip(role))
	}
	if overflow > 0 {
		chips = append(chips, NewChip(fmt.Sprintf("+%d", overflow), theme.Colors.TimestampText))
	}

	return NewFlow(width, theme.Sizes.ChipSpacing, chips...)
}

// profileBadges draws the platform badges an account carries.
func profileBadges(names []string, width float32) fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, len(names))
	for i, name := range names {
		chips[i] = NewChip(name, theme.Colors.MentionText)
	}

	return NewFlow(width, theme.Sizes.ChipSpacing, chips...)
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
