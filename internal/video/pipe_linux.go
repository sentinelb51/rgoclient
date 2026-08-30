//go:build linux

package video

import (
	"os"

	"golang.org/x/sys/unix"
)

// sizedPipe makes an anonymous pipe holding bytes where the kernel allows it,
// and leaves it at its own 64 KiB at zero. The ceiling is
// /proc/sys/fs/pipe-max-size — a megabyte by default — and a refusal leaves the
// pipe as it was, which is worth less rather than wrong, so it is not reported.
//
// Through SyscallConn rather than Fd: Fd takes the file out of the runtime's
// poller and leaves it blocking, which is a lot to pay for one fcntl.
func sizedPipe(bytes int) (*os.File, *os.File, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	// The buffer belongs to the pipe rather than to an end of it, so one end asks.
	if conn, err := write.SyscallConn(); err == nil && bytes > 0 {
		_ = conn.Control(func(fd uintptr) { _, _ = unix.FcntlInt(fd, unix.F_SETPIPE_SZ, bytes) })
	}

	return read, write, nil
}
