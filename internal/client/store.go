package client

// The domain.Store implementation: every read the UI makes of what the client
// already knows, answered out of revoltgo.State and converted on the way out.
//
// Being logged out is a valid state, not an error — every method then reports
// nothing known, so no caller has to check first. That is also what keeps
// ui.Deps.Store always set: the store outlives any one session.

import (
	"cmp"
	"image/color"
	"slices"
	"strings"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/config"
	"RGOClient/internal/domain"
)

// store answers reads out of the client's current session. It holds no state of
// its own — the session is read back through the client on every call — so a
// re-login is one pointer swap with nothing here to invalidate.
type store struct {
	client *Client
}

var _ domain.Store = (*store)(nil)

// state returns the session's local cache, or nil when there is no session.
func (s *store) state() *revoltgo.State {
	if session := s.client.session.Load(); session != nil {
		return session.State
	}

	return nil
}

/* Users */

// Self returns the logged-in account.
func (s *store) Self() (domain.User, bool) {
	state := s.state()
	if state == nil {
		return domain.User{}, false
	}

	return toUser(state.Self())
}

// SelfID is the logged-in account's ID, or "" when logged out.
func (s *store) SelfID() string {
	state := s.state()
	if state == nil {
		return ""
	}
	if self := state.Self(); self != nil {
		return self.ID
	}

	return ""
}

// HasUser reports whether a user is already resolved, without resolving them.
func (s *store) HasUser(userID string) bool {
	state := s.state()

	return state != nil && userID != "" && state.User(userID) != nil
}

// HasMember reports whether a membership is already resolved, without resolving
// it.
func (s *store) HasMember(serverID, userID string) bool {
	state := s.state()

	return state != nil && serverID != "" && userID != "" && state.Member(serverID, userID) != nil
}

// User resolves a user ID against what State already holds.
func (s *store) User(userID string) (domain.User, bool) {
	state := s.state()
	if state == nil || userID == "" {
		return domain.User{}, false
	}

	return toUser(state.User(userID))
}

func toUser(user *revoltgo.User) (domain.User, bool) {
	if user == nil {
		return domain.User{}, false
	}

	out := domain.User{
		ID:        user.ID,
		Name:      displayName(user),
		Username:  user.Username,
		Handle:    handle(user),
		AvatarURL: user.AvatarURL(avatarSize),
		Presence:  toPresence(user),
		Badges:    toBadges(user),
		Online:    user.Online,
		Bot:       user.Bot != nil,
	}
	if user.Status != nil {
		out.StatusText = user.Status.Text
	}

	return out, true
}

// displayName is a user's chosen name, falling back to the username.
func displayName(user *revoltgo.User) string {
	if user.DisplayName != nil && *user.DisplayName != "" {
		return *user.DisplayName
	}

	return user.Username
}

// handle is the account's unique "@username#0001", which is what tells two
// people sharing a display name apart. The discriminator is left off when the
// account carries none.
func handle(user *revoltgo.User) string {
	if user.Username == "" {
		return ""
	}
	if user.Discriminator == "" {
		return "@" + user.Username
	}

	return "@" + user.Username + "#" + user.Discriminator
}

// UserName resolves a user ID to the name to show for them, or "" when State has
// never heard of them. It reads through to the raw user rather than going via
// User, which would build a handle and a badge list nobody asked for.
func (s *store) UserName(userID string) string {
	state := s.state()
	if state == nil || userID == "" {
		return ""
	}
	if user := state.User(userID); user != nil {
		return displayName(user)
	}

	return ""
}

/* Members */

// Member resolves one member of a server.
func (s *store) Member(serverID, userID string) (domain.Member, bool) {
	state := s.state()
	if state == nil || serverID == "" || userID == "" {
		return domain.Member{}, false
	}

	member := state.Member(serverID, userID)
	if member == nil {
		return domain.Member{}, false
	}

	return toMember(state, member, state.Server(serverID)), true
}

// Members lists everyone the client knows of in a server, ordered by display
// name. Sorting here rather than at the call sites is what lets the member
// sidebar and the mention picker share one walk: they are the same people under
// the same names in the same order.
//
// The ordering can be turned off in the settings, which is the point of the
// setting: this runs on every member event, and on a large server the walk that
// lowers a name per member and then sorts them is the most expensive thing the
// client does in response to somebody coming online. Unordered, the list is
// whatever order State holds — stable enough to read, and free.
func (s *store) Members(serverID string) []domain.Member {
	state := s.state()
	if state == nil || serverID == "" {
		return nil
	}

	raw := state.Members(serverID)
	server := state.Server(serverID)

	if !config.Current().Behaviour.SortMembers {
		members := make([]domain.Member, len(raw))
		for i, member := range raw {
			members[i] = toMember(state, member, server)
		}

		return members
	}

	// The sort key is resolved alongside the member rather than lowered inside the
	// comparator, which would redo that work O(n log n) times on a large server.
	type entry struct {
		member domain.Member
		key    string
	}
	entries := make([]entry, len(raw))
	for i, member := range raw {
		resolved := toMember(state, member, server)
		entries[i] = entry{member: resolved, key: strings.ToLower(resolved.Name)}
	}
	slices.SortFunc(entries, func(x, y entry) int { return strings.Compare(x.key, y.key) })

	members := make([]domain.Member, len(entries))
	for i := range entries {
		members[i] = entries[i].member
	}

	return members
}

