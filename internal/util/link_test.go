package util

import "testing"

// A link out of somebody else's message reaches the system opener, which runs
// whatever a scheme is registered to — so a scheme that slips through here is a
// program launch that nothing else in the client would notice, and a masked link
// that stops being reported opens the wrong host in silence. Neither failure
// shows up as a crash or a wrong-looking screen, which is why these are asserted
// rather than left to a reader.
func TestSafeLinkRefusesEverythingButThePage(t *testing.T) {
	allowed := []string{
		"https://stoat.chat/x",
		"http://example.test",
		"HTTPS://example.test",
		"mailto:someone@example.test",
	}
	for _, raw := range allowed {
		if _, ok := SafeLink(raw); !ok {
			t.Errorf("SafeLink(%q) refused a link that should open", raw)
		}
	}

	refused := []string{
		"",
		"file:///C:/Windows/System32/cmd.exe",
		"file://attacker.test/share/payload.exe",
		`\attacker.test\share\payload.exe`,
		"//attacker.test/share",
		"javascript:alert(1)",
		"ms-msdt:/id",
		"https://",
		"mailto:",
		"https://example.test\nfile:///etc/passwd",
	}
	for _, raw := range refused {
		if _, ok := SafeLink(raw); ok {
			t.Errorf("SafeLink(%q) would have opened", raw)
		}
	}
}

func TestLinkDeceivesReportsAMaskedHost(t *testing.T) {
	cases := []struct {
		label, destination string
		want               bool
	}{
		{"https://stoat.chat", "https://evil.test/x", true},
		{"stoat.chat/invite", "https://evil.test", true},
		{"https://stoat.chat", "https://www.stoat.chat/x", false},
		{"https://stoat.chat", "https://stoat.chat@evil.test", true},
		{"", "https://stoat.chat@evil.test", true},
		{"the docs", "https://evil.test", false},
		{"", "https://example.test", false},
		{"1.5", "https://example.test", false},
		{"v2.0.1", "https://example.test", false},
		{"stoat.chat", "https://stoat.chat/x", false},
	}

	for _, c := range cases {
		parsed, ok := SafeLink(c.destination)
		if !ok {
			t.Fatalf("SafeLink(%q) refused the destination under test", c.destination)
		}
		if got := LinkDeceives(c.label, parsed); got != c.want {
			t.Errorf("LinkDeceives(%q, %q) = %v, want %v", c.label, c.destination, got, c.want)
		}
	}
}
