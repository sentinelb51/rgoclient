// Package util holds the small, UI-free helpers shared across the client: byte
// sizes and Autumn file IDs, ULID timestamps and the ways a time is worded,
// string truncation, and the two invite-code matchers.
//
// It resolves nothing. Turning an ID into something drawable is domain.Store's
// job, inside internal/client — util sits at the bottom of the graph beside
// domain and markdown, and imports nothing internal but config, which the clock
// format is read from.
package util

import "strings"

// emojiIDLen is the exact length of a custom emoji's ULID. markdown's own
// scanner names the same number for the same reason and does not share this one:
// that package parses bytes out of a body and imports nothing, where this
// answers about a whole string somebody already has.
const emojiIDLen = 26

// IsEmojiID reports whether s is the ULID of a custom emoji rather than a
// literal one. Revolt carries the two in the one field — a reaction's emoji, a
// message's — and says nothing about which, so whoever has to draw it decides
// from the value. Nothing looser than the exact length will do: an emoji is a
// short string of anything at all, and a length range would read a two-letter
// flag as an ID.
func IsEmojiID(s string) bool {
	if len(s) != emojiIDLen {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			continue
		}

		return false
	}

	return true
}

// Truncate shortens s to at most limit runes, replacing the tail with "..."
// when it was cut. Slicing runes keeps multi-byte characters intact.
func Truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit <= 3 {
		return string(r[:limit])
	}

	return string(r[:limit-3]) + "..."
}

// InviteCode extracts the invite code from whatever the user pasted: a bare
// code, a full invite link, or a link with the scheme left off.
//
//	http://stt.gg/dcRHWEF1
//	              └returned┘
//
// The code is the last path segment, so the longer "<host>/invite/<code>" form
// other front-ends hand out works too. Anything not shaped like a code comes
// back as "", catching a typo before it costs a request.
func InviteCode(input string) string {
	code := strings.TrimRight(hostAndPath(input), "/")
	if slash := strings.LastIndexByte(code, '/'); slash != -1 {
		code = code[slash+1:]
	}

	if code == "" || !isInviteCode(code) {
		return ""
	}

	return code
}

// invitePathPrefix is what a host has to put in front of a code to announce one.
// Any host may — a self-hosted instance serves invites off its own domain — and
// being specific is what makes accepting them all safe.
const invitePathPrefix = "invite/"

// inviteShortHosts serve an invite as the whole path, with no /invite/ in front
// of it. Only a host on this list may, since on any other "example.com/about"
// would read as the code "about".
var inviteShortHosts = [...]string{"rvlt.gg", "stt.gg"}

// inviteLinkHost is the one this client *writes*. It is named separately from
// the list above on purpose: InviteLinkCode has to keep reading every host
// Revolt has ever served invites from, because that is what other people's
// messages contain, while a link composed here should say what the platform is
// called now. Adding a host to that list must not silently change this.
const inviteLinkHost = "stt.gg"

// InviteLink is the shareable form of a code the client has just created.
func InviteLink(code string) string {
	if code == "" {
		return ""
	}

	return "https://" + inviteLinkHost + "/" + code
}

// InviteLinkCode extracts the invite code a URL points at, or "" when the URL is
// not an invite link at all.
//
// It is the strict counterpart to InviteCode, and the two are not
// interchangeable. InviteCode serves a field somebody typed into on purpose, so
// it takes the last path segment of whatever it is handed; this one is pointed
// at every link in every message that goes past, where the same rule would unfurl
// an invite card under half the conversation.
func InviteLinkCode(rawURL string) string {
	// Everything before the first slash is the authority. A link that smuggles a
	// known host into it — "rvlt.gg@example.com/x" — simply fails to match one.
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

// MayContainInvite is a cheap negative test over a whole message: false means no
// invite link can be in it, true means one might be. It exists so a caller
// looking at every message that goes past can rule nearly all of them out before
// parsing anything.
//
// It matches the hosts as they are written, where InviteLinkCode folds their
// case. That is the one thing a fast path is allowed to be stricter about: the
// cost of missing "RVLT.GG/x" is a card not drawn for a URL nobody types.
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

// hostAndPath strips a URL down to what either matcher below reads: the scheme
// goes, and so does anything from the first "?" or "#". Both start here, and the
// lenient one differing from the strict one about what counts as a path would be
// the kind of disagreement that only shows up on somebody else's link.
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
	host = strings.ToLower(host)
	for _, known := range inviteShortHosts {
		if host == known {
			return true
		}
	}

	return false
}

// isInviteCode reports whether every rune could be part of an invite code.
// Codes are alphanumeric; "-" and "_" pass so a slightly different format isn't
// rejected locally. Rejecting the rest stops a bare host ("stt.gg") being sent.
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
