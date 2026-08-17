package ui

// User profiles: one resolved Profile behind two presentations, the compact card
// a click on an avatar opens beside it and the dialog that card expands into.
// Both are built from the same helpers, so what separates them is how much they
// say — the card names someone, the dialog tells you about them.
//
// Neither reaches for State: the controller resolves a Profile (app/profile.go),
// so a card can be built and measured from a value alone.

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
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

	// profileMutualLimit is the same for what two accounts have in common, and binds
	// the dialog too: roles are a fact that stops, where mutual friends on a busy
	// account run to hundreds and would be the whole card.
	profileMutualLimit = 6

	// profileHandleShare is how much of the identity line the handle may take
	// before it is the one being shortened. The display name is what a card is
	// for, so it keeps the rest.
	profileHandleShare = 0.5
)

/* Presence */

// presenceColor fills the presence ring. What a state is *called* belongs to
// domain.Presence; what colour it is drawn in belongs here.
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

// ProfileButton is one thing a card offers to do about somebody. A nil Do draws
// it disabled rather than leaving it out: "Request sent" is the state, and a card
// that simply omitted it would say nothing about it at all.
type ProfileButton struct {
	Label  string
	Danger bool // drawn in the destructive weight
	Do     func()

	// Overflow files the action behind the card's hamburger rather than drawing it
	// under the card: blocking somebody or copying their ID is not what a profile is
	// opened for, and a row of buttons leading with them says the wrong thing about
	// the person on screen. Icon marks it there, already in the colour it is drawn
	// in. Only the menu reads either field — a surface drawing its own rows ignores
	// both and draws the lot.
	Overflow bool
	Icon     fyne.Resource
}

// ProfileActions are the buttons a presentation offers. A nil field leaves its
// button out, which is how the dialog — already expanded — drops "Full profile".
type ProfileActions struct {
	// Buttons is what to do about this person, most useful first. Which apply is a
	// question about the relationship, answered by the controller. The compact card
	// draws only the first: it names somebody, and a row of ways to act on them is
	// what expanding it is for.
	Buttons []ProfileButton

	// OnCopied names what a click just put on the clipboard, so the controller can
	// say so. A clipboard write is invisible, and the handle is the one thing here
	// copied by clicking the thing itself — with no receipt, nothing tells it from a
	// click that missed.
	OnCopied func(what string)

	OnExpand func() // swap the card for the dialog
	OnClose  func() // dismiss the layer; drawn on the dialog's banner
}

// MutualEntry is one thing two accounts have in common: a shared server or a
// shared friend. Open is what tapping its chip does, supplied by the controller —
// where a name leads is a question about what is behind the dialog. A nil Open
// draws the plain chip, which is what a name with nowhere to go looks like.
type MutualEntry struct {
	Name string
	Open func()
}

// MutualProfile is what this account has in common with the person on screen.
// The counts are kept apart from the entries: somebody the store cannot name is
// still one of the people in common, so the "+n" counts them rather than the
// total quietly shrinking to what happens to be cached.
type MutualProfile struct {
	Servers     []MutualEntry
	ServerCount int

	Friends     []MutualEntry
	FriendCount int
}

// any reports whether there is anything to draw.
func (m MutualProfile) any() bool { return m.ServerCount > 0 || m.FriendCount > 0 }

/* Cards */

// ProfileCard is a mounted profile, compact or full. Content is what goes on the
// overlay layer; SetProfile fills in the half of a profile that arrives after
// the card is up.
type ProfileCard struct {
	Content fyne.CanvasObject

	deps   Deps
	about  *fyne.Container // the About slot, empty and hidden until SetProfile
	mutual *fyne.Container // the same for what the two accounts have in common
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
	c := &ProfileCard{
		deps:   deps,
		about:  container.NewStack(),
		mutual: container.NewStack(),
		full:   full,
	}
	c.about.Hide()
	c.mutual.Hide()

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

	// Fixed rather than minimum: every row inside shortens to what it is given, so a
	// long name or role cannot widen the card.
	c.Content = newTapSink(NewFixedWidthContainer(width, container.NewStack(background, body)))

	return c
}

// SetProfile fills in what only the profile request carries: the bio, and the
// background replacing the accent banner. The dialog grows by filling one in, so
// the caller re-places it (Overlay.Reposition). Call on the UI thread.
func (c *ProfileCard) SetProfile(profile domain.UserProfile) {
	c.setBackground(profile.BackgroundURL)
	c.setBio(profile.Bio)
}

