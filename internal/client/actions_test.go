package client

import (
	"strings"
	"testing"
)

// zeroWidth is the space Revolt's display-name pattern forbids, spelled out
// because it is invisible in a source file.
const zeroWidth = "​"

// cleanDisplayName decides three things at once — what is dropped, what is cut
// and what cannot be sent — and only the last of them reaches the user as a
// notice. A silent slip in either of the first two is a name they did not choose.
func TestCleanDisplayName(t *testing.T) {
	long := strings.Repeat("a", MaxDisplayName+4)
	wide := strings.Repeat("語", MaxDisplayName+4)

	tests := []struct {
		name  string
		in    string
		want  string
		wants bool
	}{
		{"plain", "Sentinel", "Sentinel", true},
		{"trimmed", "  Sentinel  ", "Sentinel", true},
		{"empty clears", "", "", true},
		{"blank clears", "   ", "", true},
		{"newline dropped", "Sen\ntinel", "Sentinel", true},
		{"carriage return dropped", "Sen\r\ntinel", "Sentinel", true},
		{"zero width dropped", "Sen" + zeroWidth + "tinel", "Sentinel", true},
		{"zero width alone clears", zeroWidth, "", true},
		{"cut to the limit", long, strings.Repeat("a", MaxDisplayName), true},
		{"cut by rune, not by byte", wide, strings.Repeat("語", MaxDisplayName), true},
		{"one rune refused", "a", "a", false},
		{"at the minimum", "ab", "ab", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := cleanDisplayName(test.in)
			if got != test.want || ok != test.wants {
				t.Errorf("cleanDisplayName(%q) = %q, %v; want %q, %v", test.in, got, ok, test.want, test.wants)
			}
		})
	}
}
