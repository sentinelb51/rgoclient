//go:build !windows

package audio

// openLoopback answers that this platform has no system-audio capture.
//
// Linux has one and it is not this shape: a PulseAudio or PipeWire monitor is
// an ordinary capture device, so it arrives through Inputs() and needs no API
// of its own — what is missing there is which of them is a monitor. macOS has
// none at all without a kernel extension the client does not ship.
func openLoopback(LoopbackConfig, *Loopback) (loopbackStream, LoopbackFormat, error) {
	return nil, LoopbackFormat{}, ErrNoLoopback
}

// loopbackAvailable is what LoopbackAvailable answers.
// See openLoopback — a monitor is an ordinary capture device on Linux, and
// macOS has nothing without a kernel extension.
const loopbackAvailable = false
