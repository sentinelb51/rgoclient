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

// loginWithToken logs in using a saved session's token.
func (a *App) loginWithToken(session SavedSession) {
	a.window.SetContent(container.NewCenter(widget.NewLabel("Logging in...")))

	go func() {
		err := a.startWithToken(session.Token)
		a.doOnUI(func() {
			if err != nil {
				log.Printf("token login: %v", err)
				_ = RemoveSession(session.UserID)
				dialog.ShowError(fmt.Errorf("session expired, please login again"), a.window)
				a.showLogin()
				return
			}
			a.showMainUI()
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

		go func() {
			token, err := a.startWithLogin(email.Text, password.Text)
			a.doOnUI(func() {
				if err != nil {
					login.Enable()
					login.SetText("Login")
					dialog.ShowError(fmt.Errorf("login failed: %v", err), a.window)
					return
				}
				a.pendingToken = token
				a.showMainUI()
			}, true)
		}()
	})

	password.OnSubmitted = func(string) { login.OnTapped() }

	return container.NewVBox(
		widget.NewLabel("Enter credentials"),
		email,
		password,
		login,
	)
}
