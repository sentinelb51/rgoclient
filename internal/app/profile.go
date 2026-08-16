package app

// User profiles. Clicking an avatar or a member row opens the compact card beside
// it, and the card expands into the dialog on the modal layer. Both are drawn
// from one domain.Profile resolved here, so the widgets look nothing up.
//
// The bio and the banner are the exception: neither is part of the user record,
// so both are fetched after the card is up and filled in when they land — which
// is why a card is a value plus a late arrival rather than a snapshot.

import (
	"log"
	"slices"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

/* Opening a profile */

// OnUserTapped opens the compact profile card beside whatever was clicked.
func (a *App) OnUserTapped(userID string, anchor fyne.CanvasObject) {
	if userID == "" || anchor == nil {
		return
	}

	profile := a.profileOf(userID)
	card := ui.NewProfileCard(a.deps(), profile, ui.ProfileActions{
		Buttons:  a.profileButtons(profile),
		OnCopied: a.copied,
		OnExpand: func() { a.showProfileDialog(userID) },
	})

	a.showPopover(card.Content, anchor)
	a.loadProfile(userID, card)
}

// showProfileDialog opens the full profile, centred on the modal layer. It
// replaces the card it was expanded from, so the two are never up together.
func (a *App) showProfileDialog(userID string) {
	profile := a.profileOf(userID)
	dialog := ui.NewProfileDialog(a.deps(), profile, ui.ProfileActions{
		Buttons:  a.profileButtons(profile),
		OnCopied: a.copied,
		OnClose:  a.closeOverlay,
	})

	a.showOverlay(dialog.Content)
	a.loadProfile(userID, dialog)
	a.loadMutual(userID, dialog)
}

/* Resolving one */

// profileOf assembles a profile from what the client already knows. An unknown
// user still gets a card — thin, with their resolution queued so a second look
// shows the real thing — because a click that does nothing is worse.
func (a *App) profileOf(userID string) domain.Profile {
	profile := domain.Profile{UserID: userID, Name: "Unknown user"}
	if created, err := util.Timestamp(userID); err == nil {
		profile.Created = created
	}

	user, ok := a.store.User(userID)
	if !ok {
		a.ensureAuthor(a.currentServerID, userID)
		return profile
	}

	profile.Name = user.Name
	profile.Handle = user.Handle
	profile.AvatarURL = user.AvatarURL
	profile.Presence = user.Presence
	profile.Status = user.StatusText
	profile.Badges = user.Badges
	profile.Relationship = user.Relationship
	profile.Bot = user.Bot

	// The server the profile was opened in is what makes them a member: nickname,
	// per-server avatar, role colour and join date all belong to that membership,
	// and none exists in a conversation.
	server, ok := a.currentServer()
	if !ok {
		return profile
	}
	if member, ok := a.store.Member(server.ID, userID); ok {
		profile.Name = member.Name
		profile.AvatarURL = member.AvatarURL
		profile.Accent = member.Color
		profile.Roles = a.store.MemberRoles(server.ID, userID)
		profile.ServerName = server.Name
		profile.Joined = member.JoinedAt
	}

	return profile
}

// loadProfile fetches the bio and banner and fills them into a card that is
// already on screen, so nothing waits on the network to appear. A card the user
// has since dismissed is detached, and updating it draws nothing.
func (a *App) loadProfile(userID string, card *ui.ProfileCard) {
	epoch := a.epoch

	go func() {
		profile, err := a.client.UserProfile(userID)
		if err != nil {
			// A profile reads well without either, and a notice over the card would be
			// louder than the miss is worth.
			log.Printf("fetch profile %s: %v", userID, err)
			return
		}
		if profile.Bio == "" && profile.BackgroundURL == "" {
			return
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			card.SetProfile(profile)
			a.repositionOverlay() // a bio grows the dialog
		}, false)
	}()
}

// loadMutual fetches what the two accounts have in common and fills it into the
// dialog — the dialog only, the compact card naming somebody rather than costing
// a second request per avatar click. Nothing is asked about this account:
// everything is in common with yourself, and Revolt refuses the route anyway.
func (a *App) loadMutual(userID string, card *ui.ProfileCard) {
	if userID == a.store.SelfID() {
		return
	}
	epoch := a.epoch

	go func() {
		mutual, err := a.client.Mutual(userID)
		if err != nil {
			// A profile reads perfectly well without it, as it does without a bio.
			log.Printf("fetch mutual %s: %v", userID, err)
			return
		}
		if len(mutual.ServerIDs) == 0 && len(mutual.UserIDs) == 0 {
			return
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			card.SetMutual(a.mutualProfile(mutual))
			a.repositionOverlay() // a section grows the dialog
		}, false)
	}()
}

// mutualProfile resolves what the two accounts have in common into what the
// dialog draws: a name and where it leads, plus the totals — somebody the store
// cannot name is still one of the people in common, and the "+n" accounts for
// them. Both destinations replace what is on the modal layer rather than stacking:
// a server is behind the dialog, and another profile is this surface with somebody
// else in it.
func (a *App) mutualProfile(mutual domain.Mutual) ui.MutualProfile {
	resolved := ui.MutualProfile{
		ServerCount: len(mutual.ServerIDs),
		FriendCount: len(mutual.UserIDs),
	}

	for _, serverID := range mutual.ServerIDs {
		server, ok := a.store.Server(serverID)
		if !ok || server.Name == "" {
			continue
		}

		resolved.Servers = append(resolved.Servers, ui.MutualEntry{
			Name: server.Name,
			Open: func() {
				a.closeOverlay()
				a.OnServerTapped(serverID)
			},
		})
	}
	for _, userID := range mutual.UserIDs {
		name := a.store.UserName(userID)
		if name == "" {
			continue
		}

		resolved.Friends = append(resolved.Friends, ui.MutualEntry{
			Name: name,
			Open: func() { a.showProfileDialog(userID) },
		})
	}

	return resolved
}