// SetMutual fills in what the two accounts have in common — like the bio, a
// request of its own that only the dialog carries. The caller re-places the
// dialog afterwards. Call on the UI thread.
func (c *ProfileCard) SetMutual(mutual MutualProfile) {
	if !c.full || !mutual.any() {
		return
	}

	var sections []fyne.CanvasObject
	if mutual.ServerCount > 0 {
		sections = append(sections, profileSection("Mutual servers",
			mutualChips(mutual.Servers, mutual.ServerCount, c.inner)))
	}
	if mutual.FriendCount > 0 {
		sections = append(sections, profileSection("Mutual friends",
			mutualChips(mutual.Friends, mutual.FriendCount, c.inner)))
	}

	fillSlot(c.mutual, VBoxNoSpacing(sections...))
}

// fillSlot puts content in a slot the card mounted empty and hidden, for the half
// of a profile that arrives after it is up.
func fillSlot(slot *fyne.Container, content fyne.CanvasObject) {
	slot.Objects = []fyne.CanvasObject{content}
	slot.Show()
	slot.Refresh()
}

// setBio fills the About section, which only the dialog carries. An empty bio
// leaves it out rather than showing an empty well.
func (c *ProfileCard) setBio(bio string) {
	bio = strings.TrimSpace(bio)
	if !c.full || bio == "" {
		return
	}

	fillSlot(c.about, profileSection("About me", c.bio(bio)))
}

// setBackground draws the user's own banner over the accent strip, cropped to
// cover it. It replaces the strip rather than sitting on it: what shows through
// the rounded corners differs top and bottom — the card's corners above, its body
// below. A canvas.Image takes one radius for all four, so the bottom band is laid
// over itself squared off and the strip meets the body flush.
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

// subImage is a view onto part of img where the decoder hands one back — no
// pixels copied — and the whole picture where it does not.
func subImage(img image.Image, rect image.Rectangle) image.Image {
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img
	}

	return sub.SubImage(rect)
}

// bottomBand is the bottom fraction of an image.
func bottomBand(img image.Image, fraction float32) image.Image {
	bounds := img.Bounds()
	if fraction <= 0 || fraction >= 1 {
		return img
	}

	band := bounds
	band.Min.Y = bounds.Max.Y - max(int(float32(bounds.Dy())*fraction), 1)

	return subImage(img, band)
}

// coverCrop is the middle of img in the proportions of size, so a background
// fills the banner without being stretched out of shape.
func coverCrop(img image.Image, size fyne.Size) image.Image {
	bounds := img.Bounds()
	if size.Width <= 0 || size.Height <= 0 || bounds.Dx() == 0 || bounds.Dy() == 0 {
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

	return subImage(img, crop)
}

// bio renders the profile text in a well of its own, markdown and mentions and
// all. It is the one block written by the person rather than about them, and at
// any length runs into the sections under it — the hairline says where it stops.
func (c *ProfileCard) bio(bio string) fyne.CanvasObject {
	pad := theme.Sizes.ProfileBioPadding
	inner := c.inner - 2*pad

	// No menu of its own: a bio is not a message, so a right-click has nothing to
	// offer beyond the selection the flattened body already supports.
	body := renderMessageBody(c.deps, util.Truncate(bio, profileDialogBioRunes), func(*fyne.PointEvent) {})

	// Sized before it is measured: a wrapping widget answers MinSize with whatever
	// it was last laid out at, so asking first returns one unwrapped line. The flush
	// container over-sizes it by the padding it cancels.
	padding := 2 * fynetheme.InnerPadding()
	body.Resize(fyne.NewSize(inner+padding, body.MinSize().Height))

	content := newFlushContainer(body)
	if body.MinSize().Height-padding <= theme.Sizes.ProfileBioMaxHeight {
		return profileWell(content, pad)
	}

	// Past that the section scrolls rather than growing: the dialog is centred with
	// its buttons below, and a long bio would push them off the screen. The flush
	// container's negative inset survives — the scroll clips to its own viewport.
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(0, theme.Sizes.ProfileBioMaxHeight))

	return profileWell(scroll, pad)
}

