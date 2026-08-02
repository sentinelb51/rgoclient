// Package app wires the Revolt client, the image caches, and the UI into a
// running application. It owns the view state and the widgets; everything that
// talks to Revolt lives in internal/client, and widgets receive what they need
// through ui.Deps.
package app

import (
	"fmt"
	"log"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	windowTitle = "Revoltgo Client"

	cachedPreviews = 100 // text-attachment previews kept in memory

	// deletePreviewRunes is how much of a message the delete confirmation quotes
	// back: enough to recognise it, short enough to keep the card one question.
	deletePreviewRunes = 120
)

// App holds the view state and the mounted widgets. It also implements
// ui.MessageActions, so widgets can call back into it.
//
// Everything here is UI-thread confined unless a field says otherwise. Worker
// goroutines reach it through doOnUI, and reach Revolt through client, which is
// safe from anywhere.
type App struct {
	fyne   fyne.App
	window fyne.Window

	client *client.Client
	store  domain.Store      // client.Store(), held for how often it is read
	images *cache.ImageCache // avatars, icons, attachments
	texts  *cache.TextCache  // text-attachment previews

	/* View state */

	serverIDs        []string
	currentServerID  string
	currentChannelID string

	// The home view (navigation.go). homeSelected is what "home is open" means:
	// home has no server, so an empty currentServerID alone can't tell it apart
	// from nothing being selected. dmChannels holds only the sidebar order — the
	// conversations themselves live in the store.
	homeSelected bool
	dmChannels   []string
	loadingDMs   bool

	collapsedCategories map[string]bool // "serverID:categoryID" -> collapsed
	unreadChannels      map[string]bool

	/* Mounted UI */

	mainRow       *fyne.Container // the four-column fill row, relaid out by toggleMemberList
	tooltip       *ui.Tooltip     // the hover label floating over that row
	notices       *ui.NoticeStack // the transient messages floating over it too
	serverList    *fyne.Container
	channelList   *fyne.Container
	memberList    *fyne.Container
	memberSidebar *fyne.Container // the member column itself, hidden by its header toggle
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
	pendingAuthors []client.AuthorRef
	authorTimer    *time.Timer

	/* Read-ack coalescing, see events.go */

	ackTimer     *time.Timer
	ackChannelID string
	ackMessageID string

	// epoch counts logins. A worker captures it before it leaves the UI thread and
	// checks it on the way back, so a response that outlived a logout is dropped
	// instead of painting the previous account's data into the new one's view.
	epoch uint64

	pendingToken   string // stashed by a credential login until Ready names the user
	pendingJoin    bool   // a join is in flight, so its ServerJoined should select
	loadingHistory bool
}

var _ ui.MessageActions = (*App)(nil)

// New creates the application and its main window.
func New(fyneApp fyne.App) *App {
	window := fyneApp.NewWindow(windowTitle)
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))
	window.SetIcon(assets.AppIcon)

	revolt := client.New()

	return &App{
		fyne:                fyneApp,
		window:              window,
		client:              revolt,
		store:               revolt.Store(),
		images:              cache.NewImageCache(),
		texts:               cache.NewTextCache(cachedPreviews),
		serverList:          container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelList:         container.NewVBox(),
		memberList:          ui.VBoxNoSpacing(),
		messageList:         ui.VBoxNoSpacing(),
		tooltip:             ui.NewTooltip(),
		notices:             ui.NewNoticeStack(),
		collapsedCategories: make(map[string]bool),
		unreadChannels:      make(map[string]bool),
		fetchedAuthors:      make(map[string]bool),
	}
}

// Run shows the login window, starts the event pump, and enters the Fyne event
// loop.
func (a *App) Run() {
	go a.pumpEvents()

	a.showLogin()
	a.styleNativeChrome(a.window)
	a.window.ShowAndRun()
}

// doOnUI runs fn on the UI thread, blocking until it returns when wait is set.
// Every worker goroutine and the event pump reach the UI through here.
func (a *App) doOnUI(fn func(), wait bool) {
	a.fyne.Driver().DoFromGoroutine(fn, wait)
}

// background runs fn off the UI thread and, when it fails, posts onFail on the
// UI thread. It is the shape every action here takes: the request goes to the
// client, the outcome comes back as a notice, and the UI itself is updated by
// the gateway event that follows.
func (a *App) background(fn func() error, onFail func(err error)) {
	go func() {
		if err := fn(); err != nil {
			a.doOnUI(func() { onFail(err) }, false)
		}
	}()
}

