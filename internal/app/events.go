package app

// The event pump: one goroutine ranging the client's event stream, hopping onto
// the UI thread once per event and repainting. Everything the server pushes
// arrives here in the order it was produced, which is what keeps a burst of
// messages grouping correctly and a logout from racing a login.
//
// Nothing in this file talks to Revolt. What each handler does is decide what
// the view should now look like.

import (
	"log"
	"slices"
	"time"

	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
)

// ackDelay is the coalescing window for read acknowledgements of the open
// channel: a burst of incoming messages produces one ack for the newest of them
// instead of one request per message.
func ackDelay() time.Duration { return config.Current().Behaviour.AckDelay() }

/* The refresh queue */

// refreshTarget names a whole surface a gateway event can invalidate. Each is a
// rebuild that walks something — every server icon, every channel of the open
// server against its permissions, every member of it — so none is worth making
// once per event.
//
// The set is deliberately only these three. What a single event changes about
// the *open* thing — a header's text, the channel glyph, whether the composer
// takes a message — is a setter and a permission lookup, and deferring those
// would make the client feel slow to save nothing.
type refreshTarget uint8

const (
	refreshServers refreshTarget = 1 << iota
	refreshChannels
	refreshMembers
)

// refreshDelay is how long a queued rebuild waits for more of the same burst.
func refreshDelay() time.Duration { return config.Current().Behaviour.RefreshDelay() }

// queueRefresh marks targets as needing a rebuild and arms the settling window,
// which is not restarted by what arrives inside it: the point is that a burst
// costs one rebuild, so the *first* event decides when the burst is drawn and
// everything behind it joins that one. Call on the UI thread.
//
// This is what makes the client's realtime handling affordable. Revolt sends a
// rank reorder as one event per role, a channel added to a server as a create
// *and* a server update, and presence on a large server continuously — all of
// which land here as bits set on a byte.
func (a *App) queueRefresh(targets refreshTarget) {
	a.dirty |= targets
	if a.refreshTimer != nil {
		return
	}

	a.refreshTimer = time.AfterFunc(refreshDelay(), func() {
		a.doOnUI(a.flushRefresh, false)
	})
}

// flushRefresh runs the rebuilds the window gathered, outermost column first so
// the state each reads is the state the one before it settled on. Every one of
// them re-reads the store rather than anything the events carried, so a flush is
// correct whenever it happens to run. Call on the UI thread.
func (a *App) flushRefresh() {
	a.refreshTimer = nil

	targets := a.dirty
	a.dirty = 0

	if targets&refreshServers != 0 {
		a.refreshServerList()
	}
	if targets&refreshChannels != 0 {
		a.refreshChannelList()
	}
	if targets&refreshMembers != 0 {
		a.refreshMemberList()
	}
}

// pumpEvents drains the client's event stream for the life of the process. It is
// started once, before the first login, so no event can arrive before there is
// somebody to read it — the stream is buffered but not unbounded.
func (a *App) pumpEvents() {
	for event := range a.client.Events() {
		a.dispatch(event)
	}
}

// dispatch hands one event to its handler on the UI thread. The type switch is
// exhaustive by construction: client.Event's marker method is unexported, so the
// set cannot grow without this file failing to compile against it.
func (a *App) dispatch(event client.Event) {
	switch e := event.(type) {
	case client.Ready:
		a.doOnUI(func() { a.onReady(e) }, true)
	case client.Disconnected:
		a.doOnUI(func() { a.onDisconnected(e) }, true)
	case client.MessageCreated:
		a.doOnUI(func() { a.onMessageCreated(e) }, false)
	case client.MessageUpdated:
		a.doOnUI(func() { a.refreshMessage(e.ChannelID, e.MessageID) }, false)
	case client.MessageDeleted:
		a.doOnUI(func() { a.removeMessages(e.ChannelID, e.MessageIDs) }, false)
	case client.ServerJoined:
		a.doOnUI(func() { a.onServerJoined(e) }, false)
	case client.ServerLeft:
		a.doOnUI(func() { a.onServerLeft(e) }, false)
	case client.ServerUpdated:
		a.doOnUI(func() { a.onServerUpdated(e) }, false)
	case client.RolesChanged:
		a.doOnUI(func() { a.onRolesChanged(e) }, false)
	case client.ChannelCreated:
		a.doOnUI(func() { a.onChannelCreated(e) }, false)
	case client.ChannelUpdated:
		a.doOnUI(func() { a.onChannelUpdated(e) }, false)
	case client.ChannelClosed:
		a.doOnUI(func() { a.onChannelClosed(e) }, false)
	case client.ChannelRead:
		a.doOnUI(func() { a.onChannelRead(e) }, false)
	case client.MembersChanged:
		a.doOnUI(func() { a.onMembersChanged(e) }, false)
	case client.MemberUpdated:
		a.doOnUI(func() { a.onMemberUpdated(e) }, false)
	case client.RecipientsChanged:
		a.doOnUI(func() { a.onRecipientsChanged(e) }, false)
	case client.UserRemoved:
		a.doOnUI(func() { a.onUserRemoved(e) }, false)
	case client.UserUpdated:
		a.doOnUI(func() { a.onUserUpdated(e) }, false)
	case client.RelationshipChanged:
		a.doOnUI(func() { a.onRelationshipChanged(e) }, false)
	case client.PresenceChanged:
		a.doOnUI(func() { a.onPresenceChanged(e) }, false)
	case client.TypingChanged:
		a.doOnUI(func() { a.onTypingChanged(e) }, false)
	}
}

