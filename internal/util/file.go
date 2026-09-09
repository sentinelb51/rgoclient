package util

import (
	"strconv"
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

	// strconv rather than fmt: a Sprintf boxes its operand into a []any and
	// re-parses the verb, which is most of the cost of a row that draws one.
	switch {
	case bytes >= gb:
		return scaled(float64(bytes)/gb, " GB")
	case bytes >= mb:
		return scaled(float64(bytes)/mb, " MB")
	case bytes >= kb:
		return scaled(float64(bytes)/kb, " KB")
	default:
		return strconv.Itoa(bytes) + " B"
	}
}

// scaled renders a size to two decimals in the given unit.
func scaled(value float64, unit string) string {
	var buf [24]byte

	return string(append(strconv.AppendFloat(buf[:0], value, 'f', 2, 64), unit...))
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

	// An Autumn path ends at the ID, so a remaining "/" means this is some other
	// route — the API's /users/<id>/default_avatar among them. The ID becomes a
	// cache filename, which a slash would send to a directory that isn't there.
	if strings.IndexByte(id, '/') != -1 {
		return ""
	}

	return id
}
