package util

import (
	"fmt"
	"strings"
)

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

// Filetype classifies a filename by its extension. It avoids allocating when
// the extension is already lowercase (the common case for web content).
func Filetype(filename string) FileType {
	dot := strings.LastIndexByte(filename, '.')
	if dot == -1 || dot == len(filename)-1 {
		return FileTypeUnknown
	}

	ext := filename[dot+1:]
	for i := 0; i < len(ext); i++ {
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

// FormatFileSize renders a byte count as a human-readable string (binary units).
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
