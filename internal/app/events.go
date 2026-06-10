package app

// Gateway event handlers are grouped by domain across sibling files —
// events_ready.go, events_message.go, events_members.go — and registered in
// session.go's openSession. This file holds the gateway lifecycle handler.

import (
	"log"

	"github.com/sentinelb51/revoltgo"
)

// onError tears down an invalid session and returns to the login screen.
func (a *App) onError(_ *revoltgo.Session, event *revoltgo.EventError) {
	log.Printf("gateway error: %s", event.Data.Type)

	if event.Data.Type != revoltgo.EventErrorInvalidSession &&
		event.Data.Type != revoltgo.EventErrorInternalError {
		return
	}

	// a.session is only ever written on the UI thread (see openSession), so the
	// teardown runs there too; worker goroutines capture the session before they
	// launch and at worst call into a closed session, which just errors.
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
