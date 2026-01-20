package util

import "github.com/sentinelb51/revoltgo"

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
