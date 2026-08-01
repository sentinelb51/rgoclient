// Package app wires the Revolt session, the caches, and the UI into a running
// client. It owns every piece of mutable state; widgets receive what they need
// through ui.Deps.
package app

import (
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

const (
	windowTitle = "Revoltgo Client"

	messagesPerChannel = 500 // cached messages per channel
	cachedChannels     = 5   // channels kept in the message cache
	cachedPreviews     = 100 // text-attachment previews kept in memory
)

// App holds the session, the caches, and all mutable UI state. It also
// implements ui.MessageActions, so widgets can call back into it.
//
// Everything here is UI-thread confined unless a field says otherwise. Worker
// goroutines reach it through doOnUI.
type App struct {
	fyne   fyne.App
	window fyne.Window

	session  *revoltgo.Session
	images   *cache.ImageCache
	messages *cache.MessageCache
	texts    *cache.TextCache

	/* View state */

	serverIDs        []string
	currentServerID  string
	currentChannelID string

	// The home view (directmessages.go). homeSelected is what "home is open"
	// means: home has no server, so an empty currentServerID alone can't tell it
	// apart from nothing being selected. dmChannels holds only the sidebar order —
	// the conversations themselves live in State.
	homeSelected bool
	dmChannels   []string
	loadingDMs   bool

	collapsedCategories map[string]bool // "serverID:categoryID" -> collapsed
	unreadChannels      map[string]bool

	/* Mounted UI */

	serverList    *fyne.Container
	channelList   *fyne.Container
	memberList    *fyne.Container
	messageList   *fyne.Container
	messageScroll *ui.ObservableScroll
	input         *ui.MessageInput
	homeButton    *ui.SidebarButton
	serverHeader  *widget.Label
	channelHeader *widget.Label
	channelGlyph  *fyne.Container // holds the message header's # / @ / group mark

	/* Modal layer and extra windows */

	settingsWindow fyne.Window          // nil when closed
	overlay        *ui.Overlay          // nil when nothing is showing
	joinDialog     *ui.JoinServerDialog // the invite dialog on the modal layer, if any
	editing        *ui.MessageWidget    // the message being edited in place, if any

	/* Lazy author resolution, see members.go */

	fetchedAuthors map[string]bool
	pendingAuthors []author
	authorTimer    *time.Timer

	/* Read-ack coalescing, see events.go */

	ackTimer     *time.Timer
	ackChannelID string
	ackMessageID string

	pendingToken   string // stashed by a credential login until Ready names the user
	pendingJoin    bool   // a join is in flight, so its ServerCreate should select
	loadingHistory bool
}

var _ ui.MessageActions = (*App)(nil)

// New creates the application and its main window.
func New(fyneApp fyne.App) *App {
	window := fyneApp.NewWindow(windowTitle)
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))
	window.SetIcon(assets.AppIcon)

	return &App{
		fyne:                fyneApp,
		window:              window,
		images:              cache.NewImageCache(),
		messages:            cache.NewMessageCache(messagesPerChannel, cachedChannels),
		texts:               cache.NewTextCache(cachedPreviews),
		serverList:          container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelList:         container.NewVBox(),
		memberList:          ui.VBoxNoSpacing(),
		messageList:         ui.VBoxNoSpacing(),
		collapsedCategories: make(map[string]bool),
		unreadChannels:      make(map[string]bool),
		fetchedAuthors:      make(map[string]bool),
	}
}

// Run shows the login window and starts the Fyne event loop.
func (a *App) Run() {
	a.showLogin()
	a.styleNativeChrome(a.window)
	a.window.ShowAndRun()
}

// doOnUI runs fn on the UI thread, blocking until it returns when wait is set.
// Every gateway handler and worker goroutine reaches the UI through here.
func (a *App) doOnUI(fn func(), wait bool) {
	a.fyne.Driver().DoFromGoroutine(fn, wait)
}

