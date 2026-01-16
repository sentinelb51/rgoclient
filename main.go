package main

import (
	"fmt"
	"image/color"
	"time"

	"github.com/sentinelb51/revoltgo"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ChatApp encapsulates the state and UI components of the application.
type ChatApp struct {
	app    fyne.App
	window fyne.Window

	Session *Session

	// Data - store IDs, use Session to access actual data (state cache + API fallback)
	ServerIDs        []string
	CurrentServerID  string
	CurrentChannelID string

	// Message cache
	Messages *MessageCache

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
func NewChatApp() *ChatApp {
	a := app.New()
	a.Settings().SetTheme(&noScrollTheme{Theme: theme.DefaultTheme()})

	w := a.NewWindow("Revoltgo Client")
	w.Resize(fyne.NewSize(AppSizes.WindowDefaultWidth, AppSizes.WindowDefaultHeight))

	c := &ChatApp{
		app:                  a,
		window:               w,
		messageListContainer: container.NewVBox(),
		serverListContainer:  container.NewGridWrap(fyne.NewSize(AppSizes.ServerSidebarWidth, AppSizes.ServerItemHeight)),
		channelListContainer: container.NewVBox(),
		ServerIDs:            make([]string, 0),
		Messages:             NewMessageCache(100), // Cache up to 100 messages per channel
		collapsedCategories:  make(map[string]bool),
	}

	return c
}

// CurrentServer returns the current server, or nil if not set.
func (c *ChatApp) CurrentServer() *revoltgo.Server {
	if c.Session == nil || c.CurrentServerID == "" {
		return nil
	}
	return c.Session.Server(c.CurrentServerID)
}

// CurrentChannel returns the current channel, or nil if not set.
func (c *ChatApp) CurrentChannel() *revoltgo.Channel {
	if c.Session == nil || c.CurrentChannelID == "" {
		return nil
	}
	return c.Session.Channel(c.CurrentChannelID)
}

// Run starts the application main loop.
func (c *ChatApp) Run() {
	// Always show login window first
	c.showLoginWindow()
	c.window.ShowAndRun()
}

// showLoginWindow displays the login form for user authentication.
func (c *ChatApp) showLoginWindow() {
	c.window.Resize(fyne.NewSize(300, 280))

	// Load saved sessions
	sessions, err := LoadSessions()
	if err != nil {
		fmt.Printf("Error loading sessions: %v\n", err)
		sessions = []SavedSession{}
	}

	// Build saved sessions section
	sessionsSection := c.buildSavedSessionsSection(sessions)

	// Build login form section
	loginSection := c.buildLoginFormSection()

	// Main layout
	content := container.NewVBox(
		widget.NewLabelWithStyle("Authentication", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		sessionsSection,
		widget.NewSeparator(),
		loginSection,
	)

	c.window.SetContent(container.NewPadded(content))
}

// buildSavedSessionsSection creates the UI section showing saved sessions.
func (c *ChatApp) buildSavedSessionsSection(sessions []SavedSession) fyne.CanvasObject {
	if len(sessions) == 0 {
		return widget.NewLabel("No recent sessions")
	}

	// Create a vertical list of session cards
	sessionList := container.NewVBox()
	for _, sess := range sessions {
		sessionList.Add(c.buildSessionCard(sess))
	}

	return container.NewVBox(
		widget.NewLabel("Recent Sessions"),
		sessionList,
	)
}

// buildSessionCard creates a clickable card for a saved session.
func (c *ChatApp) buildSessionCard(session SavedSession) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.RGBA{R: 50, G: 50, B: 50, A: 255})
	bg.CornerRadius = 4

	avatarSize := AppSizes.SessionCardAvatarSize
	placeholder := canvas.NewCircle(AppColors.AvatarPlaceholder)
	avatarContainer := container.NewGridWrap(fyne.NewSize(avatarSize, avatarSize), placeholder)

	if session.AvatarID != "" {
		avatarURL := fmt.Sprintf("https://autumn.revolt.chat/avatars/%s?max_side=64", session.AvatarID)
		GetImageCache().LoadImageToContainer(session.AvatarID, avatarURL, fyne.NewSize(avatarSize, avatarSize), avatarContainer, true, nil)
	}

	username := widget.NewLabel(session.Username)
	username.TextStyle.Bold = true

	xButton := NewXButton(func() {
		_ = RemoveSession(session.UserID)
		c.showLoginWindow()
	})

	// Layout: [avatar] [username stretches] [x button]
	content := container.NewBorder(nil, nil, avatarContainer, xButton, username)

	tappable := NewTappableContainer(content, func() {
		c.loginWithSavedSession(session)
	})

	return container.NewStack(bg, container.NewPadded(tappable))
}

// loginWithSavedSession attempts to login using a saved session token.
func (c *ChatApp) loginWithSavedSession(session SavedSession) {
	fmt.Printf("Attempting login with saved session for: %s\n", session.Username)

	// Show loading state
	c.window.SetContent(container.NewCenter(widget.NewLabel("Logging in...")))

	go func() {
		err := c.StartRevoltSessionWithToken(session.Token)

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if err != nil {
				fmt.Printf("Failed to login with saved session: %v\n", err)
				// Remove invalid session
				_ = RemoveSession(session.UserID)
				dialog.ShowError(fmt.Errorf("session expired, please login again"), c.window)
				c.showLoginWindow()
				return
			}

			// Update session info and switch to main UI
			c.switchToMainUI()
		}, true)
	}()
}

