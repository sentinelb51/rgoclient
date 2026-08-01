package app

// Every gateway handler lives here and is registered in session.go's
// openSession. revoltgo's own default handlers run first and keep State in sync,
// so these only maintain the app's own view state and repaint.

import (
	"log"
	"slices"
	"time"

	"github.com/sentinelb51/revoltgo"
)

// ackDelay is the coalescing window for read acknowledgements of the open
// channel: a burst of incoming messages produces one MessageAck for the newest
// of them instead of one request per message.
const ackDelay = time.Second

/* Lifecycle */

// onReady handles the initial gateway snapshot: it persists a pending token,
// records unread channels, and shows the main UI on the first server.
func (a *App) onReady(_ *revoltgo.Session, event *revoltgo.EventReady) {
	log.Printf("ready: %d user(s), %d server(s)", len(event.Users), len(event.Servers))

	a.doOnUI(func() {
		a.savePendingToken()

		for _, unread := range event.ChannelUnreads {
			a.unreadChannels[unread.ID.Channel] = true
		}

		a.showMainUI()

		a.serverIDs = make([]string, 0, len(event.Servers))
		for _, server := range event.Servers {
			a.serverIDs = append(a.serverIDs, server.ID)
		}
		a.refreshServerList()

		if len(a.serverIDs) > 0 {
			a.selectServer(a.serverIDs[0])
		}
	}, true)
}

// savePendingToken persists the token captured during a credential login, now
// that the user's identity is known. Call on the UI thread.
func (a *App) savePendingToken() {
	if a.pendingToken == "" {
		return
	}

	self := a.session.State.Self()
	if self == nil {
		return
	}

	saved := SavedSession{Token: a.pendingToken, UserID: self.ID, Username: self.Username}
	if self.Avatar != nil {
		saved.AvatarID = self.Avatar.ID
	}
	if err := AddOrUpdateSession(saved); err != nil {
		log.Printf("save session: %v", err)
	}

	a.pendingToken = ""
}

// onError tears down an invalid session and returns to the login screen.
func (a *App) onError(_ *revoltgo.Session, event *revoltgo.EventError) {
	log.Printf("gateway error: %s", event.Data.Type)

	if event.Data.Type != revoltgo.EventErrorInvalidSession &&
		event.Data.Type != revoltgo.EventErrorInternalError {
		return
	}

	// a.session is only ever written on the UI thread (see openSession), so the
	// teardown runs there too; worker goroutines capture the session before they
	// launch and at worst call into a closed one, which just errors.
	a.doOnUI(func() {
		if a.session != nil {
			if self := a.session.State.Self(); self != nil {
				if err := RemoveSession(self.ID); err != nil {
					log.Printf("remove session: %v", err)
				}
			}
			_ = a.session.Close()
			a.session = nil
		}
		a.showLogin()
	}, true)
}

/* Messages */

// onMessage caches an incoming message and either appends it to the open channel,
// acknowledging it as read, or marks the channel unread.
func (a *App) onMessage(_ *revoltgo.Session, event *revoltgo.EventMessage) {
	// Capture the predecessor at cache time: under a burst several messages can be
	// cached before their UI callbacks run, so deriving it later would group
	// against the wrong message.
	prev := a.messages.Append(event.Channel, &event.Message)

	a.doOnUI(func() {
		if event.Channel == a.currentChannelID {
			a.appendMessage(&event.Message, prev)
			a.scheduleAck(event.Channel, event.Message.ID)
			return
		}

		a.unreadChannels[event.Channel] = true
		a.refreshChannelRow(event.Channel)
	}, false)
}

// onMessageUpdate applies an edit to the cached message and rebuilds its mounted
// widget. Cached messages are immutable, since the UI reads them without the
// cache lock, so the edit goes onto a copy that replaces the original.
func (a *App) onMessageUpdate(_ *revoltgo.Session, event *revoltgo.EventMessageUpdate) {
	current := a.messages.Find(event.Channel, event.ID)
	if current == nil {
		return
	}

	updated := *current
	if event.Data.Content != "" {
		updated.Content = event.Data.Content
	}
	if event.Data.Edited != nil {
		updated.Edited = event.Data.Edited
	}
	if len(event.Data.Embeds) > 0 {
		updated.Embeds = event.Data.Embeds
	}
	a.messages.Replace(event.Channel, &updated)

	a.doOnUI(func() { a.refreshMessage(event.Channel, event.ID) }, false)
}

// onMessageDelete drops a deleted message from the cache and unmounts its widget
// when the channel is open.
func (a *App) onMessageDelete(_ *revoltgo.Session, event *revoltgo.EventMessageDelete) {
	a.messages.Remove(event.Channel, event.ID)
	a.doOnUI(func() { a.removeMessage(event.Channel, event.ID) }, false)
}

// onBulkMessageDelete handles a moderation sweep deleting several at once.
func (a *App) onBulkMessageDelete(_ *revoltgo.Session, event *revoltgo.EventBulkMessageDelete) {
	for _, id := range event.IDs {
		a.messages.Remove(event.Channel, id)
	}

	a.doOnUI(func() {
		for _, id := range event.IDs {
			a.removeMessage(event.Channel, id)
		}
	}, false)
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

// sendAck fires a MessageAck request in the background. Call on the UI thread.
func (a *App) sendAck(channelID, messageID string) {
	session := a.session
	if session == nil {
		return
	}

	go func() {
		if err := session.MessageAck(channelID, messageID); err != nil {
			log.Printf("ack channel %s: %v", channelID, err)
		}
	}()
}

/* Servers and members */

// onServerCreate adds a newly joined server to the sidebar, keeping the app's own
// ordered server list in step with State.
//
// Selecting it is deliberately conditional on pendingJoin: the invite dialog is
// the one path where the user asked to go there, and a server appearing for any
// other reason must not yank the view out of the channel they are reading.
func (a *App) onServerCreate(_ *revoltgo.Session, event *revoltgo.EventServerCreate) {
	log.Printf("joined server %s", event.ID)

	a.doOnUI(func() {
		selecting := a.pendingJoin
		a.pendingJoin = false

		if !slices.Contains(a.serverIDs, event.ID) {
			a.serverIDs = append(a.serverIDs, event.ID)
			a.refreshServerList()
		}
		if selecting {
			a.selectServer(event.ID)
		}
	}, false)
}

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

// onServerMemberUpdate refreshes the member list when a member of the open server
// changes, and updates that author's mounted messages in place so their name,
// role colour, and avatar stay current.
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
