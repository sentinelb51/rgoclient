package app

import (
	"log"

	"github.com/sentinelb51/revoltgo"
)

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

// savePendingToken persists the token captured during email/password login,
// now that the user's identity is known. Call on the UI thread: it reads
// a.session and a.pendingToken, which are confined there.
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
