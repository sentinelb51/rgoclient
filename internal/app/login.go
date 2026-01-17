package app

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/api"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
)

// ShowLoginWindow displays the login form for user authentication.
func (application *ChatApp) ShowLoginWindow() {
	application.window.Resize(fyne.NewSize(300, 280))

	// Load saved sessions
	sessions, err := api.LoadSessions()
	if err != nil {
		fmt.Printf("Error loading sessions: %v\n", err)
		sessions = []api.SavedSession{}
	}

	// Build saved sessions section
	sessionsSection := application.buildSavedSessionsSection(sessions)

	// Build login form section
	loginSection := application.buildLoginFormSection()

	// Main layout
	content := container.NewVBox(
		widget.NewLabelWithStyle("Authentication", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		sessionsSection,
		widget.NewSeparator(),
		loginSection,
	)

	application.window.SetContent(container.NewPadded(content))
}

// buildSavedSessionsSection creates the UI section showing saved sessions.
func (application *ChatApp) buildSavedSessionsSection(sessions []api.SavedSession) fyne.CanvasObject {
	if len(sessions) == 0 {
		return widget.NewLabel("No recent sessions")
	}

	// Create a vertical list of session cards
	sessionList := container.NewVBox()
	for _, session := range sessions {
		sessionList.Add(application.buildSessionCard(session))
	}

	return container.NewVBox(
		widget.NewLabel("Recent Sessions"),
		sessionList,
	)
}

// buildSessionCard creates a clickable card for a saved session.
func (application *ChatApp) buildSessionCard(session api.SavedSession) fyne.CanvasObject {
	return widgets.NewSessionCard(
		session.Username,
		session.AvatarID,
		func() {
			application.loginWithSavedSession(session)
		},
		func() {
			_ = api.RemoveSession(session.UserID)
			application.ShowLoginWindow()
		},
	)
}

// loginWithSavedSession attempts to login using a saved session token.
func (application *ChatApp) loginWithSavedSession(session api.SavedSession) {
	fmt.Printf("Attempting login with saved session for: %s\n", session.Username)

	// Show loading state
	application.window.SetContent(container.NewCenter(widget.NewLabel("Logging in...")))

	go func() {
		err := application.StartRevoltSessionWithToken(session.Token)

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			if err != nil {
				fmt.Printf("Failed to login with saved session: %v\n", err)
				// Remove invalid session
				_ = api.RemoveSession(session.UserID)
				dialog.ShowError(fmt.Errorf("session expired, please login again"), application.window)
				application.ShowLoginWindow()
				return
			}

			// Update session info and switch to main UI
			application.SwitchToMainUI()
		}, true)
	}()
}

// buildLoginFormSection creates the email/password login form.
func (application *ChatApp) buildLoginFormSection() fyne.CanvasObject {
	emailEntry := widget.NewEntry()
	emailEntry.SetPlaceHolder("Email")

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Password")

	var loginButton *widget.Button
	loginButton = widget.NewButton("Login", func() {
		email := emailEntry.Text
		password := passwordEntry.Text

		if email == "" || password == "" {
			dialog.ShowError(fmt.Errorf("please enter both email and password"), application.window)
			return
		}

		// Disable button while logging in
		loginButton.Disable()
		loginButton.SetText("Logging in...")

		go func() {
			token, err := application.StartRevoltSessionWithLogin(email, password)

			fyne.CurrentApp().Driver().DoFromGoroutine(func() {
				if err != nil {
					loginButton.Enable()
					loginButton.SetText("Login")
					dialog.ShowError(fmt.Errorf("login failed: %v", err), application.window)
					return
				}

				// Set pending token - session will be saved when Ready event fires
				application.SetPendingSessionToken(token)

				// Switch to main UI
				application.SwitchToMainUI()
			}, true)
		}()
	})

	// Submit on Enter key in password field
	passwordEntry.OnSubmitted = func(_ string) {
		loginButton.OnTapped()
	}

	// Form with full-width button
	form := container.NewVBox(
		widget.NewLabel("Enter credentials"),
		emailEntry,
		passwordEntry,
		loginButton,
	)

	return form
}

// LoginWindowSize returns the size for the login window.
func LoginWindowSize() fyne.Size {
	return fyne.NewSize(300, 280)
}

// MainWindowSize returns the default size for the main window.
func MainWindowSize() fyne.Size {
	return fyne.NewSize(theme.Sizes.WindowDefaultWidth, theme.Sizes.WindowDefaultHeight)
}
