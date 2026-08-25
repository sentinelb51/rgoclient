package app

import (
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"RGOClient/assets"
	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// memberFetchTimeout is how long the sidebar says it is loading before it gives
// up and offers to ask again. A const rather than a setting: what would be chosen
// is how long to watch a sweeping line before being told nothing came.
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

// ensureAuthor makes a message author renderable. A message carries only the
// author's ID, so an unseen user renders as a raw one. This queues both gaps —
// the user (name, avatar) and, in a server channel, the member (nickname, role
// colour) — guarded by fetchedAuthors so each pair is queued once; flushAuthors
// does the fetching a moment later.
//
// It stays useful alongside loadMembers: this covers a webhook's author, somebody
// since departed, a server whose fetch failed or was turned off, and every
// conversation. Once that fetch lands the batch simply finds nothing to do.
//
// Call on the UI thread: it touches the maps unlocked. HasUser and HasMember
// exist for this — it runs once per mounted message, so it must not allocate.
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

// flushAuthors resolves everything ensureAuthor queued and repaints once for the
// whole batch. Failures lose their guard, so a later message retries. One hop
// rather than one per author: each refresh scans every mounted widget, so a page
// of unseen people used to walk the column dozens of times and still arrive one
// flicker at a time. Call on the UI thread.
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

			// Releasing those guards is the whole of a batch that named nobody: none
			// of the repaints below can draw anything they did not draw before.
			if len(result.Resolved) == 0 {
				return
			}

			a.refreshAuthorMessages(result.Resolved...)
			a.refreshTyping()  // somebody the line could only count may now have a name
			a.refreshFriends() // and somebody the friends list could not name at all

			// Whichever people the sidebar is drawing, a name is what it drops them
			// for: toMember fills a membership's from the account behind it, and both
			// candidate walks skip whoever they cannot name — so a resolved *user* can
			// make an already-cached membership mentionable. refreshMemberList is the
			// one entry to both, falling through to refreshRecipients outside a server.
			a.queueRefresh(refreshMembers)
		}, false)
	}()
}

// refreshAuthorMessages updates the mounted widgets authored by any of userIDs in
// place — name, role colour, avatar — rather than re-rendering the channel. A
// batch at a time, so the column is scanned once rather than once per author.
//
// Membership is a scan rather than a map: a batch is bounded by the mounted
// window and most calls carry a single ID, which an event delivers often enough
// that the map cost more to build than the compares it saved.
func (a *App) refreshAuthorMessages(userIDs ...string) {
	if len(userIDs) == 0 || a.messages == nil {
		return
	}

	a.messages.EachMounted(func(w *ui.MessageWidget) {
		// A system message that targets nobody has no author, and an empty ID in the
		// batch would otherwise match every one of them.
		if author := w.Author(); author != "" && slices.Contains(userIDs, author) {
			w.RefreshAuthor()
		}
	})
}

/* Member sidebar */

// buildMemberList builds the right-hand member sidebar.
func (a *App) buildMemberList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MemberListBackground)

	a.memberList = ui.NewMemberList(a.deps())

	// Handed over once for the whole list rather than closed over per row: the rows
	// are recycled, so one capturing a member would offer to kick whoever used to be
	// drawn there.
	a.memberList.RowMenu = func(userID string) []*fyne.MenuItem {
		if a.currentServerID == "" {
			return a.groupMemberMenu(a.currentChannelID, userID)
		}

		return a.memberMenu(a.currentServerID, userID)
	}

	// The one column with nothing to its right, so it carries its seam on the left —
	// and hiding it takes the seam too, leaving the message area flush to the window.
	a.memberSidebar = ui.NewFixedWidthContainer(theme.Sizes.MemberSidebarWidth, background,
		ui.NewFillRow(1, ui.NewColumnDivider(), a.memberList))

	if !config.Current().Interface.ShowMemberSidebar {
		a.memberSidebar.Hide()
	}
	a.refreshMemberList()

	return a.memberSidebar
}

