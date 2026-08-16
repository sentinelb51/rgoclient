//go:build !windows

package ui

import "fyne.io/fyne/v2"

// alertSupported is false where there is no way to signal a window that is not
// in front, which is what keeps the setting off the page rather than offering
// one that does nothing.
const alertSupported = false

// FlashTaskbar does nothing here. Fyne exposes no attention request, and the
// platforms behind this build tag each want a different one — see
// docs/known-gaps.md.
func FlashTaskbar(window fyne.Window) {}
