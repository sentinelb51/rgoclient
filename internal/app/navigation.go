package app

import (
	"iter"
	"log"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// buildUI assembles the four-column layout: servers | channels | messages |
// members. Only the message area (index 2) stretches; the rest keep their fixed
// widths, which is what makes the sections sit flush.
//
// Six layers sit over the row — the call island, notices, the tooltip and the
// three settings pages — and none matches a pointer event bar the island and a
// notice card themselves, so the row keeps every click and hover. Settings is a
// layer rather than an overlay because the modal layer holds one thing at a time
// and a confirmation raised from a page has to draw over it; only one of the
// three is ever up, each opening by closing the other two.
//
// The island is the lowest of the six, being a standing report rather than an
// answer to anything.
func (a *App) buildUI() fyne.CanvasObject {
	a.callIsland = ui.NewCallIsland(a.images, ui.CallIslandActions{
		OnMute:    a.toggleMute,
		OnDeafen:  a.toggleDeafen,
		OnHangUp:  a.leaveCall,
		OnJoin:    a.joinCallHere,
		OnChannel: func() { a.OnChannelTapped(a.callChannelID) },
		OnState:   a.showCallState,
	})
	a.callIslandLayer = ui.NewCallIslandLayer(a.callIsland)

	a.mainRow = ui.NewFillRow(2,
		a.buildServerList(),
		a.buildChannelList(),
		a.buildMessageArea(),
		a.buildMemberList(),
	)

	// The modal notice is the topmost of the six: it is what the client says when
	// it matters most, and it is click-through, so covering a settings page costs
	// that page nothing.
	return container.NewStack(a.mainRow, a.callIslandLayer, a.notices.Layer,
		a.tooltip.Layer, a.settings.Layer, a.serverSettings.Layer, a.groupSettings.Layer,
		a.modal.Layer)
}

/* Server sidebar */

// buildServerList is the server rail: fixed home, inbox and settings buttons
// bookending the scrolling icons, outside the scroll so they stay put as the list
// grows.
//
// The inbox is here rather than in the message header because it is the one
// surface about no channel in particular: the header's three buttons are all
// about the one on screen, and the rail is where this client keeps what is about
// the account.
func (a *App) buildServerList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ServerListBackground)

	a.homeButton = ui.NewSidebarButton(fynetheme.HomeIcon(), a.selectHome)
	a.inboxButton = ui.NewSidebarButton(assets.MentionIcon, a.showMentions)
	settings := ui.NewSidebarButton(fynetheme.SettingsIcon(), a.openSettings)

	// Bare, not wrapped in a Center, for the reason refreshServerList mounts a
	// ServerWidget bare: the button centres its own icon, and its marker is pinned
	// to whatever width it is given. Centred, that width is the icon's, which puts
	// the bar under the circle instead of on the rail's edge.
	top := container.NewVBox(
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		a.homeButton,
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		a.inboxButton,
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		ui.NewSidebarSeparator(),
	)
	bottom := container.NewVBox(
		ui.NewSidebarSeparator(),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		settings,
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
	)

	// Dropped rather than re-taken: refreshServerList reuses the icons the rail is
	// holding, and the one reason this runs twice in a session is a restyle — a
	// widget bakes the palette into its renderer, so a reused one would come back
	// in the colours it was made in.
	a.serverList.Objects = nil

	a.refreshServerList()
	content := container.NewBorder(top, bottom, nil, nil, container.NewVScroll(a.serverList))

	// Each divider is the last child of the column it edges, so the main row keeps
	// its four children — see ui.NewColumnDivider.
	return ui.NewFixedWidthContainer(theme.Sizes.ServerSidebarWidth, background,
		ui.NewFillRow(0, content, ui.NewColumnDivider()))
}

