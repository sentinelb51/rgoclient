package domain

// Store is the read side of a Revolt session: everything needed to turn an ID
// into something drawable, answered from what is already known and never from
// the network. A miss reports ok=false rather than blocking — the caller decides
// between a placeholder, a queued fetch, or both.
//
// internal/client implements it over revoltgo.State; a test implements it with a
// struct of maps. That is why it exists: State's caches are unexported, so code
// written against a *revoltgo.Session cannot be given contents to answer from.
//
// Everything here returns resolved domain values. Handing back the wire types
// would only move the resolution to the call sites and put revoltgo back inside
// internal/ui.
type Store interface {
	// Self is the logged-in account; SelfID the same question unresolved, for the
	// many places that only ask whether something belongs to the user.
	Self() (User, bool)
	SelfID() string

	User(userID string) (User, bool)

	// UserName is the name to show for a user, or "" when unknown — all a rendered
	// mention needs, and unlike User it allocates nothing.
	UserName(userID string) string

	// Relationships is everybody this account stands in some relation to, ordered
	// by display name. A walk rather than a lookup, so it belongs off the UI
	// thread with Members: Revolt files a relationship on the other account rather
	// than as a collection, so this reports whoever is both known and related.
	Relationships() []User

	// HasIncomingRequest reports whether anybody is waiting on this account to
	// answer a friend request. The same walk Relationships makes, resolving
	// nobody and ordering nothing: the sidebar's mark is one boolean, and it is
	// asked for again every time a batch of message authors lands.
	HasIncomingRequest() bool

	Member(serverID, userID string) (Member, bool)
	Channel(channelID string) (Channel, bool)
	Server(serverID string) (Server, bool)

	// ChannelName is UserName's counterpart for a rendered <#id>, allocating
	// nothing where Channel would resolve a picture and slowmode nobody asked for.
	ChannelName(channelID string) string

	// EmojiURL is where a custom emoji's picture is served from, or "" for an
	// empty ID. Derived from the ID rather than looked up — hence no ok: a message
	// can carry an emoji from a server the account is not in, and the CDN serves
	// it regardless.
	EmojiURL(emojiID string) string

	// EmojiName is the shortcode a custom emoji is written as, or "" for one the
	// account holds no server for — unlike the picture, a name can only be looked
	// up, and a message routinely carries an emoji from somewhere the account is
	// not. It is what stands in where a picture cannot be drawn at all.
	EmojiName(emojiID string) string

	// Emojis is every custom emoji the account may use, ordered by name. A walk,
	// so the picker asks once when it opens rather than per entry.
	Emojis() []Emoji

	// HasUser and HasMember answer whether a record is resolved without resolving
	// it. Lazy author resolution asks once per mounted message, so this pair is on
	// the render hot path and must not allocate.
	HasUser(userID string) bool
	HasMember(serverID, userID string) bool

	// Members lists everyone the client knows of in a server, ordered by display
	// name unless the settings say otherwise — the whole membership once fetched,
	// otherwise the gateway's plus whoever lazy author resolution pulled in.
	//
	// It resolves a nickname, avatar, presence and role colour per member, so it
	// is the most expensive read here. Call it off the UI thread.
	Members(serverID string) []Member

	// VoiceParticipants is everybody connected to a voice channel's call, ordered
	// by display name. A walk like Members and resolved the same way, but of the
	// handful in one call rather than a whole membership, so the channel sidebar
	// asks for it per voice channel as it builds.
	VoiceParticipants(channelID string) []VoiceParticipant

	// MemberRoles resolves one member's roles, most senior first. Kept off Member
	// because only a profile draws them.
	MemberRoles(serverID, userID string) []Role

	// HoistedRoles lists the roles a server displays as sections of their own,
	// most senior first — the sections once, not every member's roles per member.
	HoistedRoles(serverID string) []Role

	// ServerRoles is every role a server defines, most senior first, each with the
	// override the role editor sets. Ready carries them, so this is a read like any
	// other rather than the fetch the route would be.
	ServerRoles(serverID string) []Role

	// MessageAuthor resolves an author in one pass — channel to member to user —
	// preferring the per-server member and falling back to the raw user.
	MessageAuthor(message *Message) Author

	// SystemTextParts renders a system message, handing the resolved name back
	// apart from the sentence around it: the client draws that name as a mention,
	// so it cannot arrive folded into prose.
	SystemTextParts(system *SystemMessage) (name, rest string)

	// Permissions is everything the account may do in a channel, and
	// ServerPermissions the same at server scope. Both report the empty set when
	// there is nothing to resolve against, which callers read as "allow nothing".
	//
	// A bitfield rather than a question per permission: a call site asking three
	// things walks the roles once, and the interface does not grow a method per
	// bit Revolt defines. Ask it with Permission.Has.
	Permissions(channelID string) Permission
	ServerPermissions(serverID string) Permission

	// MemberServerPermissions is the same question about somebody else, which is
	// what a moderation action asks: Revolt refuses a timeout against anybody
	// holding the permission to hand one out.
	MemberServerPermissions(serverID, userID string) Permission

	// ChannelOverrides is what one server channel changes about its server's
	// permissions, which is what the channel half of the permission editor sets.
	// Kept off Channel, which is asked for on every sidebar row and every header:
	// this copies a map, and only one page ever reads it. Answers false for a
	// conversation, which has no roles to override anything for.
	ChannelOverrides(channelID string) (ChannelOverrides, bool)
}
