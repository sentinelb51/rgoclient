package util

import (
	"fmt"
	"strings"

	"github.com/sentinelb51/revoltgo"
)

/* Classification */

// FileType categorises a file by its extension.
type FileType uint8

const (
	FileTypeUnknown FileType = iota
	FileTypeImage
	FileTypeVideo
	FileTypeText
	FileTypeAudio
	FileTypeArchive
	FileTypePDF
)

// Filetype classifies a filename by its extension, avoiding an allocation when
// the extension is already lowercase (the common case for web content).
func Filetype(filename string) FileType {
	dot := strings.LastIndexByte(filename, '.')
	if dot == -1 || dot == len(filename)-1 {
		return FileTypeUnknown
	}

	ext := filename[dot+1:]
	for i := range len(ext) {
		if c := ext[i]; c >= 'A' && c <= 'Z' {
			ext = strings.ToLower(ext)
			break
		}
	}

	switch ext {
	case "jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico", "heic", "tiff":
		return FileTypeImage
	case "mp4", "webm", "mov", "mkv", "avi", "flv", "wmv", "m4v":
		return FileTypeVideo
	case "mp3", "wav", "ogg", "flac", "m4a", "aac":
		return FileTypeAudio
	case "zip", "rar", "7z", "tar", "gz", "bz2":
		return FileTypeArchive
	case "pdf":
		return FileTypePDF
	case "txt", "md", "csv", "json", "xml", "html", "css", "js", "ts", "go", "py", "java", "c", "cpp", "h", "rs", "log":
		return FileTypeText
	default:
		return FileTypeUnknown
	}
}

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

/* Attachments */

// Attachment metadata is optional — the API omits it for files it could not
// introspect — so both accessors below tolerate a nil Metadata rather than
// making every call site nil-check.

// IsImageAttachment reports whether an attachment is an image.
func IsImageAttachment(file *revoltgo.File) bool {
	return file.Metadata != nil && file.Metadata.Type == revoltgo.FileMetadataTypeImage
}

// AttachmentDimensions returns an attachment's pixel dimensions, or zeroes when
// it carries no metadata.
func AttachmentDimensions(file *revoltgo.File) (width, height int) {
	if file.Metadata == nil {
		return 0, 0
	}

	return file.Metadata.Width, file.Metadata.Height
}

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
