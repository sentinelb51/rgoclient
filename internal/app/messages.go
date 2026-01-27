package app

import (
	"RGOClient/internal/ui/widgets/input"
	"fmt"
	"image"
	"net/url"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
)

// Message rendering batch size for responsive UI.
const messageBatchSize = 100

// showCenteredStatus displays a centered status message in the message area.
func (app *ChatApp) showCenteredStatus(text string) {
	app.messageListContainer.Objects = nil

	label := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Use scroll height to center vertically
	height := float32(400) // Default fallback
	if app.messageScroll != nil {
		h := app.messageScroll.Size().Height
		// Subtract a small buffer to ensure no scrolling due to rounding errors
		h -= 5
		if h > 100 {
			height = h
		}
	}

	app.messageListContainer.Add(widgets.NewMinHeightContainer(height, container.NewCenter(label)))
	app.messageListContainer.Refresh()
}

// showLoadingMessages displays a loading placeholder.
func (app *ChatApp) showLoadingMessages() {
	app.showCenteredStatus("Loading messages...")
}

// loadChannelMessages fetches messages from API in background.
func (app *ChatApp) loadChannelMessages(channelID string) {
	// Reset depleted state on load attempt
	app.Messages.SetDepleted(channelID, false)

	go func() {
		if app.Session == nil {
			return
		}

		// API returns newest → oldest (first element = latest message)
		messages, err := app.Session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			IncludeUsers: true,
			Limit:        100,
		})

		if err != nil {
			app.GoDo(func() {
				if app.CurrentChannelID == channelID {
					app.showErrorMessage("Failed to load messages")
				}
			}, true)
			return
		}

		if len(messages.Messages) == 0 {
			app.GoDo(func() {
				if app.CurrentChannelID == channelID {
					app.showCenteredStatus("No messages in this channel")
					app.Messages.SetDepleted(channelID, true)
				}
			}, true)
			return
		}

		// Store directly - cache maintains newest→oldest order
		app.Messages.Set(channelID, messages.Messages)

		app.GoDo(func() {
			if app.CurrentChannelID == channelID {
				app.displayMessages(messages.Messages)
			}
		}, true)
	}()
}

// showErrorMessage displays an error in the message area.
func (app *ChatApp) showErrorMessage(msg string) {
	app.showCenteredStatus(msg)
}

// displayMessages renders messages using batched rendering.
// Messages are stored oldest→newest, iterate forward.
func (app *ChatApp) displayMessages(messages []*revoltgo.Message) {
	app.messageListContainer.Objects = nil
	channelID := app.CurrentChannelID

	go func() {
		// Iterate forward: oldest→newest (chronological order)
		for i := 0; i < len(messages); i += messageBatchSize {
			end := i + messageBatchSize
			if end > len(messages) {
				end = len(messages)
			}

			// Capture range for closure
			batchStart, batchEnd := i, end

			app.GoDo(func() {
				if app.CurrentChannelID != channelID {
					return
				}

				for j := batchStart; j < batchEnd; j++ {
					w := widgets.NewMessageWidget(messages[j], app)
					app.messageListContainer.Add(w)
				}
				app.messageListContainer.Refresh()
			}, true)
		}

		app.GoDo(func() {
			if app.CurrentChannelID == channelID {
				app.scrollToBottom()
			}
		}, false)
	}()
}

// refreshMessageList rebuilds the message list UI.
func (app *ChatApp) refreshMessageList() {
	app.messageListContainer.Objects = nil
	app.messageListContainer.Refresh()
	app.scrollToBottom()
}

// scrollToBottom scrolls the message area to the bottom.
func (app *ChatApp) scrollToBottom() {
	if app.messageScroll != nil {
		app.messageScroll.ScrollToBottom()
	}
}

