package app

import (
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	initialPageSize = 30 // messages fetched when first opening a channel

	// initialMountCount is how many messages a channel switch mounts. Far fewer
	// than the cache holds: only ~20 fit on screen, scrollback re-mounts the
	// rest from cache instantly, and every mounted widget is real work — rapid
	// channel switching churns widgets the renderer cache then holds onto for
	// up to a minute, so this directly bounds how fast memory can ratchet up.
	initialMountCount = 50

	messageGroupWindow = 7 * time.Minute // max gap for a message to group under the previous
)

// buildMessageArea builds the message list, header, and composer.
func (a *App) buildMessageArea() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	a.messageScroll = ui.NewObservableVScroll(a.messageList)
	a.messageScroll.OnScroll = func(pos fyne.Position) {
		if pos.Y <= 0 {
			a.loadMoreHistory()
			return
		}
		if a.messageList.MinSize().Height-a.messageScroll.Size().Height-pos.Y <= remountThreshold {
			a.mountNewerFromCache()
		}
	}
	a.clearMessages()

	a.input = ui.NewMessageInput(a.deps())
	a.input.SetPlaceHolder("Send a message...")
	a.input.OnSubmit = a.handleSubmit
	a.input.OnEditLast = a.editLastOwnMessage
	a.input.RegisterDropHandler(a.window)

	// Floating composer dock: the entry, reply and attachment rows sit in a square
	// card inset from the window edges so it floats just above the bottom. A grey
	// left bar — the same indicator a selected channel carries — runs the dock's
	// full height. The card fill matches the entry's own input background, so the
	// entry's box blends seamlessly into the card.
	dockBg := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	leftBar := canvas.NewRectangle(theme.Colors.TextPrimary)
	leftBar.SetMinSize(fyne.NewSize(3, 0))
	inner := ui.VBoxNoSpacing(
		a.input.ReplyContainer,
		a.input.AttachmentContainer,
		ui.WithCaret(a.input),
	)
	dock := container.NewStack(dockBg, container.NewBorder(nil, nil, leftBar, nil, inner))
	composer := container.NewPadded(dock)

	a.channelHeader = widget.NewLabelWithStyle(a.channelName(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(container.NewHBox(ui.HashtagIcon(), a.channelHeader))

	layout := container.NewBorder(header, composer, nil, nil, a.messageScroll)
	return container.NewStack(background, layout)
}

// newMessageWidget builds a message widget, drawing curr as a grouped
// continuation of prev when they belong together (see continuesGroup) and
// tightening its bottom margin when next continues it.
//
// Every mount path funnels through here, so this is also where an unresolved
// author is chased down: whether the message came from the gateway, the initial
// page, or scrollback, the widget can't render a name until State knows the
// user. ensureAuthor is a no-op once it does, so this costs two map lookups per
// widget in the common case.
func (a *App) newMessageWidget(prev, curr, next *revoltgo.Message) *ui.MessageWidget {
	if curr.System == nil && curr.Webhook == nil {
		a.ensureAuthor(a.channelServerID(curr.Channel), curr.Author)
	}
	return ui.NewMessageWidget(a.deps(), curr, continuesGroup(prev, curr), continuesGroup(curr, next))
}

// continuesGroup reports whether curr should render as a continuation of prev —
// same author, neither a system/webhook/masqueraded message, and within
// messageGroupWindow — so it's drawn without a repeated avatar/name header. A
// reply always starts a fresh group.
func continuesGroup(prev, curr *revoltgo.Message) bool {
	if prev == nil || curr == nil || curr.Author == "" || prev.Author != curr.Author {
		return false
	}
	if curr.System != nil || prev.System != nil ||
		curr.Webhook != nil || prev.Webhook != nil ||
		curr.Masquerade != nil || prev.Masquerade != nil {
		return false
	}
	if len(curr.Replies) > 0 {
		return false
	}

	pt, err1 := util.Timestamp(prev.ID)
	ct, err2 := util.Timestamp(curr.ID)
	if err1 != nil || err2 != nil {
		return false
	}
	gap := ct.Sub(pt)
	return gap >= 0 && gap <= messageGroupWindow
}

// showStatus replaces the message list with a single centered line.
func (a *App) showStatus(text string) {
	a.cancelActiveEdit()
	label := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	height := float32(400)
	if a.messageScroll != nil {
		if h := a.messageScroll.Size().Height - 5; h > 100 {
			height = h
		}
	}

	a.messageList.Objects = []fyne.CanvasObject{ui.NewMinHeightContainer(height, container.NewCenter(label))}
	a.messageList.Refresh()

	// The inner list refresh alone doesn't repaint the scroll viewport until an
	// event forces relayout, so the status would only appear after a scroll.
	// Reset to the top and refresh the scroll explicitly.
	if a.messageScroll != nil {
		a.messageScroll.ScrollToTop()
		a.messageScroll.Refresh()
	}
}

// clearMessages empties the message list.
func (a *App) clearMessages() {
	a.cancelActiveEdit()
	a.messageList.Objects = nil
	a.messageList.Refresh()
	a.scrollToBottom()
}

// loadChannelMessages fetches the newest page of messages for a channel.
func (a *App) loadChannelMessages(channelID string) {
	a.messageCache.SetDepleted(channelID, false)

	session := a.session
	if session == nil {
		return
	}

	go func() {
		page, err := session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			IncludeUsers: true,
			Limit:        initialPageSize,
		})
		if err != nil {
			a.doOnUI(func() {
				if a.currentChannelID == channelID {
					a.showStatus("Failed to load messages")
				}
			}, true)
			return
		}

		if len(page.Messages) == 0 {
			a.doOnUI(func() {
				if a.currentChannelID == channelID {
					a.showStatus("No messages in this channel")
					a.messageCache.SetDepleted(channelID, true)
				}
			}, true)
			return
		}

		a.messageCache.Set(channelID, page.Messages)
		a.doOnUI(func() {
			if a.currentChannelID == channelID {
				// Render from the cache, not from the page we just stored: a
				// gateway message can land between the two, and the cache is the
				// one view that already includes it.
				a.displayCached()
			}
		}, true)
	}()
}

