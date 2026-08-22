package app

// The conversation half of the mention pool, exercised as the pure function it
// is. The server half is deliberately not reachable from here: it belongs to
// refreshMemberList, which makes its walk off the UI thread — see
// refreshMentionCandidates.

import (
	"testing"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// storeStub answers the one read recipientCandidates makes. The interface is
// embedded rather than implemented: anything else reaching through it is a nil
// dereference, which is the loudest way for a test to say it has grown a
// dependency it did not declare.
type storeStub struct {
	domain.Store
	users  map[string]domain.User
	selfID string
}

func (s storeStub) User(userID string) (domain.User, bool) {
	user, ok := s.users[userID]

	return user, ok
}

func (s storeStub) SelfID() string { return s.selfID }

func names(candidates []ui.MentionCandidate) []string {
	out := make([]string, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate.Name
	}

	return out
}

// TestRecipientCandidates pins the two rules that are not obvious from reading
// the loop: the order is by display name folded to lower case — not the order the
// channel lists its recipients in, which is what a group would otherwise offer —
// and a recipient the store cannot name is left out rather than offered as a
// blank row nothing would match.
func TestRecipientCandidates(t *testing.T) {
	store := storeStub{users: map[string]domain.User{
		"01ZOE":   {ID: "01ZOE", Name: "zoe", Username: "zoe"},
		"01ADA":   {ID: "01ADA", Name: "Ada", Username: "ada"},
		"01BRIAN": {ID: "01BRIAN", Name: "brian", Username: "brian"},
	}}

	channel := domain.Channel{
		ID:   "01GROUP",
		Kind: domain.ChannelGroup,
		// Deliberately unsorted, and carrying somebody nobody has resolved.
		Recipients: []string{"01ZOE", "01UNKNOWN", "01BRIAN", "01ADA"},
	}

	got := names(recipientCandidates(store, channel))
	want := []string{"Ada", "brian", "zoe"}

	if len(got) != len(want) {
		t.Fatalf("resolved %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolved %v, want %v", got, want)
		}
	}
}

// TestRecipientCandidatesEmpty covers a channel with nobody in it — a direct
// message whose recipients have not arrived yet — which must offer nothing rather
// than panic on the walk.
func TestRecipientCandidatesEmpty(t *testing.T) {
	store := storeStub{users: map[string]domain.User{}}

	if got := recipientCandidates(store, domain.Channel{ID: "01DM"}); len(got) != 0 {
		t.Fatalf("an empty conversation offered %v", names(got))
	}
}

// TestMemberStatusFor pins the precedence, which is the only part of the strip
// that can be wrong without being visibly wrong: a fetch still out must outrank
// the failure that a retry cleared, and a failure must outrank an empty list —
// "nobody to show here" for a membership that never arrived is a claim the user
// has no way to see through, and the retry goes with it.
func TestMemberStatusFor(t *testing.T) {
	cases := []struct {
		name                   string
		serverID               string
		loading, failed, empty bool
		text, action           string
		busy                   bool
	}{
		{name: "home has no membership", empty: true},
		{name: "first load", serverID: "01S", loading: true, empty: true,
			text: "Loading members", busy: true},
		{name: "refresh keeps the rows", serverID: "01S", loading: true,
			text: "Refreshing members", busy: true},
		{name: "a retry outranks the failure it clears", serverID: "01S", loading: true, failed: true, empty: true,
			text: "Loading members", busy: true},
		{name: "a failure outranks an empty list", serverID: "01S", failed: true, empty: true,
			text: "Couldn't load members.", action: "Retry"},
		{name: "genuinely empty", serverID: "01S", empty: true, text: "Nobody to show here."},
		{name: "nothing to say", serverID: "01S"},
	}

	for _, c := range cases {
		got := memberStatusFor(c.serverID, c.loading, c.failed, c.empty)

		if got.Text != c.text || got.Busy != c.busy || got.Action != c.action {
			t.Errorf("%s: got %+v, want text %q busy %v action %q", c.name, got, c.text, c.busy, c.action)
		}
	}
}

// memberStub answers the one read patchedMembers makes. Its misses are the
// interesting half: a member the store has forgotten is one whose row must stand.
type memberStub struct {
	domain.Store
	members map[string]domain.Member // userID -> what the store says now
}

