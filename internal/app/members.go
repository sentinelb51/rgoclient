package app

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// ensureAuthor makes a message author renderable. A gateway message carries only
// the author's ID, so a user we haven't seen yet renders as "Unknown user" until
// we resolve them. This fills both gaps when missing from State: the user (name,
// avatar) and, in a server channel, the member (nickname, role colour). Each
// (server, user) pair is fetched at most once — guarded by fetchedAuthors — and
// the network runs off the UI thread, refreshing the member sidebar and the open
// channel once the data lands. The guard is cleared on failure so a later message
// from the same author can retry.
//
// This is the lazy, per-author counterpart to a bulk member fetch: Revolt's
// members endpoint has no pagination, so pulling every member of a large server
// floods memory (2000+ members/users). We resolve authors as they speak instead.
//
// Call on the UI thread: it reads State and touches fetchedAuthors without locking.
func (a *App) ensureAuthor(serverID, userID string) {
	if a.session == nil || userID == "" {
		return
	}

	needUser := a.session.State.User(userID) == nil
	needMember := serverID != "" && a.session.State.Member(serverID, userID) == nil
	key := serverID + ":" + userID
	if (!needUser && !needMember) || a.fetchedAuthors[key] {
		return
	}
	a.fetchedAuthors[key] = true

	session := a.session
	go func() {
		ok := true
		if needUser {
			if _, err := session.User(userID); err != nil {
				log.Printf("fetch user %s: %v", userID, err)
				ok = false
			}
		}
		if needMember {
			if _, err := session.ServerMember(serverID, userID); err != nil {
				log.Printf("fetch member %s in server %s: %v", userID, serverID, err)
				ok = false
			}
		}
		a.doOnUI(func() {
			if !ok {
				delete(a.fetchedAuthors, key)
			}
			// Only the member fetch changes the sidebar; a pure user fetch (DM, or
			// a member already present) leaves it untouched.
			if needMember && serverID == a.currentServerID {
				a.refreshMemberList()
			}
			a.refreshAuthorMessages(userID)
		}, false)
	}()
}

// refreshAuthorMessages updates the mounted message widgets authored by userID
// in place — name, role colour, avatar — after a lazy author fetch resolves,
// avoiding a full re-render of the open channel.
func (a *App) refreshAuthorMessages(userID string) {
	if userID == "" {
		return
	}
	for _, obj := range a.messageList.Objects {
		if w, ok := obj.(*ui.MessageWidget); ok && w.Author() == userID {
			w.RefreshAuthor()
		}
	}
}

// buildMemberList builds the right-hand member sidebar.
func (a *App) buildMemberList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MemberListBackground)
	background.SetMinSize(fyne.NewSize(theme.Sizes.MemberSidebarWidth, 0))

	a.refreshMemberList()
	return container.NewStack(background, container.NewVScroll(a.memberList))
}

// refreshMemberList rebuilds the member rows for the current server, grouped
// into Online and Offline sections and sorted by display name.
func (a *App) refreshMemberList() {
	a.memberList.Objects = nil
	if a.session == nil || a.currentServerID == "" {
		a.memberList.Refresh()
		return
	}

	var online, offline []*revoltgo.ServerMember
	for _, member := range a.session.State.Members(a.currentServerID) {
		if util.MemberOnline(a.session, member) {
			online = append(online, member)
		} else {
			offline = append(offline, member)
		}
	}

	deps := a.deps()
	a.addMemberSection("Online", online, true, deps)
	a.addMemberSection("Offline", offline, false, deps)
	a.memberList.Refresh()
}

// addMemberSection appends a titled section of member rows when non-empty,
// sorting members by display name (case-insensitive).
func (a *App) addMemberSection(title string, members []*revoltgo.ServerMember, online bool, deps ui.Deps) {
	if len(members) == 0 {
		return
	}

	sort.Slice(members, func(i, j int) bool {
		return strings.ToLower(util.MemberName(a.session, members[i])) <
			strings.ToLower(util.MemberName(a.session, members[j]))
	})

	a.memberList.Add(ui.NewMemberSection(fmt.Sprintf("%s — %d", title, len(members))))
	for _, member := range members {
		a.memberList.Add(ui.NewMemberWidget(deps, member, online))
	}
}
