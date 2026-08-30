//go:build linux || freebsd || openbsd || netbsd

package ui

import "github.com/godbus/dbus/v5"

// statusNotifierWatcher is the bus name a desktop's notification area registers.
// Fyne's tray is a StatusNotifierItem, so this name having an owner is the whole
// of whether an icon would appear: without a host — GNOME without the
// AppIndicator extension is the common one — systray registers and nothing draws
// it.
const statusNotifierWatcher = "org.kde.StatusNotifierWatcher"

// trayAvailable asks the session bus whether anything is listening for tray
// icons. A failure of any kind is a no: an icon that cannot be drawn is the one
// case the client must not hide a window behind.
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}

	// Not conn.Close(): SessionBus hands out a shared connection the toolkit's own
	// tray goes on to use, and closing it here would take that down with it.

	var owned bool
	call := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, statusNotifierWatcher)
	if call.Err != nil {
		return false
	}
	if err := call.Store(&owned); err != nil {
		return false
	}

	return owned
}
