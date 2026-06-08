package app

import (
	"fmt"
	"log"

	"github.com/sentinelb51/revoltgo"
)

// onReady handles the initial gateway snapshot: it persists a pending token,
// records unread channels, and shows the main UI on the first server.
func (a *App) onReady(_ *revoltgo.Session, event *revoltgo.EventReady) {
	fmt.Printf("ready: %d user(s), %d server(s)\n", len(event.Users), len(event.Servers))

	a.savePendingToken()

	a.doOnUI(func() {
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

// savePendingToken persists the token captured during email/password login,
// now that the user's identity is known.
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

// onMessage caches an incoming message and either appends it to the open channel
// or marks the channel unread.
func (a *App) onMessage(_ *revoltgo.Session, event *revoltgo.EventMessage) {
	// Copy the message, as the event may be pooled and reused.
	message := event.Message
	a.messages.Append(event.Channel, &message)

	a.doOnUI(func() {
		if event.Channel == a.currentChannelID {
			a.appendMessage(&message)
			return
		}
		a.unreadChannels[event.Channel] = true
		a.syncChannelList()
	}, false)
}

// onError tears down an invalid session and returns to the login screen.
func (a *App) onError(_ *revoltgo.Session, event *revoltgo.EventError) {
	log.Printf("gateway error: %s", event.Data.Type)

	if event.Data.Type != revoltgo.EventErrorInvalidSession &&
		event.Data.Type != revoltgo.EventErrorInternalError {
		return
	}

	if a.session != nil {
		if self := a.session.State.Self(); self != nil {
			if err := RemoveSession(self.ID); err != nil {
				log.Printf("remove session: %v", err)
			}
		}
		_ = a.session.Close()
		a.session = nil
	}

	a.doOnUI(a.showLogin, true)
}
