package app

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/api"
	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"

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
	Session *api.Session

	// Server/Channel state
	ServerIDs        []string
	CurrentServerID  string
	CurrentChannelID string

	// Message cache for fast channel switching
	Messages *cache.MessageCache

	// Category collapsed state: "serverID:categoryID" → collapsed
	collapsedCategories map[string]bool

	// Pending token to save after Ready event
	pendingSessionToken string

	// UI containers
	serverListContainer  *fyne.Container
	channelListContainer *fyne.Container
	messageListContainer *fyne.Container
	messageScroll        *container.Scroll

	// UI labels
	channelHeaderLabel *widget.Label
	serverHeaderLabel  *widget.Label
}

// NewChatApp creates and initializes a new ChatApp instance.
func NewChatApp(fyneApp fyne.App) *ChatApp {
	window := fyneApp.NewWindow(name)
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	return &ChatApp{
		fyneApp:              fyneApp,
		window:               window,
		messageListContainer: container.NewVBox(),
		serverListContainer:  container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelListContainer: container.NewVBox(),
		ServerIDs:            make([]string, 0),
		Messages:             cache.NewMessageCache(defaultMessageCacheSize),
		collapsedCategories:  make(map[string]bool),
	}
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
	return app.Session.Server(app.CurrentServerID)
}

// CurrentChannel returns the current channel, or nil if not set.
func (app *ChatApp) CurrentChannel() *revoltgo.Channel {
	// todo: what if we return a dummy channel with fake messages: "You're in a loading screen
	if app.Session == nil || app.CurrentChannelID == "" {
		return nil
	}
	return app.Session.Channel(app.CurrentChannelID)
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
