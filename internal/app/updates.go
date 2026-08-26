package app

// The update check: one request to GitHub per run, what it found, and the modal
// that says so. Nothing is downloaded and no binary is replaced — the reader is
// handed the release and the browser does the rest, which is why this file is
// short and `internal/update` imports nothing.

import (
	"context"
	"errors"
	"log"
	"runtime"
	"time"

	"RGOClient/internal/config"
	"RGOClient/internal/ui"
	"RGOClient/internal/update"
)

/* The startup check */

// checkUpdatesOnReady runs the check the settings offer, once per run. It hangs
// off Ready rather than off launch because that is when there is a window to
// report into: the login screens are drawn before the notice layer exists, and a
// client nobody signs into has nowhere to put an answer.
func (a *App) checkUpdatesOnReady() {
	if a.updateAsked || !config.Current().Updates.Check {
		return
	}
	a.updateAsked = true // Ready repeats on every reconnect; the release does not

	a.checkUpdates(a.announceUpdate)
}

// checkUpdates asks GitHub for the newest release and records what it says.
// Single-flighted on the held state's own Checking flag — the startup check and
// the settings button are one request, and pressing the button while the first is
// still out has to wait for it rather than send a second.
//
// onDone runs on the UI thread once the answer is recorded, whatever it was.
// Call on the UI thread.
func (a *App) checkUpdates(onDone func()) {
	if a.updates.Checking {
		return
	}

	// The held answer is kept while the new one is fetched: a section re-checking
	// must not blank the release it is already showing.
	a.updates.Current = a.info.Version
	a.updates.Platform = update.Platform(runtime.GOOS, runtime.GOARCH)
	a.updates.Checking = true
	a.refreshSettingsUpdates()

	agent := "RGOClient/" + a.info.Version

	a.background(func() error {
		release, err := update.Latest(context.Background(), agent)

		// Deliberately not epoch-guarded. What GitHub publishes has nothing to do
		// with which account is signed in, so an answer that outlived its session is
		// still the answer — and leaving the claim set would kill the button for the
		// rest of the run.
		a.doOnUI(func() { a.settleUpdate(release, err, onDone) }, false)

		return nil
	}, func(error) {})
}

// settleUpdate records an answer and redraws whatever is showing one. A failure
// is held here rather than reported as a notice: nobody asked for the startup
// check, so nobody should be interrupted by it going wrong. Call on the UI thread.
func (a *App) settleUpdate(release update.Release, err error, onDone func()) {
	state := ui.UpdateState{
		Current:  a.info.Version,
		Platform: update.Platform(runtime.GOOS, runtime.GOARCH),
		Checked:  time.Now(),
	}

	switch {
	case errors.Is(err, update.ErrNoRelease):
		// A state rather than a failure: there is nothing newer because there is
		// nothing published at all.

	case err != nil:
		log.Printf("update check: %v", err)
		state.Err = "Couldn't reach GitHub to check for updates."

	default:
		state.Available = update.Newer(state.Current, release.Version)
		state.Release = drawnRelease(release)
	}

	a.updates = state
	a.refreshSettingsUpdates()

	if onDone != nil {
		onDone()
	}
}

// drawnRelease is a release as the settings page draws it, with this machine's
// own asset already picked out — which platform gets which file is
// `internal/update`'s to say, and `ui` never sees a GOOS.
func drawnRelease(release update.Release) ui.UpdateRelease {
	drawn := ui.UpdateRelease{
		Version:   release.Version,
		Notes:     release.Notes,
		PageURL:   release.URL,
		Published: release.Published,
	}

	if asset, ok := release.AssetFor(runtime.GOOS, runtime.GOARCH); ok {
		drawn.AssetURL, drawn.AssetName, drawn.AssetSize = asset.URL, asset.Name, asset.Size
	}

	return drawn
}

/* Saying so */

// announceUpdate raises the modal the startup check earned, at most once per
// release. The version announced is persisted rather than kept in memory: a
// client relaunched twice a day would otherwise interrupt twice a day over one
// release, which is the difference between telling somebody and nagging them.
//
// An unstamped working tree is never announced to. It is behind every release by
// definition, and saying so on every `go build` is noise rather than news.
// Call on the UI thread.
func (a *App) announceUpdate() {
	release := a.updates.Release
	if !a.updates.Available || !update.Comparable(a.updates.Current) {
		return
	}
	if config.Current().Updates.Announced == release.Version {
		return
	}
	a.updateSettings(func(s *config.Settings) { s.Updates.Announced = release.Version })

	a.confirm(ui.Confirm{
		Title:     "Update available",
		Body:      "RGOClient " + release.Version + " is out. This build is " + a.updates.Current + ".",
		Action:    "See what's new",
		Tone:      ui.ToneInfo,
		OnConfirm: func() { a.openSettingsAt(ui.SectionUpdates) },
	})
}

// refreshSettingsUpdates redraws the Updates section when it is what is on
// screen, an answer landing long after whatever asked for it. Call on the UI
// thread.
func (a *App) refreshSettingsUpdates() {
	if a.settings == nil {
		return
	}

	a.settings.RefreshUpdates()
}
