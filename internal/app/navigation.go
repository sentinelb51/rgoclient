package app

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// buildUI assembles the three-column layout: servers | channels | messages.
// FillLastRowLayout keeps the sections flush (no seams) for the flat metro look;
// the message area fills whatever width the two fixed sidebars leave.
func (a *App) buildUI() fyne.CanvasObject {
	return container.New(&ui.FillLastRowLayout{},
		a.buildServerList(),
		a.buildChannelList(),
		a.buildMessageArea(),
	)
}

// buildServerList builds the server icon sidebar.
func (a *App) buildServerList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ServerListBackground)
	background.SetMinSize(fyne.NewSize(theme.Sizes.ServerSidebarWidth, 0))

	a.refreshServerList()
	return container.NewStack(background, container.NewVScroll(a.serverList))
}

// refreshServerList rebuilds the server icons from the current server list.
func (a *App) refreshServerList() {
	a.serverList.Objects = nil
	for _, serverID := range a.serverIDs {
		server := a.session.State.Server(serverID)
		if server == nil {
			continue
		}

		id := serverID
		w := ui.NewServerWidget(a.images, server, func() { a.selectServer(id) })
		w.SetSelected(serverID == a.currentServerID)
		a.serverList.Add(container.NewCenter(w))
	}
	a.serverList.Refresh()
}

// buildChannelList builds the channel sidebar with its server-name header.
func (a *App) buildChannelList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	background.SetMinSize(fyne.NewSize(theme.Sizes.ChannelSidebarWidth, 0))

	name := "Server"
	if s := a.currentServer(); s != nil {
		name = s.Name
	}
	a.serverHeader = widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	a.refreshChannelList()
	pad := theme.Sizes.ChannelSidebarPadding
	scroll := container.NewBorder(nil, nil, ui.HorizontalSpacer(pad), ui.HorizontalSpacer(pad), container.NewVScroll(a.channelList))

	content := container.NewBorder(container.NewPadded(a.serverHeader), nil, nil, nil, scroll)
	return container.NewStack(background, content)
}

// refreshChannelList rebuilds the channel rows for the current server, grouping
// channels under their categories.
func (a *App) refreshChannelList() {
	a.channelList.Objects = nil

	server := a.currentServer()
	if server == nil {
		a.channelList.Refresh()
		return
	}

	categorized := make(map[string]bool)
	for _, cat := range server.Categories {
		for _, id := range cat.Channels {
			categorized[id] = true
		}
	}

	// Uncategorized channels come first.
	for _, channelID := range server.Channels {
		if categorized[channelID] {
			continue
		}
		if w := a.createChannelWidget(channelID); w != nil {
			a.channelList.Add(w)
		}
	}

	for i, cat := range server.Categories {
		key := server.ID + ":" + cat.ID
		category := ui.NewCategoryWidget(cat.Title, func(collapsed bool) {
			a.collapsedCategories[key] = collapsed
		})
		category.SetFirst(i == 0)

		var rows []fyne.CanvasObject
		for _, channelID := range cat.Channels {
			if w := a.createChannelWidget(channelID); w != nil {
				rows = append(rows, w)
			}
		}

		a.channelList.Add(category)
		for _, row := range rows {
			a.channelList.Add(row)
		}
		category.SetChannels(rows, a.channelList)
		if a.collapsedCategories[key] {
			category.SetCollapsed(true)
		}
	}

	a.channelList.Refresh()
}

// createChannelWidget builds a channel row reflecting its current state.
func (a *App) createChannelWidget(channelID string) *ui.ChannelWidget {
	channel := a.session.State.Channel(channelID)
	if channel == nil {
		return nil
	}
	id := channelID
	w := ui.NewChannelWidget(channel, func() { a.selectChannel(id) })
	w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])
	return w
}

// selectServer switches to a server and selects its first channel.
func (a *App) selectServer(serverID string) {
	a.currentServerID = serverID
	server := a.currentServer()
	if server == nil {
		return
	}

	a.syncServerSelection(serverID)
	a.setHeader(a.serverHeader, server.Name)
	a.refreshChannelList()

	if len(server.Channels) > 0 {
		a.selectChannel(server.Channels[0])
	} else {
		a.clearChannelSelection()
	}
}

// selectChannel switches to a channel, acknowledging unreads and showing its
// messages (from cache when available).
func (a *App) selectChannel(channelID string) {
	if a.currentChannelID == channelID {
		return
	}

	unread := a.unreadChannels[channelID]
	a.currentChannelID = channelID

	if channel := a.currentChannel(); channel != nil {
		a.setHeader(a.channelHeader, channel.Name)
		if unread && channel.LastMessageID != nil {
			delete(a.unreadChannels, channelID)
			lastID := *channel.LastMessageID
			go func() { _ = a.session.MessageAck(channelID, lastID) }()
		}
	}

	a.syncChannelList()

	if cached := a.messageCache.Get(channelID); len(cached) > 0 {
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
	a.syncChannelList()
}

// syncServerSelection updates the highlighted server icon.
func (a *App) syncServerSelection(selectedID string) {
	for _, obj := range a.serverList.Objects {
		center, ok := obj.(*fyne.Container)
		if !ok || len(center.Objects) == 0 {
			continue
		}
		if w, ok := center.Objects[0].(*ui.ServerWidget); ok {
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

// refreshChannelRow updates the state of a single channel row, repainting only
// that widget. Used on the per-message hot path so an incoming message in a
// background channel doesn't refresh the entire sidebar.
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

// channelName returns the current channel's name, or a fallback.
func (a *App) channelName() string {
	if ch := a.currentChannel(); ch != nil {
		return ch.Name
	}
	return "channel"
}
