package context

import (
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// Global session context for efficient access throughout the app.
// Thread-safe and optimized for high-frequency reads (like message rendering).
var (
	globalSession *revoltgo.Session
	sessionMutex  sync.RWMutex
)

// SetSession sets the global session.
// Call this when session is established or changes.
func SetSession(session *revoltgo.Session) {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	globalSession = session
}

// Session returns the current session (read-optimized with RLock).
// Returns nil if no session is active.
func Session() *revoltgo.Session {
	sessionMutex.RLock()
	defer sessionMutex.RUnlock()
	return globalSession
}

// ClearSession clears the global session (e.g., on logout).
func ClearSession() {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	globalSession = nil
}
