package app

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/api"
	"RGOClient/internal/ui/widgets"
)

// StartRevoltSessionWithToken initializes the Revolt session using an existing token.
func (application *ChatApp) StartRevoltSessionWithToken(token string) error {
	session := revoltgo.New(token)
	session.HTTP.Debug = true

	application.Session = api.NewSession(session)
	application.registerEventHandlers(session)

	if err := application.Session.Open(); err != nil {
		return fmt.Errorf("failed to open session: %w", err)
	}

	return nil
}

// StartRevoltSessionWithLogin initializes the Revolt session using email and password.
// Returns the session token on success for storage.
func (application *ChatApp) StartRevoltSessionWithLogin(email, password string) (string, error) {
	loginData := revoltgo.LoginData{
		Email:    email,
		Password: password,
	}

	session, loginResponse, err := revoltgo.NewWithLogin(loginData)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	session.HTTP.Debug = true

	application.Session = api.NewSession(session)
	application.registerEventHandlers(session)

	if err := application.Session.Open(); err != nil {
		return "", fmt.Errorf("failed to open session: %w", err)
	}

	return loginResponse.Token, nil
}

// registerEventHandlers sets up all event handlers for the Revolt session.
func (application *ChatApp) registerEventHandlers(session *revoltgo.Session) {
	revoltgo.AddHandler(session, application.onReady)
	revoltgo.AddHandler(session, application.onMessage)
	revoltgo.AddHandler(session, application.onError)
}

func (application *ChatApp) onError(_ *revoltgo.Session, event *revoltgo.EventError) {
	log.Printf("Received error event: %s\n", event.Error)

	// Handle authentication errors by invalidating token and showing login
	if event.Error == revoltgo.EventErrorTypeInvalidSession ||
		event.Error == revoltgo.EventErrorTypeInternalError {

		// Remove the invalid session if we have user info
		if application.Session != nil && application.Session.State != nil {
			self := application.Session.State.Self()
			if self != nil {
				if err := api.RemoveSession(self.ID); err != nil {
					log.Printf("Failed to remove session: %v\n", err)
				}
			}
		}

		// Close the current session if open
		if application.Session != nil {
			_ = application.Session.Close()
			application.Session = nil
		}

		// Show login screen on the UI thread
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			application.ShowLoginWindow()
		}, true)
	}
}

// onReady handles the EventReady event when the client is connected.
func (application *ChatApp) onReady(_ *revoltgo.Session, event *revoltgo.EventReady) {
	fmt.Printf("Ready: %d user(s) across %d server(s)\n", len(event.Users), len(event.Servers))

	// Save session if we have a pending token from login
	if application.GetPendingSessionToken() != "" {
		self := application.Session.State.Self()
		if self != nil {
			savedSession := api.SavedSession{
				Token:    application.GetPendingSessionToken(),
				UserID:   self.ID,
				Username: self.Username,
			}
			if self.Avatar != nil {
				savedSession.AvatarID = self.Avatar.ID
			}

			if err := api.AddOrUpdateSession(savedSession); err != nil {
				fmt.Printf("Warning: failed to save session: %v\n", err)
			} else {
				fmt.Println("Session saved successfully")
			}
		}
		application.ClearPendingSessionToken()
	}

	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		// Store server IDs - actual data is accessed via Session.State
		application.ServerIDs = make([]string, 0, len(event.Servers))
		for _, server := range event.Servers {
			application.ServerIDs = append(application.ServerIDs, server.ID)
		}

		// Refresh server list first
		application.RefreshServerList()

		// Select first server and channel if available
		if len(application.ServerIDs) > 0 {
			application.CurrentServerID = application.ServerIDs[0]
			application.updateServerSelectionUI(application.CurrentServerID)

			if server := application.CurrentServer(); server != nil {
				application.updateServerHeader(server.Name)
				application.RefreshChannelList()

				// Select first channel (this will show loading state and fetch messages)
				if len(server.Channels) > 0 {
					application.SelectChannel(server.Channels[0])
				}
			}
		}
	}, true)
}

// onMessage handles incoming messages from the Revolt websocket.
func (application *ChatApp) onMessage(_ *revoltgo.Session, event *revoltgo.EventMessage) {
	// Create a Message from the event for caching
	message := &revoltgo.Message{
		ID:      event.ID,
		Channel: event.Channel,
		Author:  event.Author,
		Content: event.Content,
	}

	// Cache the message
	application.Messages.Append(event.Channel, message)

	// Only process if this is the current channel
	if application.CurrentChannelID == "" || event.Channel != application.CurrentChannelID {
		return
	}

	// Resolve user info via Session wrapper (handles cache + API fallback)
	username := event.Author
	avatarID := ""
	avatarURL := ""

	if author := application.Session.User(event.Author); author != nil {
		username = author.Username
		avatarID, avatarURL = widgets.GetAvatarInfo(author)
	}

	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		application.AddMessageWithAvatar(username, event.Content, avatarID, avatarURL)
	}, true)
}
