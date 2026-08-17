// RGOClient, a desktop chat client for Revolt/Stoat.
// Copyright (C) 2026 sentinelb51
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version. It is distributed WITHOUT ANY WARRANTY; see the GNU General Public
// License in LICENSE, or <https://www.gnu.org/licenses/>, for details.
//
// The embedded Montserrat cuts under assets/fonts are not covered by that
// licence: they stay under the SIL Open Font License 1.1 (assets/fonts/OFL.txt).
package main

import (
	"log"
	"strconv"

	"RGOClient/internal/app"
	"RGOClient/internal/config"
	apptheme "RGOClient/internal/ui/theme"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"
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

	// The settings are read before the first widget is built: the palette and the
	// size table are applied from them here, and everything above reads those
	// tables at construction.
	if err := config.Load(); err != nil {
		log.Printf("load settings: %v", err)
	}
	settings := config.Current()
	apptheme.Apply(settings.Styles.Colors, settings.Styles.Sizes)
	apptheme.SetFontSize(settings.Interface.FontSize)

	fyneApp := fyneapp.NewWithID(appID)
	fyneApp.Settings().SetTheme(apptheme.NewAppTheme(theme.DefaultTheme()))

	app.New(fyneApp, app.Info{Version: version, Build: build}).Run()
}
