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
	users map[string]domain.User
}

func (s storeStub) User(userID string) (domain.User, bool) {
	user, ok := s.users[userID]

	return user, ok
}

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