// refreshServerList re-takes the icons. Any tooltip is taken down first: the icon
// that raised it may be about to be dropped, and would never report the pointer
// leaving.
//
// The icons already mounted are **reused**, keyed by the server they draw: an
// update about any one server queues this, so all but one icon in a rail is
// drawing exactly what it drew before, and a rebuilt one asks the image cache for
// its picture again. The list itself is written only when the objects in it move,
// a Refresh being a repaint of the whole window whatever changed.
func (a *App) refreshServerList() {
	a.tooltip.Hide()

	held := make(map[string]*ui.ServerWidget, len(a.serverList.Objects))
	var join fyne.CanvasObject
	for _, obj := range a.serverList.Objects {
		if w, ok := obj.(*ui.ServerWidget); ok {
			held[w.Server.ID] = w
			continue
		}
		join = obj
	}

	icons := make([]fyne.CanvasObject, 0, len(a.serverIDs)+1)
	for _, serverID := range a.serverIDs {
		server, ok := a.store.Server(serverID)
		if !ok {
			continue
		}

		// Taken out of the pool as it is used, so a server listed twice draws two
		// icons rather than one object mounted in two places.
		w, reused := held[serverID]
		delete(held, serverID)

		if !reused {
			w = ui.NewServerWidget(a.images, server, func() { a.selectServer(serverID) })

			// The name is read when the pointer arrives rather than captured here:
			// the widget outlives the value it was built from, and a renamed server
			// would otherwise go on being labelled with the old one.
			w.OnHover = func(hovering bool) {
				if hovering {
					a.tooltip.Show(w.Server.Name, w)
					return
				}
				a.tooltip.Hide()
			}
			w.Menu = func() []*fyne.MenuItem { return a.serverMenu(serverID) }
		}

		w.SetServer(server)
		w.SetSelected(serverID == a.currentServerID)
		w.SetMentioned(a.serverMentioned(serverID))

		// Bare, not wrapped in a Center: ServerWidget centres its own icon, and
		// keeping it at the top level lets syncServerSelection find it unwrapped.
		icons = append(icons, w)
	}

	// The join button reads as one more server icon at the end, so it lives inside
	// the scroll rather than in the fixed bookends. The selection sync skips it,
	// not being a ServerWidget.
	if join == nil {
		join = ui.NewSidebarButton(fynetheme.ContentAddIcon(), a.showJoinServer)
	}
	icons = append(icons, join)

	if !slices.Equal(a.serverList.Objects, icons) {
		a.serverList.Objects = icons
		a.serverList.Refresh()
	}

	// The two fixed buttons are outside that list and so outside the walk above.
	a.syncMentionMarks()
}

/* Channel sidebar */

// buildChannelList builds the channel sidebar with its server-name header.
func (a *App) buildChannelList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)

	name := "Server"
	if server, ok := a.currentServer(); ok {
		name = server.Name
	}
	a.serverHeader = widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.serverHeader.Truncation = fyne.TextTruncateEllipsis

	// The cog and the plus share the header's one trailing slot, neither view
	// having the other's button: a server is configured, and the home view is added
	// to. Both are built once and shown per view, as the message header's own
	// buttons are — a button rebuilt per switch is a widget per switch.
	a.serverCog = ui.NewIconButton(assets.CogIcon, a.openServerSettings, nil)

	// Only the plus is labelled: a cog in the corner of a header says settings
	// wherever it is drawn, where a plus says "new" and nothing about what of. The
	// closure reads the field rather than closing over the button, which does not
	// exist until the call it is being passed to returns.
	a.groupAdd = ui.NewIconButton(assets.ActionAddIcon, a.showCreateGroup, func(hovering bool) {
		if hovering {
			a.tooltip.Show(newGroupTip, a.groupAdd)
			return
		}
		a.tooltip.Hide()
	})

	a.syncServerCog()
	a.syncGroupAdd()

	// As in buildServerList, and for the same reason: the rows are reused across a
	// refresh but not across the rebuild a restyle makes. Released first — a row
	// forgotten while its mark is sweeping is a repaint a second for nothing.
	a.releaseChannelRows()
	a.channelTop.Objects = nil
	a.channelList.Objects = nil

	a.refreshChannelList()

	pad := theme.Sizes.ChannelSidebarPadding
	scroll := container.NewBorder(nil, nil, ui.HorizontalSpacer(pad), ui.HorizontalSpacer(pad),
		container.NewVScroll(a.channelList))

	// The name takes what the buttons leave, so a long one shortens rather than
	// pushing them out of the column. The row charges nothing for the hidden one,
	// which is always exactly one of the two.
	title := container.NewBorder(nil, nil, nil,
		ui.HBoxNoSpacing(a.groupAdd, a.serverCog), a.serverHeader)

	// The pinned group sits outside that padding and above the scroll: full column
	// width, which is what says it is not one of the rows below, and it does not
	// scroll away from what it leads to.
	header := ui.VBoxNoSpacing(container.NewPadded(title), a.channelTop)

	a.channelColumn = container.NewBorder(header, nil, nil, nil, scroll)

	return ui.NewFixedWidthContainer(theme.Sizes.ChannelSidebarWidth, background,
		ui.NewFillRow(0, a.channelColumn, ui.NewColumnDivider()))
}

