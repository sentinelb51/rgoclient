package app

import (
	"log"

	"RGOClient/internal/ui"
)

// showJoinServer opens the join-by-invite modal over the main window. Like the
// attachment viewer it is an overlay rather than a window: there is no native
// chrome to recolour, and it cannot be left behind.
func (a *App) showJoinServer() {
	if a.session == nil {
		return
	}

	dialog := ui.NewJoinServerDialog(a.joinServer, a.createServer, a.closeOverlay)
	a.showOverlay(dialog.Content)
	a.joinDialog = dialog // after showOverlay: closeOverlay clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// createServer is the dialog's "Create a server" action. Server creation isn't
// built yet, so it says so on the status line rather than doing nothing at all.
func (a *App) createServer() {
	// todo: server creation (ServerCreate + name/icon form)
	log.Print("create server requested")
	if a.joinDialog != nil {
		a.joinDialog.Notice("Creating a server isn't available yet.")
	}
}

// joinServer redeems an invite code, closing the dialog once the server is in.
//
// The joined server reaches the sidebar through the ServerCreate gateway event
// rather than this response: the join payload carries the server as an object,
// and revoltgo decodes it into an Invite whose ServerID comes from a
// "server_id" field that payload never sets. pendingJoin therefore marks the
// request, so onServerCreate knows this is the server to switch to.
func (a *App) joinServer(code string) {
	session := a.session
	if session == nil {
		return
	}
	a.pendingJoin = true

	go func() {
		_, err := session.InviteJoin(code)
		a.doOnUI(func() {
			if err != nil {
				// The API's message ("bad status code 404: ...") is no use to the
				// user, so it goes to the log and the dialog says what to do.
				log.Printf("join invite %s: %v", code, err)
				a.pendingJoin = false
				if a.joinDialog != nil {
					a.joinDialog.Fail("Could not join. Check the invite and try again.")
				}
				return
			}
			a.closeOverlay()
		}, false)
	}()
}
