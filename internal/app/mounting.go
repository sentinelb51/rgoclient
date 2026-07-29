package app

// The mounted window: which slice of a channel's cached messages currently has
// live widgets in the message list, and how that window slides as the user
// scrolls. displayMessages (messages.go) opens the window at the live tail;
// everything here moves it.
//
// Invariants:
//   - Widgets are mounted oldest-first, matching the cache's own order.
//   - The window never exceeds mountedCap widgets, in either scroll direction.
//   - Scrolling down never needs the network: trimming only ever drops a
//     channel's oldest messages, so everything below the window is still cached.

import (
	"slices"

	"fyne.io/fyne/v2"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui"
)

const (
	renderedCap       = 200 // max message widgets kept mounted by live appends
	historyPageSize   = 50  // older messages fetched (or re-mounted) per scroll-up
	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"

	// mountedCap bounds the widget window during scrollback: prepends past it
	// trim widgets off the bottom (and vice versa), so scrolling through any
	// amount of history keeps a constant number of widgets mounted.
	mountedCap = renderedCap + historyPageSize

	// remountThreshold is how close to the bottom (px) the view must be before
	// trimmed-off newer messages are re-mounted from the cache.
	remountThreshold = 200
)

// mountedMessage returns the message rendered by the widget at index i in the
// message list, or nil when i is out of range or not a message widget (the list
// also holds status lines).
func (a *App) mountedMessage(i int) *revoltgo.Message {
	if i < 0 || i >= len(a.messageList.Objects) {
		return nil
	}
	if w, ok := a.messageList.Objects[i].(*ui.MessageWidget); ok {
		return w.Message()
	}
	return nil
}

// messageWidgetIndex returns the index of the mounted widget rendering
// messageID, or -1.
func (a *App) messageWidgetIndex(messageID string) int {
	for i, obj := range a.messageList.Objects {
		if w, ok := obj.(*ui.MessageWidget); ok && w.Message().ID == messageID {
			return i
		}
	}
	return -1
}

// appendMessage adds a freshly received message, trimming the oldest widget when
// over the render cap and keeping the scroll position stable. prev is the
// message's predecessor in its channel, captured when the message was cached.
func (a *App) appendMessage(message, prev *revoltgo.Message) {
	if a.currentChannelID == "" {
		return
	}

	// A status line ("No messages in this channel") may be showing; the first
	// real message replaces it.
	if len(a.messageList.Objects) == 1 && a.mountedMessage(0) == nil {
		a.messageList.Objects = nil
	}

	// When scrollback has trimmed the newest widgets, the view is detached from
	// the live tail: don't mount (the predecessor isn't on screen, so the row
	// would render against the wrong neighbour); the message is cached and
	// mounts via mountNewerFromCache on the way back down.
	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)
	if bottom != nil && prev != nil && bottom.ID != prev.ID {
		return
	}

	contentHeight := a.messageList.MinSize().Height
	viewHeight := a.messageScroll.Size().Height
	atBottom := contentHeight-viewHeight-a.messageScroll.Offset.Y < atBottomTolerance

	// When this message continues the one above it, tighten that message's bottom
	// margin so the group reads as a block.
	if continuesGroup(prev, message) {
		a.tightenBottomWidget()
	}
	a.messageList.Add(a.newMessageWidget(prev, message, nil))

	if over := len(a.messageList.Objects) - renderedCap; over > 0 {
		a.trimMountedTop(over, atBottom)
	}

	a.messageList.Refresh()
	if atBottom {
		a.scrollToBottom()
	} else {
		a.messageScroll.Refresh()
	}
}

// loadMoreHistory mounts older messages when the user scrolls to the top. It is
// two-tier: messages already cached but not mounted (displayMessages mounts only
// the newest initialMountCount) prepend synchronously from the cache; past that,
// an older page is fetched from the network. Both tiers anchor on the oldest
// mounted message, so cache trimming can never cause a refetch loop.
func (a *App) loadMoreHistory() {
	channelID := a.currentChannelID
	if a.loadingHistory || channelID == "" {
		return
	}
	top := a.mountedMessage(0)
	if top == nil {
		return
	}

	cached := a.messageCache.Get(channelID)
	if i, ok := slices.BinarySearchFunc(cached, top.ID, cache.CompareMessageID); ok && i > 0 {
		a.prependMessages(cached[max(0, i-historyPageSize):i])
		return
	}

	session := a.session
	if session == nil || a.messageCache.IsDepleted(channelID) {
		return
	}
	a.loadingHistory = true

	go func() {
		defer a.doOnUI(func() { a.loadingHistory = false }, true)

		page, err := session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			Before:       top.ID,
			Limit:        historyPageSize,
			IncludeUsers: true,
		})
		if err != nil || len(page.Messages) == 0 {
			if err == nil {
				a.messageCache.SetDepleted(channelID, true)
			}
			return
		}

		older := a.messageCache.Prepend(channelID, page.Messages)
		a.doOnUI(func() {
			if a.currentChannelID == channelID && a.mountedMessage(0) == top {
				a.prependMessages(older)
			}
		}, true)
	}()
}