// refreshChannelList rebuilds the rows for the current server, grouped under
// their categories — or, in the home view, the flat list of cached conversations,
// which has none. The composer's #mention candidates come off this same walk, as
// the member sidebar's @mentions come off its own. The home view contributes none:
// a conversation is not something a message can link to.
func (a *App) refreshChannelList() {
	// The rows already mounted, keyed by the channel they draw, so a rebuild for an
	// event about one of them reuses the rest rather than building a widget and
	// re-asking the image cache for every conversation's picture. Taken before the
	// release below, which only stops what those rows are running.
	held := make(map[string]*ui.ChannelWidget)
	for w := range a.channelRows() {
		held[w.Channel.ID] = w
	}
	a.releaseChannelRows()

	animate := config.Current().Behaviour.TypingAnimation

	// Written once at the end rather than through Container.Add, which refreshes
	// the whole column per child — and only when the column's objects moved, a
	// Refresh being a repaint of the whole window whatever changed.
	var rows []fyne.CanvasObject
	mount := func() {
		if slices.Equal(a.channelList.Objects, rows) {
			return
		}

		a.channelList.Objects = rows
		a.channelList.Refresh()
	}

	if a.homeSelected {
		// Rebuilt with the list rather than kept aside: the sidebar's objects are
		// replaced wholesale, so a row held across one is a widget in no container.
		a.friendsRow = ui.NewFriendsRow(a.showFriendsPage)

		// Neither of these is a conversation with somebody, so they are pinned as
		// their own group rather than sorted among the ones that are — Saved Notes
		// would move about with its last message, and the friends list sorts by
		// nothing at all.
		group := []fyne.CanvasObject{a.friendsRow}
		saved := a.savedNotesID()
		if w := a.newChannelRow(saved, animate, held); w != nil {
			group = append(group, w)
		}
		a.setChannelGroup(group)

		for _, channelID := range a.dmChannels {
			if channelID == saved {
				continue
			}
			if w := a.newChannelRow(channelID, animate, held); w != nil {
				rows = append(rows, w)
			}
		}

		mount()
		a.setMentionCandidates(ui.MentionChannel, nil)
		a.refreshFriends()
		return
	}

	a.friendsRow = nil
	a.setChannelGroup(nil)

	server, ok := a.currentServer()
	if !ok {
		mount()
		a.setMentionCandidates(ui.MentionChannel, nil)
		return
	}

	categorized := make(map[string]bool)
	for _, category := range server.Categories {
		for _, id := range category.Channels {
			categorized[id] = true
		}
	}

	var candidates []ui.MentionCandidate

	// Uncategorized channels come first.
	for _, channelID := range server.Channels {
		if categorized[channelID] {
			continue
		}
		if w := a.newChannelRow(channelID, animate, held); w != nil {
			candidates = append(candidates, ui.NewChannelCandidate(w.Channel))
			rows = append(rows, w)
			rows = append(rows, a.callRows(w.Channel)...)
		}
	}

	for i, category := range server.Categories {
		key := server.ID + ":" + category.ID
		header := ui.NewCategoryWidget(category.Title, func(collapsed bool) {
			a.collapsedCategories[key] = collapsed
		})
		header.SetFirst(i == 0)

		var under []fyne.CanvasObject
		for _, channelID := range category.Channels {
			if w := a.newChannelRow(channelID, animate, held); w != nil {
				candidates = append(candidates, ui.NewChannelCandidate(w.Channel))
				under = append(under, w)
				under = append(under, a.callRows(w.Channel)...)
			}
		}

		rows = append(rows, header)
		rows = append(rows, under...)

		header.SetChannels(under, a.channelList)
		if a.collapsedCategories[key] {
			header.SetCollapsed(true)
		}
	}

	mount()
	a.setMentionCandidates(ui.MentionChannel, candidates)
}

// newChannelRow is a channel row in its current state, or nil when the store does
// not know the channel or the account cannot see it. Hidden this way is hidden
// everywhere at once: this walk also feeds the composer its #mention candidates,
// so one that never becomes a row is never a candidate either.
//
// held is the rows the sidebar was drawing, and one already drawing this channel
// is taken back rather than replaced — keyed by the channel, so what its callbacks
// captured still names the row they are on. It is shown on the way out: a row that
// was under a collapsed category is hidden, and the category it lands in this time
// need not be.
//
// animate is passed rather than read, as applyChannelState's is and for the same
// reason: one rebuild reads the setting once for every row it makes.
func (a *App) newChannelRow(channelID string, animate bool, held map[string]*ui.ChannelWidget) *ui.ChannelWidget {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canViewChannel(channel) {
		return nil
	}

	if w, ok := held[channelID]; ok {
		delete(held, channelID) // so a channel listed twice draws two rows, not one object twice
		w.SetChannel(channel)
		w.Show()
		a.applyChannelState(w, animate)

		return w
	}

	w := ui.NewChannelWidget(a.deps(), channel, func() { a.selectChannel(channelID) })
	a.applyChannelState(w, animate)
	w.Menu = func() []*fyne.MenuItem { return a.channelMenu(channelID) }

	return w
}

