package app

// Measurement harness for the message column — what a channel switch, a wheel
// tick, a page of history, a live message and a frame's min-size walk cost at
// the default open size and at the mounted cap. Run with:
//
//	go test ./internal/app -run xxx -bench . -benchmem

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"
	"github.com/oklog/ulid/v2"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

const (
	benchChannel = "01BENCHCHANNEL000000000000"
	benchWidth   = 1400
	benchHeight  = 900
)

/* The three calls that name the column's API — the only lines that change
   between the flat column and the virtual one. */

// scrollColumnTop moves the column to its start.
func scrollColumnTop(a *App) { a.messages.ScrollToTop() }

// scrollColumnMid centres the column on the middle of its window.
func scrollColumnMid(a *App) { a.messages.Reveal(a.messages.Message(a.messages.Len() / 2).ID) }

// mountEvery puts every message in the window at once, as a jump does.
func mountEvery(a *App, messages []*domain.Message) {
	a.mountJumpWindow(messages, messages[len(messages)/2].ID)
}

/* Scaffold */

// benchApp builds the main UI in a desktop-sized test window with n messages in
// the open channel's cache, so every path renders from cache as the client does.
func benchApp(tb testing.TB, n int) (*App, fyne.Window, []*domain.Message) {
	tb.Helper()

	fyneApp := test.NewTempApp(tb)
	fyneApp.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	a := New(fyneApp, Info{})
	root := a.buildUI()

	window := test.NewWindow(root)
	window.SetPadded(false)
	window.Resize(fyne.NewSize(benchWidth, benchHeight))
	tb.Cleanup(window.Close)

	messages := benchMessages(n)
	page := make([]*domain.Message, n) // the cache takes an API page, newest first
	for i, message := range messages {
		page[n-1-i] = message
	}
	a.client.Messages().Set(benchChannel, page)
	a.client.Messages().SetDepleted(benchChannel, true)
	a.currentChannelID = benchChannel

	return a, window, messages
}

// quiet stops the timers an operation arms — the settle re-scroll, the author and
// reply fetches — which the test driver would otherwise run on their own
// goroutines against the tree being measured.
func quiet(a *App) {
	for _, timer := range []*time.Timer{a.settleTimer, a.authorTimer, a.replyTimer, a.editMarkTimer} {
		if timer != nil {
			timer.Stop()
		}
	}
}

// benchMessages is a conversation shaped like a real one: runs of a few messages
// per author close enough to group, a mix of one-liners, paragraphs and markdown,
// and the occasional picture, quote, reaction and system event. Two day breaks.
func benchMessages(n int) []*domain.Message {
	authors := []string{"01AUTHORA00000000000000000", "01AUTHORB00000000000000000", "01AUTHORC00000000000000000", "01AUTHORD00000000000000000"}
	texts := []string{
		"ok",
		"sounds good, see you there",
		"I pushed the fix for the scroll offset — it was clamping against the pre-mount size, so the column scrolled as though it still fitted the viewport.",
		"first line\nsecond line\nthird line",
		strings.Repeat("This is a long paragraph that will wrap several times across the column at any sensible width. ", 3),
		"**bold** and _italic_ with `inline code` and a link https://example.com/path",
	}

	at := time.Date(2026, 8, 1, 9, 0, 0, 0, time.Local)
	messages := make([]*domain.Message, n)
	for i := range n {
		at = at.Add(100 * time.Second)
		if i > 0 && i%100 == 0 {
			at = at.Add(14 * time.Hour)
		}

		message := &domain.Message{
			ID:        ulidAt(at),
			ChannelID: benchChannel,
			AuthorID:  authors[(i/4)%len(authors)],
			Content:   texts[i%len(texts)],
		}
		switch {
		case i%19 == 0:
			message.Attachments = []*domain.File{{ID: "01FILE", Name: "photo.png", Kind: domain.FileImage, Size: 1 << 20, Width: 1200, Height: 800}}
		case i%23 == 0 && i > 0:
			message.Replies = []string{messages[i-1].ID}
		case i%37 == 0:
			message.Content = ""
			message.System = &domain.SystemMessage{Kind: domain.SystemUserJoined, Target: message.AuthorID}
		case i%29 == 0:
			message.Reactions = []domain.Reaction{{Emoji: "👍", Users: []string{authors[0], authors[1]}}}
		}
		messages[i] = message
	}

	return messages
}

// ulidAt is a message ID whose timestamp is t — what grouping and day labels read.
func ulidAt(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), ulid.DefaultEntropy()).String()
}

