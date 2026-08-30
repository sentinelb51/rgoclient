package app

// The client's presence on the machine rather than in Revolt: the icon beside
// the clock, what the window's close button does, and the one teardown every way
// out of the process runs.

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/systray"

	"RGOClient/internal/config"
	"RGOClient/internal/ui"
)

// startSystem mounts the notification-area icon and takes the window's close
// button. Called from Run before the loop starts, which is where Fyne wants it:
// the driver executes what a tray set-up queues inline while it is still on the
// main goroutine, and the icon has to exist before a window can be hidden behind
// it.
//
// A desktop with no notification area is left with a plain close button — see
// ui.TrayAvailable. Nothing about that is reported: the settings row says it, on
// the page where somebody is looking for the setting.
func (a *App) startSystem() {
	a.window.SetCloseIntercept(a.closeRequested)

	if !ui.TrayAvailable() {
		return
	}

	desk, ok := a.fyne.(desktop.App)
	if !ok {
		return
	}

	// IsQuit rather than the label alone: Fyne appends a Quit item of its own
	// unless the last one is marked, and it recognises an unmarked one by
	// comparing against the *translated* word — so on a machine in another
	// language the menu would carry two of them.
	quit := fyne.NewMenuItem("Quit", a.fyne.Quit)
	quit.IsQuit = true

	desk.SetSystemTrayMenu(fyne.NewMenu(windowTitle,
		fyne.NewMenuItem("Open "+windowTitle, a.showWindow),
		fyne.NewMenuItemSeparator(),
		quit,
	))

	// What the icon is called under the pointer. Fyne names the tray only on the
	// desktops whose implementation crashes without one, so everywhere else an icon
	// beside a dozen others answers a hover with nothing. Safe here rather than
	// deferred: SetSystemTrayMenu registers the icon before it returns.
	systray.SetTooltip(windowTitle)

	// The click on the icon itself, which Fyne only offers through the window it is
	// pointed at — and pointing it there overwrites the close intercept with a bare
	// Hide, so ours goes back on afterwards or the window would close to the tray
	// whatever the setting says.
	desk.SetSystemTrayWindow(a.window)
	a.window.SetCloseIntercept(a.closeRequested)

	a.trayReady = true
}

// closeRequested is the window's close button. With an icon to go behind, and
// the setting on, closing the window hides it and the client keeps running;
// otherwise closing is quitting.
//
// Quit rather than Close: once a tray is up Fyne holds a hidden window of its
// own, so the last window closing no longer ends the loop, and the client would
// go on running with nothing on screen and no way back to it.
func (a *App) closeRequested() {
	if a.trayReady && config.Current().System.CloseToTray {
		a.window.Hide()
		return
	}

	a.fyne.Quit()
}

// showWindow brings the client back from the notification area — the icon's own
// click, and the first item of its menu. RequestFocus as well as Show: a hidden
// window is restored where it was, behind whatever has been opened over it since.
func (a *App) showWindow() {
	a.window.Show()
	a.window.RequestFocus()
}

// shutdown is what every way out of the process runs: the close button, the
// tray's Quit, a signal. It hangs off the toolkit's stopped hook rather than the
// window's SetOnClosed, which fires for a window being closed and not for the app
// being quit — with a tray up, the second is the common one.
//
// Off the UI thread, the loop having already ended. Nothing here touches a
// widget.
func (a *App) shutdown() {
	if err := config.Save(); err != nil {
		log.Printf("save settings: %v", err)
	}

	a.images.Shutdown()
	a.emojis.Shutdown()
	a.sounds.Close()
	a.client.Shutdown()
}
