package app

// The modal layer: one overlay at a time over the main window — the attachment
// lightbox, the join dialog, a confirmation, a profile. Overlays rather than
// windows: no native chrome to recolour, nothing to leave behind, nothing that
// can drift off-centre. A profile card is the one anchored rather than centred
// (showPopover), which is the only difference between the two ways in.

import (
	"errors"
	"log"
	"time"

	"fyne.io/fyne/v2"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

/* The layer itself */

// showOverlay puts content on the modal layer, replacing what was there. While it
// is up Esc closes it: Fyne gives each overlay its own focus manager, so with
// nothing focused inside, keys reach the canvas handler rather than the composer.
// Content that *does* take focus handles Esc itself. Call on the UI thread.
func (a *App) showOverlay(content fyne.CanvasObject) {
	a.mountOverlay(ui.NewOverlay(content, a.closeOverlay))
}

// showPopover puts content on the modal layer beside anchor, undimmed — for a
// card that belongs to the widget it points at rather than to the window. Call
// on the UI thread.
func (a *App) showPopover(content, anchor fyne.CanvasObject) {
	a.mountOverlay(ui.NewPopover(content, anchor, a.closeOverlay))
}

// mountOverlay replaces the modal layer and takes the keyboard. Escape belongs to
// whichever surface is topmost, which bindKeys decides — the settings page is
// under this layer and gets it back when the overlay goes.
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
	a.window.Canvas().RemoveShortcut(copyShortcut)

	a.overlay = nil
	a.joinDialog = nil
	a.prompt = nil
	a.channelDialog = nil
	a.closeFriends()
	a.closePins()
	a.closeSearch()
	a.bindKeys()

	// The settings page has its own focus to keep; only the client underneath
	// wants the composer back.
	if a.settings == nil || !a.settings.IsOpen() {
		a.focusInput()
	}
}

/* Attachment viewer */

// showAttachmentViewer opens an attachment in the modal lightbox.
//
// Ctrl+C is bound on the canvas rather than by the card: a shortcut reaches a
// focused widget first, and the card focuses nothing — except the text pane,
// which keeps the copy for its own selection. closeOverlay unbinds it.
func (a *App) showAttachmentViewer(attachment *domain.File) {
	viewer := ui.NewAttachmentViewer(a.deps(), attachment, a.viewerBounds(), a.closeOverlay)

	a.showOverlay(viewer.Content)
	a.window.Canvas().AddShortcut(copyShortcut, func(fyne.Shortcut) { viewer.Copy() })
}

// copyShortcut is the binding the lightbox takes while it is up. Shortcuts are
// keyed by name, so the same value adds and removes it.
var copyShortcut = &fyne.ShortcutCopy{}

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

