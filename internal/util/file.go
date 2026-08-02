package util

import (
	"fmt"
	"strings"
)

/* Sizes */

// FormatFileSize renders a byte count in binary units.
func FormatFileSize(bytes int) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)

	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.2f GB", float64(bytes)/gb)
	case bytes >= mb:
		return fmt.Sprintf("%.2f MB", float64(bytes)/mb)
	case bytes >= kb:
		return fmt.Sprintf("%.2f KB", float64(bytes)/kb)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

/* Attachment URLs */

// autumnPathSegments is how many "/"-separated parts precede the file ID in an
// Autumn CDN URL: "https:", "", "<host>", "<bucket>", then the ID.
const autumnPathSegments = 4

// IDFromAttachmentURL extracts the file ID from an Autumn CDN URL, dropping any
// query string. It returns "" for anything not shaped like one.
//
//	https://cdn.stoatusercontent.com/avatars/0d_oHg1EDTnfeBNDMJGa?max_side=256
//	                                         └──── returned ────┘
func IDFromAttachmentURL(url string) string {
	slashes, start := 0, -1
	for i := range len(url) {
		if url[i] != '/' {
			continue
		}
		if slashes++; slashes == autumnPathSegments {
			start = i + 1
			break
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
