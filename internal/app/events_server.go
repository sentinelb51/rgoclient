package app

import (
	"log"
	"slices"

	"github.com/sentinelb51/revoltgo"
)

// onServerCreate adds a newly joined server to the sidebar. State already holds
// it — revoltgo's default handlers run before ours — so this only keeps the
// app's own ordered server list in step.
//
// Selecting the new server is deliberately conditional on pendingJoin: the
// invite dialog is the one path where the user asked to go there, and a server
// appearing for any other reason (added elsewhere, another client) must not
// yank the view out of the channel they are reading.
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
