// Package app wires the Revolt client, the image caches, and the UI into a
// running application. It owns the view state and the widgets; everything that
// talks to Revolt lives in internal/client, and widgets receive what they need
// through ui.Deps.
package app

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/audio"
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

	/* Alerts, see alerts.go */

	// sounds is the audio engine; it holds no device until something is played.
	// focused is whether the window is in front, written by Fyne's foreground hooks
	// from the driver's own goroutine rather than the UI thread — hence the atomic,
	// the one field here that is not UI-thread confined.
	sounds  *audio.Engine
	focused atomic.Bool

	// reconnected marks a session that follows one this run already lost, which is
	// the difference between the connection coming back and the client starting.
	reconnected bool

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
	channelColumn   *fyne.Container // header + pinned group over the list; relaid out when the group changes
	channelTop      *fyne.Container // that group: what is pinned above the channels, full column width
	channelList     *fyne.Container
	memberList      *ui.MemberList  // virtualised: it mounts only the rows on screen
	memberSidebar   *fyne.Container // the member column itself, hidden by its header toggle
	messageList     *fyne.Container
	messageScroll   *ui.ObservableScroll
	messageHeader   *fyne.Container   // the channel's name row
	messageColumn   *fyne.Container   // header + note + dock; relaid out when the note appears
	channelNote     fyne.CanvasObject // the standing caption under that header, shown in a voice channel
	composerDock    *fyne.Container   // badge row + jump bar + card: what the message column runs under
	floatingDock    *fyne.Container   // that stack hung over messageScroll; relaid out when either appears
	input           *ui.MessageInput
	composerEntry   *fyne.Container     // the entry row, hidden where the account may not write
	composerNotice  *ui.ComposerNotice  // what stands in its place then
	jumpBar         *ui.JumpBar         // the way back to the live tail, over that card
	slowmodeBadge   *ui.SlowmodeBadge   // the cooldown chip above that card's top-right corner
	typingIndicator *ui.TypingIndicator // who is composing, at the other end of that row
	homeButton      *ui.SidebarButton
	friendsRow      *ui.FriendsRow // the way into the friends list, above the conversations
	serverHeader    *widget.Label
	channelHeader   *widget.Label
	channelGlyph    *fyne.Container  // holds the message header's # / @ / group mark
	channelTopic    *ui.ChannelTopic // what the channel is for, after its name in that row

	/* Modal layer and the settings page */

	// settings is a layer in the window's content stack rather than a canvas
	// overlay, so a confirmation — which is one — can still be shown over it.
	settings *ui.SettingsPage

	overlay       *ui.Overlay          // nil when nothing is showing
	joinDialog    *ui.JoinServerDialog // the invite dialog on the modal layer, if any
	prompt        *ui.PromptDialog     // the field-and-a-button card on that layer, if any
	channelDialog *ui.ChannelDialog    // the channel editor on that layer, if any
	friends       *ui.FriendsDialog    // the friends list on that layer, if any
	editing       *ui.MessageWidget    // the message being edited in place, if any

	/* What the settings page draws about this account, see settings.go */

	// selfProfile is this account's bio and banner, selfProfileOK whether they have
	// been fetched. A profile is not on the user record and no event announces one,
	// so it is asked for once and dropped after an edit.
	selfProfile   domain.UserProfile
	selfProfileOK bool

	// selfAvatarURL and selfHandle are what the open Account section was drawn from,
	// so a user update naming this account can be told from one that moved something
	// else and the section rebuilt only when it must be.
	selfAvatarURL string
	selfHandle    string

	/* The pinned-messages panel, see pins.go */

	// pins is the panel on the modal layer, pinned what it is drawn from — a fetched
	// snapshot rather than anything cached, kept only while the panel is up.
	// pinsChannelID is which channel was asked about, so an answer arriving after
	// the reader moved on is dropped.
	pins          *ui.PinsDialog
	pinsChannelID string
	pinned        []*domain.Message

	/* Channel search, see search.go */

	// search is the panel on the modal layer, if any, and searchQuery the query
	// it is waiting on — an answer to an older one is dropped rather than drawn
	// under a newer. searchChannelID is what pins' own is, for the same reason.
	search          *ui.SearchDialog
	searchChannelID string
	searchQuery     string

	/* Lazy author resolution, see members.go */

	fetchedAuthors map[string]bool
	pendingAuthors []client.AuthorRef
	authorTimer    *time.Timer

	/* Lazy reply resolution, see messages.go */

	// uncached are the messages the channel cache cannot answer for: quote targets
	// older than its tail, and the window a jump landed in. Both reach as far back
	// as somebody cared to answer or to name, where the cache is a contiguous tail —
	// filed among its messages, either would read as history.
	uncached       map[string]*domain.Message
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

	// invites is what a code resolved to, kept because a card is rebuilt every time
	// its message scrolls back into view and the answer never changes within a
	// session. pendingInvites holds the cards waiting on a request already out, so
	// the same invite posted twice costs one.
	invites        map[string]inviteResult
	pendingInvites map[string][]func(domain.Invite, error)

	// epoch counts logins. A worker captures it before leaving the UI thread and
	// checks it on the way back, so a response that outlived a logout is dropped
	// rather than painting the old account's data into the new one's view.
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

	pendingToken string // stashed by a credential login until Ready names the user
	pendingJoin  bool   // a join is in flight, so its ServerJoined should select

	/* The mounted window, see messages.go */

	// loadingPage is a page request in flight for the open channel. One flag
	// covers both directions: the client refuses a second request per channel
	// anyway, so a scroll up and a scroll down could never be out at once.
	//
	// jumped marks the column as showing a window a jump landed in rather than
	// the channel's tail, which is what offers the way back. atOldest records
	// that such a window has reached the start of the channel — the cache
	// answers that for itself, and a jump window is not in the cache.
	//
	// settleTimer is the pending re-scroll to the bottom after a channel's tail
	// is mounted, see settleAtBottom.
	//
	// editMarkTimer is the clock rewriting the "edited N ago" spans on the mounted
	// rows, armed only while one of them carries a mark — see refreshEditMarks.
	loadingPage bool
	jumped      bool
	atOldest    bool

	settleTimer   *time.Timer
	editMarkTimer *time.Timer
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
		sounds:              audio.NewEngine(),
		serverList:          container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelTop:          ui.VBoxNoSpacing(),
		channelList:         container.NewVBox(),
		messageList:         ui.VBoxNoSpacing(),
		tooltip:             ui.NewTooltip(),
		notices:             ui.NewNoticeStack(),
		collapsedCategories: make(map[string]bool),
		unreadChannels:      make(map[string]bool),
		fetchedAuthors:      make(map[string]bool),
		fetchedMembers:      make(map[string]bool),
		memberFailed:        make(map[string]bool),
		uncached:            make(map[string]*domain.Message),
		fetchedReplies:      make(map[string]bool),
		slowmodeUntil:       make(map[string]time.Time),
		typing:              make(map[string]map[string]time.Time),
	}

	// Built here rather than on first open, so the layer is a fixed object buildUI
	// can stack: the page has to survive the rebuild a style change asks for.
	a.settings = ui.NewSettingsPage(a.settingsHooks())

	return a
}

