package app

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/api"
	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
)

// ChatApp encapsulates the state and UI components of the application.
type ChatApp struct {
	fyneApp fyne.App
	window  fyne.Window

	Session *api.Session

	// Data - store IDs, use Session to access actual data (state cache + API fallback)
	ServerIDs        []string
	CurrentServerID  string
	CurrentChannelID string

	// Message cache
	Messages *cache.MessageCache

	// Category collapsed state - maps "serverID:categoryID" to collapsed bool
	collapsedCategories map[string]bool

	// Pending session token to save after Ready event
	pendingSessionToken string

	// UI Components
	serverListContainer  *fyne.Container
	channelListContainer *fyne.Container
	messageListContainer *fyne.Container
	messageScroll        *container.Scroll

	channelHeaderLabel *widget.Label
	serverHeaderLabel  *widget.Label
}

// NewChatApp creates and initializes a new ChatApp instance.
func NewChatApp(fyneApp fyne.App) *ChatApp {
	window := fyneApp.NewWindow("Revoltgo Client")
	window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))

	application := &ChatApp{
		fyneApp:              fyneApp,
		window:               window,
		messageListContainer: container.NewVBox(),
		serverListContainer:  container.NewGridWrap(fyne.NewSize(theme.Sizes.ServerSidebarWidth, theme.Sizes.ServerItemHeight)),
		channelListContainer: container.NewVBox(),
		ServerIDs:            make([]string, 0),
		Messages:             cache.NewMessageCache(100), // Cache up to 100 messages per channel
		collapsedCategories:  make(map[string]bool),
	}

	return application
}

// Window returns the main application window.
func (application *ChatApp) Window() fyne.Window {
	return application.window
}

// CurrentServer returns the current server, or nil if not set.
func (application *ChatApp) CurrentServer() *revoltgo.Server {
	if application.Session == nil || application.CurrentServerID == "" {
		return nil
	}
	return application.Session.Server(application.CurrentServerID)
}

// CurrentChannel returns the current channel, or nil if not set.
func (application *ChatApp) CurrentChannel() *revoltgo.Channel {
	if application.Session == nil || application.CurrentChannelID == "" {
		return nil
	}
	return application.Session.Channel(application.CurrentChannelID)
}

// Run starts the application main loop.
func (application *ChatApp) Run() {
	// Always show login window first
	application.ShowLoginWindow()
	application.window.ShowAndRun()
}

// SwitchToMainUI transitions from login to the main application UI.
func (application *ChatApp) SwitchToMainUI() {
	application.window.SetContent(application.buildUI())
	application.window.Resize(fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight))
	application.window.SetOnClosed(func() {
		cache.GetImageCache().Shutdown()
		if application.Session != nil {
			_ = application.Session.Close()
		}
	})
}

// buildUI constructs the main application layout.
func (application *ChatApp) buildUI() fyne.CanvasObject {
	serverList := application.buildServerList()
	channelList := application.buildChannelList()
	messageBox := application.buildMessageBox()

	// Layout: [serverList | channelList | messageBox]
	content := container.NewBorder(nil, nil, channelList, nil, messageBox)
	return container.NewBorder(nil, nil, serverList, nil, content)
}

// buildServerList creates the server sidebar component.
func (application *ChatApp) buildServerList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ServerListBackground)
	background.SetMinSize(fyne.NewSize(theme.Sizes.ServerSidebarWidth, 0))

	application.RefreshServerList()
	scroll := container.NewVScroll(application.serverListContainer)

	return container.NewStack(background, scroll)
}

// RefreshServerList rebuilds the server list UI from current data.
func (application *ChatApp) RefreshServerList() {
	application.serverListContainer.Objects = nil

	for _, serverID := range application.ServerIDs {
		server := application.Session.Server(serverID)
		if server == nil {
			continue
		}
		capturedServer := server // capture for closure
		capturedServerID := serverID
		serverWidget := widgets.NewServerWidget(capturedServer, func() {
			application.SelectServer(capturedServerID)
		})
		if serverID == application.CurrentServerID {
			serverWidget.SetSelected(true)
		}
		application.serverListContainer.Add(container.NewCenter(serverWidget))
	}

	application.serverListContainer.Refresh()
}

