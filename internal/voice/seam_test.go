package voice_test

import (
	"testing"

	"RGOClient/internal/audio"
	"RGOClient/internal/voice"
)

// The seam the whole design rests on: `app` is the only package importing both,
// and these are the two assertions that say the hand-off compiles.
var (
	_ voice.PCMSource = (*audio.Capture)(nil)
	_ voice.PCMSink   = (*audio.Sink)(nil)
)

func TestSeam(t *testing.T) {
	t.Log("audio.Capture satisfies voice.PCMSource; audio.Sink satisfies voice.PCMSink")
}