// toggleMemberList shows or hides the member sidebar, handing its width to the
// message area. Hiding is a real saving rather than a visual one: a hidden list
// is not modelled at all (see refreshMemberList), which on a large server is the
// cheapest thing the user can do — so showing it rebuilds what it stopped
// following.
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

// refreshMemberList rebuilds the member list and hands the picker its mention
// candidates off the same walk — the same people under the same names, where
// deriving them separately meant a second walk, a second round of resolution and
// a second sort per member event.
//
// The walk is the expensive half — Store.Members resolves a nickname, avatar,
// presence and role colour per member, then sorts — so it happens **off the UI
// thread** along with the model build. Only installing the result comes back.
//
// Candidates are handed over whatever the sidebar shows: somebody hidden from the
// list is still mentionable. A hidden sidebar therefore skips the model but never
// the walk, and records that it stopped following. Call on the UI thread.
func (a *App) refreshMemberList() {
	if a.memberList == nil {
		return
	}

	serverID := a.currentServerID
	if serverID == "" {
		a.memberStale = false
		a.dropMemberCache()
		a.refreshRecipients()

		return
	}

	// A walk already in flight is this same membership being resolved, and a second
	// would take the claim the first still holds — memberRebuilt releases on
	// whichever lands, so a presence patch would wait on a claim nobody will drop.
	// The deferral is the queue's own bit rather than a flag of its own, which is
	// what stops one outliving the walk it waited on: flushRefresh clears it by
	// running this again. The window is deliberately not armed here — a flush
	// landing while the claim is held would only defer it a second time — so
	// memberRebuilt is what arms it.
	if a.memberWorking {
		a.dirty |= refreshMembers
		return
	}

	visible := a.memberSidebar == nil || a.memberSidebar.Visible()
	a.memberStale = !visible

	// The claim above is what keeps two walks from overlapping; the counter is what
	// a landing is checked against, so an answer is still dropped rather than
	// installed if a path ever starts a rebuild without taking the claim.
	a.memberSeq++
	seq := a.memberSeq
	epoch := a.epoch
	options := a.memberListOptions()

	// The walk resolves everybody's presence on the way past, so whatever is
	// waiting to be patched is about to be answered by this instead.
	a.presenceDirty = nil
	a.memberWorking = true

	go func() {
		members := a.store.Members(serverID)
		candidates := memberCandidates(members)

		var entries []ui.MemberEntry
		if visible {
			entries = ui.NewMemberModel(members, a.store.HoistedRoles(serverID), options)
		}

		a.doOnUI(func() {
			a.memberRebuilt()
			if a.stale(epoch) || a.memberSeq != seq || a.currentServerID != serverID {
				return
			}

			a.memberCache, a.memberCacheServer = members, serverID

			a.setMentionCandidates(ui.MentionUser, candidates)
			if visible {
				a.memberList.SetModel(entries)
				a.updateMemberStatus()
			}
		}, false)
	}()
}

// refreshMemberPresence redraws the sidebar for the people who came or went,
// without asking the store for anybody else. Presence moves a member between the
// list's sections and changes nothing they are ordered by, so the membership the
// last walk resolved still stands: this copies it, re-resolves only who moved and
// hands the copy to the model.
//
// That copy is what keeps it off the UI thread and free of a lock. The published
// membership is never written into, so a worker reading one an older flush
// published is reading something nothing will change under it — which is also why
// two of these must not overlap, the second having started from the first's
// source rather than from its answer. Call on the UI thread.
func (a *App) refreshMemberPresence() {
	changed := a.presenceDirty
	a.presenceDirty = nil

	if a.memberList == nil || len(changed) == 0 {
		return
	}

	serverID := a.currentServerID
	if serverID == "" || serverID != a.memberCacheServer || a.memberCache == nil {
		a.refreshMemberList() // nothing resolved to patch
		return
	}

	// A rebuild in flight is about to publish a membership this would have copied
	// the previous one of. Its own landing re-queues these.
	if a.memberWorking {
		a.presenceDirty = changed
		return
	}

	// Nothing on screen and nothing to feed: the picker takes its candidates from
	// a member's name, which is not what moved.
	if a.memberSidebar != nil && !a.memberSidebar.Visible() {
		a.memberStale = true
		return
	}

	previous := a.memberCache
	hoisted := a.store.HoistedRoles(serverID)
	options := a.memberListOptions()

	a.memberSeq++
	seq := a.memberSeq
	epoch := a.epoch
	a.memberWorking = true

	go func() {
		members := patchedMembers(a.store, serverID, previous, changed)
		entries := ui.NewMemberModel(members, hoisted, options)

		a.doOnUI(func() {
			a.memberRebuilt()
			if a.stale(epoch) || a.memberSeq != seq || a.currentServerID != serverID {
				return
			}

			a.memberCache = members
			a.memberList.SetModel(entries)
			a.updateMemberStatus()
		}, false)
	}()
}

