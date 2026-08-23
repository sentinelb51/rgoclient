//go:build windows

package ui

import "syscall"

// Push-to-talk asks a question `desktop.Canvas`'s key hooks cannot answer: is
// this key held *right now*, while the composer holds canvas focus. glfw hands a
// key to the focused widget instead of to the canvas, and the composer is focused
// for most of the client's life — so a key tracked there is missed exactly when
// somebody is talking and typing at once.
//
// GetAsyncKeyState asks the platform directly and needs no focus at all, which is
// the same reason ShiftHeld uses it. See the modifier-key footgun in CLAUDE.md.
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

// pushToTalkKeys is what the settings page offers, in the order it lists them.
// A curated list rather than a captured key: reading an arbitrary key needs
// canvas focus, which is the problem this file exists to route around, and these
// are the keys people actually bind. Names are stored in the settings file, so
// renaming one silently unbinds whatever the reader chose.
//
// The virtual-key codes are Windows' own.
var pushToTalkKeys = []hotkey{
	{"Left Ctrl", 0xA2},
	{"Right Ctrl", 0xA3},
	{"Left Alt", 0xA4},
	{"Right Alt", 0xA5},
	{"Left Shift", 0xA0},
	{"Right Shift", 0xA1},
	{"Caps Lock", 0x14},
	{"Mouse 4", 0x05},
	{"Mouse 5", 0x06},
	{"F13", 0x7C},
}

type hotkey struct {
	Name string
	code uintptr
}

// PushToTalkKeys is the list the settings page draws, and the only place the
// names come from.
func PushToTalkKeys() []string {
	names := make([]string, 0, len(pushToTalkKeys))
	for _, key := range pushToTalkKeys {
		names = append(names, key.Name)
	}

	return names
}

// KeyHeld reports whether the named key is down. An unknown name — a setting
// written by an older build, or by hand — is never held, so a call falls back to
// silence rather than to a stuck-open microphone.
//
// Polled rather than hooked: a global keyboard hook is a message pump of its own
// and reads as a keylogger to every endpoint product on the machine.
func KeyHeld(name string) bool {
	for _, key := range pushToTalkKeys {
		if key.Name != name {
			continue
		}

		// The high bit is "down now"; the low bit is "was pressed since the last
		// call" and is deliberately ignored — this is a state, not an event.
		state, _, _ := procGetAsyncKeyState.Call(key.code)

		return state&0x8000 != 0
	}

	return false
}

// PushToTalkSupported is whether this platform can answer KeyHeld at all. The
// settings page hides the mode where it cannot, rather than offering one that
// silently behaves as voice activity.
const PushToTalkSupported = true
