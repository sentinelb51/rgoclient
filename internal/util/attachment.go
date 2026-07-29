package util

import "github.com/sentinelb51/revoltgo"

// Attachment metadata is optional: the API omits it for files it could not
// introspect, so both accessors below tolerate a nil Metadata rather than making
// every call site nil-check.

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