// callRows is who is in a voice channel's call, as the rows that hang under its
// own. Empty for anything that is not a voice channel, and for one nobody is in:
// a call with no participants is a channel row and nothing beneath it.
//
// They go in with the channel rather than beside it so a collapsed category takes
// them with it — a CategoryWidget hides the objects it was handed, and a
// participant left behind would hang under nothing.
func (a *App) callRows(channel domain.Channel) []fyne.CanvasObject {
	if channel.Kind != domain.ChannelVoice {
		return nil
	}

	participants := a.store.VoiceParticipants(channel.ID)
	if len(participants) == 0 {
		return nil
	}

	deps := a.deps()

	rows := make([]fyne.CanvasObject, 0, len(participants))
	for _, participant := range participants {
		row := ui.NewVoiceParticipantRow(deps, participant)

		// Built when the click arrives rather than captured: what the menu may offer
		// depends on permissions a role change can move under a standing sidebar.
		row.Menu = func() []*fyne.MenuItem {
			return a.voiceParticipantMenu(row, channel.ID, participant)
		}

		// A rebuilt row is a new object and knows nothing of who was talking when
		// the old one was dropped.
		row.SetSpeaking(a.speaking[participant.UserID])

		rows = append(rows, row)
	}

	return rows
}

// setChannelGroup fills the block pinned above the channel list, or empties it
// for a server, which has nothing to pin. The divider goes with the rows rather
// than in the column: it marks the group off from the list, and an empty group
// has nothing to mark. The block's height is what the column places the list
// from, hence the relayout — Fyne reclaims nothing for a shrunken slot.
func (a *App) setChannelGroup(rows []fyne.CanvasObject) {
	if len(rows) > 0 {
		rows = append(rows, ui.NewRowDivider())
	}

	a.channelTop.Objects = rows
	a.channelTop.Refresh()
	ui.Relayout(a.channelColumn)
}

// savedNotesID is the account's own conversation, which Revolt files among the
// direct messages like any other. Read off the kind rather than held, the list
// being a fetched snapshot that is replaced wholesale.
func (a *App) savedNotesID() string {
	for _, channelID := range a.dmChannels {
		if channel, ok := a.store.Channel(channelID); ok && channel.Kind == domain.ChannelSavedMessages {
			return channelID
		}
	}

	return ""
}

// applyChannelState paints a channel row from what the app knows about it. Three
// paths need it — building a row, syncing the sidebar, repainting one row — and
// they must agree, so the state is derived in one place. Both setters no-op on an
// unchanged value. animate is passed rather than read: syncChannelList reads the
// setting once for the whole sidebar.
func (a *App) applyChannelState(w *ui.ChannelWidget, animate bool) {
	channelID := w.Channel.ID

	w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID], a.mentionCount(channelID))
	w.SetTyping(a.isTypingIn(channelID), animate)
}

/* Context menus */

// serverMenu builds the items a server icon offers on right-click. Built per
// click rather than per row, so what it offers reflects the state now.
func (a *App) serverMenu(serverID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy server ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(serverID)
		}),
	}

	// Anything irreversible goes below a separator and asks first, so no single
	// misclick can act.
	if a.canLeaveServer(serverID) {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon("Leave server", fynetheme.LogoutIcon(), func() { a.confirmLeaveServer(serverID) }),
		)
	}

	return leadWithMarkRead(items, a.serverUnread(serverID), func() { a.markServerRead(serverID) })
}

// leadWithMarkRead puts "Mark as read" at the head of a menu, where the one thing
// clicked without reading the menu should be. Nothing to clear leaves it out
// rather than greying it: a disabled first item is a menu that looks broken.
func leadWithMarkRead(items []*fyne.MenuItem, unread bool, mark func()) []*fyne.MenuItem {
	if !unread {
		return items
	}

	return append([]*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Mark as read", fynetheme.ConfirmIcon(), mark),
		fyne.NewMenuItemSeparator(),
	}, items...)
}

// leadWithCall puts joining or leaving a call at the head of a voice channel's
// menu, and changes nothing for any other kind. Placed above Copy channel ID the
// way marking read leads, so a call is reachable from the sidebar without
// leaving whatever is being read.
func (a *App) leadWithCall(items []*fyne.MenuItem, channelID string) []*fyne.MenuItem {
	if a.callChannelID == channelID {
		return append([]*fyne.MenuItem{
			fyne.NewMenuItemWithIcon("Disconnect", ui.CautionMark(assets.CallEndIcon), a.leaveCall),
			fyne.NewMenuItemSeparator(),
		}, items...)
	}

	if !a.canJoinCall(channelID) {
		return items
	}

	return append([]*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Join call", assets.MicIcon, func() { a.joinCall(channelID) }),
		fyne.NewMenuItemSeparator(),
	}, items...)
}

