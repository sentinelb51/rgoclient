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
//
// A *monitor* is grabbed with ddagrab instead, where it can be: gdigrab's
// BitBlt carries CAPTUREBLT, which makes the whole desktop's pointer flicker
// once per captured frame — measured on this machine, and visible to
// everybody using it, not only to the viewer. ddagrab is the Desktop
// Duplication API and touches no DC, so it does not. It has no window form,
// which is why a shared window still flickers; see docs/known-gaps.md.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
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
// enumOutputs is DXGI's answer, read by the monitor callback and filled in
// just before it runs.
var (
	enumMu       sync.Mutex
	enumOutputs  map[uintptr]string
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
		ID:    enumOutputs[hMonitor], // where ddagrab is aimed; empty falls back to gdigrab
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
	enumOutputs = dxgiOutputs()
	_, _, _ = procEnumDisplayMonitors.Call(0, 0, monitorCallback, 0)
	_, _, _ = procEnumWindows.Call(windowCallback, 0)

	sources := append(enumMonitors, enumWindows...)
	enumMonitors, enumWindows, enumOutputs = nil, nil, nil
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

// dxgiOutputs maps each attached monitor's HMONITOR to the ddagrab address
// that captures it. ddagrab counts outputs within *one* adapter — the device
// `-init_hw_device d3d11va` made — so both numbers are needed: a monitor on
// the second GPU is output 0 of adapter 1, and asking adapter 0 for it
// captures somebody else's screen or nothing at all. Nothing else exposes
// DXGI's own ordering, which is the only order ddagrab counts in.
//
// A nil answer is not an error: every monitor then falls back to gdigrab.
func dxgiOutputs() map[uintptr]string {
	var factory *comObject
	if r, _, _ := procCreateDXGIFactory.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory)),
		uintptr(unsafe.Pointer(&factory)),
	); r != 0 || factory == nil {
		return nil
	}
	defer factory.release()

	found := make(map[uintptr]string)
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
				found[desc.Monitor] = fmt.Sprintf("dda:%d:%d", adapter, output)
			}
			out.release()
		}
		ad.release()
	}

	return found
}

/* The grab */

// grabArgs is ddagrab for a monitor DXGI named, and gdigrab for everything
// else: one window by title, or a monitor as a region of the whole desktop
// in virtual-screen coordinates — negative for a monitor left of or above
// the primary.
func grabArgs(tool string, cfg CaptureConfig) (grab, error) {
	switch cfg.Source.Kind {
	case CaptureWindow:
		if cfg.Source.ID == "" {
			return grab{}, errors.New("video: the window has no title to find it by")
		}

		return grab{args: []string{
			"-f", "gdigrab", "-framerate", fmt.Sprint(cfg.FPS),
			"-i", "title=" + cfg.Source.ID,
		}}, nil

	case CaptureMonitor:
		if g, ok := ddagrabFor(tool, cfg); ok {
			return g, nil
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

// ddagrabFor builds the Desktop Duplication grab for a monitor, if this
// machine can run it at all. ddagrab hands back D3D11 surfaces, so
// hwdownload is what brings them where the scale filter can reach them; the
// whole output is taken and the caller's own scale is what fits it, DXGI's
// rectangle and EnumDisplayMonitors' being the same rectangle.
func ddagrabFor(tool string, cfg CaptureConfig) (grab, bool) {
	adapter, output, ok := parseDDA(cfg.Source.ID)
	if !ok || !ddagrabWorks(tool, adapter, output) {
		return grab{}, false
	}

	return grab{
		args:   []string{"-init_hw_device", "d3d11va:" + strconv.Itoa(adapter)},
		source: ddagrabSource(output, cfg.FPS),
	}, true
}

// ddagrabSource is the filter itself, at whatever rate is wanted.
func ddagrabSource(output, fps int) string {
	return fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d,hwdownload,format=bgra", output, fps)
}

// captureFallback reports whether any monitor here has to be grabbed the old
// way. Asking is what runs the probes, so it belongs on the worker the
// enumeration is already on — and by the time a share starts they are
// answered, which is why the picker can say so before anything is picked.
func captureFallback(tool string, sources []CaptureSource) bool {
	for _, source := range sources {
		if source.Kind != CaptureMonitor {
			continue
		}

		adapter, output, ok := parseDDA(source.ID)
		if !ok || !ddagrabWorks(tool, adapter, output) {
			return true
		}
	}

	return false
}

// parseDDA reads back what dxgiOutputs wrote on the source.
func parseDDA(id string) (adapter, output int, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "dda" {
		return 0, 0, false
	}

	adapter, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	output, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, false
	}

	return adapter, output, true
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

	ctx, cancel := context.WithTimeout(context.Background(), ddaProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tool, "-v", "error", "-nostdin",
		"-init_hw_device", "d3d11va:"+strconv.Itoa(adapter),
		"-filter_complex", ddagrabSource(output, ddaProbeFPS),
		"-frames:v", "1", "-f", "null", "-")
	captureAttrs(cmd)

	ok := cmd.Run() == nil
	ddaProbes[key] = ok
	if !ok {
		log.Printf("video: no Desktop Duplication for output %s; "+
			"monitors fall back to gdigrab, which costs CPU and flickers the pointer", key)
	}

	return ok
}

const (
	// ddaProbeTimeout bounds the probe: a duplication that neither answers nor
	// fails must not be what a share waits on.
	ddaProbeTimeout = 6 * time.Second

	// ddaProbeFPS is the rate the probe asks for, which is only ever one
	// frame's worth. Low, so a machine that answers slowly is not also asked
	// to answer often.
	ddaProbeFPS = 5
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
