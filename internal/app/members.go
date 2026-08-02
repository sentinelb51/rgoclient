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
	// authorFetchDelay is how long author resolution waits for more authors before
	// going to the network. Mounting a page calls ensureAuthor once per widget, so
	// a short window turns that burst into one batch.
	authorFetchDelay = 50 * time.Millisecond

	// authorFetchWorkers bounds how many authors are fetched at once, so a channel
	// full of unseen people doesn't open dozens of connections.
	authorFetchWorkers = 4
)

// author identifies one author to resolve: the user, plus the server whose member
// record carries their nickname and role colour ("" in a DM or group).
type author struct {
	serverID string
	userID   string
}

/* Lazy author resolution */

// ensureAuthor makes a message author renderable. Messages carry only the
// author's ID, so a user we haven't seen renders as a raw ID until resolved. This
// queues both gaps when missing from State — the user (name, avatar) and, in a
// server channel, the member (nickname, role colour) — guarded by fetchedAuthors
// so each pair is queued at most once. The fetching itself happens a moment later
// in flushAuthors.
//
// This is the lazy counterpart to a bulk member fetch: Revolt's members endpoint
// has no pagination, so pulling every member of a large server floods memory.
//
// Call on the UI thread: it reads State and touches the maps without locking.
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
// Authors that fail lose their guard, so a later message can retry. Call on the
// UI thread.
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
			wg     sync.WaitGroup
		)

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
			// Only a member fetch changes the sidebar; pure user fetches leave it be.
			// The mention picker is refreshed either way — a resolved user is a new
			// candidate in a DM even when no member record was involved.
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

	// A missing member is only worth asking for in a server channel.
	if target.serverID == "" || session.State.Member(target.serverID, target.userID) != nil {
		return true, false
	}

	if _, err := session.ServerMember(target.serverID, target.userID); err != nil {
		log.Printf("fetch member %s in server %s: %v", target.userID, target.serverID, err)
		return false, false
	}

	return true, true
}

// refreshAuthorMessages updates the mounted message widgets authored by userID in
// place — name, role colour, avatar — after a lazy fetch resolves, avoiding a
// full re-render of the open channel.
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

/* Member sidebar */

// buildMemberList builds the right-hand member sidebar.
func (a *App) buildMemberList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MemberListBackground)

	a.refreshMemberList()
	a.memberSidebar = ui.NewFixedWidthContainer(theme.Sizes.MemberSidebarWidth,
		background, container.NewVScroll(a.memberList))

	return a.memberSidebar
}

// toggleMemberList shows or hides the member sidebar, handing its width to the
// message area. Rows keep being rebuilt while it is hidden, so re-showing it
// costs nothing beyond the layout.
func (a *App) toggleMemberList() {
	if a.memberSidebar == nil {
		return
	}

	if a.memberSidebar.Visible() {
		a.memberSidebar.Hide()
	} else {
		a.memberSidebar.Show()
	}

	ui.Relayout(a.mainRow)
}

// refreshMemberList rebuilds the member rows for the current server, grouped into
// Online and Offline sections and sorted by display name.
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
// sorting by display name.
func (a *App) addMemberSection(title string, members []*revoltgo.ServerMember, online bool, deps ui.Deps) {
	if len(members) == 0 {
		return
	}

	// Sort on a precomputed key: resolving the display name inside the comparator
	// would re-hit State O(n log n) times on large servers.
	type entry struct {
		member *revoltgo.ServerMember
		key    string
	}

	entries := make([]entry, len(members))
	for i, member := range members {
		entries[i] = entry{member, strings.ToLower(util.MemberName(a.session, member))}
	}
	slices.SortFunc(entries, func(x, y entry) int { return strings.Compare(x.key, y.key) })

	serverID := a.currentServerID
	a.memberList.Add(ui.NewMemberSection(fmt.Sprintf("%s — %d", title, len(members))))
	for _, e := range entries {
		w := ui.NewMemberWidget(deps, e.member, online)
		userID := e.member.ID.User
		w.Menu = func() []*fyne.MenuItem { return a.memberMenu(serverID, userID) }
		a.memberList.Add(w)
	}
}

