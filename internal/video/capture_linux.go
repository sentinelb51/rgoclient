package video

// Linux capture is x11grab, and enumeration is the X server asked directly —
// ffmpeg lists nothing. jezek/xgb is a pure-Go X connection of our own (the
// toolkit's belongs to glfw and is not reachable): monitors come from RandR's
// CRTCs and windows from the WM's EWMH client list. Pure Wayland has no
// grabber in stock ffmpeg at all — the portal is its own project — so a
// machine with no X answers with no sources and the picker says so; under
// XWayland the X half of the world is capturable and the Wayland half simply
// is not there to list.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/sys/unix"
)

// ShareSources lists what this machine can share: every enabled monitor, then
// every window the window manager admits to. An error is the display being
// unreachable — Wayland without XWayland, or no session at all.
func ShareSources() ([]CaptureSource, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, errors.New("no X display to capture — Wayland capture is not supported yet")
	}
	defer conn.Close()

	screen := xproto.Setup(conn).DefaultScreen(conn)

	sources := monitorSources(conn, screen)
	sources = append(sources, windowSources(conn, screen)...)

	return sources, nil
}

// monitorSources is RandR's answer, falling back to the whole root screen as
// one monitor where the extension is missing — a bare Xvfb still shares.
func monitorSources(conn *xgb.Conn, screen *xproto.ScreenInfo) []CaptureSource {
	whole := CaptureSource{
		Kind:  CaptureMonitor,
		Title: "Whole screen",
		Width: int(screen.WidthInPixels), Height: int(screen.HeightInPixels),
	}

	if randr.Init(conn) != nil {
		return []CaptureSource{whole}
	}
	res, err := randr.GetScreenResourcesCurrent(conn, screen.Root).Reply()
	if err != nil {
		return []CaptureSource{whole}
	}

	var monitors []CaptureSource
	for _, crtc := range res.Crtcs {
		info, err := randr.GetCrtcInfo(conn, crtc, res.ConfigTimestamp).Reply()
		if err != nil || info.Width == 0 || info.Height == 0 {
			continue // a CRTC with nothing driving it
		}

		monitors = append(monitors, CaptureSource{
			Kind:  CaptureMonitor,
			Title: fmt.Sprintf("Monitor %d (%d×%d)", len(monitors)+1, info.Width, info.Height),
			X:     int(info.X), Y: int(info.Y),
			Width: int(info.Width), Height: int(info.Height),
		})
	}

	if len(monitors) == 0 {
		return []CaptureSource{whole}
	}

	return monitors
}

// windowSources walks the EWMH client list. A WM that keeps none answers with
// no windows rather than an error — monitors still stand.
func windowSources(conn *xgb.Conn, screen *xproto.ScreenInfo) []CaptureSource {
	clientList, err := internAtom(conn, "_NET_CLIENT_LIST")
	if err != nil {
		return nil
	}
	reply, err := xproto.GetProperty(conn, false, screen.Root, clientList,
		xproto.AtomWindow, 0, 1<<16).Reply()
	if err != nil || reply.Format != 32 {
		return nil
	}

	utf8Name, _ := internAtom(conn, "_NET_WM_NAME")
	utf8String, _ := internAtom(conn, "UTF8_STRING")

	var windows []CaptureSource
	for i := 0; i+4 <= len(reply.Value); i += 4 {
		win := xproto.Window(uint32(reply.Value[i]) | uint32(reply.Value[i+1])<<8 |
			uint32(reply.Value[i+2])<<16 | uint32(reply.Value[i+3])<<24)

		title := windowName(conn, win, utf8Name, utf8String)
		if title == "" {
			continue
		}
		geo, err := xproto.GetGeometry(conn, xproto.Drawable(win)).Reply()
		if err != nil || geo.Width < 64 || geo.Height < 64 {
			continue // too small to be anything anybody means to share
		}

		windows = append(windows, CaptureSource{
			ID:    fmt.Sprintf("0x%x", uint32(win)),
			Kind:  CaptureWindow,
			Title: title,
			Width: int(geo.Width), Height: int(geo.Height),
		})
	}

	return windows
}

func internAtom(conn *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, true, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}

	return reply.Atom, nil
}

// windowName prefers the EWMH UTF-8 name and falls back to the ancient
// WM_NAME, which is what a bare X11 program still sets.
func windowName(conn *xgb.Conn, win xproto.Window, utf8Name, utf8String xproto.Atom) string {
	if utf8Name != 0 && utf8String != 0 {
		reply, err := xproto.GetProperty(conn, false, win, utf8Name, utf8String, 0, 256).Reply()
		if err == nil && len(reply.Value) > 0 {
			return string(reply.Value)
		}
	}

	reply, err := xproto.GetProperty(conn, false, win, xproto.AtomWmName, xproto.AtomString, 0, 256).Reply()
	if err == nil && len(reply.Value) > 0 {
		return string(reply.Value)
	}

	return ""
}

/* The grab */

// grabArgs is the x11grab input: a monitor is a region of the root at its own
// offset, a window is the drawable itself. An occluded window captures
// whatever X holds for it — garbage without a compositor — which is every X11
// capturer's limit, stated in the picker rather than fixed here.
func grabArgs(cfg CaptureConfig) ([]string, error) {
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0"
	}

	args := []string{"-f", "x11grab", "-framerate", fmt.Sprint(cfg.FPS)}

	switch cfg.Source.Kind {
	case CaptureWindow:
		if cfg.Source.ID == "" {
			return nil, errors.New("video: the window has no id")
		}

		return append(args, "-window_id", cfg.Source.ID, "-i", display), nil

	case CaptureMonitor:
		args = append(args,
			"-video_size", fmt.Sprintf("%dx%d", cfg.Source.Width, cfg.Source.Height),
			"-i", fmt.Sprintf("%s+%d,%d", display, cfg.Source.X, cfg.Source.Y))

		return args, nil
	}

	return nil, fmt.Errorf("video: unknown capture kind %d", cfg.Source.Kind)
}

// captureAttrs has nothing to set on Linux; the priority lands in
// hardenCapture, the process already running.
func captureAttrs(cmd *exec.Cmd) {}

// hardenCapture is the resource half of harden with the CPU-seconds cap left
// off: an encoder spends an hour of CPU on an afternoon of sharing, which is
// exactly what RLIMIT_CPU would kill mid-call.
func hardenCapture(cmd *exec.Cmd) func() {
	pid := cmd.Process.Pid

	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, videoNice)
	capLimit(pid, unix.RLIMIT_AS, childMemoryCap)
	capLimit(pid, unix.RLIMIT_CORE, 0)
	capLimit(pid, unix.RLIMIT_FSIZE, 0) // stdout is a pipe, which FSIZE does not count

	return nil
}
