package audio

import (
	"fmt"
	"testing"
	"time"
)

// What the mixer callback costs, which is the only arithmetic-shaped hot loop in
// the client and therefore the only place SIMD could plausibly apply.
func TestBenchMixer(t *testing.T) {
	pcm := make([]int16, 480)
	for i := range pcm {
		pcm[i] = int16(i * 7)
	}

	for _, lanes := range []int{1, 5, 20, 50} {
		m := newMixer()
		s := newSink(m)
		for i := range lanes {
			s.Write(fmt.Sprintf("user%d", i), pcm)
		}

		out := make([]byte, 480*channelCount*2)

		const runs = 20000
		start := time.Now()
		for range runs {
			for i := range lanes {
				s.Write(fmt.Sprintf("user%d", i), pcm)
			}
			m.render(out)
		}
		each := time.Since(start) / runs

		// A 480-frame period at 48 kHz is 10 ms of audio.
		t.Logf("%2d lanes: %6.1f us/period  (%.3f%% of the 10 ms budget)",
			lanes, float64(each.Nanoseconds())/1000, float64(each.Nanoseconds())/1e7*100)
	}
}
