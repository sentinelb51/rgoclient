package app

// User profiles. Clicking a message avatar or a member row opens the compact
// card beside it, and the card expands into the full dialog on the modal layer.
// Both are drawn from one domain.Profile resolved out of the store here, so the
// widgets never look anything up themselves.
//
// The bio is the exception. It is not part of the user record the client already
// holds, so it is fetched after the card is up and filled in when it lands —
// which is why a card is a value plus one late arrival rather than a snapshot.

import (
	"log"
	"slices"

	"fyne.io/fyne/v2"

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

	card := ui.NewProfileCard(a.deps(), a.profileOf(userID), ui.ProfileActions{
		OnMessage: a.messageAction(userID),
		OnExpand:  func() { a.showProfileDialog(userID) },
	})

	a.showPopover(card.Content, anchor)
	a.loadBio(userID, card)
}

// showProfileDialog opens the full profile, centred on the modal layer. It
// replaces the card it was expanded from, so the two are never up together.
func (a *App) showProfileDialog(userID string) {
	dialog := ui.NewProfileDialog(a.deps(), a.profileOf(userID), ui.ProfileActions{
		OnMessage: a.messageAction(userID),
		OnClose:   a.closeOverlay,
	})

	a.showOverlay(dialog.Content)
	a.loadBio(userID, dialog)
}

// messageAction is what the "Message" button does, or nil when there is nothing
// for it to do — the account's own profile, where it would open a conversation
// with yourself.
func (a *App) messageAction(userID string) func() {
	if a.store.SelfID() == userID {
		return nil
	}

	return func() {
		a.closeOverlay()
		a.openConversation(userID)
	}
}

/* Resolving one */

// profileOf assembles a profile from what the client already knows. A user the
// store has never heard of still gets a card — with what little is known, and
// their resolution queued so a second look shows the real thing — because a click
// that does nothing is worse than a card that is thin.
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
	profile.Bot = user.Bot

	// The server the profile was opened in is what makes them a member: the
	// nickname, the per-server avatar, the role colour and the join date all
	// belong to that membership, and none of them exists in a conversation.
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

// loadBio fetches the profile text and fills it into a card that is already on
// screen, so nothing waits on the network to appear. A card the user has since
// dismissed is detached, and updating it draws nothing.
func (a *App) loadBio(userID string, card *ui.ProfileCard) {
	epoch := a.epoch

	go func() {
		bio, err := a.client.UserBio(userID)
		if err != nil {
			// A profile reads perfectly well without a bio, and a notice over the card
			// would be louder than the miss is worth — so this only reaches the log.
			log.Printf("fetch profile %s: %v", userID, err)
			return
		}
		if bio == "" {
			return
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			card.SetBio(bio)
			a.repositionOverlay() // the card just grew
		}, false)
	}()
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
// when the DM list hasn't caught up: that list is a fetched snapshot with no
// gateway event behind it, so one opened a moment ago is not in it yet. Call on
// the UI thread.
func (a *App) showConversation(channelID string) {
	if !slices.Contains(a.dmChannels, channelID) {
		a.dmChannels = append([]string{channelID}, a.dmChannels...)
	}

	a.selectHome() // a no-op when home is already open
	a.refreshChannelList()
	a.selectChannel(channelID)
}
