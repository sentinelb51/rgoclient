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
	TODO: Notification toasts for warnings; slide from bottom right, 5 dots each counts every second, click to dismiss, auto-dismiss after 5s (5 dots)
*/

// appID identifies the client to Fyne's preferences and storage APIs. Without
// it, Fyne invents a throwaway ID per launch and logs an error.
const appID = "com.sentinelb51.rgoclient"

// version and build are stamped at link time by the CI workflows
// (-X main.version=... -X main.build=...). Versions are calendar-based,
// YY.M.D with no zero padding (26.7.29), so a version reads as the date it
// shipped; a second release on the same day gets a fourth component
// (26.7.29.1). build is the workflow run number, unique across every build.
//
// These defaults are what a plain `go build` produces locally, and are what
// marks a binary as an unreleased local build.
var (
	version = "0.0.0"
	build   = "0"
)

func main() {
	// Metadata is set in code rather than through FyneApp.toml: the toml is only
	// read for development builds, and only from the working directory or the
	// binary's own directory, so a packaged client would silently lose it.
	//
	// The fyneDo migration flag declares that this app never touches widgets off
	// the UI thread — every gateway handler and worker goroutine goes through
	// App.doOnUI / ui.DoOnUI. Without it Fyne assumes the legacy threading model
	// and keeps its compatibility layer (and the startup warning) in place.
	buildNumber, err := strconv.Atoi(build)
	if err != nil {
		buildNumber = 0
	}

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
