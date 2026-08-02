package ui

import (
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// crowdedProfile is a profile with everything filled in and nothing short: it is
// what a card has to survive without growing.
func crowdedProfile() Profile {
	return Profile{
		UserID:     "01AVATAR",
		Name:       strings.Repeat("Extremely Long Display Name ", 4),
		Handle:     "@" + strings.Repeat("username", 8) + "#9147",
		Status:     strings.Repeat("a status nobody could read in one line ", 3),
		Presence:   PresenceOnline,
		ServerName: "A Server",
		Badges:     []string{"Developer", "Early Adopter", "Responsible Disclosure"},
		Roles: []util.Role{
			{Name: "Maintainer", Color: color.NRGBA{R: 200, G: 90, B: 90, A: 255}},
			{Name: "Reviewer"},
			{Name: "Triage"},
			{Name: "Release Manager"},
			{Name: "Everyone Else"},
			{Name: "A Role With A Genuinely Excessive Name"},
		},
		Bot: true,
	}
}

// TestProfileCardsKeepTheirWidth is the fixed-width discipline the sidebars
// follow, applied to a card: every row inside shortens to the width it is given,
// so no name, handle, status or role can widen the card that carries it.
func TestProfileCardsKeepTheirWidth(t *testing.T) {
	test.NewTempApp(t)

	cases := []struct {
		name  string
		build func(Deps, Profile, ProfileActions) *ProfileCard
		width float32
	}{
		{"card", NewProfileCard, theme.Sizes.ProfileCardWidth},
		{"dialog", NewProfileDialog, theme.Sizes.ProfileDialogWidth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := tc.build(viewerDeps(), crowdedProfile(), ProfileActions{
				OnMessage: func() {},
				OnClose:   func() {},
			})
			card.SetBio(strings.Repeat("a bio that goes on and on and on. ", 40))

			if got := card.Content.MinSize().Width; got != tc.width {
				t.Errorf("card is %vpx wide, want %v", got, tc.width)
			}
		})
	}
}

// TestProfileAvatarOverhangsTheBanner covers the header's one piece of geometry:
// the avatar is raised so its centre lands on the banner's bottom edge, which is
// what makes the banner read as something the picture sits on rather than above.
func TestProfileAvatarOverhangsTheBanner(t *testing.T) {
	test.NewTempApp(t)

	card := NewProfileCard(viewerDeps(), Profile{Name: "Someone"}, ProfileActions{})
	card.Content.Resize(card.Content.MinSize())

	// The placeholder circle is the avatar until an image lands over it, and the
	// ring behind it is the larger circle drawn at the same centre.
	var avatar *canvas.Circle
	var centre fyne.Position
	walkTree(card.Content, func(obj fyne.CanvasObject, pos fyne.Position) {
		circle, ok := obj.(*canvas.Circle)
		if !ok || circle.Size().Width != theme.Sizes.ProfileAvatarSize {
			return
		}
		if avatar == nil {
			avatar = circle
			centre = pos.Add(fyne.NewPos(circle.Size().Width/2, circle.Size().Height/2))
		}
	})

	if avatar == nil {
		t.Fatal("the card drew no avatar")
	}
	if want := theme.Sizes.ProfileBannerHeight; centre.Y != want {
		t.Errorf("avatar centred at y=%v, want the banner's edge at %v", centre.Y, want)
	}
}

// TestPresenceRingSurroundsTheAvatar covers the header's other piece of geometry:
// availability is a ring the avatar sits inside, drawn wider than the picture so
// it shows all the way round — and drawn not at all when someone is offline,
// which is the state invisible has to be indistinguishable from.
func TestPresenceRingSurroundsTheAvatar(t *testing.T) {
	test.NewTempApp(t)

	ringOf := func(presence Presence) *canvas.Circle {
		card := NewProfileCard(viewerDeps(), Profile{Name: "Someone", Presence: presence}, ProfileActions{})
		card.Content.Resize(card.Content.MinSize())

		var ring *canvas.Circle
		walkTree(card.Content, func(obj fyne.CanvasObject, _ fyne.Position) {
			circle, ok := obj.(*canvas.Circle)
			if ok && ring == nil && circle.FillColor == presence.Color() {
				ring = circle
			}
		})

		return ring
	}

	for _, presence := range []Presence{PresenceOnline, PresenceIdle, PresenceFocus, PresenceBusy} {
		ring := ringOf(presence)
		if ring == nil {
			t.Fatalf("%s drew no ring", presence.Label())
		}
		if got := ring.Size().Width; got <= theme.Sizes.ProfileAvatarSize {
			t.Errorf("%s ring is %vpx across, want wider than the %vpx avatar",
				presence.Label(), got, theme.Sizes.ProfileAvatarSize)
		}
	}

	if ringOf(PresenceOffline) != nil {
		t.Error("an offline user was given a presence ring")
	}
}

