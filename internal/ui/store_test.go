package ui

import (
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
	members  map[string]domain.Member // "serverID:userID"
	roles    map[string][]domain.Role // "serverID:userID"
	channels map[string]domain.Channel
	servers  map[string]domain.Server

	manageMessages bool
	kickMembers    bool
	bypassSlowmode bool
}

var _ domain.Store = (*fakeStore)(nil)

func (s *fakeStore) Self() (domain.User, bool) { return s.self, s.self.ID != "" }
func (s *fakeStore) SelfID() string            { return s.self.ID }

func (s *fakeStore) User(userID string) (domain.User, bool) {
	user, ok := s.users[userID]
	return user, ok
}

func (s *fakeStore) UserName(userID string) string { return s.users[userID].Name }

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

func (s *fakeStore) Members(string) []domain.Member { return nil }

func (s *fakeStore) MemberRoles(serverID, userID string) []domain.Role {
	return s.roles[serverID+":"+userID]
}

func (s *fakeStore) Channel(channelID string) (domain.Channel, bool) {
	channel, ok := s.channels[channelID]
	return channel, ok
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
		return domain.Author{Name: message.Webhook.Name, AvatarURL: message.Webhook.AvatarURL}
	}

	serverID := s.channels[message.ChannelID].ServerID
	if member, ok := s.Member(serverID, message.AuthorID); ok {
		return domain.Author{Name: member.Name, AvatarURL: member.AvatarURL, Color: member.Color}
	}
	if user, ok := s.User(message.AuthorID); ok {
		return domain.Author{Name: user.Name, AvatarURL: user.AvatarURL}
	}

	return domain.Author{Name: "Message author: " + message.AuthorID}
}

func (s *fakeStore) SystemText(system *domain.SystemMessage) string {
	return system.Text(s.UserName(system.Target))
}

func (s *fakeStore) CanManageMessages(string) bool { return s.manageMessages }
func (s *fakeStore) CanKickMembers(string) bool    { return s.kickMembers }
func (s *fakeStore) CanBypassSlowmode(string) bool { return s.bypassSlowmode }

// testDeps is the bundle a widget test mounts against: a store that knows
// nothing, and the real caches, which are inert without a network. Every field
// is filled in because Deps promises that in the app, and a widget is entitled
// to rely on it.
func testDeps() Deps {
	return Deps{
		Store:   &fakeStore{},
		Images:  cache.NewImageCache("", cache.DefaultImageLimits()),
		Texts:   cache.NewTextCache(8),
		Actions: stubActions{},
	}
}
