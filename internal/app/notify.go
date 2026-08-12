package app

// The notification system's controller half: notify posts a transient message,
// confirm asks a question that has to be answered before something irreversible
// happens, and the destructive actions themselves live here because each is the
// same shape — confirm, fire off-thread, report the failure.

import (
	"fmt"
	"log"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

/* Posting */

// notify puts a message on the notification layer and logs it. The text is what
// the user reads, so it says what happened rather than what the API returned —
// callers log the error itself alongside. Call on the UI thread.
func (a *App) notify(tone ui.Tone, format string, args ...any) {
	a.notifyNotice(ui.Notice{Tone: tone, Body: fmt.Sprintf(format, args...)})
}

// notifyNotice is notify for an outcome that deserves a heading of its own,
// rather than the one its tone would give it. Call on the UI thread.
func (a *App) notifyNotice(notice ui.Notice) {
	log.Printf("notice: %s", notice.Body)

	a.notices.PushNotice(notice)
}

// confirm puts a question on the modal layer. The dialog closes itself whichever
// way it is answered; only the confirming branch calls back. Call on the UI
// thread.
func (a *App) confirm(question ui.Confirm) {
	a.showOverlay(ui.NewConfirmDialog(question, a.closeOverlay))
}

// notifyFailure is the standard failure handler for an action whose only visible
// outcome is a notice: the API error goes to the log under what, the user gets
// the sentence. It is what App.background's onFail wants nearly everywhere, so an
// action that needs more should wrap this rather than restate it.
func (a *App) notifyFailure(what string, format string, args ...any) func(error) {
	return func(err error) {
		log.Printf("%s: %v", what, err)
		a.notify(ui.ToneDanger, format, args...)
	}
}

/* Leaving a server */

// confirmLeaveServer asks before leaving. The owner is never offered this — see
// canLeaveServer — so the question is only ever about the user's own membership.
func (a *App) confirmLeaveServer(serverID string) {
	server, ok := a.store.Server(serverID)
	if !ok {
		return
	}

	a.confirm(ui.Confirm{
		Title:  "Leave server",
		Body:   fmt.Sprintf("You'll lose access to %s and need a new invite to come back.", server.Name),
		Action: "Leave",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.leaveServer(serverID, server.Name)
		},
	})
}

// leaveServer leaves without waiting for the response to update the sidebar: the
// server disappears through the ServerLeft event, which is also what covers being
// kicked or the server being deleted out from under us.
func (a *App) leaveServer(serverID, name string) {
	a.background(
		func() error { return a.client.LeaveServer(serverID) },
		a.notifyFailure("leave server "+serverID, "Could not leave %s.", name),
	)
}

// canLeaveServer reports whether leaving is something to offer. The owner is
// excluded: for them the same endpoint deletes the server outright, which is a
// different question needing a much sterner one than this.
func (a *App) canLeaveServer(serverID string) bool {
	server, ok := a.store.Server(serverID)
	self := a.store.SelfID()

	return ok && self != "" && server.OwnerID != self
}

/* Closing a conversation */

// confirmCloseChannel asks before closing a DM or leaving a group. Neither
// destroys any messages — the conversation just leaves the sidebar — so the
// question is a warning rather than a danger.
func (a *App) confirmCloseChannel(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok {
		return
	}

	name := channel.Name
	question := ui.Confirm{
		Title:  "Close conversation",
		Body:   fmt.Sprintf("%s will leave your direct messages. Its history is kept, and a new message reopens it.", name),
		Action: "Close",
		Tone:   ui.ToneWarning,
		OnConfirm: func() {
			a.closeChannel(channelID, name)
		},
	}

	if channel.Kind == domain.ChannelGroup {
		question.Title = "Leave group"
		question.Body = fmt.Sprintf("You'll leave %s and need to be added again to rejoin.", name)
		question.Action = "Leave"
		question.Tone = ui.ToneDanger
	}

	a.confirm(question)
}

// closeChannel closes a DM or leaves a group. As with leaving a server, the
// sidebar is updated by the ChannelClosed event rather than here.
func (a *App) closeChannel(channelID, name string) {
	a.background(
		func() error { return a.client.CloseChannel(channelID) },
		a.notifyFailure("close channel "+channelID, "Could not close %s.", name),
	)
}

// isConversation reports whether a channel is one the user can close: a direct
// message or a group, as opposed to a channel belonging to a server.
func (a *App) isConversation(channelID string) bool {
	channel, ok := a.store.Channel(channelID)

	return ok && (channel.Kind == domain.ChannelDM || channel.Kind == domain.ChannelGroup)
}

/* Sharing a channel */

// canInviteTo reports whether an invite to a channel is worth offering. Only a
// server's channels have one: a conversation is opened by naming somebody, and
// Revolt has no code that would let a third person into it.
func (a *App) canInviteTo(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.ServerID == "" {
		return false
	}

	return a.store.Permissions(channelID).Has(domain.PermissionInviteOthers)
}

