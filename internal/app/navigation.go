package app

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// buildUI assembles the four-column layout: servers | channels | messages |
// members. The fill row keeps the sections flush for the flat look; only the
// message area (index 2) stretches, the rest stay at their fixed widths.
func (a *App) buildUI() fyne.CanvasObject {
	return ui.NewFillRow(2,
		a.buildServerList(),
		a.buildChannelList(),
		a.buildMessageArea(),
		a.buildMemberList(),
	)
}

/* Server sidebar */

// buildServerList builds the server icon sidebar: fixed home and settings buttons
// bookend the scrolling icons, each set off by a short separator. They sit
// outside the scroll, so they stay put when the list grows tall enough to scroll.
func (a *App) buildServerList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ServerListBackground)
	background.SetMinSize(fyne.NewSize(theme.Sizes.ServerSidebarWidth, 0))

	home := ui.NewSidebarButton(fynetheme.HomeIcon(), a.selectHome)
	settings := ui.NewSidebarButton(fynetheme.SettingsIcon(), a.openSettings)

	top := container.NewVBox(
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		container.NewCenter(home),
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

	return container.NewStack(background, content)
}

// refreshServerList rebuilds the server icons from the current server list.
func (a *App) refreshServerList() {
	a.serverList.Objects = nil

	for _, serverID := range a.serverIDs {
		server := a.session.State.Server(serverID)
		if server == nil {
			continue
		}

		w := ui.NewServerWidget(a.images, server, func() { a.selectServer(serverID) })
		w.SetSelected(serverID == a.currentServerID)

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

// selectHome is the home button handler.
func (a *App) selectHome() {
	// todo: home / direct-message view
	log.Print("home selected")
}

// openSettings opens the settings window. A second open focuses the existing
// window rather than spawning another.
func (a *App) openSettings() {
	if a.settingsWindow != nil {
		a.settingsWindow.RequestFocus()
		return
	}

	window := a.fyne.NewWindow("Settings")
	a.settingsWindow = window
	window.SetOnClosed(func() { a.settingsWindow = nil })

	// todo: actual settings
	heading := widget.NewLabelWithStyle("Settings", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	note := widget.NewLabelWithStyle("Work in progress.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	window.SetContent(container.NewCenter(container.NewVBox(heading, note)))

	window.Resize(fyne.NewSize(420, 320))
	window.CenterOnScreen()
	a.styleNativeChrome(window)
	window.Show()
}

/* Channel sidebar */

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
	scroll := container.NewBorder(nil, nil, ui.HorizontalSpacer(pad), ui.HorizontalSpacer(pad),
		container.NewVScroll(a.channelList))
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
		if w := a.newChannelRow(channelID); w != nil {
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
			if w := a.newChannelRow(channelID); w != nil {
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

// newChannelRow builds a channel row reflecting its current state, or nil when
// State doesn't know the channel.
func (a *App) newChannelRow(channelID string) *ui.ChannelWidget {
	channel := a.session.State.Channel(channelID)
	if channel == nil {
		return nil
	}

	w := ui.NewChannelWidget(channel, func() { a.selectChannel(channelID) })
	w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])

	return w
}

/* Selection */

// selectServer switches to a server and selects its first channel. Re-clicking
// the current server is a no-op, which would otherwise rebuild both sidebars and
// yank the view to the first channel.
func (a *App) selectServer(serverID string) {
	if a.currentServerID == serverID {
		return
	}

	a.currentServerID = serverID
	server := a.currentServer()
	if server == nil {
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

	if channel := a.currentChannel(); channel != nil {
		a.setHeader(a.channelHeader, channel.Name)
		if unread && channel.LastMessageID != nil {
			delete(a.unreadChannels, channelID)
			a.scheduleAck(channelID, *channel.LastMessageID)
		}
	}

	a.syncChannelList()
	a.focusInput() // so the user can type straight away

	if cached := a.messages.Get(channelID); len(cached) > 0 {
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

// channelName returns the current channel's name, or a fallback.
func (a *App) channelName() string {
	if channel := a.currentChannel(); channel != nil {
		return channel.Name
	}

	return "channel"
}
