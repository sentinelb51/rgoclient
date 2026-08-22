package app

// The event pump: one goroutine ranging the client's event stream, hopping onto
// the UI thread once per event. Everything arrives in the order it was produced,
// which is what keeps a burst of messages grouping correctly and a logout from
// racing a login. Nothing here talks to Revolt — a handler decides what the view
// should now look like.

import (
	"log"
	"slices"
	"time"

	"RGOClient/internal/audio"
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
// rebuild that walks something — every server icon, every channel against its
// permissions, every member, every emoji — so none is worth making once per
// event.
//
// Deliberately only these five. What an event changes about the *open* thing — a
// header, the channel glyph, whether the composer takes a message — is a setter
// and a lookup, and deferring those would feel slow to save nothing.
//
// refreshPresence is refreshMembers' cheap half: the same sidebar, rebuilt from
// the membership already resolved rather than from a fresh walk. Presence moves
// a member between sections and nothing else — not their name, so not the order
// — which is what lets the two be different amounts of work.
type refreshTarget uint8

const (
	refreshServers refreshTarget = 1 << iota
	refreshChannels
	refreshMembers
	refreshEmojis
	refreshPresence
)

// refreshDelay is how long a queued rebuild waits for more of the same burst.
func refreshDelay() time.Duration { return config.Current().Behaviour.RefreshDelay() }

// queueRefresh marks targets as needing a rebuild and arms the settling window,
// which is *not* restarted by what arrives inside it: a burst costs one rebuild,
// so the first event decides when it is drawn and everything behind joins it.
// Call on the UI thread.
//
// This is what makes realtime handling affordable. Revolt sends a rank reorder as
// one event per role, a new channel as a create *and* a server update, and
// presence on a large server continuously — all landing here as bits on a byte.
func (a *App) queueRefresh(targets refreshTarget) {
	a.dirty |= targets
	if a.refreshTimer != nil {
		return
	}

	a.refreshTimer = time.AfterFunc(refreshDelay(), func() {
		a.doOnUI(a.flushRefresh, false)
	})
}

// flushRefresh runs what the window gathered, outermost column first so each
// reads the state the one before it settled on. All of them re-read the store
// rather than anything the events carried, so a flush is correct whenever it
// happens to run. Call on the UI thread.
func (a *App) flushRefresh() {
	a.refreshTimer = nil

	targets := a.dirty
	a.dirty = 0

	if targets&refreshServers != 0 {
		a.refreshServerList()
	}
	if targets&refreshChannels != 0 {
		a.refreshChannelList()

		// The cog and the page it opens are drawn from the same server this rebuild
		// just re-read — and a role change is the one thing that moves what either
		// may offer without the selection moving at all.
		a.syncServerCog()
		a.refreshServerSettings()
	}
	if targets&refreshMembers != 0 {
		a.refreshMemberList()
	} else if targets&refreshPresence != 0 {
		// Only when the walk did not run: it resolved everybody's presence on the
		// way past, so patching the same people again would be the cheap half of a
		// rebuild that has already happened.
		a.refreshMemberPresence()
	}

	// Last, and skipped when the rail was rebuilt: which servers the account is in
	// is also which emoji it may type, so refreshServerList re-takes these itself
	// and a flush that ran it has already paid for this walk.
	if targets&refreshEmojis != 0 && targets&refreshServers == 0 {
		a.refreshEmojiCandidates()
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
		a.doOnUI(func() { a.onMessageUpdated(e) }, false)
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
	case client.EmojisChanged:
		a.doOnUI(a.onEmojisChanged, false)
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
	case client.VoiceChanged:
		a.doOnUI(func() { a.onVoiceChanged(e) }, false)
	}
}

/* Lifecycle */

// onReady handles the initial gateway snapshot: it persists a pending token,
// records unread channels, and shows the main UI on the first server.
func (a *App) onReady(event client.Ready) {
	a.stopAwaitingReady()
	a.savePendingToken()

	// Only a session that follows one this run already lost is worth a sound. The
	// first Ready of a launch is the client starting, which the user is watching.
	if a.reconnected {
		a.reconnected = false
		a.playSound(audio.Online)
	}

	for _, channelID := range event.UnreadChannelIDs {
		a.unreadChannels[channelID] = true
	}
	// Taken wholesale rather than merged: Ready is the account's whole read state,
	// and a reconnect must not carry over a mention the reader has since cleared.
	a.mentions = event.MentionIDs
	if a.mentions == nil {
		a.mentions = make(map[string][]string)
	}

	a.showMainUI()

	// Seed what a later user update is compared against, so the first one to name
	// this account is not read as a picture or a handle that has just changed.
	a.refreshSettingsAccount()

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
// a fatal drop reaches here — a flaky connection is the gateway's problem.
func (a *App) onDisconnected(event client.Disconnected) {
	if !event.Fatal {
		return
	}

	a.reconnected = true
	a.playSound(audio.Offline)

	if userID := a.store.SelfID(); userID != "" {
		if err := RemoveSession(userID); err != nil {
			log.Printf("remove session: %v", err)
		}
	}

	a.client.Close()
	a.showLogin()

	// The saved login has just been removed, so the one-click card is gone too —
	// saying why is what tells that from the client forgetting who was signed in.
	a.reportLogin("The server ended this session. Sign in again.")
}

/* Messages */

// onMessageCreated appends an incoming message to the open channel, acknowledging
// it as read, or marks its channel unread.
func (a *App) onMessageCreated(event client.MessageCreated) {
	channelID := event.Message.ChannelID

	// One of our own starts the cooldown. The send path already did for a message
	// composed here, so this covers one sent from another client.
	if event.Message.AuthorID == a.store.SelfID() {
		a.startSlowmode(channelID)
	}

	// Sending is the end of typing and Revolt does not reliably say so, so left to
	// lapse the line would name somebody under the message they just posted.
	a.forgetTyping(channelID, event.Message.AuthorID)

	a.alertMessage(event.Message)

	// The open channel is acknowledged as it arrives, which is what clears the
	// mention on Revolt's side too, so nothing here is recorded for one.
	if channelID == a.currentChannelID {
		a.appendMessage(event.Message, event.Previous)
		a.scheduleAck(channelID, event.Message.ID)
		return
	}

	a.unreadChannels[channelID] = true
	a.recordMention(event.Message)
	a.refreshChannelRow(channelID)
}

// onMessageUpdated repaints a message the cache has already replaced. The event
// carries who reacted when that is what moved, which is the only kind of update
// worth announcing — and the only one the reader could not otherwise tell apart,
// an edit and a reaction both arriving as the same "this message changed".
func (a *App) onMessageUpdated(event client.MessageUpdated) {
	a.refreshMessage(event.ChannelID, event.MessageID)

	if event.ReactedBy != "" {
		a.alertReaction(event.ChannelID, event.MessageID, event.ReactedBy)
	}
}

/* Read acknowledgement */

// scheduleAck records messageID as channelID's newest seen and acknowledges it
// after ackDelay, coalescing bursts into one request. An ack pending for a
// *different* channel — the user switched inside the window — is flushed at once
// rather than lost to the overwrite. Call on the UI thread.
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

// onServerJoined adds a newly joined server, keeping the app's ordered list in
// step with the store. Selecting it is conditional on pendingJoin: the invite
// dialog is the one path where the user asked to go there, and a server appearing
// for any other reason must not yank the view out of what they are reading.
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

	// Immediate rather than queued: the page's every section is about a server the
	// account is no longer in, and a settling window would leave it up saying so.
	if a.serverSettingsID == event.ServerID {
		a.closeServerSettings()
	}

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

// onServerUpdated repaints what a server's details are drawn into: its icon, the
// header, and the channel list — the same event carries a re-ordered category or
// a channel moved between two. Revolt sends it alongside the create or delete
// that caused it, so both rebuilds are queued: acting on each as it lands would
// rebuild the sidebar twice for one change.
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
// rank and permissions, so this is the one event that can repaint every name in
// the member list, re-sort it, *and* change which channels are listed. Queued
// rather than made: a rank reorder arrives as one event per role, and creating a
// role arrives as an update for one State has never heard of.
func (a *App) onRolesChanged(event client.RolesChanged) {
	// A role created here is drawn from the store, so the event is the first
	// moment the page can open the one it just made — and the page is about a
	// server whether or not it is the one selected.
	a.openPendingRole(event.ServerID)

	if a.currentServerID != event.ServerID {
		return
	}

	a.queueRefresh(refreshChannels | refreshMembers)
	a.syncComposer()
}

// onChannelCreated adds a channel that now exists: one added to the open server,
// or a conversation opened from another client. A conversation is prepended
// rather than sorted in — it has no messages yet, so no LastMessageID to sort by.
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
	a.dmChannels = slices.Insert(a.dmChannels, 0, event.ChannelID)

	if a.homeSelected {
		a.queueRefresh(refreshChannels)
	}
}

// onChannelUpdated repaints a channel whose name, icon or permissions changed.
// The whole sidebar rather than the one row: a permission overwrite is among what
// this announces, and ViewChannel decides whether the channel is a row at all —
// so one can have to appear or disappear, which repainting in place cannot do.
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
	a.syncChannelKind()
	a.syncChannelTopic()
	a.syncComposer()

	// The overwrites may have taken the channel away, in which case the page under
	// it is no longer ours to show.
	if !a.canViewChannel(channel) {
		a.showStatus("You don't have access to this channel")
	}
}

