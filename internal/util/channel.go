package util

import "github.com/sentinelb51/revoltgo"

// ChannelName returns the best display name for a channel. Server channels and
// groups carry their own name, but a direct message has none — it is titled
// after the other participant, resolved from State — and the saved-messages
// channel every account has gets a fixed title rather than the user's own name.
func ChannelName(session *revoltgo.Session, channel *revoltgo.Channel) string {
	if channel == nil {
		return ""
	}

	switch channel.ChannelType {
	case revoltgo.ChannelTypeSavedMessages:
		return "Saved Notes"
	case revoltgo.ChannelTypeDM:
		if session != nil {
			if user := session.State.User(DMRecipientID(session, channel)); user != nil {
				return userDisplayName(user)
			}
		}
		return "Direct Message"
	}

	if channel.Name != "" {
		return channel.Name
	}
	return "Unnamed channel"
}

// DMRecipientID returns the other participant of a direct message channel, or
// "" when the channel isn't a DM or holds nobody but the current user (a DM
// with yourself lists only you). Groups have many recipients and no single
// "other", so they are not handled here.
func DMRecipientID(session *revoltgo.Session, channel *revoltgo.Channel) string {
	if session == nil || channel == nil || channel.ChannelType != revoltgo.ChannelTypeDM {
		return ""
	}

	var selfID string
	if self := session.State.Self(); self != nil {
		selfID = self.ID
	}
	for _, id := range channel.Recipients {
		if id != selfID {
			return id
		}
	}
	return ""
}
