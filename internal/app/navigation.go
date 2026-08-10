package app

import (
	"log"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// buildUI assembles the four-column layout: servers | channels | messages |
// members. The fill row keeps the sections flush for the flat look; only the
// message area (index 2) stretches, the rest stay at their fixed widths.
//
// Three layers sit over the whole row: notices, which appear in its top-right
// corner whatever column is there; the tooltip, since a server icon's name has
// to be able to overhang the narrow column it is anchored in; and the settings
// page. The first two carry nothing that matches a pointer event bar a notice
// card itself, so the row underneath keeps receiving every click and hover.
//
// Settings is a layer here rather than an overlay because the modal layer holds
// one thing at a time: a confirmation raised from the settings page has to be
// able to draw over it. It is hidden until opened, and opaque when it is not.
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

// buildServerList builds the server icon sidebar: fixed home and settings buttons
// bookend the scrolling icons, each set off by a short separator. They sit
// outside the scroll, so they stay put when the list grows tall enough to scroll.
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

	// The seam is drawn by the column to its left, so each divider is the last
	// child of the column it edges and the main row keeps its four children.
	return ui.NewFixedWidthContainer(theme.Sizes.ServerSidebarWidth, background,
		ui.NewFillRow(0, content, ui.NewColumnDivider()))
}

// refreshServerList rebuilds the server icons from the current server list. Any
// tooltip is taken down first: the icon that raised it is about to be replaced,
// so it will never report the pointer leaving.
func (a *App) refreshServerList() {
	a.serverList.Objects = nil
	a.tooltip.Hide()

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

		// Added bare, not wrapped in a Center: ServerWidget already centres its own
		// icon, and keeping the widget at the top level lets syncServerSelection
		// find it without unwrapping.
		a.serverList.Add(w)
	}

	// The join button reads as one more server icon at the end of the list, so it
	// lives inside the scroll rather than in the fixed bookends. Objects are
	// rebuilt wholesale here, hence re-adding it every time; the selection sync
	// skips it because it isn't a ServerWidget.
	a.serverList.Add(ui.NewSidebarButton(fynetheme.ContentAddIcon(), a.showJoinServer))
	a.serverList.Refresh()
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
	content := container.NewBorder(container.NewPadded(a.serverHeader), nil, nil, nil, scroll)

	return ui.NewFixedWidthContainer(theme.Sizes.ChannelSidebarWidth, background,
		ui.NewFillRow(0, content, ui.NewColumnDivider()))
}

// refreshChannelList rebuilds the channel rows for the current server, grouping
// channels under their categories — or, in the home view, the flat list of
// cached direct messages and groups, which has no categories to group under.
//
// The composer's #mention candidates come off this same walk, as the member
// sidebar's @mentions come off its own: they are the same channels under the
// same names, in the order the sidebar lists them. The home view contributes
// none — a conversation is not something a message can link to.
func (a *App) refreshChannelList() {
	a.channelList.Objects = nil

	if a.homeSelected {
		for _, channelID := range a.dmChannels {
			if w := a.newChannelRow(channelID); w != nil {
				a.channelList.Add(w)
			}
		}
		a.channelList.Refresh()
		a.setMentionCandidates(ui.MentionChannel, nil)
		return
	}

	server, ok := a.currentServer()
	if !ok {
		a.channelList.Refresh()
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
		if w := a.newChannelRow(channelID); w != nil {
			candidates = append(candidates, ui.NewChannelCandidate(w.Channel))
			a.channelList.Add(w)
		}
	}

	for i, category := range server.Categories {
		key := server.ID + ":" + category.ID
		header := ui.NewCategoryWidget(category.Title, func(collapsed bool) {
			a.collapsedCategories[key] = collapsed
		})
		header.SetFirst(i == 0)

		var rows []fyne.CanvasObject
		for _, channelID := range category.Channels {
			if w := a.newChannelRow(channelID); w != nil {
				candidates = append(candidates, ui.NewChannelCandidate(w.Channel))
				rows = append(rows, w)
			}
		}

		a.channelList.Add(header)
		for _, row := range rows {
			a.channelList.Add(row)
		}

		header.SetChannels(rows, a.channelList)
		if a.collapsedCategories[key] {
			header.SetCollapsed(true)
		}
	}

	a.channelList.Refresh()
	a.setMentionCandidates(ui.MentionChannel, candidates)
}