// channelMenu builds the items a channel row offers on right-click. A DM row is
// a channel like any other here, so the home view needs no special case.
func (a *App) channelMenu(channelID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy channel ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(channelID)
		}),
	}

	// A group is joined by being put in rather than by redeeming anything, so the
	// two ways in are named apart: this one adds somebody, the one below hands out
	// a code.
	if a.canInviteToGroup(channelID) {
		items = append(items,
			fyne.NewMenuItemWithIcon("Add people", assets.SystemAddedIcon,
				func() { a.showInviteToGroup(channelID) }),
		)
	}

	// Offered here rather than on the server icon: Revolt has no server-wide invite,
	// only one per *channel*, which is where it lands the joiner.
	if a.canInviteTo(channelID) {
		items = append(items,
			fyne.NewMenuItemWithIcon("Create invite", fynetheme.MailSendIcon(),
				func() { a.createInvite(channelID) }),
		)
	}

	// A group is configured on a page of its own rather than through the edit card:
	// it has a picture and a say in what the people in it may do, neither of which
	// is a field. So the two are alternatives — one surface per channel, never a
	// card and a page both offering to rename it.
	switch {
	case a.canManageGroup(channelID):
		items = append(items,
			fyne.NewMenuItemWithIcon("Group settings", fynetheme.SettingsIcon(),
				func() { a.openGroupSettings(channelID) }),
		)
	case a.canEditChannel(channelID):
		items = append(items,
			fyne.NewMenuItemWithIcon("Edit channel", fynetheme.SettingsIcon(),
				func() { a.editChannel(channelID) }),
		)
	}

	// Only a conversation can be closed: a server's channels are not the user's to
	// remove from their own sidebar.
	if a.isConversation(channelID) {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon(a.closeChannelLabel(channelID), fynetheme.LogoutIcon(),
				func() { a.confirmCloseChannel(channelID) }),
		)
	}

	unread := a.unreadChannels[channelID] || a.mentionCount(channelID) > 0
	items = leadWithMarkRead(items, unread, func() { a.markChannelRead(channelID) })

	// Above everything else, the way marking read leads: a call is joinable
	// without leaving the channel being read.
	return a.leadWithCall(items, channelID)
}

// closeChannelLabel names what closing a conversation means for its kind: a
// group is left, a direct message merely leaves the sidebar.
func (a *App) closeChannelLabel(channelID string) string {
	if channel, ok := a.store.Channel(channelID); ok && channel.Kind == domain.ChannelGroup {
		return "Leave group"
	}

	return "Close conversation"
}

// memberMenu builds the items a member row offers on right-click. Every entry is
// offered only where it can actually be taken — Revolt gates each of these
// separately and refuses the rest — so the menu never presents an action the
// server will refuse.
//
// A menu item carries no colour of its own, so the mark is what separates them:
// a timeout ends by itself, a kick is undone by a new invite, a ban until it is
// lifted is not.
func (a *App) memberMenu(serverID, userID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy user ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(userID)
		}),
	}

	edits := append(a.memberNicknameItems(serverID, userID), a.memberRoleItems(serverID, userID)...)
	if len(edits) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		items = append(items, edits...)
	}

	// Between the roles and the timeout: each is undone by doing the opposite, so
	// they are caution rather than danger, and they belong above the kick and ban
	// the separator below leads.
	if voice := a.memberVoiceItems(serverID, userID); len(voice) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		items = append(items, voice...)
	}

	timeout := a.memberTimeoutItems(serverID, userID)
	kick, ban := a.canKickMember(serverID, userID), a.canBanMember(serverID, userID)

	if len(timeout) > 0 || kick || ban {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	items = append(items, timeout...)
	if kick {
		items = append(items, fyne.NewMenuItemWithIcon("Kick", ui.CautionMark(assets.SystemKickedIcon),
			func() { a.confirmKickMember(serverID, userID) }))
	}
	if ban {
		items = append(items, fyne.NewMenuItemWithIcon("Ban", ui.DangerMark(assets.SystemBannedIcon),
			func() { a.promptBanMember(serverID, userID) }))
	}

	return items
}

// serverUnread reports whether any of a server's channels is marked unread.
func (a *App) serverUnread(serverID string) bool {
	server, ok := a.store.Server(serverID)
	if !ok {
		return false
	}

	return slices.ContainsFunc(server.Channels, func(channelID string) bool {
		return a.unreadChannels[channelID]
	})
}

// markChannelRead clears a channel's unread mark and acknowledges its newest
// message, through the same coalescing path selecting the channel would use.
func (a *App) markChannelRead(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok || channel.LastMessageID == "" {
		return
	}

	delete(a.unreadChannels, channelID)
	a.clearMentions(channelID)
	a.refreshChannelRow(channelID)
	a.scheduleAck(channelID, channel.LastMessageID)
}

