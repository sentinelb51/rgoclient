package video

// Windows capture is Windows Graphics Capture — ffmpeg's gfxcapture filter
// source — for a monitor and for a window alike, with two older grabbers
// behind it. Enumeration is Win32 through x/sys, the cpu package's
// precedent: no cgo, the procs declared by hand where x/sys stops short.
// Monitors come from EnumDisplayMonitors in virtual-screen coordinates, which
// is the space gdigrab's desktop offsets are in; windows from EnumWindows,
// filtered to what a picker should offer — visible, named, not a tool window,
// not cloaked. **Minimised is offered**, which is the shell's own alt-tab
// rule: a fullscreen game is minimised by Windows the instant it loses the
// foreground, and losing the foreground is what opening the picker does, so
// rejecting iconic handles hid the one window the picker was opened for.
//
// A source is addressed by **handle**: Graphics Capture takes the HMONITOR or
// the HWND itself, so a window whose title changes between enumeration and
// start is still the window that was picked. Only the gdigrab floor needs the
// title, FindWindow having no id form.
//
// The order is a performance answer as much as a correctness one, and each
// step down is a real loss:
//
//   - **gfxcapture** hands back a D3D11 surface DWM already has, and is the
//     only one of the three that will *scale on the GPU* — so the readback
//     is the encode box rather than the whole screen, which is where most of
//     the CPU in a share went. It also captures a window that is covered,
//     and several clients may capture one output at once.
//   - **ddagrab** is Desktop Duplication: a GPU surface too, but full-size
//     and monitors only. Kept because it predates gfxcapture in both ffmpeg
//     and Windows, and because DDA answers in a session WGC may not.
//   - **gdigrab** is a CPU BitBlt carrying CAPTUREBLT, which makes the whole
//     desktop's pointer flicker once per captured frame — visible to
//     everybody at the machine, not only to the viewer. The floor, warned
//     about in the picker; see docs/known-gaps.md.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	dwmapi                  = windows.NewLazySystemDLL("dwmapi.dll")
	dxgi                    = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory   = dxgi.NewProc("CreateDXGIFactory")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowLongW      = user32.NewProc("GetWindowLongW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetWindowPlacement  = user32.NewProc("GetWindowPlacement")
	procGetWindow           = user32.NewProc("GetWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procDwmGetWindowAttr    = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	wsExToolWindow   = 0x00000080
	dwmaCloaked      = 14
	gwOwner          = 4
	swShowNoActivate = 4
	monitorPrimary   = 0x1

	// minShareSide is the smallest edge worth offering: below it a window is
	// a tooltip, a splash or a shadow rather than anything a reader means.
	minShareSide = 64
)

// gwlExStyle is GWL_EXSTYLE (-20) as GetWindowLongW's uintptr argument reads
// it: user32 takes the index as a 32-bit int, so the negative rides the low
// word.
var gwlExStyle = uintptr(uint32(0xFFFFFFEC))

type winRect struct {
	Left, Top, Right, Bottom int32
}

type winPoint struct {
	X, Y int32
}

// windowPlacement is WINDOWPLACEMENT, which is where a minimised window's
// real rectangle is kept. Length is checked, so the struct has to be the 44
// bytes the header describes — every field is 4-byte aligned, so Go lays it
// out the same.
type windowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    winPoint
	MaxPosition    winPoint
	NormalPosition winRect
}

type monitorInfoEx struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32

	// CCHDEVICENAME wide chars, and the width matters: GetMonitorInfoW
	// refuses any cbSize but the two structs it knows, so a padded Go
	// struct fails silently and every monitor is skipped.
	Device [32]uint16
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
		ID:    strconv.FormatUint(uint64(hMonitor), 10),
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
	if style, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle); style&wsExToolWindow != 0 {
		return 1
	}

	// A cloaked window is one DWM holds no content for — another virtual
	// desktop, a suspended UWP app, or one the program cloaked itself — and
	// all three capture as black. Minimised is deliberately *not* this: the
	// content is still there, and Graphics Capture delivers again the moment
	// the window is back, which is exactly the shape of sharing a game.
	var cloaked uint32
	_, _, _ = procDwmGetWindowAttr.Call(hwnd, dwmaCloaked,
		uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
	if cloaked != 0 {
		return 1
	}

	iconic, _, _ := procIsIconic.Call(hwnd)

	width, height, ok := windowSize(hwnd, iconic != 0)
	if !ok || width < minShareSide || height < minShareSide {
		return 1 // too small to be anything anybody means to share
	}

	title := windowTitle(hwnd)
	if title == "" {
		return 1
	}

	enumWindows = append(enumWindows, CaptureSource{
		ID:    strconv.FormatUint(uint64(hwnd), 10),
		Kind:  CaptureWindow,
		Title: title,
		Width: width, Height: height,
		Minimised: iconic != 0,
	})

	return 1
})

