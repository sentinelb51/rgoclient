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

// A link this client composes has to be one it can read back: the same scan
// unfurls an invite card under a message, so a host or a shape the writer and
// the reader disagreed about would post links the client itself ignores.
func TestInviteLinkRoundTrips(t *testing.T) {
	for _, code := range []string{"dcRHWEF1", "AbCd1234"} {
		link := InviteLink(code)
		if got := InviteLinkCode(link); got != code {
			t.Errorf("InviteLinkCode(InviteLink(%q)) = %q, want %q (link %q)", code, got, code, link)
		}
	}

	if got := InviteLink(""); got != "" {
		t.Errorf("InviteLink(\"\") = %q, want an empty string", got)
	}
}

// TestIsEmojiID covers the one thing that decides whether an emoji is drawn as a
// picture or as a character. Revolt carries both in the one field, so a rule that
// is even slightly loose turns somebody's flag or shortcode into a request for a
// picture that does not exist — an empty square where the emoji was.
func TestIsEmojiID(t *testing.T) {
	custom := []string{
		"01J9WN3PHX4ZQSNSZH10CK4RHS",
		"01HB2K3M4N5P6Q7R8S9T0V1W2X",
	}
	for _, id := range custom {
		if !IsEmojiID(id) {
			t.Errorf("%q was not read as a custom emoji", id)
		}
	}

	literal := []string{
		"", "\U0001F44D", "❤️", "\U0001F1EC\U0001F1E7",
		"01J9WN3PHX4ZQSNSZH10CK4RH",   // one short
		"01J9WN3PHX4ZQSNSZH10CK4RHSX", // one long
		"01J9WN3PHX4ZQSNSZH10CK4RH-",  // right length, not alphanumeric
	}
	for _, emoji := range literal {
		if IsEmojiID(emoji) {
			t.Errorf("%q was read as a custom emoji ID", emoji)
		}
	}
}
