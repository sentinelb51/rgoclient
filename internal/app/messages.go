package app

import (
	"fmt"
	"image"
	"log"
	"net/url"
	"os"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	messageBatchSize  = 100 // messages rendered per UI-thread batch
	renderedCap       = 200 // max message widgets kept mounted by live appends
	historyPageSize   = 50  // older messages fetched per scroll-up
	initialPageSize   = 30  // messages fetched when first opening a channel
	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"

	// initialMountCount is how many messages a channel switch mounts. Far fewer
	// than the cache holds: only ~20 fit on screen, scrollback re-mounts the
	// rest from cache instantly, and every mounted widget is real work — rapid
	// channel switching churns widgets the renderer cache then holds onto for
	// up to a minute, so this directly bounds how fast memory can ratchet up.
	initialMountCount = 50

	// mountedCap bounds the widget window during scrollback: prepends past it
	// trim widgets off the bottom (and vice versa), so scrolling through any
	// amount of history keeps a constant number of widgets mounted.
	mountedCap = renderedCap + historyPageSize

	// remountThreshold is how close to the bottom (px) the view must be before
	// trimmed-off newer messages are re-mounted from the cache.
	remountThreshold = 200

	messageGroupWindow = 7 * time.Minute // max gap for a message to group under the previous
)

// newMessageWidget builds a message widget, drawing curr as a grouped
// continuation of prev when they belong together (see continuesGroup) and
// tightening its bottom margin when next continues it.
func (a *App) newMessageWidget(prev, curr, next *revoltgo.Message) *ui.MessageWidget {
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

// mountedMessage returns the message rendered by the widget at index i in the
// message list, or nil when i is out of range or not a message widget.
func (a *App) mountedMessage(i int) *revoltgo.Message {
	if i < 0 || i >= len(a.messageList.Objects) {
		return nil
	}
	if w, ok := a.messageList.Objects[i].(*ui.MessageWidget); ok {
		return w.Message()
	}
	return nil
}

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
		a.input,
	)
	dock := container.NewStack(dockBg, container.NewBorder(nil, nil, leftBar, nil, inner))
	composer := container.NewPadded(dock)

	a.channelHeader = widget.NewLabelWithStyle(a.channelName(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(container.NewHBox(ui.HashtagIcon(), a.channelHeader))

	layout := container.NewBorder(header, composer, nil, nil, a.messageScroll)
	return container.NewStack(background, layout)
}

// showStatus replaces the message list with a single centered line.
func (a *App) showStatus(text string) {
	a.renderGen++
	a.rendering = false
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
	a.renderGen++
	a.rendering = false
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

		stored := a.messageCache.Set(channelID, page.Messages)
		a.doOnUI(func() {
			if a.currentChannelID == channelID {
				a.displayMessages(stored)
			}
		}, true)
	}()
}

// displayMessages renders messages (oldest first) in batches to keep the UI
// responsive, scrolling to the bottom when done. Only the newest
// initialMountCount messages are mounted; older cached ones are re-mounted on
// scrollback by loadMoreHistory's cache tier. Each call bumps renderGen so the
// batches of a superseded render abort, even when the user switches away and
// back to the same channel before it finishes.
func (a *App) displayMessages(messages []*revoltgo.Message) {
	a.renderGen++
	gen := a.renderGen
	a.rendering = true
	a.messageList.Objects = nil

	if len(messages) > initialMountCount {
		messages = messages[len(messages)-initialMountCount:]
	}

	go func() {
		for start := 0; start < len(messages); start += messageBatchSize {
			end := min(start+messageBatchSize, len(messages))

			stale := false
			a.doOnUI(func() {
				if a.renderGen != gen {
					stale = true
					return
				}
				for idx := start; idx < end; idx++ {
					var prev, next *revoltgo.Message
					if idx > 0 {
						prev = messages[idx-1]
					}
					if idx+1 < len(messages) {
						next = messages[idx+1]
					}
					a.messageList.Add(a.newMessageWidget(prev, messages[idx], next))
				}
				a.messageList.Refresh()
			}, true)
			if stale {
				return
			}
		}

		a.doOnUI(func() {
			if a.renderGen == gen {
				a.rendering = false
				// Messages that arrived mid-render were cached but not mounted
				// (appendMessage holds off while rendering); catch up now.
				a.mountNewerFromCache()
				a.scrollToBottom()
			}
		}, false)
	}()
}

