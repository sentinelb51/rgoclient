package voice

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
)

// Two accounts in one room, which is the only check that covers the whole path:
// tone -> Opus -> LiveKit -> RTP -> jitter -> decode -> sink. Skipped unless
// RGO_TWO_ENDED is set, and it signs in with the saved sessions on this machine.
//
// It is internal to the package on purpose. What it measures that nothing else
// can is the jitter buffer's depth over a real connection — the ratchet that
// used to hold a call at maxDepth was invisible from outside, every observable
// number being correct while latency climbed.
//
// A tone rather than a microphone, because the assertion is that what arrives is
// what was sent: a 440 Hz sine leaves the far end identifiable by frequency and
// amplitude, and neither survives a path that is quietly substituting silence.

// toneHz is what the speaker sends and the listener is checked against.
const toneHz = 440

// toneAmplitude is the sine's peak. Its RMS is this over root two, which is the
// figure the received audio is compared with.
const toneAmplitude = 9000

// sinkTargetSamples mirrors the real sink's own: two 20 ms frames at 48 kHz.
const sinkTargetSamples = sampleRate * 40 / 1000

// sineSource is a paced tone standing in for a microphone.
type sineSource struct {
	phase float64
	hz    float64
}

func (s *sineSource) Read(pcm []int16) (int, error) {
	for i := range pcm {
		pcm[i] = int16(toneAmplitude * math.Sin(s.phase))
		s.phase += 2 * math.Pi * s.hz / sampleRate
	}
	time.Sleep(frameMillis * time.Millisecond) // pace like a device, roughly

	return len(pcm), nil
}

func (s *sineSource) Voiced() bool { return true }

// measuringSink is the receiving speakers. It has to behave like them rather
// than only satisfy the interface — the receive path is paced by whatever is on
// the other side of Wake, so a sink that never asks is a call that never decodes
// — and it measures what it was handed on the way past.
type measuringSink struct {
	mu     sync.Mutex
	frames map[string]int
	held   map[string]int
	peak   map[string]int

	// Enough to identify a sine without an FFT: zero crossings give the
	// frequency, the sum of squares gives the amplitude.
	crossings map[string]int
	samples   map[string]int
	energy    map[string]float64
	last      map[string]int16

	wake chan struct{}
	stop chan struct{}
}

func newMeasuringSink() *measuringSink {
	s := &measuringSink{
		frames:    map[string]int{},
		held:      map[string]int{},
		peak:      map[string]int{},
		crossings: map[string]int{},
		samples:   map[string]int{},
		energy:    map[string]float64{},
		last:      map[string]int16{},
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}

	go s.run()

	return s
}

// run is the device callback: it takes a period out of every lane and then asks
// for more, which is the whole of what paces playout.
func (s *measuringSink) run() {
	const period = 10 * time.Millisecond

	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		}

		s.mu.Lock()
		open := len(s.held) > 0
		for userID, n := range s.held {
			s.held[userID] = max(0, n-sampleRate*10/1000)
		}
		s.mu.Unlock()

		if !open {
			continue
		}

		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *measuringSink) Write(userID string, pcm []int16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.frames[userID]++
	s.held[userID] += len(pcm)
	if s.held[userID] > s.peak[userID] {
		s.peak[userID] = s.held[userID]
	}

	previous := s.last[userID]
	for _, sample := range pcm {
		if (previous < 0) != (sample < 0) {
			s.crossings[userID]++
		}
		previous = sample
		s.energy[userID] += float64(sample) * float64(sample)
	}
	s.last[userID] = previous
	s.samples[userID] += len(pcm)
}

func (s *measuringSink) Open(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.held[userID]; !ok {
		s.held[userID] = 0
	}
}

func (s *measuringSink) Wake() <-chan struct{} { return s.wake }

func (s *measuringSink) Want(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, open := s.held[userID]
	if !open {
		return 0
	}

	return max(0, sinkTargetSamples-held)
}

func (s *measuringSink) Remove(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.frames, userID)
	delete(s.held, userID)
}

func (s *measuringSink) Reset() {}

