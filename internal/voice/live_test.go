package voice_test

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/voice"
)

// A live call against the real server. Skipped unless RGO_LIVE is set, and it
// signs in with the saved session on this machine.
//
// It publishes a sine and never opens a microphone. An automated test that puts
// the room on the wire is a hazard with no assertion behind it, so the device is
// deliberately unreachable from here rather than a mode to be asked for.
//
// What it is actually for is the one assumption nothing else can check: that
// LiveKit's participant identity is the Revolt user ID. Everything per-person —
// lane routing, the speaking ring, per-user volume — is keyed on it.
const liveServerName = "Big up testers"

type tone struct {
	phase  float64
	voiced bool
}

// Read is a 440 Hz sine, so anybody listening hears something unambiguous.
func (t *tone) Read(pcm []int16) (int, error) {
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(t.phase))
		t.phase += 2 * math.Pi * 440 / 48000
	}
	time.Sleep(20 * time.Millisecond) // pace like a real device

	return len(pcm), nil
}

func (t *tone) Voiced() bool { return true }

// countingSink stands in for the speakers, and has to behave like them rather
// than only satisfy the interface: the receive path is paced by whatever is on
// the other side of Wake, so a sink that never asks is a call that never
// decodes. It drains each lane on a 10 ms period the way a device callback
// would, and reports what it was handed.
type countingSink struct {
	mu     sync.Mutex
	frames map[string]int
	bytes  map[string]int
	held   map[string]int // samples buffered per lane, drained on the period

	wake chan struct{}
	stop chan struct{}
}

// sinkTarget mirrors the real sink's own: two 20 ms frames at 48 kHz.
const sinkTarget = 48000 * 40 / 1000

func newCountingSink() *countingSink {
	s := &countingSink{
		frames: map[string]int{},
		bytes:  map[string]int{},
		held:   map[string]int{},
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
	}

	go s.run()

	return s
}

// run is the device callback: it takes a period out of every lane and then asks
// for more, which is the whole of what paces playout.
func (s *countingSink) run() {
	ticker := time.NewTicker(10 * time.Millisecond)
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
			s.held[userID] = max(0, n-48000*10/1000)
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

func (s *countingSink) Write(userID string, pcm []int16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.frames[userID]++
	s.bytes[userID] += len(pcm)
	s.held[userID] += len(pcm)
}

func (s *countingSink) Open(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.held[userID]; !ok {
		s.held[userID] = 0
	}
}

func (s *countingSink) Wake() <-chan struct{} { return s.wake }

func (s *countingSink) Want(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, open := s.held[userID]
	if !open {
		return 0
	}

	return max(0, sinkTarget-held)
}

func (s *countingSink) Remove(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.frames, userID)
	delete(s.held, userID)
}

func (s *countingSink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.frames)
	clear(s.held)
}

// counts is the frame tally, taken under the lock the device side also uses.
func (s *countingSink) counts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.frames)
}

func TestLiveCall(t *testing.T) {
	if os.Getenv("RGO_LIVE") == "" {
		t.Skip("set RGO_LIVE=1 to run against the real server")
	}

	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".rgoclient_sessions.json"))
	if err != nil {
		t.Skipf("no saved session: %v", err)
	}

	var sessions []struct {
		Token    string `json:"token"`
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(data, &sessions); err != nil || len(sessions) == 0 {
		t.Fatalf("saved sessions unreadable: %v", err)
	}
	me := sessions[0]
	t.Logf("signed in as %s (%s)", me.Username, me.UserID)

	c := client.New()
	if err := c.Open(me.Token); err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer c.Close()

	// Wait for Ready so the store knows about servers and channels.
	var ready client.Ready
	deadline := time.After(30 * time.Second)
	for got := false; !got; {
		select {
		case event := <-c.Events():
			if r, ok := event.(client.Ready); ok {
				ready, got = r, true
			}
		case <-deadline:
			t.Fatal("no Ready in 30s")
		}
	}
	t.Logf("ready: %d servers", len(ready.ServerIDs))

	store := c.Store()

	// Every server, so a voice channel anywhere can stand in if the named one has
	// none.
	var channelID, channelName string
	for _, serverID := range ready.ServerIDs {
		server, ok := store.Server(serverID)
		if !ok {
			continue
		}

		voiceHere := 0
		for _, id := range server.Channels {
			channel, ok := store.Channel(id)
			if !ok || channel.Kind != domain.ChannelVoice {
				continue
			}
			voiceHere++

			// The named server wins outright; anything else is only a fallback.
			if channelID == "" || server.Name == liveServerName {
				channelID, channelName = id, server.Name+" / "+channel.Name
			}
		}

		t.Logf("server %-28q %2d channels, %d voice", server.Name, len(server.Channels), voiceHere)
	}
	if channelID == "" {
		t.Fatalf("no voice channel found in %q", liveServerName)
	}
	t.Logf("voice channel: %s (%s)", channelName, channelID)

	creds, err := c.JoinCall(channelID, true)
	if err != nil {
		t.Fatalf("join_call: %v", err)
	}
	t.Logf("credentials: url=%s token=%d bytes", creds.URL, len(creds.Token))

	// A synthetic source, always. This test joins a real channel on a real server
	// and publishes for twenty seconds, so opening the machine's microphone would
	// put whatever is in the room on the wire — and it never asserted anything
	// about the device to pay for that. What the real capture chain does is the
	// app's to demonstrate, not an automated test's.
	sink := newCountingSink()

	call, err := voice.Join(creds, &tone{}, sink, voice.Options{SelfID: me.UserID})
	if err != nil {
		t.Fatalf("voice.Join: %v", err)
	}

	// Drain events for a while, recording what identities turn up.
	seen := map[string]bool{}
	states := []string{}

	done := time.After(20 * time.Second)
	for running := true; running; {
		select {
		case event, ok := <-call.Events():
			if !ok {
				running = false
				break
			}
			switch e := event.(type) {
			case voice.SpeakingChanged:
				if e.Speaking {
					seen[e.UserID] = true
					t.Logf("speaking: %q", e.UserID)
				}
			case voice.ParticipantChanged:
				seen[e.UserID] = true
				t.Logf("participant %q joined=%v", e.UserID, e.Joined)
			case voice.ConnectionChanged:
				states = append(states, e.State.String())
				t.Logf("connection: %s", e.State)
			case voice.CallEnded:
				t.Logf("call ended: %v", e.Err)
				running = false
			}
		case <-done:
			running = false
		}
	}

	t.Logf("connection states: %v", states)
	t.Logf("identities seen: %v", keys(seen))
	t.Logf("sink frames: %v", sink.counts())

	// The assumption under test, asked of the *voice server* rather than of our own
	// echo: the publisher reports SelfID back whatever the server thinks, so
	// counting speaking events would prove only that the harness can spell.
	identity := call.Identity()
	t.Logf("voice server calls us %q; the account is %q", identity, me.UserID)

	switch {
	case identity == "":
		t.Error("IDENTITY UNPROVEN: the room reported no local identity")
	case identity == me.UserID:
		t.Log("IDENTITY OK: the voice server's identity is the Revolt user ID")
	default:
		t.Errorf("IDENTITY MISMATCH: %q != %q — per-user audio would be misfiled", identity, me.UserID)
	}

	call.Close()
	time.Sleep(500 * time.Millisecond)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
