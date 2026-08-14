//go:build !windows

package ui

// shiftSkippable is false where the key cannot be read, so no card offers a way
// out that would never be taken.
const shiftSkippable = false

// ShiftHeld reports nothing on platforms with no way to ask for a key the client
// is not being typed into — see the Windows half. Every confirmation is asked,
// which is the safe answer for a question about doing something irreversible.
func ShiftHeld() bool { return false }
