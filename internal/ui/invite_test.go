package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
)

// TestInviteCodesIn covers the scan a message body is put through before any
// card is built. Both halves have to hold: an invite written any of the ways
// markdown allows is found, and an ordinary link — which util.InviteCode would
// happily read a code out of — never unfurls one.
func TestInviteCodesIn(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"bare link", "join us at https://rvlt.gg/dcRHWEF1 today", []string{"dcRHWEF1"}},
		{"masked link", "[our server](https://rvlt.gg/dcRHWEF1)", []string{"dcRHWEF1"}},
		{"bracketed link", "<https://rvlt.gg/dcRHWEF1>", []string{"dcRHWEF1"}},
		{"long form", "https://app.revolt.chat/invite/dcRHWEF1", []string{"dcRHWEF1"}},
		{"quoted", "> https://rvlt.gg/dcRHWEF1", []string{"dcRHWEF1"}},

		// The same server linked twice, once bare and once masked, is one card.
		{
			"deduped",
			"https://rvlt.gg/dcRHWEF1 and [again](https://rvlt.gg/dcRHWEF1)",
			[]string{"dcRHWEF1"},
		},
		{
			"two servers",
			"https://rvlt.gg/aaaaaaaa then https://rvlt.gg/bbbbbbbb",
			[]string{"aaaaaaaa", "bbbbbbbb"},
		},

		// Nothing here is a link, so nothing here is an invite.
		{"code span", "the code is `https://rvlt.gg/dcRHWEF1`", nil},
		{"code block", "```\nhttps://rvlt.gg/dcRHWEF1\n```", nil},
		{"spoiler", "||https://rvlt.gg/dcRHWEF1||", nil},
		{"ordinary link", "https://example.com/dcRHWEF1", nil},
		{"repo link", "see https://github.com/sentinelb51/revoltgo", nil},
		{"plain text", "no links at all in this one", nil},
		{"empty", "", nil},
	}

	for _, c := range cases {
		got := inviteCodesIn(c.body)
		if len(got) != len(c.want) {
			t.Errorf("%s: inviteCodesIn(%q) = %q, want %q", c.name, c.body, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: inviteCodesIn(%q) = %q, want %q", c.name, c.body, got, c.want)
				break
			}
		}
	}
}

// TestInviteCodesCapped verifies a body listing more servers than a message may
// unfurl is cut back rather than burying the conversation in cards.
func TestInviteCodesCapped(t *testing.T) {
	var body strings.Builder
	for _, code := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd", "eeeeeeee"} {
		body.WriteString("https://rvlt.gg/" + code + " ")
	}

	if got := inviteCodesIn(body.String()); len(got) != inviteCardsPerMessage {
		t.Errorf("a body naming five servers unfurled %d cards, want the cap of %d", len(got), inviteCardsPerMessage)
	}
}

/* The card */

// inviteRecorder answers the two navigations a card can make, recording which.
type inviteRecorder struct {
	stubActions
	joined string
	opened string
}

func (r *inviteRecorder) OnJoinInvite(code string) { r.joined = code }
func (r *inviteRecorder) OnServerTapped(id string) { r.opened = id }

// cardButton returns the card's action button, or nil when it offers none.
func cardButton(card *InviteCard) *Button {
	var found *Button
	walkTree(card.Content, func(obj fyne.CanvasObject, _ fyne.Position) {
		if b, ok := obj.(*Button); ok {
			found = b
		}
	})

	return found
}

// cardTexts returns every string the card draws, so a test can assert on what it
// says without depending on where it says it.
func cardTexts(card *InviteCard) []string {
	var found []string
	walkTree(card.Content, func(obj fyne.CanvasObject, _ fyne.Position) {
		if t, ok := obj.(*canvas.Text); ok && t.Text != "" {
			found = append(found, t.Text)
		}
	})

	return found
}

func says(card *InviteCard, want string) bool {
	for _, text := range cardTexts(card) {
		if strings.Contains(text, want) {
			return true
		}
	}

	return false
}

