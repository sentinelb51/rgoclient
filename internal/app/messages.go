package app

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/widgets"
)

// Message rendering batch size for responsive UI.
const messageBatchSize = 10

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
	label.Importance = widget.HighImportance

	app.messageListContainer.Add(container.NewCenter(label))
	app.messageListContainer.Refresh()
}

// loadChannelMessages fetches messages from API in background.
func (app *ChatApp) loadChannelMessages(channelID string) {
	go func() {
		if app.Session == nil {
			return
		}

		msgs, err := app.Session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
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

		// Reverse to oldest-first order
		reverseMessages(msgs.Messages)

		app.Messages.Set(channelID, msgs.Messages)

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if app.CurrentChannelID == channelID {
				app.displayMessages(msgs.Messages)
			}
		}, true)
	}()
}

// reverseMessages reverses the slice in place.
func reverseMessages(msgs []*revoltgo.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
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
		dataList := make([]messageData, 0, len(messages))
		for _, msg := range messages {
			dataList = append(dataList, app.extractMessageData(msg))
		}

		for i := 0; i < len(dataList); i += messageBatchSize {
			end := i + messageBatchSize
			if end > len(dataList) {
				end = len(dataList)
			}
			batch := dataList[i:end]

			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if app.CurrentChannelID != channelID {
					return
				}

				for _, d := range batch {
					atts := convertAttachments(d.attachments)
					w := widgets.NewMessageWidget(d.username, d.content, d.avatarID, d.avatarURL, atts,
						nil,
						func(att widgets.MessageAttachment) {
							app.showImageViewer(att)
						},
					)
					app.messageListContainer.Add(w)
				}
				app.messageListContainer.Refresh()
			}, true)

			time.Sleep(5 * time.Millisecond)
		}

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if app.CurrentChannelID == channelID {
				app.scrollToBottom()
			}
		}, false)
	}()
}

// messageData holds extracted display data for a message.
type messageData struct {
	username    string
	content     string
	avatarID    string
	avatarURL   string
	attachments []attachmentData
}

// attachmentData holds extracted display data for an attachment.
type attachmentData struct {
	id     string
	url    string
	width  int
	height int
}

// extractMessageData extracts display data from a message.
func (app *ChatApp) extractMessageData(msg *revoltgo.Message) messageData {
	atts := extractImageAttachments(msg.Attachments)

	// Webhook message
	if msg.Webhook != nil {
		avatarURL := ""
		if msg.Webhook.Avatar != nil {
			avatarURL = *msg.Webhook.Avatar
		}
		return messageData{
			username:    msg.Webhook.Name,
			content:     msg.Content,
			avatarURL:   avatarURL,
			attachments: atts,
		}
	}

	// System message
	if msg.System != nil {
		return messageData{
			username:    "System",
			content:     formatSystemMessage(msg.System),
			attachments: atts,
		}
	}

	// Regular user message
	username := msg.Author
	avatarID := ""
	avatarURL := ""

	if app.Session != nil && app.Session.State != nil {
		if author := app.Session.State.User(msg.Author); author != nil {
			username = author.Username
			avatarID, avatarURL = widgets.GetAvatarInfo(author)
		}
	}

	return messageData{
		username:    username,
		content:     msg.Content,
		avatarID:    avatarID,
		avatarURL:   avatarURL,
		attachments: atts,
	}
}

// extractImageAttachments extracts image attachments from a message.
func extractImageAttachments(attachments []*revoltgo.Attachment) []attachmentData {
	var result []attachmentData
	for _, att := range attachments {
		if att == nil || att.Metadata == nil {
			continue
		}
		if att.Metadata.Type == revoltgo.AttachmentMetadataTypeImage {
			result = append(result, attachmentData{
				id:     att.ID,
				url:    att.URL(""),
				width:  att.Metadata.Width,
				height: att.Metadata.Height,
			})
		}
	}
	return result
}

// convertAttachments converts internal attachment data to widget format.
func convertAttachments(atts []attachmentData) []widgets.MessageAttachment {
	if len(atts) == 0 {
		return nil
	}
	result := make([]widgets.MessageAttachment, len(atts))
	for i, a := range atts {
		result[i] = widgets.MessageAttachment{
			ID:     a.id,
			URL:    a.url,
			Width:  a.width,
			Height: a.height,
		}
	}
	return result
}

// formatSystemMessage converts a system message to readable text.
func formatSystemMessage(sys *revoltgo.MessageSystem) string {
	switch sys.Type {
	case revoltgo.MessageSystemUserAdded:
		return "A user was added to the group"
	case revoltgo.MessageSystemUserRemove:
		return "A user was removed from the group"
	case revoltgo.MessageSystemUserJoined:
		return "A user joined the server"
	case revoltgo.MessageSystemUserLeft:
		return "A user left the server"
	case revoltgo.MessageSystemUserKicked:
		return "A user was kicked"
	case revoltgo.MessageSystemUserBanned:
		return "A user was banned"
	case revoltgo.MessageSystemChannelRenamed:
		return "Channel was renamed"
	case revoltgo.MessageSystemChannelDescriptionChanged:
		return "Channel description was changed"
	case revoltgo.MessageSystemChannelIconChanged:
		return "Channel icon was changed"
	case revoltgo.MessageSystemChannelOwnershipChanged:
		return "Channel ownership was changed"
	case revoltgo.MessageSystemMessagePinned:
		return "A message was pinned"
	case revoltgo.MessageSystemMessageUnpinned:
		return "A message was unpinned"
	case revoltgo.MessageSystemCallStarted:
		return "A call was started"
	case revoltgo.MessageSystemText:
		return "System message"
	default:
		return "System event"
	}
}
