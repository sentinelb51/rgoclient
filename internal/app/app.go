package app

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
	"RGOClient/internal/util"

	"fyne.io/fyne/v2/widget"
)

// Default message cache size per channel.
const (
	name                    = "Revoltgo Client"
	defaultMessageCacheSize = 100
)

// ChatApp encapsulates the state and UI components of the application.
type ChatApp struct {
	fyneApp fyne.App
	window  fyne.Window

	// Session is the active API session.
	Session *revoltgo.Session

	// Server/Channel state
	ServerIDs        []string
	CurrentServerID  string
	CurrentChannelID string

	// Message cache for fast channel switching
	Messages *cache.MessageCache

	// Category collapsed state: "serverID:categoryID" → collapsed
	collapsedCategories map[string]bool

	// Unread state: channelID → true if unread
	UnreadChannels map[string]bool

	// Pending token to save after Ready event
	pendingSessionToken string

	// UI containers
	serverListContainer  *fyne.Container
	channelListContainer *fyne.Container
	messageListContainer *fyne.Container
	messageScroll        *container.Scroll
	messageInput         *widgets.MessageInput

	// UI labels
	channelHeaderLabel *widget.Label
	serverHeaderLabel  *widget.Label
}

// NewChatApp creates and initializes a new ChatApp instance.
func NewChatApp(fyneApp fyne.App) *ChatApp {
	window := fyneApp.NewWindow(name)
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	app := &ChatApp{
		fyneApp:              fyneApp,
		window:               window,
		messageListContainer: widgets.NewVerticalNoSpacingContainer(),
		serverListContainer:  container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelListContainer: container.NewVBox(),
		ServerIDs:            make([]string, 0),
		Messages:             cache.NewMessageCache(defaultMessageCacheSize),
		collapsedCategories:  make(map[string]bool),
		UnreadChannels:       make(map[string]bool),
	}

	window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if app.messageInput != nil {
			for _, u := range uris {
				// Most local files have file:// scheme
				if u.Scheme() == "file" {
					app.messageInput.AddAttachment(u.Path())
				}
			}
		}
	})

	return app
}

func (app *ChatApp) GoDo(fn func(), waitForSync bool) {
	fyne.CurrentApp().Driver().DoFromGoroutine(fn, waitForSync)
}

// Window returns the main application window.
func (app *ChatApp) Window() fyne.Window {
	return app.window
}

// CurrentServer returns the current server, or nil if not set.
func (app *ChatApp) CurrentServer() *revoltgo.Server {
	if app.Session == nil || app.CurrentServerID == "" {
		return nil
	}
	return app.Session.State.Server(app.CurrentServerID)
}

// CurrentChannel returns the current channel, or nil if not set.
func (app *ChatApp) CurrentChannel() *revoltgo.Channel {
	// todo: what if we return a dummy channel with fake messages: "You're in a loading screen"
	if app.Session == nil || app.CurrentChannelID == "" {
		return nil
	}
	return app.Session.State.Channel(app.CurrentChannelID)
}

// OnAvatarTapped handles avatar tap events to implement MessageActions.
func (app *ChatApp) OnAvatarTapped(userID string) {
	fmt.Printf("Avatar tapped: %s\n", userID)
	// TODO: open user profile
}

// OnImageTapped handles image tap events to implement MessageActions.
func (app *ChatApp) OnImageTapped(attachment *revoltgo.Attachment) {
	app.showImageViewerAttachment(attachment)
}

// OnReply handles reply action.
func (app *ChatApp) OnReply(message *revoltgo.Message) {
	if app.CurrentChannelID == "" || app.messageInput == nil || message == nil {
		return
	}

	displayName := util.DisplayName(app.Session, message)
	avatarURL := util.DisplayAvatarURL(app.Session, message)
	app.messageInput.AddReply(message, displayName, avatarURL)
	app.window.Canvas().Focus(app.messageInput)
}

// ResolveMessage resolves a message from cache.
func (app *ChatApp) ResolveMessage(channelID, messageID string) *revoltgo.Message {
	// Check cache
	messages := app.Messages.Get(channelID)
	for _, m := range messages {
		if m.ID == messageID {
			return m
		}
	}
	return nil
}

// OnDelete handles delete action.
func (app *ChatApp) OnDelete(messageID string) {
	fmt.Printf("Delete message: %s\n", messageID)
}

// OnEdit handles edit action.
func (app *ChatApp) OnEdit(messageID string) {
	fmt.Printf("Edit message: %s\n", messageID)
}

// Run starts the application main loop.
func (app *ChatApp) Run() {
	app.ShowLoginWindow()
	app.window.ShowAndRun()
}

// SwitchToMainUI transitions from login to the main application UI.
func (app *ChatApp) SwitchToMainUI() {
	app.window.SetContent(app.buildUI())
	app.window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))
	app.window.SetOnClosed(func() {
		cache.GetImageCache().Shutdown()
		if app.Session != nil {
			_ = app.Session.Close()
		}
	})
}

// SetPendingSessionToken sets a token to be saved after the Ready event.
func (app *ChatApp) SetPendingSessionToken(token string) {
	app.pendingSessionToken = token
}

// GetPendingSessionToken returns the pending session token.
func (app *ChatApp) GetPendingSessionToken() string {
	return app.pendingSessionToken
}

// ClearPendingSessionToken clears the pending session token.
func (app *ChatApp) ClearPendingSessionToken() {
	app.pendingSessionToken = ""
}

// SelectServer handles server selection and updates the UI.
func (app *ChatApp) SelectServer(serverID string) {
	app.CurrentServerID = serverID
	server := app.CurrentServer()
	if server == nil {
		return
	}

	app.updateServerSelectionUI(serverID)
	app.updateServerHeader(server.Name)

	if len(server.Channels) > 0 {
		app.SelectChannel(server.Channels[0])
	} else {
		app.clearChannelSelection()
	}

	app.RefreshChannelList()
}

// SelectChannel handles channel selection and updates the UI.
func (app *ChatApp) SelectChannel(channelID string) {

	if app.CurrentChannelID == channelID {
		return
	}

	_, unread := app.UnreadChannels[channelID]

	app.CurrentChannelID = channelID
	if ch := app.CurrentChannel(); ch != nil {
		app.updateChannelHeader(ch.Name)

		// Acknowledge last message to clear unreads
		if unread && ch.LastMessageID != nil {
			delete(app.UnreadChannels, channelID)
			go func() {
				_ = app.Session.MessageAck(channelID, *ch.LastMessageID)
			}()
		}
	}

	// Update list visual state (selection + unread)
	app.syncChannelListUI()

	// Display cached messages immediately if available
	if cached := app.Messages.Get(channelID); len(cached) > 0 {
		app.displayMessages(cached)
		return
	}

	app.showLoadingMessages()
	app.loadChannelMessages(channelID)
}

// clearChannelSelection clears the current channel and updates the UI.
func (app *ChatApp) clearChannelSelection() {
	app.CurrentChannelID = ""
	app.refreshMessageList()
	app.updateChannelHeader("")
	app.syncChannelListUI()
}