// joinInvite redeems a code and reports to done on the UI thread. Shared by the
// two places a join starts from — the dialog and an invite card — which differ
// only in where a failure is said. The joined server reaches the sidebar through
// the ServerJoined event rather than this response (see Client.JoinInvite), so
// pendingJoin marks the request as the one to switch to.
func (a *App) joinInvite(code string, done func(err error)) {
	a.pendingJoin = true

	go func() {
		err := a.client.JoinInvite(code)

		a.doOnUI(func() {
			if err != nil {
				// The API's message ("bad status code 404: …") is no use to the user, so
				// it goes to the log and the caller says what to do.
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
			a.joinDialog.Fail("Could not join. Check the invite and retry.")
		}
	})
}

// OnJoinInvite redeems a code from an invite card. No dialog to answer in and no
// overlay to close — the sidebar simply gains the server — so only the failure is
// worth saying, on the notice layer.
func (a *App) OnJoinInvite(code string) {
	a.joinInvite(code, func(err error) {
		if err != nil {
			a.notify(ui.ToneWarning, "Could not join that server. The invite may have expired.")
		}
	})
}

// inviteResult is what a code resolved to. Failures are remembered too: an
// expired invite stays expired, and a card re-asking on every scroll past its
// message would be a request per frame.
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

			// A card from the previous account's view is gone with the tree it was in,
			// and its answer must not be cached against this one.
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

/* Creating a server */

// createServer replaces the join dialog with the one asking for a name — replaces
// rather than stacks, the modal layer holding one card and the two being the same
// question asked of a server that exists and one that does not. A name is all
// Revolt takes at creation, so the card is one field.
func (a *App) createServer() {
	if !a.client.Connected() {
		return
	}

	dialog := ui.NewPromptDialog(ui.Prompt{
		Title:  "Create a server",
		Action: "Create",
		Busy:   "Creating...",
		Fields: []ui.PromptField{{Label: "Server name", Placeholder: "My server"}},
		OnSubmit: func(values []string) {
			a.submitServer(values[0])
		},
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.prompt = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// submitServer creates the server and leaves the dialog up until it exists. The
// server arrives on the gateway rather than in the response (see
// Client.CreateServer), so pendingJoin selects it — the same mark a join leaves.
func (a *App) submitServer(name string) {
	a.pendingJoin = true
	epoch := a.epoch

	go func() {
		err := a.client.CreateServer(name)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err == nil {
				a.closeOverlay()
				return
			}

			log.Printf("create server: %v", err)
			a.pendingJoin = false

			if a.prompt != nil {
				a.prompt.Fail(createServerFailure(err))
			}
		}, false)
	}()
}

// createServerFailure is what the card says about a refusal. Only one is worth
// naming: an empty name is the reader's to fix, and the rest are status codes
// they can do nothing with.
func createServerFailure(err error) string {
	if errors.Is(err, client.ErrServerNameEmpty) {
		return "Give the server a name."
	}

	return "Could not create that server."
}

/* Editing a channel */

// canEditChannel reports whether the account may change what a channel is. One
// question answers the menu item and the request alike: Stoat's channel_edit
// route checks ManageChannel once, for the whole edit, and gates no field behind
// anything further.
//
// A direct message and saved notes are left out by kind rather than by
// permission — the conversation grant does carry ManageChannel, but their names
// are the client's own invention (see Store.Channel) and there is nothing on
// either that an edit could reach.
func (a *App) canEditChannel(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Kind == domain.ChannelDM || channel.Kind == domain.ChannelSavedMessages {
		return false
	}

	return a.store.Permissions(channelID).Has(domain.PermissionManageChannel)
}

// editChannel raises the card that changes what a channel is.
//
// The cooldown is read before the card goes up, and only then: revoltgo drops the
// field from the channel it caches (see Client.FetchSlowmode), so the store's zero
// means "none" and "never asked" alike — and a card opened on that zero is one
// that clears a slowmode the moment it is saved. A channel whose cooldown cannot
// be read is offered without the row rather than with a wrong one. A group has no
// cooldown to ask about.
func (a *App) editChannel(channelID string) {
	if !a.canEditChannel(channelID) {
		return
	}

	channel, _ := a.store.Channel(channelID)
	if channel.Kind == domain.ChannelGroup {
		a.showChannelDialog(channelID, nil)
		return
	}

	epoch := a.epoch
	go func() {
		slowmode, err := a.client.FetchSlowmode(channelID)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("fetch slowmode for %s: %v", channelID, err)
				a.showChannelDialog(channelID, nil)
				return
			}

			a.showChannelDialog(channelID, &slowmode)
		}, false)
	}()
}

// showChannelDialog puts the card up on what the channel is now, offering the
// fields its kind has: the cooldown when one could be read, and the user limit on
// a voice channel. A nil field is a row the card leaves out and one the request
// omits — see client.ChannelEdit, where `voice` sent to a channel that is not a
// voice channel is what would turn it into one.
//
// The permission is asked again here rather than trusted from the menu: the menu
// is built per click, but a role can be taken away while the request above is out.
// Call on the UI thread.
func (a *App) showChannelDialog(channelID string, slowmode *time.Duration) {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canEditChannel(channelID) {
		return
	}

	current := ui.ChannelSettings{
		Name:        channel.Name,
		Description: channel.Description,
		Slowmode:    slowmode,
		NSFW:        channel.NSFW,
	}
	if channel.Kind == domain.ChannelVoice {
		current.UserLimit = &channel.UserLimit
	}

	dialog := ui.NewChannelDialog(current,
		func(edited ui.ChannelSettings) { a.submitChannelEdit(channelID, edited) },
		a.closeOverlay,
	)

	a.showOverlay(dialog.Content)
	a.channelDialog = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// submitChannelEdit sends the edit and leaves the card up until it takes, so a
// refusal can be corrected in the fields it came from. What took is drawn by the
// ChannelUpdate the gateway sends back — except the cooldown, which no event
// carries and the badge is therefore repainted from here.
func (a *App) submitChannelEdit(channelID string, edited ui.ChannelSettings) {
	epoch := a.epoch

	go func() {
		err := a.client.EditChannel(channelID, client.ChannelEdit{
			Name:        edited.Name,
			Description: edited.Description,
			Slowmode:    edited.Slowmode,
			UserLimit:   edited.UserLimit,
			NSFW:        edited.NSFW,
		})

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err == nil {
				a.closeOverlay()
				if channelID == a.currentChannelID {
					a.refreshSlowmode()
				}
				return
			}

			log.Printf("edit channel %s: %v", channelID, err)

			if a.channelDialog != nil {
				a.channelDialog.Fail(editChannelFailure(err))
			}
		}, false)
	}()
}

// editChannelFailure is what the card says about a refusal. As with a server, an
// empty name is the only one the reader can act on.
func editChannelFailure(err error) string {
	if errors.Is(err, client.ErrChannelNameEmpty) {
		return "Give the channel a name."
	}

	return "Could not save those changes."
}
