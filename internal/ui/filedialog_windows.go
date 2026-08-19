//go:build windows

package ui

// The file and folder pickers, which are the shell's own rather than drawn here.
// Fyne ships an in-canvas browser and it is the wrong thing twice over: it knows
// nothing the shell knows — pinned and recent places, cloud providers, search, a
// typed path, %APPDATA% — and it is not the dialog every other program on the
// machine opens, which is most of what a reader already knows about picking a file.
//
// IFileDialog rather than GetOpenFileName: one object covers files and folders,
// and it is what the shell itself shows. COM has no binding in the standard
// library, so the vtable is walked by hand below.

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

/* COM */

// guid is Win32's GUID, for the two the dialog is asked for by.
type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

// Vtable slots. IFileOpenDialog inherits IUnknown, then IModalWindow, then
// IFileDialog, so every index is fixed by that order and by nothing else — none
// of it is discoverable at runtime, and one wrong number calls a different
// method. IShellItem's own table starts over at IUnknown.
const (
	unknownRelease = 2

	dialogShow         = 3
	dialogSetFileTypes = 4
	dialogSetOptions   = 9
	dialogGetOptions   = 10
	dialogSetTitle     = 17
	dialogGetResult    = 20

	itemGetDisplayName = 5
)

const (
	clsCtxInprocServer = 0x1
	coinitApartment    = 0x2
	coinitNoOLE1DDE    = 0x4

	// FILEOPENDIALOGOPTIONS. FORCEFILESYSTEM keeps the answer to something with a
	// path: the shell namespace is wider than the file system, and a caller here
	// has only a path to open.
	fosPickFolders   = 0x20
	fosForceFileSys  = 0x40
	fosPathMustExist = 0x800
	fosFileMustExist = 0x1000

	sigdnFileSysPath = 0x80058000

	hrCancelled = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED): the reader closed it
)

var (
	ole32            = syscall.NewLazyDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coUninitialize   = ole32.NewProc("CoUninitialize")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	coTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

// filterSpec is COMDLG_FILTERSPEC: what the kinds box lists, and what it matches.
type filterSpec struct {
	Name *uint16
	Spec *uint16
}

// comCall invokes the method at index in obj's vtable, passing obj as the
// receiver COM expects first.
func comCall(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	method := *(*uintptr)(unsafe.Add(vtbl, index*int(unsafe.Sizeof(uintptr(0)))))

	hr, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(obj)}, args...)...)
	return hr
}

// failed reports an HRESULT error. The sign bit is the whole test — S_FALSE is a
// success and would fail an equality against S_OK.
func failed(hr uintptr) bool { return int32(hr) < 0 }

// utf16String reads a NUL-terminated wide string the shell allocated. Walked
// rather than sliced: nothing reports the length before the NUL is found.
func utf16String(p *uint16) string {
	var out []uint16

	for at := unsafe.Pointer(p); *(*uint16)(at) != 0; at = unsafe.Add(at, unsafe.Sizeof(uint16(0))) {
		out = append(out, *(*uint16)(at))
	}

	return string(utf16.Decode(out))
}

/* The pickers */

// PickFile shows the shell's file picker over win, and reports true so the caller
// knows not to fall back. onDone runs on the UI thread, with an empty path and no
// error when the reader cancelled.
//
// Call on the UI thread: the owner handle comes from the driver's own RunNative.
// The dialog itself runs on a thread of its own, so the client keeps painting
// behind it.
func PickFile(win fyne.Window, title string, filter FileFilter, onDone func(path string, err error)) bool {
	pick(win, title, filter, false, onDone)
	return true
}

// PickFolder shows the same dialog in folder mode, on the same terms as PickFile.
func PickFolder(win fyne.Window, title string, onDone func(path string, err error)) bool {
	pick(win, title, FileFilter{}, true, onDone)
	return true
}

func pick(win fyne.Window, title string, filter FileFilter, folders bool, onDone func(string, error)) {
	owner := ownerHandle(win)

	go func() {
		path, err := showItemDialog(owner, title, filter, folders)
		DoOnUI(func() { onDone(path, err) })
	}()
}

