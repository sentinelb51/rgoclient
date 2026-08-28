package app

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/video"
)

const sessionsFile = ".rgoclient_sessions.json"

// readyTimeout is how long the client waits for the gateway's opening snapshot
// before handing the login screen back. Everything up to here can succeed and
// still leave nothing on screen — Open returns once the websocket is up, but
// Ready is the only thing that names the account, and revoltgo drops an event it
// cannot decode before any handler runs. Without this the client sits on "Logging
// in..." forever, the one failure that looks like a hang rather than an error.
const readyTimeout = 20 * time.Second

// loginMargin is the gutter around the card on the screens before Ready. Neither
// dimension is a number here: the card names its own width (ui.NewAuthCard) and
// what it measures is what the window is resized to, the saved-login list being
// as long as it is — a fixed height is a gap under one screen and a scrollbar on
// the other.
const loginMargin = 24

// registerURL is Stoat's signup page. Registration cannot happen in the client:
// POST /auth/account/create takes an hCaptcha token, which needs a browser to
// solve, and the verification code arrives by email either way.
var registerURL = &url.URL{Scheme: "https", Host: "stoat.chat", Path: "/login/create"}

/* Opening a session */

// startWithToken opens a session from a saved login: the token, and the ID of the
// session that token *is* where the saved login carries one. A login saved before
// the client began recording it opens exactly as before and simply cannot mark
// itself in the account's own session list — see domain.AccountSession.
func (a *App) startWithToken(session SavedSession) error {
	a.doOnUI(a.resetSessionState, true)

	return a.client.OpenAs(session.Token, session.SessionID)
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
	a.doOnUI(func() {
		a.pendingToken, a.pendingSessionID = result.Token, result.SessionID
	}, true)

	return result, nil
}

// resetSessionState clears the per-account view state, so a re-login — possibly
// as somebody else — starts clean rather than carrying the previous account's
// unread marks, collapsed categories and fetch guards. The client clears its own
// half, the message cache, when the session is replaced. Call on the UI thread.
func (a *App) resetSessionState() {
	// The rows a deletion was being held over belong to a column about to be
	// replaced, and the wake over them would fire into the next session.
	a.dropRemoval()

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

	// The page guard is released here rather than by the worker holding it: a page
	// still in flight is now stale and skips its own cleanup, and a flag left set
	// would stop scrollback for the rest of the run. setJumped also zeroes atOldest
	// and re-asks the jump bar, which the next session's first channel needs down.
	a.loadingPage = false
	a.setJumped(false)

	// A queued rebuild is of sidebars this account is about to stop having, and the
	// membership guards belong to its view of servers the next may not be in.
	if a.refreshTimer != nil {
		a.refreshTimer.Stop()
		a.refreshTimer = nil
	}
	a.dirty = 0
	a.memberStale = false
	a.dropMemberCache()
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

	// The call's token was minted against a session that no longer exists, and a
	// microphone must not stay open across a sign-out. The settings page is not
	// closed by signing out, so its level meter is stopped here as well rather than
	// left holding the input device for the rest of the run.
	a.leaveCall()
	a.forgetInputMonitor()

	// A video's decoder is a device the way a microphone is: stopped by whoever
	// drops the surface, and signing out drops every surface. What was learned
	// about the files goes too — it is bounded only by use, and the next session
	// re-derives it from the store on the way past.
	a.stopVideo()
	a.videoInfo = make(map[string]video.Info)
	a.videoAt = make(map[string]time.Duration)
	a.videoBusy = make(map[string]bool)
	a.videoFailed = make(map[string]bool)

	a.resetInvites()

	// A server's settings are about a server the account signing out is in, and the
	// page holds its own fetched lists — one of which may have a request out that
	// a.stale is about to swallow, leaving the section claiming to be fetching for
	// as long as it stayed open. Closing it drops both halves.
	a.closeServerSettings()
	a.closeGroupSettings() // about a conversation the account signing out is in

	// The profile and the fields the Account section was drawn from belong to the
	// account signing out.
	a.selfProfile, a.selfProfileOK = domain.UserProfile{}, false
	a.selfAvatarURL, a.selfHandle = "", ""

	a.unreadChannels = make(map[string]bool)
	a.mentions = make(map[string][]string)
	a.refetching = nil                            // claims belonging to a window this session was drawing
	a.dismissedMentions = make(map[string]bool)   // another account's mentions are not these
	a.collapsedCategories = make(map[string]bool) // keyed per server, so another account's keys are noise
	a.serverIDs = nil
	a.currentServerID = ""
	a.currentChannelID = ""
	a.pendingJoin = false
	a.homeSelected = false
	a.dmChannels = nil
	a.friendsRow = nil
	a.loadingDMs = false

	// The friends page lists the account signing out. It stands in the message
	// column's slot, so leaving it is what puts that column back for whoever signs
	// in next.
	a.leaveFriendsPage()
	if a.friendsPage != nil {
		a.friendsPage.SetSections(nil)
	}
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
			a.reportLogin(ui.ToneDanger, "Signed in, but your account never arrived. Retry.")
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

	sessions, err := LoadSessions()
	if err != nil {
		log.Printf("load sessions: %v", err)
	}

	rows, focus := a.buildLoginForm()

	rows = append([]fyne.CanvasObject{a.buildSavedSessions(sessions), ui.NewRowDivider()}, rows...)
	rows = append(rows, a.buildRegisterLink())

	a.mountLogin(ui.NewAuthCard("Sign in to Stoat", rows...), focus)
}