/* Lifecycle */

// onReady handles the initial gateway snapshot: it persists a pending token,
// records unread channels, and shows the main UI on the first server.
func (a *App) onReady(event client.Ready) {
	a.stopAwaitingReady()
	a.savePendingToken()

	for _, channelID := range event.UnreadChannelIDs {
		a.unreadChannels[channelID] = true
	}

	a.showMainUI()

	a.serverIDs = slices.Clone(event.ServerIDs)
	a.refreshServerList()

	// An account in no servers still has somewhere to land: the home view opens on
	// its direct messages rather than leaving the client blank.
	if len(a.serverIDs) > 0 {
		a.selectServer(a.serverIDs[0])
		return
	}
	a.selectHome()
}

// savePendingToken persists the token captured during a credential login, now
// that the user's identity is known. Call on the UI thread.
func (a *App) savePendingToken() {
	if a.pendingToken == "" {
		return
	}

	self, ok := a.store.Self()
	if !ok {
		return
	}

	saved := SavedSession{
		Token:     a.pendingToken,
		UserID:    self.ID,
		Username:  self.Username,
		AvatarURL: self.AvatarURL,
	}
	if err := AddOrUpdateSession(saved); err != nil {
		log.Printf("save session: %v", err)
	}

	a.pendingToken = ""
}

// onDisconnected tears down a dead session and returns to the login screen. Only
// a fatal drop reaches here — a flaky connection is the gateway's problem, not
// the user's.
func (a *App) onDisconnected(event client.Disconnected) {
	if !event.Fatal {
		return
	}

	if userID := a.store.SelfID(); userID != "" {
		if err := RemoveSession(userID); err != nil {
			log.Printf("remove session: %v", err)
		}
	}

	a.client.Close()
	a.showLogin()

	// The saved login has just been removed, so the card that would have taken one
	// click is gone as well — saying why is the difference between that and the
	// client having forgotten who was signed in.
	a.reportLogin("The server ended this session. Sign in again.")
}

/* Messages */

// onMessageCreated appends an incoming message to the open channel, acknowledging
// it as read, or marks its channel unread.
func (a *App) onMessageCreated(event client.MessageCreated) {
	channelID := event.Message.ChannelID

	// One of our own starts the channel's cooldown. The send path has already
	// started it for a message composed here, so this is what covers one the same
	// account sent from another client.
	if event.Message.AuthorID == a.store.SelfID() {
		a.startSlowmode(channelID)
	}

	// Sending is the end of typing, and Revolt does not reliably say so before the
	// message lands — left to lapse, the line would name somebody under the message
	// they just posted.
	a.forgetTyping(channelID, event.Message.AuthorID)

	if channelID == a.currentChannelID {
		a.appendMessage(event.Message, event.Previous)
		a.scheduleAck(channelID, event.Message.ID)
		return
	}

	a.unreadChannels[channelID] = true
	a.refreshChannelRow(channelID)
}

/* Read acknowledgement */

