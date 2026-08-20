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
// Three layers sit over the row — notices, the tooltip (a server icon's name has
// to overhang the narrow column it is anchored in) and the settings page. The
// first two match no pointer event bar a notice card itself, so the row keeps
// receiving every click and hover. Settings is a layer rather than an overlay
// because the modal layer holds one thing at a time, and a confirmation raised
// from the page has to draw over it.
func (a *App) buildUI() fyne.CanvasObject {
	a.mainRow = ui.NewFillRow(2,
		a.buildServerList(),
		a.buildChannelList(),
		a.buildMessageArea(),
		a.buildMemberList(),
	)

	return container.NewStack(a.mainRow, a.notices.Layer, a.tooltip.Layer, a.settings.Layer)
}

/* Server sidebar */

// buildServerList is the server rail: fixed home and settings buttons bookending
// the scrolling icons, outside the scroll so they stay put as the list grows.
func (a *App) buildServerList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ServerListBackground)

	a.homeButton = ui.NewSidebarButton(fynetheme.HomeIcon(), a.selectHome)
	settings := ui.NewSidebarButton(fynetheme.SettingsIcon(), a.openSettings)

	top := container.NewVBox(
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		container.NewCenter(a.homeButton),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		ui.NewSidebarSeparator(),
	)
	bottom := container.NewVBox(
		ui.NewSidebarSeparator(),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		container.NewCenter(settings),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
	)

	a.refreshServerList()
	content := container.NewBorder(top, bottom, nil, nil, container.NewVScroll(a.serverList))

	// Each divider is the last child of the column it edges, so the main row keeps
	// its four children — see ui.NewColumnDivider.
	return ui.NewFixedWidthContainer(theme.Sizes.ServerSidebarWidth, background,
		ui.NewFillRow(0, content, ui.NewColumnDivider()))
}

