package app

// The friends list — the one surface showing relationships as a set rather than
// one person at a time, opened from above the conversations because a
// relationship is a fact about somebody rather than about a server.
//
// Nothing is fetched: Revolt files each relationship on the account it is with,
// so the list is a walk of Store.Relationships and the gateway keeps it current.
//
// It is a *view*, not an overlay: it stands where a channel's messages would,
// and opening it deselects the channel exactly as picking another one would. The
// modal layer holds one thing at a time and takes the keyboard with it, which is
// wrong for a surface a reader stays on — and four sections of people with what
// can be done about each is a page rather than an answer to a question.

import (
	"errors"
	"log"
	"strings"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

/* Opening it */

// The line under each caption, saying what the section is for. Only the two that
// are not self-evident carry one: "Friends" and "Blocked" say themselves, and a
// sentence under either would be the caption again in more words. Both are kept
// short enough to read whole in a narrow window — the page stands where the
// messages do, so its column is whatever is left between the sidebars, and the
// line shortens rather than wrapping.
const (
	incomingDetail = "Declining is not final — they can ask again."
	outgoingDetail = "Withdrawing one is not something they are told."
)

// showFriendsPage puts the friends list where the messages go. The channel is
// deselected first, the page standing in the same slot: a sidebar marking both
// would be claiming two views are open. Call on the UI thread.
func (a *App) showFriendsPage() {
	if a.friendsOpen || a.friendsPage == nil {
		return
	}

	// Before the flag, not after: clearChannelSelection puts the message column
	// back, which is the very thing about to be hidden.
	a.clearChannelSelection()

	a.friendsOpen = true
	a.messageColumn.Hide()
	a.friendsPage.Show()

	a.syncChannelList()
	a.refreshFriends()
}

// leaveFriendsPage puts the message column back, reporting whether it had to.
// Every path that selects a channel goes through clearChannelSelection or calls
// this itself, the page and a channel being the same slot. Call on the UI thread.
func (a *App) leaveFriendsPage() bool {
	if !a.friendsOpen {
		return false
	}

	a.friendsOpen = false
	a.friendsPage.Hide()
	a.messageColumn.Show()

	return true
}

// restoreFriendsPage puts the page back on screen after a restyle, which hands
// back a fresh widget tree: the page in it starts hidden and the message column
// visible, where the flag saying which of the two is the open view outlived the
// rebuild. Call on the UI thread.
func (a *App) restoreFriendsPage() {
	if !a.friendsOpen || a.friendsPage == nil {
		return
	}

	a.messageColumn.Hide()
	a.friendsPage.Show()
	a.refreshFriends()
}

// refreshFriends refills the page and re-marks the sidebar row. Called from
// every side that can change a relationship, and returning early when the page is
// down — the row still has to follow, an incoming request being the one thing
// here that arrives unasked. Call on the UI thread.
func (a *App) refreshFriends() {
	// With the page down the row's mark is the whole of what is at stake, and every
	// path that can change a relationship reaches here — flushAuthors among them,
	// once per batch of resolved authors. Building four sections of rows and their
	// buttons to ask whether one of them is empty is the whole list's cost for one
	// boolean.
	if !a.friendsOpen || a.friendsPage == nil {
		a.syncFriendsRow(a.awaitingAnswer())

		return
	}

	sections := a.friendSections()
	a.syncFriendsRow(len(sections.incoming) > 0)

	// Blocked starts shut and the rest open: it is the one section nobody opens this
	// page to read, and the one that only ever grows. Every heading folds, so a
	// hundred sent requests nobody ever cleaned up need not stand between the reader
	// and their friends — what the reader shuts is remembered for as long as the
	// page lives.
	a.friendsPage.SetSections([]ui.FriendSection{
		{Title: "Incoming requests", Detail: incomingDetail, Entries: sections.incoming},
		{Title: "Sent requests", Detail: outgoingDetail, Entries: sections.outgoing},
		{Title: "Friends", Entries: sections.friends},
		{Title: "Blocked", Entries: sections.blocked, Folded: true},
	})
}

// syncFriendsRow paints the sidebar row: which view is open, and whether anybody
// is waiting on an answer. Both at once, the row drawing them against each other
// as a channel row draws selection against unread.
func (a *App) syncFriendsRow(pending bool) {
	if a.friendsRow != nil {
		a.friendsRow.SetState(a.friendsOpen, pending)
	}
}

// awaitingAnswer reports whether anybody is waiting on this account to answer a
// friend request, which is all the sidebar row draws.
func (a *App) awaitingAnswer() bool {
	return a.store.HasIncomingRequest()
}

/* Asking somebody new */

// askFriend sends a request to a typed handle. It is the one way to reach an
// account this client has never drawn: every other route to a person is a
// surface they appear on — their message, their member row — and a stranger
// appears on none of them.
//
// done reports back so the field is cleared by what took and kept by what did
// not: a handle refused over a typo is one to correct rather than retype. The
// request itself reaches this list through the gateway, which names the account
// nothing here has: Store.Relationships is a walk of the *cached* users, so what
// files a stranger is UserRelationship's own handler.
func (a *App) askFriend(handle string, done func(sent bool)) {
	epoch := a.epoch

	go func() {
		err := a.client.AddFriendByHandle(handle)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			if err != nil {
				log.Printf("ask %s to be friends: %v", handle, err)
				a.notify(ui.ToneDanger, "%s", askFriendFailure(err))
				done(false)

				return
			}

			a.notify(ui.ToneInfo, "Asked %s to be friends.", handle)
			done(true)
		}, false)
	}()
}

