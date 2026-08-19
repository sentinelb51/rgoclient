//go:build !windows

package ui

import "fyne.io/fyne/v2"

// PickFile shows nothing here and reports false, so the caller falls back to
// Fyne's in-canvas browser. Only Windows has the shell dialog wired up — see
// docs/known-gaps.md.
func PickFile(_ fyne.Window, _ string, _ FileFilter, _ func(path string, err error)) bool {
	return false
}

// PickFolder shows nothing here and reports false, on the same terms as PickFile.
func PickFolder(_ fyne.Window, _ string, _ func(path string, err error)) bool {
	return false
}