// markServerRead clears the unread marks of every channel in a server. The
// server-wide ack is a single request covering all of them, so this one doesn't
// go through the per-channel coalescing in events.go.
func (a *App) markServerRead(serverID string) {
	server, ok := a.store.Server(serverID)
	if !ok {
		return
	}

	for _, channelID := range server.Channels {
		delete(a.unreadChannels, channelID)
		a.clearMentions(channelID)
	}
	a.syncChannelList()

	a.background(
		func() error { return a.client.AckServer(serverID) },
		func(err error) { log.Printf("ack server %s: %v", serverID, err) },
	)
}

/* Selection */

// selectServer switches to a server and selects its first channel. Re-clicking
// the open one is a no-op: it would rebuild both sidebars and yank the view back
// to the first channel.
func (a *App) selectServer(serverID string) {
	if a.currentServerID == serverID && !a.homeSelected {
		return
	}

	server, ok := a.enterServer(serverID)
	if !ok {
		return
	}

	if channelID, ok := a.firstVisibleChannel(server); ok {
		a.selectChannel(channelID)
		return
	}

	a.clearChannelSelection()
	a.showStatus("No channels you can see in this server")
}

// firstVisibleChannel is the channel a server opens on: the first in its own
// order the account can see. Landing on a hidden one would show the no-access
// line for a server whose channels are perfectly readable.
func (a *App) firstVisibleChannel(server domain.Server) (string, bool) {
	for _, channelID := range server.Channels {
		if channel, ok := a.store.Channel(channelID); ok && a.canViewChannel(channel) {
			return channelID, true
		}
	}

	return "", false
}

// canViewChannel is the one permission the client answers by hiding rather than
// refusing: a channel you cannot look into has nothing to offer a sidebar row.
// Only a server decides it — a conversation is in the user's own list because
// they are in it, and a closed DM leaves by its Active flag, not a permission.
func (a *App) canViewChannel(channel domain.Channel) bool {
	return channel.ServerID == "" || a.store.Permissions(channel.ID).Has(domain.PermissionViewChannel)
}

// enterServer switches both sidebars to a server without choosing a channel.
// Following a #mention lands on the one it names, and going through selectServer
// to get there would load the first channel's history on the way past.
func (a *App) enterServer(serverID string) (domain.Server, bool) {
	server, ok := a.store.Server(serverID)
	if !ok {
		return domain.Server{}, false
	}

	a.homeSelected = false
	a.currentServerID = serverID

	a.syncServerSelection(serverID)
	a.setHeader(a.serverHeader, server.Name)
	a.syncServerCog()
	a.syncGroupAdd()
	a.refreshChannelList()

	// The emoji of the server being entered come first in the composer's list, as
	// they do in the picker, so the order is re-taken here rather than only when the
	// set changes.
	a.refreshEmojiCandidates()

	// Paint what is known, then fetch the rest. Both selectServer and #mention
	// navigation funnel through here, so it is the one place to ask for membership.
	a.refreshMemberList()
	a.loadMembers(serverID)

	return server, true
}

// openChannel moves the sidebars to wherever a channel lives and selects it,
// reporting whether it could. What it names need not be in the open server, or
// in a server at all: following a rendered #mention and following a card in the
// mention inbox are the same walk, the inbox's cards coming from as many servers
// as the account is in.
func (a *App) openChannel(channelID string) bool {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canViewChannel(channel) {
		return false
	}

	// A conversation lives in the home view, which knows how to open one its list
	// has not caught up with.
	if channel.ServerID == "" {
		a.showConversation(channelID)

		return true
	}

	if a.homeSelected || a.currentServerID != channel.ServerID {
		if _, ok := a.enterServer(channel.ServerID); !ok {
			return false
		}
	}
	a.selectChannel(channelID)

	return true
}

// OnChannelTapped follows a rendered #mention. Saying a channel is unavailable
// beats a click that does nothing.
func (a *App) OnChannelTapped(channelID string) {
	if !a.openChannel(channelID) {
		a.notify(ui.ToneWarning, "That channel isn't available.")
	}
}

// OnServerTapped goes to a server the account is already in, as an invite card's
// "Go to server" does; nothing inside is named, so selectServer picks the first
// visible channel. A server the store has never heard of is one since left — the
// card was drawn from an invite resolved earlier — so it says so rather than
// opening an empty shell.
func (a *App) OnServerTapped(serverID string) {
	if _, ok := a.store.Server(serverID); !ok {
		a.notify(ui.ToneWarning, "That server isn't available.")
		return
	}

	a.selectServer(serverID)
}