// TestProfileHandleSitsBesideTheName checks the identity line: the account's real
// handle sits to the right of the display name, on the same line and at its own
// smaller size — a qualifier to the name, not a second title.
func TestProfileHandleSitsBesideTheName(t *testing.T) {
	test.NewTempApp(t)

	card := NewProfileCard(viewerDeps(), Profile{
		Name:   "Someone",
		Handle: "@someone#9147",
	}, ProfileActions{})
	card.Content.Resize(card.Content.MinSize())

	var name, handle *canvas.Text
	var namePos, handlePos fyne.Position
	walkTree(card.Content, func(obj fyne.CanvasObject, pos fyne.Position) {
		text, ok := obj.(*canvas.Text)
		if !ok {
			return
		}
		switch text.TextSize {
		case theme.Sizes.ProfileNameSize:
			name, namePos = text, pos
		case theme.Sizes.ProfileHandleSize:
			handle, handlePos = text, pos
		}
	})

	if name == nil || handle == nil {
		t.Fatal("the card drew no name or no handle")
	}
	if handle.Text != "@someone#9147" {
		t.Errorf("handle reads %q, want the discriminator intact", handle.Text)
	}
	if handlePos.X <= namePos.X {
		t.Errorf("handle starts at x=%v, want it right of the name at %v", handlePos.X, namePos.X)
	}

	centre := func(text *canvas.Text, pos fyne.Position) float32 { return pos.Y + text.Size().Height/2 }
	if drift := centre(name, namePos) - centre(handle, handlePos); drift < -1 || drift > 1 {
		t.Errorf("handle sits %vpx off the name's line, want them centred together", drift)
	}
}

// TestProfileBioArrivesAfterTheCard covers the late half of a profile: the bio is
// a request of its own, so the card is built without one and grows when it lands.
// An empty bio leaves the section out rather than showing an empty well.
func TestProfileBioArrivesAfterTheCard(t *testing.T) {
	test.NewTempApp(t)

	t.Run("filled in", func(t *testing.T) {
		card := NewProfileCard(viewerDeps(), Profile{Name: "Someone"}, ProfileActions{})
		before := card.Content.MinSize().Height

		card.SetBio("Developer at Team Eidolonic")
		if after := card.Content.MinSize().Height; after <= before {
			t.Errorf("card is %vpx tall with a bio, want more than the %v without one", after, before)
		}
	})

	t.Run("empty", func(t *testing.T) {
		card := NewProfileCard(viewerDeps(), Profile{Name: "Someone"}, ProfileActions{})
		before := card.Content.MinSize().Height

		card.SetBio("   ")
		if after := card.Content.MinSize().Height; after != before {
			t.Errorf("an empty bio changed the card's height from %v to %v", before, after)
		}
	})
}

// TestPresenceOfReadsTheUser covers the two cases the mapping is there for: a
// user who is not connected is offline whatever they picked, and invisible is
// indistinguishable from offline.
func TestPresenceOfReadsTheUser(t *testing.T) {
	cases := []struct {
		name string
		user *revoltgo.User
		want Presence
	}{
		{"unknown", nil, PresenceOffline},
		{"disconnected", &revoltgo.User{}, PresenceOffline},
		{"connected", &revoltgo.User{Online: true}, PresenceOnline},
		{"busy", &revoltgo.User{Online: true, Status: &revoltgo.UserStatus{
			Presence: revoltgo.UserStatusPresenceBusy,
		}}, PresenceBusy},
		{"invisible", &revoltgo.User{Online: true, Status: &revoltgo.UserStatus{
			Presence: revoltgo.UserStatusPresenceInvisible,
		}}, PresenceOffline},
		{"idle but disconnected", &revoltgo.User{Status: &revoltgo.UserStatus{
			Presence: revoltgo.UserStatusPresenceIdle,
		}}, PresenceOffline},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PresenceOf(tc.user); got != tc.want {
				t.Errorf("presence is %v, want %v", got, tc.want)
			}
		})
	}
}
