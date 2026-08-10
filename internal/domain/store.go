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

	// EmojiURL is where a custom emoji's picture is served from, or "" for an
	// empty ID. It is *derived* from the ID rather than looked up, which is why it
	// has no ok: a message can carry an emoji from a server the account is not in,
	// so no local record covers it — and the CDN serves the picture regardless.
	EmojiURL(emojiID string) string

	// HasUser and HasMember answer whether a record is already resolved, without
	// resolving it. Lazy author resolution asks once per mounted message, so this
	// pair is on the render hot path and must not allocate.
	HasUser(userID string) bool
	HasMember(serverID, userID string) bool

	// Members lists everyone the client knows of in a server, ordered by display
	// name unless the settings say otherwise. That is whoever has been fetched —
	// the whole membership once the client has asked for it, otherwise the
	// gateway's members plus whoever lazy author resolution has pulled in.
	//
	// It resolves a nickname, an avatar, a presence and a role colour per member,
	// so it is the most expensive read here. Call it off the UI thread.
	Members(serverID string) []Member

	// MemberRoles resolves one member's roles, most senior first. Kept off Member
	// because only a profile draws them and building a sidebar would otherwise
	// allocate a slice per row.
	MemberRoles(serverID, userID string) []Role

	// HoistedRoles lists the roles a server displays as sections of their own,
	// most senior first. Separate from MemberRoles because the member list needs
	// the server's sections once, not every member's roles once per member.
	HoistedRoles(serverID string) []Role

	// MessageAuthor resolves a message's author in one pass — channel to member to
	// user — preferring the per-server member and falling back to the raw user.
	MessageAuthor(message *Message) Author

	// SystemTextParts renders a system message, resolving whoever it is about and
	// handing the name back apart from the sentence around it — the client draws
	// that name as a mention, so it cannot arrive already folded into prose.
	SystemTextParts(system *SystemMessage) (name, rest string)

	// Permissions is everything the account may do in a channel, and
	// ServerPermissions the same question at server scope. Both report the empty
	// set when there is nothing to resolve against — logged out, or an ID nothing
	// is known about — which callers read as "assume nothing is allowed".
	//
	// A whole bitfield rather than a question per permission because a call site
	// asking three things should walk the roles once, and because the interface
	// would otherwise grow a method for every bit Revolt defines. Ask it with
	// Permission.Has.
	Permissions(channelID string) Permission
	ServerPermissions(serverID string) Permission
}