// toMember resolves the display fields of a membership. server may be nil — a
// member of a server State has not published to us still has a nickname and an
// avatar, just no role colour.
func toMember(state *revoltgo.State, member *revoltgo.ServerMember, server *revoltgo.Server) domain.Member {
	out := domain.Member{
		ServerID: member.ID.Server,
		UserID:   member.ID.User,
		JoinedAt: member.JoinedAt,
	}

	if member.Nickname != nil {
		out.Name = *member.Nickname
	}
	if member.Avatar != nil {
		out.AvatarURL = member.Avatar.URL(avatarSize)
	}

	// The account behind the membership fills in whatever the nickname and the
	// per-server avatar did not override.
	if user := state.User(member.ID.User); user != nil {
		out.Username = user.Username
		out.Online = user.Online
		if out.Name == "" {
			out.Name = displayName(user)
		}
		if out.AvatarURL == "" {
			out.AvatarURL = user.AvatarURL(avatarSize)
		}
	}
	if out.Name == "" {
		out.Name = "Unknown user"
	}

	if c, ok := roleColor(server, member.Roles); ok {
		out.Color = c
	}

	return out
}

// MemberRoles resolves a member's roles, most senior first — Revolt ranks the
// most senior lowest — skipping any the server has not published to us.
func (s *store) MemberRoles(serverID, userID string) []domain.Role {
	state := s.state()
	if state == nil {
		return nil
	}

	member := state.Member(serverID, userID)
	if member == nil {
		return nil
	}

	return serverRoles(state.Server(serverID), member.Roles)
}

// serverRoles resolves role IDs against the server that defines them, in the
// order MemberRoles promises, skipping any it does not.
func serverRoles(server *revoltgo.Server, roleIDs []string) []domain.Role {
	if server == nil {
		return nil
	}

	// The ID is the map key rather than a field on the role, so it is paired with
	// its definition here to survive the sort.
	type known struct {
		id   string
		role *revoltgo.ServerRole
	}

	found := make([]known, 0, len(roleIDs))
	for _, id := range roleIDs {
		if role := server.Roles[id]; role != nil {
			found = append(found, known{id: id, role: role})
		}
	}
	slices.SortFunc(found, func(x, y known) int { return cmp.Compare(x.role.Rank, y.role.Rank) })

	roles := make([]domain.Role, len(found))
	for i, k := range found {
		roles[i] = domain.Role{ID: k.id, Name: k.role.Name}
		if k.role.Colour != nil {
			if c, ok := parseColor(*k.role.Colour); ok {
				roles[i].Color = c
			}
		}
	}

	return roles
}

// roleColor returns the colour of the most-senior coloured role among roleIDs
// (lowest Rank, by Revolt's convention), or ok=false when none has a colour that
// parses.
func roleColor(server *revoltgo.Server, roleIDs []string) (color.Color, bool) {
	if server == nil {
		return nil, false
	}

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

	return parseColor(*best.Colour)
}

/* Channels and servers */

// Channel resolves a channel, titling it the way its kind demands: a direct
// message is named and pictured after the other participant, saved notes after
// what they are rather than after the account reading them.
func (s *store) Channel(channelID string) (domain.Channel, bool) {
	state := s.state()
	if state == nil || channelID == "" {
		return domain.Channel{}, false
	}

	channel := state.Channel(channelID)
	if channel == nil {
		return domain.Channel{}, false
	}

	out := domain.Channel{
		ID:         channel.ID,
		Kind:       toChannelKind(channel.ChannelType),
		Name:       channel.Name,
		Slowmode:   s.client.slowmodeOf(channel.ID),
		Recipients: channel.Recipients,
		Active:     channel.Active,
	}
	if channel.Server != nil {
		out.ServerID = *channel.Server
	}
	if channel.LastMessageID != nil {
		out.LastMessageID = *channel.LastMessageID
	}

	switch out.Kind {
	case domain.ChannelSavedMessages:
		out.Name = "Saved Notes"
		if self := state.Self(); self != nil {
			out.AvatarURL = self.AvatarURL(avatarSize)
		}
	case domain.ChannelDM:
		out.Name = "Direct Message"
		if user := state.User(recipientID(state, channel)); user != nil {
			out.Name = displayName(user)
			out.AvatarURL = user.AvatarURL(avatarSize)
		}
	case domain.ChannelGroup:
		if channel.Icon != nil {
			out.AvatarURL = channel.Icon.URL(avatarSize)
		}
	}

	if out.Name == "" {
		out.Name = "Unnamed channel"
	}

	return out, true
}