// mountLogin puts one of the screens before Ready up: the card, the modal notice
// layer over it, and the window shrunk to what the card measures. That layer is
// the whole of what either screen reports with — the notice stack belongs to a
// main UI that does not exist until Ready — and it takes no room, so neither
// reserves a line for a message that is usually not there.
//
// The order is load-bearing twice over: a container reports a minimum only once
// it holds something, and canvas focus is a property of the tree that is up.
func (a *App) mountLogin(card fyne.CanvasObject, focus fyne.Focusable) {
	framed := ui.NewInset(container.NewCenter(card), loginMargin, loginMargin, loginMargin, loginMargin)
	content := container.NewStack(framed, a.modal.Layer)

	a.window.SetContent(content)

	size := content.MinSize()
	a.window.Resize(fyne.NewSize(size.Width, size.Height))

	// Nil for the screen with nothing to type into — the one held while a saved
	// token is exchanged — which Fyne reads as "focus nothing".
	a.window.Canvas().Focus(focus)
}

// buildRegisterLink is the way out to Stoat's signup page, for a reader with no
// account to sign in with. A link rather than a button: it leads out of the
// client, and a second target the size of Login above it would read as the other
// half of a pair.
func (a *App) buildRegisterLink() fyne.CanvasObject {
	link := ui.NewLinkTextWith("Create an account", 0, func() {
		if err := a.fyne.OpenURL(registerURL); err != nil {
			log.Printf("open %s: %v", registerURL, err)
			a.reportLogin(ui.ToneDanger, "Couldn't open the browser. Visit "+registerURL.String())
		}
	})

	return container.NewCenter(link)
}

// reportLogin says how a sign-in went, on whichever of the two screens is up.
// Call on the UI thread, after the screen it reports on has been built.
func (a *App) reportLogin(tone ui.Tone, message string) {
	a.notifyModal(tone, "%s", message)
}

// buildSavedSessions lists the saved sessions as clickable cards, under the same
// caption a field carries. An account with none is told so rather than shown a
// caption over nothing.
func (a *App) buildSavedSessions(sessions []SavedSession) fyne.CanvasObject {
	if len(sessions) == 0 {
		return ui.NewAuthNote("No saved logins yet. Sign in below and this one is kept.")
	}

	rows := []fyne.CanvasObject{
		ui.NewAuthCaption("Saved logins"),
		ui.VerticalSpacer(theme.Sizes.DialogLabelGap),
	}
	for i, session := range sessions {
		if i > 0 {
			rows = append(rows, ui.VerticalSpacer(theme.Sizes.DialogLabelGap))
		}

		rows = append(rows, ui.NewSessionCard(a.images, session.Username, session.avatarURL(),
			func() { a.loginWithToken(session) },
			func() {
				_ = RemoveSession(session.UserID)
				a.showLogin()
			},
		))
	}

	return ui.VBoxNoSpacing(rows...)
}

