//go:build !(linux || freebsd || openbsd || netbsd)

package ui

// trayAvailable is true where the notification area is part of the OS rather
// than something a desktop may or may not run: Windows always has one, and so
// does the macOS menu bar. An icon may still sit in an overflow the user has to
// open, which is a place, not an absence.
func trayAvailable() bool { return true }