// onChannelRead clears a channel's unread mark when the account acknowledged it
// somewhere else. Our own acks echo back here too, where they land on a mark
// already cleared and cost one map delete.
func (a *App) onChannelRead(event client.ChannelRead) {
	// Pruned rather than cleared, matching what the ack does to the account's own
	// record: Revolt drops the mentions as far as the message acknowledged and no
	// further, so one that arrived past it is still standing.
	pruned := a.pruneMentions(event.ChannelID, event.MessageID)

	if !a.unreadChannels[event.ChannelID] {
		if pruned {
			a.refreshChannelRow(event.ChannelID)
		}

		return
	}

	delete(a.unreadChannels, event.ChannelID)
	a.refreshChannelRow(event.ChannelID)
}

// onChannelClosed drops a closed conversation or a deleted server channel. Both
// arrive the same way, so only the home view's own ordering needs maintaining.
func (a *App) onChannelClosed(event client.ChannelClosed) {
	delete(a.unreadChannels, event.ChannelID)
	a.clearMentions(event.ChannelID)
	if i := slices.Index(a.dmChannels, event.ChannelID); i >= 0 {
		a.dmChannels = slices.Delete(a.dmChannels, i, i+1)
	}

	if a.currentChannelID != event.ChannelID {
		a.queueRefresh(refreshChannels)
		return
	}

	// The channel in view is about to stop existing, so its row goes now rather than
	// at the end of a window: what follows selects another, and selection is painted
	// onto the rows the sidebar is holding.
	a.refreshChannelList()

	a.clearChannelSelection()
	if a.homeSelected && len(a.dmChannels) > 0 {
		a.selectChannel(a.dmChannels[0])
	}
}

