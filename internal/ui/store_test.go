package ui

import (
	"slices"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
)

// fakeStore is domain.Store backed by maps. It is the whole point of the Store
// seam: a widget can now be given known people, channels and permissions to draw
// without a Revolt session, which nothing written against revoltgo.State can be —
// its caches are unexported.
//
// The zero value answers "nothing known", which is what most widget tests want.
type fakeStore struct {
	self     domain.User
	users    map[string]domain.User
	related  []domain.User
	members  map[string]domain.Member // "serverID:userID"
	roles    map[string][]domain.Role // "serverID:userID"
	channels map[string]domain.Channel
	servers  map[string]domain.Server
	emojis   []domain.Emoji

	serverMembers map[string][]domain.Member // serverID
	hoisted       map[string][]domain.Role   // serverID
	serverRoles   map[string][]domain.Role   // serverID

	participants map[string][]domain.VoiceParticipant // channelID

	channelOverrides map[string]domain.ChannelOverrides // channelID

	permissions       domain.Permission
	serverPermissions domain.Permission
	memberPermissions domain.Permission
}

var _ domain.Store = (*fakeStore)(nil)

func (s *fakeStore) Self() (domain.User, bool) { return s.self, s.self.ID != "" }
func (s *fakeStore) SelfID() string            { return s.self.ID }

func (s *fakeStore) User(userID string) (domain.User, bool) {
	user, ok := s.users[userID]
	return user, ok
}

func (s *fakeStore) UserName(userID string) string { return s.users[userID].Name }

func (s *fakeStore) Relationships() []domain.User { return s.related }

func (s *fakeStore) HasIncomingRequest() bool {
	return slices.ContainsFunc(s.related, func(user domain.User) bool {
		return user.Relationship == domain.RelationshipIncoming
	})
}

func (s *fakeStore) HasUser(userID string) bool {
	_, ok := s.users[userID]
	return ok
}

func (s *fakeStore) Member(serverID, userID string) (domain.Member, bool) {
	member, ok := s.members[serverID+":"+userID]
	return member, ok
}

func (s *fakeStore) HasMember(serverID, userID string) bool {
	_, ok := s.members[serverID+":"+userID]
	return ok
}

func (s *fakeStore) Members(serverID string) []domain.Member {
	return s.serverMembers[serverID]
}

func (s *fakeStore) VoiceParticipants(channelID string) []domain.VoiceParticipant {
	return s.participants[channelID]
}

func (s *fakeStore) MemberRoles(serverID, userID string) []domain.Role {
	return s.roles[serverID+":"+userID]
}

func (s *fakeStore) HoistedRoles(serverID string) []domain.Role { return s.hoisted[serverID] }

func (s *fakeStore) ServerRoles(serverID string) []domain.Role { return s.serverRoles[serverID] }

func (s *fakeStore) Channel(channelID string) (domain.Channel, bool) {
	channel, ok := s.channels[channelID]
	return channel, ok
}

func (s *fakeStore) ChannelName(channelID string) string { return s.channels[channelID].Name }

// EmojiURL derives a URL from the ID the way the real store does. The host is
// invented: nothing in a widget test fetches one.
func (s *fakeStore) EmojiURL(emojiID string) string {
	if emojiID == "" {
		return ""
	}

	return "https://cdn.invalid/emojis/" + emojiID
}

func (s *fakeStore) Emojis() []domain.Emoji { return s.emojis }

// EmojiName answers only for what the fake was given, as the real store answers
// only for the servers the account is in.
func (s *fakeStore) EmojiName(emojiID string) string {
	for _, emoji := range s.emojis {
		if emoji.ID == emojiID {
			return emoji.Name
		}
	}

	return ""
}

func (s *fakeStore) Server(serverID string) (domain.Server, bool) {
	server, ok := s.servers[serverID]
	return server, ok
}

// MessageAuthor answers the way the real store does: the per-server member wins,
// then the raw user, then a placeholder naming the ID.
func (s *fakeStore) MessageAuthor(message *domain.Message) domain.Author {
	switch {
	case message.System != nil:
		return domain.Author{Name: "System"}
	case message.Webhook != nil:
		return domain.Author{Name: message.Webhook.Name, AvatarURL: message.Webhook.AvatarURL, Mark: domain.AuthorWebhook}
	}

	serverID := s.channels[message.ChannelID].ServerID
	if member, ok := s.Member(serverID, message.AuthorID); ok {
		return domain.Author{
			Name: member.Name, AvatarURL: member.AvatarURL, Color: member.Color,
			Mark: fakeAuthorMark(message, member.Bot),
		}
	}
	if user, ok := s.User(message.AuthorID); ok {
		return domain.Author{Name: user.Name, AvatarURL: user.AvatarURL, Mark: fakeAuthorMark(message, user.Bot)}
	}

	return domain.Author{Name: "Message author: " + message.AuthorID, Mark: fakeAuthorMark(message, false)}
}

// fakeAuthorMark mirrors client.authorMark: a mask outranks a bot.
func fakeAuthorMark(message *domain.Message, bot bool) domain.AuthorMark {
	switch {
	case message.Masquerade:
		return domain.AuthorMasquerade
	case bot:
		return domain.AuthorBot
	}

	return domain.AuthorPerson
}

func (s *fakeStore) SystemTextParts(system *domain.SystemMessage) (name, rest string) {
	return system.TextParts(s.UserName(system.Target))
}

func (s *fakeStore) Permissions(string) domain.Permission       { return s.permissions }
func (s *fakeStore) ServerPermissions(string) domain.Permission { return s.serverPermissions }

func (s *fakeStore) MemberServerPermissions(string, string) domain.Permission {
	return s.memberPermissions
}

func (s *fakeStore) ChannelOverrides(channelID string) (domain.ChannelOverrides, bool) {
	overrides, ok := s.channelOverrides[channelID]

	return overrides, ok
}

// testDeps is the bundle a widget test mounts against: a store that knows
// nothing, and the real caches, which are inert without a network. Every field
// is filled in because Deps promises that in the app, and a widget is entitled
// to rely on it.
func testDeps() Deps {
	return Deps{
		Store:   &fakeStore{},
		Images:  cache.NewImageCache("", cache.ImagesFolder, cache.DefaultImageLimits()),
		Emojis:  cache.NewImageCache("", cache.EmojisFolder, cache.DefaultImageLimits()),
		Texts:   cache.NewTextCache(8),
		Actions: stubActions{},
		Tooltip: NewTooltip(),
	}
}
