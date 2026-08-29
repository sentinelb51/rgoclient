//go:build !windows

package audio

import "errors"

// inputEffects has nothing to report here.
//
// macOS states the mic mode through AVCaptureDevice.activeMicrophoneMode, which
// needs an Objective-C bridge this package does not have; Linux applies nothing
// by default, a PipeWire echo-cancel node being a device the reader picked
// rather than a filter over the one they did. Either way the honest answer is
// that it was not asked, not that there is none. See docs/known-gaps.md.
func inputEffects(string) ([]Effect, error) {

	return nil, errors.New("audio: this platform does not report its own input processing")
}
