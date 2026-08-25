package audio

import (
	"fmt"
	"testing"
	"time"
)

// openLanes opens n lanes on a sink and answers the IDs they were opened under.
// Open is the only thing that starts a lane, so a mixer written to without it
// has none: Write drops on a miss and every count below would render nothing.
func openLanes(s *Sink, n int) []string {
	ids := make([]string, n)

	for i := range ids {
		ids[i] = fmt.Sprintf("user%d", i)
		s.Open(ids[i])
	}

	return ids
}

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

		// Built once and outside the timing: formatting an ID per write measures
		// fmt rather than the mixer.
		ids := openLanes(s, lanes)

		out := make([]byte, 480*channelCount*2)

		const runs = 20000
		start := time.Now()
		for range runs {
			for _, id := range ids {
				s.Write(id, pcm)
			}
			m.render(out)
		}
		each := time.Since(start) / runs

		// A 480-frame period at 48 kHz is 10 ms of audio.
		t.Logf("%2d lanes: %6.1f us/period  (%.3f%% of the 10 ms budget)",
			lanes, float64(each.Nanoseconds())/1000, float64(each.Nanoseconds())/1e7*100)
	}
}

// The render path allocates nothing, which is the one rule it has: an allocation
// on the device thread can trip a GC assist, and a callback that stops to help
// the collector is a dropout. Asserted rather than benchmarked — a version that
// allocates still runs fast enough to pass a timing threshold, and only fails
// under load.
func TestMixerAllocs(t *testing.T) {
	m := newMixer()
	s := newSink(m)
	id := openLanes(s, 1)[0]

	pcm := make([]int16, 480)
	out := make([]byte, 480*channelCount*2)

	allocs := testing.AllocsPerRun(1000, func() {
		s.Write(id, pcm)
		m.render(out)
	})

	if allocs != 0 {
		t.Errorf("Sink.Write + render allocated %.0f times per period, want 0", allocs)
	}
}
