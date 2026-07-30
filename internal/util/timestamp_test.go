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
