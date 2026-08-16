//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

// FLASHWINFO flags. TRAY is the taskbar button alone — the caption is left
// alone, a title bar strobing behind whatever the user is doing being the
// version of this everybody turns off. TIMERNOFG keeps it flashing until the
// window comes forward, which is what makes it a message waiting rather than a
// blink that is gone before anybody looks.
const (
	flashwTray      = 0x00000002
	flashwTimerNoFG = 0x0000000C
)

// flashInfo is Win32's FLASHWINFO. Size is the struct's own length, which the
// call refuses without.
type flashInfo struct {
	Size    uint32
	HWND    uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
}

var flashWindowEx = syscall.NewLazyDLL("user32.dll").NewProc("FlashWindowEx")

// alertSupported says the platform can signal a window that is not in front,
// which is what keeps the setting off a page where it would do nothing.
const alertSupported = true

// FlashTaskbar flashes a window's taskbar button until it is brought forward.
//
// This is the client's whole out-of-app signal. Windows drops a toast raised
// under an AppUserModelID with no registered shortcut, which an unpackaged build
// has none of — see docs/known-gaps.md — where the taskbar answers to any window
// with a handle. Flashing one that already has focus is a no-op in Windows
// itself, so no caller has to check.
//
// Call on the UI thread: RunNative is the driver's own.
func FlashTaskbar(window fyne.Window) {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return
	}

	native.RunNative(func(ctx any) {
		wc, ok := ctx.(driver.WindowsWindowContext)
		if !ok || wc.HWND == 0 {
			return
		}

		info := flashInfo{
			HWND:  wc.HWND,
			Flags: flashwTray | flashwTimerNoFG,
			Count: 0, // until the window is brought forward, per TIMERNOFG
		}
		info.Size = uint32(unsafe.Sizeof(info))

		flashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
	})
}