// prependMessages mounts older messages (oldest first) above the current view,
// preserving the user's scroll position and trimming the bottom past mountedCap.
func (a *App) prependMessages(older []*revoltgo.Message) {
	if len(older) == 0 {
		return
	}
	oldHeight := a.messageList.MinSize().Height

	// The newest prepended message lands directly above the previously topmost
	// message, so that existing row is each one's neighbour at the seam.
	topMessage, topNext := a.mountedMessage(0), a.mountedMessage(1)

	// The oldest message has no loaded predecessor, so it renders as a full
	// message; every other one sees its true neighbours for grouping.
	widgets := make([]fyne.CanvasObject, len(older))
	for i, msg := range older {
		var prev, next *revoltgo.Message
		if i > 0 {
			prev = older[i-1]
		}
		if i+1 < len(older) {
			next = older[i+1]
		} else {
			next = topMessage
		}
		widgets[i] = a.newMessageWidget(prev, msg, next)
	}

	// The previously-topmost message now has a predecessor above it, so
	// re-evaluate its grouping (its successor is unchanged).
	if topMessage != nil {
		a.messageList.Objects[0] = a.newMessageWidget(older[len(older)-1], topMessage, topNext)
	}

	a.messageList.Objects = append(widgets, a.messageList.Objects...)
	a.messageList.Refresh()

	if diff := a.messageList.MinSize().Height - oldHeight; diff > 0 {
		a.messageScroll.Offset.Y += diff
		a.messageScroll.Refresh()
	}
	a.trimMountedBottom()
}

// mountNewerFromCache re-mounts cached messages below the bottom-most mounted
// one — the downward counterpart of loadMoreHistory's cache tier. It never
// needs the network: trimming only ever drops a channel's oldest messages, so
// everything below the mounted window is always in the cache.
func (a *App) mountNewerFromCache() {
	if a.currentChannelID == "" {
		return
	}
	bottom := a.mountedMessage(len(a.messageList.Objects) - 1)
	if bottom == nil {
		return
	}

	cached := a.messageCache.Get(a.currentChannelID)
	i, ok := slices.BinarySearchFunc(cached, bottom.ID, cache.CompareMessageID)
	if !ok || i+1 == len(cached) {
		return // bottom is the live tail (or no longer cached)
	}
	a.appendMessages(cached[i+1:min(i+1+historyPageSize, len(cached))], bottom)
}

// appendMessages mounts newer messages below the current view, preserving the
// scroll position and trimming the top past mountedCap.
func (a *App) appendMessages(page []*revoltgo.Message, bottom *revoltgo.Message) {
	if len(page) == 0 {
		return
	}

	// Tighten the old bottom widget's margin when the first new message
	// continues its group.
	if continuesGroup(bottom, page[0]) {
		a.tightenBottomWidget()
	}

	prev := bottom
	for i, msg := range page {
		var next *revoltgo.Message
		if i+1 < len(page) {
			next = page[i+1]
		}
		a.messageList.Add(a.newMessageWidget(prev, msg, next))
		prev = msg
	}

	if over := len(a.messageList.Objects) - mountedCap; over > 0 {
		a.trimMountedTop(over, false)
	}
	a.messageList.Refresh()
	a.messageScroll.Refresh()
}

// trimMountedTop unmounts the oldest n widgets, keeping the scroll position
// stable unless the view is pinned to the bottom anyway (where the caller's
// scrollToBottom makes the adjustment moot).
func (a *App) trimMountedTop(n int, atBottom bool) {
	objects := a.messageList.Objects

	var removed float32
	if !atBottom {
		for _, obj := range objects[:n] {
			removed += obj.MinSize().Height
		}
	}

	a.messageList.Objects = objects[n:]
	clear(objects[:n]) // release the trimmed widgets; the retained slice shares this array

	if !atBottom {
		a.messageScroll.Offset.Y = max(a.messageScroll.Offset.Y-removed, 0)
	}
}

// trimMountedBottom unmounts widgets far below the viewport after a prepend;
// they re-mount via mountNewerFromCache on the way back down. It stops when the
// would-be bottom row is no longer cached: the cache window ends at the live
// tail, so once scrollback runs past its cap the downward remount could not
// re-serve trimmed rows, and the window is allowed to grow instead.
func (a *App) trimMountedBottom() {
	over := len(a.messageList.Objects) - mountedCap
	if over <= 0 {
		return
	}
	objects := a.messageList.Objects
	keep := len(objects) - over

	newBottom := a.mountedMessage(keep - 1)
	if newBottom == nil || a.messageCache.Find(a.currentChannelID, newBottom.ID) == nil {
		return
	}
	a.messageList.Objects = objects[:keep]
	clear(objects[keep:])
	a.messageList.Refresh()
}

// tightenBottomWidget closes the gap under the bottom-most mounted message,
// marking it as the head of a group that continues into the row about to be
// appended.
func (a *App) tightenBottomWidget() {
	n := len(a.messageList.Objects)
	if n == 0 {
		return
	}
	if w, ok := a.messageList.Objects[n-1].(*ui.MessageWidget); ok {
		w.SetFollowedByGroup(true)
	}
}

// scrollToBottom scrolls the message view to the newest message.
func (a *App) scrollToBottom() {
	if a.messageScroll != nil {
		a.messageScroll.ScrollToBottom()
	}
}