// refreshServerList rebuilds the icons. Any tooltip is taken down first: the icon
// that raised it is about to be replaced, and will never report the pointer
// leaving.
func (a *App) refreshServerList() {
	a.tooltip.Hide()

	icons := make([]fyne.CanvasObject, 0, len(a.serverIDs)+1)
	for _, serverID := range a.serverIDs {
		server, ok := a.store.Server(serverID)
		if !ok {
			continue
		}

		w := ui.NewServerWidget(a.images, server, func() { a.selectServer(serverID) })
		w.SetSelected(serverID == a.currentServerID)
		w.OnHover = func(hovering bool) {
			if hovering {
				a.tooltip.Show(server.Name, w)
				return
			}
			a.tooltip.Hide()
		}
		w.Menu = func() []*fyne.MenuItem { return a.serverMenu(serverID) }

		// Bare, not wrapped in a Center: ServerWidget centres its own icon, and
		// keeping it at the top level lets syncServerSelection find it unwrapped.
		icons = append(icons, w)
	}

	// The join button reads as one more server icon at the end, so it lives inside
	// the scroll rather than in the fixed bookends. The selection sync skips it,
	// not being a ServerWidget.
	icons = append(icons, ui.NewSidebarButton(fynetheme.ContentAddIcon(), a.showJoinServer))

	a.serverList.Objects = icons
	a.serverList.Refresh()

	// Which servers the account is in is also which emoji it may type, and this runs
	// wherever that changes: ready, joined, left.
	a.refreshEmojiCandidates()
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

	a.refreshChannelList()

	pad := theme.Sizes.ChannelSidebarPadding
	scroll := container.NewBorder(nil, nil, ui.HorizontalSpacer(pad), ui.HorizontalSpacer(pad),
		container.NewVScroll(a.channelList))

	// The pinned group sits outside that padding and above the scroll: full column
	// width, which is what says it is not one of the rows below, and it does not
	// scroll away from what it leads to.
	header := ui.VBoxNoSpacing(container.NewPadded(a.serverHeader), a.channelTop)
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
	a.releaseChannelRows()

	animate := config.Current().Behaviour.TypingAnimation

	// Written once at the end rather than through Container.Add, which refreshes
	// the whole column per child.
	var rows []fyne.CanvasObject
	mount := func() {
		a.channelList.Objects = rows
		a.channelList.Refresh()
	}

	if a.homeSelected {
		// Rebuilt with the list rather than kept aside: the sidebar's objects are
		// replaced wholesale, so a row held across one is a widget in no container.
		a.friendsRow = ui.NewFriendsRow(a.showFriends)

		// Neither of these is a conversation with somebody, so they are pinned as
		// their own group rather than sorted among the ones that are — Saved Notes
		// would move about with its last message, and the friends list sorts by
		// nothing at all.
		group := []fyne.CanvasObject{a.friendsRow}
		saved := a.savedNotesID()
		if w := a.newChannelRow(saved, animate); w != nil {
			group = append(group, w)
		}
		a.setChannelGroup(group)

		for _, channelID := range a.dmChannels {
			if channelID == saved {
				continue
			}
			if w := a.newChannelRow(channelID, animate); w != nil {
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
		if w := a.newChannelRow(channelID, animate); w != nil {
			candidates = append(candidates, ui.NewChannelCandidate(w.Channel))
			rows = append(rows, w)
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
			if w := a.newChannelRow(channelID, animate); w != nil {
				candidates = append(candidates, ui.NewChannelCandidate(w.Channel))
				under = append(under, w)
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
// animate is passed rather than read, as applyChannelState's is and for the same
// reason: one rebuild reads the setting once for every row it makes.
func (a *App) newChannelRow(channelID string, animate bool) *ui.ChannelWidget {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canViewChannel(channel) {
		return nil
	}

	w := ui.NewChannelWidget(a.deps(), channel, func() { a.selectChannel(channelID) })
	a.applyChannelState(w, animate)
	w.Menu = func() []*fyne.MenuItem { return a.channelMenu(channelID) }

	return w
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

	w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])
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

// channelMenu builds the items a channel row offers on right-click. A DM row is
// a channel like any other here, so the home view needs no special case.
func (a *App) channelMenu(channelID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy channel ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(channelID)
		}),
	}

	// Offered here rather than on the server icon: Revolt has no server-wide invite,
	// only one per *channel*, which is where it lands the joiner.
	if a.canInviteTo(channelID) {
		items = append(items,
			fyne.NewMenuItemWithIcon("Create invite", fynetheme.MailSendIcon(),
				func() { a.createInvite(channelID) }),
		)
	}

	if a.canEditChannel(channelID) {
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

	return leadWithMarkRead(items, a.unreadChannels[channelID], func() { a.markChannelRead(channelID) })
}

// closeChannelLabel names what closing a conversation means for its kind: a
// group is left, a direct message merely leaves the sidebar.
func (a *App) closeChannelLabel(channelID string) string {
	if channel, ok := a.store.Channel(channelID); ok && channel.Kind == domain.ChannelGroup {
		return "Leave group"
	}

	return "Close conversation"
}

// memberMenu builds the items a member row offers on right-click. Each way out
// of the server is offered only where it can actually be taken (canKickMember,
// canBanMember), so the menu never presents an action the server will refuse.
//
// A menu item carries no colour of its own, so the mark is what separates the
// two: a kick is undone by a new invite, a ban until it is lifted is not.
func (a *App) memberMenu(serverID, userID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy user ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(userID)
		}),
	}

	kick, ban := a.canKickMember(serverID, userID), a.canBanMember(serverID, userID)
	if kick || ban {
		items = append(items, fyne.NewMenuItemSeparator())
	}
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

// OnChannelTapped follows a rendered #mention. What it names need not be in the
// open server, or in a server at all, so the sidebars move to wherever it lives
// before it is selected. Saying a channel is unavailable beats a click that does
// nothing.
func (a *App) OnChannelTapped(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canViewChannel(channel) {
		a.notify(ui.ToneWarning, "That channel isn't available.")
		return
	}

	// A conversation lives in the home view, which knows how to open one its list
	// has not caught up with.
	if channel.ServerID == "" {
		a.showConversation(channelID)
		return
	}

	if a.homeSelected || a.currentServerID != channel.ServerID {
		if _, ok := a.enterServer(channel.ServerID); !ok {
			a.notify(ui.ToneWarning, "That channel isn't available.")
			return
		}
	}
	a.selectChannel(channelID)
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
	// stop being told it is still being written in.
	a.stopTyping(a.currentChannelID)

	unread := a.unreadChannels[channelID]
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
			a.scheduleAck(channelID, channel.LastMessageID)
		}
	}

	a.syncChannelList()
	a.refreshMentionCandidates()
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

// clearChannelSelection deselects the current channel and clears the view.
func (a *App) clearChannelSelection() {
	a.stopTyping(a.currentChannelID)

	a.currentChannelID = ""
	a.clearMessages()
	a.setHeader(a.channelHeader, "")
	a.syncChannelKind()
	a.syncChannelTopic()
	a.syncChannelList()
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
// channel row.
func (a *App) syncChannelList() {
	animate := config.Current().Behaviour.TypingAnimation

	for w := range a.channelRows() {
		a.applyChannelState(w, animate)
	}
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

// setHeader updates a header label if it exists.
func (a *App) setHeader(label *widget.Label, text string) {
	if label != nil {
		label.SetText(text)
	}
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
// in for the server name.
const homeHeader = "Direct Messages"

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