// liveAccount is one saved session, as the client writes them.
type liveAccount struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// openLive signs in and waits for Ready, so the store knows about channels.
func openLive(t *testing.T, account liveAccount) (*client.Client, client.Ready) {
	t.Helper()

	c := client.New()
	if err := c.Open(account.Token); err != nil {
		t.Fatalf("%s: open: %v", account.Username, err)
	}

	deadline := time.After(30 * time.Second)
	for {
		select {
		case event := <-c.Events():
			if ready, ok := event.(client.Ready); ok {
				return c, ready
			}
		case <-deadline:
			t.Fatalf("%s: no Ready in 30s", account.Username)
		}
	}
}

// voiceChannelsIn lists every voice channel the account can see, by ID.
func voiceChannelsIn(store domain.Store, ready client.Ready) map[string]string {
	out := map[string]string{}

	for _, serverID := range ready.ServerIDs {
		server, ok := store.Server(serverID)
		if !ok {
			continue
		}
		for _, id := range server.Channels {
			channel, ok := store.Channel(id)
			if !ok || channel.Kind != domain.ChannelVoice {
				continue
			}
			out[id] = server.Name + " / " + channel.Name
		}
	}

	return out
}

func TestLiveTwoEnded(t *testing.T) {
	if os.Getenv("RGO_TWO_ENDED") == "" {
		t.Skip("set RGO_TWO_ENDED=1 to run against the real server")
	}

	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".rgoclient_sessions.json"))
	if err != nil {
		t.Skipf("no saved session: %v", err)
	}

	var accounts []liveAccount
	if err := json.Unmarshal(data, &accounts); err != nil || len(accounts) < 2 {
		t.Skipf("two saved accounts needed, one for each end: %v", err)
	}
	speaker, listener := accounts[0], accounts[1]
	t.Logf("speaker  %s (%s)", speaker.Username, speaker.UserID)
	t.Logf("listener %s (%s)", listener.Username, listener.UserID)

	speakerClient, speakerReady := openLive(t, speaker)
	defer speakerClient.Close()
	listenerClient, listenerReady := openLive(t, listener)
	defer listenerClient.Close()

	// Both ends have to be able to connect, so the channel is the intersection
	// rather than either account's own first pick — one of them being in servers
	// the other is not is the ordinary case.
	speakerChannels := voiceChannelsIn(speakerClient.Store(), speakerReady)
	listenerChannels := voiceChannelsIn(listenerClient.Store(), listenerReady)

	shared := make([]string, 0, len(listenerChannels))
	for id := range listenerChannels {
		if _, ok := speakerChannels[id]; ok {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		t.Skipf("%s and %s share no voice channel", speaker.Username, listener.Username)
	}
	channelID := shared[0]
	t.Logf("channel %s (%s)", listenerChannels[channelID], channelID)

	speakerCreds, err := speakerClient.JoinCall(channelID, true)
	if err != nil {
		t.Fatalf("speaker join_call: %v", err)
	}
	speakerCall, err := Join(speakerCreds, &sineSource{hz: toneHz}, newMeasuringSink(), Options{SelfID: speaker.UserID})
	if err != nil {
		t.Fatalf("speaker Join: %v", err)
	}
	defer speakerCall.Close()
	t.Logf("speaker publishing %d Hz", toneHz)

	// The listener joins second, so it arrives to a track already publishing —
	// which is the ordering a person joining an occupied channel gets.
	time.Sleep(2 * time.Second)

	listenerCreds, err := listenerClient.JoinCall(channelID, true)
	if err != nil {
		t.Fatalf("listener join_call: %v", err)
	}
	sink := newMeasuringSink()
	defer close(sink.stop)

	// A different tone from the listener, so a path that crossed the two lanes
	// would be caught by the frequency rather than pass as a match.
	listenerCall, err := Join(listenerCreds, &sineSource{hz: toneHz / 2}, sink, Options{SelfID: listener.UserID})
	if err != nil {
		t.Fatalf("listener Join: %v", err)
	}
	defer listenerCall.Close()

	go func() {
		for event := range listenerCall.Events() {
			switch e := event.(type) {
			case ParticipantChanged:
				t.Logf("  listener sees %q joined=%v", e.UserID, e.Joined)
			case ConnectionChanged:
				t.Logf("  listener connection %s", e.State)
			case CallEnded:
				t.Logf("  listener ended: %v", e.Err)
			}
		}
	}()

	const (
		seconds     = 25
		deepPLCOn   = 9
		deepPLCOff  = 17
		maxSaneHeld = maxDepth
	)

	lastFrames := map[string]int{}
	deepestSeen, lastDepth := 0, 0
	grew, shrank := false, false

	for tick := range seconds {
		time.Sleep(time.Second)

		// Toggled mid-call, which is the only place the decoder ctl is exercised
		// against a decoder that is running.
		switch tick {
		case deepPLCOn:
			listenerCall.SetDeepPLC(true)
			t.Log("  deep PLC on")
		case deepPLCOff:
			listenerCall.SetDeepPLC(false)
			t.Log("  deep PLC off")
		}

		listenerCall.mu.Lock()
		ids := make([]string, 0, len(listenerCall.lanes))
		for id := range listenerCall.lanes {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		line := ""
		for _, id := range ids {
			buffer, ok := listenerCall.lanes[id].buffer.(*adaptiveJitter)
			if !ok {
				continue
			}
			buffer.mu.Lock()
			deepestSeen = max(deepestSeen, buffer.depth)
			if lastDepth > 0 {
				grew = grew || buffer.depth > lastDepth
				shrank = shrank || buffer.depth < lastDepth
			}
			lastDepth = buffer.depth
			line += fmt.Sprintf("%s held=%2d depth=%2d filling=%-5v loss=%d%%  ",
				id[len(id)-4:], buffer.held, buffer.depth, buffer.filling, buffer.lossPercent)
			buffer.mu.Unlock()
		}
		listenerCall.mu.Unlock()

		sink.mu.Lock()
		for _, id := range ids {
			line += fmt.Sprintf("%s %2d/s peak=%dms",
				id[len(id)-4:], sink.frames[id]-lastFrames[id], sink.peak[id]*1000/sampleRate)
			lastFrames[id] = sink.frames[id]
		}
		sink.mu.Unlock()

		if line == "" {
			line = "(no lanes)"
		}
		t.Logf("[%2ds] %s", tick+1, line)
	}

	if identity := listenerCall.Identity(); identity != listener.UserID {
		t.Errorf("IDENTITY MISMATCH: %q != %q — per-user audio would be misfiled", identity, listener.UserID)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()

	samples, ok := sink.samples[speaker.UserID]
	if !ok || samples == 0 {
		t.Fatalf("NOTHING RECEIVED: no audio arrived from %s", speaker.Username)
	}

	elapsed := float64(samples) / sampleRate
	hz := float64(sink.crossings[speaker.UserID]) / (2 * elapsed)
	rms := math.Sqrt(sink.energy[speaker.UserID] / float64(samples))
	rate := float64(sink.frames[speaker.UserID]) / elapsed

	t.Logf("received %d frames, %.1fs, %.0f Hz, rms %.0f, %.1f frames/s, deepest buffer %d",
		sink.frames[speaker.UserID], elapsed, hz, rms, rate, deepestSeen)

	// The tone identifies the audio as the one that was sent. A path substituting
	// silence or concealment for the whole run answers zero, and a path that
	// resampled or crossed the lanes answers the wrong frequency.
	if hz < toneHz*0.9 || hz > toneHz*1.1 {
		t.Errorf("TONE WRONG: sent %d Hz, received %.0f Hz", toneHz, hz)
	}

	// A sine's RMS is its amplitude over root two. Opus is lossy, so this is a
	// sanity bound rather than an equality: it catches a path that is attenuating
	// or clipping, not one that is merely coding.
	wantRMS := toneAmplitude / math.Sqrt2
	if rms < wantRMS*0.7 || rms > wantRMS*1.3 {
		t.Errorf("AMPLITUDE WRONG: sent rms %.0f, received %.0f", wantRMS, rms)
	}

	// Playout is the device's clock, so the frame rate is the period's and not
	// the network's. Anything else means the two have come uncoupled.
	if rate < 45 || rate > 55 {
		t.Errorf("PLAYOUT RATE WRONG: %.1f frames/s, expected ~50", rate)
	}

	// The depth ratchet: any steady loss used to reset the run that shrinking
	// waits on, so the depth only ever climbed. How deep it goes is the
	// connection's business — that it comes back down is ours.
	//
	// Asserted only where it grew at all. On a clean run it can sit at the initial
	// depth for the whole call, which is not a ratchet and not a failure.
	if grew && !shrank {
		t.Errorf("BUFFER RATCHET: depth only ever grew, reaching %d of %d", deepestSeen, maxDepth)
	}
}
