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
	"RGOClient/internal/ui"
)

// ackDelay is the coalescing window for read acknowledgements of the open
// channel: a burst of incoming messages produces one ack for the newest of them
// instead of one request per message.
const ackDelay = time.Second

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
	case client.ChannelClosed:
		a.doOnUI(func() { a.onChannelClosed(e) }, false)
	case client.MembersChanged:
		a.doOnUI(func() { a.onMembersChanged(e) }, false)
	case client.MemberUpdated:
		a.doOnUI(func() { a.onMemberUpdated(e) }, false)
	}
}

/* Lifecycle */

// onReady handles the initial gateway snapshot: it persists a pending token,
// records unread channels, and shows the main UI on the first server.
func (a *App) onReady(event client.Ready) {
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
}

/* Messages */

// onMessageCreated appends an incoming message to the open channel, acknowledging
// it as read, or marks its channel unread.
func (a *App) onMessageCreated(event client.MessageCreated) {
	channelID := event.Message.ChannelID

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

	a.ackTimer = time.AfterFunc(ackDelay, func() {
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

// onChannelClosed drops a closed conversation or a deleted server channel. Both
// arrive the same way, so the sidebar is rebuilt either way and only the home
// view's own ordering needs maintaining.
func (a *App) onChannelClosed(event client.ChannelClosed) {
	delete(a.unreadChannels, event.ChannelID)
	if i := slices.Index(a.dmChannels, event.ChannelID); i >= 0 {
		a.dmChannels = slices.Delete(a.dmChannels, i, i+1)
	}
	a.refreshChannelList()

	if a.currentChannelID != event.ChannelID {
		return
	}

	a.clearChannelSelection()
	if a.homeSelected && len(a.dmChannels) > 0 {
		a.selectChannel(a.dmChannels[0])
	}
}

// onMembersChanged repaints the member sidebar when the open server's membership
// changes.
func (a *App) onMembersChanged(event client.MembersChanged) {
	if a.currentServerID == event.ServerID {
		a.refreshMemberList()
	}
}

// onMemberUpdated repaints the member sidebar when a member of the open server
// changes, and updates that author's mounted messages in place so their name,
// role colour and avatar stay current.
func (a *App) onMemberUpdated(event client.MemberUpdated) {
	if a.currentServerID == event.ServerID {
		a.refreshMemberList()
	}
	a.refreshAuthorMessages(event.UserID)
}

// notifyFailure is the standard failure handler for an action whose only visible
// outcome is a notice: the API error goes to the log, the user gets a sentence.
func (a *App) notifyFailure(what string, format string, args ...any) func(error) {
	return func(err error) {
		log.Printf("%s: %v", what, err)
		a.notify(ui.ToneDanger, format, args...)
	}
}