// createInvite makes an invite to a channel and puts the link on the clipboard,
// which is the whole of what somebody asking for one wants.
//
// The code is shown in the notice as well as copied. A clipboard write is
// invisible, and this is the one action in the client whose entire result is a
// string the user now has to paste somewhere — so the notice is the receipt, and
// it names the channel because the menu it was raised from may be long gone.
func (a *App) createInvite(channelID string) {
	name := "this channel"
	if channel, ok := a.store.Channel(channelID); ok {
		name = "#" + channel.Name
	}

	epoch := a.epoch
	onFail := a.notifyFailure("create invite for "+channelID, "Could not create an invite to %s.", name)

	go func() {
		code, err := a.client.CreateInvite(channelID)

		a.doOnUI(func() {
			if err != nil {
				onFail(err)
				return
			}
			if a.stale(epoch) {
				return
			}

			ui.CopyToClipboard(util.InviteLink(code))
			a.notifyNotice(ui.Notice{
				Tone:  ui.ToneInfo,
				Title: "Invite copied",
				Body:  fmt.Sprintf("A link to %s is on your clipboard.", name),
			})
		}, false)
	}()
}

/* Relationships */

// Each of these is the usual shape — fire off-thread, report the failure — with
// one difference: nothing here has a gateway event of its own that repaints
// anything, the card that raised it having already been dismissed. So each says
// what it did, because otherwise a friend request would be indistinguishable
// from a click that missed.

// addFriend asks somebody to be friends. Revolt names the person by handle here
// rather than by ID, which the client resolves — see Client.AddFriend.
func (a *App) addFriend(userID, name string) {
	a.relate(
		func() error { return a.client.AddFriend(userID) },
		"add friend "+userID, "Could not send %s a friend request.", "Friend request sent to %s.", name,
	)
}

// acceptFriend answers a request that has already arrived.
func (a *App) acceptFriend(userID, name string) {
	a.relate(
		func() error { return a.client.AcceptFriend(userID) },
		"accept friend "+userID, "Could not accept %s's friend request.", "You and %s are now friends.", name,
	)
}

// removeFriend covers unfriending, declining and withdrawing alike: Revolt
// spends one route on all three, and what it means is decided by where the
// relationship stood — which the button that raised it has already read.
func (a *App) removeFriend(userID, name string) {
	a.relate(
		func() error { return a.client.RemoveFriend(userID) },
		"remove friend "+userID, "Could not update your relationship with %s.", "%s is no longer a friend.", name,
	)
}

func (a *App) blockUser(userID, name string) {
	a.relate(
		func() error { return a.client.BlockUser(userID) },
		"block user "+userID, "Could not block %s.", "%s is blocked.", name,
	)
}

func (a *App) unblockUser(userID, name string) {
	a.relate(
		func() error { return a.client.UnblockUser(userID) },
		"unblock user "+userID, "Could not unblock %s.", "%s is unblocked.", name,
	)
}

// relate runs one relationship change and reports either way. The success notice
// is the receipt: the card is gone by the time this runs, so nothing else on
// screen would change.
func (a *App) relate(request func() error, what, failure, success, name string) {
	onFail := a.notifyFailure(what, failure, name)

	go func() {
		err := request()

		a.doOnUI(func() {
			if err != nil {
				onFail(err)
				return
			}

			a.notify(ui.ToneInfo, success, name)
		}, false)
	}()
}

// confirmRemoveFriend asks before unfriending. Declining or withdrawing a
// request is not put through this: neither takes anything away that asking again
// would not restore.
func (a *App) confirmRemoveFriend(userID, name string) {
	a.confirm(ui.Confirm{
		Title:  "Remove friend",
		Body:   fmt.Sprintf("%s will be removed from your friends. Either of you can ask again.", name),
		Action: "Remove",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.removeFriend(userID, name)
		},
	})
}

// confirmBlockUser asks before blocking. Revolt keeps the history readable and
// takes everything else away in both directions, which is what the question has
// to say — it is not a mute.
func (a *App) confirmBlockUser(userID, name string) {
	a.confirm(ui.Confirm{
		Title:  "Block user",
		Body:   fmt.Sprintf("Neither of you will be able to write to the other. Your conversation with %s stays readable.", name),
		Action: "Block",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.blockUser(userID, name)
		},
	})
}

/* Removing a member */

// confirmKickMember asks before removing someone from the open server.
func (a *App) confirmKickMember(serverID, userID string) {
	// The member record carries the nickname the sidebar shows them under, which
	// is the name to ask about; the raw user is the fallback.
	name := a.store.UserName(userID)
	if member, ok := a.store.Member(serverID, userID); ok {
		name = member.Name
	}

	a.confirm(ui.Confirm{
		Title:  "Remove member",
		Body:   fmt.Sprintf("%s will be removed from the server. They can rejoin with a new invite.", name),
		Action: "Remove",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.kickMember(serverID, userID, name)
		},
	})
}

// kickMember removes a member. The member sidebar is repainted by the
// MembersChanged event, which arrives for any departure however it was caused.
func (a *App) kickMember(serverID, userID, name string) {
	go func() {
		if err := a.client.KickMember(serverID, userID); err != nil {
			log.Printf("kick member %s from server %s: %v", userID, serverID, err)
			a.doOnUI(func() { a.notify(ui.ToneDanger, "Could not remove %s.", name) }, false)
			return
		}

		a.doOnUI(func() { a.notify(ui.ToneInfo, "%s was removed.", name) }, false)
	}()
}

// canKickMember reports whether removing this member is something to offer:
// permission to kick, and a target who is neither the user themselves nor the
// owner, whom the server will refuse to remove anyway.
func (a *App) canKickMember(serverID, userID string) bool {
	server, ok := a.store.Server(serverID)
	if !ok || server.OwnerID == userID {
		return false
	}

	self := a.store.SelfID()
	if self == "" || self == userID {
		return false
	}

	return a.store.ServerPermissions(serverID).Has(domain.PermissionKickMembers)
}