// stale reports whether epoch — captured before a worker left the UI thread — is
// from a session that has since been replaced. Call on the UI thread.
func (a *App) stale(epoch uint64) bool { return a.epoch != epoch }

// deps returns the dependency bundle handed to widgets.
func (a *App) deps() ui.Deps {
	return ui.Deps{Store: a.store, Images: a.images, Texts: a.texts, Actions: a}
}

// showMainUI swaps the window to the main layout and wires up shutdown.
func (a *App) showMainUI() {
	a.window.SetPadded(false) // sections sit flush against the window chrome
	a.window.SetContent(a.buildUI())
	a.window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	a.window.SetOnClosed(func() {
		a.images.Shutdown()
		a.client.Shutdown()
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

// currentServer returns the selected server, or false when none is.
func (a *App) currentServer() (domain.Server, bool) {
	return a.store.Server(a.currentServerID)
}

// currentChannel returns the selected channel, or false when none is.
func (a *App) currentChannel() (domain.Channel, bool) {
	return a.store.Channel(a.currentChannelID)
}

// channelServerID returns the server a channel belongs to, or "" for a
// conversation.
func (a *App) channelServerID(channelID string) string {
	if channel, ok := a.store.Channel(channelID); ok {
		return channel.ServerID
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
func (a *App) ResolveMessage(channelID, messageID string) *domain.Message {
	return a.client.Messages().Find(channelID, messageID)
}

// OnReply focuses the composer with the given message queued as a reply.
func (a *App) OnReply(message *domain.Message) {
	if a.currentChannelID == "" || a.input == nil || message == nil {
		return
	}

	a.input.AddReply(message)
	a.window.Canvas().Focus(a.input)
}

// OnAttachmentTapped opens an attachment in the viewer.
func (a *App) OnAttachmentTapped(attachment *domain.File) {
	a.showAttachmentViewer(attachment)
}

// OnUserTapped opens a profile; it lives in profile.go with the rest of them.

// OnDelete asks before deleting a message. Whether the action is offered at all
// is decided by the widget; the confirmation is here because deleting is
// irreversible and the quick actions put it one click from the pointer.
func (a *App) OnDelete(message *domain.Message) {
	if message == nil {
		return
	}

	a.confirm(ui.Confirm{
		Title:     "Delete message",
		Body:      deletePrompt(message),
		Action:    "Delete",
		Tone:      ui.ToneDanger,
		OnConfirm: func() { a.deleteMessage(message) },
	})
}

// deletePrompt quotes what is about to go, so a misaimed delete shows itself
// before it happens rather than after. The quoted text is flattened onto one
// line: the card asks a question, it isn't a second copy of the message.
func deletePrompt(message *domain.Message) string {
	content := strings.Join(strings.Fields(message.Content), " ")
	if content == "" {
		return "This message will be deleted for everyone."
	}

	return fmt.Sprintf("“%s” will be deleted for everyone.", util.Truncate(content, deletePreviewRunes))
}

// deleteMessage deletes without waiting: the message leaves the view through the
// MessageDeleted event. The server is the final authority, so a rejected delete
// leaves it where it is and says so.
func (a *App) deleteMessage(message *domain.Message) {
	a.background(
		func() error { return a.client.DeleteMessage(message.ChannelID, message.ID) },
		func(err error) {
			log.Printf("delete message %s: %v", message.ID, err)
			a.notify(ui.ToneDanger, "Could not delete that message.")
		},
	)
}

// OnEdit opens the in-place editor on the message's mounted widget. Only one edit
// is active at a time; starting another cancels the previous one.
func (a *App) OnEdit(message *domain.Message) {
	if message == nil || message.ChannelID != a.currentChannelID {
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
// through the MessageUpdated event, which refreshes the widget.
func (a *App) startEditing(w *ui.MessageWidget) {
	a.cancelActiveEdit()

	if !a.client.Connected() {
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
		a.client.Messages().Replace(message.ChannelID, &updated)
		a.refreshMessage(message.ChannelID, message.ID)

		a.background(
			func() error { return a.client.EditMessage(message.ChannelID, message.ID, newContent) },
			func(err error) {
				log.Printf("edit message %s: %v", message.ID, err)
				a.client.Messages().Replace(message.ChannelID, message)
				a.refreshMessage(message.ChannelID, message.ID)
				a.notify(ui.ToneDanger, "Could not save your edit.")
			},
		)
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