// patchedMembers is a copy of previous with everybody named in changed resolved
// again, in the order they were already in — presence being the only thing that
// moved and the order being by name. Apart from the rebuild so the picking can be
// tested without a session or a widget, as memberStatusFor is.
//
// A copy rather than a write, because previous is published: a worker still
// reading it must not see it move. Somebody the store has since forgotten keeps
// the value that was drawn for them, which is what the next walk is for; somebody
// changed who is not in previous at all is not added — they arrived after this
// membership was resolved, so they are the next walk's too.
//
// One pass over the membership rather than a lookup per name: an index over
// thousands of members costs more to build than the walk costs to make.
func patchedMembers(store domain.Store, serverID string, previous []domain.Member, changed map[string]bool) []domain.Member {
	members := make([]domain.Member, len(previous))
	copy(members, previous)

	for i := range members {
		if !changed[members[i].UserID] {
			continue
		}
		if member, ok := store.Member(serverID, members[i].UserID); ok {
			members[i] = member
		}
	}

	return members
}

// memberRebuilt releases the single-flight claim and picks up whatever arrived
// while it was held. Called by both rebuilds whether or not their answer was
// still wanted — a claim outliving its worker stops the sidebar following
// presence for the rest of the session. Call on the UI thread.
func (a *App) memberRebuilt() {
	a.memberWorking = false

	// A rebuild deferred by the claim is already queued and only wants the window
	// it is drawn in; presence has neither. The walk supersedes the patch, which is
	// flushRefresh's own rule.
	switch {
	case a.dirty&refreshMembers != 0:
		a.queueRefresh(refreshMembers)
	case len(a.presenceDirty) > 0:
		a.queueRefresh(refreshPresence)
	}
}

// dropMemberCache forgets the resolved membership, so the next presence event
// walks rather than patching something that is no longer the open server's. Call
// on the UI thread.
func (a *App) dropMemberCache() {
	a.memberCache, a.memberCacheServer, a.presenceDirty = nil, "", nil
}

/* What the sidebar says when its rows cannot */

// updateMemberStatus decides the strip over the list, computed from the state
// rather than written at each place that changes it: a fetch starting, finishing,
// timing out and a model landing can reach it in any order, and four call sites
// each setting a message is four chances to be left claiming to load what already
// arrived. Call on the UI thread, after anything it depends on has moved.
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

