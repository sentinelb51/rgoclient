package main

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"github.com/sentinelb51/revoltgo"
)

// StartRevoltSessionWithToken initializes the Revolt session using an existing token.
func (c *ChatApp) StartRevoltSessionWithToken(token string) error {
	session := revoltgo.New(token)
	session.HTTP.Debug = true

	c.Session = NewSession(session)
	c.registerEventHandlers(session)

	if err := c.Session.Open(); err != nil {
		return fmt.Errorf("failed to open session: %w", err)
	}

	return nil
}

// StartRevoltSessionWithLogin initializes the Revolt session using email and password.
// Returns the session token on success for storage.
func (c *ChatApp) StartRevoltSessionWithLogin(email, password string) (string, error) {
	data := revoltgo.LoginData{
		Email:    email,
		Password: password,
	}

	session, loginResp, err := revoltgo.NewWithLogin(data)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	session.HTTP.Debug = true

	c.Session = NewSession(session)
	c.registerEventHandlers(session)

	if err := c.Session.Open(); err != nil {
		return "", fmt.Errorf("failed to open session: %w", err)
	}

	return loginResp.Token, nil
}

// registerEventHandlers sets up all event handlers for the Revolt session.
func (c *ChatApp) registerEventHandlers(session *revoltgo.Session) {
	revoltgo.AddHandler(session, c.onReady)
	revoltgo.AddHandler(session, c.onMessage)
	revoltgo.AddHandler(session, c.onError)
}

func (c *ChatApp) onError(_ *revoltgo.Session, e *revoltgo.EventError) {
	log.Printf("Received error event: %s\n", e.Error)

	// Handle authentication errors by invalidating token and showing login
	if e.Error == revoltgo.EventErrorTypeInvalidSession ||
		e.Error == revoltgo.EventErrorTypeInternalError {

		// Remove the invalid session if we have user info
		if c.Session != nil && c.Session.State != nil {
			self := c.Session.State.Self()
			if self != nil {
				if err := RemoveSession(self.ID); err != nil {
					log.Printf("Failed to remove session: %v\n", err)
				}
			}
		}

		// Close the current session if open
		if c.Session != nil {
			_ = c.Session.Close()
			c.Session = nil
		}

		// Show login screen on the UI thread
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			c.showLoginWindow()
		}, true)
	}
}

// onReady handles the EventReady event when the client is connected.
func (c *ChatApp) onReady(_ *revoltgo.Session, e *revoltgo.EventReady) {
	fmt.Printf("Ready: %d user(s) across %d server(s)\n", len(e.Users), len(e.Servers))

	// Save session if we have a pending token from login
	if c.pendingSessionToken != "" {
		self := c.Session.State.Self()
		if self != nil {
			savedSession := SavedSession{
				Token:    c.pendingSessionToken,
				UserID:   self.ID,
				Username: self.Username,
			}
			if self.Avatar != nil {
				savedSession.AvatarID = self.Avatar.ID
			}

			if err := AddOrUpdateSession(savedSession); err != nil {
				fmt.Printf("Warning: failed to save session: %v\n", err)
			} else {
				fmt.Println("Session saved successfully")
			}
		}
		c.pendingSessionToken = ""
	}

	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		// Store server IDs - actual data is accessed via Session.State
		c.ServerIDs = make([]string, 0, len(e.Servers))
		for _, server := range e.Servers {
			c.ServerIDs = append(c.ServerIDs, server.ID)
		}

		// Refresh server list first
		c.refreshServerList()

		// Select first server and channel if available
		if len(c.ServerIDs) > 0 {
			c.CurrentServerID = c.ServerIDs[0]
			c.updateServerSelectionUI(c.CurrentServerID)

			if srv := c.CurrentServer(); srv != nil {
				c.updateServerHeader(srv.Name)
				c.refreshChannelList()

				// Select first channel (this will show loading state and fetch messages)
				if len(srv.Channels) > 0 {
					c.selectChannel(srv.Channels[0])
				}
			}
		}
	}, true)
}

// onMessage handles incoming messages from the Revolt websocket.
func (c *ChatApp) onMessage(s *revoltgo.Session, event *revoltgo.EventMessage) {
	// Create a Message from the event for caching
	msg := &revoltgo.Message{
		ID:      event.ID,
		Channel: event.Channel,
		Author:  event.Author,
		Content: event.Content,
	}

	// Cache the message
	c.Messages.Append(event.Channel, msg)

	// Only process if this is the current channel
	if c.CurrentChannelID == "" || event.Channel != c.CurrentChannelID {
		return
	}

	// Resolve user info via Session wrapper (handles cache + API fallback)
	username := event.Author
	avatarID := ""
	avatarURL := ""

	if author := c.Session.User(event.Author); author != nil {
		username = author.Username
		avatarID, avatarURL = getAvatarInfo(author)
	}

	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		c.addMessageWithAvatar(username, event.Content, avatarID, avatarURL)
	}, true)
}
