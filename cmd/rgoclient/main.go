package main

import (
	"strconv"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"

	"RGOClient/internal/app"
	apptheme "RGOClient/internal/ui/theme"
)

/*
	TODO: Login screen, hovering over an active session; highlight is partial, should highlight the whole tile
*/

// appID identifies the client to Fyne's preferences and storage APIs. Without it
// Fyne invents a throwaway ID per launch and logs an error.
const appID = "com.sentinelb51.rgoclient"

// version and build are stamped at link time by CI (-X main.version=...).
// Versions are calendar-based, YY.M.D with no zero padding, so a version reads as
// the date it shipped; build is the workflow run number. These defaults are what
// a plain `go build` produces, marking an unreleased local build.
var (
	version = "0.0.0"
	build   = "0"
)

func main() {
	buildNumber, err := strconv.Atoi(build)
	if err != nil {
		buildNumber = 0
	}

	// Metadata is set in code rather than through FyneApp.toml, which is only read
	// for development builds and only from the working or binary directory, so a
	// packaged client would silently lose it.
	//
	// The fyneDo migration declares that this app never touches widgets off the UI
	// thread — everything goes through App.doOnUI / ui.DoOnUI. Without it Fyne
	// keeps its legacy compatibility layer, and the startup warning, in place.
	fyneapp.SetMetadata(fyne.AppMetadata{
		ID:         appID,
		Name:       "RGOClient",
		Version:    version,
		Build:      buildNumber,
		Migrations: map[string]bool{"fyneDo": true},
	})

	fyneApp := fyneapp.NewWithID(appID)
	fyneApp.Settings().SetTheme(apptheme.NewAppTheme(theme.DefaultTheme()))

	app.New(fyneApp).Run()
}
