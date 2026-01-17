package api

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const sessionsFileName = ".rgoclient_sessions.json"

// SavedSession represents a persisted user session with metadata.
type SavedSession struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	AvatarID string `json:"avatar_id"`
}

// getSessionsPath returns the path to the sessions file in the user's home directory.
func getSessionsPath() (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDirectory, sessionsFileName), nil
}

// LoadSessions loads all saved sessions from disk.
// Returns empty slice if no sessions are found.
func LoadSessions() ([]SavedSession, error) {
	sessionsPath, err := getSessionsPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(sessionsPath)
	if os.IsNotExist(err) {
		return []SavedSession{}, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions []SavedSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}

	return sessions, nil
}

// SaveSessions saves all sessions to disk.
func SaveSessions(sessions []SavedSession) error {
	sessionsPath, err := getSessionsPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sessionsPath, data, 0600)
}

// AddOrUpdateSession adds a new session or updates an existing one by UserID.
func AddOrUpdateSession(session SavedSession) error {
	sessions, err := LoadSessions()
	if err != nil {
		return err
	}

	// Find and update existing session, or append new one
	found := false
	for index, existingSession := range sessions {
		if existingSession.UserID == session.UserID {
			sessions[index] = session
			found = true
			break
		}
	}

	if !found {
		sessions = append(sessions, session)
	}

	return SaveSessions(sessions)
}

// RemoveSession removes a session by UserID.
func RemoveSession(userID string) error {
	sessions, err := LoadSessions()
	if err != nil {
		return err
	}

	filteredSessions := make([]SavedSession, 0, len(sessions))
	for _, session := range sessions {
		if session.UserID != userID {
			filteredSessions = append(filteredSessions, session)
		}
	}

	return SaveSessions(filteredSessions)
}

// DeleteAllSessions removes all saved sessions.
func DeleteAllSessions() error {
	sessionsPath, err := getSessionsPath()
	if err != nil {
		return err
	}

	err = os.Remove(sessionsPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// GetSessionByUserID finds a session by UserID.
func GetSessionByUserID(userID string) (*SavedSession, error) {
	sessions, err := LoadSessions()
	if err != nil {
		return nil, err
	}

	for _, session := range sessions {
		if session.UserID == userID {
			return &session, nil
		}
	}

	return nil, nil
}
