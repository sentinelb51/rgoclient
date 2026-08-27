package audio

import "testing"

// TestResetKeepsTheEchoLane covers the one rule in Reset that nothing can be
// seen to break: hanging up closes every lane a call opened and must leave the
// microphone test's alone. Miss it and the test simply goes quiet with its own
// switch still on, which reads as a broken microphone rather than as a bug here —
// and the tidier `clear(s.byOne)` this replaced is exactly how it would come back.
func TestResetKeepsTheEchoLane(t *testing.T) {
	const participant = "01JCZ0000000000000000000AB"

	s := newSink(newMixer())
	s.StartEcho()
	s.Open(participant)

	s.Reset()

	// A write for a lane that is not there is dropped, so what lands is the whole
	// of what the exemption is worth.
	frame := make([]int16, FrameSamples)
	s.Write(echoLane, frame)

	if want := laneTarget - len(frame); s.Want(echoLane) != want {
		t.Fatalf("echo lane after Reset: Want %d, want %d", s.Want(echoLane), want)
	}

	// The other half of the same rule: the exemption must spare that one lane and
	// no others, or hanging up leaves the last call buffered behind the next.
	if s.Want(participant) != 0 {
		t.Fatal("Reset left a participant's lane open")
	}
}
