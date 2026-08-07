package domain

// Store is the read side of a Revolt session: everything the client needs to
// turn an ID into something it can draw, answered from what is already known and
// never from the network. A miss reports ok=false rather than blocking — the
// caller decides whether to show a placeholder, queue a fetch, or both.
//
// internal/client implements it over revoltgo.State; a test implements it with a
// struct of maps. That is the whole reason it exists: State's caches are
// unexported, so code written against a *revoltgo.Session cannot be given known
// contents to answer from.
//
// Everything here returns resolved domain values. A Member already carries the
// nickname, per-server avatar and role colour the sidebar shows; a Channel
// already carries the name a direct message is titled under. Handing back the
// wire types instead would only move the resolution to the call sites and put
// revoltgo back inside internal/ui.
type Store interface {
	// Self is the logged-in account. SelfID is the same question without the
	// resolution, for the many places that only want to know whether something
	// belongs to the user.
	Self() (User, bool)
	SelfID() string

	User(userID string) (User, bool)

	// UserName is the name to show for a user, or "" when they are unknown. It is
	// the whole of what a rendered mention needs, and unlike User it allocates
	// nothing to answer.
	UserName(userID string) string

	Member(serverID, userID string) (Member, bool)
	Channel(channelID string) (Channel, bool)
	Server(serverID string) (Server, bool)

	// ChannelName is UserName's counterpart for a rendered <#id>: the name to show
	// for a channel, or "" when it is unknown. Like UserName it allocates nothing,
	// where Channel would resolve a picture and a slowmode nobody asked for.
	ChannelName(channelID string) string

	// HasUser and HasMember answer whether a record is already resolved, without
	// resolving it. Lazy author resolution asks once per mounted message, so this
	// pair is on the render hot path and must not allocate.
	HasUser(userID string) bool
	HasMember(serverID, userID string) bool

	// Members lists everyone the client knows of in a server. That is the gateway's
	// members plus whoever lazy author resolution has pulled in, not the full
	// membership: Revolt's members endpoint has no pagination, so asking for every
	// member of a large server would flood memory.
	Members(serverID string) []Member

	// MemberRoles resolves one member's roles, most senior first. Kept off Member
	// because only a profile draws them and building a sidebar would otherwise
	// allocate a slice per row.
	MemberRoles(serverID, userID string) []Role

	// MessageAuthor resolves a message's author in one pass — channel to member to
	// user — preferring the per-server member and falling back to the raw user.
	MessageAuthor(message *Message) Author

	// SystemTextParts renders a system message, resolving whoever it is about and
	// handing the name back apart from the sentence around it — the client draws
	// that name as a mention, so it cannot arrive already folded into prose.
	SystemTextParts(system *SystemMessage) (name, rest string)

	// CanManageMessages reports whether the account may delete other people's
	// messages in a channel.
	CanManageMessages(channelID string) bool

	// CanKickMembers reports whether the account may remove members from a server.
	CanKickMembers(serverID string) bool

	// CanBypassSlowmode reports whether the account may send in a channel without
	// waiting out its cooldown. A channel's Slowmode is what it is configured at;
	// this is whether it applies to us.
	CanBypassSlowmode(channelID string) bool
}