/* Mention candidates */

// refreshMentionCandidates hands the composer's @picker the people mentionable
// in the open channel. The picker snapshots the list and filters that snapshot
// on every keystroke, so this is where the (comparatively expensive) State walk
// and name resolution happen — once per membership change, not once per key.
//
// Call on the UI thread, whenever the open channel or its membership changes.
func (a *App) refreshMentionCandidates() {
	if a.input == nil {
		return
	}

	a.input.Mentions.SetCandidates(a.mentionCandidates())
}

// mentionCandidates resolves the mentionable people in the open channel from
// State alone — no network, the same rule the member sidebar follows. In a
// server that means whoever State knows: the gateway's members plus the ones
// lazy author resolution has pulled in (see ensureAuthor), which is exactly the
// set of people already visible in the channel. In a DM or group it is the
// channel's recipients.
func (a *App) mentionCandidates() []ui.MentionCandidate {
	if a.session == nil || a.currentChannelID == "" {
		return nil
	}

	if serverID := a.channelServerID(a.currentChannelID); serverID != "" {
		members := a.session.State.Members(serverID)
		candidates := make([]ui.MentionCandidate, 0, len(members))
		for _, member := range members {
			if candidate, ok := a.memberCandidate(member); ok {
				candidates = append(candidates, candidate)
			}
		}

		return sortCandidates(candidates)
	}

	channel := a.currentChannel()
	if channel == nil {
		return nil
	}

	candidates := make([]ui.MentionCandidate, 0, len(channel.Recipients))
	for _, userID := range channel.Recipients {
		if candidate, ok := a.userCandidate(userID); ok {
			candidates = append(candidates, candidate)
		}
	}

	return sortCandidates(candidates)
}

// memberCandidate builds a candidate from a server member, carrying the same
// nickname, per-server avatar and role colour the member sidebar shows, so the
// picker looks like the list the user is picking from. It reports false for a
// member whose user State hasn't resolved and who has no nickname either —
// there would be nothing to display or match against.
func (a *App) memberCandidate(member *revoltgo.ServerMember) (ui.MentionCandidate, bool) {
	userID := member.ID.User
	user := a.session.State.User(userID)
	if user == nil && (member.Nickname == nil || *member.Nickname == "") {
		return ui.MentionCandidate{}, false
	}

	var username string
	if user != nil {
		username = user.Username
	}

	return ui.NewMentionCandidate(
		userID,
		util.MemberName(a.session, member),
		username,
		util.MemberAvatarURL(a.session, member),
		util.MemberColor(a.session, member),
	), true
}

// userCandidate builds a candidate for a DM or group recipient, who has no
// member record and so no nickname or role colour.
func (a *App) userCandidate(userID string) (ui.MentionCandidate, bool) {
	user := a.session.State.User(userID)
	if user == nil {
		return ui.MentionCandidate{}, false
	}

	return ui.NewMentionCandidate(
		userID,
		util.UserName(a.session, userID),
		user.Username,
		user.AvatarURL("256"),
		nil,
	), true
}

// sortCandidates orders the list by display name, case-insensitively. State
// hands members back in map order, so without this the picker's suggestions
// would shuffle every time the list was rebuilt.
func sortCandidates(candidates []ui.MentionCandidate) []ui.MentionCandidate {
	// Sort on a precomputed key: lowering the name inside the comparator would
	// redo that work O(n log n) times on a large server.
	type entry struct {
		candidate ui.MentionCandidate
		key       string
	}
	entries := make([]entry, len(candidates))
	for i, candidate := range candidates {
		entries[i] = entry{candidate, strings.ToLower(candidate.Name)}
	}
	slices.SortFunc(entries, func(x, y entry) int { return strings.Compare(x.key, y.key) })

	for i, e := range entries {
		candidates[i] = e.candidate
	}

	return candidates
}
