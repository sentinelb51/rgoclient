package util

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short untouched", "hello", 10, "hello"},
		{"exact length untouched", "hello", 5, "hello"},
		{"ascii cut", "hello world", 8, "hello..."},
		{"multibyte cut keeps runes intact", "héllo wörld", 8, "héllo..."},
		{"cjk cut", "你好世界你好世界", 6, "你好世..."},
		{"emoji cut", "🙂🙂🙂🙂🙂", 4, "🙂..."},
		{"tiny max", "hello", 2, "he"},
		{"empty", "", 5, ""},
	}
	for _, tt := range tests {
		if got := Truncate(tt.in, tt.max); got != tt.want {
			t.Errorf("%s: Truncate(%q, %d) = %q, want %q", tt.name, tt.in, tt.max, got, tt.want)
		}
	}
}
