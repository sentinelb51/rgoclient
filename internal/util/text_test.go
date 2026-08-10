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

// TestInviteLinkCode covers the half of the pair that has to say no. It is
// pointed at every link in every message, so the cases that matter are the
// ordinary URLs it must leave alone — InviteCode accepts most of them, which is
// exactly why the two are separate functions.
func TestInviteLinkCode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://rvlt.gg/dcRHWEF1", "dcRHWEF1"},
		{"http://stt.gg/dcRHWEF1", "dcRHWEF1"},
		{"rvlt.gg/dcRHWEF1", "dcRHWEF1"},
		{"https://RVLT.GG/dcRHWEF1", "dcRHWEF1"},
		{"https://rvlt.gg/dcRHWEF1/", "dcRHWEF1"},
		{"https://rvlt.gg/dcRHWEF1?ref=x", "dcRHWEF1"},
		{"https://app.revolt.chat/invite/dcRHWEF1", "dcRHWEF1"},

		// A self-hosted instance serves invites off its own domain, so the
		// /invite/ form is accepted whoever is hosting it.
		{"https://chat.example.org/invite/dcRHWEF1", "dcRHWEF1"},

		// Ordinary links, which the lenient InviteCode would happily read a code
		// out of. None of these may unfurl a card.
		{"https://example.com/dcRHWEF1", ""},
		{"https://github.com/sentinelb51/revoltgo", ""},
		{"https://example.com/blog/2026/some-post", ""},
		{"https://rvlt.gg", ""},
		{"https://rvlt.gg/", ""},
		{"https://rvlt.gg/a/b", ""},
		{"https://rvlt.gg/not a code", ""},

		// The known host smuggled into the authority is not the known host.
		{"https://rvlt.gg@example.com/dcRHWEF1", ""},
		{"", ""},
	}

	for _, c := range cases {
		if got := InviteLinkCode(c.input); got != c.want {
			t.Errorf("InviteLinkCode(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
