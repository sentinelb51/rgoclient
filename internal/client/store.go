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
	"time"

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

/* Ordering */

// keyed pairs a resolved value with what it sorts by. The fold is taken once per
// element rather than inside the comparator, which a sort would ask O(log n)
// times per element and allocate on every one.
type keyed[T any] struct {
	value T
	name  string // case-folded
	id    string
}

// sortKey is where one entry lands, apart from the value it belongs to.
type sortKey struct {
	name string // case-folded
	id   string
	at   int32 // its entry's index
}

// sortedByName orders entries by their folded name, tie-broken on ID so the
// order is total: the UI buckets these slices without re-sorting them, and two
// equal names swapping places between rebuilds would move a row out from under
// the pointer about to tap it.
//
// The keys are sorted and the values permuted once, rather than the entries
// themselves being sorted. A resolved member is over 200 bytes and every swap
// would move one; a key is 40 and the comparator reads nothing else. Over 20,000
// members that is 9.4ms against 6.2ms, for one more slice.
func sortedByName[T any](entries []keyed[T]) []T {
	keys := make([]sortKey, len(entries))
	for i := range entries {
		keys[i] = sortKey{name: entries[i].name, id: entries[i].id, at: int32(i)}
	}

	slices.SortFunc(keys, func(x, y sortKey) int {
		if by := strings.Compare(x.name, y.name); by != 0 {
			return by
		}

		return strings.Compare(x.id, y.id)
	})

	values := make([]T, len(entries))
	for i, key := range keys {
		values[i] = entries[key.at].value
	}

	return values
}

/* Users */

// Self returns the logged-in account.
func (s *store) Self() (domain.User, bool) {
	state := s.state()
	if state == nil {
		return domain.User{}, false
	}

	return s.toUser(state.Self())
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

	return s.toUser(state.User(userID))
}

func (s *store) toUser(user *revoltgo.User) (domain.User, bool) {
	if user == nil {
		return domain.User{}, false
	}

	return resolveUser(user, s.client.relationshipWith(user)), true
}