// SelectServer handles server selection and updates the UI accordingly.
func (application *ChatApp) SelectServer(serverID string) {
	application.CurrentServerID = serverID
	server := application.CurrentServer()
	if server == nil {
		return
	}

	application.updateServerSelectionUI(serverID)
	application.updateServerHeader(server.Name)

	// Select first channel or clear selection
	if len(server.Channels) > 0 {
		application.SelectChannel(server.Channels[0])
	} else {
		application.clearChannelSelection()
	}

	application.RefreshChannelList()
}

// updateServerSelectionUI updates the visual selection state of server widgets.
func (application *ChatApp) updateServerSelectionUI(selectedID string) {
	for _, object := range application.serverListContainer.Objects {
		if center, ok := object.(*fyne.Container); ok && len(center.Objects) > 0 {
			if serverWidget, ok := center.Objects[0].(*widgets.ServerWidget); ok {
				serverWidget.SetSelected(serverWidget.Server.ID == selectedID)
			}
		}
	}
}

// clearChannelSelection clears the current channel and updates the UI.
func (application *ChatApp) clearChannelSelection() {
	application.CurrentChannelID = ""
	application.refreshMessageList()
	application.updateChannelHeader("#")
}

// updateServerHeader updates the server header label text.
func (application *ChatApp) updateServerHeader(name string) {
	if application.serverHeaderLabel != nil {
		application.serverHeaderLabel.SetText(name)
	}
}

// updateChannelHeader updates the channel header label text.
func (application *ChatApp) updateChannelHeader(name string) {
	if application.channelHeaderLabel != nil {
		application.channelHeaderLabel.SetText(name)
	}
}

// buildChannelList creates the channel sidebar component.
func (application *ChatApp) buildChannelList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)
	background.SetMinSize(fyne.NewSize(theme.Sizes.ChannelSidebarWidth, 0))

	// Server title header
	serverName := "Server"
	if server := application.CurrentServer(); server != nil {
		serverName = server.Name
	}
	application.serverHeaderLabel = widget.NewLabelWithStyle(serverName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(application.serverHeaderLabel)

	application.RefreshChannelList()
	// Wrap channel list in a scroll container to handle many channels
	channelScroll := container.NewVScroll(application.channelListContainer)

	// Add customizable padding to the channel list content
	padding := theme.Sizes.ChannelSidebarPadding
	paddedScroll := container.NewBorder(
		nil, nil,
		newSpacer(padding, 0), newSpacer(padding, 0),
		channelScroll,
	)
	content := container.NewBorder(header, nil, nil, nil, paddedScroll)
	return container.NewStack(background, content)
}

// newSpacer creates a transparent rectangle with the given minimum size.
func newSpacer(width, height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, height))
	return spacer
}

// RefreshChannelList rebuilds the channel list UI from current server data.
// Channels are organized by categories, with uncategorized channels shown first.
func (application *ChatApp) RefreshChannelList() {
	application.channelListContainer.Objects = nil

	server := application.CurrentServer()
	if server == nil {
		application.channelListContainer.Refresh()
		return
	}

	// Build a set of channel IDs that belong to categories
	categorizedChannels := make(map[string]bool)
	for _, category := range server.Categories {
		for _, channelID := range category.Channels {
			categorizedChannels[channelID] = true
		}
	}

	// First, add uncategorized channels (channels not in any category)
	for _, channelID := range server.Channels {
		if categorizedChannels[channelID] {
			continue
		}
		channel := application.Session.Channel(channelID)
		if channel == nil {
			continue
		}
		capturedChannelID := channelID // capture for closure
		channelWidget := widgets.NewChannelWidget(channel, func() {
			application.SelectChannel(capturedChannelID)
		})
		if capturedChannelID == application.CurrentChannelID {
			channelWidget.SetSelected(true)
		}
		application.channelListContainer.Add(channelWidget)
	}

	// Then, add categories with their channels
	for index, category := range server.Categories {
		categoryKey := server.ID + ":" + category.ID
		collapsed := application.collapsedCategories[categoryKey]
		capturedCategoryKey := categoryKey // capture for closure

		// Create category widget
		categoryWidget := widgets.NewCategoryWidget(category.Title, func(isCollapsed bool) {
			application.collapsedCategories[capturedCategoryKey] = isCollapsed
		})

		// First category doesn't need top spacing
		if index == 0 {
			categoryWidget.SetIsFirstCategory(true)
		}

		// Collect channel widgets for this category
		var channelWidgetsList []fyne.CanvasObject
		for _, channelID := range category.Channels {
			channel := application.Session.Channel(channelID)
			if channel == nil {
				continue
			}
			capturedChannelID := channelID // capture for closure
			channelWidget := widgets.NewChannelWidget(channel, func() {
				application.SelectChannel(capturedChannelID)
			})
			if capturedChannelID == application.CurrentChannelID {
				channelWidget.SetSelected(true)
			}
			channelWidgetsList = append(channelWidgetsList, channelWidget)
		}

		// Add category widget
		application.channelListContainer.Add(categoryWidget)

		// Add channel widgets
		for _, channelWidget := range channelWidgetsList {
			application.channelListContainer.Add(channelWidget)
		}

		// Set up channel widgets in category for collapse functionality
		categoryWidget.SetChannelWidgets(channelWidgetsList, application.channelListContainer)

		// Apply collapsed state if needed
		if collapsed {
			categoryWidget.SetCollapsed(true)
		}
	}

	application.channelListContainer.Refresh()
}

