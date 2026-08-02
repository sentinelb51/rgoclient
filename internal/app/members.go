package app

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// authorFetchDelay is how long author resolution waits for more authors before
// going to the network. Mounting a page calls ensureAuthor once per widget, so a
// short window turns that burst into one batch.
const authorFetchDelay = 50 * time.Millisecond

/* Lazy author resolution */

// ensureAuthor makes a message author renderable. Messages carry only the
// author's ID, so a user we haven't seen renders as a raw ID until resolved. This
// queues both gaps when missing from the store — the user (name, avatar) and, in
// a server channel, the member (nickname, role colour) — guarded by
// fetchedAuthors so each pair is queued at most once. The fetching itself happens
// a moment later in flushAuthors.
//
// This is the lazy counterpart to a bulk member fetch: Revolt's members endpoint
// has no pagination, so pulling every member of a large server floods memory.
//
// Call on the UI thread: it touches the maps without locking. The two store
// lookups are the reason HasUser and HasMember exist — it runs once per mounted
// message, so it must not allocate.
func (a *App) ensureAuthor(serverID, userID string) {
	if userID == "" {
		return
	}

	needUser := !a.store.HasUser(userID)
	needMember := serverID != "" && !a.store.HasMember(serverID, userID)

	key := serverID + ":" + userID
	if (!needUser && !needMember) || a.fetchedAuthors[key] {
		return
	}

	a.fetchedAuthors[key] = true
	a.pendingAuthors = append(a.pendingAuthors, client.AuthorRef{ServerID: serverID, UserID: userID})

	if a.authorTimer == nil {
		a.authorTimer = time.AfterFunc(authorFetchDelay, func() {
			a.doOnUI(a.flushAuthors, false)
		})
	}
}

// flushAuthors resolves everything ensureAuthor has queued and repaints once for
// the whole batch. Authors that fail lose their guard, so a later message can
// retry. Call on the UI thread.
//
// The repaint is deliberately one hop rather than one per author as it lands:
// each refresh scans every mounted widget, so a page of unseen people used to
// walk the column dozens of times and repaint after each — and the names still
// arrived one flicker at a time. They now settle together.
func (a *App) flushAuthors() {
	a.authorTimer = nil

	pending := a.pendingAuthors
	a.pendingAuthors = nil
	if len(pending) == 0 {
		return
	}
	epoch := a.epoch

	go func() {
		result := a.client.ResolveAuthors(pending)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			for _, ref := range result.Failed {
				delete(a.fetchedAuthors, ref.ServerID+":"+ref.UserID)
			}
			a.refreshAuthorMessages(result.Resolved...)

			// Only a member fetch changes the sidebar; pure user fetches leave it be.
			// Rebuilding it also re-derives the mention candidates, so the picker is
			// only refreshed separately when it doesn't — a resolved user is a new
			// candidate in a DM even when no member record was involved.
			if result.Member {
				a.refreshMemberList()
				return
			}
			a.refreshMentionCandidates()
		}, false)
	}()
}

// refreshAuthorMessages updates the mounted message widgets authored by any of
// userIDs in place — name, role colour, avatar — after a lazy fetch resolves,
// avoiding a full re-render of the open channel. A whole batch is passed at once
// so the column is scanned once rather than once per author.
func (a *App) refreshAuthorMessages(userIDs ...string) {
	authors := make(map[string]bool, len(userIDs))
	for _, userID := range userIDs {
		if userID != "" {
			authors[userID] = true
		}
	}
	if len(authors) == 0 {
		return
	}

	for _, obj := range a.messageList.Objects {
		if w, ok := obj.(*ui.MessageWidget); ok && authors[w.Author()] {
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
// Online and Offline sections. The store hands them back resolved and ordered by
// display name, and the mention candidates come off that same walk: they are the
// same people under the same names, and deriving them separately meant a second
// walk, a second round of name resolution and a second sort on every member
// event.
func (a *App) refreshMemberList() {
	a.memberList.Objects = nil
	if a.currentServerID == "" {
		a.memberList.Refresh()
		return
	}

	members := a.store.Members(a.currentServerID)

	deps := a.deps()
	a.addMemberSection("Online", members, true, deps)
	a.addMemberSection("Offline", members, false, deps)
	a.memberList.Refresh()

	a.setMentionCandidates(memberCandidates(members))
}

// addMemberSection appends a titled section holding the members whose presence
// matches, keeping the order they arrived in.
func (a *App) addMemberSection(title string, members []domain.Member, online bool, deps ui.Deps) {
	var count int
	for i := range members {
		if members[i].Online == online {
			count++
		}
	}
	if count == 0 {
		return
	}

	serverID := a.currentServerID
	a.memberList.Add(ui.NewMemberSection(fmt.Sprintf("%s — %d", title, count)))
	for i := range members {
		if members[i].Online != online {
			continue
		}

		userID := members[i].UserID
		w := ui.NewMemberWidget(deps, members[i], online)
		w.Menu = func() []*fyne.MenuItem { return a.memberMenu(serverID, userID) }
		a.memberList.Add(w)
	}
}

/* Mention candidates */

// refreshMentionCandidates hands the composer's @picker the people mentionable
// in the open channel. The picker snapshots the list and filters that snapshot
// on every keystroke, so this is where the (comparatively expensive) walk and
// name resolution happen — once per membership change, not once per key.
//
// Call on the UI thread, whenever the open channel or its membership changes.
func (a *App) refreshMentionCandidates() {
	a.setMentionCandidates(a.mentionCandidates())
}

// setMentionCandidates hands the picker a list somebody else has already
// resolved, which is how refreshMemberList shares its own walk with it.
func (a *App) setMentionCandidates(candidates []ui.MentionCandidate) {
	if a.input == nil {
		return
	}

	a.input.Mentions.SetCandidates(candidates)
}

// mentionCandidates resolves the mentionable people in the open channel from
// what the client already knows — no network, the same rule the member sidebar
// follows. In a server that means whoever the store knows: the gateway's members
// plus the ones lazy author resolution has pulled in (see ensureAuthor), which is
// exactly the set of people already visible in the channel. In a DM or group it
// is the channel's recipients.
func (a *App) mentionCandidates() []ui.MentionCandidate {
	if a.currentChannelID == "" {
		return nil
	}

	channel, ok := a.currentChannel()
	if !ok {
		return nil
	}
	if channel.ServerID != "" {
		return memberCandidates(a.store.Members(channel.ServerID))
	}

	candidates := make([]ui.MentionCandidate, 0, len(channel.Recipients))
	for _, userID := range channel.Recipients {
		user, ok := a.store.User(userID)
		if !ok {
			continue
		}
		candidates = append(candidates,
			ui.NewMentionCandidate(user.ID, user.Name, user.Username, user.AvatarURL, nil))
	}
	ui.SortCandidates(candidates)

	return candidates
}

// memberCandidates turns resolved members into mention candidates, which arrive
// already ordered. They carry the same nickname, per-server avatar and role
// colour the member sidebar shows, so the picker looks like the list the user is
// picking from. A member whose account the store hasn't resolved and who has no
// nickname either is dropped: there would be nothing to display or match against.
func memberCandidates(members []domain.Member) []ui.MentionCandidate {
	candidates := make([]ui.MentionCandidate, 0, len(members))

	for i := range members {
		member := &members[i]
		if member.Username == "" && member.Name == "Unknown user" {
			continue
		}
		candidates = append(candidates, ui.NewMentionCandidate(
			member.UserID, member.Name, member.Username, member.AvatarURL, member.Color,
		))
	}

	return candidates
}
