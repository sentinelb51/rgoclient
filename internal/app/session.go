package app

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

const sessionsFile = ".rgoclient_sessions.json"

// readyTimeout is how long the client waits for the gateway's opening snapshot
// before handing the login screen back. Everything up to here can succeed and
// still leave nothing on screen — Open returns once the websocket is up, but
// Ready is the only thing that names the account, and revoltgo drops an event it
// cannot decode before any handler runs. Without this the client sits on "Logging
// in..." forever, the one failure that looks like a hang rather than an error.
const readyTimeout = 20 * time.Second

// loginWindowSize is the compact size used for the login screen.
var loginWindowSize = fyne.NewSize(300, 320)

/* Opening a session */

// startWithToken opens a session using an existing token.
func (a *App) startWithToken(token string) error {
	a.doOnUI(a.resetSessionState, true)

	return a.client.Open(token)
}

// startWithLogin opens a session using credentials. An account with a second
// factor comes back pending instead — nothing is logged in, and the caller shows
// the challenge.
func (a *App) startWithLogin(email, password string) (client.Login, error) {
	a.doOnUI(a.resetSessionState, true)

	return a.stashToken(a.client.Login(email, password))
}

// answerMFA finishes a login the server is holding. The session state was
// cleared when the password was accepted, so nothing is reset again here.
func (a *App) answerMFA(ticket string, method client.MFAMethod, code string) (client.Login, error) {
	return a.stashToken(a.client.AnswerMFA(ticket, method, code))
}

// stashToken records a completed login's token before the gateway can report
// Ready. onReady persists it, and Ready can arrive before the goroutine that
// asked for the login would otherwise get back onto the UI thread.
func (a *App) stashToken(result client.Login, err error) (client.Login, error) {
	if err != nil || result.Token == "" {
		return result, err
	}
	a.doOnUI(func() { a.pendingToken = result.Token }, true)

	return result, nil
}

// resetSessionState clears the per-account view state, so a re-login — possibly
// as somebody else — starts clean rather than carrying the previous account's
// unread marks, collapsed categories and fetch guards. The client clears its own
// half, the message cache, when the session is replaced. Call on the UI thread.
func (a *App) resetSessionState() {
	a.epoch++         // anything still in flight for the old session is now stale
	a.notices.Clear() // a failure from the last account has nothing to say to this one

	if a.authorTimer != nil {
		a.authorTimer.Stop()
		a.authorTimer = nil
	}
	a.pendingAuthors = nil
	a.fetchedAuthors = make(map[string]bool)

	// Messages held outside the cache belong to channels the next account may not be
	// able to read, and their guards to a session that can no longer retry.
	if a.replyTimer != nil {
		a.replyTimer.Stop()
		a.replyTimer = nil
	}
	a.pendingReplies = nil
	a.uncached = make(map[string]*domain.Message)
	a.fetchedReplies = make(map[string]bool)

	// A queued rebuild is of sidebars this account is about to stop having, and the
	// membership guards belong to its view of servers the next may not be in.
	if a.refreshTimer != nil {
		a.refreshTimer.Stop()
		a.refreshTimer = nil
	}
	a.dirty = 0
	a.memberStale = false
	a.fetchedMembers = make(map[string]bool)
	a.memberFailed = make(map[string]bool)
	a.memberLoading = ""
	a.stopMemberWatchdog()

	// The gateway that was going to send Ready is the one being replaced, so a
	// watchdog left armed would drop the login that is starting now.
	a.stopAwaitingReady()

	// A pending ack fires against whatever session is current, not the one that
	// scheduled it — left running it would acknowledge the previous account's
	// channel through the new account's session.
	if a.ackTimer != nil {
		a.ackTimer.Stop()
		a.ackTimer = nil
	}
	a.ackChannelID, a.ackMessageID = "", ""

	// The cooldowns belong to the account that earned them, and the badge's tick has
	// nothing left to count either way.
	if a.slowmodeTimer != nil {
		a.slowmodeTimer.Stop()
		a.slowmodeTimer = nil
	}
	a.slowmodeUntil = make(map[string]time.Time)

	// Nobody the previous account could see is typing as far as this one is
	// concerned, and an announcement of our own has no socket left to take back.
	a.resetTyping()

	a.resetInvites()

	// The profile and the fields the Account section was drawn from belong to the
	// account signing out.
	a.selfProfile, a.selfProfileOK = domain.UserProfile{}, false
	a.selfAvatarURL, a.selfHandle = "", ""

	a.unreadChannels = make(map[string]bool)
	a.collapsedCategories = make(map[string]bool) // keyed per server, so another account's keys are noise
	a.serverIDs = nil
	a.currentServerID = ""
	a.currentChannelID = ""
	a.pendingJoin = false
	a.homeSelected = false
	a.dmChannels = nil
	a.friendsRow = nil
	a.loadingDMs = false
}