// displayCached re-renders the open channel from its cached messages.
func (a *App) displayCached() {
	a.displayMessages(a.messageCache.Get(a.currentChannelID))
}

// displayMessages renders the newest initialMountCount messages (oldest first)
// and scrolls to the bottom. Older cached messages are re-mounted on scrollback
// by loadMoreHistory's cache tier. Call on the UI thread.
func (a *App) displayMessages(messages []*revoltgo.Message) {
	a.cancelActiveEdit()
	if len(messages) > initialMountCount {
		messages = messages[len(messages)-initialMountCount:]
	}

	widgets := make([]fyne.CanvasObject, len(messages))
	for i, message := range messages {
		var prev, next *revoltgo.Message
		if i > 0 {
			prev = messages[i-1]
		}
		if i+1 < len(messages) {
			next = messages[i+1]
		}
		widgets[i] = a.newMessageWidget(prev, message, next)
	}

	a.messageList.Objects = widgets
	a.messageList.Refresh()
	a.scrollToBottom()
}

// jumpToLatest brings the view back to the newest message: a plain scroll when
// the live tail is mounted, a re-render when scrollback has trimmed it away.
// Used on send, mirroring Discord's jump-to-present behaviour.
func (a *App) jumpToLatest() {
	cached := a.messageCache.Get(a.currentChannelID)
	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)
	if len(cached) == 0 || (bottom != nil && bottom.ID == cached[len(cached)-1].ID) {
		a.scrollToBottom()
		return
	}
	a.displayCached()
}

// removeMessage unmounts a deleted message, re-evaluating its neighbours'
// grouping (a continuation whose group head was deleted regains its header).
// Call on the UI thread.
func (a *App) removeMessage(channelID, messageID string) {
	if channelID != a.currentChannelID {
		return
	}
	i := a.messageWidgetIndex(messageID)
	if i == -1 {
		return
	}
	if a.editing != nil && a.editing.Message().ID == messageID {
		a.editing = nil // the editor unmounts with its widget
	}
	// slices.Delete zeroes the vacated tail slot, so the unmounted widget isn't
	// kept alive by the list's backing array.
	a.messageList.Objects = slices.Delete(a.messageList.Objects, i, i+1)

	prev, next := a.mountedMessage(i-1), a.mountedMessage(i)
	if next != nil {
		a.messageList.Objects[i] = a.newMessageWidget(prev, next, a.mountedMessage(i+1))
	}
	if i > 0 {
		if w, ok := a.messageList.Objects[i-1].(*ui.MessageWidget); ok {
			w.SetFollowedByGroup(continuesGroup(prev, next))
		}
	}
	a.messageList.Refresh()
}

// refreshMessage rebuilds the widget of an edited message in place from its
// updated cache entry. A message the user is in-place editing is left alone —
// the rebuild would discard their open editor; the cache already holds the
// remote update, so the next rebuild renders it. Call on the UI thread.
func (a *App) refreshMessage(channelID, messageID string) {
	if channelID != a.currentChannelID {
		return
	}
	if a.editing != nil && a.editing.Message().ID == messageID {
		return
	}
	i := a.messageWidgetIndex(messageID)
	if i == -1 {
		return
	}
	message := a.messageCache.Find(channelID, messageID)
	if message == nil {
		return
	}
	a.messageList.Objects[i] = a.newMessageWidget(a.mountedMessage(i-1), message, a.mountedMessage(i+1))
	a.messageList.Refresh()
}

// editLastOwnMessage opens the in-place editor on the user's newest editable
// message in the open channel — triggered by Up in an empty composer. It scans
// the message cache rather than tracking "last sent" state: the cache only
// ever gains own messages through the gateway echo (or fetched pages), so the
// scan can't race the send path — right after a send it simply targets the
// newest own message the server has actually confirmed.
func (a *App) editLastOwnMessage() {
	if a.session == nil || a.currentChannelID == "" {
		return
	}
	self := a.session.State.Self()
	if self == nil {
		return
	}

	cached := a.messageCache.Get(a.currentChannelID)
	for i := len(cached) - 1; i >= 0; i-- {
		message := cached[i]
		if message.Author == self.ID && message.System == nil && message.Content != "" {
			a.OnEdit(message)
			return
		}
	}
}
