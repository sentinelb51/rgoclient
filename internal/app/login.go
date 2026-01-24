package app

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/widgets"
)

// ShowLoginWindow displays the login form.
func (app *ChatApp) ShowLoginWindow() {
	app.window.Resize(fyne.NewSize(300, 280))

	sessions, err := LoadSessions()
	if err != nil {
		fmt.Printf("Error loading sessions: %v\n", err)
		sessions = []SavedSession{}
	}

	sessionsSection := app.buildSavedSessionsSection(sessions)
	loginSection := app.buildLoginFormSection()

	content := container.NewVBox(
		widget.NewLabelWithStyle("Authentication", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		sessionsSection,
		widget.NewSeparator(),
		loginSection,
	)

	app.window.SetContent(container.NewPadded(content))
}

// buildSavedSessionsSection creates the UI for saved sessions.
func (app *ChatApp) buildSavedSessionsSection(sessions []SavedSession) fyne.CanvasObject {
	if len(sessions) == 0 {
		return widget.NewLabel("No recent sessions")
	}

	list := container.NewVBox()
	for _, s := range sessions {
		list.Add(app.buildSessionCard(s))
	}

	return container.NewVBox(
		widget.NewLabel("Recent Sessions"),
		list,
	)
}

// buildSessionCard creates a clickable card for a saved session.
func (app *ChatApp) buildSessionCard(session SavedSession) fyne.CanvasObject {
	return widgets.NewSessionCard(
		session.Username,
		session.AvatarID,
		func() { app.loginWithSavedSession(session) },
		func() {
			_ = RemoveSession(session.UserID)
			app.ShowLoginWindow()
		},
	)
}

// loginWithSavedSession attempts to login using a saved token.
func (app *ChatApp) loginWithSavedSession(session SavedSession) {
	fmt.Printf("Logging in as: %s\n", session.Username)

	app.window.SetContent(container.NewCenter(widget.NewLabel("Logging in...")))

	go func() {
		err := app.StartRevoltSessionWithToken(session.Token)

		app.GoDo(func() {
			if err != nil {
				fmt.Printf("Login failed: %v\n", err)
				_ = RemoveSession(session.UserID)
				dialog.ShowError(fmt.Errorf("session expired, please login again"), app.window)
				app.ShowLoginWindow()
				return
			}
			app.SwitchToMainUI()
		}, true)
	}()
}

// buildLoginFormSection creates the email/password login form.
func (app *ChatApp) buildLoginFormSection() fyne.CanvasObject {
	emailEntry := widget.NewEntry()
	emailEntry.SetPlaceHolder("Email")

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Password")

	var loginButton *widget.Button
	loginButton = widget.NewButton("Login", func() {
		email := emailEntry.Text
		password := passwordEntry.Text

		if email == "" || password == "" {
			dialog.ShowError(fmt.Errorf("please enter both email and password"), app.window)
			return
		}

		loginButton.Disable()
		loginButton.SetText("Logging in...")

		go func() {
			token, err := app.StartRevoltSessionWithLogin(email, password)

			app.GoDo(func() {
				if err != nil {
					loginButton.Enable()
					loginButton.SetText("Login")
					dialog.ShowError(fmt.Errorf("login failed: %v", err), app.window)
					return
				}

				app.SetPendingSessionToken(token)
				app.SwitchToMainUI()
			}, true)
		}()
	})

	passwordEntry.OnSubmitted = func(_ string) {
		loginButton.OnTapped()
	}

	return container.NewVBox(
		widget.NewLabel("Enter credentials"),
		emailEntry,
		passwordEntry,
		loginButton,
	)
}