// windowSize is a window's own rectangle, or — where it is minimised — the
// one it will come back to. GetWindowRect answers a minimised window with the
// slot its icon used to live in, around −32000 and 160 square: neither a size
// nor a refusal, so a minimised window would otherwise be offered at a shape
// it has never had.
//
// A window minimised out of maximised comes back larger than rcNormalPosition
// says. Only the aspect ratio is read from this (gfxFit) and the picker prints
// it, so the cost of that case is a line of text rather than a wrong box.
func windowSize(hwnd uintptr, iconic bool) (width, height int, ok bool) {
	if iconic {
		var placement windowPlacement
		placement.Length = uint32(unsafe.Sizeof(placement))
		if r, _, _ := procGetWindowPlacement.Call(hwnd,
			uintptr(unsafe.Pointer(&placement))); r == 0 {
			return 0, 0, false
		}

		at := placement.NormalPosition

		return int(at.Right - at.Left), int(at.Bottom - at.Top), true
	}

	var rect winRect
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); r == 0 {
		return 0, 0, false
	}

	return int(rect.Right - rect.Left), int(rect.Bottom - rect.Top), true
}

// wakeSource brings a minimised window back so there is something to capture.
// SHOWNOACTIVATE rather than RESTORE: the window has to be composed for
// Graphics Capture to hold anything for it, but taking the foreground is the
// reader's to do — this runs while they are still looking at the client.
//
// A game Windows minimised on losing focus may go straight back down, and
// nothing here can stop it; that case ends as a stalled share, which is what
// ShareTee.Idle is for.
func wakeSource(source CaptureSource) {
	if source.Kind != CaptureWindow || !source.Minimised {
		return
	}

	handle := sourceHandle(source.ID)
	if handle == 0 {
		return
	}

	_, _, _ = procShowWindow.Call(handle, swShowNoActivate)
}

// windowTitle is what a window is offered as: its caption, or — where it has
// none and answers to nothing above it — the program behind it. A game draws
// into a caption-less WS_POPUP as often as into a titled window, and a source
// nobody can name is a source nobody can pick. An owned window is held to its
// caption instead: a nameless dialog is its parent's business, and naming it
// after the program would list one program several times over.
func windowTitle(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n > 0 {
		return windows.UTF16ToString(buf[:n])
	}

	if owner, _, _ := procGetWindow.Call(hwnd, gwOwner); owner != 0 {
		return ""
	}

	return windowProgram(hwnd)
}

// windowProgram is the file name of the program a window belongs to, empty
// where it cannot be had. QUERY_LIMITED_INFORMATION is the right of the two:
// it is granted across integrity levels, so a game running elevated under an
// anti-cheat still answers.
func windowProgram(hwnd uintptr) string {
	var pid uint32
	if _, _, _ = procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid))); pid == 0 {
		return ""
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}

	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

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

/* Which DXGI output a monitor is */

// iidIDXGIFactory is {7b7166ec-21c7-44ae-b21a-c9ae321ae369}. The plain
// factory rather than one of its numbered successors: EnumAdapters is on it,
// and nothing below needs a method the later ones added.
var iidIDXGIFactory = windows.GUID{
	Data1: 0x7b7166ec, Data2: 0x21c7, Data3: 0x44ae,
	Data4: [8]byte{0xb2, 0x1a, 0xc9, 0xae, 0x32, 0x1a, 0xe3, 0x69},
}

