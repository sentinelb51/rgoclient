package util

import (
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
