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
func (a *App) refreshChannelList() {
	a.channelList.Objects = nil

	if a.homeSelected {
		for _, channelID := range a.dmChannels {
			if w := a.newChannelRow(channelID); w != nil {
				a.channelList.Add(w)
			}
		}
		a.channelList.Refresh()
		return
	}

	server, ok := a.currentServer()
	if !ok {
		a.channelList.Refresh()
		return
	}

	categorized := make(map[string]bool)
	for _, category := range server.Categories {
		for _, id := range category.Channels {
			categorized[id] = true
		}
	}

	// Uncategorized channels come first.
	for _, channelID := range server.Channels {
		if categorized[channelID] {
			continue
		}
		if w := a.newChannelRow(channelID); w != nil {
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
}

// newChannelRow builds a channel row reflecting its current state, or nil when
// the store doesn't know the channel.
func (a *App) newChannelRow(channelID string) *ui.ChannelWidget {
	channel, ok := a.store.Channel(channelID)
	if !ok {
		return nil
	}

	w := ui.NewChannelWidget(a.deps(), channel, func() { a.selectChannel(channelID) })
	w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])
	w.Menu = func() []*fyne.MenuItem { return a.channelMenu(channelID) }

	return w
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

	a.homeSelected = false
	a.currentServerID = serverID
	server, ok := a.currentServer()
	if !ok {
		return
	}

	a.syncServerSelection(serverID)
	a.setHeader(a.serverHeader, server.Name)
	a.refreshChannelList()
	a.refreshMemberList()

	if len(server.Channels) > 0 {
		a.selectChannel(server.Channels[0])
		return
	}
	a.clearChannelSelection()
}

// selectChannel switches to a channel, acknowledging unreads and showing its
// messages from cache when available.
func (a *App) selectChannel(channelID string) {
	if a.currentChannelID == channelID {
		return
	}

	unread := a.unreadChannels[channelID]
	a.currentChannelID = channelID

	a.setChannelGlyph()
	if channel, ok := a.currentChannel(); ok {
		a.setHeader(a.channelHeader, channel.Name)
		if unread && channel.LastMessageID != "" {
			delete(a.unreadChannels, channelID)
			a.scheduleAck(channelID, channel.LastMessageID)
		}
	}

	a.syncChannelList()
	a.refreshMentionCandidates()
	a.refreshSlowmode() // what is already known, before the request below lands
	a.loadSlowmode(channelID)
	a.focusInput() // so the user can type straight away

	if cached := a.client.Messages().Get(channelID); len(cached) > 0 {
		a.displayMessages(cached)
		return
	}

	a.showStatus("Loading messages...")
	a.loadChannelMessages(channelID)
}

// clearChannelSelection deselects the current channel and clears the view.
func (a *App) clearChannelSelection() {
	a.currentChannelID = ""
	a.clearMessages()
	a.setHeader(a.channelHeader, "")
	a.setChannelGlyph()
	a.syncChannelList()
	a.refreshSlowmode()
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

// syncChannelList refreshes the selection and unread state of every channel row.
func (a *App) syncChannelList() {
	for _, obj := range a.channelList.Objects {
		if w, ok := obj.(*ui.ChannelWidget); ok {
			w.SetState(w.Channel.ID == a.currentChannelID, a.unreadChannels[w.Channel.ID])
		}
	}
}

// refreshChannelRow updates a single channel row, repainting only that widget.
// Used on the per-message hot path, so an incoming message in a background
// channel doesn't refresh the entire sidebar.
func (a *App) refreshChannelRow(channelID string) {
	for _, obj := range a.channelList.Objects {
		if w, ok := obj.(*ui.ChannelWidget); ok && w.Channel.ID == channelID {
			w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])
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
