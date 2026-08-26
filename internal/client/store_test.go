package client

import (
	"image/color"
	"slices"
	"testing"
	"time"

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
		{"5B7CFA", nil, false},  // missing #
		{"#5B7CF", nil, false},  // wrong length
		{"#GGGGGG", nil, false}, // not hex
		{"", nil, false},

		{"red", color.NRGBA{R: 255, A: 255}, true},
		{"REBECCAPURPLE", color.NRGBA{R: 0x66, G: 0x33, B: 0x99, A: 255}, true},
		{"rgb(91, 124, 250)", color.NRGBA{R: 0x5B, G: 0x7C, B: 0xFA, A: 255}, true},
		{"rgb(100% 0% 0% / 50%)", color.NRGBA{R: 255, A: 128}, true},
		{"hsl(120, 100%, 50%)", color.NRGBA{G: 255, A: 255}, true},
		{"#f008", color.NRGBA{R: 255, A: 136}, true},
		{"to right", nil, false}, // a gradient's own words are not colours
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

// TestMemberRoleInfoPicksMostSeniorOfEach covers the one walk that decides both
// of the things a member row asks of its roles. The two answers are independent
// — the most senior *coloured* role need not be the most senior *hoisted* one —
// and answering them from one pass is exactly where that could be got wrong.
func TestMemberRoleInfoPicksMostSeniorColouredAndHoistedRoles(t *testing.T) {
	server := &revoltgo.Server{
		Roles: map[string]*revoltgo.ServerRole{
			"owner":    {Rank: 0, Hoist: true},            // hoisted, no colour
			"admin":    {Rank: 1, Colour: new("#ff0000")}, // coloured, not hoisted
			"mod":      {Rank: 5, Colour: new("#00ff00"), Hoist: true},
			"everyone": {Rank: 10},                         // neither
			"broken":   {Rank: 0, Colour: new("gradient")}, // most senior but unparseable
		},
	}

	table := newRoleTable(server)

	fill, hoist := memberRoleInfo(table, []string{"mod", "admin", "owner", "everyone"})
	if fill != (color.NRGBA{R: 255, A: 255}) {
		t.Errorf("colour = %v, want admin red (lowest rank wins)", fill)
	}
	if hoist != "owner" {
		t.Errorf("hoist = %q, want owner — the senior hoisted role, colourless or not", hoist)
	}

	// The most-senior coloured role has an unparseable colour: it still wins the
	// comparison, so nothing is resolved. Documents current behaviour.
	if fill, _ := memberRoleInfo(table, []string{"broken", "admin"}); fill != nil {
		t.Errorf("unparseable senior colour: got %v, want nil", fill)
	}

	for _, roles := range [][]string{{"everyone"}, nil, {"missing"}} {
		if fill, hoist := memberRoleInfo(table, roles); fill != nil || hoist != "" {
			t.Errorf("roles %v: got (%v, %q), want (nil, \"\")", roles, fill, hoist)
		}
	}

	if fill, hoist := memberRoleInfo(nil, []string{"admin"}); fill != nil || hoist != "" {
		t.Errorf("no server: got (%v, %q), want (nil, \"\")", fill, hoist)
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

/* Permissions */

// permissionFixture is the shape every case below varies: a server whose default
// role may see and read, one senior role and one junior one, and a text channel
// in it. ViewChannel is in the server's grant because losing it zeroes everything
// else, so a case that is not about visibility has to keep it.
func permissionFixture() (*revoltgo.Server, *revoltgo.Channel) {
	server := &revoltgo.Server{
		ID:                 "server",
		Owner:              "owner",
		DefaultPermissions: int64(domain.PermissionViewChannel | domain.PermissionReadMessageHistory),
		Roles: map[string]*revoltgo.ServerRole{
			// Revolt ranks the most senior lowest, so "senior" outranks "junior".
			"senior": {Rank: 1},
			"junior": {Rank: 5},
		},
	}
	channel := &revoltgo.Channel{
		ID:          "channel",
		ChannelType: revoltgo.ChannelTypeText,
		Server:      new("server"),
	}

	return server, channel
}

func member(roles ...string) *revoltgo.ServerMember {
	return &revoltgo.ServerMember{
		ID:    revoltgo.MemberCompositeID{Server: "server", User: "self"},
		Roles: roles,
	}
}

// A channel denied to everyone and handed back to one role is how Revolt hides a
// channel: the channel's default overwrite has to be applied before the roles, or
// the denial has the last word and the role that holds the channel cannot see it.
func TestChannelPermissionsAppliesRoleOverwrites(t *testing.T) {
	server, channel := permissionFixture()
	channel.DefaultPermissions = &revoltgo.PermissionOverwrite{Deny: int64(domain.PermissionViewChannel)}
	channel.RolePermissions = map[string]revoltgo.PermissionOverwrite{
		"senior": {Allow: int64(domain.PermissionViewChannel)},
	}

	if got := channelPermissions(server, member("senior"), channel, "self"); !got.Has(domain.PermissionViewChannel) {
		t.Error("a role the channel grants ViewChannel to cannot see it")
	}
	if got := channelPermissions(server, member("junior"), channel, "self"); got.Has(domain.PermissionViewChannel) {
		t.Error("a role the channel says nothing about can see a channel denied to everyone")
	}
}

// The same denial handed back by a *server* role rather than a channel overwrite,
// which is the ordering the channel's default overwrite coming first is for: apply
// it after the roles and the denial wins, and the role that holds the channel
// cannot see it.
func TestChannelPermissionsServerRoleOutlastsChannelDefault(t *testing.T) {
	server, channel := permissionFixture()
	server.Roles["senior"].Permissions = revoltgo.PermissionOverwrite{Allow: int64(domain.PermissionViewChannel)}
	channel.DefaultPermissions = &revoltgo.PermissionOverwrite{Deny: int64(domain.PermissionViewChannel)}

	if got := channelPermissions(server, member("senior"), channel, "self"); !got.Has(domain.PermissionViewChannel) {
		t.Error("a server role granting ViewChannel cannot see a channel denied to everyone")
	}
	if got := channelPermissions(server, member("junior"), channel, "self"); got.Has(domain.PermissionViewChannel) {
		t.Error("a role granting nothing can see a channel denied to everyone")
	}
}

// Two roles disagreeing about one permission is settled by rank, not by the
// order the member happens to carry them in.
func TestChannelPermissionsSeniorRoleWins(t *testing.T) {
	server, channel := permissionFixture()
	server.Roles["senior"].Permissions = revoltgo.PermissionOverwrite{Allow: int64(domain.PermissionSendMessage)}
	server.Roles["junior"].Permissions = revoltgo.PermissionOverwrite{Deny: int64(domain.PermissionSendMessage)}

	for _, order := range [][]string{{"senior", "junior"}, {"junior", "senior"}} {
		if got := channelPermissions(server, member(order...), channel, "self"); !got.Has(domain.PermissionSendMessage) {
			t.Errorf("roles %v: the junior role's denial beat the senior role's grant", order)
		}
	}

	// And the other way round, so the test can't pass by ignoring rank entirely.
	server.Roles["senior"].Permissions = revoltgo.PermissionOverwrite{Deny: int64(domain.PermissionSendMessage)}
	server.Roles["junior"].Permissions = revoltgo.PermissionOverwrite{Allow: int64(domain.PermissionSendMessage)}

	if got := channelPermissions(server, member("junior", "senior"), channel, "self"); got.Has(domain.PermissionSendMessage) {
		t.Error("the junior role's grant beat the senior role's denial")
	}
}

// A timeout is clamped last, after the channel's overwrites: an overwrite that
// grants SendMessage must not hand back what the timeout took.
func TestChannelPermissionsClampsTimeoutLast(t *testing.T) {
	server, channel := permissionFixture()
	channel.RolePermissions = map[string]revoltgo.PermissionOverwrite{
		"senior": {Allow: int64(domain.PermissionViewChannel | domain.PermissionSendMessage)},
	}

	timedOut := member("senior")
	timedOut.Timeout = new(time.Now().Add(time.Hour))

	got := channelPermissions(server, timedOut, channel, "self")
	if got.Has(domain.PermissionSendMessage) {
		t.Error("a timed-out member can still send")
	}
	if !got.Has(domain.PermissionViewChannel) {
		t.Error("a timed-out member should keep what the timeout preset leaves")
	}

	// An expired timeout is no timeout.
	served := member("senior")
	served.Timeout = new(time.Now().Add(-time.Hour))
	if !channelPermissions(server, served, channel, "self").Has(domain.PermissionSendMessage) {
		t.Error("an expired timeout is still being enforced")
	}
}

// The owner holds everything, whatever the channel says — and a membership State
// has not caught up with resolves as one carrying no roles, not as no access:
// see rankRoles.
func TestChannelPermissionsOwnerAndUnknownMember(t *testing.T) {
	server, channel := permissionFixture()
	channel.DefaultPermissions = &revoltgo.PermissionOverwrite{Deny: revoltgo.PermissionGrantAllSafe}

	if !channelPermissions(server, nil, channel, "owner").Has(domain.PermissionViewChannel) {
		t.Error("the owner was refused a channel that denies everything")
	}

	server.DefaultPermissions = int64(domain.PermissionViewChannel)
	channel.DefaultPermissions = nil
	if !channelPermissions(server, nil, channel, "self").Has(domain.PermissionViewChannel) {
		t.Error("an unresolved membership was refused what the server grants by default")
	}
}

// A DM carries no permissions field — Revolt only sends one on a group — so it is
// the relationship that decides whether the composer is live. A group's field is
// an allow-only overwrite over the view-only floor, never the whole answer.
func TestConversationPermissions(t *testing.T) {
	dm := &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeDM, Recipients: []string{"self", "them"}}
	group := &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeGroup, Owner: "them"}
	notes := &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeSavedMessages}

	if !conversationPermissions(dm, domain.RelationshipFriend, "self").Has(domain.PermissionSendMessage) {
		t.Error("an ordinary direct message will not take a message")
	}
	if !conversationPermissions(dm, domain.RelationshipNone, "self").Has(domain.PermissionSendMessage) {
		t.Error("a direct message with an unresolved recipient will not take a message")
	}

	for _, relationship := range []domain.Relationship{
		domain.RelationshipBlocked, domain.RelationshipBlockedBy,
	} {
		got := conversationPermissions(dm, relationship, "self")
		if got.Has(domain.PermissionSendMessage) {
			t.Errorf("%v: a blocked direct message still takes messages", relationship)
		}
		if !got.Has(domain.PermissionReadMessageHistory) {
			t.Errorf("%v: a blocked direct message should keep its history readable", relationship)
		}
	}

	if !conversationPermissions(group, domain.RelationshipNone, "self").Has(domain.PermissionSendMessage) {
		t.Error("a group with no permissions of its own will not take a message")
	}
	if !conversationPermissions(group, domain.RelationshipNone, "them").Has(domain.PermissionManageMessages) {
		t.Error("the group's owner cannot manage it")
	}
	group.Permissions = new(int64(domain.PermissionViewChannel))
	if conversationPermissions(group, domain.RelationshipNone, "self").Has(domain.PermissionSendMessage) {
		t.Error("a group's own permissions were ignored")
	}

	if !conversationPermissions(notes, domain.RelationshipNone, "self").Has(domain.PermissionSendMessage) {
		t.Error("saved notes will not take a note")
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
