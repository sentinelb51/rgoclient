package audio

import (
	"math"
	"testing"
	"time"
)

// TestMicSignal reports the peak each input device actually produces, so a
// silent call can be told from a silent microphone.
func TestMicSignal(t *testing.T) {
	devices, err := Inputs()
	if err != nil {
		t.Skipf("no inputs: %v", err)
	}

	for _, device := range devices {
		c, err := OpenInput(device.ID, InputConfig{Sensitivity: 0, Gain: 1})
		if err != nil {
			t.Logf("%-45s OPEN FAILED: %v", device.Name, err)
			continue
		}

		frame := make([]int16, FrameSamples)

		var peak float64
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := c.Read(frame); err != nil {
				break
			}
			for _, s := range frame {
				peak = math.Max(peak, math.Abs(float64(s))/32767)
			}
		}
		c.Close()

		verdict := "signal"
		if peak == 0 {
			verdict = "SILENT — muted, or no permission"
		} else if peak < 0.001 {
			verdict = "almost silent"
		}

		t.Logf("%-45s default=%-5v peak=%.5f  %s", device.Name, device.Default, peak, verdict)
	}
}
