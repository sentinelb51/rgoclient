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

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	messageBatchSize  = 100 // messages rendered per UI-thread batch
	renderedCap       = 200 // max message widgets kept mounted at once
	historyPageSize   = 50  // older messages fetched per scroll-up
	initialPageSize   = 30  // messages fetched when first opening a channel
	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"

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
// responsive, scrolling to the bottom when done. Only the newest renderedCap
// messages are mounted; older cached ones are re-mounted on scrollback by
// loadMoreHistory's cache tier. Each call bumps renderGen so the batches of a
// superseded render abort, even when the user switches away and back to the
// same channel before it finishes.
func (a *App) displayMessages(messages []*revoltgo.Message) {
	a.renderGen++
	gen := a.renderGen
	a.messageList.Objects = nil

	if len(messages) > renderedCap {
		messages = messages[len(messages)-renderedCap:]
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
				a.scrollToBottom()
			}
		}, false)
	}()
}

// appendMessage adds a freshly received message, trimming the oldest widget when
// over the render cap and keeping the scroll position stable. prev is the
// message's predecessor in its channel, captured when the message was cached.
func (a *App) appendMessage(message, prev *revoltgo.Message) {
	if a.currentChannelID == "" {
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

	if len(a.messageList.Objects) > renderedCap {
		var removedHeight float32
		if !atBottom {
			removedHeight = a.messageList.Objects[0].MinSize().Height
		}
		a.messageList.Objects = a.messageList.Objects[1:]
		if !atBottom {
			a.messageScroll.Offset.Y = max(a.messageScroll.Offset.Y-removedHeight, 0)
		}
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
	if i := slices.IndexFunc(cached, func(m *revoltgo.Message) bool { return m.ID == top.ID }); i > 0 {
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
}

// scrollToBottom scrolls the message view to the newest message.
func (a *App) scrollToBottom() {
	if a.messageScroll != nil {
		a.messageScroll.ScrollToBottom()
	}
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
