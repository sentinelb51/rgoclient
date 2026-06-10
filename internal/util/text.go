package util

// Truncate shortens s to at most max runes, replacing the tail with "..." when
// it was cut. Slicing runes (not bytes) keeps multi-byte characters intact.
func Truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}