// buildLoginFormSection creates the email/password login form.
func (c *ChatApp) buildLoginFormSection() fyne.CanvasObject {
	emailEntry := widget.NewEntry()
	emailEntry.SetPlaceHolder("Email")

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Password")

	var loginButton *widget.Button
	loginButton = widget.NewButton("Login", func() {
		email := emailEntry.Text
		password := passwordEntry.Text

		if email == "" || password == "" {
			dialog.ShowError(fmt.Errorf("please enter both email and password"), c.window)
			return
		}

		// Disable button while logging in
		loginButton.Disable()
		loginButton.SetText("Logging in...")

		go func() {
			token, err := c.StartRevoltSessionWithLogin(email, password)

			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if err != nil {
					loginButton.Enable()
					loginButton.SetText("Login")
					dialog.ShowError(fmt.Errorf("login failed: %v", err), c.window)
					return
				}

				// Set pending token - session will be saved when Ready event fires
				c.pendingSessionToken = token

				// Switch to main UI
				c.switchToMainUI()
			}, true)
		}()
	})

	// Submit on Enter key in password field
	passwordEntry.OnSubmitted = func(_ string) {
		loginButton.OnTapped()
	}

	// Form with full-width button
	form := container.NewVBox(
		widget.NewLabel("Enter credentials"),
		emailEntry,
		passwordEntry,
		loginButton,
	)

	return form
}

// switchToMainUI transitions from login to the main application UI.
func (c *ChatApp) switchToMainUI() {
	c.window.SetContent(c.buildUI())
	c.window.Resize(fyne.NewSize(AppSizes.WindowDefaultWidth, AppSizes.WindowDefaultHeight))
	c.window.SetOnClosed(func() {
		GetImageCache().Shutdown()
		if c.Session != nil {
			_ = c.Session.Close()
		}
	})
}

// buildUI constructs the main application layout.
func (c *ChatApp) buildUI() fyne.CanvasObject {
	serverList := c.buildServerList()
	channelList := c.buildChannelList()
	messageBox := c.buildMessageBox()

	// Layout: [serverList | channelList | messageBox]
	content := container.NewBorder(nil, nil, channelList, nil, messageBox)
	return container.NewBorder(nil, nil, serverList, nil, content)
}

// buildServerList creates the server sidebar component.
func (c *ChatApp) buildServerList() fyne.CanvasObject {
	bg := canvas.NewRectangle(AppColors.ServerListBackground)
	bg.SetMinSize(fyne.NewSize(AppSizes.ServerSidebarWidth, 0))

	c.refreshServerList()
	scroll := container.NewVScroll(c.serverListContainer)

	return container.NewStack(bg, scroll)
}