// resolveUser is toUser with the relationship already in hand, for the walk that
// has to ask about it before deciding whether the account is worth resolving at
// all — see Relationships.
func resolveUser(user *revoltgo.User, relationship domain.Relationship) domain.User {
	out := domain.User{
		ID:           user.ID,
		Name:         displayName(user),
		Username:     user.Username,
		Handle:       handle(user),
		AvatarURL:    user.AvatarURL(avatarSize),
		Presence:     toPresence(user),
		Badges:       toBadges(user),
		Relationship: relationship,
		Online:       user.Online,
		Bot:          user.Bot != nil,
	}
	if user.DisplayName != nil {
		out.DisplayName = *user.DisplayName
	}
	if user.Status != nil {
		out.StatusText = user.Status.Text
	}

	return out
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

// Relationships walks the cached accounts because there is no list to read:
// Ready names everybody this account is related to and files the relation on
// each of them, so the collection only exists as a property of the people in it.
//
// Somebody the gateway has since named that State has never cached is missed —
// EventUserRelationship carries the account but nothing files it, which is what
// Client.relations records and App.friendsChanged resolves.
func (s *store) Relationships() []domain.User {
	state := s.state()
	if state == nil {
		return nil
	}

	selfID := s.SelfID()

	// The relationship is asked *before* the account is resolved: this walks every
	// cached user, most of whom are members of a server and nothing more, and
	// resolving one builds a handle, an avatar URL and a badge list. The answer is
	// then carried into the resolution rather than asked for again — each ask takes
	// the client's lock, and on a large server this loop runs thousands of times.
	var related []keyed[domain.User]
	for _, raw := range state.Users() {
		if raw == nil || raw.ID == selfID {
			continue
		}

		relationship := s.client.relationshipWith(raw)
		if !relationship.Known() {
			continue
		}

		user := resolveUser(raw, relationship)
		related = append(related, keyed[domain.User]{user, strings.ToLower(user.Name), user.ID})
	}

	return sortedByName(related)
}

// HasIncomingRequest reports whether any cached account is waiting on an answer.
//
// Its own walk rather than a filter over Relationships: that resolves a handle,
// an avatar and a badge list per related account and then sorts them, where what
// is being asked is one boolean — and it is asked on the UI thread every time a
// batch of resolved authors lands. The walk itself cannot be avoided, a
// relationship being a property of the accounts rather than a list, so the
// relations the gateway announced are taken once instead of per account.
func (s *store) HasIncomingRequest() bool {
	state := s.state()
	if state == nil {
		return false
	}

	known := s.client.knownRelations()

	// UserSeq holds State's lock across the body, so the body reads nothing but
	// the account and a map already in hand.
	for user := range state.UserSeq() {
		if user == nil {
			continue
		}

		relationship, ok := known[user.ID]
		if !ok {
			relationship = toRelationship(user.Relationship)
		}
		if relationship == domain.RelationshipIncoming {
			return true
		}
	}

	return false
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

	entries := make([]keyed[domain.Member], len(raw))
	for i, member := range raw {
		resolved := toMember(state, member, server)
		entries[i] = keyed[domain.Member]{resolved, strings.ToLower(resolved.Name), resolved.UserID}
	}

	return sortedByName(entries)
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
		out.Name, out.Nickname = *member.Nickname, *member.Nickname
	}
	if member.Avatar != nil {
		out.AvatarURL = member.Avatar.URL(avatarSize)
	}
	if member.Timeout != nil {
		out.Timeout = *member.Timeout
	}

	// The account behind the membership fills in whatever the nickname and the
	// per-server avatar did not override. A membership with no account cached
	// stays PresenceOffline, which is the right default and one more reason the
	// bulk fetch — which brings the accounts with it — matters.
	if user := state.User(member.ID.User); user != nil {
		out.Username = user.Username
		out.Presence = toPresence(user)
		out.Bot = user.Bot != nil
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

	out.HasRoles = len(member.Roles) > 0
	out.Color, out.HoistRoleID = memberRoleInfo(server, member.Roles)

	return out
}

// VoiceParticipants is everybody connected to a voice channel's call, ordered by
// display name.
//
// Resolved through the same membership toMember reads, so a participant is drawn
// with the nickname, per-server avatar and role colour the member sidebar would
// give them, and falls back to the account alone where there is no membership —
// a call in a group conversation, or somebody the server has not published to us.
//
// A snapshot rather than VoiceStatesSeq: resolving a member takes State's own
// locks, and the sequence holds the voice lock across the loop body.
func (s *store) VoiceParticipants(channelID string) []domain.VoiceParticipant {
	state := s.state()
	if state == nil || channelID == "" {
		return nil
	}

	states := state.VoiceStates(channelID)
	if len(states) == 0 {
		return nil
	}

	var serverID string
	if channel := state.Channel(channelID); channel != nil && channel.Server != nil {
		serverID = *channel.Server
	}
	server := state.Server(serverID)

	entries := make([]keyed[domain.VoiceParticipant], 0, len(states))
	for _, voice := range states {
		participant := domain.VoiceParticipant{
			UserID:        voice.ID,
			Camera:        voice.Camera,
			Screensharing: voice.Screensharing,
		}

		if member := state.Member(serverID, voice.ID); member != nil {
			resolved := toMember(state, member, server)
			participant.Name = resolved.Name
			participant.AvatarURL = resolved.AvatarURL
			participant.Color = resolved.Color
			participant.Bot = resolved.Bot
		} else if user := state.User(voice.ID); user != nil {
			participant.Name = displayName(user)
			participant.AvatarURL = user.AvatarURL(avatarSize)
			participant.Bot = user.Bot != nil
		}
		if participant.Name == "" {
			participant.Name = "Unknown user"
		}

		entries = append(entries, keyed[domain.VoiceParticipant]{
			participant, strings.ToLower(participant.Name), participant.UserID,
		})
	}

	return sortedByName(entries)
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
		roles[i] = toRole(k.id, k.role)
	}

	return roles
}

