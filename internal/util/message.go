package util

import (
	"fmt"
	"image/color"
	"strconv"

	"github.com/sentinelb51/revoltgo"
)

/* Authors */

// Author bundles the display fields for a message's author.
type Author struct {
	Name      string
	AvatarURL string
	Color     color.Color // most-senior coloured role; nil when none applies
}

// MessageAuthor resolves the name, avatar, and role colour for a message's
// author in one pass over State (channel -> member -> user), preferring the
// per-server member (nickname, server avatar, role colour) and falling back to
// the raw user. Color is nil when there is no member, no coloured role, or the
// colour isn't plain hex (gradients and CSS names are not parsed), and callers
// should then use their own default.
func MessageAuthor(session *revoltgo.Session, message *revoltgo.Message) Author {
	switch {
	case session == nil:
		return Author{Name: "Unknown user (nil session)"}
	case message.System != nil:
		return Author{Name: "System"}
	case message.Webhook != nil:
		return Author{Name: message.Webhook.Name, AvatarURL: message.Webhook.AvatarURL("256")}
	}

	if member := messageMember(session, message); member != nil {
		author := Author{
			Name:      MemberName(session, member),
			AvatarURL: MemberAvatarURL(session, member),
		}
		if server := session.State.Server(member.ID.Server); server != nil {
			if c, ok := roleColor(server, member.Roles); ok {
				author.Color = c
			}
		}
		return author
	}

	if message.Author != "" {
		if user := session.State.User(message.Author); user != nil {
			return Author{Name: userDisplayName(user), AvatarURL: user.AvatarURL("256")}
		}
	}

	return Author{Name: "Message author: " + message.Author}
}

// messageMember resolves the server member that authored a message, or nil when
// the message isn't in a server channel, the author isn't a known member, or the
// message has no real author (system / webhook).
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

// roleColor returns the colour of the member's most-senior coloured role (lowest
// Rank, by Revolt's convention), or ok=false when none has a parseable colour.
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

// parseHexColor parses "#RGB" and "#RRGGBB". Anything else yields ok=false.
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

/* Members */

// MemberName returns the best display name for a server member: the per-server
// nickname, then the user's display name, then the username.
func MemberName(session *revoltgo.Session, member *revoltgo.ServerMember) string {
	if member.Nickname != nil && *member.Nickname != "" {
		return *member.Nickname
	}
	if session != nil {
		if user := session.State.User(member.ID.User); user != nil {
			return userDisplayName(user)
		}
	}

	return "Unknown user"
}

// MemberAvatarURL returns a member's per-server avatar, else the user's avatar,
// else "".
func MemberAvatarURL(session *revoltgo.Session, member *revoltgo.ServerMember) string {
	if member.Avatar != nil {
		return member.Avatar.URL("256")
	}
	if session != nil {
		if user := session.State.User(member.ID.User); user != nil {
			return user.AvatarURL("256")
		}
	}

	return ""
}

// MemberOnline reports whether the member's underlying user is online.
func MemberOnline(session *revoltgo.Session, member *revoltgo.ServerMember) bool {
	if session == nil {
		return false
	}
	if user := session.State.User(member.ID.User); user != nil {
		return user.Online
	}

	return false
}

// userDisplayName returns a user's display name, falling back to the username.
func userDisplayName(user *revoltgo.User) string {
	if user.DisplayName != nil && *user.DisplayName != "" {
		return *user.DisplayName
	}

	return user.Username
}

/* System messages */

// FormatSystemMessage renders a system message as human-readable text.
func FormatSystemMessage(session *revoltgo.Session, message *revoltgo.MessageSystem) string {
	if session == nil {
		return "System message"
	}

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