// refreshServerList rebuilds the server list UI from current data.
func (c *ChatApp) refreshServerList() {
	c.serverListContainer.Objects = nil

	for _, serverID := range c.ServerIDs {
		srv := c.Session.Server(serverID)
		if srv == nil {
			continue
		}
		srvCopy := srv // capture for closure
		sw := NewServerWidget(srvCopy, func() {
			c.selectServer(serverID)
		})
		if serverID == c.CurrentServerID {
			sw.SetSelected(true)
		}
		c.serverListContainer.Add(container.NewCenter(sw))
	}

	c.serverListContainer.Refresh()
}

// selectServer handles server selection and updates the UI accordingly.
func (c *ChatApp) selectServer(serverID string) {
	c.CurrentServerID = serverID
	srv := c.CurrentServer()
	if srv == nil {
		return
	}

	c.updateServerSelectionUI(serverID)
	c.updateServerHeader(srv.Name)

	// Select first channel or clear selection
	if len(srv.Channels) > 0 {
		c.selectChannel(srv.Channels[0])
	} else {
		c.clearChannelSelection()
	}

	c.refreshChannelList()
}

// updateServerSelectionUI updates the visual selection state of server widgets.
func (c *ChatApp) updateServerSelectionUI(selectedID string) {
	for _, obj := range c.serverListContainer.Objects {
		if center, ok := obj.(*fyne.Container); ok && len(center.Objects) > 0 {
			if sw, ok := center.Objects[0].(*ServerWidget); ok {
				sw.SetSelected(sw.server.ID == selectedID)
			}
		}
	}
}

// clearChannelSelection clears the current channel and updates the UI.
func (c *ChatApp) clearChannelSelection() {
	c.CurrentChannelID = ""
	c.refreshMessageList()
	c.updateChannelHeader("#")
}

// updateServerHeader updates the server header label text.
func (c *ChatApp) updateServerHeader(name string) {
	if c.serverHeaderLabel != nil {
		c.serverHeaderLabel.SetText(name)
	}
}

// updateChannelHeader updates the channel header label text.
func (c *ChatApp) updateChannelHeader(name string) {
	if c.channelHeaderLabel != nil {
		c.channelHeaderLabel.SetText(name)
	}
}

// buildChannelList creates the channel sidebar component.
func (c *ChatApp) buildChannelList() fyne.CanvasObject {
	bg := canvas.NewRectangle(AppColors.ChannelListBackground)
	bg.SetMinSize(fyne.NewSize(AppSizes.ChannelSidebarWidth, 0))

	// Server title header
	serverName := "Server"
	if srv := c.CurrentServer(); srv != nil {
		serverName = srv.Name
	}
	c.serverHeaderLabel = widget.NewLabelWithStyle(serverName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(c.serverHeaderLabel)

	c.refreshChannelList()
	// Wrap channel list in a scroll container to handle many channels
	channelScroll := container.NewVScroll(c.channelListContainer)

	// Add customizable padding to the channel list content
	padding := AppSizes.ChannelSidebarPadding
	paddedScroll := container.NewBorder(
		nil, nil,
		newSpacer(padding, 0), newSpacer(padding, 0),
		channelScroll,
	)
	content := container.NewBorder(header, nil, nil, nil, paddedScroll)
	return container.NewStack(bg, content)
}

// newSpacer creates a transparent rectangle with the given minimum size.
func newSpacer(width, height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, height))
	return spacer
}