// HoistedRoles lists the roles a server displays as sections of their own, most
// senior first — which is ascending Rank, Revolt ranking the most senior lowest.
//
// It hands back values: nothing may escape holding a *revoltgo.ServerRole, which
// the gateway rewrites in place.
func (s *store) HoistedRoles(serverID string) []domain.Role {
	state := s.state()
	if state == nil || serverID == "" {
		return nil
	}

	server := state.Server(serverID)
	if server == nil {
		return nil
	}

	roles := make([]domain.Role, 0, len(server.Roles))
	for id, role := range server.Roles {
		if role != nil && role.Hoist {
			roles = append(roles, toRole(id, role))
		}
	}
	sortRoles(roles)

	return roles
}

// ServerRoles is every role a server defines, most senior first. The role editor
// asks for all of them where the sidebar asks only for the hoisted ones, and both
// read what Ready already brought.
func (s *store) ServerRoles(serverID string) []domain.Role {
	state := s.state()
	if state == nil || serverID == "" {
		return nil
	}

	server := state.Server(serverID)
	if server == nil {
		return nil
	}

	roles := make([]domain.Role, 0, len(server.Roles))
	for id, role := range server.Roles {
		if role != nil {
			roles = append(roles, toRole(id, role))
		}
	}
	sortRoles(roles)

	return roles
}

// sortRoles orders roles most senior first, which is ascending Rank.
func sortRoles(roles []domain.Role) {
	slices.SortFunc(roles, func(x, y domain.Role) int {
		if by := cmp.Compare(x.Rank, y.Rank); by != 0 {
			return by
		}

		// Ranks are not guaranteed distinct and map iteration is not ordered, so
		// two roles sharing one would otherwise swap places between rebuilds.
		return strings.Compare(x.ID, y.ID)
	})
}

// toRole converts one role definition. revoltgo leaves ServerRole.ID empty — the
// map key is the ID — so it is passed in.
func toRole(id string, role *revoltgo.ServerRole) domain.Role {
	out := domain.Role{
		ID:    id,
		Name:  role.Name,
		Allow: domain.Permission(role.Permissions.Allow),
		Deny:  domain.Permission(role.Permissions.Deny),
		Rank:  role.Rank,
		Hoist: role.Hoist,
	}
	if role.Colour != nil {
		out.ColorText = *role.Colour
		if c, ok := parseColor(*role.Colour); ok {
			out.Color = c
		}
	}

	return out
}

