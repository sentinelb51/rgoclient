package util

import (
	"net/url"
	"strings"
)

/* Links */

// linkSchemes is every scheme a link out of somebody else's message may be
// opened with. What is behind fyne.App.OpenURL is ShellExecute on Windows
// ("rundll32 url.dll,FileProtocolHandler") and xdg-open elsewhere, and both hand
// the string to whatever is registered for its scheme — so "file:", a UNC path
// spelled as one, or any of the shell protocols is a program launch that a
// message anybody can send is one click away from. Only the three a chat message
// has a use for are let through; everything else is shown as the text it is.
var linkSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
}

// SafeLink parses a link that arrived in content this client did not write and
// reports whether it may be handed to the system opener. A link the client built
// itself — a settings folder, the release page — does not come through here:
// those are not somebody else's strings.
//
// Scheme-relative ("//host/path") and relative links are refused rather than
// resolved: there is no base to resolve them against in a message, and a bare
// "\host\share" is exactly the shape that would be launched.
func SafeLink(raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, false
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if !linkSchemes[strings.ToLower(parsed.Scheme)] {
		return nil, false
	}

	// mailto carries its address in Opaque; the other two are nothing without a
	// host, and a hostless "https:///x" would be read as a local path by some
	// openers.
	if parsed.Scheme == "mailto" {
		return parsed, parsed.Opaque != "" || parsed.Path != ""
	}

	return parsed, parsed.Host != ""
}

// LinkHost is the host a link opens, lowercased and without the "www." every
// site answers to either way. It is what a person is asked to recognise, so it
// is what a warning names.
func LinkHost(u *url.URL) string {
	if u == nil {
		return ""
	}

	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// LinkDeceives reports whether a link's visible text claims a destination other
// than the one it opens — the masked link "[https://revolt.chat](https://evil.example)",
// which reads as the site it names and goes somewhere else.
//
// Only a label that is itself a link is judged: prose ("the docs") claims
// nothing and warning about it would train the warning away. Userinfo is
// deceptive on its own — "https://revolt.chat@evil.example" *is* its own label
// and still opens evil.example — so it is reported whatever the label says.
func LinkDeceives(label string, destination *url.URL) bool {
	if destination == nil {
		return true
	}
	if destination.User != nil {
		return true
	}

	claimed, ok := labelHost(label)
	if !ok {
		return false
	}

	return claimed != LinkHost(destination)
}

// labelHost reads the host a link's visible text claims, reporting false for
// text that claims none. A bare "example.com/path" counts: it is what a reader
// takes for a destination, whether or not it was written as a URL.
func labelHost(label string) (string, bool) {
	label = strings.TrimSpace(label)
	if label == "" || strings.ContainsAny(label, " \t\n") {
		return "", false
	}

	if !strings.Contains(label, "://") {
		label = "https://" + label
	}

	parsed, err := url.Parse(label)
	if err != nil {
		return "", false
	}

	host := LinkHost(parsed)

	// A host is a dotted name ending in letters. The last part is what rules out
	// prose that happens to parse: "1.5" and "3.14" are a version and a number,
	// and warning about the link under one would train the warning away.
	dot := strings.LastIndexByte(host, '.')
	if dot <= 0 || !isAlphaRun(host[dot+1:]) {
		return "", false
	}

	return host, true
}

// isAlphaRun reports whether s is two or more letters, which is every top-level
// domain there is.
func isAlphaRun(s string) bool {
	if len(s) < 2 {
		return false
	}

	for i := range len(s) {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}

	return true
}
