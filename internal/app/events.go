package app

import (
	"fmt"
	"log"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/api"
)

// StartRevoltSessionWithToken initializes the session using an existing token.
func (app *ChatApp) StartRevoltSessionWithToken(token string) error {
	session := revoltgo.New(token)
	session.HTTP.Debug = true

	app.Session = api.NewSession(session)
	app.registerEventHandlers(session)

	if err := app.Session.Open(); err != nil {
		return fmt.Errorf("failed to open session: %w", err)
	}
	return nil
}

// StartRevoltSessionWithLogin initializes the session using credentials.
// Returns the session token on success.
func (app *ChatApp) StartRevoltSessionWithLogin(email, password string) (string, error) {
	loginData := revoltgo.LoginData{
		Email:    email,
		Password: password,
	}

	session, resp, err := revoltgo.NewWithLogin(loginData)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	session.HTTP.Debug = true

	app.Session = api.NewSession(session)
	app.registerEventHandlers(session)

	if err := app.Session.Open(); err != nil {
		return "", fmt.Errorf("failed to open session: %w", err)
	}

	return resp.Token, nil
}

// registerEventHandlers sets up event handlers for the session.
func (app *ChatApp) registerEventHandlers(session *revoltgo.Session) {
	revoltgo.AddHandler(session, app.onReady)
	revoltgo.AddHandler(session, app.onMessage)
	revoltgo.AddHandler(session, app.onError)
}

// onError handles error events from the websocket.
func (app *ChatApp) onError(_ *revoltgo.Session, event *revoltgo.EventError) {
	log.Printf("Error event: %s\n", event.Error)

	if event.Error == revoltgo.EventErrorTypeInvalidSession ||
		event.Error == revoltgo.EventErrorTypeInternalError {

		// Remove invalid session
		if app.Session != nil && app.Session.State != nil {
			if self := app.Session.State.Self(); self != nil {
				if err := api.RemoveSession(self.ID); err != nil {
					log.Printf("Failed to remove session: %v\n", err)
				}
			}
		}

		// Close session and show login
		if app.Session != nil {
			_ = app.Session.Close()
			app.Session = nil
		}

		app.GoDo(func() {
			app.ShowLoginWindow()
		}, true)
	}
}

// onReady handles the Ready event when connected.
func (app *ChatApp) onReady(_ *revoltgo.Session, event *revoltgo.EventReady) {
	fmt.Printf("Ready: %d user(s), %d server(s)\n", len(event.Users), len(event.Servers))

	// Save pending session token
	if token := app.GetPendingSessionToken(); token != "" {
		if self := app.Session.State.Self(); self != nil {
			saved := api.SavedSession{
				Token:    token,
				UserID:   self.ID,
				Username: self.Username,
			}
			if self.Avatar != nil {
				saved.AvatarID = self.Avatar.ID
			}

			if err := api.AddOrUpdateSession(saved); err != nil {
				fmt.Printf("Warning: failed to save session: %v\n", err)
			}
		}
		app.ClearPendingSessionToken()
	}

	app.GoDo(func() {
		// Store server IDs
		app.ServerIDs = make([]string, 0, len(event.Servers))
		for _, server := range event.Servers {
			app.ServerIDs = append(app.ServerIDs, server.ID)
		}

		app.RefreshServerList()

		// Select first server and channel
		if len(app.ServerIDs) > 0 {
			app.CurrentServerID = app.ServerIDs[0]
			app.updateServerSelectionUI(app.CurrentServerID)

			if server := app.CurrentServer(); server != nil {
				app.updateServerHeader(server.Name)
				app.RefreshChannelList()

				if len(server.Channels) > 0 {
					app.SelectChannel(server.Channels[0])
				}
			}
		}
	}, true)
}

// onMessage handles incoming messages from the websocket.
func (app *ChatApp) onMessage(_ *revoltgo.Session, event *revoltgo.EventMessage) {
	app.Messages.Append(event.Channel, &event.Message)

	if event.Channel != app.CurrentChannelID {
		return
	}

	app.GoDo(func() {
		app.AddMessage(&event.Message)
	}, true)
}
