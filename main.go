package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ChatApp encapsulates the state and UI components of the application.
type ChatApp struct {
	app    fyne.App
	window fyne.Window

	// Data
	Servers        []*Server
	CurrentServer  *Server
	CurrentChannel *Channel

	// UI Components
	serverListContainer  *fyne.Container
	channelListContainer *fyne.Container
	messageListContainer *fyne.Container
	messageScroll        *container.Scroll

	channelHeaderLabel *widget.Label
	serverHeaderLabel  *widget.Label
}

func NewChatApp() *ChatApp {
	a := app.New()
	a.Settings().SetTheme(&noScrollTheme{Theme: theme.DefaultTheme()})

	w := a.NewWindow("Revoltgo Client")
	w.Resize(fyne.NewSize(1000, 600))

	c := &ChatApp{
		app:                  a,
		window:               w,
		messageListContainer: container.NewVBox(),
		serverListContainer:  container.NewGridWrap(fyne.NewSize(60, 50)),
		channelListContainer: container.NewVBox(),
	}

	c.initDummyData()

	// Default selection
	if len(c.Servers) > 0 {
		c.CurrentServer = c.Servers[0]
		if len(c.CurrentServer.Channels) > 0 {
			c.CurrentChannel = c.CurrentServer.Channels[0]
		}
	}

	return c
}

func (c *ChatApp) initDummyData() {
	// helpers to create dummy data
	c.Servers = []*Server{
		{ID: "1", Name: "Revolt", IconURL: "", Channels: []*Channel{
			{ID: "c1", Name: "general", ServerID: "1", Messages: []*Message{
				{ID: "m1", Author: "User1", Content: "Hello world", AvatarURL: ""},
			}},
			{ID: "c2", Name: "random", ServerID: "1", Messages: []*Message{}},
		}},
		{ID: "2", Name: "Golang", IconURL: "", Channels: []*Channel{
			{ID: "c3", Name: "dev", ServerID: "2", Messages: []*Message{
				{ID: "m2", Author: "Gopher", Content: "Go is great", AvatarURL: ""},
			}},
		}},
		{ID: "3", Name: "Fyne", IconURL: "", Channels: []*Channel{}},
		{ID: "4", Name: "Test", IconURL: "", Channels: []*Channel{}},
	}
}

func (c *ChatApp) Run() {
	c.window.SetContent(c.buildUI())
	c.window.ShowAndRun()
}

func (c *ChatApp) buildUI() fyne.CanvasObject {
	// Build server list (far left sidebar)
	serverList := c.buildServerList()

	// Build channel list (left sidebar)
	channelList := c.buildChannelList()

	// Build message box (right content)
	messageBox := c.buildMessageBox()

	// content comprises the channel list and message box
	content := container.NewBorder(nil, nil, channelList, nil, messageBox)

	// Use Border layout instead of Split to keep sidebar fixed
	return container.NewBorder(nil, nil, serverList, nil, content)
}

func (c *ChatApp) buildServerList() fyne.CanvasObject {
	bgColor := color.RGBA{R: 20, G: 20, B: 20, A: 255}
	bg := canvas.NewRectangle(bgColor)
	bg.SetMinSize(fyne.NewSize(60, 0))

	c.refreshServerList()

	scroll := container.NewVScroll(c.serverListContainer)

	return container.NewStack(bg, scroll)
}

func (c *ChatApp) refreshServerList() {
	c.serverListContainer.Objects = nil
	for _, s := range c.Servers {
		srv := s // capture loop var
		w := container.NewCenter(NewServerWidget(srv, func() {
			c.selectServer(srv)
		}))
		c.serverListContainer.Add(w)
	}
	c.serverListContainer.Refresh()
}

func (c *ChatApp) selectServer(s *Server) {
	c.CurrentServer = s
	if len(s.Channels) > 0 {
		c.selectChannel(s.Channels[0])
	} else {
		c.CurrentChannel = nil
		c.refreshChannelList()
		c.refreshMessageList()
		if c.channelHeaderLabel != nil {
			c.channelHeaderLabel.SetText("#")
		}
	}

	// Update headers if any
	if c.serverHeaderLabel != nil {
		c.serverHeaderLabel.SetText(s.Name)
	}

	c.refreshChannelList()
}