func (s memberStub) Member(_, userID string) (domain.Member, bool) {
	member, ok := s.members[userID]

	return member, ok
}

// summarise is what a member is worth to these tests. Not a struct comparison:
// domain.Member holds a color.Color, which a role gradient makes uncomparable.
func summarise(members []domain.Member) []string {
	out := make([]string, len(members))
	for i, member := range members {
		out[i] = member.UserID + "/" + member.Name + "/" + member.Presence.Label()
	}

	return out
}

func sameSummary(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

// TestPatchedMembers pins what a presence flush may and may not touch. Every one
// of these fails silently — the sidebar redraws either way, with somebody's dot
// on somebody else's row — which is the whole reason the picking is a function
// rather than four lines inside the worker.
//
// The store deliberately disagrees about a member nobody named: a walk would take
// its answer, and taking it here would mean the flush had quietly become one.
func TestPatchedMembers(t *testing.T) {
	previous := []domain.Member{
		{UserID: "01ADA", Name: "Ada", Presence: domain.PresenceOnline},
		{UserID: "01BRIAN", Name: "Brian", Presence: domain.PresenceOffline},
		{UserID: "01ZOE", Name: "Zoe", Presence: domain.PresenceOnline},
	}

	store := memberStub{members: map[string]domain.Member{
		// Named, and the whole value is taken: Brian is idle *and* renamed.
		"01BRIAN": {UserID: "01BRIAN", Name: "Bri", Presence: domain.PresenceIdle},
		// Not named. A walk would file Ada under Offline; this must not.
		"01ADA": {UserID: "01ADA", Name: "Ada", Presence: domain.PresenceOffline},
		// Named, and not in the membership at all — they joined after it was
		// resolved, so they belong to the next walk rather than to this copy.
		"01CARA": {UserID: "01CARA", Name: "Cara", Presence: domain.PresenceOnline},
	}}

	// 01ZOE is named and the store has forgotten them, which must leave the row
	// standing rather than blanking it.
	changed := map[string]bool{"01BRIAN": true, "01ZOE": true, "01CARA": true}

	got := summarise(patchedMembers(store, "01SERVER", previous, changed))
	want := []string{
		"01ADA/Ada/Online",
		"01BRIAN/Bri/Idle",
		"01ZOE/Zoe/Online",
	}

	if !sameSummary(got, want) {
		t.Errorf("patched %v, want %v", got, want)
	}
}

// TestPatchedMembersLeavesPreviousAlone is the invariant the whole design rests
// on: the membership handed in is published, and another worker may be reading it
// while this one runs. Patching in place would be correct on screen and a data
// race underneath, which no other test here would notice.
func TestPatchedMembersLeavesPreviousAlone(t *testing.T) {
	previous := []domain.Member{
		{UserID: "01ADA", Name: "Ada", Presence: domain.PresenceOnline},
		{UserID: "01BRIAN", Name: "Brian", Presence: domain.PresenceOffline},
	}
	before := summarise(previous)

	store := memberStub{members: map[string]domain.Member{
		"01ADA":   {UserID: "01ADA", Name: "Ada", Presence: domain.PresenceOffline},
		"01BRIAN": {UserID: "01BRIAN", Name: "Brian", Presence: domain.PresenceOnline},
	}}

	patched := patchedMembers(store, "01SERVER", previous, map[string]bool{"01ADA": true, "01BRIAN": true})

	if after := summarise(previous); !sameSummary(after, before) {
		t.Errorf("the published membership moved: %v, was %v", after, before)
	}
	if &patched[0] == &previous[0] {
		t.Error("patchedMembers handed back the slice it was given")
	}
}

// TestPatchedMembersNothingNamed covers the flush that finds its people already
// current — a burst coalesced onto somebody whose presence came back where it
// started. It must still hand back a copy, the caller publishing what it returns.
func TestPatchedMembersNothingNamed(t *testing.T) {
	previous := []domain.Member{{UserID: "01ADA", Name: "Ada", Presence: domain.PresenceOnline}}

	got := patchedMembers(memberStub{}, "01SERVER", previous, map[string]bool{"01NOBODY": true})

	if want := summarise(previous); !sameSummary(summarise(got), want) {
		t.Errorf("patched %v, want %v", summarise(got), want)
	}
	if &got[0] == &previous[0] {
		t.Error("patchedMembers handed back the slice it was given")
	}
}