// appendMessage adds a freshly received message, trimming the oldest widget when
// over the render cap and keeping the scroll position stable. prev is the
// message's predecessor in its channel, captured when the message was cached.
func (a *App) appendMessage(message, prev *revoltgo.Message) {
	if a.currentChannelID == "" || a.rendering {
		return // mid-render arrivals are cached; the render's final pass mounts them
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
		if n := len(a.messageList.Objects); n > 0 {
			if last, ok := a.messageList.Objects[n-1].(*ui.MessageWidget); ok {
				last.SetFollowedByGroup(true)
			}
		}
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

// trimMountedTop unmounts the oldest n widgets, keeping the scroll position
// stable unless the view is pinned to the bottom anyway (where the caller's
// scrollToBottom makes the adjustment moot).
func (a *App) trimMountedTop(n int, atBottom bool) {
	var removed float32
	if !atBottom {
		for _, obj := range a.messageList.Objects[:n] {
			removed += obj.MinSize().Height
		}
	}
	a.messageList.Objects = a.messageList.Objects[n:]
	if !atBottom {
		a.messageScroll.Offset.Y = max(a.messageScroll.Offset.Y-removed, 0)
	}
}

// loadMoreHistory mounts older messages when the user scrolls to the top. It is
// two-tier: messages already cached but not mounted (displayMessages mounts only
// the newest renderedCap) prepend synchronously from the cache; past that, an
// older page is fetched from the network. Both tiers anchor on the oldest
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
// preserving the user's scroll position. The mounted list intentionally grows
// past renderedCap during scrollback — the cap bounds the initial render, while
// appendMessage keeps trimming the top as new messages arrive.
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
	widgets := make([]fyne.CanvasObject, 0, len(older))
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
		widgets = append(widgets, a.newMessageWidget(prev, msg, next))
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

	// Bound the mounted window by trimming widgets far below the viewport; they
	// re-mount via mountNewerFromCache on the way back down. Skipped mid-render
	// (displayMessages' batches assume the list tail is theirs) and skipped when
	// the would-be bottom row is no longer cached: the cache window ends at the
	// live tail, so once scrollback runs past its cap the downward remount could
	// not re-serve trimmed rows, and the window is allowed to grow instead.
	if over := len(a.messageList.Objects) - mountedCap; over > 0 && !a.rendering {
		objects := a.messageList.Objects
		newBottom := a.mountedMessage(len(objects) - over - 1)
		if newBottom != nil && a.messageCache.Find(a.currentChannelID, newBottom.ID) != nil {
			a.messageList.Objects = objects[:len(objects)-over]
			a.messageList.Refresh()
		}
	}
}

// mountNewerFromCache re-mounts cached messages below the bottom-most mounted
// one — the downward counterpart of loadMoreHistory's cache tier. It never
// needs the network: trimming only ever drops a channel's oldest messages, so
// everything below the mounted window is always in the cache.
func (a *App) mountNewerFromCache() {
	if a.currentChannelID == "" || a.rendering {
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
	if n := len(a.messageList.Objects); n > 0 && continuesGroup(bottom, page[0]) {
		if w, ok := a.messageList.Objects[n-1].(*ui.MessageWidget); ok {
			w.SetFollowedByGroup(true)
		}
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

// scrollToBottom scrolls the message view to the newest message.
func (a *App) scrollToBottom() {
	if a.messageScroll != nil {
		a.messageScroll.ScrollToBottom()
	}
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
	a.displayMessages(cached)
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
	a.messageList.Objects = append(a.messageList.Objects[:i], a.messageList.Objects[i+1:]...)

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
// updated cache entry. Call on the UI thread.
func (a *App) refreshMessage(channelID, messageID string) {
	if channelID != a.currentChannelID {
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

// handleSubmit sends the composed message, its attachments, and its replies.
func (a *App) handleSubmit(text string) {
	if (text == "" && len(a.input.Attachments) == 0) || a.currentChannelID == "" || a.session == nil {
		return
	}

	session := a.session
	channelID := a.currentChannelID
	attachments := append([]ui.Attachment(nil), a.input.Attachments...)
	replies := append([]ui.Reply(nil), a.input.Replies...)

	a.input.SetText("")
	a.input.ClearAttachments()
	a.input.ClearReplies()
	a.jumpToLatest()

	go func() {
		send := revoltgo.MessageSend{
			Content:     text,
			Attachments: uploadAttachments(session, attachments),
			Replies:     toMessageReplies(replies),
		}
		if _, err := session.ChannelMessageSend(channelID, send); err != nil {
			log.Printf("send message: %v", err)
		}
	}()
}

// uploadAttachments uploads each local file and returns the resulting IDs.
func uploadAttachments(session *revoltgo.Session, attachments []ui.Attachment) []string {
	ids := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		file, err := os.Open(attachment.Path)
		if err != nil {
			log.Printf("open attachment %s: %v", attachment.Path, err)
			continue
		}

		uploaded, err := session.AttachmentUpload(&revoltgo.File{Name: attachment.Name, Reader: file})
		_ = file.Close()
		if err != nil {
			log.Printf("upload attachment %s: %v", attachment.Name, err)
			continue
		}
		ids = append(ids, uploaded.ID)
	}
	return ids
}

// toMessageReplies converts composer replies to the API representation.
func toMessageReplies(replies []ui.Reply) []*revoltgo.MessageReplies {
	out := make([]*revoltgo.MessageReplies, len(replies))
	for i, r := range replies {
		out[i] = &revoltgo.MessageReplies{ID: r.ID, Mention: r.Mention}
	}
	return out
}

// showImageViewer opens an attachment in a resizable popup window.
func (a *App) showImageViewer(attachment *revoltgo.Attachment) {
	window := a.fyne.NewWindow(attachment.Filename)
	w, h := viewerSize(attachment.Metadata.Width, attachment.Metadata.Height)

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	viewer := container.NewStack(placeholder)

	attachmentURL := attachment.URL("")
	if attachmentURL != "" && attachment.ID != "" {
		a.images.LoadAsync(attachment.ID, attachmentURL, false, func(img image.Image) {
			canvasImg := canvas.NewImageFromImage(img)
			canvasImg.FillMode = canvas.ImageFillContain
			viewer.Objects = []fyne.CanvasObject{canvasImg}
			viewer.Refresh()
		})
	}

	openBrowser := widget.NewButton("Open in Browser", func() {
		if u, err := url.Parse(attachmentURL); err == nil {
			_ = a.fyne.OpenURL(u)
		}
	})
	dims := widget.NewLabel(fmt.Sprintf("%dx%d", attachment.Metadata.Width, attachment.Metadata.Height))
	bottom := container.NewHBox(container.NewPadded(dims), container.NewPadded(openBrowser))

	window.SetContent(container.NewBorder(nil, container.NewCenter(bottom), nil, nil, viewer))
	window.Resize(fyne.NewSize(w+40, h+80))
	window.CenterOnScreen()
	window.Show()
}

// viewerSize fits an image within the configured viewer bounds, preserving
// aspect ratio and enforcing a minimum size.
func viewerSize(width, height int) (float32, float32) {
	w, h := float32(width), float32(height)
	maxW, maxH := theme.Sizes.ImageViewerMaxWidth, theme.Sizes.ImageViewerMaxHeight

	if w > maxW {
		h *= maxW / w
		w = maxW
	}
	if h > maxH {
		w *= maxH / h
		h = maxH
	}
	return max(w, theme.Sizes.ImageViewerMinWidth), max(h, theme.Sizes.ImageViewerMinHeight)
}