// AddMessage adds a new message to the current channel.
func (app *ChatApp) AddMessage(msg *revoltgo.Message) {
	if app.CurrentChannelID == "" {
		return
	}

	w := widgets.NewMessageWidget(msg, app) // Pass app as MessageActions

	// Smart scrolling logic
	contentHeight := app.messageListContainer.MinSize().Height
	viewHeight := app.messageScroll.Size().Height
	offsetY := app.messageScroll.Offset.Y
	// Tolerance for "at bottom"
	isAtBottom := (contentHeight - viewHeight - offsetY) < 100

	app.messageListContainer.Add(w)

	// Prevent UI freeze by limiting rendered widgets
	if len(app.messageListContainer.Objects) > 200 {
		// If we are about to remove the top item, and we are NOT at the bottom (reading history),
		// we need to adjust the scroll offset so the view doesn't jump.
		removedHeight := float32(0)
		if !isAtBottom && len(app.messageListContainer.Objects) > 0 {
			removedHeight = app.messageListContainer.Objects[0].MinSize().Height
		}

		app.messageListContainer.Objects = app.messageListContainer.Objects[1:]

		if !isAtBottom && removedHeight > 0 {
			app.messageScroll.Offset.Y -= removedHeight
			if app.messageScroll.Offset.Y < 0 {
				app.messageScroll.Offset.Y = 0
			}
		}
	}

	app.messageListContainer.Refresh()

	if isAtBottom {
		// Build queue might delay layout, so we might need to defer this or rely on Fyne's layout loop.
		// For now simple ScrollToBottom is usually adequate if called after Refresh.
		app.scrollToBottom()
	} else {
		// If we adjusted offset manually
		app.messageScroll.Refresh()
	}
}

// showImageViewerAttachment displays an image attachment in a popup window.
func (app *ChatApp) showImageViewerAttachment(att *revoltgo.Attachment) {
	window := app.fyneApp.NewWindow(att.Filename)

	// Calculate constrained window size using theme sizes
	maxW := theme.Sizes.ImageViewerMaxWidth
	maxH := theme.Sizes.ImageViewerMaxHeight
	w := float32(att.Metadata.Width)
	h := float32(att.Metadata.Height)

	if w > maxW {
		h = h * (maxW / w)
		w = maxW
	}
	if h > maxH {
		w = w * (maxH / h)
		h = maxH
	}
	if w < theme.Sizes.ImageViewerMinWidth {
		w = theme.Sizes.ImageViewerMinWidth
	}
	if h < theme.Sizes.ImageViewerMinHeight {
		h = theme.Sizes.ImageViewerMinHeight
	}

	// Image container with stacking to allow Proper resizing
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	imgContainer := container.NewStack(placeholder)

	attURL := att.URL("")
	if attURL != "" && att.ID != "" {
		cache.GetImageCache().LoadFromURLAsync(att.ID, attURL, false, func(i image.Image) {
			cImg := canvas.NewImageFromImage(i)
			cImg.FillMode = canvas.ImageFillContain
			imgContainer.Objects = []fyne.CanvasObject{cImg}
			imgContainer.Refresh()
		})
	}

	// Bottom toolbar
	btnBrowser := widget.NewButton("Open in Browser", func() {
		u, err := url.Parse(attURL)
		if err == nil {
			_ = app.fyneApp.OpenURL(u)
		}
	})

	dimsLabel := widget.NewLabel(fmt.Sprintf("%dx%d", att.Metadata.Width, att.Metadata.Height))
	bottomBar := container.NewHBox(
		container.NewPadded(dimsLabel),
		container.NewPadded(btnBrowser),
	)

	content := container.NewBorder(nil, container.NewCenter(bottomBar), nil, nil, imgContainer)
	window.SetContent(content)
	window.Resize(fyne.NewSize(w+40, h+80))
	window.CenterOnScreen()
	window.Show()
}