// profileWell frames content in the hairline with pad inside it. Unfilled: the
// card is already a surface, and a second fill reads as a panel rather than as
// the edge of one block.
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

	// Over the banner rather than beside it, so the chrome costs no height. The menu
	// comes first, so the way out stays in the corner every other card puts it in.
	var chrome []fyne.CanvasObject
	if items := profileMenuItems(actions); len(items) > 0 {
		menu := NewGlyphButton(tintedIcon(assets.ActionMoreIcon, theme.Colors.TextPrimary), nil)
		menu.onTap = func() { ShowContextMenu(menu, items, AnchorBelow(menu)) }
		chrome = append(chrome, menu)
	}
	if actions.OnClose != nil {
		chrome = append(chrome, NewCloseButton(actions.OnClose))
	}

	banner := fyne.CanvasObject(c.banner)
	if len(chrome) > 0 {
		inset := theme.Sizes.ProfileTightGap
		banner = container.NewStack(banner,
			container.New(&overlayLayout{yOffset: inset, rightOffset: inset}, HBoxNoSpacing(chrome...)))
	}

	// Raised half its own height, ring included, by a negative inset — which also
	// shortens the row, so the name starts directly under it.
	avatar := c.avatar(profile, side)

	return VBoxNoSpacing(banner,
		NewInset(avatar, -avatar.MinSize().Height/2, 0, theme.Sizes.ProfilePadding, 0))
}

// profileBanner is the strip the avatar overhangs, in the user's most-senior role
// colour until a background lands over it. Two rectangles: the rounded one gives
// the card its top corners, the square one covers the lower half so the strip
// meets the body flush instead of pinching in.
func profileBanner(accent color.Color, height float32) *fyne.Container {
	if accent == nil {
		accent = theme.Colors.ProfileBannerBg
	}

	rounded := canvas.NewRectangle(accent)
	rounded.CornerRadius = theme.Sizes.ProfileCornerRadius
	square := canvas.NewRectangle(accent)

	return NewMinHeightContainer(height, rounded, NewInset(square, height/2, 0, 0, 0))
}

// avatar is the picture inside its presence ring, sized to the card. The block is
// the same size whatever the presence, so nothing around it moves when someone
// comes online — an offline user simply has the ring's width in card colour.
func (c *ProfileCard) avatar(profile domain.Profile, side float32) fyne.CanvasObject {
	cut, ring := theme.Sizes.ProfileAvatarRing, theme.Sizes.ProfilePresenceRing
	inner := side + 2*ring
	outer := inner + 2*cut

	// The card's own colour, so the outermost band reads as the avatar cut out of the
	// banner rather than outlined on it — and keeps the presence ring off that colour.
	backdrop := canvas.NewCircle(theme.Colors.ViewerCardBg)
	avatar := circularAvatar(c.deps.Images, profile.AvatarURL, fyne.NewSize(side, side))

	layers := []fyne.CanvasObject{backdrop}
	if band := presenceRing(profile.Presence, inner); band != nil {
		layers = append(layers, band)
	}
	layers = append(layers, container.NewCenter(avatar))

	return container.NewGridWrap(fyne.NewSize(outer, outer), container.NewStack(layers...))
}

// presenceRing is the band the avatar sits in. Offline gets nothing rather than a
// grey ring: absence is what offline looks like, and it is the one presence
// invisible has to be indistinguishable from.
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
	rows := []fyne.CanvasObject{c.identity(profile, actions, width)}

	if profile.Status != "" {
		rows = append(rows, VerticalSpacer(theme.Sizes.ProfileTightGap), profileStatus(profile.Status))
	}

	// Empty and hidden: the bio is a request of its own, landing after the dialog is
	// up (SetProfile). The compact card never carries one.
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

		// Empty and hidden like the bio. Above the dates: how you already know somebody
		// is worth more than when they turned up.
		rows = append(rows, c.mutual)

		if history := profileHistory(profile); history != nil {
			rows = append(rows, VerticalSpacer(theme.Sizes.ProfileGap), history)
		}
	}

	if buttons := c.buttons(actions); buttons != nil {
		rows = append(rows, VerticalSpacer(theme.Sizes.ProfileGap), buttons)
	}

	return VBoxNoSpacing(rows...)
}

// identity is the display name with the real handle beside it: the name is what
// someone chose to be called, the handle is who they are, and on one line the
// second qualifies the first. The name carries the role colour and gives up its
// width first, so neither can widen the card.
func (c *ProfileCard) identity(profile domain.Profile, actions ProfileActions, width float32) fyne.CanvasObject {
	size := theme.Sizes.ProfileNameSize
	if c.full {
		size = theme.Sizes.ProfileDialogNameSize
	}

	nameColor := theme.Colors.TextPrimary
	if profile.Accent != nil {
		nameColor = profile.Accent
	}

	name := NewAccentText(profile.Name, nameColor, size, fyne.TextStyle{Bold: true})

	// Each trailing item gets its own spacer: one object laid out twice keeps only
	// the last position it was moved to.
	gap := theme.Sizes.ProfileGap
	room := width

	var trailing []fyne.CanvasObject
	if profile.Handle != "" {
		handle := profileHandle(profile.Handle, width*profileHandleShare, size, actions.OnCopied)
		room -= gap + handle.MinSize().Width
		trailing = append(trailing, HorizontalSpacer(gap), handle)
	}
	if profile.Bot {
		mark := NewBotMark(theme.Sizes.ProfileBotMarkSize)
		room -= gap + mark.MinSize().Width
		trailing = append(trailing, HorizontalSpacer(gap), mark)
	}

	// The name takes what it needs and no more, so the handle follows it rather than
	// sitting at the card's far edge — and shortens before the line can grow.
	name.Fit(max(room, 0))

	return HBoxNoSpacing(append([]fyne.CanvasObject{name}, trailing...)...)
}

