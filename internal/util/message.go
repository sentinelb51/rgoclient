package util

import (
	"fmt"

	"github.com/sentinelb51/revoltgo"
)

func DisplayName(session *revoltgo.Session, message *revoltgo.Message) string {

	if message.System != nil {
		return "System"
	}

	if message.Webhook != nil {
		return message.Webhook.Name
	}

	if message.Author != "" {
		user := session.State.User(message.Author)
		if user != nil {
			return user.Username
		}
	}

	return "Unknown user"
}

func DisplayAvatarURL(session *revoltgo.Session, message *revoltgo.Message) string {
	if message.System != nil {
		// Maybe return a custom URL in the future?
		return ""
	}

	if message.Webhook != nil {
		return message.Webhook.AvatarURL("256")
	}

	if message.Author != "" {
		user := session.State.User(message.Author)
		if user != nil && user.Avatar != nil {
			return user.Avatar.URL("256")
		}
	}

	return ""
}

// FormatSystemMessage converts system message to readable text.
func FormatSystemMessage(session *revoltgo.Session, message *revoltgo.MessageSystem) string {
	switch message.Type {
	case revoltgo.MessageSystemUserAdded:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s added to group", user.Username)
	case revoltgo.MessageSystemUserRemove:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s removed from group", user.Username)
	case revoltgo.MessageSystemUserJoined:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s joined", user.Username)
	case revoltgo.MessageSystemUserLeft:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s left", user.Username)
	case revoltgo.MessageSystemUserKicked:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s was kicked", user.Username)
	case revoltgo.MessageSystemUserBanned:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s banned", user.Username)
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
	case revoltgo.MessageSystemText:
		return "System message"
	default:
		return "System event"
	}
}
