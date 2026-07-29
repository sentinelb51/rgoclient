package util

import "strings"

// InviteCode extracts the invite code from whatever the user pasted: a bare
// code, a full invite link, or a link with the scheme left off.
//
//	http://stt.gg/dcRHWEF1
//	              └returned┘
//
// The code is the last path segment, so the longer "<host>/invite/<code>" form
// other Revolt front-ends hand out works too. Anything not shaped like a code
// comes back as "", so a typo is caught before it costs a request.
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
// Revolt's codes are alphanumeric; "-" and "_" are allowed through so a
// slightly different code format doesn't get rejected locally. Rejecting the
// rest is what stops a bare host ("stt.gg") from being sent as a code.
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
