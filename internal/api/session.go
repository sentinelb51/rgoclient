package api

import (
	"fmt"

	"github.com/sentinelb51/revoltgo"
)

// Session wraps revoltgo.Session and provides convenient methods
// that check the state cache first before falling back to API calls.
type Session struct {
	*revoltgo.Session
}

// NewSession creates a new Session wrapper around a revoltgo.Session.
func NewSession(session *revoltgo.Session) *Session {
	return &Session{Session: session}
}

// Server returns a server by ID, checking state cache first, then API.
func (session *Session) Server(id string) *revoltgo.Server {
	if session.Session == nil || id == "" {
		return nil
	}

	server := session.State.Server(id)
	if server == nil {
		fmt.Println("Server not found in state, fetching from API:", id)
		if fetched, err := session.Session.Server(id); err == nil {
			server = fetched
		}
	}

	return server
}

// Channel returns a channel by ID from state cache.
func (session *Session) Channel(id string) *revoltgo.Channel {
	if session.Session == nil || id == "" {
		return nil
	}

	// Don't check API for channels; if we don't have it, it'll be a permission error
	return session.State.Channel(id)
}

// User returns a user by ID, checking state cache first, then API.
func (session *Session) User(id string) *revoltgo.User {
	if session.Session == nil || id == "" {
		return nil
	}

	user := session.State.User(id)
	if user == nil {
		if fetched, err := session.Session.User(id); err == nil {
			user = fetched
		}
	}

	return user
}

// Self returns the current authenticated user from state.
func (session *Session) Self() *revoltgo.User {
	if session.Session == nil {
		return nil
	}
	return session.State.Self()
}

// Open opens the websocket connection.
func (session *Session) Open() error {
	if session.Session == nil {
		return fmt.Errorf("session is nil")
	}
	return session.Session.Open()
}

// Close gracefully closes the websocket connection.
func (session *Session) Close() error {
	if session.Session == nil || session.WS == nil {
		return nil
	}
	return session.WS.WriteClose()
}

// ChannelMessages fetches messages for a channel via API.
func (session *Session) ChannelMessages(channelID string, params revoltgo.ChannelMessagesParams) (revoltgo.ChannelMessages, error) {
	if session.Session == nil {
		return revoltgo.ChannelMessages{}, fmt.Errorf("session is nil")
	}
	return session.Session.ChannelMessages(channelID, params)
}

// SendMessage sends a message to a channel.
func (session *Session) SendMessage(channelID string, content string) (*revoltgo.Message, error) {
	if session.Session == nil {
		return nil, fmt.Errorf("session is nil")
	}
	return session.Session.ChannelMessageSend(channelID, revoltgo.MessageSend{
		Content: content,
	})
}
