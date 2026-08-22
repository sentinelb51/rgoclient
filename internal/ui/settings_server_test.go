package ui

// What a fetched list costs, tested because every way of getting it wrong is
// invisible. A duplicate request looks exactly like a single one; an answer
// recorded for a page that has moved on looks exactly like an answer. Nothing
// crashes and no row is drawn wrong — the client just asks Revolt twice, or files
// one server's bans under another's name and never asks again.

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
)

// TestServerListsAskOnce covers cachedList's three rules through the taps that
// exercise them: a section mounted twice while its first request is still out
// must not send a second, the answer must fill the card that is mounted when it
// lands rather than the one it was asked from, a held answer must not be re-asked,
// and one past listTTL must be.
func TestServerListsAskOnce(t *testing.T) {
	test.NewTempApp(t)

	probe := &serverListProbe{}
	page := newTestServerSettingsPage(probe)
	page.Open()

	page.showSection(ServerSectionInvites)
	if probe.inviteCalls != 1 {
		t.Fatalf("mounting Invites made %d requests, want 1", probe.inviteCalls)
	}
	asked := page.listBody // the card this request went out from

	// Single-flight: the answer is still on its way, so tapping away and back waits
	// for it rather than asking again.
	page.showSection(ServerSectionBans)
	page.showSection(ServerSectionInvites)

	if probe.inviteCalls != 1 {
		t.Errorf("re-mounting Invites with a request already out made %d in all, want 1", probe.inviteCalls)
	}

	// And it has to land in the card mounted *now*: the one above is detached, and
	// single-flight means nothing else is coming to fill this one.
	probe.answerInvites([]ServerInviteEntry{{Code: "aaa"}, {Code: "bbb"}, {Code: "ccc"}}, nil)

	// Invites are drawn two to a row, so three of them fill two rows — which is also
	// what tells the filled card from the one still holding its "Fetching…" line.
	if rows := len(page.listBody.Objects); rows != 3 {
		t.Errorf("the mounted card holds %d objects, want the two rows three invites fill and the hairline between them", rows)
	}
	if len(asked.Objects) != 1 {
		t.Errorf("the answer was drawn into the card it was asked from, which is no longer on screen")
	}

	// Held: nothing announces a revoked invite, so a second fetch could only answer
	// what the first one did.
	page.showSection(ServerSectionBans)
	page.showSection(ServerSectionInvites)

	if probe.inviteCalls != 1 {
		t.Errorf("a held answer was re-asked: %d requests in all, want 1", probe.inviteCalls)
	}

	// Expiring: one older than listTTL is not believed.
	page.invites.at = time.Now().Add(-listTTL - time.Second)
	page.showSection(ServerSectionBans)
	page.showSection(ServerSectionInvites)

	if probe.inviteCalls != 2 {
		t.Errorf("a stale answer was drawn without asking again: %d requests in all, want 2", probe.inviteCalls)
	}
}

// TestServerListsDropAnAnswerFromAClosedVisit covers the guard a bool could not
// carry. The page is one widget reused for every server, so an answer that lands
// after it has been closed and reopened is not merely late — it is about a
// different server. Recording it would file those bans under this server and mark
// them fresh, so the section would draw them and never ask.
func TestServerListsDropAnAnswerFromAClosedVisit(t *testing.T) {
	test.NewTempApp(t)

	probe := &serverListProbe{}
	page := newTestServerSettingsPage(probe)

	page.Open()
	page.showSection(ServerSectionBans)
	late := probe.answerBans

	page.Close()
	page.Open() // another server, the same page

	late([]domain.Ban{{UserID: "u", Username: "Someone"}}, nil)

	if !page.bans.pending() || len(page.bans.entries) != 0 {
		t.Errorf("an answer from a closed visit was recorded: %d entries held", len(page.bans.entries))
	}

	page.showSection(ServerSectionBans)
	if probe.banCalls != 2 {
		t.Errorf("the reopened page made %d requests in all, want one of its own", probe.banCalls)
	}
}

// TestChangingAListClearsItFirst covers the one thing that can make a held answer
// wrong: this client changing it. Revoking an invite answers with nothing, so what
// the section draws afterwards is whatever the page is still holding — and that
// still has the revoked invite in it.
func TestChangingAListClearsItFirst(t *testing.T) {
	test.NewTempApp(t)

	probe := &serverListProbe{}
	page := newTestServerSettingsPage(probe)
	page.Open()

	page.showSection(ServerSectionInvites)
	probe.answerInvites([]ServerInviteEntry{{Code: "aaa"}}, nil)

	page.reloadList(nil, "Could not revoke that invite.")

	if len(page.invites.entries) != 0 {
		t.Errorf("the revoked invite is still held: %v", page.invites.entries)
	}
	if probe.inviteCalls != 2 {
		t.Errorf("%d requests in all, want the change to have asked again", probe.inviteCalls)
	}
}

// serverListProbe stands in for the controller: it counts the fetches and keeps
// each one's callback, so a test decides when an answer lands — or whether it
// lands at all.
type serverListProbe struct {
	inviteCalls int
	banCalls    int

	answerInvites func(invites []ServerInviteEntry, err error)
	answerBans    func(bans []domain.Ban, err error)
}

// newTestServerSettingsPage is a page whose hooks answer without a controller.
// Every one is filled in: a section is entitled to call any of them while it
// builds.
func newTestServerSettingsPage(probe *serverListProbe) *ServerSettingsPage {
	return NewServerSettingsPage(ServerSettingsHooks{
		Deps:    testDeps(),
		Close:   func() {},
		Confirm: func(Confirm) {},

		Server: func() (ServerSummary, bool) { return ServerSummary{ID: "srv", Name: "Test"}, true },
		Can:    func(domain.Permission) bool { return true },

		SetName:        func(string) {},
		SetDescription: func(string) {},
		ChangeIcon:     func() {},
		RemoveIcon:     func() {},
		ChangeBanner:   func() {},
		RemoveBanner:   func() {},

		Channels:      func() []ServerCategoryEntry { return nil },
		CreateChannel: func() {},
		EditChannel:   func(string) {},
		MoveChannel:   func(string, bool) {},

		CreateCategory: func() {},
		RenameCategory: func(string) {},
		MoveCategory:   func(string, bool) {},
		DeleteCategory: func(string) {},

		Roles:                 func() []ServerRoleEntry { return nil },
		CreateRole:            func() {},
		SetRoleName:           func(string, string) {},
		SetRoleColor:          func(string, string) {},
		SetRoleHoist:          func(string, bool) {},
		SetRolePermissions:    func(string, domain.Permission, domain.Permission) {},
		SetDefaultPermissions: func(domain.Permission) {},
		MoveRole:              func(string, bool) {},
		DeleteRole:            func(string) {},

		LoadInvites: func(onLoaded func([]ServerInviteEntry, error)) {
			probe.inviteCalls++
			probe.answerInvites = onLoaded
		},
		CopyInvite:   func(string) {},
		RevokeInvite: func(string, func(error)) {},
		OpenProfile:  func(string, fyne.CanvasObject) {},

		LoadBans: func(onLoaded func([]domain.Ban, error)) {
			probe.banCalls++
			probe.answerBans = onLoaded
		},
		LiftBan: func(string, func(error)) {},
	})
}