// Run shows the login window, starts the event pump, and enters the Fyne event
// loop.
func (a *App) Run() {
	applyPacing()

	go a.pumpEvents()

	a.startAlerts()
	a.showLogin()
	a.styleNativeChrome(a.window)
	a.window.ShowAndRun()
}

// doOnUI runs fn on the UI thread, blocking until it returns when wait is set.
// Every worker goroutine and the event pump reach the UI through here.
func (a *App) doOnUI(fn func(), wait bool) {
	a.fyne.Driver().DoFromGoroutine(fn, wait)
}

// background runs fn off the UI thread and posts onFail there when it fails. The
// shape every action here takes: the request goes to the client, a failure comes
// back as a notice, and the UI is updated by the gateway event that follows.
func (a *App) background(fn func() error, onFail func(err error)) {
	a.run(workLabel(2), fn, onFail, nil)
}

// backgroundThen is background with somewhere to hear about a success, for the
// actions the gateway cannot be trusted to announce. then runs on the UI thread
// and only for a session still current; a failure is reported whatever the
// epoch, an error being worth saying either way.
func (a *App) backgroundThen(fn func() error, onFail func(err error), then func()) {
	a.run(workLabel(2), fn, onFail, then)
}

// run is the worker both take. The pprof label is what a traceback header and
// the goroutine-leak profile print, so a worker still blocked names the action
// that started it rather than naming this one function.
func (a *App) run(label string, fn func() error, onFail func(err error), then func()) {
	epoch := a.epoch

	go pprof.Do(context.Background(), pprof.Labels("work", label), func(context.Context) {
		err := fn()
		if err == nil && then == nil {
			return
		}

		a.doOnUI(func() {
			switch {
			case err != nil:
				onFail(err)
			case !a.stale(epoch):
				then()
			}
		}, false)
	})
}

// workLabel names the caller skip frames up with its package path trimmed. Read
// on the UI thread, before the worker it labels exists.
func workLabel(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return "background"
	}

	name := runtime.FuncForPC(pc).Name()
	if _, after, cut := strings.CutLast(name, "/"); cut {
		name = after
	}

	return name
}

// stale reports whether epoch — captured before a worker left the UI thread — is
// from a session that has since been replaced. Call on the UI thread.
func (a *App) stale(epoch uint64) bool { return a.epoch != epoch }

// deps returns the dependency bundle handed to widgets.
func (a *App) deps() ui.Deps {
	return ui.Deps{
		Store: a.store, Images: a.images, Emojis: a.emojis, Texts: a.texts,
		Actions: a, Tooltip: a.tooltip,
	}
}

