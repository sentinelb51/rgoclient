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

	return a.store.CanKickMembers(serverID)
}