// profileHandle is the "@username#0001" beside the display name — set bare and
// muted rather than framed, a second outlined thing on the line competing with
// the name for the eye. Its width is capped at a share of the row: a zero-minimum
// ellipsis in a fill row is handed nothing, an unbounded one leaves the name none.
//
// nameSize is what it sits beside. The row stretches every child to its own
// height, which would centre the handle rather than seat it on the same line, so
// the tag takes its own height and is pushed down onto the name's baseline.
func profileHandle(handle string, limit, nameSize float32, onCopied func(string)) fyne.CanvasObject {
	tag := newHandleTag(handle, limit, onCopied)

	return VBoxNoSpacing(
		VerticalSpacer(max(baselineOffset(nameSize, tag.text.TextSize)-theme.Sizes.ProfileHandlePaddingV, 0)),
		tag,
	)
}

// handleTag is that handle, and a click on it copies it. A widget because only a
// widget is offered hover and taps, and the pill it fills under the pointer is
// the whole affordance — with no border at rest, nothing else says it answers one.
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

func newHandleTag(handle string, limit float32, onCopied func(string)) *handleTag {
	text := newText(handle, theme.Colors.TimestampText, theme.Sizes.ProfileHandleSize)

	padV, padH := theme.Sizes.ProfileHandlePaddingV, theme.Sizes.ProfileHandlePaddingH
	width := min(fyne.MeasureText(handle, text.TextSize, text.TextStyle).Width, max(limit-2*padH, 0))

	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = theme.Sizes.ProfileHandleRadius

	t := &handleTag{
		background: background,
		text:       text,
		content:    NewInset(NewFixedWidthContainer(width, NewEllipsisText(text)), padV, padV, padH, padH),
	}
	t.onTap = func() {
		CopyToClipboard(handle)
		if onCopied != nil {
			onCopied("Username")
		}
	}
	t.ExtendBaseWidget(t)

	return t
}

func (t *handleTag) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(t.background, t.content))
}

func (t *handleTag) MouseIn(*desktop.MouseEvent) { t.hover(true) }

func (t *handleTag) MouseOut() { t.hover(false) }

// hover lifts the text as well as filling behind it: the fill alone is faint
// against the card, and the handle is muted enough at rest that brightening it is
// what reads as the thing under the pointer.
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
	text := newText(status, theme.Colors.TextPrimary, theme.Sizes.ProfileStatusSize)
	text.TextStyle = fyne.TextStyle{Italic: true}

	return NewEllipsisText(text)
}

// profileSection titles a block of the card, in the small caps the channel
// sidebar's category headers are set in.
func profileSection(title string, content fyne.CanvasObject) fyne.CanvasObject {
	label := newBoldText(strings.ToUpper(title), theme.Colors.CategoryText, theme.Sizes.ProfileSectionSize)

	return VBoxNoSpacing(
		VerticalSpacer(theme.Sizes.ProfileGap),
		label,
		VerticalSpacer(theme.Sizes.ProfileTightGap),
		content,
	)
}

// profileDetail is one muted line of fact behind its own mark. The mark is what
// tells two dates apart — under a caption they differ only in the word opening
// each, which is not what a line of small grey text is read for.
func profileDetail(mark fyne.Resource, text string) fyne.CanvasObject {
	line := newText(text, theme.Colors.TimestampText, theme.Sizes.ProfileDetailSize)

	side := theme.Sizes.ProfileDetailIconSize
	icon := container.NewGridWrap(fyne.NewSize(side, side),
		newScaledIcon(tintedIcon(mark, theme.Colors.TimestampText), side))

	// A fill row rather than an HBox: an ellipsis box reports no width of its own, so
	// a layout handing every child its minimum draws the mark alone.
	return NewFillRow(2,
		container.NewCenter(icon),
		HorizontalSpacer(theme.Sizes.ProfileTightGap),
		NewEllipsisText(line),
	)
}