/* Waiting for Ready */

// awaitReady starts the watchdog on the gateway's opening snapshot. Call on the
// UI thread, once the session is open.
func (a *App) awaitReady() {
	a.stopAwaitingReady()

	var watchdog *time.Timer
	watchdog = time.AfterFunc(readyTimeout, func() {
		a.doOnUI(func() {
			// A fired timer cannot be recalled, so the wake checks it is still the one
			// the field holds — a re-login replaces it rather than stopping it.
			if a.readyTimer != watchdog {
				return
			}
			a.readyTimer = nil

			log.Printf("no ready event after %s", readyTimeout)
			a.client.Close()
			a.showLogin()
			a.reportLogin("Signed in, but your account never arrived. Retry.")
		}, false)
	})
	a.readyTimer = watchdog
}

// stopAwaitingReady disarms the watchdog. Call on the UI thread.
func (a *App) stopAwaitingReady() {
	if a.readyTimer == nil {
		return
	}

	a.readyTimer.Stop()
	a.readyTimer = nil
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

	// Its own line: the notice layer is part of the main UI, which is not built until
	// Ready, so until then every outcome has exactly one place to go.
	a.loginStatus = ui.NewStatusLine()

	content := container.NewVBox(
		widget.NewLabelWithStyle("Authentication", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		a.buildSavedSessions(sessions),
		widget.NewSeparator(),
		a.buildLoginForm(),
		a.loginStatus.Content,
	)
	a.window.SetContent(container.NewPadded(content))
}

// reportLogin says why a sign-in did not work, on whichever of the two screens
// is up. Call on the UI thread, after the screen it reports on has been built.
func (a *App) reportLogin(message string) {
	if a.loginStatus == nil {
		return
	}

	a.loginStatus.Fail(message)
}

// buildSavedSessions lists the saved sessions as clickable cards.
func (a *App) buildSavedSessions(sessions []SavedSession) fyne.CanvasObject {
	if len(sessions) == 0 {
		return widget.NewLabel("No recent sessions")
	}

	cards := make([]fyne.CanvasObject, len(sessions))
	for i, session := range sessions {
		cards[i] = ui.NewSessionCard(a.images, session.Username, session.avatarURL(),
			func() { a.loginWithToken(session) },
			func() {
				_ = RemoveSession(session.UserID)
				a.showLogin()
			},
		)
	}

	return container.NewVBox(widget.NewLabel("Recent Sessions"), container.NewVBox(cards...))
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
			a.reportLogin("Enter both an email address and a password.")
			return
		}

		a.loginStatus.Clear()
		login.Disable()
		login.SetText("Logging in...")

		go func() {
			result, err := a.startWithLogin(email.Text, password.Text)

			a.doOnUI(func() {
				switch {
				case err != nil:
					login.Enable()
					login.SetText("Login")
					a.reportLogin("Login failed: " + err.Error())
				case result.Pending():
					a.showMFAChallenge(result)
				default:
					// The session is open and the screen stays up until Ready lands.
					a.awaitReady()
				}
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

/* The second factor */

// showMFAChallenge asks for the code the held login is waiting on. It replaces
// the login screen rather than stacking a dialog over it: the ticket is what the
// account is now being signed in *by*, the password having been spent, so there
// is nothing behind worth returning to but starting again. The picker is drawn
// from what came back rather than everything Revolt supports, and one method —
// the ordinary case — needs none.
func (a *App) showMFAChallenge(challenge client.Login) {
	method := preferredMethod(challenge.Methods)

	code := widget.NewEntry()
	code.SetPlaceHolder("Code")

	a.loginStatus = ui.NewStatusLine()

	var submit *widget.Button
	submit = widget.NewButton("Verify", func() {
		if code.Text == "" {
			a.reportLogin("Enter the code first.")
			return
		}

		a.loginStatus.Clear()
		submit.Disable()
		submit.SetText("Verifying...")

		go func() {
			_, err := a.answerMFA(challenge.Ticket, method, code.Text)

			a.doOnUI(func() {
				if err != nil {
					submit.Enable()
					submit.SetText("Verify")
					a.reportLogin("Verification failed: " + err.Error())

					return
				}
				a.awaitReady()
			}, true)
		}()
	})
	code.OnSubmitted = func(string) { submit.OnTapped() }

	rows := []fyne.CanvasObject{
		widget.NewLabelWithStyle("Two-factor authentication", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
	}
	if len(challenge.Methods) > 1 {
		rows = append(rows, mfaMethodPicker(challenge.Methods, method, func(picked client.MFAMethod) {
			method = picked
		}))
	}
	rows = append(rows,
		widget.NewLabel(mfaPrompt(method)),
		ui.WithCaret(code),
		submit,
		widget.NewButton("Cancel", a.showLogin),
		a.loginStatus.Content,
	)

	a.window.SetContent(container.NewPadded(container.NewVBox(rows...)))
	a.window.Canvas().Focus(code)
}

// mfaMethodPicker offers the factors this account will accept. The labels are
// what the user picks between, so the selection is mapped back rather than the
// method being read off the string.
func mfaMethodPicker(methods []client.MFAMethod, selected client.MFAMethod, onPick func(client.MFAMethod)) fyne.CanvasObject {
	byLabel := make(map[string]client.MFAMethod, len(methods))
	labels := make([]string, 0, len(methods))
	for _, method := range methods {
		byLabel[method.Label()] = method
		labels = append(labels, method.Label())
	}

	picker := widget.NewSelect(labels, func(label string) {
		if method, known := byLabel[label]; known {
			onPick(method)
		}
	})
	picker.SetSelected(selected.Label())

	return picker
}

// preferredMethod picks what to ask for first. An authenticator app is what
// almost every account with a second factor has, and a recovery code is the one
// you use when you cannot reach it — so offering recovery first would put the
// emergency route in front of the everyday one.
func preferredMethod(methods []client.MFAMethod) client.MFAMethod {
	for _, wanted := range []client.MFAMethod{client.MFATOTP, client.MFAPassword, client.MFARecovery} {
		if slices.Contains(methods, wanted) {
			return wanted
		}
	}
	if len(methods) > 0 {
		return methods[0]
	}

	return client.MFATOTP
}

// mfaPrompt says where the code comes from. A recovery code is written down
// somewhere rather than generated, so naming the app would send somebody looking
// in the wrong place.
func mfaPrompt(method client.MFAMethod) string {
	switch method {
	case client.MFARecovery:
		return "Enter one of your recovery codes."
	case client.MFAPassword:
		return "Enter your password again to continue."
	}

	return "Enter the code from your authenticator app."
}

// loginWithToken logs in using a saved session's token. On success the window
// stays on the "Logging in..." screen until onReady swaps in the main UI —
// building it here too would construct the whole layout twice.
func (a *App) loginWithToken(session SavedSession) {
	a.window.SetContent(container.NewCenter(widget.NewLabel("Logging in...")))

	go func() {
		err := a.startWithToken(session.Token)

		a.doOnUI(func() {
			if err == nil {
				a.awaitReady()
				return
			}

			log.Printf("token login: %v", err)
			_ = RemoveSession(session.UserID)
			a.showLogin()
			a.reportLogin("That saved login no longer works. Sign in again.")
		}, true)
	}()
}

/* Saved-session store */

// SavedSession is a persisted login plus the metadata shown on its card.
//
// AvatarID is only read, never written: sessions saved before the client stopped
// building CDN URLs outside internal/client still carry one, and a card with the
// right face is worth the four lines that keep it working.
type SavedSession struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	AvatarID  string `json:"avatar_id,omitempty"`
}

// avatarURL is the picture the session's card shows.
func (s SavedSession) avatarURL() string {
	if s.AvatarURL != "" {
		return s.AvatarURL
	}

	return client.AvatarURL(s.AvatarID)
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
