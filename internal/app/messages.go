package app

import (
	"fmt"
	"image"
	"log"
	"net/url"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

const (
	messageBatchSize  = 100 // messages rendered per UI-thread batch
	renderedCap       = 200 // max message widgets kept mounted at once
	historyPageSize   = 50  // older messages fetched per scroll-up
	initialPageSize   = 100 // messages fetched when first opening a channel
	atBottomTolerance = 100 // px from the bottom still counted as "at bottom"
)

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

	// Flat composer: the entry is a full-width bar flush with the window edges,
	// divided from the message list by a hairline seam rather than a soft shadow.
	seam := canvas.NewRectangle(theme.Colors.ChannelSelectedBg)
	seam.SetMinSize(fyne.NewSize(0, 1))
	composer := ui.VBoxNoSpacing(
		seam,
		a.input.ReplyContainer,
		a.input.AttachmentContainer,
		a.input,
	)

	a.channelHeader = widget.NewLabelWithStyle(a.channelName(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(container.NewHBox(ui.HashtagIcon(), a.channelHeader))

	layout := container.NewBorder(header, composer, nil, nil, a.messageScroll)
	return container.NewStack(background, layout)
}

// showStatus replaces the message list with a single centered line.
func (a *App) showStatus(text string) {
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
	a.messageList.Objects = nil
	a.messageList.Refresh()
	a.scrollToBottom()
}

// loadChannelMessages fetches the newest page of messages for a channel.
func (a *App) loadChannelMessages(channelID string) {
	a.messageCache.SetDepleted(channelID, false)

	go func() {
		if a.session == nil {
			return
		}

		page, err := a.session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
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
// responsive, scrolling to the bottom when done.
func (a *App) displayMessages(messages []*revoltgo.Message) {
	a.messageList.Objects = nil
	channelID := a.currentChannelID
	deps := a.deps()

	go func() {
		for start := 0; start < len(messages); start += messageBatchSize {
			end := min(start+messageBatchSize, len(messages))
			batch := messages[start:end]

			a.doOnUI(func() {
				if a.currentChannelID != channelID {
					return
				}
				for _, message := range batch {
					a.messageList.Add(ui.NewMessageWidget(deps, message))
				}
				a.messageList.Refresh()
			}, true)
		}

		a.doOnUI(func() {
			if a.currentChannelID == channelID {
				a.scrollToBottom()
			}
		}, false)
	}()
}

// appendMessage adds a freshly received message, trimming the oldest widget when
// over the render cap and keeping the scroll position stable.
func (a *App) appendMessage(message *revoltgo.Message) {
	if a.currentChannelID == "" {
		return
	}

	contentHeight := a.messageList.MinSize().Height
	viewHeight := a.messageScroll.Size().Height
	atBottom := contentHeight-viewHeight-a.messageScroll.Offset.Y < atBottomTolerance

	a.messageList.Add(ui.NewMessageWidget(a.deps(), message))

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

// loadMoreHistory fetches an older page when the user scrolls to the top.
func (a *App) loadMoreHistory() {
	channelID := a.currentChannelID
	if a.loadingHistory || channelID == "" || a.messageCache.IsDepleted(channelID) {
		return
	}
	a.loadingHistory = true

	go func() {
		defer a.doOnUI(func() { a.loadingHistory = false }, true)

		current := a.messageCache.Get(channelID)
		if len(current) == 0 {
			return
		}
		oldestID := current[0].ID

		page, err := a.session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			Before:       oldestID,
			Limit:        historyPageSize,
			IncludeUsers: true,
		})
		if err != nil || len(page.Messages) == 0 {
			if err == nil {
				a.messageCache.SetDepleted(channelID, true)
			}
			return
		}

		a.messageCache.Prepend(channelID, page.Messages)
		a.doOnUI(func() {
			if a.currentChannelID == channelID {
				a.prependMessages(page.Messages)
			}
		}, true)
	}()
}

// prependMessages mounts an older page (API order, newest first) above the
// current view, preserving the user's scroll position.
func (a *App) prependMessages(page []*revoltgo.Message) {
	if len(page) == 0 {
		return
	}
	deps := a.deps()
	oldHeight := a.messageList.MinSize().Height

	widgets := make([]fyne.CanvasObject, 0, len(page))
	for i := len(page) - 1; i >= 0; i-- { // reverse to chronological order
		widgets = append(widgets, ui.NewMessageWidget(deps, page[i]))
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

	channelID := a.currentChannelID
	attachments := append([]ui.Attachment(nil), a.input.Attachments...)
	replies := append([]ui.Reply(nil), a.input.Replies...)

	a.input.SetText("")
	a.input.ClearAttachments()
	a.input.ClearReplies()

	go func() {
		send := revoltgo.MessageSend{
			Content:     text,
			Attachments: a.uploadAttachments(attachments),
			Replies:     toMessageReplies(replies),
		}
		if _, err := a.session.ChannelMessageSend(channelID, send); err != nil {
			log.Printf("send message: %v", err)
		}
	}()
}

// uploadAttachments uploads each local file and returns the resulting IDs.
func (a *App) uploadAttachments(attachments []ui.Attachment) []string {
	ids := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		file, err := os.Open(attachment.Path)
		if err != nil {
			log.Printf("open attachment %s: %v", attachment.Path, err)
			continue
		}

		uploaded, err := a.session.AttachmentUpload(&revoltgo.File{Name: attachment.Name, Reader: file})
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
