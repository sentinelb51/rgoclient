package main

import (
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"

	"RGOClient/internal/app"
	apptheme "RGOClient/internal/ui/theme"
)

/*
	TODO: Message delete should remove from message cache and update display
	TODO: Login screen, hovering over an active session; highlight is partial, should highlight the whole tile
	TODO: Edit messages in place
	TODO: Cursor when typing is invisible
	TODO: Right-click context menu on messages
 	TODO: Selectable text for messages?
	TODO: Notification toasts for warnings; slide from bottom right, 5 dots each counts every second, click to dismiss, auto-dismiss after 5s (5 dots)
*/

func main() {
	fyneApp := fyneapp.New()
	fyneApp.Settings().SetTheme(apptheme.NewAppTheme(theme.DefaultTheme()))

	app.New(fyneApp).Run()
}