/* What a profile offers to do */

// profileButtons is what a card offers to do about somebody — decided here rather
// than in the widget, the answer being a question about the relationship and a
// card having no business knowing Revolt's states.
//
// "Message" is not always offered: Revolt will not open a conversation with a
// stranger, so a button that could only fail is worse than the one leading
// somewhere. A bot is the exception — nobody befriends one, and writing to it is
// what it is for.
//
// Copying the ID is added here rather than in relationshipButtons: it is the one
// thing offered about *anybody*, this account included, where the relationship
// policy answers with nothing for yourself. The friends list is spared it, a row
// already leading to the profile that carries it.
func (a *App) profileButtons(profile domain.Profile) []ui.ProfileButton {
	buttons := a.relationshipButtons(profile, a.closeOverlay)

	if profile.UserID == "" {
		return buttons
	}

	return append(buttons, ui.ProfileButton{
		Label:    "Copy user ID",
		Overflow: true,
		Icon:     fynetheme.ContentCopyIcon(),
		Do: func() {
			ui.CopyToClipboard(profile.UserID)
			a.copied("User ID")
		},
	})
}

// copied is the receipt for a clipboard write nobody can see happen. what *names*
// the thing rather than quoting it: a handle read back is the same string twice,
// and an ID is 26 characters nobody reads.
func (a *App) copied(what string) {
	a.notify(ui.ToneInfo, "%s copied.", what)
}

// relationshipButtons is that policy with the way out left open. A card is taken
// down before it acts, a profile not refreshing while it is up; the friends list
// stays and refills instead, one row changing being the whole result.
func (a *App) relationshipButtons(profile domain.Profile, done func()) []ui.ProfileButton {
	userID, name := profile.UserID, profile.Name
	if userID == "" || userID == a.store.SelfID() {
		return nil
	}

	message := ui.ProfileButton{Label: "Message", Do: func() {
		a.closeOverlay()
		a.openConversation(userID)
	}}
	if profile.Bot {
		return []ui.ProfileButton{message}
	}

	// Each settles the surface that raised it before firing, so a button cannot be
	// clicked twice against a state the first click changed.
	act := func(label string, danger bool, run func()) ui.ProfileButton {
		return ui.ProfileButton{Label: label, Danger: danger, Do: func() {
			done()
			run()
		}}
	}

	// The two that cannot be taken back by the person they are done to are also the
	// two nobody opens a profile to do, so both go behind the hamburger. Everything
	// else is the point of the card: a request answered, a block lifted.
	overflow := func(button ui.ProfileButton, icon fyne.Resource) ui.ProfileButton {
		button.Overflow, button.Icon = true, icon

		return button
	}

	block := overflow(act("Block", true, func() { a.confirmBlockUser(userID, name) }),
		fynetheme.VisibilityOffIcon())

	switch profile.Relationship {
	case domain.RelationshipFriend:
		return []ui.ProfileButton{message,
			overflow(act("Remove", true, func() { a.confirmRemoveFriend(userID, name) }),
				fynetheme.ContentRemoveIcon()),
			block}

	case domain.RelationshipIncoming:
		// Neither is drawn as destructive, for the same reason neither is confirmed: a
		// declined request can be sent again.
		return []ui.ProfileButton{
			act("Accept request", false, func() { a.acceptFriend(userID, name) }),
			act("Ignore", false, func() { a.removeFriend(userID, name) }),
		}

	case domain.RelationshipOutgoing:
		// Nothing to do but wait or withdraw, and a card leaving the first out would
		// read as one that had never been asked.
		return []ui.ProfileButton{
			{Label: "Request sent"},
			act("Cancel request", false, func() { a.removeFriend(userID, name) }),
		}

	case domain.RelationshipBlocked:
		return []ui.ProfileButton{act("Unblock", false, func() { a.unblockUser(userID, name) })}

	case domain.RelationshipBlockedBy:
		// Blocking back is the only thing that still works from this side.
		return []ui.ProfileButton{block}
	}

	return []ui.ProfileButton{
		act("Add friend", false, func() { a.addFriend(userID, name) }),
		block,
	}
}

/* Opening a conversation */

// openConversation switches to the direct message with a user, asking the server
// to open one when there isn't one yet.
func (a *App) openConversation(userID string) {
	epoch := a.epoch

	go func() {
		channelID, err := a.client.OpenConversation(userID)
		if err != nil {
			log.Printf("open conversation with %s: %v", userID, err)
			a.doOnUI(func() { a.notify(ui.ToneDanger, "Could not open a conversation.") }, false)
			return
		}

		a.doOnUI(func() {
			if !a.stale(epoch) {
				a.showConversation(channelID)
			}
		}, false)
	}()
}

// showConversation opens a conversation in the home view, adding its row itself
// when the list has not caught up — that list is a fetched snapshot with no
// gateway event behind it. Call on the UI thread.
func (a *App) showConversation(channelID string) {
	if !slices.Contains(a.dmChannels, channelID) {
		a.dmChannels = slices.Insert(a.dmChannels, 0, channelID)
	}

	a.selectHome() // a no-op when home is already open
	a.refreshChannelList()
	a.selectChannel(channelID)
}
