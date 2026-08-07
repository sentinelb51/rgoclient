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
