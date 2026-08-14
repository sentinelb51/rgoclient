package app

import (
	"strings"
	"testing"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
)

// pinStoreStub answers the two "is this already known" questions unknownAuthors
// asks. It is storeStub with a membership table, the panel being the one surface
// that resolves authors without ensureAuthor's guards behind it.
type pinStoreStub struct {
	storeStub
	members map[string]bool
}

func (s pinStoreStub) HasUser(userID string) bool {
	_, ok := s.users[userID]

	return ok
}

func (s pinStoreStub) HasMember(serverID, userID string) bool {
	return s.members[serverID+":"+userID]
}

// TestUnknownAuthors pins what the panel asks the network for. Every mistake
// here is invisible on screen: asking again for somebody already resolved is a
// request per pin nobody sees, and skipping somebody who is a known *user* but
// not a known member of this server draws them without their nickname or role
// colour — the same person the rest of the client names correctly.
func TestUnknownAuthors(t *testing.T) {
	a := &App{store: pinStoreStub{
		storeStub: storeStub{users: map[string]domain.User{
			"01KNOWN":     {ID: "01KNOWN"},
			"01USERONLY":  {ID: "01USERONLY"},
			"01REPEATING": {ID: "01REPEATING"},
		}},
		members: map[string]bool{"01SERVER:01KNOWN": true},
	}}

	messages := []*domain.Message{
		{ID: "01A", AuthorID: "01KNOWN"},     // resolved both ways
		{ID: "01B", AuthorID: "01USERONLY"},  // the account, not the membership
		{ID: "01C", AuthorID: "01STRANGER"},  // neither
		{ID: "01D", AuthorID: "01STRANGER"},  // the same stranger again
		{ID: "01E", AuthorID: "01REPEATING"}, // a user with no membership
		{ID: "01F"},                          // a system message, nobody's
	}

	cases := []struct {
		name     string
		serverID string
		want     []string
	}{
		{
			name:     "in a server",
			serverID: "01SERVER",
			want:     []string{"01USERONLY", "01STRANGER", "01REPEATING"},
		},
		{
			// A conversation has no membership to resolve, so a known account is
			// the whole answer and only the stranger is worth a request.
			name: "in a conversation",
			want: []string{"01STRANGER"},
		},
	}

	for _, c := range cases {
		got := userIDs(a.unknownAuthors(c.serverID, messages))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: would fetch %v, want %v", c.name, got, c.want)
		}
	}
}

func userIDs(targets []client.AuthorRef) []string {
	out := make([]string, len(targets))
	for i, target := range targets {
		out[i] = target.UserID
	}

	return out
}

// TestPinPreview covers the fallback ladder, which exists because a pin is
// routinely a picture: a row summarising a message with no text would otherwise
// be a name over a blank line, and nothing else in the panel says what the pin
// is. The flattening matters for the same reason — a pinned code block is worth
// keeping and is several lines of it.
func TestPinPreview(t *testing.T) {
	cases := []struct {
		name    string
		message domain.Message
		want    string
	}{
		{
			name:    "text is flattened onto one line",
			message: domain.Message{Content: "read this\n\n  before   Monday "},
			want:    "read this before Monday",
		},
		{
			name:    "a long body is cut",
			message: domain.Message{Content: strings.Repeat("a", 200)},
			want:    strings.Repeat("a", pinPreviewRunes-3) + "...",
		},
		{
			name:    "one attachment is named",
			message: domain.Message{Attachments: []*domain.File{{Name: "plan.png"}}},
			want:    "plan.png",
		},
		{
			name:    "several are counted as a kind",
			message: domain.Message{Attachments: []*domain.File{{Name: "a.png"}, {Name: "b.png"}}},
			want:    "Attachments",
		},
		{
			name:    "a bare link says what it unfurled to",
			message: domain.Message{Embeds: []*domain.Embed{{}}},
			want:    "Embed",
		},
		{
			name:    "nothing at all still says something",
			message: domain.Message{},
			want:    "No text",
		},
		{
			// Text wins over what is attached to it: the words are what the pin was
			// made for, and the file is beside them when the reader gets there.
			name:    "text wins over an attachment",
			message: domain.Message{Content: "the plan", Attachments: []*domain.File{{Name: "plan.png"}}},
			want:    "the plan",
		},
	}

	for _, c := range cases {
		if got := pinPreview(&c.message); got != c.want {
			t.Errorf("%s: summarised as %q, want %q", c.name, got, c.want)
		}
	}
}