// buildLoginForm builds the email/password rows, handing back the field the
// screen opens focused on. Rows rather than a container: they are spaced by the
// card holding them, at the gap every other card sets its fields at. On success onReady swaps in the main UI and persists
// the token; only failures come back here.
func (a *App) buildLoginForm() ([]fyne.CanvasObject, fyne.Focusable) {
	email := widget.NewEntry()
	email.SetPlaceHolder("Email")

	password := widget.NewPasswordEntry()
	password.SetPlaceHolder("Password")

	var login *ui.Button
	login = ui.NewWeightedButton("Login", ui.ButtonPrimary, func() {
		if email.Text == "" || password.Text == "" {
			a.reportLogin(ui.ToneWarning, "Enter both an email address and a password.")
			return
		}

		a.modal.Clear()
		login.Disable()
		login.SetText("Logging in...")

		go func() {
			result, err := a.startWithLogin(email.Text, password.Text)

			a.doOnUI(func() {
				switch {
				case err != nil:
					login.Enable()
					login.SetText("Login")
					a.reportLogin(ui.ToneDanger, "Login failed: "+err.Error())
				case result.Pending():
					a.showMFAChallenge(result)
				default:
					// The session is open and the screen stays up until Ready lands.
					a.awaitReady()
				}
			}, true)
		}()
	})
	// Enter carries down the form rather than submitting an empty half of it.
	email.OnSubmitted = func(string) { a.window.Canvas().Focus(password) }
	password.OnSubmitted = func(string) { login.Tap() }

	return []fyne.CanvasObject{
		ui.NewAuthField("Email", email),
		ui.NewAuthField("Password", password),
		login,
	}, email
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

	var submit *ui.Button
	submit = ui.NewWeightedButton("Verify", ui.ButtonPrimary, func() {
		if code.Text == "" {
			a.reportLogin(ui.ToneWarning, "Enter the code first.")
			return
		}

		a.modal.Clear()
		submit.Disable()
		submit.SetText("Verifying...")

		go func() {
			_, err := a.answerMFA(challenge.Ticket, method, code.Text)

			a.doOnUI(func() {
				if err != nil {
					submit.Enable()
					submit.SetText("Verify")
					a.reportLogin(ui.ToneDanger, "Verification failed: "+err.Error())

					return
				}
				a.awaitReady()
			}, true)
		}()
	})
	code.OnSubmitted = func(string) { submit.Tap() }

	var rows []fyne.CanvasObject
	if len(challenge.Methods) > 1 {
		rows = append(rows, mfaMethodPicker(challenge.Methods, method, func(picked client.MFAMethod) {
			method = picked
		}))
	}
	rows = append(rows,
		ui.NewAuthNote(mfaPrompt(method)),
		ui.NewAuthField("Code", code),
		submit,
		ui.NewButton("Cancel", a.showLogin),
	)

	a.mountLogin(ui.NewAuthCard("Two-factor authentication", rows...), code)
}

// mfaMethodPicker offers the factors this account will accept. The labels are
// what the user picks between, so the selection is mapped back rather than the
// method being read off the string.
func mfaMethodPicker(methods []client.MFAMethod, selected client.MFAMethod, onPick func(client.MFAMethod)) fyne.CanvasObject {
	labels := make([]string, len(methods))
	for i, method := range methods {
		labels[i] = method.Label()
	}

	chosen := max(slices.Index(methods, selected), 0)

	return ui.NewAuthField("Method", ui.NewAuthChoice(labels, chosen, func(index int) {
		if index >= 0 && index < len(methods) {
			onPick(methods[index])
		}
	}))
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
	a.mountLogin(ui.NewAuthCard("Signing in",
		ui.NewAuthNote("Opening the session for "+session.Username+".")), nil)

	go func() {
		err := a.startWithToken(session)

		a.doOnUI(func() {
			if err == nil {
				a.awaitReady()
				return
			}

			log.Printf("token login: %v", err)
			_ = RemoveSession(session.UserID)
			a.showLogin()
			a.reportLogin(ui.ToneDanger, "That saved login no longer works. Sign in again.")
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
	Token string `json:"token"`

	// SessionID is which of the account's sessions that token *is*, answered only
	// by the login that made it — see client.Login. Absent from anything saved
	// before the client began recording one, which is why nothing may assume it:
	// what it is missing from is a session that cannot mark itself in the account's
	// own list, and a fresh sign-in is what puts it back.
	SessionID string `json:"session_id,omitempty"`

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

// saveSessions writes the full session list to disk. Through config.WriteAtomic
// rather than os.WriteFile: this one file holds every saved login, so a write cut
// short is not one session lost but all of them, and the tokens in it are the
// account — it is written at a mode nobody else on the machine can read.
func saveSessions(sessions []SavedSession) error {
	path, err := sessionsPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	return config.WriteAtomic(path, data)
}

// sessionsPath returns the path to the saved-sessions file in the home dir.
func sessionsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, sessionsFile), nil
}
