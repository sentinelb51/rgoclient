package app

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/widgets"
)

// Message rendering batch size for responsive UI.
const messageBatchSize = 100

// SelectServer handles server selection and updates the UI.
func (app *ChatApp) SelectServer(serverID string) {
	app.CurrentServerID = serverID
	server := app.CurrentServer()
	if server == nil {
		return
	}

	app.updateServerSelectionUI(serverID)
	app.updateServerHeader(server.Name)

	if len(server.Channels) > 0 {
		app.SelectChannel(server.Channels[0])
	} else {
		app.clearChannelSelection()
	}

	app.RefreshChannelList()
}

// SelectChannel handles channel selection and updates the UI.
func (app *ChatApp) SelectChannel(channelID string) {
	if app.CurrentChannelID == channelID {
		return
	}

	app.CurrentChannelID = channelID
	if ch := app.CurrentChannel(); ch != nil {
		app.updateChannelHeader(ch.Name)
	}
	app.updateChannelSelectionUI(channelID)

	// Display cached messages immediately if available
	if cached := app.Messages.Get(channelID); len(cached) > 0 {
		app.displayMessages(cached)
		return
	}

	app.showLoadingMessages()
	app.loadChannelMessages(channelID)
}

// clearChannelSelection clears the current channel and updates the UI.
func (app *ChatApp) clearChannelSelection() {
	app.CurrentChannelID = ""
	app.refreshMessageList()
	app.updateChannelHeader("")
}

// showLoadingMessages displays a loading placeholder.
func (app *ChatApp) showLoadingMessages() {
	app.messageListContainer.Objects = nil

	label := widget.NewLabelWithStyle("Loading messages...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	label.Importance = widget.MediumImportance

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
			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if app.CurrentChannelID == channelID {
					app.showErrorMessage("Failed to load messages")
				}
			}, true)
			return
		}

		app.Messages.Set(channelID, messages.Messages)

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
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

			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if app.CurrentChannelID != channelID {
					return
				}

				for j := len(batch) - 1; j >= 0; j-- {
					msg := batch[j]
					w := widgets.NewMessageWidget(msg, app.Session,
						nil,
						func(att widgets.MessageAttachment) {
							app.showImageViewer(att)
						},
					)
					app.messageListContainer.Add(w)
				}
				app.messageListContainer.Refresh()
			}, true)
		}

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if app.CurrentChannelID == channelID {
				app.scrollToBottom()
			}
		}, false)
	}()
}
