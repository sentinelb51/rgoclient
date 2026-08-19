// Package util holds the UI-free helpers shared across the client: byte sizes
// and Autumn file IDs, ULID timestamps and time wording, truncation, and the two
// invite-code matchers. It resolves nothing — that is domain.Store's job — and
// imports nothing internal but config, which the clock format is read from.
package util

import (
	"strings"
	"unicode/utf8"
)

// emojiIDLen is the exact length of a custom emoji's ULID.
const emojiIDLen = 26

// IsEmojiID reports whether s is a custom emoji's ULID rather than a literal
// emoji. Revolt carries both in one field and says nothing about which, so the
// value decides. Only the exact length will do — a range would read a two-letter
// flag as an ID.
func IsEmojiID(s string) bool {
	if len(s) != emojiIDLen {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			continue
		}

		return false
	}

	return true
}

// Truncate shortens s to at most limit runes, replacing the tail with "..."
// when it was cut.
func Truncate(s string, limit int) string {
	// The common case is no truncation; counting avoids the []rune allocation.
	if utf8.RuneCountInString(s) <= limit {
		return s
	}

	r := []rune(s)
	if limit <= 3 {
		return string(r[:limit])
	}

	return string(r[:limit-3]) + "..."
}

// invitePathPrefix is what a host has to put in front of a code to announce one.
// Any host may — a self-hosted instance serves invites off its own domain — and
// being specific is what makes accepting them all safe.
const invitePathPrefix = "invite/"

// inviteShortHosts serve an invite as the whole path, with no /invite/ in front.
// Only a host on this list may: on any other, "example.com/about" would read as
// the code "about".
var inviteShortHosts = [...]string{"rvlt.gg", "stt.gg"}

// inviteLinkHost is the one this client *writes*, named apart from the list
// above because that list must keep reading every host Revolt has ever served
// invites from — that is what other people's messages contain.
const inviteLinkHost = "stt.gg"

// InviteLink is the shareable form of a code the client has just created.
func InviteLink(code string) string {
	if code == "" {
		return ""
	}

	return "https://" + inviteLinkHost + "/" + code
}

// InviteCode extracts the invite code from whatever the user pasted: a bare
// code, a full link, or a link with the scheme left off. The code is the last
// path segment, so other front-ends' "<host>/invite/<code>" works too.
//
//	http://stt.gg/dcRHWEF1
//	              └returned┘
func InviteCode(input string) string {
	code := strings.TrimRight(hostAndPath(input), "/")
	if _, after, ok := strings.CutLast(code, "/"); ok {
		code = after
	}

	if code == "" || !isInviteCode(code) {
		return ""
	}

	return code
}

// InviteLinkCode extracts the code a URL points at, or "" when it is not an
// invite link. Strict where InviteCode is lenient: that one serves a field
// somebody typed into on purpose, this one is pointed at every link in every
// message that goes past.
func InviteLinkCode(rawURL string) string {
	// Everything before the first slash is the authority, so a link smuggling a
	// known host into it — "rvlt.gg@example.com/x" — fails to match one.
	host, path, _ := strings.Cut(hostAndPath(rawURL), "/")
	code := strings.Trim(path, "/")

	if after, found := strings.CutPrefix(code, invitePathPrefix); found {
		code = after
	} else if !isInviteShortHost(host) {
		return ""
	}

	if code == "" || strings.ContainsRune(code, '/') || !isInviteCode(code) {
		return ""
	}

	return code
}

// MayContainInvite is a cheap negative test over a whole message, so a caller
// watching every message can rule nearly all of them out before parsing.
//
// It matches hosts as written where InviteLinkCode folds case — the cost of
// missing "RVLT.GG/x" is a card not drawn for a URL nobody types.
func MayContainInvite(text string) bool {
	if strings.Contains(text, invitePathPrefix) {
		return true
	}

	for _, host := range inviteShortHosts {
		if strings.Contains(text, host) {
			return true
		}
	}

	return false
}

// hostAndPath strips a URL to what both matchers read: no scheme, nothing from
// the first "?" or "#". They start here so the lenient one and the strict one
// cannot disagree about what counts as a path.
func hostAndPath(raw string) string {
	rest := strings.TrimSpace(raw)
	if scheme := strings.Index(rest, "://"); scheme != -1 {
		rest = rest[scheme+3:]
	}
	if query := strings.IndexAny(rest, "?#"); query != -1 {
		rest = rest[:query]
	}

	return rest
}

// isInviteShortHost reports whether a host serves invites off its bare path.
func isInviteShortHost(host string) bool {
	for _, known := range inviteShortHosts {
		if strings.EqualFold(host, known) {
			return true
		}
	}

	return false
}

// isInviteCode reports whether every rune could be part of a code. "-" and "_"
// pass so a slightly different format isn't rejected locally; rejecting the rest
// stops a bare host ("stt.gg") being sent.
func isInviteCode(code string) bool {
	for _, r := range code {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '-', r == '_':
		default:
			return false
		}
	}

	return true
}