// frameWalk is what the driver's EnsureMinSize does on every dirty frame: a
// post-order walk of the whole tree asking every visible object its MinSize. It
// reports how many objects the walk visited.
func frameWalk(obj fyne.CanvasObject) int {
	var children []fyne.CanvasObject
	switch o := obj.(type) {
	case *fyne.Container:
		children = o.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(o).Objects()
	}

	count := 1
	for _, child := range children {
		count += frameWalk(child)
	}
	if obj.Visible() {
		obj.MinSize()
	}

	return count
}

// mountFor mounts n messages the way the client reaches that many: the default
// open mounts InitialMountCount, and a jump mounts a whole window.
func mountFor(a *App, messages []*domain.Message, n int) {
	if n <= initialMountCount() {
		a.displayCached()
	} else {
		mountEvery(a, messages[len(messages)-n:])
	}
	quiet(a)
}

/* Benchmarks */

func BenchmarkOpenChannel(b *testing.B) {
	a, _, _ := benchApp(b, 250)

	b.ReportAllocs()
	for b.Loop() {
		a.displayCached()
		quiet(a)
	}
}

func BenchmarkWheelTick(b *testing.B) {
	for _, n := range []int{50, 250} {
		b.Run(fmt.Sprintf("mounted=%d", n), func(b *testing.B) {
			a, window, messages := benchApp(b, 250)
			mountFor(a, messages, n)
			scrollColumnMid(a)

			canvas := window.Canvas()
			inColumn := fyne.NewPos(700, 400)

			b.ReportAllocs()
			for b.Loop() {
				test.Scroll(canvas, inColumn, 0, 10)
				test.Scroll(canvas, inColumn, 0, -10)
			}
		})
	}
}

func BenchmarkPrependPage(b *testing.B) {
	a, _, _ := benchApp(b, 250)

	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		a.displayCached()
		quiet(a)
		scrollColumnTop(a)
		b.StartTimer()

		a.loadMoreHistory()
	}
}

func BenchmarkAppendLive(b *testing.B) {
	a, _, _ := benchApp(b, 250)
	a.displayCached()
	quiet(a)

	at := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		at = at.Add(time.Second)
		message := &domain.Message{ID: ulidAt(at), ChannelID: benchChannel, AuthorID: "01AUTHORZ00000000000000000", Content: "a message arriving"}
		previous := a.client.Messages().Append(benchChannel, message)

		a.appendMessage(message, previous)
		quiet(a)
	}
}

func BenchmarkFrameWalk(b *testing.B) {
	for _, n := range []int{50, 250} {
		b.Run(fmt.Sprintf("mounted=%d", n), func(b *testing.B) {
			a, window, messages := benchApp(b, 250)
			mountFor(a, messages, n)
			root := window.Content()

			objects := frameWalk(root)
			for b.Loop() {
				frameWalk(root)
			}
			b.ReportMetric(float64(objects), "objects") // after the loop: b.Loop's timer reset clears metrics
		})
	}
}

// liveDelta is the live heap one mount of n adds, GC'd either side, as the
// median of several samples — a single one is at the mercy of whatever else
// happens to be collectable at that moment.
func liveDelta(a *App, root fyne.CanvasObject, messages []*domain.Message, n int) float64 {
	samples := make([]float64, 0, 7)
	for range cap(samples) {
		a.clearMessages()
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		mountFor(a, messages, n)
		frameWalk(root)

		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&after)
		samples = append(samples, float64(after.HeapAlloc)-float64(before.HeapAlloc))
	}
	slices.Sort(samples)

	return samples[len(samples)/2]
}

// BenchmarkMountedFootprint reports the live heap the column holds once mounted
// and measured, at the default open size and at the cap, after a warm-up mount
// has filled the process-wide caches — fonts, measurements, icon rasters — that
// a first mount would otherwise be charged for.
func BenchmarkMountedFootprint(b *testing.B) {
	for _, n := range []int{50, 250} {
		b.Run(fmt.Sprintf("mounted=%d", n), func(b *testing.B) {
			a, window, messages := benchApp(b, 250)
			root := window.Content()

			mountFor(a, messages, n)
			frameWalk(root)
			delta := liveDelta(a, root, messages, n)
			objects := frameWalk(root)

			for b.Loop() {
				mountFor(a, messages, n)
			}
			b.ReportMetric(delta/1024, "KiB-live")
			b.ReportMetric(float64(objects), "objects")
		})
	}
}
