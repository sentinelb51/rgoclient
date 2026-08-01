// Package util holds the small, UI-free helpers shared across the client:
// author and member resolution, file classification, timestamps, and string
// tidying. Everything here takes an explicit *revoltgo.Session; nothing reaches
// for global state.
package util

import "strings"

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
	code := strings.TrimSpace(input)
	if scheme := strings.Index(code, "://"); scheme != -1 {
		code = code[scheme+3:]
	}
	if query := strings.IndexAny(code, "?#"); query != -1 {
		code = code[:query]
	}

	code = strings.TrimRight(code, "/")
	if slash := strings.LastIndexByte(code, '/'); slash != -1 {
		code = code[slash+1:]
	}

	if code == "" || !isInviteCode(code) {
		return ""
	}

	return code
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
