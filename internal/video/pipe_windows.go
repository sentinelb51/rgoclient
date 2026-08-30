//go:build windows

package video

import (
	"os"
	"syscall"
	"unsafe"
)

var createPipe = syscall.NewLazyDLL("kernel32.dll").NewProc("CreatePipe")

// sizedPipe makes an anonymous pipe holding bytes, or the system default at
// zero — which is what CreatePipe already reads a zero as, so the two are one
// call. os.Pipe cannot be asked for a size at all: it passes zero always, and a
// Windows pipe's buffer is fixed when the pipe is made, there being no
// F_SETPIPE_SZ to widen it afterwards.
//
// The handles are left uninheritable. exec duplicates whatever it is handed into
// an inheritable copy of its own for the child, so marking these would only widen
// them to every other child started in the same moment.
func sizedPipe(bytes int) (*os.File, *os.File, error) {
	var read, write syscall.Handle

	ok, _, err := createPipe.Call(uintptr(unsafe.Pointer(&read)),
		uintptr(unsafe.Pointer(&write)), 0, uintptr(bytes))
	if ok == 0 {
		return nil, nil, err
	}

	return os.NewFile(uintptr(read), "|0"), os.NewFile(uintptr(write), "|1"), nil
}
