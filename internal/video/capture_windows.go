package video

// Windows capture is gdigrab — CPU BitBlt, works on every machine — and
// enumeration is Win32 through x/sys, the cpu package's precedent: no cgo,
// the procs declared by hand where x/sys stops short. Monitors come from
// EnumDisplayMonitors in virtual-screen coordinates, which is exactly the
// space gdigrab's desktop offsets are in; windows from EnumWindows, filtered
// to what a picker should offer — visible, titled, not a tool window, not
// cloaked, not minimised (gdigrab reads garbage off an iconified window).
//
// gdigrab names a window by its exact title, there being no id form: a title
// that changes between enumeration and start misses, and one that changes
// mid-share keeps capturing — FindWindow runs once, at the child's start.

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	dwmapi                  = windows.NewLazySystemDLL("dwmapi.dll")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowLongW      = user32.NewProc("GetWindowLongW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procDwmGetWindowAttr    = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	wsExToolWindow = 0x00000080
	dwmaCloaked    = 14
	monitorPrimary = 0x1
)

// gwlExStyle is GWL_EXSTYLE (-20) as GetWindowLongW's uintptr argument reads
// it: user32 takes the index as a 32-bit int, so the negative rides the low
// word.
var gwlExStyle = uintptr(uint32(0xFFFFFFEC))

type winRect struct {
	Left, Top, Right, Bottom int32
}

type monitorInfoEx struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
	Device  [64]uint16
}

// enumMu serialises the two enumerations: their Win32 callbacks are created
// once — NewCallback slots are never freed, so one per call would exhaust the
// process's small allowance — and write into the package-level slices below.
var (
	enumMu       sync.Mutex
	enumMonitors []CaptureSource
	enumWindows  []CaptureSource
)

var monitorCallback = syscall.NewCallback(func(hMonitor, hdc uintptr, rect *winRect, lparam uintptr) uintptr {
	var info monitorInfoEx
	info.Size = uint32(unsafe.Sizeof(info))

	if r, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&info))); r == 0 {
		return 1
	}

	m := info.Monitor
	title := fmt.Sprintf("Monitor %d (%d×%d)", len(enumMonitors)+1, m.Right-m.Left, m.Bottom-m.Top)
	if info.Flags&monitorPrimary != 0 {
		title += " — primary"
	}

	enumMonitors = append(enumMonitors, CaptureSource{
		Kind:  CaptureMonitor,
		Title: title,
		X:     int(m.Left), Y: int(m.Top),
		Width: int(m.Right - m.Left), Height: int(m.Bottom - m.Top),
	})

	return 1
})

var windowCallback = syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		return 1
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		return 1
	}
	if style, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle); style&wsExToolWindow != 0 {
		return 1
	}

	// A cloaked window is one DWM is not drawing — another virtual desktop, a
	// suspended UWP app — which captures as black.
	var cloaked uint32
	_, _, _ = procDwmGetWindowAttr.Call(hwnd, dwmaCloaked,
		uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
	if cloaked != 0 {
		return 1
	}

	var buf [256]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return 1
	}
	title := windows.UTF16ToString(buf[:n])

	var rect winRect
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); r == 0 {
		return 1
	}
	width, height := int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
	if width < 64 || height < 64 {
		return 1 // too small to be anything anybody means to share
	}

	enumWindows = append(enumWindows, CaptureSource{
		ID:    title, // gdigrab addresses a window by exact title
		Kind:  CaptureWindow,
		Title: title,
		Width: width, Height: height,
	})

	return 1
})

// ShareSources lists every monitor and then every window worth offering.
func ShareSources() ([]CaptureSource, error) {
	enumMu.Lock()
	defer enumMu.Unlock()

	enumMonitors, enumWindows = nil, nil
	_, _, _ = procEnumDisplayMonitors.Call(0, 0, monitorCallback, 0)
	_, _, _ = procEnumWindows.Call(windowCallback, 0)

	sources := append(enumMonitors, enumWindows...)
	enumMonitors, enumWindows = nil, nil
	if len(sources) == 0 {
		return nil, errors.New("nothing to capture was found")
	}

	return sources, nil
}

/* The grab */

// grabArgs is the gdigrab input: the whole desktop at a monitor's own offset
// and size — virtual-screen coordinates, negative for a monitor left of or
// above the primary — or one window by title.
func grabArgs(cfg CaptureConfig) ([]string, error) {
	args := []string{"-f", "gdigrab", "-framerate", fmt.Sprint(cfg.FPS)}

	switch cfg.Source.Kind {
	case CaptureWindow:
		if cfg.Source.ID == "" {
			return nil, errors.New("video: the window has no title to find it by")
		}

		return append(args, "-i", "title="+cfg.Source.ID), nil

	case CaptureMonitor:
		args = append(args,
			"-offset_x", fmt.Sprint(cfg.Source.X), "-offset_y", fmt.Sprint(cfg.Source.Y),
			"-video_size", fmt.Sprintf("%dx%d", cfg.Source.Width, cfg.Source.Height),
			"-i", "desktop")

		return args, nil
	}

	return nil, fmt.Errorf("video: unknown capture kind %d", cfg.Source.Kind)
}

// captureAttrs is platformAttrs minus the restricted token: a low-integrity
// child cannot BitBlt other programs' windows, and the input is this
// machine's own screen — containment is the job object's.
func captureAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.BELOW_NORMAL_PRIORITY_CLASS,
	}
}

// hardenCapture is the same job object playback children get — memory cap,
// no grandchildren, kill on the client dying — which caps no CPU seconds.
func hardenCapture(cmd *exec.Cmd) func() {
	return harden(cmd)
}
