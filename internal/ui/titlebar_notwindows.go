//go:build !windows

package ui

import "fyne.io/fyne/v2"

// StyleTitlebar is a no-op on platforms without a recolorable native title bar.
// It returns true so callers don't retry.
func StyleTitlebar(_ fyne.Window) bool { return true }