// scheduleAck records messageID as the newest seen message of channelID and
// acknowledges it after ackDelay, coalescing bursts into one request. An ack
// still pending for a different channel — the user switched inside the window —
// is flushed immediately, so it isn't lost to the overwrite. Call on the UI
// thread.
func (a *App) scheduleAck(channelID, messageID string) {
	if a.ackTimer != nil && a.ackChannelID != channelID {
		a.ackTimer.Stop()
		a.ackTimer = nil
		a.sendAck(a.ackChannelID, a.ackMessageID)
	}

	a.ackChannelID, a.ackMessageID = channelID, messageID
	if a.ackTimer != nil {
		return
	}

	a.ackTimer = time.AfterFunc(ackDelay(), func() {
		a.doOnUI(func() {
			a.ackTimer = nil
			a.sendAck(a.ackChannelID, a.ackMessageID)
		}, false)
	})
}

// sendAck acknowledges a channel in the background. Call on the UI thread.
func (a *App) sendAck(channelID, messageID string) {
	a.background(
		func() error { return a.client.AckMessage(channelID, messageID) },
		func(err error) { log.Printf("ack channel %s: %v", channelID, err) },
	)
}

/* Servers and members */

// onServerJoined adds a newly joined server to the sidebar, keeping the app's own
// ordered server list in step with the store.
//
// Selecting it is deliberately conditional on pendingJoin: the invite dialog is
// the one path where the user asked to go there, and a server appearing for any
// other reason must not yank the view out of the channel they are reading.
func (a *App) onServerJoined(event client.ServerJoined) {
	selecting := a.pendingJoin
	a.pendingJoin = false

	if !slices.Contains(a.serverIDs, event.ServerID) {
		a.serverIDs = append(a.serverIDs, event.ServerID)
		a.refreshServerList()
	}
	if selecting {
		a.selectServer(event.ServerID)
	}
}

// onServerLeft drops a server the user is no longer a member of. If it was the
// server in view, the client moves somewhere that still exists rather than
// showing an empty shell.
func (a *App) onServerLeft(event client.ServerLeft) {
	i := slices.Index(a.serverIDs, event.ServerID)
	if i == -1 {
		return
	}

	a.serverIDs = slices.Delete(a.serverIDs, i, i+1)
	a.refreshServerList()

	if a.currentServerID != event.ServerID {
		return
	}

	// Cleared first: selectServer treats re-selecting the current server as a
	// no-op, and this one is about to stop existing.
	a.currentServerID = ""
	if len(a.serverIDs) > 0 {
		a.selectServer(a.serverIDs[0])
		return
	}
	a.selectHome()
}

// onServerUpdated repaints what a server's own details are drawn into: its icon
// in the sidebar, the header above the channel list, and the channel list itself
// — the same event carries a re-ordered category or a channel moved between two.
//
// Revolt sends this alongside the channel create or delete that caused it, so
// both rebuilds are queued: acting on each as it lands would rebuild the sidebar
// twice for one change.
func (a *App) onServerUpdated(event client.ServerUpdated) {
	a.queueRefresh(refreshServers)

	if a.currentServerID != event.ServerID {
		return
	}

	if server, ok := a.store.Server(event.ServerID); ok {
		a.setHeader(a.serverHeader, server.Name)
	}
	a.queueRefresh(refreshChannels)
}

// onRolesChanged re-reads a server whose roles moved. A role carries a colour, a
// rank and a set of permissions, so this is the one event that can repaint every
// name in the member list, re-sort it, and change which channels are listed at
// all — hence the same three calls onMemberUpdated makes for our own member.
//
// Both rebuilds are queued rather than made: a rank reorder arrives as one event
// per role, and creating a role arrives as an update for one State has never
// heard of — revoltgo files it on the way past, so there is no separate create
// to handle.
func (a *App) onRolesChanged(event client.RolesChanged) {
	if a.currentServerID != event.ServerID {
		return
	}

	a.queueRefresh(refreshChannels | refreshMembers)
	a.syncComposer()
}

// onChannelCreated adds a channel that now exists: one added to the open server,
// or a conversation opened from another client — the case the home view could
// previously only learn about by being re-entered.
//
// A conversation is prepended rather than sorted in. It has no messages yet, so
// it has no LastMessageID to sort by, and the list is ordered by that.
func (a *App) onChannelCreated(event client.ChannelCreated) {
	if event.ServerID != "" {
		if a.currentServerID == event.ServerID {
			a.queueRefresh(refreshChannels)
		}
		return
	}

	if slices.Contains(a.dmChannels, event.ChannelID) {
		return
	}
	a.dmChannels = append([]string{event.ChannelID}, a.dmChannels...)

	if a.homeSelected {
		a.queueRefresh(refreshChannels)
	}
}

