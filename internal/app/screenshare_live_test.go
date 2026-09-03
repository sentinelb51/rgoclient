package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/video"
	"RGOClient/internal/voice"
)

// Two accounts in one room, one sharing a window and the other watching it,
// which is the only check that covers the whole path: capture -> encode ->
// tee -> track -> relay -> RTP -> reassemble -> remux -> decode -> RGBA.
// Skipped unless RGO_SHARE_LIVE is set, and it signs in with the two saved
// sessions on this machine, the voice package's own live test's arrangement.
//
// What it measures that nothing else can is glass-to-glass latency: the
// shared window is a clock (scripts/share-clock.py — sixteen blocks carrying
// a 25 ms tick counter and a checksum), so every decoded frame says when it was
// painted, and the difference from its arrival is the whole path's delay.
// Frame arrival gaps are the freezes; repeated watches, a sender restart and a
// sender stopping under a watch are the teardown seams; the sender's own
// preview is the tee measured the same way.
//
// Knobs, all environment: RGO_SHARE_CLOCK (the script, required),
// RGO_SHARE_CODEC=h264, RGO_SHARE_FPS (5/15/30/60), RGO_SHARE_CLOCK_SIZE
// ("1600x900" — the window is the encode box, so this is how the box grows).

/* Accounts and the room */

type liveAccount struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

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
				go func() {
					for range c.Events() {
					}
				}()
				return c, ready
			}
		case <-deadline:
			t.Fatalf("%s: no Ready in 30s", account.Username)
		}
	}
}

// voiceChannelsIn lists every voice channel the account can see, by ID,
// named "server / channel".
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

/* The audio halves, which this test does not measure */

type silenceSource struct{}

func (silenceSource) Read(pcm []int16) (int, error) {
	clear(pcm)
	time.Sleep(20 * time.Millisecond)

	return len(pcm), nil
}

func (silenceSource) Voiced() bool { return false }

// nullSink discards audio but ticks, because the receive path is paced by
// the speakers and a sink that never asks is a call that never decodes.
type nullSink struct {
	wake chan struct{}
	stop chan struct{}
}

func newNullSink() *nullSink {
	s := &nullSink{wake: make(chan struct{}, 1), stop: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				select {
				case s.wake <- struct{}{}:
				default:
				}
			}
		}
	}()

	return s
}

func (s *nullSink) Write(string, []int16) {}
func (s *nullSink) Remove(string)         {}
func (s *nullSink) Reset()                {}
func (s *nullSink) Open(string)           {}
func (s *nullSink) Wake() <-chan struct{} { return s.wake }
func (s *nullSink) Want(string) int       { return 960 }

/* The clock in the picture */

const (
	clockCols, clockRows = 8, 2
	clockTickMillis      = 25
)

// decodeClock reads the painted tick counter back out of one RGBA frame. The
// picture is found as the bounding box of the grey field the blocks sit on,
// so letterbox pad on either side is skipped. Not ok is a frame whose
// checksum does not hold — a torn paint, or a frame that is not the clock.
func decodeClock(pix []byte, width, height int) (int, bool) {
	x0, y0, x1, y1 := width, height, -1, -1
	for y := 0; y < height; y += 2 {
		row := pix[y*width*4:]
		for x := 0; x < width; x += 2 {
			r, g, b := row[x*4], row[x*4+1], row[x*4+2]
			if r > 40 && r < 90 && g > 40 && g < 90 && b > 40 && b < 90 {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x), max(y1, y)
			}
		}
	}
	if x1-x0 < 64 || y1-y0 < 32 {
		return 0, false
	}

	cw := float64(x1-x0+1) / clockCols
	ch := float64(y1-y0+1) / clockRows
	bits := 0
	for i := range clockCols * clockRows {
		cx := x0 + int((float64(i%clockCols)+0.5)*cw)
		cy := y0 + int((float64(i/clockCols)+0.5)*ch)
		sum := 0
		for dy := -2; dy <= 2; dy++ {
			for dx := -2; dx <= 2; dx++ {
				p := ((cy+dy)*width + (cx + dx)) * 4
				sum += int(pix[p]) + int(pix[p+1]) + int(pix[p+2])
			}
		}
		if sum > 25*3*128 {
			bits |= 1 << i
		}
	}

	v := bits & 0xFFF
	check := ((v & 0xF) + ((v >> 4) & 0xF) + ((v >> 8) & 0xF) + 3) & 0xF
	if bits>>12 != check {
		return 0, false
	}

	return v, true
}