// SelectChannel handles channel selection and updates the UI accordingly.
func (application *ChatApp) SelectChannel(channelID string) {
	// Skip if already on this channel
	if application.CurrentChannelID == channelID {
		return
	}

	application.CurrentChannelID = channelID
	channel := application.CurrentChannel()
	if channel != nil {
		application.updateChannelHeader("#" + channel.Name)
	}
	application.updateChannelSelectionUI(channelID)

	// Check if we have cached messages - show immediately if so
	cachedMessages := application.Messages.Get(channelID)
	if len(cachedMessages) > 0 {
		application.displayMessages(cachedMessages)
		return
	}

	// Show loading state and fetch messages in background
	application.showLoadingMessages()
	application.loadChannelMessages(channelID)
}

// showLoadingMessages displays a "Loading messages..." placeholder in the message area.
func (application *ChatApp) showLoadingMessages() {
	application.messageListContainer.Objects = nil

	loadingLabel := widget.NewLabelWithStyle("Loading messages...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	loadingLabel.Importance = widget.HighImportance

	// Use a spacer to push content to center vertically
	centered := container.NewCenter(loadingLabel)
	application.messageListContainer.Add(centered)
	application.messageListContainer.Refresh()
}

// loadChannelMessages fetches messages for a channel and updates the UI.
func (application *ChatApp) loadChannelMessages(channelID string) {
	// Fetch messages from API in background
	go func() {
		if application.Session == nil {
			return
		}

		messages, err := application.Session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			IncludeUsers: true,
			Limit:        100,
		})

		if err != nil {
			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if application.CurrentChannelID == channelID {
					application.showErrorMessage("Failed to load messages")
				}
			}, true)
			return
		}

		// Messages come in newest-first order, reverse to oldest-first for display
		for i, j := 0, len(messages.Messages)-1; i < j; i, j = i+1, j-1 {
			messages.Messages[i], messages.Messages[j] = messages.Messages[j], messages.Messages[i]
		}

		// Cache the messages
		application.Messages.Set(channelID, messages.Messages)

		// Update UI on main thread
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			// Only update if still on the same channel
			if application.CurrentChannelID == channelID {
				application.displayMessages(messages.Messages)
			}
		}, true)
	}()
}

// showErrorMessage displays an error message in the message area.
func (application *ChatApp) showErrorMessage(message string) {
	application.messageListContainer.Objects = nil

	errorLabel := widget.NewLabel(message)
	errorLabel.Alignment = fyne.TextAlignCenter

	centered := container.NewCenter(errorLabel)
	application.messageListContainer.Add(centered)
	application.messageListContainer.Refresh()
}

// messageDisplayData holds extracted display information for a message.
type messageDisplayData struct {
	username    string
	content     string
	avatarID    string
	avatarURL   string
	attachments []attachmentDisplayData
}

// attachmentDisplayData holds extracted display information for an attachment.
type attachmentDisplayData struct {
	id          string
	url         string
	contentType string
	width       int
	height      int
}

