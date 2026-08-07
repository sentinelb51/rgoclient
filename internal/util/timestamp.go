package util

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"RGOClient/internal/config"
)

const (
	dayLayout   = "January 2, 2006"
	daysInMonth = 30
	daysInYear  = 365
)

// timeLayout is the configured clock format. It is read per call rather than
// resolved once: changing the format takes effect on the next repaint, and every
// caller here is already formatting a string.
func timeLayout() string {
	settings := config.Current().Interface

	twelveHour := settings.TimeFormat != config.TimeFormat24

	switch {
	case twelveHour && settings.ShowSeconds:
		return "3:04:05 PM"
	case twelveHour:
		return "3:04 PM"
	case settings.ShowSeconds:
		return "15:04:05"
	}

	return "15:04"
}

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

// FullDate names a calendar day outright, for the dates a profile carries —
// when an account was made, or when someone joined a server — where "Today" and
// a relative age tell the reader less than the day itself.
func FullDate(t time.Time) string {
	return t.Local().Format(dayLayout)
}

// ShortTime formats just the local clock time, for the gutter timestamp on
// grouped continuation messages.
func ShortTime(t time.Time) string {
	return t.Local().Format(timeLayout())
}

// NiceTime formats a message time the way a chat client reads it: the clock time
// for today and yesterday, then a coarsening relative age.
func NiceTime(t time.Time) string {
	t, now := t.Local(), time.Now().Local()

	if t.After(now) {
		return "Just now" // clock skew between us and the server
	}

	// Compare calendar days, not elapsed hours: both dates are rebuilt at
	// midnight UTC so a 23- or 25-hour DST day can't skew the division.
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(nowDate.Sub(tDate).Hours() / 24)

	switch {
	case days == 0:
		return fmt.Sprintf("Today, %s", t.Format(timeLayout()))
	case days == 1:
		return fmt.Sprintf("Yesterday, %s", t.Format(timeLayout()))
	case days < daysInMonth:
		return fmt.Sprintf("%d days ago, %s", days, t.Format(timeLayout()))
	case days < daysInYear:
		return plural(days/daysInMonth, "month")
	default:
		return plural(days/daysInYear, "year")
	}
}

// ShortDuration names a span the way a countdown shows one: "5s", "1m 30s",
// "2h". It rounds *up* to the next whole second, so a cooldown with a fraction
// of a second left still reads "1s" rather than "0s" — the badge drawn from this
// must not claim the wait is over before it is.
func ShortDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = (d + time.Second - 1).Truncate(time.Second)

	hours, minutes, seconds := int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60

	switch {
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case minutes > 0 && seconds > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	}

	return fmt.Sprintf("%ds", seconds)
}

// plural renders "1 month ago" / "3 months ago".
func plural(count int, unit string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s ago", count, unit)
	}

	return fmt.Sprintf("%d %ss ago", count, unit)
}
