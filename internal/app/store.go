package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

const sessionsFile = ".rgoclient_sessions.json"

// SavedSession is a persisted login plus the metadata shown on its card.
type SavedSession struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	AvatarID string `json:"avatar_id"`
}

// sessionsPath returns the path to the saved-sessions file in the home dir.
func sessionsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, sessionsFile), nil
}

// LoadSessions reads all saved sessions, returning an empty slice if none exist.
func LoadSessions() ([]SavedSession, error) {
	path, err := sessionsPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
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

// saveSessions writes the full session list to disk.
func saveSessions(sessions []SavedSession) error {
	path, err := sessionsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// AddOrUpdateSession stores a session, replacing any existing one for the user.
func AddOrUpdateSession(session SavedSession) error {
	sessions, err := LoadSessions()
	if err != nil {
		return err
	}

	i := slices.IndexFunc(sessions, func(s SavedSession) bool { return s.UserID == session.UserID })
	if i >= 0 {
		sessions[i] = session
	} else {
		sessions = append(sessions, session)
	}
	return saveSessions(sessions)
}

// RemoveSession deletes the saved session for a user, if present.
func RemoveSession(userID string) error {
	sessions, err := LoadSessions()
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(sessions, func(s SavedSession) bool { return s.UserID == userID })
	return saveSessions(kept)
}
