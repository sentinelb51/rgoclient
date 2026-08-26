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
	"strings"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/assets"
	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
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
			a.notifyTitled(ui.ToneWarning, "Partly added",
				"Added %d of %d — the rest were refused.", added, len(userIDs))
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

/* Its settings */

// openGroupSettings covers the client with the group's own page. Offered on the
// channel's menu wherever the account may change something about it — which for
// a group is anybody holding ManageChannel or ManagePermissions, and the owner
// unconditionally. Call on the UI thread.
func (a *App) openGroupSettings(channelID string) {
	if a.groupSettings == nil || !a.canManageGroup(channelID) {
		return
	}

	a.closeOverlay() // a lightbox left up would draw over the page it was opened from
	a.closeSettings()
	a.closeServerSettings()

	a.groupSettingsID = channelID
	a.groupSettings.Open()
	a.bindKeys()
}

// closeGroupSettings takes the layer down. Call on the UI thread.
func (a *App) closeGroupSettings() {
	if a.groupSettings == nil || !a.groupSettings.IsOpen() {
		return
	}

	a.groupSettings.Close()
	a.groupSettingsID = ""
	a.bindKeys()
	a.focusInput()
}

// groupSettingsOpen reports whether the page is covering the client, for bindKeys
// and for the handlers that repaint it.
func (a *App) groupSettingsOpen() bool {
	return a.groupSettings != nil && a.groupSettings.IsOpen()
}

// canManageGroup reports whether there is anything on that page to do. Two
// permissions, either one being enough: the page lists both sections for anybody
// who reaches it and states read-only whatever the other does not cover.
func (a *App) canManageGroup(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.Kind != domain.ChannelGroup {
		return false
	}

	permissions := a.store.Permissions(channelID)

	return permissions.Has(domain.PermissionManageChannel) ||
		permissions.Has(domain.PermissionManagePermissions)
}

// groupSettingsHooks is everything ui.GroupSettingsPage asks of the controller.
// Every one of them reads a.groupSettingsID rather than closing over a channel,
// so opening the page on another group is a field and a rebuild — the same shape
// a server's page has.
func (a *App) groupSettingsHooks() ui.GroupSettingsHooks {
	return ui.GroupSettingsHooks{
		Deps:    a.deps(),
		Close:   a.closeGroupSettings,
		Confirm: a.confirm,
		Group:   a.groupSummary,
		Can: func(permission domain.Permission) bool {
			return a.store.Permissions(a.groupSettingsID).Has(permission)
		},
		SetName:        a.setGroupName,
		SetDescription: a.setGroupDescription,
		SetNSFW:        a.setGroupNSFW,
		ChangeIcon:     a.changeGroupIcon,
		RemoveIcon:     a.removeGroupIcon,
		SetPermissions: a.setGroupPermissions,
	}
}

// groupSummary resolves the group the page is open on. Answering false is a group
// closed while its settings were up, which the page draws as a sentence rather
// than as an empty surface.
func (a *App) groupSummary() (ui.GroupSummary, bool) {
	channel, ok := a.store.Channel(a.groupSettingsID)
	if !ok || channel.Kind != domain.ChannelGroup {
		return ui.GroupSummary{}, false
	}

	summary := ui.GroupSummary{
		ID:          channel.ID,
		Name:        channel.Name,
		Description: channel.Description,
		IconURL:     channel.AvatarURL,
		Owner:       a.store.UserName(channel.OwnerID),
		Members:     len(channel.Recipients),
		Permissions: channel.Permissions,
		Owned:       channel.OwnerID != "" && channel.OwnerID == a.store.SelfID(),
		NSFW:        channel.NSFW,
	}

	// An owner is somebody in the group, so the store almost always has them — but
	// a group opened before its people resolved has not, and an ID says more than
	// "Unknown" for the one account every action here defers to.
	if summary.Owner == "" {
		summary.Owner = channel.OwnerID
	}
	if created, err := util.Timestamp(channel.ID); err == nil {
		summary.Created = util.FullDate(created)
	}

	return summary, true
}