// comObject is any of the three DXGI interfaces walked below, which are only
// ever called by vtable slot: a COM object is a pointer to a table of
// function pointers, indexed by the interface's own method order. The three
// used here all inherit IUnknown (QueryInterface, AddRef, Release) then
// IDXGIObject (four more), so each interface's first method is slot 7.
type comObject struct{ vtbl *[16]uintptr }

const (
	comRelease  = 2 // IUnknown::Release
	dxgiEnum    = 7 // IDXGIFactory::EnumAdapters, IDXGIAdapter::EnumOutputs
	dxgiGetDesc = 7 // IDXGIOutput::GetDesc
)

func (o *comObject) call(slot int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[slot], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)

	return r
}

func (o *comObject) release() { o.call(comRelease) }

// dxgiOutputDesc is DXGI_OUTPUT_DESC. Only Monitor is read — the HMONITOR is
// the one key both enumerations share, which is what makes this a lookup
// rather than two orders trusted to agree.
type dxgiOutputDesc struct {
	DeviceName         [32]uint16
	DesktopCoordinates winRect
	AttachedToDesktop  int32
	Rotation           uint32
	Monitor            uintptr
}

// dxgiOutputs maps each attached monitor's HMONITOR to the adapter and output
// ddagrab captures it as. ddagrab counts outputs within *one* adapter — the
// device `-init_hw_device d3d11va` made — so both numbers are needed: a
// monitor on the second GPU is output 0 of adapter 1, and asking adapter 0 for
// it captures somebody else's screen or nothing at all. Nothing else exposes
// DXGI's own ordering, which is the only order ddagrab counts in.
//
// It is walked **only where ddagrab is what a monitor will be grabbed with**,
// which is why the answer is not packed into the source at enumeration: on a
// machine with Graphics Capture nothing reads it, and this is a COM walk per
// opening of the picker.
//
// A nil answer is not an error: it removes the middle rung, and a monitor is
// then Graphics Capture or gdigrab.
func dxgiOutputs() map[uintptr][2]int {
	var factory *comObject
	if r, _, _ := procCreateDXGIFactory.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory)),
		uintptr(unsafe.Pointer(&factory)),
	); r != 0 || factory == nil {
		return nil
	}
	defer factory.release()

	found := make(map[uintptr][2]int)
	for adapter := 0; ; adapter++ {
		var ad *comObject
		if factory.call(dxgiEnum, uintptr(adapter), uintptr(unsafe.Pointer(&ad))) != 0 || ad == nil {
			break
		}

		for output := 0; ; output++ {
			var out *comObject
			if ad.call(dxgiEnum, uintptr(output), uintptr(unsafe.Pointer(&out))) != 0 || out == nil {
				break
			}

			var desc dxgiOutputDesc
			if out.call(dxgiGetDesc, uintptr(unsafe.Pointer(&desc))) == 0 && desc.Monitor != 0 {
				found[desc.Monitor] = [2]int{adapter, output}
			}
			out.release()
		}
		ad.release()
	}

	return found
}

/* The grab */

// grabArgs walks the three grabbers in order for the kind of source it is
// given: Graphics Capture by handle, then Desktop Duplication for a monitor
// DXGI named, then gdigrab — one window by title, or a monitor as a region of
// the whole desktop in virtual-screen coordinates, negative for a monitor
// left of or above the primary.
func grabArgs(tool string, cfg CaptureConfig, enc shareEncoder) (grab, error) {
	switch cfg.Source.Kind {
	case CaptureWindow:
		if handle := sourceHandle(cfg.Source.ID); handle != 0 && wgcWorks(tool) {
			return gfxGrab(tool, fmt.Sprintf("hwnd=%d", handle), cfg, enc), nil
		}
		if cfg.Source.Title == "" {
			return grab{}, errors.New("video: the window has no title to find it by")
		}

		return grab{args: []string{
			"-f", "gdigrab", "-framerate", fmt.Sprint(cfg.FPS),
			"-i", "title=" + cfg.Source.Title,
		}}, nil

	case CaptureMonitor:
		handle := sourceHandle(cfg.Source.ID)
		if handle != 0 && wgcWorks(tool) {
			return gfxGrab(tool, fmt.Sprintf("hmonitor=%d", handle), cfg, enc), nil
		}
		if at, ok := dxgiOutputs()[handle]; ok && ddagrabWorks(tool, at[0], at[1]) {
			return grab{
				args:   []string{"-init_hw_device", "d3d11va:" + strconv.Itoa(at[0])},
				source: ddagrabSource(at[1], cfg.FPS),
			}, nil
		}

		return grab{args: []string{
			"-f", "gdigrab", "-framerate", fmt.Sprint(cfg.FPS),
			"-offset_x", fmt.Sprint(cfg.Source.X), "-offset_y", fmt.Sprint(cfg.Source.Y),
			"-video_size", fmt.Sprintf("%dx%d", cfg.Source.Width, cfg.Source.Height),
			"-i", "desktop",
		}}, nil
	}

	return grab{}, fmt.Errorf("video: unknown capture kind %d", cfg.Source.Kind)
}

