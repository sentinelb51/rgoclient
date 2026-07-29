package util

import "testing"

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