// selectChannel switches to a channel, acknowledging unreads and painting from
// cache where it can. What the account may do decides how far the switch goes: a
// channel it cannot see is a dead end, and the slowmode request and first page of
// history would be two requests the server can only refuse.
func (a *App) selectChannel(channelID string) {
	if a.currentChannelID == channelID {
		return
	}

	// What was half-composed stays in the entry, but the channel being left should
	// stop being told it is still being written in. The friends page goes too — it
	// stands in the message column's slot — and it never holds a selection to be
	// this one, showFriendsPage having cleared it on the way in.
	a.stopTyping(a.currentChannelID)
	a.leaveFriendsPage()

	// A selection belongs to the channel it was picked in: the window is about to
	// be replaced, and a set carried across the switch is one the reader can no
	// longer see they hold. Before syncComposer, which reads the mode.
	a.endSelection()

	unread := a.unreadChannels[channelID] || a.mentionCount(channelID) > 0
	channel, known := a.store.Channel(channelID)
	permissions := a.store.Permissions(channelID)
	viewable := known && a.canViewChannel(channel)
	a.currentChannelID = channelID

	a.syncChannelKind()
	a.syncChannelTopic()
	if known {
		a.setHeader(a.channelHeader, channel.Name)
		if viewable && unread && channel.LastMessageID != "" {
			delete(a.unreadChannels, channelID)
			a.clearMentions(channelID)
			a.scheduleAck(channelID, channel.LastMessageID)
		}
	}

	a.syncChannelList()
	a.refreshRecipients()
	a.ensureRecipients()
	a.syncComposer()
	a.refreshSlowmode()
	a.refreshTyping() // whoever was typing here while the channel was in the background

	if !viewable {
		a.showStatus("You don't have access to this channel")
		return
	}

	a.focusInput() // so the user can type straight away

	// Seeing a channel and reading what was said in it are separate permissions: an
	// announcement channel can be one without the other, and asking anyway is refused.
	if !permissions.Has(domain.PermissionReadMessageHistory) {
		a.showStatus("You can't read this channel's history")
		return
	}

	if cached := a.client.Messages().Get(channelID); len(cached) > 0 {
		a.displayMessages(cached)
		return
	}

	a.showStatus("Loading messages...")
	a.loadChannelMessages(channelID)
}

// clearChannelSelection deselects the current channel and clears the view. The
// friends page goes with it — it stands in the message column's slot, so
// whatever is putting the column back is also taking the page down.
func (a *App) clearChannelSelection() {
	a.stopTyping(a.currentChannelID)
	a.leaveFriendsPage()
	a.endSelection()

	a.currentChannelID = ""
	a.clearMessages()
	a.setHeader(a.channelHeader, "")
	a.syncChannelKind()
	a.syncChannelTopic()
	a.syncChannelList()
	a.refreshRecipients() // a group's people go with the group, and a server's stay
	a.refreshSlowmode()
	a.refreshTyping()
	a.syncComposer()
}

// syncServerSelection updates the highlighted server icon. The home button is
// part of the same one-of-N selection, so it is lit or cleared here rather than
// by whichever handler happens to run.
func (a *App) syncServerSelection(selectedID string) {
	if a.homeButton != nil {
		a.homeButton.SetSelected(a.homeSelected)
	}
	for _, obj := range a.serverList.Objects {
		if w, ok := obj.(*ui.ServerWidget); ok {
			w.SetSelected(w.Server.ID == selectedID)
		}
	}

	// Selection outranks the mention bar and they share one marker, so the icon
	// being entered or left is one that has to re-decide which it wears.
	a.syncMentionMarks()
}

// channelRows walks every mounted channel row: the pinned group and the list.
// Saved Notes is in the first and answers to selection, unread and typing exactly
// as the conversations do, so a walk of the list alone would leave one row that
// never repaints.
func (a *App) channelRows() iter.Seq[*ui.ChannelWidget] {
	return func(yield func(*ui.ChannelWidget) bool) {
		for _, host := range [...]*fyne.Container{a.channelTop, a.channelList} {
			for _, obj := range host.Objects {
				if w, ok := obj.(*ui.ChannelWidget); ok && !yield(w) {
					return
				}
			}
		}
	}
}

// syncChannelList refreshes the selection, unread and typing state of every
// channel row, and the friends row above them — it answers to selection as they
// do, the page it opens standing in the same slot their messages would.
func (a *App) syncChannelList() {
	animate := config.Current().Behaviour.TypingAnimation

	for w := range a.channelRows() {
		a.applyChannelState(w, animate)
	}

	a.syncFriendsRow(a.awaitingAnswer())
}

// releaseChannelRows stops what the rows about to be dropped are still running.
// A row is discarded by having the list forget it, which tells the row nothing:
// Fyne destroys a renderer — and the animation its Destroy stops — only when its
// cache expires the widget, a minute after the last paint. A mark left sweeping
// repaints sixty times a second for a row nothing can see, and every rebuild of a
// sidebar somebody is typing in adds another.
func (a *App) releaseChannelRows() {
	for w := range a.channelRows() {
		w.SetTyping(false, false)
	}
}

// refreshChannelRow repaints one row. On the per-message hot path, so a message
// in a background channel does not refresh the whole sidebar.
func (a *App) refreshChannelRow(channelID string) {
	for w := range a.channelRows() {
		if w.Channel.ID == channelID {
			a.applyChannelState(w, config.Current().Behaviour.TypingAnimation)
			return
		}
	}
}