// ownerHandle is the window the dialog is modal to. Zero is answered by a dialog
// with no owner rather than by an error, which is the right end for a window not
// realized yet.
func ownerHandle(win fyne.Window) uintptr {
	native, ok := win.(driver.NativeWindow)
	if !ok {
		return 0
	}

	var hwnd uintptr
	native.RunNative(func(ctx any) {
		if wc, ok := ctx.(driver.WindowsWindowContext); ok {
			hwnd = wc.HWND
		}
	})

	return hwnd
}

// showItemDialog runs the Common Item Dialog to its end. It blocks for as long as
// the dialog is up — pick gives it a goroutine of its own — and reports an empty
// path and no error when the reader cancelled, that being an answer rather than a
// failure.
func showItemDialog(owner uintptr, title string, filter FileFilter, folders bool) (string, error) {
	// COM is per thread, and this one is a single-threaded apartment because the
	// dialog pumps messages. Locked for the same reason: another goroutine picking
	// the thread up mid-dialog would be running in an apartment it never entered.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := coInitializeEx.Call(0, coinitApartment|coinitNoOLE1DDE)
	if failed(hr) {
		return "", hresult("CoInitializeEx", hr)
	}
	defer coUninitialize.Call()

	var dialog unsafe.Pointer
	hr, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsCtxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dialog)))
	if failed(hr) {
		return "", hresult("CoCreateInstance", hr)
	}
	defer comCall(dialog, unknownRelease)

	if err := configureDialog(dialog, title, filter, folders); err != nil {
		return "", err
	}

	switch hr := comCall(dialog, dialogShow, owner); {
	case hr == hrCancelled:
		return "", nil
	case failed(hr):
		return "", hresult("Show", hr)
	}

	var item unsafe.Pointer
	if hr := comCall(dialog, dialogGetResult, uintptr(unsafe.Pointer(&item))); failed(hr) {
		return "", hresult("GetResult", hr)
	}
	defer comCall(item, unknownRelease)

	var wide *uint16
	if hr := comCall(item, itemGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&wide))); failed(hr) {
		return "", hresult("GetDisplayName", hr)
	}
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(wide)))

	return utf16String(wide), nil
}

// configureDialog sets what the reader is shown. Options are read back rather
// than assumed: the dialog arrives carrying defaults of its own, and overwriting
// them wholesale is how a picker loses its view mode.
func configureDialog(dialog unsafe.Pointer, title string, filter FileFilter, folders bool) error {
	var options uint32
	if hr := comCall(dialog, dialogGetOptions, uintptr(unsafe.Pointer(&options))); failed(hr) {
		return hresult("GetOptions", hr)
	}

	options |= fosForceFileSys | fosPathMustExist | fosFileMustExist
	if folders {
		options |= fosPickFolders
	}

	if hr := comCall(dialog, dialogSetOptions, uintptr(options)); failed(hr) {
		return hresult("SetOptions", hr)
	}

	heading, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	if hr := comCall(dialog, dialogSetTitle, uintptr(unsafe.Pointer(heading))); failed(hr) {
		return hresult("SetTitle", hr)
	}

	specs, err := fileTypes(filter)
	if err != nil || len(specs) == 0 {
		return err
	}
	if hr := comCall(dialog, dialogSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))); failed(hr) {
		return hresult("SetFileTypes", hr)
	}

	return nil
}

// fileTypes is the kinds box: the caller's filter, then everything. The escape
// hatch is always offered — the filter is a courtesy in every caller here, the
// server or the decoder behind it being what actually decides.
func fileTypes(filter FileFilter) ([]filterSpec, error) {
	if len(filter.Extensions) == 0 {
		return nil, nil
	}

	patterns := make([]string, 0, len(filter.Extensions))
	for _, ext := range filter.Extensions {
		patterns = append(patterns, "*"+ext)
	}

	var specs []filterSpec
	for _, pair := range [][2]string{
		{filter.Label, strings.Join(patterns, ";")},
		{"All files", "*.*"},
	} {
		label, err := syscall.UTF16PtrFromString(pair[0])
		if err != nil {
			return nil, err
		}

		pattern, err := syscall.UTF16PtrFromString(pair[1])
		if err != nil {
			return nil, err
		}

		specs = append(specs, filterSpec{Name: label, Spec: pattern})
	}

	return specs, nil
}

// hresult names the call that failed and the code it failed with. The code is all
// Windows says, and it is what a reader of the log has to search for.
func hresult(call string, hr uintptr) error {
	return fmt.Errorf("%s: 0x%08X", call, uint32(hr))
}
