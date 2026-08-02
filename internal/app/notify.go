package app

// The notification system's controller half: notify posts a transient message,
// confirm asks a question that has to be answered before something irreversible
// happens, and the destructive actions themselves live here because each is the
// same shape — confirm, fire off-thread, report the failure.

import (
	"fmt"
	"log"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

/* Posting */

// notify puts a message on the notification layer and logs it. The text is what
// the user reads, so it says what happened rather than what the API returned —
// callers log the error itself alongside. Call on the UI thread.
func (a *App) notify(tone ui.Tone, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	log.Printf("notice: %s", text)

	a.notices.Push(tone, text)
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
	server := a.stateServer(serverID)
	if server == nil {
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
// server disappears through the ServerDelete gateway event, which is also what
// covers being kicked or the server being deleted out from under us.
func (a *App) leaveServer(serverID, name string) {
	session := a.session
	if session == nil {
		return
	}

	go func() {
		if err := session.ServerDelete(serverID); err != nil {
			log.Printf("leave server %s: %v", serverID, err)
			a.doOnUI(func() { a.notify(ui.ToneDanger, "Could not leave %s.", name) }, false)
		}
	}()
}

// canLeaveServer reports whether leaving is something to offer. The owner is
// excluded: for them the same endpoint deletes the server outright, which is a
// different question needing a much sterner one than this.
func (a *App) canLeaveServer(serverID string) bool {
	session, server := a.session, a.stateServer(serverID)
	if session == nil || server == nil {
		return false
	}

	self := session.State.Self()

	return self != nil && server.Owner != self.ID
}

/* Closing a conversation */

// confirmCloseChannel asks before closing a DM or leaving a group. Neither
// destroys any messages — the conversation just leaves the sidebar — so the
// question is a warning rather than a danger.
func (a *App) confirmCloseChannel(channelID string) {
	channel := a.stateChannel(channelID)
	if channel == nil {
		return
	}

	name := util.ChannelName(a.session, channel)
	question := ui.Confirm{
		Title:  "Close conversation",
		Body:   fmt.Sprintf("%s will leave your direct messages. Its history is kept, and a new message reopens it.", name),
		Action: "Close",
		Tone:   ui.ToneWarning,
		OnConfirm: func() {
			a.closeChannel(channelID, name)
		},
	}

	if channel.ChannelType == revoltgo.ChannelTypeGroup {
		question.Title = "Leave group"
		question.Body = fmt.Sprintf("You'll leave %s and need to be added again to rejoin.", name)
		question.Action = "Leave"
		question.Tone = ui.ToneDanger
	}

	a.confirm(question)
}

// closeChannel closes a DM or leaves a group. As with leaving a server, the
// sidebar is updated by the ChannelDelete gateway event rather than here.
func (a *App) closeChannel(channelID, name string) {
	session := a.session
	if session == nil {
		return
	}

	go func() {
		if err := session.ChannelDelete(channelID); err != nil {
			log.Printf("close channel %s: %v", channelID, err)
			a.doOnUI(func() { a.notify(ui.ToneDanger, "Could not close %s.", name) }, false)
		}
	}()
}

// isConversation reports whether a channel is one the user can close: a direct
// message or a group, as opposed to a channel belonging to a server.
func (a *App) isConversation(channelID string) bool {
	channel := a.stateChannel(channelID)
	if channel == nil {
		return false
	}

	return channel.ChannelType == revoltgo.ChannelTypeDM || channel.ChannelType == revoltgo.ChannelTypeGroup
}

/* Removing a member */

// confirmKickMember asks before removing someone from the open server.
func (a *App) confirmKickMember(serverID, userID string) {
	session := a.session
	if session == nil {
		return
	}

	// The member record carries the nickname the sidebar shows them under, which
	// is the name to ask about; the raw user is the fallback.
	name := util.UserName(session, userID)
	if member := session.State.Member(serverID, userID); member != nil {
		name = util.MemberName(session, member)
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
// ServerMemberLeave gateway event, which arrives for any departure however it
// was caused.
func (a *App) kickMember(serverID, userID, name string) {
	session := a.session
	if session == nil {
		return
	}

	go func() {
		if err := session.ServerMemberDelete(serverID, userID); err != nil {
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
	session, server := a.session, a.stateServer(serverID)
	if session == nil || server == nil || server.Owner == userID {
		return false
	}

	self := session.State.Self()
	if self == nil || self.ID == userID {
		return false
	}

	permissions, err := session.State.ServerPermissions(self, server)

	return err == nil && permissions&revoltgo.PermissionKickMembers != 0
}
