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
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	windowTitle = "Revoltgo Client"

	// deletePreviewRunes is how much of a message the delete confirmation quotes
	// back: enough to recognise it, short enough to keep the card one question.
	deletePreviewRunes = 120
)

// Info is what the binary knows about itself, stamped at link time and shown in
// the settings page's About section. main owns the values; internal/app cannot
// read them.
type Info struct {
	Version string
	Build   string
}

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
	emojis *cache.ImageCache // custom emoji, kept in a pool of their own
	texts  *cache.TextCache  // text-attachment previews

	// assetDir is the root both picture caches live under, held because the
	// settings page names the directory in use rather than the configured one — a
	// change to it only takes effect at the next start.
	assetDir string

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

	mainRow         *fyne.Container // the four-column fill row, relaid out by toggleMemberList
	tooltip         *ui.Tooltip     // the hover label floating over that row
	notices         *ui.NoticeStack // the transient messages floating over it too
	serverList      *fyne.Container
	channelList     *fyne.Container
	memberList      *ui.MemberList  // virtualised: it mounts only the rows on screen
	memberSidebar   *fyne.Container // the member column itself, hidden by its header toggle
	messageList     *fyne.Container
	messageScroll   *ui.ObservableScroll
	composerDock    *fyne.Container // slowmode chip + card: what the message column runs under
	floatingDock    *fyne.Container // that stack hung over messageScroll; relaid out when the chip appears
	input           *ui.MessageInput
	slowmodeBadge   *ui.SlowmodeBadge   // the cooldown chip above that card's top-right corner
	typingIndicator *ui.TypingIndicator // who is composing, at the other end of that row
	homeButton      *ui.SidebarButton
	friendsRow      *ui.FriendsRow // the way into the friends list, above the conversations
	serverHeader    *widget.Label
	channelHeader   *widget.Label
	channelGlyph    *fyne.Container // holds the message header's # / @ / group mark

	/* Modal layer and the settings page */

	// settings is a layer in the window's content stack rather than a canvas
	// overlay, so a confirmation — which is one — can still be shown over it.
	settings *ui.SettingsPage

	overlay    *ui.Overlay          // nil when nothing is showing
	joinDialog *ui.JoinServerDialog // the invite dialog on the modal layer, if any
	friends    *ui.FriendsDialog    // the friends list on that layer, if any
	editing    *ui.MessageWidget    // the message being edited in place, if any

	/* Lazy author resolution, see members.go */

	fetchedAuthors map[string]bool
	pendingAuthors []client.AuthorRef
	authorTimer    *time.Timer

	/* Lazy reply resolution, see messages.go */

	// replies are the messages quoted by mounted replies that the message cache
	// could not answer for — a reply reaches as far back as somebody cared to
	// answer, while the cache is only a channel's tail. Kept apart from that cache
	// for exactly that reason: filed among its messages, one would read as history.
	replies        map[string]*domain.Message
	fetchedReplies map[string]bool
	pendingReplies []client.MessageRef
	replyTimer     *time.Timer

	/* Coalesced sidebar rebuilds, see events.go */

	// dirty is which whole-surface rebuilds the events since the last flush have
	// invalidated, and refreshTimer is the settling window they are gathered over.
	dirty        refreshTarget
	refreshTimer *time.Timer

	/* The member sidebar, see members.go */

	// memberSeq drops the older of two rebuilds racing back from their walks;
	// memberStale records that the sidebar is hidden and has stopped following.
	memberSeq   uint64
	memberStale bool

	fetchedMembers map[string]bool // serverID -> its whole membership has been pulled

	// memberLoading is the server whose membership is in flight, memberWatchdog
	// the timer that gives the sidebar an answer when it never lands, and
	// memberFailed the servers whose fetch failed and has not been retried.
	memberLoading  string
	memberWatchdog *time.Timer
	memberFailed   map[string]bool

	/* Read-ack coalescing, see events.go */

	ackTimer     *time.Timer
	ackChannelID string
	ackMessageID string

	/* Slowmode, see messages.go */

	slowmodeUntil map[string]time.Time // channelID -> when this account may send there again
	slowmodeTimer *time.Timer          // re-armed a second at a time while one is running

	/* Typing indicators, see typing.go */

	// typing is who is composing where and when to stop saying so. Every channel
	// is tracked, not only the open one, because the sidebar marks the others.
	// typingTimer is re-armed to the next expiry across all of them.
	typing      map[string]map[string]time.Time // channelID -> userID -> when it lapses
	typingTimer *time.Timer

	// The sending half: where this account last announced itself, when, and the
	// quiet period after which it takes that back. lastTypedAt is what the idle
	// timer checks rather than trusting its own firing — see armTypingIdle.
	typingChannelID string
	sentTypingAt    time.Time
	lastTypedAt     time.Time
	typingIdleTimer *time.Timer

	/* Invite cards, see overlay.go */

	// invites is what a code resolved to, kept because a card is rebuilt every
	// time its message scrolls back into view and the answer never changes within
	// a session. pendingInvites holds the cards waiting on a request already in
	// flight, so the same invite posted twice costs one.
	invites        map[string]inviteResult
	pendingInvites map[string][]func(domain.Invite, error)

	// epoch counts logins. A worker captures it before it leaves the UI thread and
	// checks it on the way back, so a response that outlived a logout is dropped
	// instead of painting the previous account's data into the new one's view.
	epoch uint64

	info Info // version and build, for the About section

	// stylesDirty records that the tables have moved under a client that has not
	// been rebuilt from them yet, because the settings page was covering it.
	stylesDirty bool

	/* The login screens, see session.go */

	// loginStatus is the one line the login and second-factor screens report on,
	// there being no notice layer until the main UI exists; readyTimer is the
	// watchdog on the gateway snapshot that builds it.
	loginStatus *ui.StatusLine
	readyTimer  *time.Timer

	pendingToken   string // stashed by a credential login until Ready names the user
	pendingJoin    bool   // a join is in flight, so its ServerJoined should select
	loadingHistory bool
}