// displayMessages renders messages in the message list container using batched rendering
// to prevent UI freezing when there are many messages.
func (application *ChatApp) displayMessages(messages []*revoltgo.Message) {
	application.messageListContainer.Objects = nil

	// Channel ID to check if we're still on the same channel
	channelID := application.CurrentChannelID

	// Process messages in background to avoid blocking UI
	go func() {
		// Pre-process messages to extract user info (fetch users in background)
		messageDataList := make([]messageDisplayData, 0, len(messages))
		for _, message := range messages {
			data := application.extractMessageData(message)
			messageDataList = append(messageDataList, data)
		}

		// Batch size for rendering - smaller batches = more responsive UI
		const batchSize = 10

		// Render messages in batches
		for i := 0; i < len(messageDataList); i += batchSize {
			end := i + batchSize
			if end > len(messageDataList) {
				end = len(messageDataList)
			}
			batch := messageDataList[i:end]

			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				// Check if we're still on the same channel
				if application.CurrentChannelID != channelID {
					return
				}

				for _, data := range batch {
					messageAttachments := convertToMessageAttachments(data.attachments)
					messageWidget := widgets.NewMessageWidget(data.username, data.content, data.avatarID, data.avatarURL, messageAttachments, func() {
						// TODO: Handle avatar click - show user profile
					}, func(attachment widgets.MessageAttachment) {
						application.showImageViewer(attachment)
					})
					application.messageListContainer.Add(messageWidget)
				}
				application.messageListContainer.Refresh()
			}, true)

			// Small delay between batches to keep UI responsive
			time.Sleep(5 * time.Millisecond)
		}

		// Final scroll to bottom after all messages are rendered
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if application.CurrentChannelID == channelID {
				application.scrollToBottom()
			}
		}, false)
	}()
}

// extractMessageData extracts display data from a message, handling webhooks and system messages.
func (application *ChatApp) extractMessageData(message *revoltgo.Message) messageDisplayData {
	// Extract image attachments
	attachments := extractImageAttachments(message.Attachments)

	// Handle webhook messages
	if message.Webhook != nil {
		avatarURL := ""
		if message.Webhook.Avatar != nil {
			avatarURL = *message.Webhook.Avatar
		}
		return messageDisplayData{
			username:    message.Webhook.Name,
			content:     message.Content,
			avatarID:    "", // Webhook avatars are direct URLs
			avatarURL:   avatarURL,
			attachments: attachments,
		}
	}

	// Handle system messages
	if message.System != nil {
		return messageDisplayData{
			username:    "System",
			content:     formatSystemMessage(message.System),
			avatarID:    "",
			avatarURL:   "",
			attachments: attachments,
		}
	}

	// Regular user message - only use cached user data (no API calls)
	username := message.Author
	avatarID := ""
	avatarURL := ""

	// Only check the state cache, don't make API calls
	if application.Session != nil && application.Session.State != nil {
		if author := application.Session.State.User(message.Author); author != nil {
			username = author.Username
			avatarID, avatarURL = widgets.GetAvatarInfo(author)
		}
	}

	return messageDisplayData{
		username:    username,
		content:     message.Content,
		avatarID:    avatarID,
		avatarURL:   avatarURL,
		attachments: attachments,
	}
}

// extractImageAttachments extracts image attachments from a message.
func extractImageAttachments(attachments []*revoltgo.Attachment) []attachmentDisplayData {
	var result []attachmentDisplayData
	for _, attachment := range attachments {
		if attachment == nil {
			continue
		}
		// Check if it's an image attachment using the revoltgo enum
		if attachment.Metadata != nil && attachment.Metadata.Type == revoltgo.AttachmentMetadataTypeImage {
			result = append(result, attachmentDisplayData{
				id:          attachment.ID,
				url:         attachment.URL(""),
				contentType: attachment.ContentType,
				width:       attachment.Metadata.Width,
				height:      attachment.Metadata.Height,
			})
		}
	}
	return result
}

// convertToMessageAttachments converts attachmentDisplayData slice to MessageAttachment slice.
func convertToMessageAttachments(attachments []attachmentDisplayData) []widgets.MessageAttachment {
	if len(attachments) == 0 {
		return nil
	}
	result := make([]widgets.MessageAttachment, len(attachments))
	for index, attachment := range attachments {
		result[index] = widgets.MessageAttachment{
			ID:     attachment.id,
			URL:    attachment.url,
			Width:  attachment.width,
			Height: attachment.height,
		}
	}
	return result
}

