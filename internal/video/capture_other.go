//go:build !windows && !linux

package video

// No grabber is wired here yet. macOS is avfoundation screens plus the OS
// consent prompt — phase 5 of docs/screenshare-todo.md — and everything else
// has no story at all, so sharing is refused with a sentence rather than
// offered and broken.

import (
	"errors"
	"os/exec"
)

// ShareSources answers that this platform cannot capture yet.
func ShareSources() ([]CaptureSource, error) {
	return nil, errors.New("sharing your screen is not supported on this platform yet")
}

func grabArgs(cfg CaptureConfig) ([]string, error) {
	return nil, errors.New("video: no capture on this platform")
}

func captureAttrs(cmd *exec.Cmd) {}

func hardenCapture(cmd *exec.Cmd) func() { return harden(cmd) }
