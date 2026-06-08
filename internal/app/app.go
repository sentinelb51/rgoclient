// Package app wires the Revolt session, caches, and UI into a running client.
package app

import (
	"fmt"
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

	pendingToken string // saved once the Ready event arrives

	serverList    *fyne.Container
	channelList   *fyne.Container
	messageList   *fyne.Container
	messageScroll *ui.ObservableScroll
	input         *ui.MessageInput
	serverHeader  *widget.Label
	channelHeader *widget.Label

	loadingHistory bool
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
		messageList:         ui.VBoxNoSpacing(),
		collapsedCategories: make(map[string]bool),
		unreadChannels:      make(map[string]bool),
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
			done := make(chan bool, 1)
			a.doOnUI(func() { done <- ui.StyleTitlebar(a.window) }, false)
			if <-done {
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

// currentServer returns the selected server, or nil.
func (a *App) currentServer() *revoltgo.Server {
	if a.session == nil || a.currentServerID == "" {
		return nil
	}
	return a.session.State.Server(a.currentServerID)
}

// currentChannel returns the selected channel, or nil.
func (a *App) currentChannel() *revoltgo.Channel {
	if a.session == nil || a.currentChannelID == "" {
		return nil
	}
	return a.session.State.Channel(a.currentChannelID)
}

// ResolveMessage looks a message up in the local cache.
func (a *App) ResolveMessage(channelID, messageID string) *revoltgo.Message {
	for _, m := range a.messageCache.Get(channelID) {
		if m.ID == messageID {
			return m
		}
	}
	return nil
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
	fmt.Printf("avatar tapped: %s\n", userID)
}

func (a *App) OnDelete(message *revoltgo.Message) {
	// todo: permission checks to see if you can delete messages
	// you can always delete your own messages

	err := a.session.ChannelMessageDelete(message.Channel, message.ID)
	if err != nil {
		log.Printf("OnDelete() error: %s\n", err)
	}
}

// OnEdit is a placeholder for message editing.
func (a *App) OnEdit(message *revoltgo.Message) {
	fmt.Printf("edit message: %s\n", message.ID)
}