func (c *ChatApp) buildChannelList() fyne.CanvasObject {
	bgColor := color.RGBA{R: 44, G: 44, B: 44, A: 255}
	bg := canvas.NewRectangle(bgColor)
	bg.SetMinSize(fyne.NewSize(180, 0))

	// Server title header
	serverName := "Server"
	if c.CurrentServer != nil {
		serverName = c.CurrentServer.Name
	}
	c.serverHeaderLabel = widget.NewLabelWithStyle(serverName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(c.serverHeaderLabel)

	// Channel items
	// c.channelListContainer initialized in NewChatApp
	c.refreshChannelList()

	listContent := container.NewPadded(c.channelListContainer)

	// Layout with header at top
	content := container.NewBorder(header, nil, nil, nil, listContent)
	return container.NewStack(bg, content)
}

func (c *ChatApp) refreshChannelList() {
	c.channelListContainer.Objects = nil
	if c.CurrentServer != nil {
		for _, ch := range c.CurrentServer.Channels {
			channel := ch
			w := NewChannelWidget(channel, func() {
				c.selectChannel(channel)
			})
			if c.CurrentChannel != nil && channel.ID == c.CurrentChannel.ID {
				w.SetSelected(true)
			}
			c.channelListContainer.Add(w)
		}
	}
	c.channelListContainer.Refresh()
}

func (c *ChatApp) selectChannel(ch *Channel) {
	c.CurrentChannel = ch
	if c.channelHeaderLabel != nil {
		c.channelHeaderLabel.SetText("#" + ch.Name)
	}

	// Update highlight
	for _, obj := range c.channelListContainer.Objects {
		if cw, ok := obj.(*ChannelWidget); ok {
			cw.SetSelected(cw.channel.ID == ch.ID)
		}
	}

	c.refreshMessageList()
}

func (c *ChatApp) buildMessageBox() fyne.CanvasObject {
	bgColor := color.RGBA{R: 28, G: 28, B: 28, A: 255}
	bg := canvas.NewRectangle(bgColor)

	// Message list container
	// c.messageListContainer initialized in NewChatApp
	c.messageScroll = container.NewVScroll(container.NewPadded(c.messageListContainer))
	c.refreshMessageList()

	// Input field
	input := widget.NewEntry()
	input.SetPlaceHolder("Send a message...")
	input.OnSubmitted = func(text string) {
		if text == "" {
			return
		}
		if c.CurrentChannel == nil {
			return
		}

		c.addMessage("User", text)
		input.SetText("")
	}
	inputContainer := container.NewPadded(input)

	// Channel title header
	channelName := "#channel"
	if c.CurrentChannel != nil {
		channelName = "#" + c.CurrentChannel.Name
	}
	c.channelHeaderLabel = widget.NewLabelWithStyle(channelName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(c.channelHeaderLabel)

	layout := container.NewBorder(header, inputContainer, nil, nil, c.messageScroll)
	return container.NewStack(bg, layout)
}

func (c *ChatApp) refreshMessageList() {
	c.messageListContainer.Objects = nil
	if c.CurrentChannel != nil {
		for _, m := range c.CurrentChannel.Messages {
			w := NewMessageWidget(m.Author, m.Content, m.AvatarURL)
			c.messageListContainer.Add(w)
		}
	}
	c.messageListContainer.Refresh()
	if c.messageScroll != nil {
		c.messageScroll.ScrollToBottom()
	}
}

func (c *ChatApp) addMessage(username, text string) {
	if c.CurrentChannel == nil {
		return
	}

	// Add to model
	msg := &Message{
		ID:        "new",
		Author:    username,
		Content:   text,
		AvatarURL: "",
	}
	c.CurrentChannel.Messages = append(c.CurrentChannel.Messages, msg)

	msgWidget := NewMessageWidget(username, text, "")
	c.messageListContainer.Add(msgWidget)
	c.messageScroll.ScrollToBottom()
}

func main() {
	chatApp := NewChatApp()
	chatApp.Run()
}
