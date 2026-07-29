package util

import "strings"

// autumnPathSegments is how many "/"-separated parts precede the file ID in an
// Autumn CDN URL: "https:", "", "<host>", "<bucket>", then the ID.
const autumnPathSegments = 4

// IDFromAttachmentURL extracts the file ID from an Autumn CDN URL, dropping any
// query string. It returns "" for anything not shaped like one.
//
//	https://cdn.stoatusercontent.com/avatars/0d_oHg1EDTnfeBNDMJGa?max_side=256
//	                                         └──── returned ────┘
func IDFromAttachmentURL(url string) string {
	slashes := 0
	start := -1
	for i := 0; i < len(url); i++ {
		if url[i] == '/' {
			slashes++
			if slashes == autumnPathSegments {
				start = i + 1
				break
			}
		}
	}
	if start == -1 {
		return ""
	}

	id := url[start:]
	if query := strings.IndexByte(id, '?'); query != -1 {
		id = id[:query]
	}
	return id
}
