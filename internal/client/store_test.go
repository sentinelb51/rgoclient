package client

import (
	"image/color"
	"slices"
	"testing"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want color.Color
		ok   bool
	}{
		{"#fff", color.NRGBA{R: 255, G: 255, B: 255, A: 255}, true},
		{"#f00", color.NRGBA{R: 255, G: 0, B: 0, A: 255}, true},
		{"#5B7CFA", color.NRGBA{R: 0x5B, G: 0x7C, B: 0xFA, A: 255}, true},
		{"#000000", color.NRGBA{A: 255}, true},
		{"5B7CFA", nil, false},                     // missing #
		{"#5B7CF", nil, false},                     // wrong length
		{"#GGGGGG", nil, false},                    // not hex
		{"linear-gradient(red, blue)", nil, false}, // named stops carry no triple
		{"", nil, false},
	}

	for _, tt := range tests {
		got, ok := parseColor(tt.in)
		if ok != tt.ok {
			t.Errorf("parseColor(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseColor(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// A role colour is as often one of Revolt's gradient presets as a triple, and
// every stop has to survive the conversion for anything to draw one.
func TestParseColorKeepsGradientStops(t *testing.T) {
	got, ok := parseColor("linear-gradient(to right, #D52D00, #EF7627, #FFFFFF)")
	if !ok {
		t.Fatal("a gradient of hex stops did not parse")
	}

	gradient, isGradient := got.(domain.Gradient)
	if !isGradient {
		t.Fatalf("parseColor returned %T, want a domain.Gradient", got)
	}

	want := domain.Gradient{
		color.NRGBA{R: 0xD5, G: 0x2D, B: 0x00, A: 255},
		color.NRGBA{R: 0xEF, G: 0x76, B: 0x27, A: 255},
		color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
	}
	if !slices.Equal(gradient, want) {
		t.Errorf("stops = %v, want %v", gradient, want)
	}
}

func TestRoleColorPicksMostSeniorColouredRole(t *testing.T) {
	server := &revoltgo.Server{
		Roles: map[string]*revoltgo.ServerRole{
			"admin":    {Rank: 1, Colour: new("#ff0000")},
			"mod":      {Rank: 5, Colour: new("#00ff00")},
			"everyone": {Rank: 10},                         // no colour: skipped
			"broken":   {Rank: 0, Colour: new("gradient")}, // most senior but unparseable
		},
	}

	got, ok := roleColor(server, []string{"mod", "admin", "everyone"})
	if !ok {
		t.Fatal("expected a colour")
	}
	if got != (color.NRGBA{R: 255, A: 255}) {
		t.Errorf("got %v, want admin red (lowest rank wins)", got)
	}

	// The most-senior role has an unparseable colour: it is still selected as
	// "best", so resolution fails — documents current behaviour.
	if _, ok := roleColor(server, []string{"broken", "admin"}); ok {
		t.Error("unparseable senior colour: expected ok=false")
	}

	if _, ok := roleColor(server, []string{"everyone"}); ok {
		t.Error("no coloured roles: expected ok=false")
	}
	if _, ok := roleColor(server, nil); ok {
		t.Error("no roles: expected ok=false")
	}
	if _, ok := roleColor(server, []string{"missing"}); ok {
		t.Error("unknown role id: expected ok=false")
	}
}

func TestServerRolesOrdersBySeniority(t *testing.T) {
	server := &revoltgo.Server{
		Roles: map[string]*revoltgo.ServerRole{
			"admin":    {Name: "Admin", Rank: 1, Colour: new("#ff0000")},
			"mod":      {Name: "Mod", Rank: 5, Colour: new("gradient")}, // unparseable: no colour
			"everyone": {Name: "Everyone", Rank: 10},
		},
	}

	roles := serverRoles(server, []string{"everyone", "missing", "mod", "admin"})
	if len(roles) != 3 {
		t.Fatalf("got %d roles, want the 3 the server defines", len(roles))
	}

	want := []string{"Admin", "Mod", "Everyone"}
	for i, name := range want {
		if roles[i].Name != name {
			t.Errorf("role %d is %q, want %q (lowest rank first)", i, roles[i].Name, name)
		}
	}

	if roles[0].Color != (color.NRGBA{R: 255, A: 255}) {
		t.Errorf("Admin is %v, want red", roles[0].Color)
	}
	if roles[1].Color != nil || roles[2].Color != nil {
		t.Error("a role with no parseable colour should carry none")
	}

	if serverRoles(nil, []string{"admin"}) != nil {
		t.Error("an unknown server should resolve no roles")
	}
}

func TestHandle(t *testing.T) {
	cases := []struct {
		name string
		user *revoltgo.User
		want string
	}{
		{"nameless", &revoltgo.User{}, ""},
		{"full", &revoltgo.User{Username: "sentinel", Discriminator: "9147"}, "@sentinel#9147"},
		{"no discriminator", &revoltgo.User{Username: "sentinel"}, "@sentinel"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := handle(tc.user); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToBadges(t *testing.T) {
	if got := toBadges(&revoltgo.User{}); got != nil {
		t.Errorf("an account with no badges got %v", got)
	}

	// Developer (1) + Supporter (4) + a bit this client doesn't know (1 << 20).
	got := toBadges(&revoltgo.User{Badges: 1 | 4 | 1<<20})
	want := []string{"Developer", "Supporter"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v with the unknown bit ignored", got, want)
	}
}

// TestToPresence covers the two cases the mapping exists for: a user who isn't
// connected is offline whatever they picked, and invisible is deliberately
// indistinguishable from offline.
func TestToPresence(t *testing.T) {
	cases := []struct {
		name string
		user *revoltgo.User
		want domain.Presence
	}{
		{"unknown", nil, domain.PresenceOffline},
		{"disconnected", &revoltgo.User{}, domain.PresenceOffline},
		{"connected", &revoltgo.User{Online: true}, domain.PresenceOnline},
		{"busy", &revoltgo.User{Online: true, Status: &revoltgo.UserStatus{
			Presence: revoltgo.UserStatusPresenceBusy,
		}}, domain.PresenceBusy},
		{"invisible", &revoltgo.User{Online: true, Status: &revoltgo.UserStatus{
			Presence: revoltgo.UserStatusPresenceInvisible,
		}}, domain.PresenceOffline},
		{"idle but disconnected", &revoltgo.User{Status: &revoltgo.UserStatus{
			Presence: revoltgo.UserStatusPresenceIdle,
		}}, domain.PresenceOffline},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toPresence(tc.user); got != tc.want {
				t.Errorf("got %v, want %v", got.Label(), tc.want.Label())
			}
		})
	}
}