/* Graphics Capture */

// gfxGrab is Graphics Capture aimed at one target, handed to the encoder as
// the texture it arrived in where a probe found that pairing willing, and
// read back for the processor chain otherwise.
func gfxGrab(tool, target string, cfg CaptureConfig, enc shareEncoder) grab {
	if directWorks(tool, enc) {
		return grab{source: gfxDirect(target, cfg), direct: true}
	}

	return grab{source: gfxSource(target, cfg)}
}

// gfxDirect is the filter with the encode box forced outright, and nothing
// after it: the encoder takes the D3D11 texture as it is and converts its
// colour on the way in. Unlike gfxSource there is no readback to spare, so a
// source smaller than the box is scaled up on the GPU as well, and the
// encoder is handed exactly Width×Height every frame — which is also what
// holds the declared size true through a window resized mid-share, the pad
// being the filter's own. Top-left rather than centred, which is the one
// thing the processor chain did that this does not; it only ever shows once
// a shared window has changed shape.
func gfxDirect(target string, cfg CaptureConfig) string {
	return fmt.Sprintf("gfxcapture=%s:max_framerate=%d:width=%d:height=%d:resize_mode=scale_aspect:scale_mode=bicubic",
		target, cfg.FPS, cfg.Width, cfg.Height)
}

// gfxSource is the Graphics Capture filter aimed at one target — `hwnd=N` or
// `hmonitor=N` — and it needs no hardware device: unlike ddagrab the filter
// opens its own, so nothing here becomes every other filter's default.
//
// The scale is the point. Graphics Capture is the only grabber on this
// platform that resizes before handing a frame over, so where the encode box
// is smaller than the source the readback is the box: a 2560×1600 monitor
// into a 1280×720 share moves 3.3 MB per frame across the bus instead of
// 16 MB, and swscale is left with a format conversion rather than a resize.
// Measured at 1080p-class output, 30 fps, on an RTX 4070 Laptop: 0.23 s of
// CPU over fifteen seconds becomes 0.05 s. Bicubic because the resampling is
// the GPU's either way, so the better filter is free.
//
// It fits rather than fills, and the chain's own pad is still what centres
// the result and holds the declared size true — Graphics Capture pads to the
// top-left corner, and a source resized mid-share must not move the frame.
func gfxSource(target string, cfg CaptureConfig) string {
	source := fmt.Sprintf("gfxcapture=%s:max_framerate=%d", target, cfg.FPS)
	if width, height, ok := gfxFit(cfg); ok {
		source += fmt.Sprintf(":width=%d:height=%d:resize_mode=scale_aspect:scale_mode=bicubic",
			width, height)
	}

	return source + ",hwdownload,format=bgra"
}