// recipientID returns the other participant of a direct message, or "" when it
// holds nobody but the current user — a DM with yourself lists only you.
func recipientID(state *revoltgo.State, channel *revoltgo.Channel) string {
	var selfID string
	if self := state.Self(); self != nil {
		selfID = self.ID
	}
	for _, id := range channel.Recipients {
		if id != selfID {
			return id
		}
	}

	return ""
}

// ChannelName resolves a channel ID to the name a rendered <#id> shows, or ""
// when State has never heard of it. It reads through to the raw channel rather
// than going via Channel, which would resolve a picture, a slowmode and a DM's
// title from its recipients — none of which a mention draws.
func (s *store) ChannelName(channelID string) string {
	state := s.state()
	if state == nil || channelID == "" {
		return ""
	}
	if channel := state.Channel(channelID); channel != nil {
		return channel.Name
	}

	return ""
}

// Server resolves a server.
func (s *store) Server(serverID string) (domain.Server, bool) {
	state := s.state()
	if state == nil || serverID == "" {
		return domain.Server{}, false
	}

	server := state.Server(serverID)
	if server == nil {
		return domain.Server{}, false
	}

	return toServer(server), true
}

// channelServerID returns the server a channel belongs to, or "" for a
// conversation. It reads State directly rather than going through Channel, which
// would resolve a name and an avatar nobody asked for.
func (s *store) channelServerID(channelID string) string {
	state := s.state()
	if state == nil {
		return ""
	}
	if channel := state.Channel(channelID); channel != nil && channel.Server != nil {
		return *channel.Server
	}

	return ""
}

/* Messages */

// MessageAuthor resolves the name, avatar and role colour for a message's author
// in one pass over State (channel to member to user), preferring the per-server
// member — nickname, server avatar, role colour — and falling back to the raw
// user.
func (s *store) MessageAuthor(message *domain.Message) domain.Author {
	switch {
	case s.state() == nil:
		return domain.Author{Name: "Unknown user (no session)"}
	case message.System != nil:
		return domain.Author{Name: "System"}
	case message.Webhook != nil:
		return domain.Author{Name: message.Webhook.Name, AvatarURL: message.Webhook.AvatarURL}
	}

	if member, ok := s.Member(s.channelServerID(message.ChannelID), message.AuthorID); ok {
		return domain.Author{Name: member.Name, AvatarURL: member.AvatarURL, Color: member.Color}
	}

	if user, ok := s.User(message.AuthorID); ok {
		return domain.Author{Name: user.Name, AvatarURL: user.AvatarURL}
	}

	return domain.Author{Name: "Message author: " + message.AuthorID}
}

// SystemTextParts renders a system message, resolving whoever it is about.
func (s *store) SystemTextParts(system *domain.SystemMessage) (name, rest string) {
	return system.TextParts(s.UserName(system.Target))
}

/* Permissions */

// permissionBypassSlowmode is Revolt's BypassSlowmode channel permission.
// revoltgo's constants stop at MentionRoles, so the bit is named here rather
// than imported. It falls inside PermissionGrantAllSafe, which is what a server
// owner is granted, so ownership already carries it.
const permissionBypassSlowmode int64 = 1 << 39

// CanManageMessages reports whether the account may delete other people's
// messages in a channel.
func (s *store) CanManageMessages(channelID string) bool {
	return s.channelPermissions(channelID)&revoltgo.PermissionManageMessages != 0
}

// CanBypassSlowmode reports whether the account may send in a channel without
// waiting out its cooldown.
func (s *store) CanBypassSlowmode(channelID string) bool {
	return s.channelPermissions(channelID)&permissionBypassSlowmode != 0
}

// channelPermissions is the account's permission bitfield in a channel, or 0
// when there is no session, no such channel, or the calculation fails — all of
// which mean the same thing to a caller: assume nothing is allowed.
func (s *store) channelPermissions(channelID string) int64 {
	state := s.state()
	if state == nil {
		return 0
	}

	self, channel := state.Self(), state.Channel(channelID)
	if self == nil || channel == nil {
		return 0
	}

	permissions, err := state.ChannelPermissions(self, channel)
	if err != nil {
		return 0
	}

	return permissions
}

// CanKickMembers reports whether the account may remove members from a server.
func (s *store) CanKickMembers(serverID string) bool {
	state := s.state()
	if state == nil {
		return false
	}

	self, server := state.Self(), state.Server(serverID)
	if self == nil || server == nil {
		return false
	}

	permissions, err := state.ServerPermissions(self, server)

	return err == nil && permissions&revoltgo.PermissionKickMembers != 0
}