// setHeader updates a header label if it exists. Guarded on the value as well as
// on the label: this is called for every channel event Revolt sends about the open
// server, and widget.Label.SetText refreshes unconditionally — which for Fyne is
// the whole window. Reading the text back is safe here where it would not be on an
// ellipsis box, a Label keeping what it was given and shortening in its provider.
func (a *App) setHeader(label *widget.Label, text string) {
	if label == nil || label.Text == text {
		return
	}

	label.SetText(text)
}

// channelName returns the current channel's display name, or a fallback.
func (a *App) channelName() string {
	if channel, ok := a.currentChannel(); ok {
		return channel.Name
	}

	return "channel"
}

/* The home view */

// homeHeader titles the channel sidebar while the home view is open, standing
// in for the server name; newGroupTip labels the button beside it.
const (
	homeHeader  = "Direct Messages"
	newGroupTip = "New group"
)

// syncGroupAdd shows the header's new-group button in the home view alone. It
// makes a conversation, and a server's channels are not conversations — the card
// it opens picks from this account's friends, which no server has to do with.
func (a *App) syncGroupAdd() {
	if a.groupAdd == nil {
		return
	}

	if a.homeSelected {
		a.groupAdd.Show()
		return
	}

	a.groupAdd.Hide()
}

// selectHome opens the home view. The cached list paints at once and a refresh
// fires regardless: it is a fetched snapshot with no gateway event behind it, so
// re-opening home is the moment to re-ask. Re-clicking is a no-op — it would yank
// the view back to the first conversation.
func (a *App) selectHome() {
	if a.homeSelected {
		return
	}

	a.homeSelected = true
	a.currentServerID = ""

	a.syncServerSelection("")
	a.setHeader(a.serverHeader, homeHeader)
	a.syncServerCog()
	a.syncGroupAdd()
	a.refreshChannelList()
	a.refreshMemberList()

	if len(a.dmChannels) > 0 {
		a.selectChannel(a.dmChannels[0])
	} else {
		a.clearChannelSelection()
		a.showStatus("Loading direct messages...")
	}
	a.loadDirectMessages()
}

// loadDirectMessages refreshes the cached conversation list. Stale-while-
// revalidate: what is cached stays on screen until the response lands, so
// re-opening home never blanks the sidebar. Recipients missing from State are
// resolved in the same pass — a DM is titled after the other participant.
func (a *App) loadDirectMessages() {
	if a.loadingDMs || !a.client.Connected() {
		return
	}

	a.loadingDMs = true
	epoch := a.epoch

	go func() {
		// Every hop back re-checks the session: a logout and re-login can land
		// mid-request, and the old account's conversations must not paint into the new
		// one's sidebar.
		defer a.doOnUI(func() {
			if !a.stale(epoch) {
				a.loadingDMs = false
			}
		}, false)

		channels, err := a.client.Conversations()
		if err != nil {
			log.Printf("fetch direct messages: %v", err)
			a.doOnUI(func() {
				if !a.stale(epoch) && a.homeSelected && len(a.dmChannels) == 0 {
					a.showStatus("Failed to load direct messages")
				}
			}, false)
			return
		}

		a.doOnUI(func() {
			if !a.stale(epoch) {
				a.setDirectMessages(channels)
			}
		}, false)
	}()
}

// setDirectMessages records the sidebar order and repaints the home view when
// it's open, selecting the first conversation if none is. Call on the UI thread.
func (a *App) setDirectMessages(channels []domain.Channel) {
	a.dmChannels = sortConversations(channels)

	if !a.homeSelected {
		return
	}

	a.refreshChannelList()

	switch {
	case len(a.dmChannels) == 0:
		a.clearChannelSelection()
		a.showStatus("No direct messages yet")
	case a.currentChannelID == "":
		a.selectChannel(a.dmChannels[0])
	default:
		a.syncChannelList()
	}
}

// sortConversations drops closed DMs, orders the rest by most recent activity and
// answers with IDs alone: the channels themselves are already in State, so a
// second copy is a cache of a cache. LastMessageID compares directly, ULIDs
// sorting chronologically as strings. The order is a snapshot — a new message
// marks its row unread but does not re-sort the sidebar under the reader.
func sortConversations(channels []domain.Channel) []string {
	channels = slices.DeleteFunc(channels, func(channel domain.Channel) bool {
		return channel.Kind == domain.ChannelDM && !channel.Active
	})
	slices.SortStableFunc(channels, func(x, y domain.Channel) int {
		return strings.Compare(y.LastMessageID, x.LastMessageID)
	})

	ids := make([]string, len(channels))
	for i, channel := range channels {
		ids[i] = channel.ID
	}

	return ids
}
