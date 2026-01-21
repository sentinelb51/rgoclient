package util

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Timestamp parses a ULID to extract its embedded timestamp.
func Timestamp(id string) (time.Time, error) {
	value, err := ulid.Parse(id)

	if err != nil {
		return time.Time{}, err
	}

	return value.Timestamp(), nil
}

const (
	timeLayout  = "3:04 PM"
	daysInMonth = 30
	daysInYear  = 365
)

func NiceTime(t time.Time) string {
	t = t.Local()
	now := time.Now().Local()

	// 1. Handle future dates (e.g., clock skew)
	if t.After(now) {
		return "A few moments ago"
	}

	// 2. Calculate calendar days difference
	// We construct dates in UTC to avoid Daylight Saving Time issues
	// where a day might be 23 or 25 hours long, breaking the /24 math.
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	days := int(nowDate.Sub(tDate).Hours() / 24)

	// 3. Format based on duration
	switch {
	case days == 0:
		return fmt.Sprintf("Today, %s", t.Format(timeLayout))

	case days == 1:
		return fmt.Sprintf("Yesterday, %s", t.Format(timeLayout))

	case days < daysInMonth:
		return fmt.Sprintf("%d days ago, %s", days, t.Format(timeLayout))

	case days < daysInYear:
		months := days / daysInMonth
		return plural(months, "month")

	default:
		years := days / daysInYear
		return plural(years, "year")
	}
}

// plural returns a formatted string with correct pluralisation.
func plural(count int, unit string) string {
	// Singular
	if count == 1 {
		return fmt.Sprintf("%d %s ago", count, unit)
	}

	// Plural
	return fmt.Sprintf("%d %ss ago", count, unit)
}
