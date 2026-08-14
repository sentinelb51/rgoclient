//go:build windows

package ui

import "syscall"

// vkShift is Win32's virtual-key code for either Shift key, and vkDown the bit
// GetAsyncKeyState sets while a key is held. Its low bit answers a different
// question — whether the key has been pressed since the last call — and is
// deliberately not read: a confirmation is about the key being down *now*.
const (
	vkShift = 0x10
	vkDown  = 0x8000
)

// shiftSkippable says a confirmation can be skipped by holding Shift here, which
// is what puts the hint on the card.
const shiftSkippable = true

var getAsyncKeyState = syscall.NewLazyDLL("user32.dll").NewProc("GetAsyncKeyState")

// ShiftHeld reports whether either Shift key is down at this moment.
//
// Fyne cannot answer this. A desktop.Canvas's key handlers fire only while
// nothing holds focus, and the composer holds it for most of the client's life,
// so a modifier tracked from them would be missed by exactly the clicks that want
// it; a pointer event carries its modifiers, but a context-menu item is Fyne's
// own widget and reports none. So the platform is asked directly, at the moment
// of the click rather than tracked.
func ShiftHeld() bool {
	state, _, _ := getAsyncKeyState.Call(vkShift)

	return state&vkDown != 0
}