// refreshChannelList rebuilds the channel list UI from current server data.
// Channels are organized by categories, with uncategorized channels shown first.
func (c *ChatApp) refreshChannelList() {
	c.channelListContainer.Objects = nil

	srv := c.CurrentServer()
	if srv == nil {
		c.channelListContainer.Refresh()
		return
	}

	// Build a set of channel IDs that belong to categories
	categorizedChannels := make(map[string]bool)
	for _, cat := range srv.Categories {
		for _, chID := range cat.Channels {
			categorizedChannels[chID] = true
		}
	}

	// First, add uncategorized channels (channels not in any category)
	for _, channelID := range srv.Channels {
		if categorizedChannels[channelID] {
			continue
		}
		ch := c.Session.Channel(channelID)
		if ch == nil {
			continue
		}
		chID := channelID // capture for closure
		w := NewChannelWidget(ch, func() {
			c.selectChannel(chID)
		})
		if chID == c.CurrentChannelID {
			w.SetSelected(true)
		}
		c.channelListContainer.Add(w)
	}

	// Then, add categories with their channels
	for i, cat := range srv.Categories {
		categoryKey := srv.ID + ":" + cat.ID
		collapsed := c.collapsedCategories[categoryKey]
		catKey := categoryKey // capture for closure

		// Create category widget
		catWidget := NewCategoryWidget(cat.Title, func(isCollapsed bool) {
			c.collapsedCategories[catKey] = isCollapsed
		})

		// First category doesn't need top spacing
		if i == 0 {
			catWidget.SetIsFirstCategory(true)
		}

		// Collect channel widgets for this category
		var channelWidgets []fyne.CanvasObject
		for _, channelID := range cat.Channels {
			ch := c.Session.Channel(channelID)
			if ch == nil {
				continue
			}
			chID := channelID // capture for closure
			w := NewChannelWidget(ch, func() {
				c.selectChannel(chID)
			})
			if chID == c.CurrentChannelID {
				w.SetSelected(true)
			}
			channelWidgets = append(channelWidgets, w)
		}

		// Add category widget
		c.channelListContainer.Add(catWidget)

		// Add channel widgets
		for _, w := range channelWidgets {
			c.channelListContainer.Add(w)
		}

		// Set up channel widgets in category for collapse functionality
		catWidget.SetChannelWidgets(channelWidgets, c.channelListContainer)

		// Apply collapsed state if needed
		if collapsed {
			catWidget.SetCollapsed(true)
		}
	}

	c.channelListContainer.Refresh()
}

// selectChannel handles channel selection and updates the UI accordingly.
func (c *ChatApp) selectChannel(channelID string) {
	// Skip if already on this channel
	if c.CurrentChannelID == channelID {
		return
	}

	c.CurrentChannelID = channelID
	ch := c.CurrentChannel()
	if ch != nil {
		c.updateChannelHeader("#" + ch.Name)
	}
	c.updateChannelSelectionUI(channelID)

	// Check if we have cached messages - show immediately if so
	cached := c.Messages.Get(channelID)
	if len(cached) > 0 {
		c.displayMessages(cached)
		return
	}

	// Show loading state and fetch messages in background
	c.showLoadingMessages()
	c.loadChannelMessages(channelID)
}

// showLoadingMessages displays a "Loading messages..." placeholder in the message area.
func (c *ChatApp) showLoadingMessages() {
	c.messageListContainer.Objects = nil

	loadingLabel := widget.NewLabelWithStyle("Loading messages...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	loadingLabel.Importance = widget.HighImportance

	// Use a spacer to push content to center vertically
	centered := container.NewCenter(loadingLabel)
	c.messageListContainer.Add(centered)
	c.messageListContainer.Refresh()
}

// loadChannelMessages fetches messages for a channel and updates the UI.
func (c *ChatApp) loadChannelMessages(channelID string) {
	// Fetch messages from API in background
	go func() {
		if c.Session == nil {
			return
		}

		messages, err := c.Session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
			IncludeUsers: true,
			Limit:        100,
		})

		if err != nil {
			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if c.CurrentChannelID == channelID {
					c.showErrorMessage("Failed to load messages")
				}
			}, true)
			return
		}

		// Messages come in newest-first order, reverse to oldest-first for display
		for i, j := 0, len(messages.Messages)-1; i < j; i, j = i+1, j-1 {
			messages.Messages[i], messages.Messages[j] = messages.Messages[j], messages.Messages[i]
		}

		// Cache the messages
		c.Messages.Set(channelID, messages.Messages)

		// Update UI on main thread
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			// Only update if still on the same channel
			if c.CurrentChannelID == channelID {
				c.displayMessages(messages.Messages)
			}
		}, true)
	}()
}

