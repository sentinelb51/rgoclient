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

	"RGOClient/internal/domain"
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
// Escape is claimed by whichever surface is topmost, which is what bindKeys
// decides — the settings page is underneath this layer and gets it back when the
// overlay goes.
func (a *App) mountOverlay(overlay *ui.Overlay) {
	a.closeOverlay()

	a.overlay = overlay
	a.window.Canvas().Overlays().Add(a.overlay)
	a.bindKeys()
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

	a.window.Canvas().Overlays().Remove(a.overlay)

	a.overlay = nil
	a.joinDialog = nil
	a.bindKeys()

	// The settings page has its own focus to keep; only the client underneath
	// wants the composer back.
	if a.settings == nil || !a.settings.IsOpen() {
		a.focusInput()
	}
}

/* Attachment viewer */

// showAttachmentViewer opens an attachment in the modal lightbox.
func (a *App) showAttachmentViewer(attachment *domain.File) {
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
	if !a.client.Connected() {
		return
	}

	dialog := ui.NewJoinServerDialog(a.joinServer, a.createServer, a.closeOverlay)
	a.showOverlay(dialog.Content)
	a.joinDialog = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// joinInvite redeems an invite code and reports the outcome to done, on the UI
// thread. It is shared by the two places a join starts from — the dialog and an
// invite card in a message — which differ only in where a failure is said.
//
// The joined server reaches the sidebar through the ServerJoined event rather
// than this response — see Client.JoinInvite for why the response cannot name it
// — so pendingJoin marks the request, telling onServerJoined this is the server
// to switch to.
func (a *App) joinInvite(code string, done func(err error)) {
	a.pendingJoin = true

	go func() {
		err := a.client.JoinInvite(code)

		a.doOnUI(func() {
			if err != nil {
				// The API's message ("bad status code 404: ...") is no use to the
				// user, so it goes to the log and the caller says what to do.
				log.Printf("join invite %s: %v", code, err)
				a.pendingJoin = false
			}
			done(err)
		}, false)
	}()
}

// joinServer redeems a code from the invite dialog, closing it once the server
// is in and reporting a failure on the dialog's own status line.
func (a *App) joinServer(code string) {
	a.joinInvite(code, func(err error) {
		if err == nil {
			a.closeOverlay()
			return
		}

		if a.joinDialog != nil {
			a.joinDialog.Fail("Could not join. Check the invite and try again.")
		}
	})
}

// OnJoinInvite redeems a code from an invite card in a message. There is no
// dialog to answer in, and no overlay to close on success — the sidebar simply
// gains the server — so only the failure is worth saying, on the notice layer.
func (a *App) OnJoinInvite(code string) {
	a.joinInvite(code, func(err error) {
		if err != nil {
			a.notify(ui.ToneWarning, "Could not join that server. The invite may have expired.")
		}
	})
}

// inviteResult is what a code resolved to. A failure is remembered alongside a
// success because an invite that has expired stays expired, and a card that
// re-asked on every scroll past its message would be a request per frame.
type inviteResult struct {
	invite domain.Invite
	err    error
}

// resetInvites drops every resolved invite and every card still waiting on one.
// The same code resolves differently for a different account — a server one is
// in and another is not — so none of it survives a change of session.
func (a *App) resetInvites() {
	a.invites = make(map[string]inviteResult)
	a.pendingInvites = make(map[string][]func(domain.Invite, error))
}

// ResolveInvite fills in what an invite code opens, from the session cache when
// it has been asked before and from the network when it has not.
func (a *App) ResolveInvite(code string, done func(domain.Invite, error)) {
	if cached, ok := a.invites[code]; ok {
		done(cached.invite, cached.err)
		return
	}

	if waiting, inFlight := a.pendingInvites[code]; inFlight {
		a.pendingInvites[code] = append(waiting, done)
		return
	}
	a.pendingInvites[code] = []func(domain.Invite, error){done}

	// Not through background: a failure here is an answer the card draws itself,
	// not a notice, so there is nothing for an onFail to do.
	epoch := a.epoch
	go func() {
		invite, err := a.client.FetchInvite(code)

		a.doOnUI(func() {
			waiting := a.pendingInvites[code]
			delete(a.pendingInvites, code)

			// A card from the previous account's view is gone along with the tree
			// it was in, and its answer must not be cached against this one.
			if a.stale(epoch) {
				return
			}

			a.invites[code] = inviteResult{invite: invite, err: err}
			for _, fill := range waiting {
				fill(invite, err)
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