// clockLatency is how long ago the tick counter v was painted, as of now.
func clockLatency(v int, now time.Time) time.Duration {
	nowMillis := now.UnixMilli()
	diff := (int(nowMillis/clockTickMillis) - v) & 0xFFF

	return time.Duration(int64(diff)*clockTickMillis+nowMillis%clockTickMillis) * time.Millisecond
}

/* One decoded feed, measured */

type watchRun struct {
	label string

	mu        sync.Mutex
	started   time.Time
	opened    time.Time
	first     time.Time
	last      time.Time
	frames    int
	undecoded int
	dupes     int
	spikes    int
	lastTick  int
	cpu       time.Duration
	latencies []time.Duration
	gaps      []time.Duration
	maxGap    time.Duration

	done chan struct{}
}

func newWatchRun(label string) *watchRun {
	return &watchRun{label: label, started: time.Now(), lastTick: -1, done: make(chan struct{})}
}

// openStream launches the sandboxed decoder at the size the app would pick
// and a reader that always drains it, timing every frame. It answers with
// the decoder's stdin, which is what a watch and a preview both feed.
func (r *watchRun) openStream(tools video.Tools, format voice.ShareCodec, srcW, srcH int) (io.WriteCloser, error) {
	width, height := shareDecodeSize(srcW, srcH)
	stream, in, err := tools.LiveFrames(video.LiveConfig{Format: string(format), Width: width, Height: height})
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.opened = time.Now()
	r.mu.Unlock()
	fmt.Printf("    [%s] %s %dx%d -> decoding at %dx%d (%.0f ms after start)\n",
		r.label, format, srcW, srcH, width, height, float64(time.Since(r.started).Microseconds())/1000)

	go r.pump(stream, width, height)

	return in, nil
}

// open is the watch's ShareOpen.
func (r *watchRun) open(tools video.Tools) voice.ShareOpen {
	return func(codec voice.ShareCodec, name string, srcW, srcH int) (io.WriteCloser, error) {
		return r.openStream(tools, codec, srcW, srcH)
	}
}

func (r *watchRun) pump(stream *video.Stream, width, height int) {
	defer close(r.done)
	defer func() {
		stream.Stop()
		user, system := stream.Usage()
		r.mu.Lock()
		r.cpu = user + system
		r.mu.Unlock()
	}()

	buf := make([]byte, width*height*4)
	for {
		if err := stream.ReadFrame(buf); err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Printf("    [%s] frame read: %v\n", r.label, err)
			}
			return
		}
		now := time.Now()

		r.mu.Lock()
		if r.frames == 0 {
			r.first = now
		} else {
			gap := now.Sub(r.last)
			r.gaps = append(r.gaps, gap)
			r.maxGap = max(r.maxGap, gap)
		}
		r.last = now
		r.frames++
		if v, ok := decodeClock(buf, width, height); ok {
			if v == r.lastTick {
				r.dupes++
			}
			r.lastTick = v
			latency := clockLatency(v, now)
			r.latencies = append(r.latencies, latency)
			if latency > 300*time.Millisecond && r.spikes < 12 {
				r.spikes++
				fmt.Printf("    [%s] spike: frame %d at +%.1fs is %.0f ms behind\n",
					r.label, r.frames, now.Sub(r.first).Seconds(), float64(latency.Microseconds())/1000)
			}
		} else {
			r.undecoded++
		}
		r.mu.Unlock()
	}
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	return sorted[min(int(float64(len(sorted))*p), len(sorted)-1)]
}