// showErrorMessage displays an error message in the message area.
func (c *ChatApp) showErrorMessage(msg string) {
	c.messageListContainer.Objects = nil

	errorLabel := widget.NewLabel(msg)
	errorLabel.Alignment = fyne.TextAlignCenter

	centered := container.NewCenter(errorLabel)
	c.messageListContainer.Add(centered)
	c.messageListContainer.Refresh()
}

// displayMessages renders messages in the message list container using batched rendering
// to prevent UI freezing when there are many messages.
func (c *ChatApp) displayMessages(messages []*revoltgo.Message) {
	c.messageListContainer.Objects = nil

	// Channel ID to check if we're still on the same channel
	channelID := c.CurrentChannelID

	// Process messages in background to avoid blocking UI
	go func() {
		// Pre-process messages to extract user info (fetch users in background)
		msgDataList := make([]messageDisplayData, 0, len(messages))
		for _, msg := range messages {
			data := c.extractMessageData(msg)
			msgDataList = append(msgDataList, data)
		}

		// Batch size for rendering - smaller batches = more responsive UI
		const batchSize = 10

		// Render messages in batches
		for i := 0; i < len(msgDataList); i += batchSize {
			end := i + batchSize
			if end > len(msgDataList) {
				end = len(msgDataList)
			}
			batch := msgDataList[i:end]

			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				// Check if we're still on the same channel
				if c.CurrentChannelID != channelID {
					return
				}

				for _, data := range batch {
					attachments := convertToMessageAttachments(data.attachments)
					msgWidget := NewMessageWidget(data.username, data.content, data.avatarID, data.avatarURL, attachments)
					c.messageListContainer.Add(msgWidget)
				}
				c.messageListContainer.Refresh()
			}, true)

			// Small delay between batches to keep UI responsive
			time.Sleep(5 * time.Millisecond)
		}

		// Final scroll to bottom after all messages are rendered
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if c.CurrentChannelID == channelID {
				c.scrollToBottom()
			}
		}, false)
	}()
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

// extractMessageData extracts display data from a message, handling webhooks and system messages.
func (c *ChatApp) extractMessageData(msg *revoltgo.Message) messageDisplayData {
	// Extract image attachments
	attachments := extractImageAttachments(msg.Attachments)

	// Handle webhook messages
	if msg.Webhook != nil {
		avatarURL := ""
		if msg.Webhook.Avatar != nil {
			avatarURL = *msg.Webhook.Avatar
		}
		return messageDisplayData{
			username:    msg.Webhook.Name,
			content:     msg.Content,
			avatarID:    "", // Webhook avatars are direct URLs
			avatarURL:   avatarURL,
			attachments: attachments,
		}
	}

	// Handle system messages
	if msg.System != nil {
		return messageDisplayData{
			username:    "System",
			content:     c.formatSystemMessage(msg.System),
			avatarID:    "",
			avatarURL:   "",
			attachments: attachments,
		}
	}

	// Regular user message - only use cached user data (no API calls)
	username := msg.Author
	avatarID := ""
	avatarURL := ""

	// Only check the state cache, don't make API calls
	if c.Session != nil && c.Session.State != nil {
		if author := c.Session.State.User(msg.Author); author != nil {
			username = author.Username
			avatarID, avatarURL = getAvatarInfo(author)
		}
	}

	return messageDisplayData{
		username:    username,
		content:     msg.Content,
		avatarID:    avatarID,
		avatarURL:   avatarURL,
		attachments: attachments,
	}
}

