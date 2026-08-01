package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
)

const sessionsFile = ".rgoclient_sessions.json"

// loginWindowSize is the compact size used for the login screen.
var loginWindowSize = fyne.NewSize(300, 280)

/* Opening a session */

// startWithToken opens a session using an existing token.
func (a *App) startWithToken(token string) error {
	return a.openSession(revoltgo.New(token))
}

// startWithLogin opens a session using credentials. The new token is stashed in
// pendingToken before the gateway opens — onReady persists it, and Ready can
// arrive before this goroutine would otherwise get back onto the UI thread.
func (a *App) startWithLogin(email, password string) error {
	session, resp, err := revoltgo.NewWithLogin(revoltgo.LoginParams{Email: email, Password: password})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	a.doOnUI(func() { a.pendingToken = resp.Token }, true)
	if err := a.openSession(session); err != nil {
		a.doOnUI(func() { a.pendingToken = "" }, true)
		return err
	}

	return nil
}

// openSession registers the gateway handlers and opens the websocket. It runs on
// a login goroutine, so the a.session write goes through the UI thread — the only
// place that field is ever written, apart from onError's teardown, which keeps
// every UI-thread read race-free.
func (a *App) openSession(session *revoltgo.Session) error {
	a.doOnUI(func() {
		a.resetSessionState()
		a.session = session
	}, true)

	revoltgo.AddHandler(session, a.onReady)
	revoltgo.AddHandler(session, a.onMessage)
	revoltgo.AddHandler(session, a.onMessageUpdate)
	revoltgo.AddHandler(session, a.onMessageDelete)
	revoltgo.AddHandler(session, a.onBulkMessageDelete)
	revoltgo.AddHandler(session, a.onServerCreate)
	revoltgo.AddHandler(session, a.onServerMemberJoin)
	revoltgo.AddHandler(session, a.onServerMemberLeave)
	revoltgo.AddHandler(session, a.onServerMemberUpdate)
	revoltgo.AddHandler(session, a.onError)

	if err := session.Open(); err != nil {
		a.doOnUI(func() { a.session = nil }, true)
		return fmt.Errorf("open session: %w", err)
	}

	return nil
}

// resetSessionState clears the per-account caches and view state, so a re-login
// (possibly as another account) starts clean instead of carrying the previous
// account's messages, unread marks, and author-fetch guards. Call on the UI
// thread.
func (a *App) resetSessionState() {
	a.messages.Clear()

	if a.authorTimer != nil {
		a.authorTimer.Stop()
		a.authorTimer = nil
	}
	a.pendingAuthors = nil
	a.fetchedAuthors = make(map[string]bool)

	a.unreadChannels = make(map[string]bool)
	a.serverIDs = nil
	a.currentServerID = ""
	a.currentChannelID = ""
	a.pendingJoin = false
	a.homeSelected = false
	a.dmChannels = nil
	a.loadingDMs = false
}

/* Login screen */

// showLogin displays the saved sessions and the credential form.
func (a *App) showLogin() {
	a.closeOverlay() // a viewer left open by a dropped session would outlive its window
	a.window.Resize(loginWindowSize)

	sessions, err := LoadSessions()
	if err != nil {
		log.Printf("load sessions: %v", err)
	}

	content := container.NewVBox(
		widget.NewLabelWithStyle("Authentication", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		a.buildSavedSessions(sessions),
		widget.NewSeparator(),
		a.buildLoginForm(),
	)
	a.window.SetContent(container.NewPadded(content))
}

// buildSavedSessions lists the saved sessions as clickable cards.
func (a *App) buildSavedSessions(sessions []SavedSession) fyne.CanvasObject {
	if len(sessions) == 0 {
		return widget.NewLabel("No recent sessions")
	}

	cards := container.NewVBox()
	for _, session := range sessions {
		cards.Add(ui.NewSessionCard(a.images, session.Username, session.AvatarID,
			func() { a.loginWithToken(session) },
			func() {
				_ = RemoveSession(session.UserID)
				a.showLogin()
			},
		))
	}

	return container.NewVBox(widget.NewLabel("Recent Sessions"), cards)
}

// buildLoginForm builds the email/password form. On success onReady swaps in the
// main UI and persists the token; only failures come back here.
func (a *App) buildLoginForm() fyne.CanvasObject {
	email := widget.NewEntry()
	email.SetPlaceHolder("Email")

	password := widget.NewPasswordEntry()
	password.SetPlaceHolder("Password")

	var login *widget.Button
	login = widget.NewButton("Login", func() {
		if email.Text == "" || password.Text == "" {
			dialog.ShowError(fmt.Errorf("please enter both email and password"), a.window)
			return
		}

		login.Disable()
		login.SetText("Logging in...")

		go func() {
			err := a.startWithLogin(email.Text, password.Text)
			if err == nil {
				return
			}
			a.doOnUI(func() {
				login.Enable()
				login.SetText("Login")
				dialog.ShowError(fmt.Errorf("login failed: %v", err), a.window)
			}, true)
		}()
	})
	password.OnSubmitted = func(string) { login.OnTapped() }

	return container.NewVBox(
		widget.NewLabel("Enter credentials"),
		ui.WithCaret(email),
		ui.WithCaret(password),
		login,
	)
}

// loginWithToken logs in using a saved session's token. On success the window
// stays on the "Logging in..." screen until onReady swaps in the main UI —
// building it here too would construct the whole layout twice.
func (a *App) loginWithToken(session SavedSession) {
	a.window.SetContent(container.NewCenter(widget.NewLabel("Logging in...")))

	go func() {
		err := a.startWithToken(session.Token)
		if err == nil {
			return
		}
		a.doOnUI(func() {
			log.Printf("token login: %v", err)
			_ = RemoveSession(session.UserID)
			dialog.ShowError(fmt.Errorf("session expired, please login again"), a.window)
			a.showLogin()
		}, true)
	}()
}

/* Saved-session store */

// SavedSession is a persisted login plus the metadata shown on its card.
type SavedSession struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	AvatarID string `json:"avatar_id"`
}

// LoadSessions reads all saved sessions, returning nil when the file is absent.
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

// sessionsPath returns the path to the saved-sessions file in the home dir.
func sessionsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, sessionsFile), nil
}