// TestInviteCardActionFollowsMembership covers the one decision the card makes
// for itself. The same invite is an offer to join or a way back to somewhere
// already joined, and only the store can tell which — getting it backwards would
// offer to join a server the account is standing in.
func TestInviteCardActionFollowsMembership(t *testing.T) {
	test.NewTempApp(t)

	deps := testDeps()
	deps.Store.(*fakeStore).servers = map[string]domain.Server{
		"01JOINED": {ID: "01JOINED", Name: "Home"},
	}
	recorder := &inviteRecorder{}
	deps.Actions = recorder

	stranger := newInviteCard(deps, "aaaaaaaa")
	stranger.SetInvite(domain.Invite{Code: "aaaaaaaa", ServerID: "01OTHER", ServerName: "Elsewhere"})

	button := cardButton(stranger)
	if button == nil {
		t.Fatal("an invite to a server the account is not in offered no action")
	}
	button.Tap()
	if recorder.joined != "aaaaaaaa" {
		t.Errorf("tapping the action joined %q, want the code aaaaaaaa", recorder.joined)
	}

	member := newInviteCard(deps, "bbbbbbbb")
	member.SetInvite(domain.Invite{Code: "bbbbbbbb", ServerID: "01JOINED", ServerName: "Home"})

	button = cardButton(member)
	if button == nil {
		t.Fatal("an invite to a server the account is already in offered no action")
	}
	button.Tap()
	if recorder.opened != "01JOINED" {
		t.Errorf("tapping the action opened %q, want the server 01JOINED", recorder.opened)
	}
	if recorder.joined != "aaaaaaaa" {
		t.Error("an invite to a server already joined tried to join it again")
	}
}

// TestInviteCardStateReplacesCaption guards the trap the card is built around:
// an ellipsis text fixes the string it shortens when it is constructed, so a
// caption rewritten in place silently keeps the one it mounted with. Every state
// has to actually reach the card.
func TestInviteCardStateReplacesCaption(t *testing.T) {
	test.NewTempApp(t)

	deps := testDeps()
	deps.Store.(*fakeStore).servers = map[string]domain.Server{
		"01JOINED": {ID: "01JOINED", Name: "Home"},
	}

	card := newInviteCard(deps, "aaaaaaaa")
	if !says(card, inviteCaptionLoading) {
		t.Fatalf("a fresh card says %q, want the loading caption", cardTexts(card))
	}

	card.SetInvite(domain.Invite{Code: "aaaaaaaa", ServerID: "01OTHER", ServerName: "Elsewhere"})
	if !says(card, inviteCaptionJoin) {
		t.Errorf("a resolved invite says %q, want the join caption", cardTexts(card))
	}

	card.SetInvite(domain.Invite{Code: "aaaaaaaa", ServerID: "01JOINED", ServerName: "Home"})
	if !says(card, inviteCaptionJoined) {
		t.Errorf("an invite to a server already joined says %q, want the member caption", cardTexts(card))
	}
}

// TestInviteCardFailOffersNothing verifies a code that never resolved drops its
// action rather than leaving a button that would ask the server again for
// something it has already refused.
func TestInviteCardFailOffersNothing(t *testing.T) {
	test.NewTempApp(t)

	card := newInviteCard(testDeps(), "aaaaaaaa")
	card.SetInvite(domain.Invite{Code: "aaaaaaaa", ServerID: "01OTHER", ServerName: "Elsewhere"})
	if cardButton(card) == nil {
		t.Fatal("a resolved invite offered no action to begin with")
	}

	card.Fail()
	if cardButton(card) != nil {
		t.Error("a failed invite still offers its button")
	}
}

// TestInviteDetail covers the line under the name, which is assembled from parts
// Revolt may or may not send rather than formatted in one go.
func TestInviteDetail(t *testing.T) {
	cases := []struct {
		invite domain.Invite
		want   string
	}{
		{domain.Invite{MemberCount: 1}, "1 member"},
		{domain.Invite{MemberCount: 2}, "2 members"},
		{domain.Invite{MemberCount: 12483}, "12,483 members"},
		{domain.Invite{MemberCount: 5, ChannelName: "general"}, "5 members · #general"},
		{domain.Invite{ChannelName: "general"}, "#general"},
		{domain.Invite{}, ""},
	}

	for _, c := range cases {
		if got := inviteDetail(c.invite); got != c.want {
			t.Errorf("inviteDetail(%+v) = %q, want %q", c.invite, got, c.want)
		}
	}
}