// askFriendFailure is what a refusal says. Only the shape of the handle is the
// client's own to answer for; the rest are one status code with no sentence in
// it, so the notice names the two ordinary reasons rather than guessing between
// them.
func askFriendFailure(err error) string {
	if errors.Is(err, client.ErrHandleMalformed) {
		return "A handle is a name and a number, like name#0000."
	}

	return "Nobody found by that handle, or you have asked them already."
}

/* What goes in it */

// friendGroups is the list split the way it is drawn. Requests come first
// because they are the only part somebody is waiting on an answer to.
type friendGroups struct {
	incoming []ui.FriendEntry
	outgoing []ui.FriendEntry
	friends  []ui.FriendEntry
	blocked  []ui.FriendEntry
}

// friendSections resolves the whole list. Store.Relationships has already
// ordered it, so each group keeps that order.
func (a *App) friendSections() friendGroups {
	var groups friendGroups

	for _, user := range a.store.Relationships() {
		entry := a.friendEntry(user)

		switch user.Relationship {
		case domain.RelationshipIncoming:
			groups.incoming = append(groups.incoming, entry)
		case domain.RelationshipOutgoing:
			groups.outgoing = append(groups.outgoing, entry)
		case domain.RelationshipFriend:
			groups.friends = append(groups.friends, entry)
		case domain.RelationshipBlocked, domain.RelationshipBlockedBy:
			groups.blocked = append(groups.blocked, entry)
		}
	}

	return groups
}

// friendEntry builds one row. What applies to somebody comes from the profile
// card's own policy, so the two can never offer different things about one
// person; the row only decides how each is *drawn*. Two are drawn as nothing: a
// disabled button, which here would repeat the heading above the row, and Message,
// which becomes the card itself.
func (a *App) friendEntry(user domain.User) ui.FriendEntry {
	name := friendName(user)

	profile := domain.Profile{
		UserID:       user.ID,
		Name:         name,
		Handle:       user.Handle,
		Relationship: user.Relationship,
		Bot:          user.Bot,
	}

	// Writing to somebody is taken *out* of the buttons and made the card's own tap:
	// it is the one thing done from a friends list often, and a target for it beside
	// two that end a relationship is a hand aiming at the wrong square. Revolt opens
	// a conversation only between friends, so most rows have none and their card
	// falls back to the profile — which is the whole of what there is to do about a
	// request or a block anyway.
	var open func()

	var buttons []ui.ProfileButton
	for _, button := range a.relationshipButtons(profile, a.refreshFriends) {
		if button.Do == nil {
			continue
		}
		if button.Action == ui.ProfileActionMessage {
			open = button.Do
			continue
		}

		buttons = append(buttons, button)
	}

	return ui.FriendEntry{
		UserID:    user.ID,
		Name:      name,
		Handle:    user.Handle,
		AvatarURL: user.AvatarURL,
		Presence:  user.Presence,
		Buttons:   buttons,
		Open:      open,
	}
}

/* Keeping it current */

// resolveRelated fetches the accounts Ready named a relationship with but did
// not send. Revolt states the graph on the account's own record and sends the
// people in it only where something else already needed them, so somebody
// befriended long ago and not spoken to since is a relationship with no account
// behind it — and this list, which is a walk of the cached accounts, has no row
// to draw for one.
//
// Queued as a message author is, so it costs one batched fetch rather than a
// request each, and flushAuthors refills the list as they land. Call on the UI
// thread.
func (a *App) resolveRelated(userIDs []string) {
	for _, userID := range userIDs {
		if a.store.HasUser(userID) {
			continue
		}

		a.ensureAuthor("", userID)
	}
}

// friendsChanged follows a relationship change into this list. The account may be
// one State has never cached — EventUserRelationship carries the user and nothing
// files it — so it is queued as a message author would be, and flushAuthors
// refills the list once it lands. Call on the UI thread.
func (a *App) friendsChanged(userID string) {
	a.ensureAuthor("", userID)
	a.refreshFriends()
}

// friendsFollowPresence queues a rebuild when somebody on the open page comes or
// goes. The member sidebar's own handler cannot cover it: this page is the one
// surface open while no server is, which is the first thing that handler drops.
// Asking the store is a lookup rather than the walk the list itself is, so a
// server full of strangers changing state costs nothing here. Call on the UI
// thread.
func (a *App) friendsFollowPresence(userID string) {
	if !a.friendsOpen {
		return
	}

	if user, ok := a.store.User(userID); ok && user.Relationship.Known() {
		a.queueRefresh(refreshFriends)
	}
}

// friendName is what a row is filed under. A list is read by scanning, so an
// unnameable account falls back to its handle rather than the "Unknown user" a
// profile shows — a column of identical names says less than one of handles.
func friendName(user domain.User) string {
	if name := strings.TrimSpace(user.Name); name != "" {
		return name
	}
	if user.Handle != "" {
		return user.Handle
	}

	return user.ID
}
