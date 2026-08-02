package app

// The modal layer: one overlay at a time over the main window, holding the
// attachment lightbox, the join-server dialog, a confirmation, or a profile.
// They are overlays rather than windows because there is no native chrome to
// recolour, they cannot be left behind, and they cannot drift off-centre.
//
// A profile card is the one that is anchored rather than centred (showPopover),
// which is the only difference between the two ways in.

import (
	"log"

	"fyne.io/fyne/v2"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

/* The layer itself */

// showOverlay puts content on the modal layer, replacing whatever was there.
// While it is up, Esc closes it: Fyne gives each overlay its own focus manager,
// so with nothing in the overlay focused, key events reach the canvas handler
// instead of the composer underneath. Content that does take focus (the invite
// dialog's entry) has to handle Esc itself, since Fyne routes keys to the focused
// widget and never calls this handler. Call on the UI thread.
func (a *App) showOverlay(content fyne.CanvasObject) {
	a.mountOverlay(ui.NewOverlay(content, a.closeOverlay))
}

// showPopover puts content on the modal layer beside anchor, undimmed — for a
// card that belongs to the widget it points at rather than to the window. Call
// on the UI thread.
func (a *App) showPopover(content, anchor fyne.CanvasObject) {
	a.mountOverlay(ui.NewPopover(content, anchor, a.closeOverlay))
}

// mountOverlay replaces the modal layer with overlay and takes the keyboard.
func (a *App) mountOverlay(overlay *ui.Overlay) {
	a.closeOverlay()

	canvas := a.window.Canvas()
	a.overlay = overlay
	canvas.Overlays().Add(a.overlay)
	canvas.SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			a.closeOverlay()
		}
	})
}

// repositionOverlay re-places what is on the modal layer after it changed size,
// as a profile card does when its bio arrives. Call on the UI thread.
func (a *App) repositionOverlay() {
	if a.overlay != nil {
		a.overlay.Reposition()
	}
}

// closeOverlay dismisses the modal layer and hands the keyboard back. Safe to
// call when nothing is showing. Call on the UI thread.
func (a *App) closeOverlay() {
	if a.overlay == nil {
		return
	}

	canvas := a.window.Canvas()
	canvas.Overlays().Remove(a.overlay)
	canvas.SetOnTypedKey(nil)

	a.overlay = nil
	a.joinDialog = nil
	a.focusInput()
}

/* Attachment viewer */

// showAttachmentViewer opens an attachment in the modal lightbox.
func (a *App) showAttachmentViewer(attachment *revoltgo.File) {
	a.showOverlay(ui.NewAttachmentViewer(a.deps(), attachment, a.viewerBounds(), a.closeOverlay))
}

// viewerBounds is the largest card the viewer may build: the window minus a
// margin, capped so the modal still reads as a card on a maximised window.
func (a *App) viewerBounds() fyne.Size {
	size := a.window.Canvas().Size()
	margin := 2 * theme.Sizes.ViewerMargin

	return fyne.NewSize(
		max(min(size.Width-margin, theme.Sizes.ViewerMaxWidth), theme.Sizes.ViewerMinWidth),
		max(min(size.Height-margin, theme.Sizes.ViewerMaxHeight), theme.Sizes.ViewerMinHeight),
	)
}

/* Joining a server */

// showJoinServer opens the join-by-invite modal.
func (a *App) showJoinServer() {
	if a.session == nil {
		return
	}

	dialog := ui.NewJoinServerDialog(a.joinServer, a.createServer, a.closeOverlay)
	a.showOverlay(dialog.Content)
	a.joinDialog = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// joinServer redeems an invite code, closing the dialog once the server is in.
//
// The joined server reaches the sidebar through the ServerCreate gateway event
// rather than this response: the join payload carries the server as an object,
// and revoltgo decodes it into an Invite whose ServerID comes from a "server_id"
// field that payload never sets. pendingJoin therefore marks the request, so
// onServerCreate knows this is the server to switch to.
func (a *App) joinServer(code string) {
	session := a.session
	if session == nil {
		return
	}
	a.pendingJoin = true

	go func() {
		_, err := session.InviteJoin(code)

		a.doOnUI(func() {
			if err == nil {
				a.closeOverlay()
				return
			}

			// The API's message ("bad status code 404: ...") is no use to the user,
			// so it goes to the log and the dialog says what to do.
			log.Printf("join invite %s: %v", code, err)
			a.pendingJoin = false
			if a.joinDialog != nil {
				a.joinDialog.Fail("Could not join. Check the invite and try again.")
			}
		}, false)
	}()
}

// createServer is the dialog's "Create a server" action.
func (a *App) createServer() {
	// todo: server creation (ServerCreate + name/icon form)
	log.Print("create server requested")

	if a.joinDialog != nil {
		a.joinDialog.Notice("Creating a server isn't available yet.")
	}
}