// handleMessageSubmit processes a submitted message from the input field.
func (app *ChatApp) handleMessageSubmit(text string, msgInput *input.MessageInput) {
	if (text == "" && len(msgInput.Attachments) == 0) || app.CurrentChannelID == "" || app.Session == nil {
		return
	}

	// Capture necessary data to avoid race conditions with UI clearing
	channelID := app.CurrentChannelID
	// Create a copy of attachments as we'll clear the widget immediately
	attachments := make([]input.Attachment, len(msgInput.Attachments))
	copy(attachments, msgInput.Attachments)

	// Copy replies
	replies := make([]input.Reply, len(msgInput.Replies))
	copy(replies, msgInput.Replies)

	// Clear UI immediately for responsiveness
	msgInput.SetText("")
	msgInput.ClearAttachments()
	msgInput.ClearReplies()

	// Perform network operations in background
	go func() {
		attachmentIDs := make([]string, 0, len(attachments))

		for _, att := range attachments {
			f, err := os.Open(att.Path)
			if err != nil {
				fmt.Printf("Failed to open attachment %s: %v\n", att.Path, err)
				continue
			}

			payload := &revoltgo.File{
				Name:   att.Name,
				Reader: f,
			}

			uploaded, err := app.Session.AttachmentUpload(payload)
			_ = f.Close()

			if err != nil {
				fmt.Printf("Failed to upload attachment %s: %v\n", att.Name, err)
				continue
			}

			attachmentIDs = append(attachmentIDs, uploaded.ID)
		}

		msgReplies := make([]*revoltgo.MessageReplies, len(replies))
		for i, r := range replies {
			msgReplies[i] = &revoltgo.MessageReplies{
				ID:      r.ID,
				Mention: r.Mention,
			}
		}

		send := revoltgo.MessageSend{
			Content:     text,
			Attachments: attachmentIDs,
			Replies:     msgReplies,
		}

		if _, err := app.Session.ChannelMessageSend(channelID, send); err != nil {
			fmt.Printf("Failed to send message: %v\n", err)
			return
		}
	}()
}

// loadMoreHistory fetches older messages when scrolling up.
func (app *ChatApp) loadMoreHistory() {
	if app.isLoadingHistory || app.CurrentChannelID == "" || app.Messages.IsDepleted(app.CurrentChannelID) {
		return
	}

	app.isLoadingHistory = true

	// Implicit loading: No visual indicator to avoid flashing
	// The user requested to avoid momentarily showing "Loading messages..."

	go func() {
		// Clean up flag on exit
		defer func() {
			app.GoDo(func() {
				app.isLoadingHistory = false
			}, true)
		}()

		// Get oldest loaded message ID
		msgs := app.Messages.Get(app.CurrentChannelID)
		if len(msgs) == 0 {
			// Should not happen as this is loadMoreHistory.
			// But if it does, it's just a no-op or error
			return
		}
		oldestID := msgs[0].ID

		// Fetch older messages
		// API returns newest->oldest
		history, err := app.Session.ChannelMessages(app.CurrentChannelID, revoltgo.ChannelMessagesParams{
			Before:       oldestID,
			Limit:        50,
			IncludeUsers: true,
		})

		if err != nil || len(history.Messages) == 0 {
			if len(history.Messages) == 0 {
				app.Messages.SetDepleted(app.CurrentChannelID, true)
			}
			// fmt.Println("No more history or error:", err)
			return
		}

		// Update cache
		app.Messages.Prepend(app.CurrentChannelID, history.Messages)

		// Update UI
		app.GoDo(func() {
			// No loader to remove
			app.prependMessagesToUI(history.Messages)
		}, true)
	}()
}

// prependMessagesToUI adds older messages to top of list and maintains scroll position.
func (app *ChatApp) prependMessagesToUI(messages []*revoltgo.Message) {
	if len(messages) == 0 {
		return
	}

	// Capture current content height
	oldHeight := app.messageListContainer.MinSize().Height

	// Convert to widgets (Chronological: reverse API response)
	var newWidgets []fyne.CanvasObject
	for i := len(messages) - 1; i >= 0; i-- {
		w := widgets.NewMessageWidget(messages[i], app)
		newWidgets = append(newWidgets, w)
	}

	// Prepend to objects
	app.messageListContainer.Objects = append(newWidgets, app.messageListContainer.Objects...)
	app.messageListContainer.Refresh()

	// Adjust scroll triggers layout, so we might need to wait or force calculation
	// MinSize should now reflect new content
	newHeight := app.messageListContainer.MinSize().Height
	diff := newHeight - oldHeight

	if diff > 0 {
		app.messageScroll.Offset.Y += diff
		app.messageScroll.Refresh()
	}
}
