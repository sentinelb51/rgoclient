// Package ui contains the reusable widgets, layouts, and theme glue for the
// client. Widgets receive their dependencies explicitly through Deps rather than
// reaching for global state.
package ui

import (
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
)

// Deps bundles everything a widget needs from the rest of the app: the active
// session for resolving users, the image cache for avatars and attachments, and
// the action callbacks for user interactions.
type Deps struct {
	Session *revoltgo.Session
	Images  *cache.ImageCache
	Actions MessageActions
}

// MessageActions handles user interactions originating from message widgets. It
// is implemented by the application controller.
type MessageActions interface {
	OnAvatarTapped(userID string)
	OnImageTapped(attachment *revoltgo.Attachment)
	OnReply(message *revoltgo.Message)
	OnDelete(message *revoltgo.Message)
	OnEdit(message *revoltgo.Message)

	// ResolveMessage looks a message up in the local cache (no network).
	ResolveMessage(channelID, messageID string) *revoltgo.Message
}
