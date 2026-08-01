package util

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	timeLayout  = "3:04 PM"
	dayLayout   = "January 2, 2006"
	daysInMonth = 30
	daysInYear  = 365
)

// Timestamp parses a ULID to extract its embedded timestamp.
func Timestamp(id string) (time.Time, error) {
	value, err := ulid.Parse(id)
	if err != nil {
		return time.Time{}, err
	}

	return value.Timestamp(), nil
}

// SameDay reports whether two times fall on the same local calendar day, so a
// pair minutes apart across midnight is correctly treated as two days.
func SameDay(a, b time.Time) bool {
	a, b = a.Local(), b.Local()
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()

	return ay == by && am == bm && ad == bd
}

// DayLabel names a calendar day for the message list's day separator: "Today"
// and "Yesterday" for the two most recent, the full date before that.
func DayLabel(t time.Time) string {
	t, now := t.Local(), time.Now().Local()

	switch {
	case SameDay(t, now):
		return "Today"
	case SameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	default:
		return t.Format(dayLayout)
	}
}

// ShortTime formats just the local clock time, for the gutter timestamp on
// grouped continuation messages.
func ShortTime(t time.Time) string {
	return t.Local().Format(timeLayout)
}

// NiceTime formats a message time the way a chat client reads it: the clock time
// for today and yesterday, then a coarsening relative age.
func NiceTime(t time.Time) string {
	t, now := t.Local(), time.Now().Local()

	if t.After(now) {
		return "A few moments ago" // clock skew between us and the server
	}

	// Compare calendar days, not elapsed hours: both dates are rebuilt at
	// midnight UTC so a 23- or 25-hour DST day can't skew the division.
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(nowDate.Sub(tDate).Hours() / 24)

	switch {
	case days == 0:
		return fmt.Sprintf("Today, %s", t.Format(timeLayout))
	case days == 1:
		return fmt.Sprintf("Yesterday, %s", t.Format(timeLayout))
	case days < daysInMonth:
		return fmt.Sprintf("%d days ago, %s", days, t.Format(timeLayout))
	case days < daysInYear:
		return plural(days/daysInMonth, "month")
	default:
		return plural(days/daysInYear, "year")
	}
}

// plural renders "1 month ago" / "3 months ago".
func plural(count int, unit string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s ago", count, unit)
	}

	return fmt.Sprintf("%d %ss ago", count, unit)
}