// extractImageAttachments extracts image attachments from a message.
func extractImageAttachments(attachments []*revoltgo.Attachment) []attachmentDisplayData {
	var result []attachmentDisplayData
	for _, att := range attachments {
		if att == nil {
			continue
		}
		// Check if it's an image attachment
		if att.Metadata != nil && att.Metadata.Type == "Image" {
			result = append(result, attachmentDisplayData{
				id:          att.ID,
				url:         att.URL(""),
				contentType: att.ContentType,
				width:       att.Metadata.Width,
				height:      att.Metadata.Height,
			})
		}
	}
	return result
}

// convertToMessageAttachments converts attachmentDisplayData slice to MessageAttachment slice.
func convertToMessageAttachments(attachments []attachmentDisplayData) []MessageAttachment {
	if len(attachments) == 0 {
		return nil
	}
	result := make([]MessageAttachment, len(attachments))
	for i, att := range attachments {
		result[i] = MessageAttachment{
			ID:     att.id,
			URL:    att.url,
			Width:  att.width,
			Height: att.height,
		}
	}
	return result
}

// formatSystemMessage converts a system message to a human-readable string.
func (c *ChatApp) formatSystemMessage(sys *revoltgo.MessageSystem) string {
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

// updateChannelSelectionUI updates the visual selection state of channel widgets.
func (c *ChatApp) updateChannelSelectionUI(selectedID string) {
	for _, obj := range c.channelListContainer.Objects {
		if cw, ok := obj.(*ChannelWidget); ok {
			cw.SetSelected(cw.channel.ID == selectedID)
		}
	}
}

// buildMessageBox creates the main message area component.
func (c *ChatApp) buildMessageBox() fyne.CanvasObject {
	bg := canvas.NewRectangle(AppColors.MessageAreaBackground)

	c.messageScroll = container.NewVScroll(container.NewPadded(c.messageListContainer))
	c.refreshMessageList()

	// Input field
	input := widget.NewEntry()
	input.SetPlaceHolder("Send a message...")
	input.OnSubmitted = func(text string) {
		c.handleMessageSubmit(text, input)
	}
	inputContainer := container.NewPadded(input)

	// Channel header
	channelName := "#channel"
	if ch := c.CurrentChannel(); ch != nil {
		channelName = "#" + ch.Name
	}
	c.channelHeaderLabel = widget.NewLabelWithStyle(channelName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	header := container.NewPadded(c.channelHeaderLabel)

	layout := container.NewBorder(header, inputContainer, nil, nil, c.messageScroll)
	return container.NewStack(bg, layout)
}

// handleMessageSubmit processes a submitted message from the input field.
func (c *ChatApp) handleMessageSubmit(text string, input *widget.Entry) {
	if text == "" || c.CurrentChannelID == "" || c.Session == nil {
		return
	}

	if _, err := c.Session.SendMessage(c.CurrentChannelID, text); err != nil {
		fmt.Printf("Failed to send message: %v\n", err)
		return
	}

	input.SetText("")
}

// refreshMessageList rebuilds the message list UI from current channel data.
func (c *ChatApp) refreshMessageList() {
	c.messageListContainer.Objects = nil
	// Messages will be populated by fetching from API or receiving via websocket
	c.messageListContainer.Refresh()
	c.scrollToBottom()
}

// scrollToBottom scrolls the message area to the bottom.
func (c *ChatApp) scrollToBottom() {
	if c.messageScroll != nil {
		c.messageScroll.ScrollToBottom()
	}
}

// addMessage adds a new message to the current channel and updates the UI.
func (c *ChatApp) addMessage(username, text string) {
	c.addMessageWithAvatar(username, text, "", "")
}

// addMessageWithAvatar adds a new message with avatar to the current channel.
func (c *ChatApp) addMessageWithAvatar(username, text, avatarID, avatarURL string) {
	if c.CurrentChannelID == "" {
		return
	}

	msgWidget := NewMessageWidget(username, text, avatarID, avatarURL, nil)
	c.messageListContainer.Add(msgWidget)
	c.messageListContainer.Refresh()
	c.scrollToBottom()
}

func main() {
	chatApp := NewChatApp()
	chatApp.Run()
}