// memberStatusFor is the decision alone, apart from the widget so it can be
// checked without one. Order is the whole of it: a fetch in flight outranks a
// previous failure — retrying is what put it in flight — and a failure outranks
// an empty list, "nobody to show" for a membership that never arrived being a lie
// the user cannot see through.
func memberStatusFor(serverID string, loading, failed, empty bool) ui.MemberListStatus {
	switch {
	case serverID == "":
		return ui.MemberListStatus{}
	case loading && empty:
		return ui.MemberListStatus{Text: "Loading members", Busy: true}
	case loading:
		return ui.MemberListStatus{Text: "Refreshing members", Busy: true}
	case failed:
		return ui.MemberListStatus{Text: "Couldn't load members.", Action: "Retry"}
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
// arrives. It cancels nothing and cannot — revoltgo's REST layer takes no
// context — so what the timeout buys is the sidebar no longer claiming to load
// something nothing is watching. An answer landing afterwards is still installed.
func (a *App) armMemberWatchdog(serverID string) {
	a.stopMemberWatchdog()

	var watchdog *time.Timer
	watchdog = time.AfterFunc(memberFetchTimeout, func() {
		a.doOnUI(func() {
			// A fired timer cannot be recalled, so the wake checks it is still the one
			// the field holds.
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

// memberListOptions is what the settings say the list should look like, read per
// rebuild so a change applies to the next one. Hoisting only means anything
// alongside the presence split — an ungrouped list has no sections to hoist into
// — and the model reads it that way. A method because the You section needs this
// account's ID.
func (a *App) memberListOptions() ui.MemberListOptions {
	settings := config.Current().Behaviour

	options := ui.MemberListOptions{
		GroupByPresence: settings.GroupByPresence,
		HoistRoles:      settings.HoistRoles,
		HideOffline:     settings.HideOfflineMembers,
		HideRoleless:    settings.HideRolelessMembers,
		FallbackToAll:   settings.MemberListFallback,
	}

	if settings.ShowSelfFirst {
		options.SelfID = a.store.SelfID()
	}

	return options
}

// refreshMemberRow redraws one member in place, for a change that does not move
// them. Somebody unmounted is a no-op: their row is built from the store when it
// scrolls into view.
func (a *App) refreshMemberRow(userID string) {
	if a.memberList == nil || a.currentServerID == "" {
		return
	}

	if member, ok := a.store.Member(a.currentServerID, userID); ok {
		a.memberList.RefreshMember(&member)
	}
}

/* The full membership */

// loadMembers pulls a server's whole membership, once per server per session.
// Without it the list holds the gateway's members plus whoever lazy resolution
// pulled in — a fraction of a large server, under section counts that mean
// nothing. Revolt has no pagination and no member search, so it is one request
// for everybody or none; the setting turns it off.
//
// It also fills the *user* cache, which is what makes presence work: revoltgo
// drops an update for an account it has never heard of, so a member nobody
// fetched can never come online.
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

	// A second attempt while the first is still out is not a failure: the first
	// request's own answer is still coming, and it is what finishes this.
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

/* A conversation's own people */

// refreshRecipients draws a conversation's participants: the group's member
// sidebar, and the people mentionable in whatever is open. One walk feeds both,
// which is the arrangement the server path already has — there refreshMemberList
// resolves a membership once and pushes the sidebar and the picker from it.
//
// A server's people are deliberately not its business: that walk is far too much
// for the UI thread, and refreshMemberList already makes it off-thread and pushes
// the result through setMentionCandidates. Every path into a server channel goes
// through enterServer first, so the pool is already there — asking again would
// walk a whole membership a second time, on the wrong thread, to arrive at what
// the picker is holding. Channels are pushed separately by refreshChannelList.
//
// Only a **group** fills the sidebar. A direct message is two people the header
// has already named and saved notes is one, so either would be a column drawing
// what is written across the top of the window.
//
// This one is cheap enough for the UI thread where a membership is not: a
// conversation carries its participants, Revolt caps a group well under a
// thousand, and every lookup is the store answering from what it holds.
//
// Call on the UI thread, when the open conversation or its recipients change.
func (a *App) refreshRecipients() {
	if a.currentServerID != "" {
		return // both lists are the server's, and its own walk is what fills them
	}

	channel, ok := a.currentChannel()
	if !ok || channel.ServerID != "" {
		a.clearRecipients()
		return
	}

	a.setMentionCandidates(ui.MentionUser, recipientCandidates(a.store, channel))

	if channel.Kind != domain.ChannelGroup {
		a.resetMemberList()
		return
	}

	members := recipientMembers(a.store, channel)

	if a.memberList != nil {
		a.memberList.SetModel(ui.NewMemberModel(members, nil, a.recipientListOptions()))
		a.updateMemberStatus()
	}
}

// ensureRecipients queues the accounts behind a group's participants. It is that
// group's answer to a server's one membership fetch: somebody who has only ever
// been in the group has never written a message here, so nothing else would ask
// who they are, and both walks above drop whoever they cannot name.
//
// Called where the conversation changes rather than from refreshRecipients, which
// runs on every batch of resolved authors: flushAuthors releases the guard on a
// failure so a later message retries, and re-queueing from inside that flush
// would be one request per batch, forever, for an account that cannot be fetched.
// Call on the UI thread.
func (a *App) ensureRecipients() {
	channel, ok := a.currentChannel()
	if !ok || channel.Kind != domain.ChannelGroup {
		return
	}

	for _, userID := range channel.Recipients {
		a.ensureAuthor("", userID)
	}
}

// clearRecipients empties both lists, which is what leaving the home view for a
// server and closing the last conversation both mean.
func (a *App) clearRecipients() {
	a.resetMemberList()
	a.setMentionCandidates(ui.MentionUser, nil)
}

// resetMemberList empties the sidebar and returns it to the top.
func (a *App) resetMemberList() {
	if a.memberList == nil {
		return
	}

	a.memberList.Reset()
	a.updateMemberStatus()
}

// recipientListOptions is memberListOptions for a conversation, which has no
// roles at all: hiding the members who hold none would hide every one of them,
// and hoisting is an arrangement only a server has. What is left — grouping by
// presence, hiding who is offline, drawing this account first — means the same
// thing in a group as in a server.
func (a *App) recipientListOptions() ui.MemberListOptions {
	options := a.memberListOptions()
	options.HoistRoles, options.HideRoleless = false, false

	return options
}

// recipientMembers resolves a conversation's people into what the sidebar draws,
// ordered by name as Store.Members orders a server's — the model never re-orders
// within a bucket, so the order has to arrive with them.
//
// A group has no memberships, so there is no nickname, role colour or join date
// to resolve: what is drawn is the account itself. It takes the store rather than
// reading a.store, which is what lets it be tested against a fake.
func recipientMembers(store domain.Store, channel domain.Channel) []domain.Member {
	members := make([]domain.Member, 0, len(channel.Recipients))

	for _, userID := range channel.Recipients {
		user, ok := store.User(userID)
		if !ok {
			continue
		}

		members = append(members, domain.Member{
			UserID:    user.ID,
			Name:      user.Name,
			Username:  user.Username,
			AvatarURL: user.AvatarURL,
			Presence:  user.Presence,
			Bot:       user.Bot,
		})
	}

	slices.SortFunc(members, func(x, y domain.Member) int {
		if by := strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)); by != 0 {
			return by
		}

		return strings.Compare(x.UserID, y.UserID)
	})

	return members
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

// recipientCandidates resolves a conversation's mentionable people from what the
// client already knows — no network, the same rule the sidebar follows. A
// conversation names its own participants, so this is bounded by the channel and
// needs no membership walk. It takes the store rather than reading a.store, which
// is what lets it be tested against a fake.
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

// memberCandidates turns resolved members into mention candidates, already
// ordered. They carry the nickname, per-server avatar and role colour the sidebar
// shows, so the picker looks like the list being picked from. A member with no
// resolved account and no nickname is dropped: nothing to display or match against.
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

/* Editing a member */

// timeoutSpans is what the timeout menu offers. Revolt bounds a timeout nowhere
// — member_edit.rs validates neither a maximum nor a value in the past — so the
// ceiling here is the client's own.
var timeoutSpans = []struct {
	label string
	span  time.Duration
}{
	{"1 minute", time.Minute},
	{"5 minutes", 5 * time.Minute},
	{"10 minutes", 10 * time.Minute},
	{"1 hour", time.Hour},
	{"1 day", 24 * time.Hour},
	{"1 week", 7 * 24 * time.Hour},
}

// serverRanking is how senior this account is in a server: the lowest rank it
// holds, everything for the owner, and nothing at all for a member with no roles.
// Revolt compares a role against it before letting that role be assigned, edited
// or reordered, so the menu and the role editor ask it before offering either.
// https://github.com/stoatchat/stoatchat/blob/main/crates/core/database/src/models/server_members/model.rs
func (a *App) serverRanking(serverID string) int64 {
	server, ok := a.store.Server(serverID)
	if !ok {
		return math.MaxInt64
	}

	self := a.store.SelfID()
	if self != "" && server.OwnerID == self {
		return math.MinInt64
	}

	roles := a.store.MemberRoles(serverID, self)
	if len(roles) == 0 {
		return math.MaxInt64
	}

	return roles[0].Rank // most senior first
}

// memberNicknameItems is the two ways a nickname is changed, offered only where
// this account may change that member's: its own takes ChangeNickname, anybody
// else's ManageNicknames.
func (a *App) memberNicknameItems(serverID, userID string) []*fyne.MenuItem {
	if !a.canRenameMember(serverID, userID) {
		return nil
	}

	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Change nickname", func() { a.promptMemberNickname(serverID, userID) }),
	}

	if member, ok := a.store.Member(serverID, userID); ok && member.Nickname != "" {
		items = append(items, fyne.NewMenuItem("Remove nickname", func() {
			a.setMemberNickname(serverID, userID, "")
		}))
	}

	return items
}

func (a *App) canRenameMember(serverID, userID string) bool {
	if serverID == "" || userID == "" {
		return false
	}

	permission := domain.PermissionManageNicknames
	if userID == a.store.SelfID() {
		permission = domain.PermissionChangeNickname
	}

	return a.store.ServerPermissions(serverID).Has(permission)
}

// promptMemberNickname raises the card that renames somebody in one server. The
// card comes down on submit and the outcome is a notice: unlike a username, a
// refusal here is nothing to correct in the field it came from. Call on the UI
// thread.
func (a *App) promptMemberNickname(serverID, userID string) {
	name := a.memberName(serverID, userID)

	a.showPrompt(ui.Prompt{
		Title:  "Change nickname",
		Action: "Change",
		Busy:   "Changing...",
		Fields: []ui.PromptField{{Label: "Nickname", Placeholder: name}},
		OnSubmit: func(values []string) {
			a.closeOverlay()
			a.setMemberNickname(serverID, userID, values[0])
		},
	})
}

// setMemberNickname renames a member, an empty name taking the nickname off. What
// took is drawn by the ServerMemberUpdate that follows.
func (a *App) setMemberNickname(serverID, userID, nickname string) {
	name := a.memberName(serverID, userID)

	failure, success := "Could not rename %s.", "%s was renamed."
	if nickname == "" {
		failure, success = "Could not take %s's nickname off.", "%s's nickname was removed."
	}

	a.reportAction(
		func() error { return a.client.SetMemberNickname(serverID, userID, nickname) },
		"rename member "+userID+" in server "+serverID, failure, success, name,
	)
}

// memberRoleItems is the roles this account may give and take, each marked with
// whether the member already holds it. A role at or above this account's own rank
// is left out rather than offered to be refused.
func (a *App) memberRoleItems(serverID, userID string) []*fyne.MenuItem {
	if serverID == "" || !a.store.ServerPermissions(serverID).Has(domain.PermissionAssignRoles) {
		return nil
	}

	roles := a.store.ServerRoles(serverID)
	ranking := a.serverRanking(serverID)

	held := make(map[string]bool)
	for _, role := range a.store.MemberRoles(serverID, userID) {
		held[role.ID] = true
	}

	items := make([]*fyne.MenuItem, 0, len(roles))
	for _, role := range roles {
		if role.Rank <= ranking {
			continue
		}

		item := fyne.NewMenuItem(role.Name, func() { a.toggleMemberRole(serverID, userID, role.ID) })
		item.Checked = held[role.ID]
		items = append(items, item)
	}

	if len(items) == 0 {
		return nil
	}

	parent := fyne.NewMenuItem("Roles", nil)
	parent.ChildMenu = fyne.NewMenu("", items...)

	return []*fyne.MenuItem{parent}
}

// toggleMemberRole gives a role or takes it back. Revolt takes the whole set
// rather than a change to it, so what the member already holds is sent with it.
func (a *App) toggleMemberRole(serverID, userID, roleID string) {
	held := a.store.MemberRoles(serverID, userID)

	next := make([]string, 0, len(held)+1)
	var wore bool
	for _, role := range held {
		if role.ID == roleID {
			wore = true
			continue
		}
		next = append(next, role.ID)
	}
	if !wore {
		next = append(next, roleID)
	}

	name, verb := a.memberName(serverID, userID), "given to"
	if wore {
		verb = "taken from"
	}

	a.reportAction(
		func() error { return a.client.SetMemberRoles(serverID, userID, next) },
		"set roles on member "+userID+" in server "+serverID,
		"Could not change what %s holds.", "That role was "+verb+" %s.", name,
	)
}

// memberTimeoutItems is the timeout menu: how long, or an end to one already
// standing. Revolt refuses a timeout aimed at this account or at anybody who may
// hand one out themselves, so neither is offered.
func (a *App) memberTimeoutItems(serverID, userID string) []*fyne.MenuItem {
	if !a.canTimeoutMember(serverID, userID) {
		return nil
	}

	if member, ok := a.store.Member(serverID, userID); ok && member.Timeout.After(time.Now()) {
		return []*fyne.MenuItem{fyne.NewMenuItem("End timeout", func() { a.removeTimeout(serverID, userID) })}
	}

	spans := make([]*fyne.MenuItem, 0, len(timeoutSpans))
	for _, option := range timeoutSpans {
		spans = append(spans, fyne.NewMenuItem(option.label, func() {
			a.confirmTimeoutMember(serverID, userID, option.label, option.span)
		}))
	}

	// The mark a timeout wears is the composer's own refusal, that being what one
	// does: the member stays and cannot write.
	parent := fyne.NewMenuItemWithIcon("Time out", ui.CautionMark(assets.ForbiddenIcon), nil)
	parent.ChildMenu = fyne.NewMenu("", spans...)

	return []*fyne.MenuItem{parent}
}

// canTimeoutMember: the permission, and a target who is neither this account nor
// somebody holding the same permission — both of which member_edit.rs refuses.
func (a *App) canTimeoutMember(serverID, userID string) bool {
	if serverID == "" || userID == "" || userID == a.store.SelfID() {
		return false
	}
	if !a.store.ServerPermissions(serverID).Has(domain.PermissionTimeoutMembers) {
		return false
	}

	return !a.store.MemberServerPermissions(serverID, userID).Has(domain.PermissionTimeoutMembers)
}

// confirmTimeoutMember asks before silencing somebody. A warning rather than a
// danger: it ends by itself, and can be ended sooner from the same menu.
func (a *App) confirmTimeoutMember(serverID, userID, label string, span time.Duration) {
	name := a.memberName(serverID, userID)

	a.confirm(ui.Confirm{
		Title:  "Time out member",
		Body:   fmt.Sprintf("%s will be able to read the server but not write in it for %s.", name, label),
		Action: "Time out",
		Tone:   ui.ToneWarning,
		OnConfirm: func() {
			a.reportAction(
				func() error { return a.client.TimeoutMember(serverID, userID, time.Now().Add(span)) },
				"time out member "+userID+" in server "+serverID,
				"Could not time %s out.", "%s was timed out for "+label+".", name,
			)
		},
	})
}

// removeTimeout ends one early.
func (a *App) removeTimeout(serverID, userID string) {
	name := a.memberName(serverID, userID)

	a.reportAction(
		func() error { return a.client.RemoveTimeout(serverID, userID) },
		"end timeout on member "+userID+" in server "+serverID,
		"Could not end %s's timeout.", "%s may write again.", name,
	)
}

/* Voice moderation */

// memberVoiceItems is what the member menu offers about somebody's voice: the
// two server-wide holds and the two ways out of a channel. Each is gated on its
// own permission **and** on the member actually being in a call — an item that
// can never do anything is worse than no item, which is the rule
// canTimeoutMember follows.
//
// Muting and deafening are caution rather than danger: each is undone by doing
// the opposite.
func (a *App) memberVoiceItems(serverID, userID string) []*fyne.MenuItem {
	if serverID == "" || userID == "" || userID == a.store.SelfID() {
		return nil
	}

	channelID, inCall := a.voiceChannelOf(serverID, userID)
	if !inCall {
		return nil
	}

	permissions := a.store.ServerPermissions(serverID)

	var items []*fyne.MenuItem

	if permissions.Has(domain.PermissionMuteMembers) {
		items = append(items, fyne.NewMenuItemWithIcon("Server mute",
			ui.CautionMark(assets.MicOffIcon),
			func() { a.setMemberVoiceMuted(serverID, userID, true) }))
	}

	if permissions.Has(domain.PermissionDeafenMembers) {
		items = append(items, fyne.NewMenuItemWithIcon("Server deafen",
			ui.CautionMark(assets.HeadphonesOffIcon),
			func() { a.setMemberVoiceDeafened(serverID, userID, true) }))
	}

	if permissions.Has(domain.PermissionMoveMembers) {
		if move := a.memberMoveItems(serverID, userID, channelID); move != nil {
			items = append(items, move)
		}

		items = append(items, fyne.NewMenuItemWithIcon("Disconnect",
			ui.CautionMark(assets.CallEndIcon),
			func() { a.disconnectMember(serverID, userID) }))
	}

	return items
}

// voiceChannelOf finds the call a member is in, so the menu can be built around
// what is actually true. The store answers per channel rather than per member —
// Revolt files a call on the channel — so this is a walk of the server's voice
// channels, of which there are a handful.
func (a *App) voiceChannelOf(serverID, userID string) (channelID string, ok bool) {
	server, found := a.store.Server(serverID)
	if !found {
		return "", false
	}

	for _, id := range server.Channels {
		channel, found := a.store.Channel(id)
		if !found || channel.Kind != domain.ChannelVoice {
			continue
		}

		for _, participant := range a.store.VoiceParticipants(id) {
			if participant.UserID == userID {
				return id, true
			}
		}
	}

	return "", false
}

// memberMoveItems is the submenu of voice channels somebody may be dragged into,
// copied from memberRoleItems: a list too long to flatten into the menu itself.
// The channel they are already in is left out, a move to it being nothing.
func (a *App) memberMoveItems(serverID, userID, currentID string) *fyne.MenuItem {
	server, found := a.store.Server(serverID)
	if !found {
		return nil
	}

	var items []*fyne.MenuItem

	for _, id := range server.Channels {
		channel, found := a.store.Channel(id)
		if !found || channel.Kind != domain.ChannelVoice || id == currentID {
			continue
		}
		if !a.canViewChannel(channel) {
			continue
		}

		items = append(items, fyne.NewMenuItem(channel.Name,
			func() { a.moveMember(serverID, userID, id) }))
	}

	if len(items) == 0 {
		return nil
	}

	parent := fyne.NewMenuItem("Move to", nil)
	parent.ChildMenu = fyne.NewMenu("", items...)

	return parent
}

func (a *App) setMemberVoiceMuted(serverID, userID string, muted bool) {
	name := a.memberName(serverID, userID)

	a.reportAction(
		func() error { return a.client.SetMemberVoiceMuted(serverID, userID, muted) },
		"mute member "+userID+" in server "+serverID,
		"Could not mute %s.", "%s can no longer speak.", name,
	)
}

func (a *App) setMemberVoiceDeafened(serverID, userID string, deafened bool) {
	name := a.memberName(serverID, userID)

	a.reportAction(
		func() error { return a.client.SetMemberVoiceDeafened(serverID, userID, deafened) },
		"deafen member "+userID+" in server "+serverID,
		"Could not deafen %s.", "%s can no longer hear the call.", name,
	)
}

func (a *App) moveMember(serverID, userID, channelID string) {
	name := a.memberName(serverID, userID)

	a.reportAction(
		func() error { return a.client.MoveMember(serverID, userID, channelID) },
		"move member "+userID+" in server "+serverID,
		"Could not move %s.", "%s was moved.", name,
	)
}

func (a *App) disconnectMember(serverID, userID string) {
	name := a.memberName(serverID, userID)

	a.reportAction(
		func() error { return a.client.DisconnectMember(serverID, userID) },
		"disconnect member "+userID+" in server "+serverID,
		"Could not disconnect %s.", "%s left the call.", name,
	)
}
