package app

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui"
)

// loginWindowSize is the compact size used for the login screen.
var loginWindowSize = fyne.NewSize(300, 280)

// showLogin displays the saved sessions and the credential form.
func (a *App) showLogin() {
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
		s := session
		cards.Add(ui.NewSessionCard(a.images, s.Username, s.AvatarID,
			func() { a.loginWithToken(s) },
			func() {
				_ = RemoveSession(s.UserID)
				a.showLogin()
			},
		))
	}

	return container.NewVBox(widget.NewLabel("Recent Sessions"), cards)
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

// buildLoginForm builds the email/password form.
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

		// On success onReady swaps in the main UI (and persists the token
		// startWithLogin stashed in pendingToken); only failures come back here.
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
