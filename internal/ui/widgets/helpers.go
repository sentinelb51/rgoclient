package widgets

import (
	"github.com/sentinelb51/revoltgo"
)

// GetAvatarInfo returns the ID and URL for a user's avatar, or empty strings if none.
func GetAvatarInfo(user *revoltgo.User) (id, url string) {
	if user == nil || user.Avatar == nil {
		return "", ""
	}
	return user.Avatar.ID, user.Avatar.URL("64")
}

// GetServerIconInfo returns the ID and URL for a server's icon, or empty strings if none.
func GetServerIconInfo(server *revoltgo.Server) (id, url string) {
	if server == nil || server.Icon == nil {
		return "", ""
	}
	return server.Icon.ID, server.Icon.URL("64")
}