// setGroupName renames the group the page is open on. An empty name is refused
// here rather than sent: Revolt takes 1–32 characters, so the request could only
// come back as a status code with nothing to say.
func (a *App) setGroupName(name string) {
	channelID := a.groupSettingsID

	if strings.TrimSpace(name) == "" {
		a.notify(ui.ToneWarning, "A group needs a name.")
		return
	}

	a.background(
		func() error { return a.client.EditChannel(channelID, a.groupEdit(channelID, name, nil)) },
		a.notifyFailure("rename group "+channelID, "Could not rename the group."),
	)
}

// setGroupDescription publishes the blurb, blank removing it.
func (a *App) setGroupDescription(description string) {
	channelID := a.groupSettingsID

	a.background(
		func() error { return a.client.EditChannel(channelID, a.groupEdit(channelID, "", &description)) },
		a.notifyFailure("describe group "+channelID, "Could not save that description."),
	)
}

// groupEdit is the whole channel as one field of it is being changed. The route
// checks its one permission once and applies every field it was given, and
// ChannelEdit refuses a blank name — so a description sent alone would clear the
// name. Whichever half the caller is not changing is read back out of the store,
// the same read-and-resend this account's own status line needs.
func (a *App) groupEdit(channelID, name string, description *string) client.ChannelEdit {
	channel, _ := a.store.Channel(channelID)

	edit := client.ChannelEdit{Name: channel.Name, Description: channel.Description, NSFW: channel.NSFW}
	if name != "" {
		edit.Name = name
	}
	if description != nil {
		edit.Description = *description
	}

	return edit
}

// setGroupNSFW moves the age gate. The one field of the channel edit card this
// page took over, so a group has one surface rather than a card and a page both
// naming it.
func (a *App) setGroupNSFW(nsfw bool) {
	channelID := a.groupSettingsID

	edit := a.groupEdit(channelID, "", nil)
	edit.NSFW = nsfw

	a.background(
		func() error { return a.client.EditChannel(channelID, edit) },
		a.notifyFailure("age-gate group "+channelID, "Could not change that."),
	)
}

// refreshGroupSettings rebuilds the page against the store, or takes it down when
// the group has stopped being one this account is in. Call on the UI thread.
func (a *App) refreshGroupSettings() {
	if !a.groupSettingsOpen() {
		return
	}

	// Gone, or nothing left on it to do — the owner can take ManageChannel and
	// ManagePermissions off everybody in one edit, and the page would otherwise
	// stay up stating a dozen values nobody can move.
	if !a.canManageGroup(a.groupSettingsID) {
		a.closeGroupSettings()
		return
	}

	a.groupSettings.Rebuild()
}

// groupPageOn reports whether the page is up on one named group, which is what
// decides whether a channel event is worth a rebuild.
func (a *App) groupPageOn(channelID string) bool {
	return channelID != "" && channelID == a.groupSettingsID && a.groupSettingsOpen()
}

// changeGroupIcon and removeGroupIcon are the one picture a group has. Both come
// back as a ChannelUpdate, which repaints the sidebar row, the strip above the
// page's rail and the row offering to remove one — so nothing is recorded here.
func (a *App) changeGroupIcon() {
	channelID := a.groupSettingsID

	a.choosePicture("Choose a group icon", func(path, name string) {
		a.background(
			func() error { return a.client.SetGroupIcon(channelID, path, name) },
			a.notifyFailure("set icon on group "+channelID, "Could not change the icon. It may be too large."),
		)
	})
}

func (a *App) removeGroupIcon() {
	channelID := a.groupSettingsID

	a.background(
		func() error { return a.client.RemoveGroupIcon(channelID) },
		a.notifyFailure("remove icon from group "+channelID, "Could not remove the icon."),
	)
}

// setGroupPermissions publishes what everybody in the group may do. Unconfirmed,
// like every other switch on a settings page: it is undone by setting it back,
// and the grid states what it is now.
func (a *App) setGroupPermissions(permissions domain.Permission) {
	channelID := a.groupSettingsID

	a.background(
		func() error { return a.client.SetGroupPermissions(channelID, permissions) },
		a.notifyFailure("set permissions on group "+channelID, "Could not save those permissions."),
	)
}