// onMembersChanged repaints the member sidebar when the open server's membership
// changes. A join arrives as a membership and nothing else — revoltgo fabricates
// one with no account attached — so the row would read "Unknown user" until
// ensureAuthor asked who it was; its own batch refreshes the sidebar when it lands.
//
// When the member is *us*, this is the only announcement that we have left:
// Revolt reports a leave to the server rather than a deletion to the leaver, and
// revoltgo evicts the server from State on the strength of it. Hence the hand-off
// to the leave path, a no-op for a server already gone from the list.
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

// onRecipientsChanged follows a group's own membership. A group is the one
// channel whose participants are a list the client reads — they are the pool the
// @mention picker offers there — so a join or leave has to reach the picker as a
// server's would reach the member sidebar. Us leaving is the group leaving the
// sidebar, announced as any other participant change, so the close path is
// reached from here rather than from a deletion nobody is sent.
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
// Everything of theirs has already gone from the store, so the pruning here is of
// the app's *own* order — a channel ID the store no longer answers for would sit
// in the home view forever, drawing no row and never leaving the list.
func (a *App) onUserRemoved(event client.UserRemoved) {
	a.refreshAuthorMessages(event.UserID)

	a.dmChannels = slices.DeleteFunc(a.dmChannels, func(channelID string) bool {
		_, ok := a.store.Channel(channelID)
		return !ok
	})
	a.queueRefresh(refreshMembers)

	// The conversation in view can be one of the ones that went, and a selection
	// pointing at nothing must not wait for a window to settle.
	if _, open := a.currentChannel(); a.currentChannelID != "" && !open {
		a.refreshChannelList()
		a.clearChannelSelection()
		a.showStatus("This conversation is no longer available")

		return
	}
	a.queueRefresh(refreshChannels)
}