// onChannelUpdated repaints a channel whose name, icon or permissions changed.
//
// The whole sidebar is rebuilt rather than the one row: a permission overwrite is
// among the things this announces, and whether the channel is a row at all is
// decided by ViewChannel — so a row can have to appear or disappear, which
// repainting it in place cannot do.
func (a *App) onChannelUpdated(event client.ChannelUpdated) {
	a.queueRefresh(refreshChannels)

	if a.currentChannelID != event.ChannelID {
		return
	}

	channel, ok := a.store.Channel(event.ChannelID)
	if !ok {
		return
	}

	a.setHeader(a.channelHeader, channel.Name)
	a.setChannelGlyph()
	a.syncComposer()

	// The overwrites may have taken the channel away entirely, in which case the
	// page under it is no longer ours to show.
	if !a.canViewChannel(channel) {
		a.showStatus("You don't have access to this channel")
	}
}

// onChannelRead clears a channel's unread mark when the account acknowledged it
// somewhere else. Our own acks echo back here too, where they land on a mark
// already cleared and cost one map delete.
func (a *App) onChannelRead(event client.ChannelRead) {
	if !a.unreadChannels[event.ChannelID] {
		return
	}

	delete(a.unreadChannels, event.ChannelID)
	a.refreshChannelRow(event.ChannelID)
}

// onChannelClosed drops a closed conversation or a deleted server channel. Both
// arrive the same way, so the sidebar is rebuilt either way and only the home
// view's own ordering needs maintaining.
func (a *App) onChannelClosed(event client.ChannelClosed) {
	delete(a.unreadChannels, event.ChannelID)
	if i := slices.Index(a.dmChannels, event.ChannelID); i >= 0 {
		a.dmChannels = slices.Delete(a.dmChannels, i, i+1)
	}

	if a.currentChannelID != event.ChannelID {
		a.queueRefresh(refreshChannels)
		return
	}

	// The channel in view is about to stop existing, so its row goes now rather
	// than at the end of a settling window: what follows selects another one, and
	// selection is painted onto the rows the sidebar is holding.
	a.refreshChannelList()

	a.clearChannelSelection()
	if a.homeSelected && len(a.dmChannels) > 0 {
		a.selectChannel(a.dmChannels[0])
	}
}

// onMembersChanged repaints the member sidebar when the open server's membership
// changes.
//
// A join arrives as a membership and nothing else — revoltgo fabricates one with
// no account attached — so the row would read "Unknown user" until something
// asked who it was. ensureAuthor is that something, and its own batch refreshes
// the sidebar when it lands.
//
// When the member is *us*, this is the only announcement that we have left the
// server: Revolt reports a leave to the server rather than reporting a deletion
// to the leaver, and revoltgo's own handler quietly evicts the server from State
// on the strength of it. So it is handed to the leave path, which is a no-op for
// a server already gone from the list — a leave made here has been through it.
func (a *App) onMembersChanged(event client.MembersChanged) {
	if event.UserID == a.store.SelfID() {
		a.onServerLeft(client.ServerLeft{ServerID: event.ServerID})
		return
	}
	if a.currentServerID != event.ServerID {
		return
	}

	a.ensureAuthor(event.ServerID, event.UserID)
	a.queueRefresh(refreshMembers)
}

// onRecipientsChanged follows a group conversation's own membership. A group is
// the one channel whose participants are a list the client reads — they are the
// pool the composer's @mention picker offers there — so a join or a leave has to
// reach the picker as a server's would reach the member sidebar.
//
// Us leaving is the group leaving the sidebar. Revolt announces it as a
// participant change like anybody else's, so the close path is reached from here
// rather than from a deletion nobody is sent.
func (a *App) onRecipientsChanged(event client.RecipientsChanged) {
	if event.UserID == a.store.SelfID() && !event.Joined {
		a.onChannelClosed(client.ChannelClosed{ChannelID: event.ChannelID})
		return
	}

	// Somebody who has only ever been a participant has no account cached, and the
	// picker drops a candidate it cannot name.
	if event.Joined {
		a.ensureAuthor("", event.UserID)
	}

	if event.ChannelID == a.currentChannelID {
		a.refreshMentionCandidates()
	}
}