// newChannelRow builds a channel row reflecting its current state, or nil when
// the store doesn't know the channel or the account cannot see it.
//
// A channel hidden this way is hidden everywhere at once: the sidebar walk that
// builds these rows is also what feeds the composer its #mention candidates, so
// one that never becomes a row is never a candidate either.
func (a *App) newChannelRow(channelID string) *ui.ChannelWidget {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canViewChannel(channel) {
		return nil
	}

	w := ui.NewChannelWidget(a.deps(), channel, func() { a.selectChannel(channelID) })
	a.applyChannelState(w, config.Current().Behaviour.TypingAnimation)
	w.Menu = func() []*fyne.MenuItem { return a.channelMenu(channelID) }

	return w
}

// applyChannelState paints a channel row from what the app currently knows about
// it. Three paths need this — building a row, syncing the whole sidebar, and
// repainting one row — and they must agree, so the row's state is derived in one
// place. Both setters no-op on unchanged state, so calling it costs nothing for a
// row that did not move.
//
// animate is passed rather than read because syncChannelList reads the setting
// once for the whole sidebar.
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
	// misclick in a menu can act.
	if a.canLeaveServer(serverID) {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon("Leave server", fynetheme.LogoutIcon(), func() { a.confirmLeaveServer(serverID) }),
		)
	}

	if a.serverUnread(serverID) {
		items = append([]*fyne.MenuItem{
			fyne.NewMenuItemWithIcon("Mark as read", fynetheme.ConfirmIcon(), func() { a.markServerRead(serverID) }),
			fyne.NewMenuItemSeparator(),
		}, items...)
	}

	return items
}

// channelMenu builds the items a channel row offers on right-click. A DM row is
// a channel like any other here, so the home view needs no special case.
func (a *App) channelMenu(channelID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy channel ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(channelID)
		}),
	}

	// Only a conversation can be closed; a server's channels are not the user's
	// to remove from their own sidebar.
	if a.isConversation(channelID) {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon(a.closeChannelLabel(channelID), fynetheme.LogoutIcon(),
				func() { a.confirmCloseChannel(channelID) }),
		)
	}

	if a.unreadChannels[channelID] {
		items = append([]*fyne.MenuItem{
			fyne.NewMenuItemWithIcon("Mark as read", fynetheme.ConfirmIcon(), func() { a.markChannelRead(channelID) }),
			fyne.NewMenuItemSeparator(),
		}, items...)
	}

	return items
}

// closeChannelLabel names what closing a conversation means for its kind: a
// group is left, a direct message merely leaves the sidebar.
func (a *App) closeChannelLabel(channelID string) string {
	if channel, ok := a.store.Channel(channelID); ok && channel.Kind == domain.ChannelGroup {
		return "Leave group"
	}

	return "Close conversation"
}

// memberMenu builds the items a member row offers on right-click. Removing
// someone is only offered to a user who can actually do it — see canKickMember —
// so the menu never presents an action the server will refuse.
func (a *App) memberMenu(serverID, userID string) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItemWithIcon("Copy user ID", fynetheme.ContentCopyIcon(), func() {
			ui.CopyToClipboard(userID)
		}),
	}

	if a.canKickMember(serverID, userID) {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItemWithIcon("Remove from server", fynetheme.ContentRemoveIcon(),
				func() { a.confirmKickMember(serverID, userID) }),
		)
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
// the current server is a no-op, which would otherwise rebuild both sidebars and
// yank the view to the first channel.
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

// firstVisibleChannel is the channel a server opens on: the first one in its own
// order the account can actually see. Landing on a hidden one would show the
// no-access line for a server whose channels are perfectly readable.
func (a *App) firstVisibleChannel(server domain.Server) (string, bool) {
	for _, channelID := range server.Channels {
		if channel, ok := a.store.Channel(channelID); ok && a.canViewChannel(channel) {
			return channelID, true
		}
	}

	return "", false
}

// canViewChannel reports whether the account may see a channel at all. It is the
// one permission the client answers by hiding rather than by refusing: a channel
// you cannot look into has nothing to offer a sidebar row.
//
// Only a server ever decides this. A conversation is in the user's own list
// because they are in it — a group's permission field says what they may do
// there, not whether the row should exist, and a closed DM leaves the list by
// its own Active flag rather than by a permission.
func (a *App) canViewChannel(channel domain.Channel) bool {
	return channel.ServerID == "" || a.store.Permissions(channel.ID).Has(domain.PermissionViewChannel)
}

// enterServer switches both sidebars to a server without choosing a channel in
// it. Selecting one lands on the first; following a #mention lands on the one it
// names, and going through selectServer to get there would load the first
// channel's history on the way past.
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

	// Paint what is known, then go and get the rest: this is the single funnel
	// both selectServer and #mention navigation pass through, so it is the one
	// place a server's membership is worth asking for.
	a.refreshMemberList()
	a.loadMembers(serverID)

	return server, true
}

