package app

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
)

// buildUI constructs the main application layout.
func (app *ChatApp) buildUI() fyne.CanvasObject {
	serverList := app.buildServerList()
	channelList := app.buildChannelList()
	messageBox := app.buildMessageBox()

	content := container.NewBorder(nil, nil, channelList, nil, messageBox)
	return container.NewBorder(nil, nil, serverList, nil, content)
}

// buildServerList creates the server sidebar component.
func (app *ChatApp) buildServerList() fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Colors.ServerListBackground)
	bg.SetMinSize(fyne.NewSize(theme.Sizes.ServerSidebarWidth, 0))

	app.RefreshServerList()
	scroll := container.NewVScroll(app.serverListContainer)

	return container.NewStack(bg, scroll)
}

// RefreshServerList rebuilds the server list UI from current data.
func (app *ChatApp) RefreshServerList() {
	app.serverListContainer.Objects = nil

	for _, serverID := range app.ServerIDs {
		server := app.Session.State.Server(serverID)
		if server == nil {
			continue
		}

		capturedID := serverID
		w := widgets.NewServerWidget(server, func() {
			app.SelectServer(capturedID)
		})

		if serverID == app.CurrentServerID {
			w.SetSelected(true)
		}
		app.serverListContainer.Add(container.NewCenter(w))
	}

	app.serverListContainer.Refresh()
}

// buildChannelList creates the channel sidebar component.
func (app *ChatApp) buildChannelList() fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	bg.SetMinSize(fyne.NewSize(theme.Sizes.ChannelSidebarWidth, 0))

	serverName := "Server"
	if s := app.CurrentServer(); s != nil {
		serverName = s.Name
	}

	app.serverHeaderLabel = widget.NewLabelWithStyle(serverName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(app.serverHeaderLabel)

	app.RefreshChannelList()
	scroll := container.NewVScroll(app.channelListContainer)

	padding := theme.Sizes.ChannelSidebarPadding
	paddedScroll := container.NewBorder(
		nil, nil,
		newSpacer(padding, 0), newSpacer(padding, 0),
		scroll,
	)

	content := container.NewBorder(header, nil, nil, nil, paddedScroll)
	return container.NewStack(bg, content)
}

// RefreshChannelList rebuilds the channel list UI from current server data.
func (app *ChatApp) RefreshChannelList() {
	app.channelListContainer.Objects = nil

	server := app.CurrentServer()
	if server == nil {
		app.channelListContainer.Refresh()
		return
	}

	// Build set of categorized channel IDs
	categorized := make(map[string]bool)
	for _, cat := range server.Categories {
		for _, id := range cat.Channels {
			categorized[id] = true
		}
	}

	// Add uncategorized channels first
	for _, channelID := range server.Channels {
		if categorized[channelID] {
			continue
		}
		app.addChannelWidget(channelID)
	}

	// Add categories with their channels
	for i, cat := range server.Categories {
		key := server.ID + ":" + cat.ID
		collapsed := app.collapsedCategories[key]
		capturedKey := key

		catWidget := widgets.NewCategoryWidget(cat.Title, func(isCollapsed bool) {
			app.collapsedCategories[capturedKey] = isCollapsed
		})

		if i == 0 {
			catWidget.SetIsFirstCategory(true)
		}

		var channelWidgets []fyne.CanvasObject
		for _, channelID := range cat.Channels {
			w := app.createChannelWidget(channelID)
			if w != nil {
				channelWidgets = append(channelWidgets, w)
			}
		}

		app.channelListContainer.Add(catWidget)
		for _, w := range channelWidgets {
			app.channelListContainer.Add(w)
		}

		catWidget.SetChannelWidgets(channelWidgets, app.channelListContainer)
		if collapsed {
			catWidget.SetCollapsed(true)
		}
	}

	app.channelListContainer.Refresh()
}

// addChannelWidget adds a channel widget to the channel list.
func (app *ChatApp) addChannelWidget(channelID string) {
	w := app.createChannelWidget(channelID)
	if w != nil {
		app.channelListContainer.Add(w)
	}
}

// createChannelWidget creates a channel widget for the given ID.
func (app *ChatApp) createChannelWidget(channelID string) *widgets.ChannelWidget {
	channel := app.Session.State.Channel(channelID)
	if channel == nil {
		return nil
	}

	capturedID := channelID
	w := widgets.NewChannelWidget(channel, func() {
		app.SelectChannel(capturedID)
	})

	w.SetState(capturedID == app.CurrentChannelID, app.UnreadChannels[capturedID])

	return w
}

// buildMessageBox creates the main message area component.
func (app *ChatApp) buildMessageBox() fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	app.messageScroll = container.NewVScroll(container.NewPadded(app.messageListContainer))
	app.refreshMessageList()

	input := widgets.NewMessageInput()
	app.messageInput = input
	input.SetPlaceHolder("Send a message...")
	input.OnSubmit = func(text string) {
		app.handleMessageSubmit(text, input)
	}
	inputContainer := container.NewPadded(container.NewVBox(input.AttachmentContainer, input))

	channelName := "channel"
	if ch := app.CurrentChannel(); ch != nil {
		channelName = ch.Name
	}

	app.channelHeaderLabel = widget.NewLabelWithStyle(channelName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	icon := widgets.GetHashtagIcon()
	headerContent := container.NewHBox(icon, app.channelHeaderLabel)
	header := container.NewPadded(headerContent)

	layout := container.NewBorder(header, inputContainer, nil, nil, app.messageScroll)
	return container.NewStack(bg, layout)
}

// newSpacer creates a transparent rectangle with the given minimum size.
func newSpacer(width, height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, height))
	return spacer
}

// updateServerSelectionUI updates the visual selection state of server widgets.
func (app *ChatApp) updateServerSelectionUI(selectedID string) {
	for _, obj := range app.serverListContainer.Objects {
		if center, ok := obj.(*fyne.Container); ok && len(center.Objects) > 0 {
			if w, ok := center.Objects[0].(*widgets.ServerWidget); ok {
				w.SetSelected(w.Server.ID == selectedID)
			}
		}
	}
}

// syncChannelListUI updates visual state of all channel widgets.
func (app *ChatApp) syncChannelListUI() {
	updateWidget := func(obj fyne.CanvasObject) {
		if w, ok := obj.(*widgets.ChannelWidget); ok {
			id := w.Channel.ID
			w.SetState(id == app.CurrentChannelID, app.UnreadChannels[id])
		}
	}

	for _, obj := range app.channelListContainer.Objects {
		updateWidget(obj)
		// Check for CategoryWidgets which might hold channels
		// Note: Current implementation adds channels directly to channelListContainer
		// but if we nest them in future, we'd need recursion here.
		// CategoryWidget logic uses SetChannelWidgets which just hides/shows them in parent container.
	}
}

// updateServerHeader updates the server header label text.
func (app *ChatApp) updateServerHeader(name string) {
	if app.serverHeaderLabel != nil {
		app.serverHeaderLabel.SetText(name)
	}
}

// updateChannelHeader updates the channel header label text.
func (app *ChatApp) updateChannelHeader(name string) {
	if app.channelHeaderLabel != nil {
		app.channelHeaderLabel.SetText(name)
	}
}
