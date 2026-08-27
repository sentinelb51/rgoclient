package app

import (
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/config"
	"RGOClient/internal/ui/theme"
)

// TestRefreshQueueGathersOneWindow covers the rule the whole realtime path rests
// on: the settling window is armed by the first event of a burst and is *not*
// restarted by the ones behind it. Restarting it reads like the more careful
// thing and is the bug — presence on a large server arrives faster than any
// window worth having, so a window that renewed itself would never elapse and
// the sidebar would stop updating exactly on the servers this exists for.
func TestRefreshQueueGathersOneWindow(t *testing.T) {
	fyneApp := test.NewTempApp(t)
	fyneApp.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	// Long enough that the window cannot elapse during the test: what is being
	// checked is the bookkeeping, not the clock.
	settings := *config.Current()
	t.Cleanup(func() { config.Update(func(s *config.Settings) { *s = settings }) })
	config.Update(func(s *config.Settings) { s.Behaviour.RefreshDelayMS = 60_000 })

	a := New(fyneApp, Info{})
	a.buildUI()

	a.queueRefresh(refreshServers)
	armed := a.refreshTimer
	if armed == nil || a.dirty != refreshServers {
		t.Fatalf("one event left dirty=%b, timer=%v", a.dirty, armed != nil)
	}
	t.Cleanup(func() { armed.Stop() })

	a.queueRefresh(refreshChannels)
	a.queueRefresh(refreshMembers | refreshChannels)

	if want := refreshServers | refreshChannels | refreshMembers; a.dirty != want {
		t.Errorf("dirty = %b, want every target of the burst %b", a.dirty, want)
	}
	if a.refreshTimer != armed {
		t.Error("an event inside the window replaced the timer it should have joined")
	}

	a.flushRefresh()

	if a.dirty != 0 {
		t.Errorf("dirty = %b after a flush, want nothing left", a.dirty)
	}
	if a.refreshTimer != nil {
		t.Error("the flush left a timer armed, so the next event would never arm one")
	}
}

// TestGatherTakesTheBurstInOrder covers what a batched pump can get wrong
// without anything looking wrong. Both pumps hand the batch to one hop and
// dispatch it in slice order, so an event gathered out of order, dropped on the
// floor or left behind in the channel is a handler that simply never runs — no
// panic, no log, and the store is right afterwards either way, which is what
// makes it invisible. The events that pay for it are the ones a burst is made
// of: a rank reorder is an event per role and a presence storm is continuous.
func TestGatherTakesTheBurstInOrder(t *testing.T) {
	t.Run("drains what is queued, in arrival order", func(t *testing.T) {
		queue := make(chan int, 8)
		for i := 2; i <= 5; i++ {
			queue <- i
		}

		batch := gather(make([]int, 0, 8), 1, queue, 8)

		if want := []int{1, 2, 3, 4, 5}; !slices.Equal(batch, want) {
			t.Errorf("batch = %v, want %v", batch, want)
		}
		if len(queue) != 0 {
			t.Errorf("%d left in the channel; a burst must not be split across hops", len(queue))
		}
	})

	t.Run("stops at max and leaves the rest", func(t *testing.T) {
		queue := make(chan int, 8)
		for i := 2; i <= 8; i++ {
			queue <- i
		}

		// The cap is the reason this is off-by-one country: it is a length, and a
		// gather that took max *more* than the first would overrun the buffer the
		// pump reuses.
		batch := gather(make([]int, 0, 3), 1, queue, 3)

		if want := []int{1, 2, 3}; !slices.Equal(batch, want) {
			t.Errorf("batch = %v, want %v", batch, want)
		}
		if len(queue) != 5 {
			t.Errorf("%d left for the next batch, want the 5 the cap did not reach", len(queue))
		}
	})

	t.Run("does not wait for what has not arrived", func(t *testing.T) {
		queue := make(chan int, 8)

		// No sender at all: an empty channel must answer now. A gather that blocked
		// here would hold the event in hand hostage to one that may never come.
		done := make(chan []int, 1)
		go func() { done <- gather(make([]int, 0, 8), 1, queue, 8) }()

		select {
		case batch := <-done:
			if want := []int{1}; !slices.Equal(batch, want) {
				t.Errorf("batch = %v, want %v", batch, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("gather blocked on an empty channel")
		}
	})

	t.Run("keeps what it holds when the channel closes", func(t *testing.T) {
		queue := make(chan int, 8)
		queue <- 2
		close(queue)

		// A closed channel is always ready, so the naive loop spins to max appending
		// zero values — and the pump then dispatches events nobody sent.
		batch := gather(make([]int, 0, 8), 1, queue, 8)

		if want := []int{1, 2}; !slices.Equal(batch, want) {
			t.Errorf("batch = %v, want %v — a zero value here is an event nobody sent", batch, want)
		}
	})

	t.Run("reuses the buffer without carrying the last batch", func(t *testing.T) {
		queue := make(chan int, 8)
		queue <- 2

		batch := gather(make([]int, 0, 8), 1, queue, 8)

		// What pumpEvents does between bursts. Truncating to anything but zero
		// re-dispatches the batch before it.
		queue <- 9
		batch = gather(batch[:0], 8, queue, 8)

		if want := []int{8, 9}; !slices.Equal(batch, want) {
			t.Errorf("batch = %v, want %v", batch, want)
		}
	})
}
