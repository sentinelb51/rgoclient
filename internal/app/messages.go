package app

import (
	"fmt"
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

// showLoadingMessages displays a loading placeholder.
func (app *ChatApp) showLoadingMessages() {
	app.messageListContainer.Objects = nil

	label := widget.NewLabelWithStyle("Loading messages...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	app.messageListContainer.Add(container.NewCenter(label))
	app.messageListContainer.Refresh()
}

// loadChannelMessages fetches messages from API in background.
func (app *ChatApp) loadChannelMessages(channelID string) {
	go func() {
		if app.Session == nil {
			return
		}

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
	app.messageListContainer.Objects = nil

	label := widget.NewLabel(msg)
	label.Alignment = fyne.TextAlignCenter

	app.messageListContainer.Add(container.NewCenter(label))
	app.messageListContainer.Refresh()
}

// displayMessages renders messages using batched rendering.
func (app *ChatApp) displayMessages(messages []*revoltgo.Message) {
	app.messageListContainer.Objects = nil
	channelID := app.CurrentChannelID

	go func() {
		for i := len(messages); i > 0; i -= messageBatchSize {
			start := i - messageBatchSize
			if start < 0 {
				start = 0
			}
			batch := messages[start:i]

			app.GoDo(func() {
				if app.CurrentChannelID != channelID {
					return
				}

				for j := len(batch) - 1; j >= 0; j-- {
					msg := batch[j]
					w := widgets.NewMessageWidget(msg, app.Session,
						nil,
						func(att *revoltgo.Attachment) {
							app.showImageViewerAttachment(att)
						},
					)
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

	w := widgets.NewMessageWidget(msg, app.Session, nil, func(attachment *revoltgo.Attachment) {
		app.showImageViewerAttachment(attachment)
	})
	app.messageListContainer.Add(w)
	app.messageListContainer.Refresh()
	app.scrollToBottom()
}

// showImageViewerAttachment displays an image attachment in a popup window.
func (app *ChatApp) showImageViewerAttachment(att *revoltgo.Attachment) {
	window := app.fyneApp.NewWindow("Image Viewer")

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

	size := fyne.NewSize(w, h)

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	imgContainer := container.NewGridWrap(size, placeholder)

	url := att.URL("")
	if url != "" && att.ID != "" {
		cache.GetImageCache().LoadImageToContainer(att.ID, url, size, imgContainer, false, nil)
	}

	content := container.NewCenter(imgContainer)
	window.SetContent(content)
	window.Resize(fyne.NewSize(w+40, h+40))
	window.CenterOnScreen()
	window.Show()
}

// handleMessageSubmit processes a submitted message from the input field.
func (app *ChatApp) handleMessageSubmit(text string, input *widgets.MessageInput) {
	if (text == "" && len(input.Attachments) == 0) || app.CurrentChannelID == "" || app.Session == nil {
		return
	}

	// Capture necessary data to avoid race conditions with UI clearing
	channelID := app.CurrentChannelID
	// Create a copy of attachments as we'll clear the widget immediately
	attachments := make([]widgets.Attachment, len(input.Attachments))
	copy(attachments, input.Attachments)

	// Clear UI immediately for responsiveness
	input.SetText("")
	input.ClearAttachments()

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

		send := revoltgo.MessageSend{
			Content:     text,
			Attachments: attachmentIDs,
		}

		if _, err := app.Session.ChannelMessageSend(channelID, send); err != nil {
			fmt.Printf("Failed to send message: %v\n", err)
			return
		}
	}()
}
