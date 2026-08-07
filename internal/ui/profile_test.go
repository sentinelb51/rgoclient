package ui

import (
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// crowdedProfile is a profile with everything filled in and nothing short: it is
// what a card has to survive without growing.
func crowdedProfile() domain.Profile {
	return domain.Profile{
		UserID:     "01AVATAR",
		Name:       strings.Repeat("Extremely Long Display Name ", 4),
		Handle:     "@" + strings.Repeat("username", 8) + "#9147",
		Status:     strings.Repeat("a status nobody could read in one line ", 3),
		Presence:   domain.PresenceOnline,
		ServerName: "A Server",
		// A gradient accent, so the name is a run of one text object per rune rather
		// than one for the whole name — the case that still has to shorten.
		Accent: domain.Gradient{
			color.NRGBA{R: 0xD5, G: 0x2D, B: 0x00, A: 255},
			color.NRGBA{R: 0xA3, G: 0x02, B: 0x62, A: 255},
		},
		Badges: []string{"Developer", "Early Adopter", "Responsible Disclosure"},
		Roles: []domain.Role{
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
		build func(Deps, domain.Profile, ProfileActions) *ProfileCard
		width float32
	}{
		{"card", NewProfileCard, theme.Sizes.ProfileCardWidth},
		{"dialog", NewProfileDialog, theme.Sizes.ProfileDialogWidth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := tc.build(testDeps(), crowdedProfile(), ProfileActions{
				OnMessage: func() {},
				OnClose:   func() {},
			})
			card.SetProfile(domain.UserProfile{Bio: strings.Repeat("a bio that goes on and on and on. ", 40)})

			if got := card.Content.MinSize().Width; got != tc.width {
				t.Errorf("card is %vpx wide, want %v", got, tc.width)
			}
		})
	}
}

// TestProfileBioArrivesAfterTheCard covers the late half of a profile: the bio is
// a request of its own, so the card is built without one and grows when it lands.
// An empty bio leaves the section out rather than showing an empty well.
func TestProfileBioArrivesAfterTheCard(t *testing.T) {
	test.NewTempApp(t)

	t.Run("filled in", func(t *testing.T) {
		card := NewProfileCard(testDeps(), domain.Profile{Name: "Someone"}, ProfileActions{})
		before := card.Content.MinSize().Height

		card.SetProfile(domain.UserProfile{Bio: "Developer at Team Eidolonic"})
		if after := card.Content.MinSize().Height; after <= before {
			t.Errorf("card is %vpx tall with a bio, want more than the %v without one", after, before)
		}
	})

	t.Run("empty", func(t *testing.T) {
		card := NewProfileCard(testDeps(), domain.Profile{Name: "Someone"}, ProfileActions{})
		before := card.Content.MinSize().Height

		card.SetProfile(domain.UserProfile{Bio: "   "})
		if after := card.Content.MinSize().Height; after != before {
			t.Errorf("an empty bio changed the card's height from %v to %v", before, after)
		}
	})
}