// onUserRemoved drops what an account taken off the platform leaves behind.
//
// Everything of theirs has already gone from the store, conversations included,
// so the pruning here is of the app's own order — a channel ID the store no
// longer answers for would otherwise sit in the home view forever, drawing no
// row and never leaving the list.
func (a *App) onUserRemoved(event client.UserRemoved) {
	a.refreshAuthorMessages(event.UserID)

	a.dmChannels = slices.DeleteFunc(a.dmChannels, func(channelID string) bool {
		_, ok := a.store.Channel(channelID)
		return !ok
	})
	a.queueRefresh(refreshMembers)

	// The conversation in view can be one of the ones that went, and a selection
	// pointing at nothing is not something to leave until a window settles.
	if _, open := a.currentChannel(); a.currentChannelID != "" && !open {
		a.refreshChannelList()
		a.clearChannelSelection()
		a.showStatus("This conversation is no longer available")

		return
	}
	a.queueRefresh(refreshChannels)
}

// onUserUpdated redraws what a change to somebody's account moves: their mounted
// messages, and their row in the member sidebar.
//
// Neither reorders anything. A rename does move a sorted list, but a rename is
// rare and a row briefly out of order is far cheaper than rebuilding the whole
// model for one — the next rebuild puts it back.
func (a *App) onUserUpdated(event client.UserUpdated) {
	a.refreshAuthorMessages(event.UserID)
	a.refreshMemberRow(event.UserID)

	// The line may be naming them, or waiting to — but only if they are composing
	// here. Asking that is one map lookup, where redrawing the line unasked walks
	// the channel's typists and resolves each of them, per account update, on a
	// server where these arrive continuously.
	if _, typing := a.typing[a.currentChannelID][event.UserID]; typing {
		a.refreshTyping()
	}
}

// onRelationshipChanged reaches two surfaces. The friends list is the one that
// draws relationships as a set, and a request arriving is the only thing about
// it nobody asked for. The other is the open conversation: a direct message with
// somebody blocked is readable and nothing else, so the composer has to be
// re-asked whether it still takes a message.
//
// Nothing else is repainted — a profile does not refresh while it is up, and a
// member row says nothing about a relationship.
func (a *App) onRelationshipChanged(event client.RelationshipChanged) {
	a.friendsChanged(event.UserID)

	channel, ok := a.currentChannel()
	if !ok || channel.Kind != domain.ChannelDM {
		return
	}
	if !slices.Contains(channel.Recipients, event.UserID) {
		return
	}

	a.syncComposer()
}

// onPresenceChanged moves somebody between the member list's sections.
//
// This is the one event a busy server produces continuously, and it is the one
// that reorders, so it is queued rather than acted on: the rebuild is a walk of
// the whole membership, and a thousand-member server can raise dozens of these a
// second. Following presence at all is a setting, since on the largest servers
// the cheapest answer is not to.
//
// HasMember decides whether it is even our business, and exists so that asking
// allocates nothing.
func (a *App) onPresenceChanged(event client.PresenceChanged) {
	if !config.Current().Behaviour.LiveMemberPresence || a.currentServerID == "" {
		return
	}
	if !a.store.HasMember(a.currentServerID, event.UserID) {
		return
	}

	a.queueRefresh(refreshMembers)
}

// onMemberUpdated repaints the member sidebar when a member of the open server
// changes, and updates that author's mounted messages in place so their name,
// role colour and avatar stay current.
//
// When the member is the account itself, its roles are what every permission in
// the server is resolved from: a role gained or lost changes which channels are
// listed at all and whether the composer will take a message, neither of which
// any other event announces.
func (a *App) onMemberUpdated(event client.MemberUpdated) {
	if a.currentServerID == event.ServerID {
		// A nickname or a role can move them between sections and re-colour the
		// name, so it is the whole model rather than the one row.
		targets := refreshMembers
		if event.UserID == a.store.SelfID() {
			targets |= refreshChannels
			a.syncComposer()
		}
		a.queueRefresh(targets)
	}
	a.refreshAuthorMessages(event.UserID)
}