var _ ui.MessageActions = (*App)(nil)

// New creates the application and its main window. The caches are sized from the
// settings here and never resized, which is why the settings page marks their
// entries as needing a restart.
func New(fyneApp fyne.App, info Info) *App {
	window := fyneApp.NewWindow(windowTitle)
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))
	window.SetIcon(assets.AppIcon)

	revolt := client.New()
	settings := config.Current().Cache
	assetDir := cache.CacheRoot(settings.AssetDir)

	a := &App{
		fyne:                fyneApp,
		window:              window,
		info:                info,
		client:              revolt,
		store:               revolt.Store(),
		assetDir:            assetDir,
		images:              cache.NewImageCache(assetDir, cache.ImagesFolder, imageLimits(settings)),
		emojis:              cache.NewImageCache(assetDir, cache.EmojisFolder, emojiLimits(settings)),
		texts:               cache.NewTextCache(settings.TextPreviews),
		serverList:          container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelList:         container.NewVBox(),
		messageList:         ui.VBoxNoSpacing(),
		tooltip:             ui.NewTooltip(),
		notices:             ui.NewNoticeStack(),
		collapsedCategories: make(map[string]bool),
		unreadChannels:      make(map[string]bool),
		fetchedAuthors:      make(map[string]bool),
		fetchedMembers:      make(map[string]bool),
		memberFailed:        make(map[string]bool),
		replies:             make(map[string]*domain.Message),
		fetchedReplies:      make(map[string]bool),
		slowmodeUntil:       make(map[string]time.Time),
		typing:              make(map[string]map[string]time.Time),
	}

	// Built here rather than on first open so the layer is a fixed object buildUI
	// can stack: the page has to survive the rebuild a style change asks for.
	a.settings = ui.NewSettingsPage(a.settingsHooks())

	return a
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
	return ui.Deps{Store: a.store, Images: a.images, Emojis: a.emojis, Texts: a.texts, Actions: a}
}

