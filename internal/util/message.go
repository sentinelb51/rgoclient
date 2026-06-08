package util

import (
	"fmt"

	"github.com/sentinelb51/revoltgo"
)

// DisplayName returns the name to show for a message's author.
func DisplayName(session *revoltgo.Session, message *revoltgo.Message) string {
	switch {
	case session == nil:
		return "Unknown user"
	case message.System != nil:
		return "System"
	case message.Webhook != nil:
		return message.Webhook.Name
	}

	if message.Author != "" {
		if user := session.State.User(message.Author); user != nil {
			return user.Username
		}
	}
	return "Unknown user"
}

// DisplayAvatarURL returns the avatar URL for a message's author, or "" if none.
func DisplayAvatarURL(session *revoltgo.Session, message *revoltgo.Message) string {
	switch {
	case session == nil, message.System != nil:
		return ""
	case message.Webhook != nil:
		return message.Webhook.AvatarURL("256")
	}

	if message.Author != "" {
		if user := session.State.User(message.Author); user != nil {
			return user.AvatarURL("256")
		}
	}
	return ""
}

// FormatSystemMessage renders a system message as human-readable text.
func FormatSystemMessage(session *revoltgo.Session, message *revoltgo.MessageSystem) string {
	if session == nil {
		return "System message"
	}

	// username resolves the user referenced by the system event.
	username := func() string {
		if user := session.State.User(message.ID); user != nil {
			return user.Username
		}
		return "Someone"
	}

	switch message.Type {
	case revoltgo.MessageSystemUserAdded:
		return fmt.Sprintf("%s added to group", username())
	case revoltgo.MessageSystemUserRemove:
		return fmt.Sprintf("%s removed from group", username())
	case revoltgo.MessageSystemUserJoined:
		return fmt.Sprintf("%s joined", username())
	case revoltgo.MessageSystemUserLeft:
		return fmt.Sprintf("%s left", username())
	case revoltgo.MessageSystemUserKicked:
		return fmt.Sprintf("%s was kicked", username())
	case revoltgo.MessageSystemUserBanned:
		return fmt.Sprintf("%s banned", username())
	case revoltgo.MessageSystemChannelRenamed:
		return "Channel renamed"
	case revoltgo.MessageSystemChannelDescriptionChanged:
		return "Channel description changed"
	case revoltgo.MessageSystemChannelIconChanged:
		return "Channel icon changed"
	case revoltgo.MessageSystemChannelOwnershipChanged:
		return "Channel ownership changed"
	case revoltgo.MessageSystemMessagePinned:
		return "Message pinned"
	case revoltgo.MessageSystemMessageUnpinned:
		return "Message unpinned"
	case revoltgo.MessageSystemCallStarted:
		return "Call started"
	default:
		return "System event"
	}
}
