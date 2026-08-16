package app

// The friends list — the one surface showing relationships as a set rather than
// one person at a time, opened from above the conversations because a
// relationship is a fact about somebody rather than about a server.
//
// Nothing is fetched: Revolt files each relationship on the account it is with,
// so the list is a walk of Store.Relationships and the gateway keeps it current.

import (
	"strings"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

/* Opening it */

// showFriends opens the friends dialog, or brings the open one up to date.
func (a *App) showFriends() {
	dialog := ui.NewFriendsDialog(a.deps(), a.OnUserTapped, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.friends = dialog // after showOverlay, which clears whatever was there

	a.refreshFriends()
}

// refreshFriends refills the open dialog and re-marks the sidebar row. Called
// from every side that can change a relationship, and returning early when the
// dialog is down — the row still has to follow, an incoming request being the one
// thing here that arrives unasked. Call on the UI thread.
func (a *App) refreshFriends() {
	sections := a.friendSections()

	if a.friendsRow != nil {
		a.friendsRow.SetPending(len(sections.incoming) > 0)
	}
	if a.friends == nil {
		return
	}

	a.friends.SetSections([]ui.FriendSection{
		{Title: "Incoming requests", Entries: sections.incoming, Awaiting: true},
		{Title: "Sent requests", Entries: sections.outgoing},
		{Title: "Friends", Entries: sections.friends},
		{Title: "Blocked", Entries: sections.blocked},
	})

	// The card is centred and sized from its own minimum, which a section gained or
	// lost changes; neither re-runs on its own.
	a.repositionOverlay()
}

// closeFriends forgets the dialog. Only closeOverlay calls it — the layer holds
// one thing at a time, so anything else opening takes this one down.
func (a *App) closeFriends() { a.friends = nil }

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

// friendEntry builds one row. The buttons come from the profile card's own
// policy, so the two can never offer different things about one person — minus
// what it draws disabled, which here would repeat the heading above the row.
func (a *App) friendEntry(user domain.User) ui.FriendEntry {
	name := friendName(user)

	profile := domain.Profile{
		UserID:       user.ID,
		Name:         name,
		Handle:       user.Handle,
		Relationship: user.Relationship,
		Bot:          user.Bot,
	}

	var buttons []ui.ProfileButton
	for _, button := range a.relationshipButtons(profile, a.refreshFriends) {
		if button.Do != nil {
			buttons = append(buttons, button)
		}
	}

	return ui.FriendEntry{
		UserID:    user.ID,
		Name:      name,
		Handle:    user.Handle,
		AvatarURL: user.AvatarURL,
		Buttons:   buttons,
	}
}

/* Keeping it current */

// friendsChanged follows a relationship change into this list. The account may be
// one State has never cached — EventUserRelationship carries the user and nothing
// files it — so it is queued as a message author would be, and flushAuthors
// refills the list once it lands. Call on the UI thread.
func (a *App) friendsChanged(userID string) {
	a.ensureAuthor("", userID)
	a.refreshFriends()
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
