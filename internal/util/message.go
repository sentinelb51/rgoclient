package util

import (
	"fmt"
	"image/color"
	"strconv"

	"github.com/sentinelb51/revoltgo"
)

// messageMember resolves the server member that authored a message, or nil when
// the message isn't in a server channel, the author isn't a known member, or
// the message has no real author (system / webhook).
func messageMember(session *revoltgo.Session, message *revoltgo.Message) *revoltgo.ServerMember {
	if session == nil || message.Author == "" {
		return nil
	}
	channel := session.State.Channel(message.Channel)
	if channel == nil || channel.Server == nil {
		return nil
	}
	return session.State.Member(*channel.Server, message.Author)
}

// DisplayName returns the name to show for a message's author, preferring the
// per-server member (nickname) and falling back to the raw user.
func DisplayName(session *revoltgo.Session, message *revoltgo.Message) string {
	switch {
	case session == nil:
		return "Unknown user (nil session)"
	case message.System != nil:
		return "System"
	case message.Webhook != nil:
		return message.Webhook.Name
	}

	if member := messageMember(session, message); member != nil {
		return MemberName(session, member)
	}

	if message.Author != "" {
		if user := session.State.User(message.Author); user != nil {
			return userDisplayName(user)
		}
	}
	return "Message author: " + message.Author
}

// DisplayAvatarURL returns the avatar URL for a message's author, preferring the
// per-server member avatar, or "" if none.
func DisplayAvatarURL(session *revoltgo.Session, message *revoltgo.Message) string {
	switch {
	case session == nil, message.System != nil:
		return ""
	case message.Webhook != nil:
		return message.Webhook.AvatarURL("256")
	}

	if member := messageMember(session, message); member != nil {
		return MemberAvatarURL(session, member)
	}

	if message.Author != "" {
		if user := session.State.User(message.Author); user != nil {
			return user.AvatarURL("256")
		}
	}
	return ""
}

// MessageNameColor returns the role color to draw a message author's name in.
// It picks the member's most-senior coloured role; ok is false when there's no
// member, no coloured role, or the colour isn't a plain hex value (gradients and
// CSS-named colours are not parsed), and the caller should use the default.
func MessageNameColor(session *revoltgo.Session, message *revoltgo.Message) (color.Color, bool) {
	member := messageMember(session, message)
	if member == nil {
		return nil, false
	}
	server := session.State.Server(member.ID.Server)
	if server == nil {
		return nil, false
	}
	return roleColor(server, member.Roles)
}

// roleColor returns the colour of the member's most-senior coloured role (lowest
// Rank in Revolt's convention), or ok=false when none has a parseable colour.
func roleColor(server *revoltgo.Server, roleIDs []string) (color.Color, bool) {
	var best *revoltgo.ServerRole
	for _, id := range roleIDs {
		role := server.Roles[id]
		if role == nil || role.Colour == nil || *role.Colour == "" {
			continue
		}
		if best == nil || role.Rank < best.Rank {
			best = role
		}
	}
	if best == nil {
		return nil, false
	}
	return parseHexColor(*best.Colour)
}

// parseHexColor parses "#RGB" and "#RRGGBB" colours. Anything else (gradients,
// CSS-named colours) yields ok=false.
func parseHexColor(s string) (color.Color, bool) {
	if len(s) == 0 || s[0] != '#' {
		return nil, false
	}
	hex := s[1:]

	parse := func(h string) (uint8, bool) {
		v, err := strconv.ParseUint(h, 16, 8)
		return uint8(v), err == nil
	}

	switch len(hex) {
	case 3: // #RGB -> #RRGGBB
		r, okR := parse(hex[0:1] + hex[0:1])
		g, okG := parse(hex[1:2] + hex[1:2])
		b, okB := parse(hex[2:3] + hex[2:3])
		if okR && okG && okB {
			return color.NRGBA{R: r, G: g, B: b, A: 255}, true
		}
	case 6: // #RRGGBB
		r, okR := parse(hex[0:2])
		g, okG := parse(hex[2:4])
		b, okB := parse(hex[4:6])
		if okR && okG && okB {
			return color.NRGBA{R: r, G: g, B: b, A: 255}, true
		}
	}
	return nil, false
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