// gfxFit is the source's own rectangle fitted into the encode box, not ok
// where there is no reduction to be had — a source smaller than the box is
// upscaled by the chain, and doing that before the readback would move *more*
// bytes rather than fewer.
//
// Enumeration reports virtual-screen coordinates, which on a scaled display
// can be smaller than the pixels Graphics Capture hands back — Win32 answers
// a process that is not per-monitor DPI aware in the scaled space. Only the
// aspect ratio is read from them, so the fit is right either way; the worst a
// mismatch costs is a saving declined, never a wrong size.
func gfxFit(cfg CaptureConfig) (width, height int, ok bool) {
	sw, sh := cfg.Source.Width, cfg.Source.Height
	if sw <= 0 || sh <= 0 || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}

	if sw*cfg.Height > sh*cfg.Width { // wider than the box: width is the limit
		width, height = cfg.Width, sh*cfg.Width/sw
	} else {
		width, height = sw*cfg.Height/sh, cfg.Height
	}
	width, height = width&^1, height&^1 // even, the encoders' floor
	if width < 2 || height < 2 || width >= sw || height >= sh {
		return 0, 0, false
	}

	return width, height, true
}

// wgcProbes remembers whether Graphics Capture answers here at all, which is
// one question rather than one per source: the API arrived in Windows 10
// 1803 and the filter is newer than that again, so a machine either has both
// or has neither. An ffmpeg found on PATH may well be the older one.
var (
	wgcMu     sync.Mutex
	wgcProbed bool
	wgcOK     bool
)

// wgcWorks grabs a single frame of the primary monitor to nowhere, once per
// run, for the reason ddagrabWorks does: a filter this build does not carry
// must be found out on the worker the picker is already waiting on, not at
// the first frame of a live track.
func wgcWorks(tool string) bool {
	wgcMu.Lock()
	defer wgcMu.Unlock()

	if wgcProbed {
		return wgcOK
	}
	wgcProbed = true

	ctx, cancel := context.WithTimeout(context.Background(), grabProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tool, "-v", "error", "-nostdin",
		"-filter_complex", fmt.Sprintf(
			"gfxcapture=monitor_idx=0:max_framerate=%d,hwdownload,format=bgra", grabProbeFPS),
		"-frames:v", "1", "-f", "null", "-")
	captureAttrs(cmd)

	wgcOK = cmd.Run() == nil
	if !wgcOK {
		log.Printf("video: no Graphics Capture here; capture falls back to " +
			"Desktop Duplication for a monitor and to GDI for a window")
	}

	return wgcOK
}

// directProbes remembers, per encoder, whether it takes Graphics Capture's
// texture straight: NVENC and AMF both list D3D11 among their inputs, QSV
// does not, and a driver may still refuse what the encoder lists.
var (
	directMu     sync.Mutex
	directProbes = map[string]bool{}
)

// directWorks grabs three frames of the primary monitor straight into the
// encoder, once per encoder per run. A success is also a Graphics Capture
// success and is recorded as one, so a machine on the direct path pays for
// one capture session rather than two — which is why the encoder probe is
// asked ahead of the fallback check on the picker's worker.
func directWorks(tool string, enc shareEncoder) bool {
	if !enc.hardware() {
		return false
	}

	directMu.Lock()
	defer directMu.Unlock()

	if ok, seen := directProbes[enc.name]; seen {
		return ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), grabProbeTimeout)
	defer cancel()

	args := []string{"-v", "error", "-nostdin", "-filter_complex",
		fmt.Sprintf("gfxcapture=monitor_idx=0:max_framerate=%d:width=320:height=180:resize_mode=scale_aspect", grabProbeFPS)}
	direct := probeOptions
	direct.direct = true
	args = append(args, enc.args(direct)...)
	args = append(args, enc.rateControl(1_000_000, CaptureLowestLatency, CaptureVariable)...)
	args = append(args, "-bf", "0", "-frames:v", "3", "-f", "null", "-")

	cmd := exec.CommandContext(ctx, tool, args...)
	captureAttrs(cmd)

	ok := cmd.Run() == nil
	directProbes[enc.name] = ok
	if ok {
		wgcMu.Lock()
		wgcProbed, wgcOK = true, true
		wgcMu.Unlock()
		log.Printf("video: %s takes Graphics Capture's texture directly; no frame is read back", enc.name)
	} else {
		log.Printf("video: %s does not take Graphics Capture's texture directly; frames are read back for the encode", enc.name)
	}

	return ok
}

// probeDirect is ShareEncoder's chance to answer the direct question on the
// picker's worker rather than at the first frame of a share.
func probeDirect(tool string, enc shareEncoder) { directWorks(tool, enc) }