// OnChannelTapped follows a rendered #mention. What it names need not be in the
// open server — or in a server at all — so the sidebars are moved to wherever it
// lives first, and only then is it selected. A channel the account cannot see
// never resolves, and saying so is better than a click that does nothing.
func (a *App) OnChannelTapped(channelID string) {
	channel, ok := a.store.Channel(channelID)
	if !ok || !a.canViewChannel(channel) {
		a.notify(ui.ToneWarning, "That channel isn't available.")
		return
	}

	// A conversation lives in the home view, which already knows how to open one
	// that its list hasn't caught up with.
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
// "Go to server" does. Unlike a channel there is nothing named to open inside it,
// so selectServer picks the first one visible.
//
// A server the store has never heard of is one the account has since left — the
// card was drawn from an invite resolved earlier in the session — so it says so
// rather than opening an empty shell.
func (a *App) OnServerTapped(serverID string) {
	if _, ok := a.store.Server(serverID); !ok {
		a.notify(ui.ToneWarning, "That server isn't available.")
		return
	}

	a.selectServer(serverID)
}

// selectChannel switches to a channel, acknowledging unreads and showing its
// messages from cache when available.
//
// What the account may do here decides how far the switch goes: a channel it
// cannot see is a dead end, and firing the slowmode request and the first page of
// history at one would be two requests the server can only refuse.
func (a *App) selectChannel(channelID string) {
	if a.currentChannelID == channelID {
		return
	}

	// Whatever was half-composed here stays in the entry, but nobody in the channel
	// being left should go on being told it is still being written in.
	a.stopTyping(a.currentChannelID)

	unread := a.unreadChannels[channelID]
	channel, known := a.store.Channel(channelID)
	permissions := a.store.Permissions(channelID)
	viewable := known && a.canViewChannel(channel)
	a.currentChannelID = channelID

	a.setChannelGlyph()
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
	a.refreshSlowmode() // what is already known, before any request below lands
	a.refreshTyping()   // whoever was typing here while the channel was in the background

	if !viewable {
		a.showStatus("You don't have access to this channel")
		return
	}

	a.loadSlowmode(channelID)
	a.focusInput() // so the user can type straight away

	// Seeing a channel and being allowed to read what was said in it are separate
	// permissions: an announcement channel a bot posts into can be one without the
	// other, and asking for the page anyway would only be refused.
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
	a.setChannelGlyph()
	a.syncChannelList()
	a.refreshSlowmode()
	a.refreshTyping()
	a.syncComposer()
}

// syncServerSelection updates the highlighted server icon. The home button is
// part of the same one-of-N selection, so it is cleared or lit here too rather
// than by whichever handler happens to run.
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

// syncChannelList refreshes the selection, unread and typing state of every
// channel row.
func (a *App) syncChannelList() {
	animate := config.Current().Behaviour.TypingAnimation

	for _, obj := range a.channelList.Objects {
		if w, ok := obj.(*ui.ChannelWidget); ok {
			a.applyChannelState(w, animate)
		}
	}
}

// refreshChannelRow updates a single channel row, repainting only that widget.
// Used on the per-message hot path, so an incoming message in a background
// channel doesn't refresh the entire sidebar.
func (a *App) refreshChannelRow(channelID string) {
	for _, obj := range a.channelList.Objects {
		if w, ok := obj.(*ui.ChannelWidget); ok && w.Channel.ID == channelID {
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

// selectHome opens the home view. The cached DM list paints immediately and a
// refresh is fired regardless: the list is a fetched snapshot with no gateway
// event behind it, so re-opening home is the natural moment to re-ask for it.
// Re-clicking home is a no-op — it would otherwise yank the view back to the
// first conversation.
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

// loadDirectMessages refreshes the cached DM/group list from the API. It is
// stale-while-revalidate: whatever is already cached stays on screen until the
// response lands, so re-opening home never blanks the sidebar. Recipients
// missing from State are resolved in the same pass, because a DM has no name of
// its own — the row is titled after the other participant.
func (a *App) loadDirectMessages() {
	if a.loadingDMs || !a.client.Connected() {
		return
	}

	a.loadingDMs = true
	epoch := a.epoch

	go func() {
		// Every hop back to the UI thread re-checks that this is still the same
		// session: a logout and re-login can land mid-request, and the previous
		// account's conversations must not be painted into the new one's sidebar.
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

// sortConversations drops closed DMs, orders the rest by most recent activity,
// and returns only their IDs: DirectMessages() feeds the channels themselves into
// State, so keeping a second copy would be a cache of a cache. LastMessageID is
// compared directly, ULIDs sorting chronologically as strings. The order is a
// snapshot — a new message marks its row unread but doesn't re-sort the sidebar
// under the user mid-read; the next refresh picks the new order up.
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
