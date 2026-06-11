// Package app wires the Revolt session, caches, and UI into a running client.
package app

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

const (
	windowTitle = "Revoltgo Client"
	iconPath    = "assets/rgo.png"

	messagesPerChannel = 500 // cap of cached messages per channel
	cachedChannels     = 5   // number of channels kept in the message cache
)

// App holds the session, caches, and all mutable UI state for the client. It
// also implements ui.MessageActions so widgets can call back into it.
type App struct {
	fyne   fyne.App
	window fyne.Window

	session      *revoltgo.Session
	images       *cache.ImageCache
	messageCache *cache.MessageCache

	serverIDs        []string
	currentServerID  string
	currentChannelID string

	collapsedCategories map[string]bool // "serverID:categoryID" -> collapsed
	unreadChannels      map[string]bool
	fetchedAuthors      map[string]bool // "serverID:userID" -> author resolved once (lazy, UI-thread only)

	pendingToken string // saved once the Ready event arrives

	serverList    *fyne.Container
	channelList   *fyne.Container
	memberList    *fyne.Container
	messageList   *fyne.Container
	messageScroll *ui.ObservableScroll
	input         *ui.MessageInput
	serverHeader  *widget.Label
	channelHeader *widget.Label

	settingsWindow fyne.Window // WIP settings window; nil when closed

	loadingHistory bool

	// renderGen identifies the current message-area render; bumping it aborts the
	// batched widget mounting of any superseded displayMessages run (channel IDs
	// alone can't tell A→B→A switches apart). UI-thread only.
	renderGen int
}

var _ ui.MessageActions = (*App)(nil)

// New creates the application and its main window.
func New(fyneApp fyne.App) *App {
	window := fyneApp.NewWindow(windowTitle)
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	a := &App{
		fyne:                fyneApp,
		window:              window,
		images:              cache.NewImageCache(),
		messageCache:        cache.NewMessageCache(messagesPerChannel, cachedChannels),
		serverList:          container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelList:         container.NewVBox(),
		memberList:          ui.VBoxNoSpacing(),
		messageList:         ui.VBoxNoSpacing(),
		collapsedCategories: make(map[string]bool),
		unreadChannels:      make(map[string]bool),
		fetchedAuthors:      make(map[string]bool),
	}
	a.setIcon()
	return a
}

// Run shows the login window and starts the Fyne event loop.
func (a *App) Run() {
	a.showLogin()
	a.styleNativeChrome()
	a.window.ShowAndRun()
}

// styleNativeChrome recolors the native window chrome (the Windows title bar) to
// match the palette. The platform window handle isn't available until the event
// loop has created it, so it retries briefly until the styling lands.
func (a *App) styleNativeChrome() {
	go func() {
		for range 40 {
			var done bool
			a.doOnUI(func() { done = ui.StyleTitlebar(a.window) }, true)
			if done {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
}

// deps returns the dependency bundle handed to widgets.
func (a *App) deps() ui.Deps {
	return ui.Deps{Session: a.session, Images: a.images, Actions: a}
}

// doOnUI runs fn on the UI thread. When wait is true it blocks until fn returns.
func (a *App) doOnUI(fn func(), wait bool) {
	fyne.CurrentApp().Driver().DoFromGoroutine(fn, wait)
}

func (a *App) setIcon() {
	data, err := os.ReadFile(filepath.Join(iconPath))
	if err != nil {
		log.Printf("read icon: %v", err)
		return
	}
	a.window.SetIcon(fyne.NewStaticResource(filepath.Base(iconPath), data))
}

// showMainUI swaps the window to the main layout and wires up shutdown.
func (a *App) showMainUI() {
	a.window.SetPadded(false) // sections sit flush against the window chrome
	a.window.SetContent(a.buildUI())
	a.window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))
	a.window.SetOnClosed(func() {
		a.images.Shutdown()
		if a.session != nil {
			_ = a.session.Close()
		}
	})
}

// stateChannel returns a channel from State, or nil when logged out or unknown.
// It centralises the session nil-check for the channel helpers below.
func (a *App) stateChannel(channelID string) *revoltgo.Channel {
	if a.session == nil || channelID == "" {
		return nil
	}
	return a.session.State.Channel(channelID)
}

// currentServer returns the selected server, or nil.
func (a *App) currentServer() *revoltgo.Server {
	if a.session == nil || a.currentServerID == "" {
		return nil
	}
	return a.session.State.Server(a.currentServerID)
}

// currentChannel returns the selected channel, or nil.
func (a *App) currentChannel() *revoltgo.Channel {
	return a.stateChannel(a.currentChannelID)
}

// channelServerID returns the server a channel belongs to, or "" for DMs/groups.
func (a *App) channelServerID(channelID string) string {
	if channel := a.stateChannel(channelID); channel != nil && channel.Server != nil {
		return *channel.Server
	}
	return ""
}

// ResolveMessage looks a message up in the local cache.
func (a *App) ResolveMessage(channelID, messageID string) *revoltgo.Message {
	return a.messageCache.Find(channelID, messageID)
}

// OnReply focuses the composer with the given message queued as a reply.
func (a *App) OnReply(message *revoltgo.Message) {
	if a.currentChannelID == "" || a.input == nil || message == nil {
		return
	}
	a.input.AddReply(message)
	a.window.Canvas().Focus(a.input)
}

// OnImageTapped opens an attachment in the image viewer.
func (a *App) OnImageTapped(attachment *revoltgo.Attachment) {
	a.showImageViewer(attachment)
}

// OnAvatarTapped is a placeholder for opening a user profile.
func (a *App) OnAvatarTapped(userID string) {
	log.Printf("avatar tapped: %s", userID)
}

func (a *App) OnDelete(message *revoltgo.Message) {
	// todo: permission checks to see if you can delete messages
	// you can always delete your own messages

	session := a.session
	if session == nil {
		return
	}
	go func() {
		if err := session.ChannelMessageDelete(message.Channel, message.ID); err != nil {
			log.Printf("delete message %s: %v", message.ID, err)
		}
	}()
}

// OnEdit is a placeholder for message editing.
func (a *App) OnEdit(message *revoltgo.Message) {
	log.Printf("edit message: %s", message.ID)
}