// memberRoleInfo answers both of the questions a member row asks of its roles in
// one walk: what colour to draw the name in, and which section to file the row
// under. The most senior role wins each — lowest Rank, by Revolt's convention —
// and they are answered together because this runs per member on every rebuild
// of a list that can hold thousands.
//
// The two are independent: the most senior *coloured* role need not be the most
// senior *hoisted* one.
func memberRoleInfo(server *revoltgo.Server, roleIDs []string) (color.Color, string) {
	if server == nil {
		return nil, ""
	}

	var coloured, hoisted *revoltgo.ServerRole
	var hoistID string

	for _, id := range roleIDs {
		role := server.Roles[id]
		if role == nil {
			continue
		}

		if role.Colour != nil && *role.Colour != "" && (coloured == nil || role.Rank < coloured.Rank) {
			coloured = role
		}
		if role.Hoist && (hoisted == nil || role.Rank < hoisted.Rank) {
			hoisted, hoistID = role, id
		}
	}

	if coloured == nil {
		return nil, hoistID
	}

	fill, _ := parseColor(*coloured.Colour)

	return fill, hoistID
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
		Kind:       toChannelKind(channel),
		Name:       channel.Name,
		Recipients: channel.Recipients,
		Active:     channel.Active,
		NSFW:       channel.NSFW,
	}
	if channel.Description != nil {
		out.Description = *channel.Description
	}
	if channel.Slowmode != nil {
		out.Slowmode = time.Duration(*channel.Slowmode) * time.Second
	}
	if channel.Voice != nil && channel.Voice.MaxUsers != nil {
		out.UserLimit = *channel.Voice.MaxUsers
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

/* Emojis */

// EmojiURL is where a custom emoji's picture is served from, built from the ID
// and asking State nothing: State holds only the emoji of servers the account is
// in, while a message routinely names one from a server it is not. A lookup
// would blank out exactly the emoji nobody could otherwise see. It is the one
// read here that answers while logged out.
func (s *store) EmojiURL(emojiID string) string {
	if emojiID == "" {
		return ""
	}

	return revoltgo.EndpointAutumnFile(revoltgo.FileTagEmojis, emojiID, emojiSize)
}

// EmojiName is the shortcode an emoji is written as. Unlike the URL above this
// is a lookup and can only answer for the servers the account is in: a name is
// held nowhere else, and there is no route that asks for one emoji.
func (s *store) EmojiName(emojiID string) string {
	state := s.state()
	if state == nil || emojiID == "" {
		return ""
	}

	emoji := state.Emoji(emojiID)
	if emoji == nil {
		return ""
	}

	return emoji.Name
}

// Emojis is every custom emoji the account may use, ordered by name.
//
// No request backs this and none is wanted: Ready carries every server's emoji
// and revoltgo files the create and delete events into State itself, so what
// State holds is already the whole set and already current. EmojiSeq iterates
// under State's read lock, so the loop stays a copy per entry; the sort is
// outside it.
func (s *store) Emojis() []domain.Emoji {
	state := s.state()
	if state == nil {
		return nil
	}

	// A map has no order of its own, and a picker whose entries moved between
	// openings could not be learned.
	all := make([]keyed[domain.Emoji], 0, state.EmojiCount())
	for raw := range state.EmojiSeq() {
		emoji := domain.Emoji{ID: raw.ID, Name: raw.Name}
		if raw.Parent != nil {
			emoji.ServerID = raw.Parent.ID
		}

		all = append(all, keyed[domain.Emoji]{emoji, strings.ToLower(raw.Name), raw.ID})
	}

	return sortedByName(all)
}

/* Messages */

// MessageAuthor resolves the name, avatar and role colour for a message's author
// in one pass over State (channel to member to user), preferring the per-server
// member — nickname, server avatar, role colour — and falling back to the raw
// user.
func (s *store) MessageAuthor(message *domain.Message) domain.Author {
	state := s.state()

	switch {
	case state == nil:
		return domain.Author{Name: "Unknown user (no session)"}
	case message.System != nil:
		return domain.Author{Name: "System"}
	case message.Webhook != nil:
		return domain.Author{Name: message.Webhook.Name, AvatarURL: message.Webhook.AvatarURL, Mark: domain.AuthorWebhook}
	}

	if member, ok := s.Member(s.channelServerID(message.ChannelID), message.AuthorID); ok {
		return domain.Author{
			Name: member.Name, AvatarURL: member.AvatarURL, Color: member.Color,
			Mark: authorMark(message, member.Bot),
		}
	}

	// Read through to the raw account rather than going via User, which would build
	// a handle, a badge list and a relationship no author line draws — the same
	// reason UserName does. This is the path every message in a conversation takes,
	// a DM having no membership to prefer.
	if message.AuthorID != "" {
		if user := state.User(message.AuthorID); user != nil {
			return domain.Author{
				Name: displayName(user), AvatarURL: user.AvatarURL(avatarSize),
				Mark: authorMark(message, user.Bot != nil),
			}
		}
	}

	return domain.Author{Name: "Message author: " + message.AuthorID, Mark: authorMark(message, false)}
}

// authorMark is the precedence domain.AuthorMark describes, for a message whose
// author is an account: a mask outranks the bot almost always wearing it — a
// bridge is a bot posting as somebody, and what the reader is being told is that
// the name is the bot's rather than the person's.
//
// An account nothing is known about yet is not a bot — that arrives with the
// fetch. The mask does not: it is on the message, so an unresolved author still
// wears it.
func authorMark(message *domain.Message, bot bool) domain.AuthorMark {
	switch {
	case message.Masquerade:
		return domain.AuthorMasquerade
	case bot:
		return domain.AuthorBot
	}

	return domain.AuthorPerson
}

// SystemTextParts renders a system message, resolving whoever it is about.
func (s *store) SystemTextParts(system *domain.SystemMessage) (name, rest string) {
	return system.TextParts(s.UserName(system.Target))
}

/* Permissions */

// The presets Revolt grants without asking any role: everything short of the
// unsafe bits for an owner, the conversation grant for a DM or group, and what a
// member serving a timeout is left with.
const (
	permissionGrantAll       = domain.Permission(revoltgo.PermissionGrantAllSafe)
	permissionInConversation = domain.Permission(revoltgo.PermissionPresetDM)
	permissionViewOnly       = domain.Permission(revoltgo.PermissionPresetViewOnly)
	permissionInTimeout      = domain.Permission(revoltgo.PermissionPresetTimeout)
)

// Permissions is everything the account may do in a channel, or none at all when
// there is no session or the channel is unknown — which a caller reads as
// "assume nothing is allowed".
//
// The whole calculation is done here rather than through revoltgo's
// State.ChannelPermissions, which agrees with the backend now but pays for its DM
// branch by walking every cached membership and group, and answers nothing at all
// for a membership State has not caught up with — see internal/client/CLAUDE.md.
//
// The State lookups happen here and the arithmetic in the resolvers below, which
// take plain values: State's caches are unexported, so nothing reaching through
// a session could be given a known server to answer about.
func (s *store) Permissions(channelID string) domain.Permission {
	state := s.state()
	if state == nil {
		return 0
	}

	self, channel := state.Self(), state.Channel(channelID)
	if self == nil || channel == nil {
		return 0
	}

	// A voice channel is a TextChannel carrying a voice object, so this covers both.
	if channel.ChannelType != revoltgo.ChannelTypeText {
		other := state.User(recipientID(state, channel))

		return conversationPermissions(channel, s.client.relationshipWith(other), self.ID)
	}

	if channel.Server == nil {
		return 0
	}
	server := state.Server(*channel.Server)
	if server == nil {
		return 0
	}

	return channelPermissions(server, state.Member(server.ID, self.ID), channel, self.ID)
}

// conversationPermissions resolves what the account may do in a channel of its
// own: saved notes, a direct message, a group. other is how the account stands
// with the DM's opposite number — nobody else's relationship comes into it.
func conversationPermissions(channel *revoltgo.Channel, other domain.Relationship, userID string) domain.Permission {
	switch channel.ChannelType {
	case revoltgo.ChannelTypeSavedMessages:
		return permissionGrantAll
	case revoltgo.ChannelTypeGroup:
		if channel.Owner == userID {
			return permissionGrantAll
		}
		// A group's own permissions are an allow-only overwrite over the view-only
		// floor: whoever set them cannot take away seeing the group at all.
		if channel.Permissions != nil {
			return permissionViewOnly | domain.Permission(*channel.Permissions)
		}

		return permissionInConversation
	case revoltgo.ChannelTypeDM:
		// Revolt decides a DM from the *relationship*: a direct message carries no
		// permissions field at all. Blocked either way leaves the history readable
		// and nothing else; anyone else you have a conversation with, you may write to.
		if other.Blocked() {
			return permissionViewOnly
		}

		return permissionInConversation
	}

	return 0
}

// ServerPermissions is everything the account may do across a server, before any
// one channel's overwrites narrow it.
func (s *store) ServerPermissions(serverID string) domain.Permission {
	state := s.state()
	if state == nil {
		return 0
	}

	self, server := state.Self(), state.Server(serverID)
	if self == nil || server == nil {
		return 0
	}

	return serverPermissions(server, state.Member(serverID, self.ID), self.ID)
}

// MemberServerPermissions is ServerPermissions asked about somebody else — what a
// moderation action has to know about the person it is aimed at, Revolt refusing
// one against somebody who holds the same permission.
func (s *store) MemberServerPermissions(serverID, userID string) domain.Permission {
	state := s.state()
	if state == nil || serverID == "" || userID == "" {
		return 0
	}

	server := state.Server(serverID)
	if server == nil {
		return 0
	}

	return serverPermissions(server, state.Member(serverID, userID), userID)
}

// channelPermissions resolves what a member may do in one of a server's
// channels, in Revolt's own order: the server's grant, the channel's default
// overwrite, the member's roles, then the channel's overwrites for those same
// roles — most senior last in both passes, so it has the last word. The channel's
// default comes *before* the roles, which is what lets a role be handed back a
// channel denied to everyone. The timeout clamp comes after all of it, so no
// overwrite can return what a timeout took.
func channelPermissions(server *revoltgo.Server, member *revoltgo.ServerMember, channel *revoltgo.Channel, userID string) domain.Permission {
	if server.Owner == userID {
		return permissionGrantAll
	}

	// One ranking of the member's roles serves both passes: the server's grant, and
	// this channel's overwrites keyed by the same roles in the same order.
	roles := rankRoles(server, member)
	permissions := domain.Permission(server.DefaultPermissions)

	if channel.DefaultPermissions != nil {
		permissions = applyOverwrite(permissions, *channel.DefaultPermissions)
	}
	for _, role := range roles {
		permissions = applyOverwrite(permissions, role.overwrite)
	}
	for _, role := range roles {
		if overwrite, ok := channel.RolePermissions[role.id]; ok {
			permissions = applyOverwrite(permissions, overwrite)
		}
	}

	permissions = clampTimeout(permissions, member)

	// Losing sight of the channel loses everything with it.
	if !permissions.Has(domain.PermissionViewChannel) {
		return 0
	}

	return permissions
}

// serverPermissions resolves what a member may do across a server, which is
// channelPermissions without any one channel narrowing it.
func serverPermissions(server *revoltgo.Server, member *revoltgo.ServerMember, userID string) domain.Permission {
	if server.Owner == userID {
		return permissionGrantAll
	}

	return clampTimeout(grantedBy(server, rankRoles(server, member)), member)
}

// rankedRole is one of a member's roles paired with the ID it is filed under.
// revoltgo leaves ServerRole.ID empty — the map key is the ID — and a channel's
// overwrites are keyed by it, so the pairing has to survive the sort.
type rankedRole struct {
	id        string
	rank      int64
	overwrite revoltgo.PermissionOverwrite
}

// rankRoles resolves a member's roles *least* senior first — descending Rank,
// Revolt ranking the most senior lowest — so the most senior applies last and
// has the last word.
//
// A nil member resolves as one holding no roles rather than as no access: that
// is what Revolt computes for someone carrying only the default role, and what
// revoltgo fabricates on ServerCreate. Answering "no access" would empty the
// channel sidebar of a server the account had just joined.
func rankRoles(server *revoltgo.Server, member *revoltgo.ServerMember) []rankedRole {
	if member == nil {
		return nil
	}

	roles := make([]rankedRole, 0, len(member.Roles))
	for _, id := range member.Roles {
		if role := server.Roles[id]; role != nil {
			roles = append(roles, rankedRole{id: id, rank: role.Rank, overwrite: role.Permissions})
		}
	}
	slices.SortFunc(roles, func(x, y rankedRole) int { return cmp.Compare(y.rank, x.rank) })

	return roles
}

// grantedBy applies a member's roles to a server's default grant, in the order
// rankRoles put them in.
func grantedBy(server *revoltgo.Server, roles []rankedRole) domain.Permission {
	permissions := domain.Permission(server.DefaultPermissions)
	for _, role := range roles {
		permissions = applyOverwrite(permissions, role.overwrite)
	}

	return permissions
}

// applyOverwrite grants and then revokes, which is Revolt's own order: a role
// that both allows and denies one permission denies it.
func applyOverwrite(permissions domain.Permission, overwrite revoltgo.PermissionOverwrite) domain.Permission {
	return (permissions | domain.Permission(overwrite.Allow)) &^ domain.Permission(overwrite.Deny)
}

// clampTimeout cuts a member serving a timeout back to what the timeout leaves
// them.
func clampTimeout(permissions domain.Permission, member *revoltgo.ServerMember) domain.Permission {
	if member == nil || member.Timeout == nil || !time.Now().Before(*member.Timeout) {
		return permissions
	}

	return permissions & permissionInTimeout
}
