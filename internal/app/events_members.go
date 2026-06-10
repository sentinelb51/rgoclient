package app

import (
	"github.com/sentinelb51/revoltgo"
)

// The session's default handlers already keep State's member cache in sync for
// these events; the handlers below only refresh the member sidebar when the
// change concerns the server currently in view.

// onServerMemberJoin refreshes the member list when someone joins the open
// server.
func (a *App) onServerMemberJoin(_ *revoltgo.Session, event *revoltgo.EventServerMemberJoin) {
	a.refreshMembersFor(event.ID)
}

// onServerMemberLeave refreshes the member list when someone leaves the open
// server.
func (a *App) onServerMemberLeave(_ *revoltgo.Session, event *revoltgo.EventServerMemberLeave) {
	a.refreshMembersFor(event.ID)
}

// onServerMemberUpdate refreshes the member list when a member of the open
// server changes (nickname, avatar, roles…) and updates that author's mounted
// messages in place so their name, role colour, and avatar stay current.
func (a *App) onServerMemberUpdate(_ *revoltgo.Session, event *revoltgo.EventServerMemberUpdate) {
	a.doOnUI(func() {
		if a.currentServerID == event.ID.Server {
			a.refreshMemberList()
		}
		a.refreshAuthorMessages(event.ID.User)
	}, false)
}

// refreshMembersFor refreshes the member sidebar on the UI thread, but only when
// serverID is the server currently in view.
func (a *App) refreshMembersFor(serverID string) {
	a.doOnUI(func() {
		if a.currentServerID == serverID {
			a.refreshMemberList()
		}
	}, false)
}
