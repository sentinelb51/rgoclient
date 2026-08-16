package util

import (
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"RGOClient/internal/config"
)

const (
	dayLayout     = "January 2, 2006"
	weekdayLayout = "Monday, January 2, 2006"
	numericLayout = "02/01/2006"

	day         = 24 * time.Hour
	daysInMonth = 30
	daysInYear  = 365
)

// timeLayout is the configured clock format, read per call so a change takes
// effect on the next repaint.
func timeLayout() string {
	return clockLayout(config.Current().Interface.ShowSeconds)
}

// clockLayout is that format with seconds decided by the caller instead: a
// body's own timestamp names which face it wants (MessageTimestamp), where
// everything else follows the setting.
func clockLayout(seconds bool) string {
	twelveHour := config.Current().Interface.TimeFormat != config.TimeFormat24

	switch {
	case twelveHour && seconds:
		return "3:04:05 PM"
	case twelveHour:
		return "3:04 PM"
	case seconds:
		return "15:04:05"
	}

	return "15:04"
}

// Timestamp extracts the instant embedded in a ULID.
func Timestamp(id string) (time.Time, error) {
	value, err := ulid.Parse(id)
	if err != nil {
		return time.Time{}, err
	}

	return value.Timestamp(), nil
}

// SameDay reports whether two times fall on the same local calendar day, so a
// pair minutes apart across midnight counts as two days.
func SameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()

	return ay == by && am == bm && ad == bd
}

// DayLabel names a calendar day for the message list's day separator.
func DayLabel(t time.Time) string {
	t, now := t.Local(), time.Now().Local()

	switch {
	case SameDay(t, now):
		return "Today"
	case SameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	}

	return t.Format(dayLayout)
}

// FullDate names a calendar day outright, for the dates a profile carries —
// where "Today" and a relative age tell the reader less than the day itself.
func FullDate(t time.Time) string {
	return t.Local().Format(dayLayout)
}

// ShortTime formats just the local clock time, for the gutter timestamp on
// grouped continuation messages.
func ShortTime(t time.Time) string {
	return t.Local().Format(timeLayout())
}

// NiceTime formats a message time the way a chat client reads it: the clock
// time for today and yesterday, then a coarsening relative age.
func NiceTime(t time.Time) string {
	t, now := t.Local(), time.Now().Local()

	if t.After(now) {
		return "Just now" // clock skew between us and the server
	}

	// Compare calendar days, not elapsed hours: both are rebuilt at midnight UTC
	// so a 23- or 25-hour DST day can't skew the division.
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(nowDate.Sub(tDate) / day)

	switch {
	case days == 0:
		return "Today, " + t.Format(timeLayout())
	case days == 1:
		return "Yesterday, " + t.Format(timeLayout())
	case days < daysInMonth:
		return strconv.Itoa(days) + " days ago, " + t.Format(timeLayout())
	case days < daysInYear:
		return quantity(days/daysInMonth, "month") + " ago"
	}

	return quantity(days/daysInYear, "year") + " ago"
}

// MessageTimestamp renders the instant a <t:seconds:style> in a body names. The
// style is Discord's set, which Revolt carries verbatim, and is honoured rather
// than flattened; an absent style means "f". Everything absolute goes through
// the configured clock so a time in a message and one beside it agree.
func MessageTimestamp(t time.Time, style string) string {
	t = t.Local()

	switch style {
	case "t":
		return t.Format(clockLayout(false))
	case "T":
		return t.Format(clockLayout(true))
	case "d":
		return t.Format(numericLayout)
	case "D":
		return t.Format(dayLayout)
	case "F":
		return t.Format(weekdayLayout + " " + clockLayout(false))
	case "R":
		return RelativeTime(t)
	}

	return t.Format(dayLayout + " " + clockLayout(false))
}

// relativeNow is how near an instant has to be to count as no distance at all.
// Below it the reader is told "just now" rather than a count of seconds, which
// would be stale by the time it is drawn and is never redrawn.
const relativeNow = 45 * time.Second

// RelativeTime names how far off an instant is in the coarsest unit that still
// says something: "5 minutes ago", "in 2 days". Unlike NiceTime it reads
// forwards too — a timestamp in a body is as often a deadline as a record.
func RelativeTime(t time.Time) string {
	distance := time.Until(t)

	ahead := distance > 0
	if !ahead {
		distance = -distance
	}
	if distance < relativeNow {
		return "just now"
	}

	var span string
	switch {
	case distance < time.Hour:
		span = quantity(max(int(distance/time.Minute), 1), "minute")
	case distance < day:
		span = quantity(int(distance/time.Hour), "hour")
	case distance < daysInMonth*day:
		span = quantity(int(distance/day), "day")
	case distance < daysInYear*day:
		span = quantity(int(distance/(daysInMonth*day)), "month")
	default:
		span = quantity(int(distance/(daysInYear*day)), "year")
	}

	if ahead {
		return "in " + span
	}

	return span + " ago"
}

// ShortDuration names a span the way a countdown shows one: "5s", "1m 30s",
// "2h". It rounds *up* to the next whole second — the badge drawn from this must
// not claim the wait is over before it is.
func ShortDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = (d + time.Second - 1).Truncate(time.Second)

	hours, minutes, seconds := int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60

	switch {
	case hours > 0 && minutes > 0:
		return strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
	case hours > 0:
		return strconv.Itoa(hours) + "h"
	case minutes > 0 && seconds > 0:
		return strconv.Itoa(minutes) + "m " + strconv.Itoa(seconds) + "s"
	case minutes > 0:
		return strconv.Itoa(minutes) + "m"
	}

	return strconv.Itoa(seconds) + "s"
}

// quantity renders "1 month" / "3 months".
func quantity(count int, unit string) string {
	if count == 1 {
		return "1 " + unit
	}

	return strconv.Itoa(count) + " " + unit + "s"
}
