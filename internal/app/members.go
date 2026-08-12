package app

import (
	"errors"
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// memberFetchTimeout is how long the sidebar keeps saying it is loading a
// membership before it gives up and offers to ask again. It is a const rather
// than a setting because it is not a preference: what the user would be choosing
// is how long to look at a sweeping line before being told nothing came.
const memberFetchTimeout = 20 * time.Second

// authorFetchDelay is how long author resolution waits for more authors before
// going to the network. Mounting a page calls ensureAuthor once per widget, so a
// short window turns that burst into one batch.
func authorFetchDelay() time.Duration { return config.Current().Behaviour.AuthorFetchDelay() }

/* Lazy author resolution */

// authorKey is how a (server, user) pair is filed in fetchedAuthors. Both the
// guard and the release that drops it after a failure build one, and a pair that
// disagreed would leak a guard and stop that author ever being retried.
func authorKey(serverID, userID string) string { return serverID + ":" + userID }

// ensureAuthor makes a message author renderable. Messages carry only the
// author's ID, so a user we haven't seen renders as a raw ID until resolved. This
// queues both gaps when missing from the store — the user (name, avatar) and, in
// a server channel, the member (nickname, role colour) — guarded by
// fetchedAuthors so each pair is queued at most once. The fetching itself happens
// a moment later in flushAuthors.
//
// It stays useful alongside loadMembers, which pulls a whole server in one go:
// this covers a webhook's author, somebody who has since left, a server whose
// fetch failed or was turned off, and every conversation, none of which a
// membership fetch reaches. Once that fetch has landed the batch simply finds
// nothing to do.
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

	key := authorKey(serverID, userID)
	if (!needUser && !needMember) || a.fetchedAuthors[key] {
		return
	}

	a.fetchedAuthors[key] = true
	a.pendingAuthors = append(a.pendingAuthors, client.AuthorRef{ServerID: serverID, UserID: userID})

	if a.authorTimer == nil {
		a.authorTimer = time.AfterFunc(authorFetchDelay(), func() {
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
				delete(a.fetchedAuthors, authorKey(ref.ServerID, ref.UserID))
			}
			a.refreshAuthorMessages(result.Resolved...)
			a.refreshTyping()  // somebody the line could only count may now have a name
			a.refreshFriends() // and somebody the friends list could not name at all

			// In a server this is refreshMemberList's to redo, whether a member record
			// was fetched or only the account behind one: toMember fills a membership's
			// name and username from that account, and memberCandidates drops a member
			// it cannot name — so a resolved *user* is what can make an already-cached
			// membership mentionable at all. It rebuilds the sidebar and re-derives the
			// candidates off the same walk, off the UI thread.
			//
			// A conversation has no membership to rebuild, so it takes the cheap path.
			if a.currentServerID != "" {
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

	a.memberList = ui.NewMemberList(a.deps())

	// The menu is handed over once for the whole list rather than closed over per
	// row: the rows are recycled, so one capturing a member would offer to kick
	// whoever used to be drawn there.
	a.memberList.RowMenu = func(userID string) []*fyne.MenuItem {
		return a.memberMenu(a.currentServerID, userID)
	}

	// The member list is the one column with nothing to its right, so it carries
	// its seam on the left — and hiding the sidebar takes that seam with it,
	// leaving the message area flush against the window edge.
	a.memberSidebar = ui.NewFixedWidthContainer(theme.Sizes.MemberSidebarWidth, background,
		ui.NewFillRow(1, ui.NewColumnDivider(), a.memberList))

	if !config.Current().Interface.ShowMemberSidebar {
		a.memberSidebar.Hide()
	}
	a.refreshMemberList()

	return a.memberSidebar
}

// toggleMemberList shows or hides the member sidebar, handing its width to the
// message area.
//
// Hiding it is a real saving rather than only a visual one: a hidden list is not
// modelled at all — see refreshMemberList — which on a large server is the
// cheapest thing the user can do. Showing it again therefore has to rebuild what
// it stopped following.
func (a *App) toggleMemberList() {
	if a.memberSidebar == nil {
		return
	}

	if a.memberSidebar.Visible() {
		a.memberSidebar.Hide()
	} else {
		a.memberSidebar.Show()
		if a.memberStale {
			a.refreshMemberList()
		}
	}

	// Hiding the column tells nothing inside it, and the status mark is an
	// animation — see MemberList.SetSweeping.
	a.memberList.SetSweeping(a.memberSidebar.Visible())

	ui.Relayout(a.mainRow)
}

// refreshMemberList rebuilds the member list for the current server, and hands
// the composer's picker the mention candidates off the same walk: they are the
// same people under the same names, and deriving them separately meant a second
// walk, a second round of name resolution and a second sort per member event.
//
// The walk is the expensive half — Store.Members resolves a nickname, an avatar,
// a presence and a role colour per member and then sorts them — so it happens
// **off the UI thread**, and the model build goes with it. Only installing the
// result comes back.
//
// The candidates are handed over whatever the sidebar was asked to show:
// somebody hidden from the list is still somebody the composer can mention. A
// *hidden* sidebar therefore skips the model but never the walk, and records
// that it has stopped following so toggleMemberList can catch it up.
//
// Call on the UI thread.
func (a *App) refreshMemberList() {
	if a.memberList == nil {
		return
	}

	serverID := a.currentServerID
	if serverID == "" {
		a.memberStale = false
		a.memberList.Reset()
		a.setMentionCandidates(ui.MentionUser, nil)
		a.updateMemberStatus()

		return
	}

	visible := a.memberSidebar == nil || a.memberSidebar.Visible()
	a.memberStale = !visible

	// Two rebuilds can be in flight at once — an event landing beside a server
	// change — and the store they read moves under them, so the older one has
	// nothing useful to install.
	a.memberSeq++
	seq := a.memberSeq
	epoch := a.epoch
	options := memberListOptions()

	go func() {
		members := a.store.Members(serverID)
		candidates := memberCandidates(members)

		var entries []ui.MemberEntry
		if visible {
			entries = ui.NewMemberModel(members, a.store.HoistedRoles(serverID), options)
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.memberSeq != seq || a.currentServerID != serverID {
				return
			}

			a.setMentionCandidates(ui.MentionUser, candidates)
			if visible {
				a.memberList.SetModel(entries)
				a.updateMemberStatus()
			}
		}, false)
	}()
}

/* What the sidebar says when its rows cannot */

// updateMemberStatus decides the strip drawn over the list. It is computed from
// the state rather than written at each place that changes it: a fetch starting,
// finishing, timing out and a model landing can all reach it in either order, and
// four call sites each setting a message is four chances for the sidebar to be
// left claiming to be loading something that arrived.
//
// Call on the UI thread, after anything either half of it depends on has moved.
func (a *App) updateMemberStatus() {
	if a.memberList == nil {
		return
	}

	serverID := a.currentServerID
	status := memberStatusFor(serverID,
		serverID != "" && a.memberLoading == serverID, a.memberFailed[serverID], a.memberList.Empty())

	// The one thing the decision cannot make for itself: what a retry retries.
	if status.Action != "" {
		status.Retry = func() { a.retryMembers(serverID) }
	}

	a.memberList.SetStatus(status)
}

// memberStatusFor is the decision alone, taken apart from the widget it is
// installed on so it can be checked without one.
//
// Order is the whole of it. A fetch in flight outranks a previous failure —
// retrying is what put it in flight — and a failure outranks an empty list,
// because "nobody to show" for a membership that never arrived is a lie the user
// has no way to see through.
func memberStatusFor(serverID string, loading, failed, empty bool) ui.MemberListStatus {
	switch {
	case serverID == "":
		return ui.MemberListStatus{}
	case loading && empty:
		return ui.MemberListStatus{Text: "Loading members", Busy: true}
	case loading:
		return ui.MemberListStatus{Text: "Refreshing members", Busy: true}
	case failed:
		return ui.MemberListStatus{Text: "Couldn't load members.", Action: "Try again"}
	case empty:
		return ui.MemberListStatus{Text: "Nobody to show here."}
	}

	return ui.MemberListStatus{}
}

// retryMembers asks for a membership again after one failed or was given up on.
// Call on the UI thread.
func (a *App) retryMembers(serverID string) {
	delete(a.fetchedMembers, serverID)
	delete(a.memberFailed, serverID)

	a.loadMembers(serverID)
}

// armMemberWatchdog gives the sidebar an answer for a membership that never
// arrives.
//
// It does not cancel anything and cannot: revoltgo's REST layer takes no context,
// so a request that has stopped being waited for is still out. What the timeout
// buys is the sidebar no longer claiming to be loading something nothing is
// watching — and if the answer does land afterwards it is still installed, the
// fetch having been left alone.
func (a *App) armMemberWatchdog(serverID string) {
	a.stopMemberWatchdog()

	var watchdog *time.Timer
	watchdog = time.AfterFunc(memberFetchTimeout, func() {
		a.doOnUI(func() {
			// A fired timer cannot be recalled, so the wake checks it is still the
			// one the field holds rather than trusting that it was not replaced.
			if a.memberWatchdog != watchdog || a.memberLoading != serverID {
				return
			}

			a.memberWatchdog = nil
			a.memberLoading = ""
			a.memberFailed[serverID] = true
			log.Printf("fetch members of %s: no answer after %s", serverID, memberFetchTimeout)

			a.updateMemberStatus()
		}, false)
	})
	a.memberWatchdog = watchdog
}

func (a *App) stopMemberWatchdog() {
	if a.memberWatchdog == nil {
		return
	}

	a.memberWatchdog.Stop()
	a.memberWatchdog = nil
}

// memberListOptions is what the settings say the list should look like. Read per
// rebuild rather than held, so a change applies to the next one. Hoisting is
// only meaningful alongside the presence split — an ungrouped list has no
// sections to hoist into — and the model reads it that way.
func memberListOptions() ui.MemberListOptions {
	settings := config.Current().Behaviour

	return ui.MemberListOptions{
		GroupByPresence: settings.GroupByPresence,
		HoistRoles:      settings.HoistRoles,
		HideOffline:     settings.HideOfflineMembers,
		HideRoleless:    settings.HideRolelessMembers,
		FallbackToAll:   settings.MemberListFallback,
	}
}

// refreshMemberRow redraws one member in place, for a change that does not move
// them in the list. Somebody who is not mounted is a no-op: their row is built
// from the store when it scrolls into view.
func (a *App) refreshMemberRow(userID string) {
	if a.memberList == nil || a.currentServerID == "" {
		return
	}

	if member, ok := a.store.Member(a.currentServerID, userID); ok {
		a.memberList.RefreshMember(member)
	}
}

/* The full membership */

// loadMembers pulls a server's whole membership, once per server per session.
//
// Without it the list holds the gateway's members plus whoever lazy author
// resolution has pulled in — a fraction of a large server, under section counts
// that mean nothing. Revolt has no pagination and no member search, so it is one
// request for everybody or none at all; the setting is what turns it off.
//
// It also fills the *user* cache, which is what makes presence work: revoltgo
// silently drops an update for an account it has never heard of, so a member
// nobody has fetched can never come online.
//
// Paint-then-fill: the caller has already drawn what is known, so re-entering a
// server never blanks its list. Call on the UI thread.
func (a *App) loadMembers(serverID string) {
	if serverID == "" || a.fetchedMembers[serverID] || !config.Current().Behaviour.FetchAllMembers {
		a.updateMemberStatus()
		return
	}
	a.fetchedMembers[serverID] = true

	a.memberLoading = serverID
	a.armMemberWatchdog(serverID)
	a.updateMemberStatus()

	epoch := a.epoch
	go func() {
		err := a.client.FetchMembers(serverID)

		a.doOnUI(func() { a.finishMembers(epoch, serverID, err) }, false)
	}()
}

// finishMembers records how a membership fetch ended and lets the sidebar say
// so. Call on the UI thread.
func (a *App) finishMembers(epoch uint64, serverID string, err error) {
	if err != nil {
		// The guard goes with it, so re-entering the server tries again.
		delete(a.fetchedMembers, serverID)
		log.Printf("fetch members of %s: %v", serverID, err)
	}
	if a.stale(epoch) {
		return
	}

	// A second attempt made while the first is still out is not a failure and must
	// not be reported as one: the first request's own answer is still coming, and
	// it is what finishes this.
	if errors.Is(err, client.ErrBusy) {
		a.fetchedMembers[serverID] = true
		return
	}

	if a.memberLoading == serverID {
		a.memberLoading = ""
		a.stopMemberWatchdog()
	}

	if err != nil {
		a.memberFailed[serverID] = true
		a.updateMemberStatus()

		return
	}
	delete(a.memberFailed, serverID)

	if a.currentServerID != serverID {
		return
	}
	a.refreshMemberList()
}

/* Mention candidates */

// refreshMentionCandidates hands the composer's picker the people mentionable in
// a *conversation* — its own recipients, which is a list the channel already
// carries and so costs nothing to resolve here.
//
// A server's people are deliberately not this function's business. Store.Members
// resolves a nickname, an avatar, a presence and a role colour per member and
// then sorts them, which is far too much for the UI thread; refreshMemberList
// already makes that walk off-thread and pushes the result through
// setMentionCandidates. Every path that opens a server channel goes through
// enterServer first, so by the time a channel is selected the pool is already
// there — asking again here would walk a whole membership a second time, on the
// wrong thread, to arrive at what the picker is holding.
//
// Its channels are pushed separately again, by refreshChannelList: they change
// only when the sidebar itself is rebuilt.
//
// Call on the UI thread, whenever the open conversation or its recipients change.
func (a *App) refreshMentionCandidates() {
	channel, ok := a.currentChannel()
	if !ok || channel.ServerID != "" {
		return
	}

	a.setMentionCandidates(ui.MentionUser, recipientCandidates(a.store, channel))
}

// setMentionCandidates hands the picker a list somebody else has already
// resolved, which is how refreshMemberList and refreshChannelList share their
// own walks with it.
func (a *App) setMentionCandidates(kind ui.MentionKind, candidates []ui.MentionCandidate) {
	if a.input == nil {
		return
	}

	a.input.Mentions.SetCandidates(kind, candidates)
}

// recipientCandidates resolves the people mentionable in a conversation from
// what the client already knows — no network, the same rule the member sidebar
// follows. A conversation names its own participants, so unlike a server this is
// bounded by the channel itself and needs no membership walk.
//
// It takes the store rather than reading a.store so it stays a pure function of
// its arguments, which is what lets it be tested against a fake.
func recipientCandidates(store domain.Store, channel domain.Channel) []ui.MentionCandidate {
	candidates := make([]ui.MentionCandidate, 0, len(channel.Recipients))
	for _, userID := range channel.Recipients {
		user, ok := store.User(userID)
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
