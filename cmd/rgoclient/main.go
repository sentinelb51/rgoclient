package main

import (
	fyneApp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"

	"RGOClient/internal/app"
	appTheme "RGOClient/internal/ui/theme"
)

func main() {
	application := fyneApp.New()
	application.Settings().SetTheme(appTheme.NewNoScrollTheme(theme.DefaultTheme()))

	chatApp := app.NewChatApp(application)
	chatApp.Run()
}
