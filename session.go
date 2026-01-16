package main

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
func NewSession(s *revoltgo.Session) *Session {
	return &Session{Session: s}
}

// Server returns a server by ID, checking state cache first, then API.
func (s *Session) Server(id string) *revoltgo.Server {
	if s.Session == nil || id == "" {
		return nil
	}
	srv := s.State.Server(id)
	if srv == nil {
		fmt.Println("Server not found in state, fetching from API:", id)
		if fetched, err := s.Session.Server(id); err == nil {
			srv = fetched
		}
	}
	return srv
}

// Channel returns a channel by ID from state cache.
func (s *Session) Channel(id string) *revoltgo.Channel {
	if s.Session == nil || id == "" {
		return nil
	}
	ch := s.State.Channel(id) // Don't check API for channels; if we don't have it, it'll be a permission error
	return ch
}

// User returns a user by ID, checking state cache first, then API.
func (s *Session) User(id string) *revoltgo.User {
	if s.Session == nil || id == "" {
		return nil
	}
	user := s.State.User(id)
	if user == nil {
		if fetched, err := s.Session.User(id); err == nil {
			user = fetched
		}
	}
	return user
}

// Self returns the current authenticated user from state.
func (s *Session) Self() *revoltgo.User {
	if s.Session == nil {
		return nil
	}
	return s.State.Self()
}

// Open opens the websocket connection.
func (s *Session) Open() error {
	if s.Session == nil {
		return fmt.Errorf("session is nil")
	}
	return s.Session.Open()
}

// Close gracefully closes the websocket connection.
func (s *Session) Close() error {
	if s.Session == nil || s.WS == nil {
		return nil
	}
	return s.WS.WriteClose()
}

// ChannelMessages fetches messages for a channel via API.
func (s *Session) ChannelMessages(channelID string, params revoltgo.ChannelMessagesParams) (revoltgo.ChannelMessages, error) {
	if s.Session == nil {
		return revoltgo.ChannelMessages{}, fmt.Errorf("session is nil")
	}
	return s.Session.ChannelMessages(channelID, params)
}

// SendMessage sends a message to a channel.
func (s *Session) SendMessage(channelID string, content string) (*revoltgo.Message, error) {
	if s.Session == nil {
		return nil, fmt.Errorf("session is nil")
	}
	return s.Session.ChannelMessageSend(channelID, revoltgo.MessageSend{
		Content: content,
	})
}
