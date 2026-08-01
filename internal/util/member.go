package util

import (
	"image/color"

	"github.com/sentinelb51/revoltgo"
)

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

// UserName resolves a user ID to the name to show for them, without going to
// the network: the display name, else the username. A user State has never
// heard of yields "" so callers can decide what to show in their place.
func UserName(session *revoltgo.Session, userID string) string {
	if session == nil || userID == "" {
		return ""
	}
	if user := session.State.User(userID); user != nil {
		return userDisplayName(user)
	}
	return ""
}

// MemberColor returns the colour of a member's most-senior coloured role, or nil
// when the server is unknown, no role is coloured, or the colour isn't a plain
// hex value. Same rule MessageAuthor applies to a message's author.
func MemberColor(session *revoltgo.Session, member *revoltgo.ServerMember) color.Color {
	if session == nil {
		return nil
	}
	server := session.State.Server(member.ID.Server)
	if server == nil {
		return nil
	}
	if c, ok := roleColor(server, member.Roles); ok {
		return c
	}
	return nil
}

// userDisplayName returns a user's display name, falling back to the username.
func userDisplayName(user *revoltgo.User) string {
	if user.DisplayName != nil && *user.DisplayName != "" {
		return *user.DisplayName
	}
	return user.Username
}

// MemberAvatarURL returns the avatar URL for a member: the per-server avatar if
// set, otherwise the user's avatar. Returns "" if neither resolves.
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