// onUserUpdated redraws what a change to an account moves: their mounted
// messages and their member row. Neither reorders: a rename does move a sorted
// list, but it is rare, and a row briefly out of order is far cheaper than
// rebuilding the model for one — the next rebuild puts it back.
func (a *App) onUserUpdated(event client.UserUpdated) {
	a.refreshAuthorMessages(event.UserID)
	a.refreshMemberRow(event.UserID)

	// The settings page names this account and can be open while the change is made
	// from it.
	if event.UserID == a.store.SelfID() {
		a.refreshSettingsAccount()
	}

	// Only if they are composing here. Asking is one map lookup, where redrawing the
	// line unasked walks the channel's typists and resolves each — per account
	// update, on a server where these arrive continuously.
	if _, typing := a.typing[a.currentChannelID][event.UserID]; typing {
		a.refreshTyping()
	}
}

// onRelationshipChanged reaches two surfaces: the friends list, which draws
// relationships as a set, and the open conversation — a DM with somebody blocked
// is readable and nothing else, so the composer is re-asked whether it still
// takes a message. Nothing else repaints: a profile does not refresh while it is
// up, and a member row says nothing about a relationship.
func (a *App) onRelationshipChanged(event client.RelationshipChanged) {
	a.friendsChanged(event.UserID)
	a.alertRelationship(event.UserID)

	channel, ok := a.currentChannel()
	if !ok || channel.Kind != domain.ChannelDM {
		return
	}
	if !slices.Contains(channel.Recipients, event.UserID) {
		return
	}

	a.syncComposer()
}

// onPresenceChanged moves somebody between the member list's sections. The one
// event a busy server produces continuously *and* the one that resections, so it
// is queued: a thousand-member server raises dozens a second. Following presence
// at all is a setting, the cheapest answer on the largest servers being not to.
// HasMember decides whether it is our business, and exists so that asking
// allocates nothing.
//
// It names who moved rather than queueing the walk, because presence is the one
// change a membership already resolved can absorb: nobody's name moves, so the
// order stands and only these people need resolving again.
func (a *App) onPresenceChanged(event client.PresenceChanged) {
	if !config.Current().Behaviour.LiveMemberPresence || a.currentServerID == "" {
		return
	}
	if !a.store.HasMember(a.currentServerID, event.UserID) {
		return
	}

	if a.presenceDirty == nil {
		a.presenceDirty = make(map[string]bool)
	}
	a.presenceDirty[event.UserID] = true

	a.queueRefresh(refreshPresence)
}

// onMemberUpdated repaints the member sidebar and that author's mounted messages,
// so their name, role colour and avatar stay current. When the member is the
// account itself, its roles are what every permission is resolved from: one
// gained or lost changes which channels are listed and whether the composer takes
// a message, neither of which any other event announces.
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

// onVoiceChanged redraws the channel sidebar when a call gained or lost somebody,
// or when a participant's camera or screen share went on or off: those people are
// rows under their voice channel, so who is in a call is part of what a rebuild of
// that column draws.
//
// Guarded on the server rather than the channel, and on both ends of a move: a
// call in a server that is not open draws nothing here, and voice events arrive
// for every server the account is in.
func (a *App) onVoiceChanged(event client.VoiceChanged) {
	if !a.drawsCall(event.ChannelID) && !a.drawsCall(event.FromChannelID) {
		return
	}

	a.queueRefresh(refreshChannels)
}

// drawsCall reports whether a voice channel's participants are on screen — the
// channel sidebar's, so the open server's and not the home view's, which lists
// conversations rather than voice channels.
func (a *App) drawsCall(channelID string) bool {
	if channelID == "" || a.homeSelected || a.currentServerID == "" {
		return false
	}

	return a.channelServerID(channelID) == a.currentServerID
}