// showMainUI swaps the window to the main layout and wires up shutdown.
func (a *App) showMainUI() {
	a.window.SetPadded(false) // sections sit flush against the window chrome
	a.window.SetContent(a.buildUI())
	a.bindKeys() // the message column answers Escape once there is one
	a.window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	a.window.SetOnClosed(func() {
		if err := config.Save(); err != nil {
			log.Printf("save settings: %v", err)
		}

		a.images.Shutdown()
		a.emojis.Shutdown()
		a.sounds.Close()
		a.client.Shutdown()
	})
}

// styleNativeChrome recolours a window's native title bar to match the palette.
// The platform handle is not available until the event loop has created the
// window, so it retries briefly. Every window goes through here, so none shows
// default chrome against our colours. Turned off in the settings the window keeps
// the system's — the only way back, the recolouring not being undoable in place.
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

// ResolveMessage looks a message up in the cache, falling back to what was
// fetched for quotes and jumps past its tail. Never the network — a widget asks
// this while it builds.
func (a *App) ResolveMessage(channelID, messageID string) *domain.Message {
	if message := a.client.Messages().Find(channelID, messageID); message != nil {
		return message
	}

	return a.uncached[messageID]
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

// OnDelete asks before deleting. Whether the action is offered is the widget's
// decision; the confirmation is here because deleting is irreversible and the
// quick actions put it one click from the pointer.
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
// beforehand. Flattened onto one line: the card asks a question rather than being
// a second copy of the message.
func deletePrompt(message *domain.Message) string {
	content := strings.Join(strings.Fields(message.Content), " ")
	if content == "" {
		return "This message will be deleted for everyone."
	}

	return fmt.Sprintf("“%s” will be deleted for everyone.", util.Truncate(content, deletePreviewRunes))
}

// deleteMessage deletes without waiting: the message leaves the view through the
// MessageDeleted event, so a refused delete leaves it where it is and says so.
func (a *App) deleteMessage(message *domain.Message) {
	a.background(
		func() error { return a.client.DeleteMessage(message.ChannelID, message.ID) },
		a.notifyFailure("delete message "+message.ID, "Could not delete that message."),
	)
}

// OnPin pins or unpins a message. Nothing is applied optimistically — the state
// is recorded only once the server agrees — and the repaint is made here rather
// than left to the gateway: Revolt does echo the pin back, but the client has
// already written what that event carries, and the handler announces nothing when
// what it is told is what it holds. Otherwise every pin would redraw twice.
func (a *App) OnPin(message *domain.Message, pinned bool) { a.setPinned(message, pinned, nil) }

// setPinned is OnPin with somewhere for a second surface to hear about it: the
// pins panel takes a pin off a message that need not be mounted at all, so
// refreshing the row cannot be the whole answer. then runs on the UI thread once
// the server agrees, and only for a session still current.
func (a *App) setPinned(message *domain.Message, pinned bool, then func()) {
	if message == nil {
		return
	}

	channelID, messageID := message.ChannelID, message.ID
	what, failure := "pin message ", "Could not pin that message."
	if !pinned {
		what, failure = "unpin message ", "Could not unpin that message."
	}

	a.backgroundThen(
		func() error { return a.client.PinMessage(channelID, messageID, pinned) },
		a.notifyFailure(what+messageID, "%s", failure),
		func() {
			a.refreshMessage(channelID, messageID)
			if then != nil {
				then()
			}
		},
	)
}

// OnReact adds or removes this account's reaction. As with a pin, nothing is
// applied before the server agrees and the repaint is made here — see OnPin.
func (a *App) OnReact(message *domain.Message, emoji string, add bool) {
	if message == nil || emoji == "" {
		return
	}

	channelID, messageID := message.ChannelID, message.ID
	what, failure := "react to message ", "Could not add that reaction."
	if !add {
		what, failure = "unreact to message ", "Could not remove that reaction."
	}

	a.backgroundThen(
		func() error { return a.client.React(channelID, messageID, emoji, add) },
		a.notifyFailure(what+messageID, "%s", failure),
		func() { a.refreshMessage(channelID, messageID) },
	)
}

// OnClearReactions asks before taking every reaction off a message — confirmed
// where adding or removing one is not, because this undoes other people's clicks
// and nothing puts them back.
func (a *App) OnClearReactions(message *domain.Message) {
	if message == nil {
		return
	}

	channelID, messageID := message.ChannelID, message.ID
	a.confirm(ui.Confirm{
		Title:     "Clear reactions",
		Body:      "Every reaction on this message will be removed, for everyone.",
		Action:    "Clear",
		Tone:      ui.ToneDanger,
		OnConfirm: func() { a.clearReactions(channelID, messageID) },
	})
}

// clearReactions clears them and repaints here for the reason a pin does: what
// Revolt sends back cannot be read for reactions, so the client's own write is
// the only thing that knows they are gone.
func (a *App) clearReactions(channelID, messageID string) {
	a.backgroundThen(
		func() error { return a.client.ClearReactions(channelID, messageID) },
		a.notifyFailure("clear reactions on message "+messageID, "Could not clear those reactions."),
		func() { a.refreshMessage(channelID, messageID) },
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
