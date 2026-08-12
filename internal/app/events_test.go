package app

import (
	"testing"

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
