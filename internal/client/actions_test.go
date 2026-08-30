package client

import (
	"strings"
	"testing"
)

// zeroWidth is the space Revolt's display-name pattern forbids, escaped because
// the character itself is invisible in a source file.
const zeroWidth = "\u200b"

// validUsername is the client's copy of a pattern the server owns, and the only
// thing standing between a typed name and a refusal with nothing to say why. A
// rule too strict silently refuses a name Revolt would have taken; one too loose
// spends a round trip to learn the same thing.
func TestValidUsername(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		wants bool
	}{
		{"plain", "sentinel", true},
		{"digits", "user2024", true},
		{"the three marks", "a_b.c-d", true},
		{"letters outside ASCII", "セン", true},
		{"at the minimum", "ab", true},
		{"under the minimum", "a", false},
		{"at the maximum", strings.Repeat("a", MaxUsername), true},
		{"over the maximum", strings.Repeat("a", MaxUsername+1), false},
		{"counted by rune, not by byte", strings.Repeat("é", MaxUsername), true},
		{"empty", "", false},
		{"a space", "sen tinel", false},
		{"the handle's separator", "sentinel#0001", false},
		{"a mention", "@sentinel", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validUsername(test.in); got != test.wants {
				t.Errorf("validUsername(%q) = %v; want %v", test.in, got, test.wants)
			}
		})
	}
}

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