// ddagrabSource is the filter itself, at whatever rate is wanted. ddagrab
// hands back D3D11 surfaces, so hwdownload is what brings them where the
// scale filter can reach them; the whole output is taken, there being no way
// to ask Desktop Duplication for less, and the caller's own scale is what
// fits it.
func ddagrabSource(output, fps int) string {
	return fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d,hwdownload,format=bgra", output, fps)
}

// captureFallback reports whether anything in this list has to be grabbed by
// BitBlt. Graphics Capture takes both kinds, so one answer settles the set;
// without it a window has nowhere else to go, and a monitor has Desktop
// Duplication in between. Asking is what runs the probes, so it belongs on
// the worker the enumeration is already on — and by the time a share starts
// they are answered, which is why the picker can say so before anything is
// picked.
func captureFallback(tool string, sources []CaptureSource) bool {
	if wgcWorks(tool) {
		return false
	}

	// One walk for the whole set rather than one per monitor, and none at all
	// on the path above.
	outputs := dxgiOutputs()
	for _, source := range sources {
		if source.Kind == CaptureWindow {
			return true
		}

		at, ok := outputs[sourceHandle(source.ID)]
		if !ok || !ddagrabWorks(tool, at[0], at[1]) {
			return true
		}
	}

	return false
}

// sourceHandle reads the HWND or HMONITOR back off a source — one form for
// both, the two enumerations writing the same shape. Zero is a source from
// some other enumeration, which leaves only what gdigrab can be told.
func sourceHandle(id string) uintptr {
	handle, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}

	return uintptr(handle)
}

// ddaProbes remembers what one *address* answered. Desktop Duplication is
// not available in every session — an RDP one has no output to duplicate,
// and an ffmpeg predating the filter has no ddagrab at all — and neither
// answer changes while the client runs. Keyed per address because "adapter 0
// works" says nothing about a monitor on the second GPU, and keyed on the
// address *alone*: the frame rate is not what is being asked about, and a
// key carrying it would re-probe the same output once per rate.
var (
	ddaMu     sync.Mutex
	ddaProbes = map[string]bool{}
)

// ddagrabWorks grabs a single frame to nowhere. It costs a few hundred
// milliseconds, once per monitor per run of the client, on a worker — which
// is the cheap half of the trade: the expensive half would be a share that
// publishes nothing because the filter this machine has no answer for was
// found out at the first frame, by which time the track is live and the
// picker is gone.
func ddagrabWorks(tool string, adapter, output int) bool {
	ddaMu.Lock()
	defer ddaMu.Unlock()

	key := fmt.Sprintf("%d:%d", adapter, output)
	if ok, seen := ddaProbes[key]; seen {
		return ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), grabProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tool, "-v", "error", "-nostdin",
		"-init_hw_device", "d3d11va:"+strconv.Itoa(adapter),
		"-filter_complex", ddagrabSource(output, grabProbeFPS),
		"-frames:v", "1", "-f", "null", "-")
	captureAttrs(cmd)

	ok := cmd.Run() == nil
	ddaProbes[key] = ok
	if !ok {
		log.Printf("video: no Desktop Duplication for output %s; "+
			"this monitor falls back to gdigrab, which costs CPU and flickers the pointer", key)
	}

	return ok
}

// Both grabber probes take these: each grabs one frame to nowhere and neither
// is asking about the frame rate.
const (
	// grabProbeTimeout bounds a probe: a grabber that neither answers nor
	// fails must not be what a share waits on.
	grabProbeTimeout = 6 * time.Second

	// grabProbeFPS is the rate a probe asks for, which is only ever one
	// frame's worth. Low, so a machine that answers slowly is not also asked
	// to answer often.
	grabProbeFPS = 5
)

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

// sourceProcess is the process behind a window handle, which is what lets a
// share send that window's own sound and not the notification arriving over it.
//
// The handle is the enumeration's and may already be stale, in which case
// Windows answers zero — the whole machine, which is the safe way to be wrong.
func sourceProcess(id string) uint32 {
	handle, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}

	var pid uint32
	procGetWindowThreadPID.Call(uintptr(handle), uintptr(unsafe.Pointer(&pid)))

	return pid
}
