package app

// Group conversations: making one, and changing who is in it. The rest of what a
// group is has been here since before groups could be made — it is a channel, so
// its rows, its messages, its header and its edit card are the ones every other
// channel uses, and leaving one is closing a conversation.
//
// What is new is that Revolt files a group's membership on the *account* rather
// than on the channel: whoever is in it is there because a friend put them there,
// and only the owner may put anybody out. So both cards are picked from
// Store.Relationships and both permissions are asked before either is offered —
// a menu item the server would refuse is one nobody should have to click.

import (
	"errors"
	"fmt"
	"log"
	"slices"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/assets"
	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

/* Making one */

// showCreateGroup raises the card that names a new group and picks who starts in
// it. Offered from the home sidebar's header, that being where the conversations
// are and where a new one belongs. Call on the UI thread.
func (a *App) showCreateGroup() {
	if !a.client.Connected() {
		return
	}

	dialog := ui.NewGroupDialog(a.deps(), a.friendCandidates(nil), a.submitCreateGroup, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.groupDialog = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// submitCreateGroup sends the request and leaves the card up until it takes, so a
// refusal can be corrected where it was typed. The group reaches the sidebar as
// ChannelCreated, which is already handled; the response is spent on selecting
// the one just made.
func (a *App) submitCreateGroup(name string, userIDs []string) {
	epoch := a.epoch

	go func() {
		channelID, err := a.client.CreateGroup(name, userIDs)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("create group: %v", err)
				if a.groupDialog != nil {
					a.groupDialog.Fail(createGroupFailure(err))
				}

				return
			}

			a.closeOverlay()
			a.selectChannel(channelID)
		}, false)
	}()
}

// createGroupFailure is what the card says about a refusal. Two of the three are
// the reader's to act on; the last covers a friend since unfriended, which the
// route refuses as a whole request.
func createGroupFailure(err error) string {
	switch {
	case errors.Is(err, client.ErrChannelNameEmpty):
		return "Give the group a name."
	case errors.Is(err, client.ErrGroupTooLarge):
		return fmt.Sprintf("A new group takes at most %d people.", client.MaxGroupCreate)
	}

	return "Could not make that group."
}

/* Who is in one */

// showInviteToGroup raises the card that adds to a group. It is handed the
// friends who are not in it already, so the card lists only what it can do — and
// nobody else: Revolt takes friends alone here, refusing a stranger and the
// request with them. Call on the UI thread.
func (a *App) showInviteToGroup(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canInviteToGroup(channelID) {
		return
	}

	dialog := ui.NewGroupInviteDialog(a.deps(), channel.Name,
		a.friendCandidates(channel.Recipients),
		func(userIDs []string) { a.submitInviteToGroup(channelID, userIDs) },
		a.closeOverlay,
	)

	a.showOverlay(dialog.Content)
	a.groupDialog = dialog
}

// submitInviteToGroup sends one request per person. Each arrival is announced as
// ChannelGroupJoin, so nothing here paints the sidebar.
//
// A partial success **closes** the card, where a total failure leaves it up. The
// card holds a set of people, and Revolt refuses somebody already in the group —
// so once any of them are in, the set on screen is no longer a request that can
// be sent again, and the honest thing is a notice saying how far it got.
func (a *App) submitInviteToGroup(channelID string, userIDs []string) {
	epoch := a.epoch

	go func() {
		added, err := a.client.AddGroupMembers(channelID, userIDs)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err == nil {
				a.closeOverlay()
				return
			}

			log.Printf("add to group %s: %v", channelID, err)

			if added == 0 {
				if a.groupDialog != nil {
					a.groupDialog.Fail("Could not add anybody. They have to be friends.")
				}

				return
			}

			a.closeOverlay()
			a.notify(ui.ToneWarning, "Added %d of %d — the rest were refused.", added, len(userIDs))
		}, false)
	}()
}

// confirmRemoveFromGroup asks before putting somebody out. Undone by adding them
// back, so it is a caution rather than the danger a ban is — but it is still a
// thing done to somebody else, which is what earns the question.
func (a *App) confirmRemoveFromGroup(channelID, userID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok {
		return
	}

	name := a.store.UserName(userID)

	a.confirm(ui.Confirm{
		Title:  "Remove from group",
		Body:   fmt.Sprintf("%s will lose access to %s. You can add them back.", name, channel.Name),
		Action: "Remove",
		Tone:   ui.ToneWarning,
		OnConfirm: func() {
			a.background(
				func() error { return a.client.RemoveGroupMember(channelID, userID) },
				a.notifyFailure("remove "+userID+" from group "+channelID,
					"Could not remove %s.", name),
			)
		},
	})
}

// groupMemberMenu is what a row in a group's sidebar offers. A group has no
// nicknames, no roles and no moderation beyond who is in it, so the menu is the
// ID every member row copies and — for the owner alone — putting somebody out.
func (a *App) groupMemberMenu(channelID, userID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy user ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(userID)
		}),
	}

	// Caution rather than danger: it is undone by adding them back, which is the
	// same reading a kick gets on a server.
	if a.canRemoveFromGroup(channelID, userID) {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon("Remove from group", ui.CautionMark(assets.SystemRemovedIcon),
				func() { a.confirmRemoveFromGroup(channelID, userID) }),
		)
	}

	return items
}

/* What the account may do about a group */

// canInviteToGroup reports whether the account may add to one. A group's grant is
// an allow-only overwrite over what everybody in it can see, and InviteOthers is
// the bit in it that covers this — the same bit a server channel's invite is
// gated on, which is why domain.Permission had to name it.
func (a *App) canInviteToGroup(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Kind != domain.ChannelGroup {
		return false
	}

	return a.store.Permissions(channelID).Has(domain.PermissionInviteOthers)
}

// canRemoveFromGroup reports whether the account may put somebody else out of
// one. Revolt allows it to the owner alone — it is not a permission bit, so no
// grant carries it — and nobody removes themselves that way: leaving is closing
// the conversation.
func (a *App) canRemoveFromGroup(channelID, userID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Kind != domain.ChannelGroup {
		return false
	}

	self := a.store.SelfID()

	return channel.OwnerID == self && userID != self
}

// inCurrentGroup reports whether somebody is in the group on screen, which is
// what decides whether their presence is worth a rebuild.
func (a *App) inCurrentGroup(userID string) bool {
	channel, ok := a.currentChannel()

	return ok && channel.Kind == domain.ChannelGroup && slices.Contains(channel.Recipients, userID)
}

/* Who is on offer */

// friendCandidates is everybody the account is friends with, less whoever is
// already in the group being added to. Store.Relationships has ordered them, so
// the card lists them in the order the friends page does.
//
// Friendship is the whole of the filter because it is the whole of what Revolt
// takes: both group routes refuse an account that is not one.
func (a *App) friendCandidates(already []string) []ui.GroupCandidate {
	in := make(map[string]bool, len(already))
	for _, userID := range already {
		in[userID] = true
	}

	var people []ui.GroupCandidate
	for _, user := range a.store.Relationships() {
		if user.Relationship != domain.RelationshipFriend || in[user.ID] {
			continue
		}

		people = append(people, ui.GroupCandidate{
			UserID:    user.ID,
			Name:      friendName(user),
			Handle:    user.Handle,
			AvatarURL: user.AvatarURL,
			Presence:  user.Presence,
		})
	}

	return people
}
