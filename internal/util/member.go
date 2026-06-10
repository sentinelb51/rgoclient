package util

import "github.com/sentinelb51/revoltgo"

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
