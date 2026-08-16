package app

// The notification system's controller half: notify posts a transient message,
// confirm asks before something irreversible, and the destructive actions
// themselves live here because each is one shape — confirm, fire off-thread,
// report the outcome.

import (
	"fmt"
	"log"

	"RGOClient/internal/audio"
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

	// Only a refusal sounds. An info notice is a receipt for something the user
	// just did — the clipboard write, the friend request — and a chime for each
	// would put a sound on every successful click in the client.
	if notice.Tone == ui.ToneDanger || notice.Tone == ui.ToneWarning {
		a.playSound(audio.Error)
	}

	a.notices.PushNotice(notice)
}

// confirm puts a question on the modal layer. The dialog closes itself whichever
// way it is answered; only the confirming branch calls back.
//
// Holding Shift answers it in advance: somebody clearing out a channel is asked
// the same question a dozen times, and a dialog answered unread protects nobody.
// Deciding it here covers every confirmation rather than the ones somebody
// remembered to wire it to. A question with no OnConfirm is never skipped — it is
// a statement, and skipping it would say nothing. Call on the UI thread.
func (a *App) confirm(question ui.Confirm) {
	if question.OnConfirm != nil && ui.ShiftHeld() {
		question.OnConfirm()
		return
	}

	a.showOverlay(ui.NewConfirmDialog(question, a.closeOverlay))
}

// notifyFailure is the standard onFail for an action whose only visible outcome
// is a notice: the API error goes to the log under what, the user gets the
// sentence. An action needing more should wrap it rather than restate it.
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

// leaveServer leaves without waiting for the response: the server disappears
// through ServerLeft, which also covers being kicked or the server being deleted.
func (a *App) leaveServer(serverID, name string) {
	a.background(
		func() error { return a.client.LeaveServer(serverID) },
		a.notifyFailure("leave server "+serverID, "Could not leave %s.", name),
	)
}

// canLeaveServer excludes the owner: for them the same endpoint deletes the
// server outright, a different question needing a much sterner one than this.
func (a *App) canLeaveServer(serverID string) bool {
	server, ok := a.store.Server(serverID)
	self := a.store.SelfID()

	return ok && self != "" && server.OwnerID != self
}

/* Closing a conversation */

// confirmCloseChannel asks before closing a DM or leaving a group. Neither
// destroys messages — the conversation leaves the sidebar — so a DM is a warning
// rather than a danger.
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

// canInviteTo: only a server's channels have an invite. A conversation is opened
// by naming somebody, and Revolt has no code that lets a third person into one.
func (a *App) canInviteTo(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.ServerID == "" {
		return false
	}

	return a.store.Permissions(channelID).Has(domain.PermissionInviteOthers)
}

// createInvite makes an invite and puts the link on the clipboard, which is the
// whole of what asking for one wants. The notice is the receipt — a clipboard
// write is invisible — and names the channel because the menu it was raised from
// is long gone.
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

// Nothing here has a gateway event that repaints anything, the card that raised
// it having been dismissed — so each says what it did, a friend request being
// otherwise indistinguishable from a click that missed.

// addFriend asks somebody to be friends. Revolt names the person by handle here
// rather than by ID, which the client resolves — see Client.AddFriend.
func (a *App) addFriend(userID, name string) {
	a.reportAction(
		func() error { return a.client.AddFriend(userID) },
		"add friend "+userID, "Could not send %s a friend request.", "Friend request sent to %s.", name,
	)
}

// acceptFriend answers a request that has already arrived.
func (a *App) acceptFriend(userID, name string) {
	a.reportAction(
		func() error { return a.client.AcceptFriend(userID) },
		"accept friend "+userID, "Could not accept %s's friend request.", "You and %s are now friends.", name,
	)
}

// removeFriend covers unfriending, declining and withdrawing alike: Revolt spends
// one route on all three, and which it is depends on where the relationship
// stood, which the button that raised it has already read.
func (a *App) removeFriend(userID, name string) {
	a.reportAction(
		func() error { return a.client.RemoveFriend(userID) },
		"remove friend "+userID, "Could not update your relationship with %s.", "%s is no longer a friend.", name,
	)
}

func (a *App) blockUser(userID, name string) {
	a.reportAction(
		func() error { return a.client.BlockUser(userID) },
		"block user "+userID, "Could not block %s.", "%s is blocked.", name,
	)
}

func (a *App) unblockUser(userID, name string) {
	a.reportAction(
		func() error { return a.client.UnblockUser(userID) },
		"unblock user "+userID, "Could not unblock %s.", "%s is unblocked.", name,
	)
}

// reportAction runs one request about somebody and says so either way, both
// messages taking their name. The success notice is the receipt: whatever raised
// it — a profile card, a menu — is gone by the time this runs.
func (a *App) reportAction(request func() error, what, failure, success, name string) {
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

// confirmRemoveFriend asks before unfriending. Declining or withdrawing is not
// put through it: neither takes away anything asking again would not restore.
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
// takes everything else away *both* ways, which is what the question has to say:
// it is not a mute.
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
	// The member record carries the nickname the sidebar shows them under, which is
	// the name to ask about; the raw user is the fallback.
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

// kickMember removes a member. The sidebar is repainted by the MembersChanged
// event, which arrives for any departure however it was caused.
func (a *App) kickMember(serverID, userID, name string) {
	a.reportAction(
		func() error { return a.client.KickMember(serverID, userID) },
		"kick member "+userID+" from server "+serverID, "Could not remove %s.", "%s was removed.", name,
	)
}

// canKickMember: permission to kick, and a target who is neither the user
// themselves nor the owner, whom the server refuses to remove anyway.
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
