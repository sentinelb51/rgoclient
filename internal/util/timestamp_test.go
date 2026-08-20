package util

import (
	"testing"
	"time"
)

func TestSameDay(t *testing.T) {
	local := time.Local
	cases := []struct {
		name string
		a, b time.Time
		want bool
	}{
		{
			"same day, hours apart",
			time.Date(2026, 7, 29, 0, 0, 1, 0, local),
			time.Date(2026, 7, 29, 23, 59, 59, 0, local),
			true,
		},
		{
			"two minutes apart across midnight",
			time.Date(2026, 7, 29, 23, 59, 0, 0, local),
			time.Date(2026, 7, 30, 0, 1, 0, 0, local),
			false,
		},
		{
			"same date a year apart",
			time.Date(2025, 7, 29, 12, 0, 0, 0, local),
			time.Date(2026, 7, 29, 12, 0, 0, 0, local),
			false,
		},
		{
			"one instant in two zones is one day",
			time.Date(2026, 7, 29, 12, 0, 0, 0, local),
			time.Date(2026, 7, 29, 12, 0, 0, 0, local).UTC(),
			true,
		},
	}

	for _, c := range cases {
		if got := SameDay(c.a, c.b); got != c.want {
			t.Errorf("SameDay(%v, %v) = %v, want %v [%s]", c.a, c.b, got, c.want, c.name)
		}
	}
}

func TestShortDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"whole seconds", 5 * time.Second, "5s"},
		{"a part second still has to be waited out", 1500 * time.Millisecond, "2s"},
		{"the last sliver never reads as none left", time.Millisecond, "1s"},
		{"nothing left", 0, "0s"},
		{"a cooldown already over", -2 * time.Second, "0s"},
		{"a round minute drops the seconds", time.Minute, "1m"},
		{"a minute and change keeps them", 90 * time.Second, "1m 30s"},
		{"an hour drops the seconds either way", time.Hour + 30*time.Second, "1h"},
		{"an hour and change keeps the minutes", 90 * time.Minute, "1h 30m"},
	}

	for _, c := range cases {
		if got := ShortDuration(c.d); got != c.want {
			t.Errorf("ShortDuration(%v) = %q, want %q [%s]", c.d, got, c.want, c.name)
		}
	}
}

// TestRelativeSpan covers the boundaries, which is where a coarsening span reads
// wrongly: the unit has to change before its count would, and it has to answer
// forwards as well as back — a timestamp in a body is as often a deadline as a
// record. It asks about a distance rather than about now plus one: the clock
// moves between the two calls, and truncation turns that sliver into a lost
// unit on any platform whose clock is finer than the gap.
func TestRelativeSpan(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"a moment either way is no distance", 10 * time.Second, "just now"},
		{"just under a minute still rounds to one", 50 * time.Second, "in 1 minute"},
		{"minutes below the hour", 45 * time.Minute, "in 45 minutes"},
		{"the hour changes the unit", time.Hour, "in 1 hour"},
		{"hours below the day", 23 * time.Hour, "in 23 hours"},
		{"the day changes the unit", 24 * time.Hour, "in 1 day"},
		{"a month is thirty days", 30 * 24 * time.Hour, "in 1 month"},
		{"a year is three hundred and sixty five", 365 * 24 * time.Hour, "in 1 year"},
		{"and it looks backwards too", -2 * time.Hour, "2 hours ago"},
		{"backwards past a year", -800 * 24 * time.Hour, "2 years ago"},
	}

	for _, c := range cases {
		if got := relativeSpan(c.d); got != c.want {
			t.Errorf("relativeSpan(%v) = %q, want %q [%s]", c.d, got, c.want, c.name)
		}
	}

	// Off a boundary, so the clock is free to move: that RelativeTime signs the
	// distance the right way round is the half the table cannot see.
	if got, want := RelativeTime(time.Now().Add(-90*time.Minute)), "1 hour ago"; got != want {
		t.Errorf("RelativeTime(90m ago) = %q, want %q", got, want)
	}
}

// TestMessageTimestamp pins that each style draws a different face of the same
// instant. The clock half follows the configured format, so only what the style
// itself decides is asserted — the date, the weekday, and that "R" is the one
// that does not name a date at all.
func TestMessageTimestamp(t *testing.T) {
	moment := time.Date(2026, 7, 13, 15, 4, 5, 0, time.Local)
	clock := moment.Format(clockLayout(false))

	cases := []struct {
		style string
		want  string
	}{
		{"d", "13/07/2026"},
		{"D", "July 13, 2026"},
		{"F", "Monday, July 13, 2026 " + clock},
		{"", "July 13, 2026 " + clock},
		{"R", RelativeTime(moment)},
	}

	for _, c := range cases {
		if got := MessageTimestamp(moment, c.style); got != c.want {
			t.Errorf("MessageTimestamp(%q) = %q, want %q", c.style, got, c.want)
		}
	}
}

func TestDayLabel(t *testing.T) {
	now := time.Now().Local()

	if got := DayLabel(now); got != "Today" {
		t.Errorf("DayLabel(now) = %q, want %q", got, "Today")
	}
	if got := DayLabel(now.AddDate(0, 0, -1)); got != "Yesterday" {
		t.Errorf("DayLabel(yesterday) = %q, want %q", got, "Yesterday")
	}

	old := time.Date(2026, 7, 13, 15, 4, 0, 0, time.Local)
	if got, want := DayLabel(old), "July 13, 2026"; got != want {
		t.Errorf("DayLabel(%v) = %q, want %q", old, got, want)
	}
}