// formatSystemMessage converts a system message to a human-readable string.
func formatSystemMessage(systemMessage *revoltgo.MessageSystem) string {
	switch systemMessage.Type {
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

// updateChannelSelectionUI updates the visual selection state of channel widgets.
func (application *ChatApp) updateChannelSelectionUI(selectedID string) {
	for _, object := range application.channelListContainer.Objects {
		if channelWidget, ok := object.(*widgets.ChannelWidget); ok {
			channelWidget.SetSelected(channelWidget.Channel.ID == selectedID)
		}
	}
}

// buildMessageBox creates the main message area component.
func (application *ChatApp) buildMessageBox() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.MessageAreaBackground)

	application.messageScroll = container.NewVScroll(container.NewPadded(application.messageListContainer))
	application.refreshMessageList()

	// Input field
	input := widget.NewEntry()
	input.SetPlaceHolder("Send a message...")
	input.OnSubmitted = func(text string) {
		application.handleMessageSubmit(text, input)
	}
	inputContainer := container.NewPadded(input)

	// Channel header
	channelName := "#channel"
	if channel := application.CurrentChannel(); channel != nil {
		channelName = "#" + channel.Name
	}
	application.channelHeaderLabel = widget.NewLabelWithStyle(channelName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(application.channelHeaderLabel)

	layout := container.NewBorder(header, inputContainer, nil, nil, application.messageScroll)
	return container.NewStack(background, layout)
}

// handleMessageSubmit processes a submitted message from the input field.
func (application *ChatApp) handleMessageSubmit(text string, input *widget.Entry) {
	if text == "" || application.CurrentChannelID == "" || application.Session == nil {
		return
	}

	if _, err := application.Session.SendMessage(application.CurrentChannelID, text); err != nil {
		fmt.Printf("Failed to send message: %v\n", err)
		return
	}

	input.SetText("")
}

// refreshMessageList rebuilds the message list UI from current channel data.
func (application *ChatApp) refreshMessageList() {
	application.messageListContainer.Objects = nil
	// Messages will be populated by fetching from API or receiving via websocket
	application.messageListContainer.Refresh()
	application.scrollToBottom()
}

// scrollToBottom scrolls the message area to the bottom.
func (application *ChatApp) scrollToBottom() {
	if application.messageScroll != nil {
		application.messageScroll.ScrollToBottom()
	}
}

// AddMessage adds a new message to the current channel and updates the UI.
func (application *ChatApp) AddMessage(username, text string) {
	application.AddMessageWithAvatar(username, text, "", "")
}

// AddMessageWithAvatar adds a new message with avatar to the current channel.
func (application *ChatApp) AddMessageWithAvatar(username, text, avatarID, avatarURL string) {
	if application.CurrentChannelID == "" {
		return
	}

	messageWidget := widgets.NewMessageWidget(username, text, avatarID, avatarURL, nil, func() {
		// TODO: Handle avatar click - show user profile
	}, nil)
	application.messageListContainer.Add(messageWidget)
	application.messageListContainer.Refresh()
	application.scrollToBottom()
}

// SetPendingSessionToken sets a token to be saved after the Ready event.
func (application *ChatApp) SetPendingSessionToken(token string) {
	application.pendingSessionToken = token
}

// GetPendingSessionToken returns the pending session token.
func (application *ChatApp) GetPendingSessionToken() string {
	return application.pendingSessionToken
}

// ClearPendingSessionToken clears the pending session token.
func (application *ChatApp) ClearPendingSessionToken() {
	application.pendingSessionToken = ""
}

// showImageViewer displays an image attachment in a larger popup window.
func (application *ChatApp) showImageViewer(attachment widgets.MessageAttachment) {
	// Create a new window for the image viewer
	viewerWindow := application.fyneApp.NewWindow("Image Viewer")

	// Calculate window size - use image dimensions but cap at reasonable screen size
	maxWidth := float32(1200)
	maxHeight := float32(800)

	width := float32(attachment.Width)
	height := float32(attachment.Height)

	// Scale down if too large
	if width > maxWidth {
		ratio := maxWidth / width
		width = maxWidth
		height = height * ratio
	}
	if height > maxHeight {
		ratio := maxHeight / height
		height = maxHeight
		width = width * ratio
	}

	// Minimum size
	if width < 400 {
		width = 400
	}
	if height < 300 {
		height = 300
	}

	imageSize := fyne.NewSize(width, height)

	// Create placeholder while loading
	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(imageSize)
	imageContainer := container.NewGridWrap(imageSize, placeholder)

	// Load the full-size image
	if attachment.URL != "" && attachment.ID != "" {
		cache.GetImageCache().LoadImageToContainer(attachment.ID, attachment.URL, imageSize, imageContainer, false, nil)
	}

	// Center the image in the window
	content := container.NewCenter(imageContainer)

	viewerWindow.SetContent(content)
	viewerWindow.Resize(fyne.NewSize(width+40, height+40)) // Add some padding
	viewerWindow.CenterOnScreen()
	viewerWindow.Show()
}
