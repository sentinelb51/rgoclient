package main

import (
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"

	"RGOClient/internal/app"
	apptheme "RGOClient/internal/ui/theme"
)

func main() {
	fyneApp := fyneapp.New()
	fyneApp.Settings().SetTheme(apptheme.NewNoScrollTheme(theme.DefaultTheme()))

	app.New(fyneApp).Run()
}
