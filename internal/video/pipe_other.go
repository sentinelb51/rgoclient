//go:build !windows && !linux

package video

import "os"

// sizedPipe is os.Pipe here, whatever it is asked for. macOS is the platform
// this is written for: its pipes take no size, and the kernel grows one to
// 64 KiB by itself the first time a writer fills the small buffer it starts at,
// which is most of what asking would have been worth.
func sizedPipe(int) (*os.File, *os.File, error) {

	return os.Pipe()
}