// showMainUI swaps the window to the main layout and wires up shutdown.
func (a *App) showMainUI() {
	a.window.SetPadded(false) // sections sit flush against the window chrome
	a.window.SetContent(a.buildUI())
	a.window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	a.window.SetOnClosed(func() {
		if err := config.Save(); err != nil {
			log.Printf("save settings: %v", err)
		}

		a.images.Shutdown()
		a.emojis.Shutdown()
		a.client.Shutdown()
	})
}

// styleNativeChrome recolours a window's native title bar to match the palette.
// The platform handle isn't available until the event loop has created the
// window, so it retries briefly until the styling lands. Every window the client
// opens goes through here, so none shows default chrome against our colours.
//
// Turned off in the settings, the window keeps whatever chrome the system gives
// it — which is the only way back, since the recolouring cannot be undone
// without restarting.
func (a *App) styleNativeChrome(window fyne.Window) {
	if !config.Current().Interface.ThemeTitleBar {
		return
	}

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

// ResolveMessage looks a message up in the local cache, falling back to the
// reply targets fetched for quotes the cache had scrolled past. Never the
// network — a widget asks this while it builds.
func (a *App) ResolveMessage(channelID, messageID string) *domain.Message {
	if message := a.client.Messages().Find(channelID, messageID); message != nil {
		return message
	}

	return a.replies[messageID]
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
		a.notifyFailure("delete message "+message.ID, "Could not delete that message."),
	)
}

// OnPin pins or unpins a message. Nothing is applied optimistically — the client
// records the new state only once the server has agreed, so a refused pin leaves
// the row exactly as it was and says so.
//
// The repaint is made here rather than left to the gateway. Revolt does echo the
// pin back as a system message, but the client has already written what that
// event carries, and the handler behind it deliberately announces nothing when
// the state it was told about is the state it holds — otherwise every pin would
// redraw its row twice.
func (a *App) OnPin(message *domain.Message, pinned bool) {
	if message == nil {
		return
	}

	channelID, messageID := message.ChannelID, message.ID
	what, failure := "pin message ", "Could not pin that message."
	if !pinned {
		what, failure = "unpin message ", "Could not unpin that message."
	}

	epoch := a.epoch
	onFail := a.notifyFailure(what+messageID, "%s", failure)

	go func() {
		err := a.client.PinMessage(channelID, messageID, pinned)

		a.doOnUI(func() {
			if err != nil {
				onFail(err)
				return
			}
			if !a.stale(epoch) {
				a.refreshMessage(channelID, messageID)
			}
		}, false)
	}()
}

// OnReact adds or removes this account's reaction. Like a pin, nothing is
// applied before the server agrees and the repaint is made here: the gateway
// does echo a reaction back, but the client has already written what that event
// carries, and the handler behind it announces nothing when the state it is told
// about is the state it holds — otherwise every chip would redraw twice.
func (a *App) OnReact(message *domain.Message, emoji string, add bool) {
	if message == nil || emoji == "" {
		return
	}

	channelID, messageID := message.ChannelID, message.ID
	what, failure := "react to message ", "Could not add that reaction."
	if !add {
		what, failure = "unreact to message ", "Could not remove that reaction."
	}

	epoch := a.epoch
	onFail := a.notifyFailure(what+messageID, "%s", failure)

	go func() {
		err := a.client.React(channelID, messageID, emoji, add)

		a.doOnUI(func() {
			if err != nil {
				onFail(err)
				return
			}
			if !a.stale(epoch) {
				a.refreshMessage(channelID, messageID)
			}
		}, false)
	}()
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

		// The revert has to happen before the notice, so this wraps notifyFailure
		// rather than restating it.
		onFail := a.notifyFailure("edit message "+message.ID, "Could not save your edit.")
		a.background(
			func() error { return a.client.EditMessage(message.ChannelID, message.ID, newContent) },
			func(err error) {
				a.client.Messages().Replace(message.ChannelID, message)
				a.refreshMessage(message.ChannelID, message.ID)
				onFail(err)
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
