package interfaces

import (
	"github.com/sentinelb51/revoltgo"
)

// MessageActions defines user interactions with messages.
// Unified interface for all message-related operations.
type MessageActions interface {
	// User interactions
	OnAvatarTapped(userID string)
	OnImageTapped(attachment *revoltgo.Attachment)
	OnReply(message *revoltgo.Message)
	OnDelete(messageID string)
	OnEdit(messageID string)

	// Message resolution (cache lookup, not network)
	ResolveMessage(channelID, messageID string) *revoltgo.Message
}
