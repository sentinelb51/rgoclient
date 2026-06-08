//go:build windows

package ui

import (
	"image/color"
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"

	"RGOClient/internal/ui/theme"
)

// DWM window attributes (Windows 11, build 22000+). They let us recolor the
// native title bar to match the app palette without going borderless.
const (
	dwmwaUseImmersiveDarkMode = 20 // force dark chrome regardless of system mode
	dwmwaBorderColor          = 34 // window border color
	dwmwaCaptionColor         = 35 // title bar background color
	dwmwaTextColor            = 36 // title bar text color
)

// colorRef converts a color to a Win32 COLORREF (0x00BBGGRR).
func colorRef(c color.Color) uint32 {
	r, g, b, _ := c.RGBA()
	return r>>8 | g>>8<<8 | b>>8<<16
}

// StyleTitlebar recolors the native Windows title bar to match the Cool Slate
// palette via DWM. It returns true once the native window handle is available
// and the styling has been applied (or there is nothing to do), so callers can
// stop retrying.
func StyleTitlebar(win fyne.Window) bool {
	native, ok := win.(driver.NativeWindow)
	if !ok {
		return true // no native handle to style; don't keep retrying
	}

	applied := false
	native.RunNative(func(ctx any) {
		wc, ok := ctx.(driver.WindowsWindowContext)
		if !ok || wc.HWND == 0 {
			return // window not realized yet — caller should retry
		}

		setAttr := syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
		set := func(attr uintptr, value *uint32) {
			setAttr.Call(wc.HWND, attr, uintptr(unsafe.Pointer(value)), 4)
		}

		dark := uint32(1)
		caption := colorRef(theme.Colors.ServerListBackground)
		text := colorRef(theme.Colors.TextPrimary)
		border := colorRef(theme.Colors.ServerListBackground)

		set(dwmwaUseImmersiveDarkMode, &dark)
		set(dwmwaCaptionColor, &caption)
		set(dwmwaTextColor, &text)
		set(dwmwaBorderColor, &border)
		applied = true
	})
	return applied
}