// deps returns the dependency bundle handed to widgets.
func (a *App) deps() ui.Deps {
	return ui.Deps{Session: a.session, Images: a.images, Texts: a.texts, Actions: a}
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

// styleNativeChrome recolours a window's native title bar to match the palette.
// The platform handle isn't available until the event loop has created the
// window, so it retries briefly until the styling lands. Every window the client
// opens goes through here, so none shows default chrome against our colours.
func (a *App) styleNativeChrome(window fyne.Window) {
	go func() {
		for range 40 {
			var done bool
			a.doOnUI(func() { done = ui.StyleTitlebar(window) }, true)
			if done {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
}

/* State accessors */

// stateChannel returns a channel from State, or nil when logged out or unknown.
// It centralises the session nil-check for the channel helpers below. DMs and
// groups need no special case: DirectMessages() feeds its result into State, so
// a conversation the Ready snapshot didn't carry is known here from the moment
// the home view loads it.
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

// channelServerID returns the server a channel belongs to, or "" for DMs.
func (a *App) channelServerID(channelID string) string {
	if channel := a.stateChannel(channelID); channel != nil && channel.Server != nil {
		return *channel.Server
	}

	return ""
}

// focusInput returns keyboard focus to the composer.
func (a *App) focusInput() {
	if a.input != nil {
		a.window.Canvas().Focus(a.input)
	}
}

/* ui.MessageActions */

// ResolveMessage looks a message up in the local cache.
func (a *App) ResolveMessage(channelID, messageID string) *revoltgo.Message {
	return a.messages.Find(channelID, messageID)
}

// OnReply focuses the composer with the given message queued as a reply.
func (a *App) OnReply(message *revoltgo.Message) {
	if a.currentChannelID == "" || a.input == nil || message == nil {
		return
	}

	a.input.AddReply(message)
	a.window.Canvas().Focus(a.input)
}

// OnAttachmentTapped opens an attachment in the viewer.
func (a *App) OnAttachmentTapped(attachment *revoltgo.File) {
	a.showAttachmentViewer(attachment)
}

// OnAvatarTapped is a placeholder for opening a user profile.
func (a *App) OnAvatarTapped(userID string) {
	// todo: user profile view
	log.Printf("avatar tapped: %s", userID)
}

// OnDelete deletes a message. Whether the action is offered at all is decided by
// the widget; the server is the final authority, so a rejected delete just logs.
func (a *App) OnDelete(message *revoltgo.Message) {
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

// OnEdit opens the in-place editor on the message's mounted widget. Only one edit
// is active at a time; starting another cancels the previous one.
func (a *App) OnEdit(message *revoltgo.Message) {
	if message == nil || message.Channel != a.currentChannelID {
		return
	}

	i := a.messageWidgetIndex(message.ID)
	if i == -1 {
		return
	}
	if w, ok := a.messageList.Objects[i].(*ui.MessageWidget); ok {
		a.startEditing(w)
	}
}

/* In-place editing */

// startEditing puts a mounted message widget into edit mode and focuses its
// entry. Saving sends the edit request; the authoritative content comes back
// through the MessageUpdate gateway event, which refreshes the widget.
func (a *App) startEditing(w *ui.MessageWidget) {
	a.cancelActiveEdit()

	session := a.session
	if session == nil {
		return
	}
	message := w.Message()

	save := func(newContent string) {
		a.editing = nil
		a.focusInput()

		// Apply the edit optimistically — onto a copy, since cache entries are
		// immutable — then reconcile: the gateway echo re-applies the authoritative
		// version, and a failed request reverts to the original.
		updated := *message
		updated.Content = newContent
		a.messages.Replace(message.Channel, &updated)
		a.refreshMessage(message.Channel, message.ID)

		go func() {
			params := revoltgo.MessageEditParams{Content: newContent}
			if _, err := session.ChannelMessageEdit(message.Channel, message.ID, params); err != nil {
				log.Printf("edit message %s: %v", message.ID, err)
				a.doOnUI(func() {
					a.messages.Replace(message.Channel, message)
					a.refreshMessage(message.Channel, message.ID)
				}, false)
			}
		}()
	}

	entry := w.StartEdit(save, func() {
		a.editing = nil
		a.focusInput()
	})
	if entry == nil {
		return
	}

	a.editing = w
	a.window.Canvas().Focus(entry)
}

// cancelActiveEdit closes any in-place editor without saving. Safe to call when
// none is active.
func (a *App) cancelActiveEdit() {
	if a.editing == nil {
		return
	}

	w := a.editing
	a.editing = nil
	w.CancelEdit()
}
