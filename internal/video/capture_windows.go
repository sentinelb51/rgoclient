package video

// Windows capture is Windows Graphics Capture — ffmpeg's gfxcapture filter
// source — for a monitor and for a window alike, with two older grabbers
// behind it. Enumeration is Win32 through x/sys, the cpu package's
// precedent: no cgo, the procs declared by hand where x/sys stops short.
// Monitors come from EnumDisplayMonitors in virtual-screen coordinates, which
// is the space gdigrab's desktop offsets are in; windows from EnumWindows,
// filtered to what a picker should offer — visible, titled, not a tool
// window, not cloaked, not minimised (nothing captures an iconified window).
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
		ID:    strconv.FormatUint(uint64(hwnd), 10),
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
func grabArgs(tool string, cfg CaptureConfig) (grab, error) {
	switch cfg.Source.Kind {
	case CaptureWindow:
		if handle := sourceHandle(cfg.Source.ID); handle != 0 && wgcWorks(tool) {
			return grab{source: gfxSource(fmt.Sprintf("hwnd=%d", handle), cfg)}, nil
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
			return grab{source: gfxSource(fmt.Sprintf("hmonitor=%d", handle), cfg)}, nil
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
