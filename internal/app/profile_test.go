package app

import (
	"testing"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// labels is what a card would draw, in order, with a disabled button and one
// filed behind the hamburger each marked. Both differences are load-bearing: the
// outgoing case's state is the whole of what its first button says, and where an
// action is drawn is what keeps a profile from leading with a way to block the
// person it names.
func labels(buttons []ui.ProfileButton) []string {
	out := make([]string, len(buttons))
	for i, button := range buttons {
		out[i] = button.Label
		if button.Do == nil {
			out[i] += " (disabled)"
		}
		if button.Overflow {
			out[i] += " (menu)"
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
	// Copying the ID is the one thing offered about anybody — including this
	// account, whose card the relationship policy answers for with nothing.
	const (
		self   = "01SELF"
		copyID = "Copy user ID (menu)"
	)

	cases := []struct {
		name         string
		userID       string
		relationship domain.Relationship
		bot          bool
		want         []string
	}{
		{name: "your own profile", userID: self, want: []string{copyID}},
		{name: "nobody", userID: ""},
		{name: "a bot", userID: "01BOT", bot: true, want: []string{"Message", copyID}},
		{name: "a stranger", userID: "01U", want: []string{"Add friend", "Block (menu)", copyID}},
		{name: "a friend", userID: "01U", relationship: domain.RelationshipFriend,
			want: []string{"Message", "Remove friend (menu)", "Block (menu)", copyID}},
		{name: "they asked", userID: "01U", relationship: domain.RelationshipIncoming,
			want: []string{"Accept request", "Ignore request", copyID}},
		{name: "we asked", userID: "01U", relationship: domain.RelationshipOutgoing,
			want: []string{"Request sent (disabled)", "Cancel request", copyID}},
		{name: "we blocked them", userID: "01U", relationship: domain.RelationshipBlocked,
			want: []string{"Unblock", copyID}},
		{name: "they blocked us", userID: "01U", relationship: domain.RelationshipBlockedBy,
			want: []string{"Block (menu)", copyID}},
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