// profileHistory is the dates block: when the account was made, when they joined
// the open server. Nil when neither is known — a conversation has no server to
// have been joined, and an ID that is not a ULID carries no date. No caption:
// "Member since" named only one of the two.
func profileHistory(profile domain.Profile) fyne.CanvasObject {
	var lines []fyne.CanvasObject

	if !profile.Created.IsZero() {
		lines = append(lines, profileDetail(assets.ProfileCreatedIcon, "Created "+util.FullDate(profile.Created)))
	}
	if !profile.Joined.IsZero() {
		lines = append(lines, profileDetail(assets.ProfileJoinedIcon, "Joined "+util.FullDate(profile.Joined)))
	}

	if len(lines) == 0 {
		return nil
	}

	return VBoxNoSpacing(lines...)
}

/* Chips */

// profileRoles draws the role chips, most senior first. The card shows a few and
// counts the rest into a final chip: something that only names a person should
// not turn into a list of their roles.
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
		chips = append(chips, NewChip("+"+strconv.Itoa(overflow), theme.Colors.TimestampText))
	}

	return NewFlow(width, theme.Sizes.ChipSpacing, chips...)
}

// mutualChips draws what two accounts have in common, the rest counted into a
// final chip. total is the whole set rather than len(entries): the overflow
// covers what the store could not name as well as what did not fit. It is never
// tappable, having nothing named to lead to.
func mutualChips(entries []MutualEntry, total int, width float32) fyne.CanvasObject {
	shown := entries
	if len(shown) > profileMutualLimit {
		shown = shown[:profileMutualLimit]
	}

	chips := make([]fyne.CanvasObject, 0, len(shown)+1)
	for _, entry := range shown {
		if entry.Open == nil {
			chips = append(chips, NewChip(entry.Name, theme.Colors.TimestampText))
			continue
		}

		chips = append(chips, NewTappableChip(entry.Name, theme.Colors.TextPrimary, entry.Open))
	}
	if overflow := total - len(shown); overflow > 0 {
		chips = append(chips, NewChip("+"+strconv.Itoa(overflow), theme.Colors.MentionText))
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

// profileMenuItems is what the hamburger offers: every action filed off the
// button row, in the controller's order. A disabled one is left out — "Request
// sent" is a line of state in a list of verbs, and the button row shows it already.
func profileMenuItems(actions ProfileActions) []*fyne.MenuItem {
	var items []*fyne.MenuItem

	for _, action := range actions.Buttons {
		if !action.Overflow || action.Do == nil {
			continue
		}

		items = append(items, fyne.NewMenuItemWithIcon(action.Label, action.Icon, action.Do))
	}

	return items
}

// buttons is the block along the bottom, nil when there is nothing to offer so
// the card doesn't reserve the gap above a row it never draws.
func (c *ProfileCard) buttons(actions ProfileActions) fyne.CanvasObject {
	var offered []ProfileButton
	for _, action := range actions.Buttons {
		if action.Overflow {
			continue
		}

		offered = append(offered, action)
		if !c.full {
			break // the compact card draws one; expanding it is what the rest are for
		}
	}

	buttons := make([]fyne.CanvasObject, 0, len(offered)+1)
	for i, action := range offered {
		buttons = append(buttons, newProfileButton(action, i == 0))
	}

	if actions.OnExpand != nil {
		buttons = append(buttons, NewButton("Full profile", actions.OnExpand))
	}

	if len(buttons) == 0 {
		return nil
	}

	return profileButtonRows(buttons)
}

// newProfileButton weights one action. Only the first is filled — a card whose
// every button is coloured says nothing about which one it is for — with the
// destructive ones the exception, reading as destructive wherever they land.
func newProfileButton(action ProfileButton, first bool) *Button {
	weight := ButtonPlain

	switch {
	case action.Danger:
		weight = ButtonDanger
	case first:
		weight = ButtonPrimary
	}

	button := NewWeightedButton(action.Label, weight, action.Do)
	if action.Do == nil {
		button.Disable()
	}

	return button
}

// profileButtonRows lays the buttons two to a row, an odd last one taking the
// whole width. One shared row would shrink every button as another was offered,
// so the same action would be a different size per relationship.
func profileButtonRows(buttons []fyne.CanvasObject) fyne.CanvasObject {
	var rows []fyne.CanvasObject

	for i := 0; i < len(buttons); i += 2 {
		row := buttons[i:min(i+2, len(buttons))]
		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.ProfileTightGap))
		}
		rows = append(rows, container.NewGridWithColumns(len(row), row...))
	}

	return VBoxNoSpacing(rows...)
}
