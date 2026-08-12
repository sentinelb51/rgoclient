package app

import (
	"testing"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// labels is what a card would draw, in order, with a disabled button marked —
// that difference is load-bearing for the outgoing case, where the state is the
// whole of what the first button says.
func labels(buttons []ui.ProfileButton) []string {
	out := make([]string, len(buttons))
	for i, button := range buttons {
		out[i] = button.Label
		if button.Do == nil {
			out[i] += " (disabled)"
		}
	}

	return out
}

// TestProfileButtons pins what a profile offers for each relationship, which is
// the one part of the card that can be wrong without looking wrong: "Message" is
// a conversation Revolt refuses to open unless the two are friends, so offering
// it to a stranger is a button that could only fail — and offering "Add friend"
// to a bot is one nothing would ever accept.
func TestProfileButtons(t *testing.T) {
	const self = "01SELF"

	cases := []struct {
		name         string
		userID       string
		relationship domain.Relationship
		bot          bool
		want         []string
	}{
		{name: "your own profile", userID: self},
		{name: "nobody", userID: ""},
		{name: "a bot", userID: "01BOT", bot: true, want: []string{"Message"}},
		{name: "a stranger", userID: "01U", want: []string{"Add friend", "Block"}},
		{name: "a friend", userID: "01U", relationship: domain.RelationshipFriend,
			want: []string{"Message", "Remove friend"}},
		{name: "they asked", userID: "01U", relationship: domain.RelationshipIncoming,
			want: []string{"Accept request", "Ignore"}},
		{name: "we asked", userID: "01U", relationship: domain.RelationshipOutgoing,
			want: []string{"Request sent (disabled)", "Cancel request"}},
		{name: "we blocked them", userID: "01U", relationship: domain.RelationshipBlocked,
			want: []string{"Unblock"}},
		{name: "they blocked us", userID: "01U", relationship: domain.RelationshipBlockedBy,
			want: []string{"Block"}},
	}

	a := &App{store: storeStub{selfID: self}}
	for _, c := range cases {
		got := labels(a.profileButtons(domain.Profile{
			UserID:       c.userID,
			Name:         "Someone",
			Relationship: c.relationship,
			Bot:          c.bot,
		}))

		if len(got) != len(c.want) {
			t.Errorf("%s: offered %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%s: offered %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
