package util

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"short untouched", "hello", 10, "hello"},
		{"exact length untouched", "hello", 5, "hello"},
		{"ascii cut", "hello world", 8, "hello..."},
		{"multibyte cut keeps runes intact", "héllo wörld", 8, "héllo..."},
		{"cjk cut", "你好世界你好世界", 6, "你好世..."},
		{"emoji cut", "🙂🙂🙂🙂🙂", 4, "🙂..."},
		{"tiny limit", "hello", 2, "he"},
		{"empty", "", 5, ""},
	}

	for _, tt := range tests {
		if got := Truncate(tt.in, tt.limit); got != tt.want {
			t.Errorf("%s: Truncate(%q, %d) = %q, want %q", tt.name, tt.in, tt.limit, got, tt.want)
		}
	}
}

func TestInviteCode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"dcRHWEF1", "dcRHWEF1"},
		{"  dcRHWEF1\n", "dcRHWEF1"},
		{"stt.gg/dcRHWEF1", "dcRHWEF1"},
		{"http://stt.gg/dcRHWEF1", "dcRHWEF1"},
		{"https://stt.gg/dcRHWEF1/", "dcRHWEF1"},
		{"https://app.revolt.chat/invite/dcRHWEF1", "dcRHWEF1"},
		{"https://stt.gg/dcRHWEF1?ref=x", "dcRHWEF1"},

		// Not invites: a bare host, an empty path, punctuation.
		{"", ""},
		{"stt.gg", ""},
		{"https://stt.gg/", ""},
		{"dcRH WEF1", ""},
		{"dcRHWEF1!", ""},
	}

	for _, c := range cases {
		if got := InviteCode(c.input); got != c.want {
			t.Errorf("InviteCode(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
