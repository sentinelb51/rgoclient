package app

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
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
		server := app.Session.Server(serverID)
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
	channel := app.Session.Channel(channelID)
	if channel == nil {
		return nil
	}

	capturedID := channelID
	w := widgets.NewChannelWidget(channel, func() {
		app.SelectChannel(capturedID)
	})

	if capturedID == app.CurrentChannelID {
		w.SetSelected(true)
	}
	return w
}

// buildMessageBox creates the main message area component.
func (app *ChatApp) buildMessageBox() fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	app.messageScroll = container.NewVScroll(container.NewPadded(app.messageListContainer))
	app.refreshMessageList()

	input := widgets.NewMessageInput()
	input.SetPlaceHolder("Send a message...")
	input.OnSubmit = func(text string) {
		app.handleMessageSubmit(text, input)
	}
	inputContainer := container.NewPadded(input)

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

// updateChannelSelectionUI updates the visual selection state of channel widgets.
func (app *ChatApp) updateChannelSelectionUI(selectedID string) {
	for _, obj := range app.channelListContainer.Objects {
		if w, ok := obj.(*widgets.ChannelWidget); ok {
			w.SetSelected(w.Channel.ID == selectedID)
		}
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

// showImageViewer displays an image attachment in a popup window.
func (app *ChatApp) showImageViewer(att widgets.MessageAttachment) {
	window := app.fyneApp.NewWindow("Image Viewer")

	// Calculate constrained window size using theme sizes
	maxW := theme.Sizes.ImageViewerMaxWidth
	maxH := theme.Sizes.ImageViewerMaxHeight
	w := float32(att.Width)
	h := float32(att.Height)

	if w > maxW {
		h = h * (maxW / w)
		w = maxW
	}
	if h > maxH {
		w = w * (maxH / h)
		h = maxH
	}
	if w < theme.Sizes.ImageViewerMinWidth {
		w = theme.Sizes.ImageViewerMinWidth
	}
	if h < theme.Sizes.ImageViewerMinHeight {
		h = theme.Sizes.ImageViewerMinHeight
	}

	size := fyne.NewSize(w, h)

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	imgContainer := container.NewGridWrap(size, placeholder)

	if att.URL != "" && att.ID != "" {
		cache.GetImageCache().LoadImageToContainer(att.ID, att.URL, size, imgContainer, false, nil)
	}

	content := container.NewCenter(imgContainer)
	window.SetContent(content)
	window.Resize(fyne.NewSize(w+40, h+40))
	window.CenterOnScreen()
	window.Show()
}

// handleMessageSubmit processes a submitted message from the input field.
func (app *ChatApp) handleMessageSubmit(text string, input *widgets.MessageInput) {
	if text == "" || app.CurrentChannelID == "" || app.Session == nil {
		return
	}

	if _, err := app.Session.SendMessage(app.CurrentChannelID, text); err != nil {
		fmt.Printf("Failed to send message: %v\n", err)
		return
	}

	input.SetText("")
}

// refreshMessageList rebuilds the message list UI.
func (app *ChatApp) refreshMessageList() {
	app.messageListContainer.Objects = nil
	app.messageListContainer.Refresh()
	app.scrollToBottom()
}

// scrollToBottom scrolls the message area to the bottom.
func (app *ChatApp) scrollToBottom() {
	if app.messageScroll != nil {
		app.messageScroll.ScrollToBottom()
	}
}

// AddMessage adds a new message to the current channel.
func (app *ChatApp) AddMessage(msg *revoltgo.Message) {
	if app.CurrentChannelID == "" {
		return
	}

	w := widgets.NewMessageWidget(msg, app.Session, nil, func(att widgets.MessageAttachment) {
		app.showImageViewer(att)
	})
	app.messageListContainer.Add(w)
	app.messageListContainer.Refresh()
	app.scrollToBottom()
}
