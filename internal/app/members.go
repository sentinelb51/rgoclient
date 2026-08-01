package app

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	// authorFetchDelay is how long author resolution waits for more authors
	// before going to the network. Mounting a page of messages calls ensureAuthor
	// once per widget, so a short window turns that burst into one batch.
	authorFetchDelay = 50 * time.Millisecond

	// authorFetchWorkers bounds how many authors are fetched at once, so a
	// channel full of unseen people doesn't open dozens of connections.
	authorFetchWorkers = 4
)

// author identifies one author to resolve: the user, plus the server whose
// member record carries their nickname and role colour ("" in a DM or group).
type author struct {
	serverID string
	userID   string
}

// ensureAuthor makes a message author renderable. Messages carry only the
// author's ID, so a user we haven't seen yet renders as "Message author: <id>"
// until we resolve them. This queues both gaps when missing from State: the user
// (name, avatar) and, in a server channel, the member (nickname, role colour).
// Each (server, user) pair is queued at most once — guarded by fetchedAuthors —
// and the actual fetching happens in flushAuthors a moment later.
//
// This is the lazy, per-author counterpart to a bulk member fetch: Revolt's
// members endpoint has no pagination, so pulling every member of a large server
// floods memory (2000+ members/users). We resolve authors as they appear instead.
//
// Call on the UI thread: it reads State and touches the pending/fetched maps
// without locking.
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
	a.pendingAuthors = append(a.pendingAuthors, author{serverID: serverID, userID: userID})

	if a.authorTimer == nil {
		a.authorTimer = time.AfterFunc(authorFetchDelay, func() {
			a.doOnUI(a.flushAuthors, false)
		})
	}
}

// flushAuthors resolves everything ensureAuthor has queued. Each author's own
// messages refresh in place as they land, so names fill in progressively, while
// the member sidebar — a full rebuild — is refreshed once for the whole batch.
// Authors that fail lose their fetchedAuthors guard so a later message can
// retry. Call on the UI thread.
func (a *App) flushAuthors() {
	a.authorTimer = nil
	pending, session := a.pendingAuthors, a.session
	a.pendingAuthors = nil
	if len(pending) == 0 || session == nil {
		return
	}

	go func() {
		var (
			mu     sync.Mutex
			failed []string // fetchedAuthors keys to release
			member bool     // a member record was fetched, so the sidebar changed
		)

		var wg sync.WaitGroup
		slots := make(chan struct{}, authorFetchWorkers)
		for _, target := range pending {
			wg.Add(1)
			slots <- struct{}{}
			go func() {
				defer func() { <-slots; wg.Done() }()

				ok, fetchedMember := resolveAuthor(session, target)
				mu.Lock()
				if !ok {
					failed = append(failed, target.serverID+":"+target.userID)
				}
				member = member || fetchedMember
				mu.Unlock()

				if ok {
					a.doOnUI(func() { a.refreshAuthorMessages(target.userID) }, false)
				}
			}()
		}
		wg.Wait()

		a.doOnUI(func() {
			for _, key := range failed {
				delete(a.fetchedAuthors, key)
			}
			// Only a member fetch changes the sidebar; pure user fetches (DMs, or
			// members already present) leave it untouched. The mention picker is
			// refreshed either way — a resolved user is a new candidate in a DM
			// even when no member record was involved.
			if member {
				a.refreshMemberList()
			}
			a.refreshMentionCandidates()
		}, false)
	}()
}

// resolveAuthor fetches the user and, in a server, the member record behind one
// author, pulling both into State. It reports whether the author is now
// resolvable and whether a member record was actually fetched.
func resolveAuthor(session *revoltgo.Session, target author) (ok, fetchedMember bool) {
	if session.State.User(target.userID) == nil {
		if _, err := session.User(target.userID); err != nil {
			log.Printf("fetch user %s: %v", target.userID, err)
			return false, false
		}
	}

	// If the user fetch failed we'd have returned already; a missing member is
	// only worth asking for in a server channel.
	if target.serverID == "" || session.State.Member(target.serverID, target.userID) != nil {
		return true, false
	}
	if _, err := session.ServerMember(target.serverID, target.userID); err != nil {
		log.Printf("fetch member %s in server %s: %v", target.userID, target.serverID, err)
		return false, false
	}
	return true, true
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

	a.refreshMemberList()
	return ui.NewFixedWidthContainer(theme.Sizes.MemberSidebarWidth, background, container.NewVScroll(a.memberList))
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
	a.refreshMentionCandidates()
}

// addMemberSection appends a titled section of member rows when non-empty,
// sorting members by display name (case-insensitive).
func (a *App) addMemberSection(title string, members []*revoltgo.ServerMember, online bool, deps ui.Deps) {
	if len(members) == 0 {
		return
	}

	// Sort on a precomputed key: resolving the display name inside the
	// comparator would re-hit State O(n log n) times on large servers.
	type entry struct {
		member *revoltgo.ServerMember
		key    string
	}
	entries := make([]entry, len(members))
	for i, member := range members {
		entries[i] = entry{member, strings.ToLower(util.MemberName(a.session, member))}
	}
	slices.SortFunc(entries, func(x, y entry) int { return strings.Compare(x.key, y.key) })

	a.memberList.Add(ui.NewMemberSection(fmt.Sprintf("%s — %d", title, len(members))))
	for _, e := range entries {
		a.memberList.Add(ui.NewMemberWidget(deps, e.member, online))
	}
}