func (r *watchRun) report(t *testing.T, fps int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frames == 0 {
		t.Errorf("[%s] NO FRAMES arrived", r.label)
		return
	}
	span := r.last.Sub(r.first).Seconds()
	rate := 0.0
	if span > 0 {
		rate = float64(r.frames-1) / span
	}
	stalls := 0
	for _, g := range r.gaps {
		if g > 3*time.Second/time.Duration(fps) {
			stalls++
		}
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

	t.Logf("[%s] first frame %.0f ms after start (%.0f ms after decoder open); "+
		"%d frames over %.1fs = %.1f fps; undecodable %d, duplicate ticks %d",
		r.label, ms(r.first.Sub(r.started)), ms(r.first.Sub(r.opened)), r.frames, span, rate, r.undecoded, r.dupes)
	t.Logf("[%s] latency p50 %.0f ms  p90 %.0f ms  p99 %.0f ms  max %.0f ms  (n=%d)",
		r.label, ms(percentile(r.latencies, 0.5)), ms(percentile(r.latencies, 0.9)),
		ms(percentile(r.latencies, 0.99)), ms(percentile(r.latencies, 1)), len(r.latencies))
	t.Logf("[%s] frame gap p50 %.0f ms  p99 %.0f ms  max %.0f ms; gaps over 3 frames: %d",
		r.label, ms(percentile(r.gaps, 0.5)), ms(percentile(r.gaps, 0.99)), ms(r.maxGap), stalls)

	if len(r.latencies) > 0 && percentile(r.latencies, 0.5) > 1500*time.Millisecond {
		t.Errorf("[%s] LATENCY: median %.0f ms", r.label, ms(percentile(r.latencies, 0.5)))
	}
	if r.maxGap > 2*time.Second {
		t.Errorf("[%s] FREEZE: %.0f ms without a frame", r.label, ms(r.maxGap))
	}
	// Tk paints the blocks one by one, and a 60 Hz capture lands mid-paint
	// on a third of them — a torn clock fails its checksum and is left out
	// of the latency figures, which is the harness's own artefact rather
	// than the pipeline's. Only a picture that is mostly not the clock fails.
	if r.undecoded > r.frames/2 {
		t.Errorf("[%s] PICTURE: %d of %d frames were not the clock", r.label, r.undecoded, r.frames)
	}
	if r.cpu > 0 && span > 0 {
		t.Logf("[%s] decoder: %.2fs of processor time over %.1fs = %.1f%% of a core",
			r.label, r.cpu.Seconds(), span, 100*r.cpu.Seconds()/span)
	}
}

// sampleFFmpeg logs every ffmpeg child's processor time against its age —
// the encoder and the decoders are the whole cost of a share. Windows only;
// elsewhere it says nothing.
func sampleFFmpeg(t *testing.T, label string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}

	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-Process ffmpeg -ErrorAction SilentlyContinue | ForEach-Object { `+
			`$age = ((Get-Date) - $_.StartTime).TotalSeconds; `+
			`"{0} cpu={1:N2}s age={2:N1}s load={3:N1}% rss={4:N0}MB" -f $_.Id, $_.CPU, $age, (100*$_.CPU/$age), ($_.WorkingSet64/1MB) }`).Output()
	if err != nil {
		t.Logf("[%s] ffmpeg sample: %v", label, err)
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			t.Logf("[%s] ffmpeg %s", label, line)
		}
	}
}

/* The test */

func TestLiveScreenshare(t *testing.T) {
	if os.Getenv("RGO_SHARE_LIVE") == "" {
		t.Skip("set RGO_SHARE_LIVE=1 to run against the real server")
	}
	clockScript := os.Getenv("RGO_SHARE_CLOCK")
	if clockScript == "" {
		t.Skip("set RGO_SHARE_CLOCK to the clock.py path")
	}
	fps := 30
	if v, err := strconv.Atoi(os.Getenv("RGO_SHARE_FPS")); err == nil && v > 0 {
		fps = v
	}
	if !filepath.IsAbs(clockScript) {
		clockScript = filepath.Join("..", "..", clockScript) // go test runs in the package directory
	}
	clockArgs := []string{clockScript}
	if size := os.Getenv("RGO_SHARE_CLOCK_SIZE"); size != "" {
		w, h, _ := strings.Cut(size, "x")
		clockArgs = append(clockArgs, w, h)
	}
	settings := config.Default().Screenshare
	if os.Getenv("RGO_SHARE_CODEC") == "h264" {
		settings.Codec = config.ShareCodecH264
	}

	tools, ok := video.Discover()
	if !ok {
		t.Skip("no ffmpeg on PATH")
	}

	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".rgoclient_sessions.json"))
	if err != nil {
		t.Skipf("no saved session: %v", err)
	}
	var accounts []liveAccount
	if err := json.Unmarshal(data, &accounts); err != nil || len(accounts) < 2 {
		t.Skipf("two saved accounts needed: %v", err)
	}
	sender, watcher := accounts[0], accounts[1]
	t.Logf("sender  %s (%s)", sender.Username, sender.UserID)
	t.Logf("watcher %s (%s)", watcher.Username, watcher.UserID)

	// The clock window, then the enumeration that finds it.
	clock := exec.Command("python", clockArgs...)
	clock.Stdout, clock.Stderr = os.Stdout, os.Stderr
	if err := clock.Start(); err != nil {
		t.Fatalf("clock: %v", err)
	}
	defer func() { _ = clock.Process.Kill() }()
	time.Sleep(1500 * time.Millisecond)

	sources, err := video.ShareSources()
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	var source video.CaptureSource
	found := false
	for _, s := range sources {
		if s.Kind == video.CaptureWindow && s.Title == "RGO-CLOCK" {
			source, found = s, true
		}
	}
	if !found {
		t.Fatalf("the clock window was not enumerated (%d sources)", len(sources))
	}
	t.Logf("capturing window %q %dx%d; fallback path: %v",
		source.Title, source.Width, source.Height, tools.CaptureFallback([]video.CaptureSource{source}))

	senderClient, senderReady := openLive(t, sender)
	defer senderClient.Close()
	watcherClient, watcherReady := openLive(t, watcher)
	defer watcherClient.Close()

	senderChannels := voiceChannelsIn(senderClient.Store(), senderReady)
	watcherChannels := voiceChannelsIn(watcherClient.Store(), watcherReady)
	channelID := ""
	shared := []string{}
	for id, name := range watcherChannels {
		if _, ok := senderChannels[id]; !ok {
			continue
		}
		shared = append(shared, id)
		if name == "jerome / vc" {
			channelID = id
		}
	}
	sort.Strings(shared)
	if channelID == "" && len(shared) > 0 {
		channelID = shared[0]
	}
	if channelID == "" {
		t.Fatalf("no shared voice channel")
	}
	t.Logf("channel %s (%s)", watcherChannels[channelID], channelID)

	// Sender joins.
	senderCreds, err := senderClient.JoinCall(channelID, true)
	if err != nil {
		t.Fatalf("sender join_call: %v", err)
	}
	senderCall, err := voice.Join(senderCreds, silenceSource{}, newNullSink(), voice.Options{Muted: true, SelfID: sender.UserID})
	if err != nil {
		t.Fatalf("sender Join: %v", err)
	}
	defer senderCall.Close()
	t.Logf("sender in the room; CanShare=%v", senderCall.CanShare())

	senderStopped := make(chan struct{}, 4)
	go func() {
		for event := range senderCall.Events() {
			switch e := event.(type) {
			case voice.ShareStopped:
				t.Logf("  sender: ShareStopped")
				senderStopped <- struct{}{}
			case voice.ConnectionChanged:
				t.Logf("  sender connection %s", e.State)
			case voice.CallEnded:
				t.Logf("  sender ended: %v", e.Err)
			}
		}
	}()

	// Watcher joins.
	watcherCreds, err := watcherClient.JoinCall(channelID, true)
	if err != nil {
		t.Fatalf("watcher join_call: %v", err)
	}
	watcherCall, err := voice.Join(watcherCreds, silenceSource{}, newNullSink(), voice.Options{Muted: true, SelfID: watcher.UserID})
	if err != nil {
		t.Fatalf("watcher Join: %v", err)
	}
	defer watcherCall.Close()

	shareEnded := make(chan voice.ShareEnded, 4)
	go func() {
		for event := range watcherCall.Events() {
			switch e := event.(type) {
			case voice.ShareEnded:
				t.Logf("  watcher: ShareEnded from %s err=%v", e.UserID, e.Err)
				shareEnded <- e
			case voice.ParticipantChanged:
				t.Logf("  watcher sees %q joined=%v", e.UserID, e.Joined)
			case voice.ConnectionChanged:
				t.Logf("  watcher connection %s", e.State)
			case voice.StatsChanged:
				t.Logf("  watcher stats: %+v", e)
			case voice.CallEnded:
				t.Logf("  watcher ended: %v", e.Err)
			}
		}
	}()

	// Sharing, the way beginShare does it.
	width, height := fitShareBox(source.Width, source.Height, shareFallbackLimits)
	startShare := func() *sendingShare {
		t.Helper()
		enc, ok := tools.ShareEncoder(captureCodec(settings.Codec))
		if !ok {
			t.Fatalf("no encoder")
		}
		stream, err := tools.CaptureShare(video.CaptureConfig{
			Source: source, Width: width, Height: height, FPS: fps,
			Bitrate:         shareBitrate(width, height, fps, enc.AV1, settings),
			KeyframeSeconds: shareKeyframeSeconds(settings.Keyframes),
			Codec:           captureCodec(settings.Codec),
			Speed:           captureSpeed(settings.EncoderSpeed),
			Latency:         captureLatency(settings.Latency),
			Rate:            captureRate(settings.RateControl),
		})
		if err != nil {
			t.Fatalf("CaptureShare: %v", err)
		}
		sending := &sendingShare{stream: stream, tee: video.NewShareTee(stream, enc.AV1, width, height), av1: enc.AV1,
			encoder: enc.Name, width: width, height: height}
		codec := voice.SendShareH264
		if enc.AV1 {
			codec = voice.SendShareAV1
		}
		began := time.Now()
		if err := senderCall.StartShare(sending.tee, codec, width, height, fps); err != nil {
			sending.halt()
			t.Fatalf("StartShare: %v", err)
		}
		t.Logf("sharing %dx%d@%d as %s via %s (%d kbit/s); publish took %.0f ms",
			width, height, fps, codec, enc.Name, shareBitrate(width, height, fps, enc.AV1, settings)/1000,
			float64(time.Since(began).Microseconds())/1000)

		return sending
	}

	// One watch: WatchShare polled until the publication is known.
	watch := func(label string) *watchRun {
		t.Helper()
		run := newWatchRun(label)
		deadline := time.Now().Add(20 * time.Second)
		for {
			err := watcherCall.WatchShare(sender.UserID, run.open(tools))
			if err == nil {
				break
			}
			if !errors.Is(err, voice.ErrNoShare) {
				t.Fatalf("[%s] WatchShare: %v", label, err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("[%s] the share never appeared in the room", label)
			}
			time.Sleep(100 * time.Millisecond)
			run.started = time.Now()
		}
		t.Logf("[%s] WatchShare accepted", label)

		return run
	}
	unwatch := func(run *watchRun) {
		t.Helper()
		began := time.Now()
		watcherCall.UnwatchShare(sender.UserID)
		select {
		case <-run.done:
			t.Logf("[%s] unwatched; decoder ended %.0f ms later", run.label, float64(time.Since(began).Microseconds())/1000)
		case <-time.After(10 * time.Second):
			t.Errorf("[%s] TEARDOWN: decoder still running 10 s after UnwatchShare", run.label)
		}
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

	sending := startShare()

	// 1. A long watch: latency, rate and freezes — and, part-way through,
	// this end's own preview attached to the tee, decoded through the same
	// child and measured the same way.
	run := watch("watch-1")
	time.Sleep(5 * time.Second)

	preview := newWatchRun("preview")
	in, err := preview.openStream(tools, voice.ShareIVF, sending.width, sending.height)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	sending.tee.Attach(in)
	time.Sleep(8 * time.Second)
	sampleFFmpeg(t, "watch-1+preview")
	began := time.Now()
	sending.tee.Detach()
	select {
	case <-preview.done:
		t.Logf("[preview] detached; decoder ended %.0f ms later", ms(time.Since(began)))
	case <-time.After(10 * time.Second):
		t.Errorf("[preview] TEARDOWN: decoder still running 10 s after Detach")
	}
	preview.report(t, fps)

	time.Sleep(7 * time.Second)
	unwatch(run)
	run.report(t, fps)

	// 2/3. Close and reopen, twice, with a pause between to catch a stale
	// subscription or a decoder that will not restart. The pauses are not
	// multiples of the keyframe interval, so the join wait is sampled at
	// different phases of the GOP.
	for i := 2; i <= 3; i++ {
		time.Sleep(time.Duration(700*i) * time.Millisecond)
		run = watch(fmt.Sprintf("watch-%d", i))
		time.Sleep(6500 * time.Millisecond)
		unwatch(run)
		run.report(t, fps)
	}

	// 4. The sender stops under a running watch: the watcher must be told
	// and its decoder must end.
	run = watch("watch-4")
	time.Sleep(4300 * time.Millisecond)
	sampleFFmpeg(t, "watch-4")
	began = time.Now()
	senderCall.StopShare()
	sending.halt()
	select {
	case e := <-shareEnded:
		t.Logf("[watch-4] ShareEnded %.0f ms after StopShare (err=%v)", ms(time.Since(began)), e.Err)
	case <-time.After(10 * time.Second):
		t.Errorf("[watch-4] NO ShareEnded 10 s after the sender stopped")
	}
	select {
	case <-run.done:
		t.Logf("[watch-4] decoder ended %.0f ms after StopShare", ms(time.Since(began)))
	case <-time.After(10 * time.Second):
		t.Errorf("[watch-4] TEARDOWN: decoder still running 10 s after the sender stopped")
	}
	run.report(t, fps)

	// 5. The sender shares again and the watcher watches again.
	time.Sleep(1300 * time.Millisecond)
	sending = startShare()
	run = watch("watch-5")
	time.Sleep(7 * time.Second)
	unwatch(run)
	run.report(t, fps)

	// 6. Sender's own stream dying (the window closing): ShareStopped.
	began = time.Now()
	_ = clock.Process.Kill()
	select {
	case <-senderStopped:
		t.Logf("[end] ShareStopped %.0f ms after the captured window died", ms(time.Since(began)))
	case <-time.After(10 * time.Second):
		t.Errorf("[end] NO ShareStopped 10 s after the captured window died")
	}
	sending.halt()
}
