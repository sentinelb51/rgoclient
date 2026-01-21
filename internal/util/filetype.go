package util

import (
	"strings"
)

type FileType uint16

const (
	FileTypeUnknown FileType = iota
	FileTypeImage
	FileTypeVideo
	FileTypeText
	FileTypeAudio
	FileTypeArchive
	FileTypePDF
)

func Filetype(filename string) FileType {
	// 1. Find the last dot.
	// strings.LastIndexByte is implemented in Assembly and is faster than filepath.Ext
	idx := strings.LastIndexByte(filename, '.')

	// If no dot or dot is the last character, unknown type
	if idx == -1 || idx == len(filename)-1 {
		return FileTypeUnknown
	}

	// 2. Extract the extension slice
	extSlice := filename[idx+1:]

	// 3. Normalize to lowercase WITHOUT allocation if possible.
	// This is a major optimization. Most files on the web are already lowercase.
	// Substringing in Go shares the backing array, so 'ext' is zero-alloc
	// unless we actually find an uppercase letter.
	ext := extSlice
	for i := 0; i < len(extSlice); i++ {
		c := extSlice[i]
		if c >= 'A' && c <= 'Z' {
			// Only allocate a new string if strictly necessary
			ext = strings.ToLower(extSlice)
			break
		}
	}

	// 4. Match extension.
	switch ext {
	// Images
	case "jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico", "heic", "tiff":
		return FileTypeImage

	// Videos
	case "mp4", "webm", "mov", "mkv", "avi", "flv", "wmv", "m4v":
		return FileTypeVideo

	// Audio
	case "mp3", "wav", "ogg", "flac", "m4a", "aac":
		return FileTypeAudio

	// Archives
	case "zip", "rar", "7z", "tar", "gz", "bz2":
		return FileTypeArchive

	// PDF
	case "pdf":
		return FileTypePDF

	// Text / Code
	case "txt", "md", "csv", "json", "xml", "html", "css", "js", "ts", "go", "py", "java", "c", "cpp", "h", "rs", "log":
		return FileTypeText

	default:
		return FileTypeUnknown
	}
}
