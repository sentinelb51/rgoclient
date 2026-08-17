// Package domain holds the value types the client is written in terms of, plus
// the one interface at the boundary between talking to Revolt and drawing it.
//
// Nothing here imports revoltgo or fyne: internal/client converts the wire types
// in once, internal/ui draws them without seeing what they came from. That seam
// is load-bearing, not tidiness — revoltgo.State's caches are unexported and its
// constructor package-private, so nothing holding a Session can be built in a
// test. A value can, and so can anything written against Store.
package domain

import (
	"image/color"
	"slices"
	"strings"
	"time"
)

/* Colours */

// Gradient is a colour of more than one stop: a role colour is a CSS colour
// value and Revolt's own presets include gradients.
//
// It is a color.Color in its own right — the mean of its stops — so everything
// filling one shape with a role's colour keeps working without knowing gradients
// exist. Only what can spread one, a run of text, asks for the stops.
type Gradient []color.Color

// RGBA averages the stops, in the premultiplied space color.Color is defined in.
func (g Gradient) RGBA() (uint32, uint32, uint32, uint32) {
	if len(g) == 0 {
		return 0, 0, 0, 0
	}

	var r, gr, b, a uint32
	for _, stop := range g {
		sr, sg, sb, sa := stop.RGBA()
		r, gr, b, a = r+sr, gr+sg, b+sb, a+sa
	}

	n := uint32(len(g))

	return r / n, gr / n, b / n, a / n
}

// At samples the gradient at t in [0,1], interpolating between the two stops it
// falls between. Stops are evenly spaced — Revolt's presets place none of them.
func (g Gradient) At(t float64) color.Color {
	if len(g) == 0 {
		return color.Transparent
	}
	if len(g) == 1 {
		return g[0]
	}

	t = min(max(t, 0), 1)
	span := t * float64(len(g)-1)
	i := min(int(span), len(g)-2)

	return blend(g[i], g[i+1], span-float64(i))
}

// blend mixes two colours, at t of the way from first to second.
func blend(first, second color.Color, t float64) color.Color {
	fr, fg, fb, fa := first.RGBA()
	sr, sg, sb, sa := second.RGBA()

	return color.RGBA{
		R: mixChannel(fr, sr, t),
		G: mixChannel(fg, sg, t),
		B: mixChannel(fb, sb, t),
		A: mixChannel(fa, sa, t),
	}
}

// mixChannel interpolates one channel. RGBA reports 16-bit premultiplied values
// and color.RGBA holds 8-bit ones, so the mix is taken wide and narrowed once.
func mixChannel(x, y uint32, t float64) uint8 {
	return uint8(uint32(float64(x)*(1-t)+float64(y)*t) >> 8)
}

/* Files */

// FileKind classifies an uploaded file by what the client can do with it.
type FileKind uint8

const (
	FileUnknown FileKind = iota
	FileImage
	FileVideo
	FileText
	FileAudio
	FileArchive
	FilePDF
)

// FileKindOf classifies a filename by extension, without allocating when it is
// already lowercase. It classifies a locally picked file; one from Revolt
// carries the server's own answer, which the conversion prefers.
func FileKindOf(filename string) FileKind {
	dot := strings.LastIndexByte(filename, '.')
	if dot == -1 || dot == len(filename)-1 {
		return FileUnknown
	}

	ext := filename[dot+1:]
	for i := range len(ext) {
		if c := ext[i]; c >= 'A' && c <= 'Z' {
			ext = strings.ToLower(ext)
			break
		}
	}

	switch ext {
	case "jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico", "heic", "tiff":
		return FileImage
	case "mp4", "webm", "mov", "mkv", "avi", "flv", "wmv", "m4v":
		return FileVideo
	case "mp3", "wav", "ogg", "flac", "m4a", "aac":
		return FileAudio
	case "zip", "rar", "7z", "tar", "gz", "bz2":
		return FileArchive
	case "pdf":
		return FilePDF
	case "txt", "md", "csv", "json", "xml", "html", "css", "js", "ts", "go", "py", "java", "c", "cpp", "h", "rs", "log":
		return FileText
	default:
		return FileUnknown
	}
}

// File is an uploaded file — an attachment, an avatar, an icon — already
// resolved to the URL it is served from. Width and Height are zero when Revolt
// could not introspect it, so callers test the dimensions rather than a pointer.
type File struct {
	ID   string
	Name string
	URL  string

	Kind   FileKind
	Size   int
	Width  int
	Height int
}

/* Messages */

// Message is a chat message as the client renders one.
type Message struct {
	ID        string
	ChannelID string
	AuthorID  string

	Content     string
	Attachments []*File
	Embeds      []*Embed
	Replies     []string // IDs of the messages this one answers

	// Mentions is who this message pings as Revolt resolved it, so the client asks
	// this rather than re-reading the content: a reply with its mention toggle on
	// lands here, a <@id> for somebody who cannot see the channel does not.
	// MentionsEveryone is @everyone/@online, which arrive as a flag with nobody
	// named in Mentions at all.
	Mentions         []string
	MentionsEveryone bool

	Edited *time.Time

	// System is set when the server generated the message and Webhook when an
	// integration posted it. Masquerade only records that the message carries one:
	// the client does not render it, but a masqueraded message must never group
	// under the account behind it.
	System     *SystemMessage
	Webhook    *Webhook
	Masquerade bool

	// Pinned is kept by the channel, not the message: it changes without an edit,
	// and the pin/unpin system event announces it — see client/events.go, where
	// the partial update Revolt sends alongside can't be read for it.
	Pinned bool

	// Reactions are in the order client/convert.go put them in; Revolt has no
	// opinion about it — see toReactions.
	Reactions []Reaction
}

// Reaction is one emoji on a message and everybody who chose it. Emoji is a
// literal unicode emoji or a custom one's ULID — Revolt uses one field for both.
//
// The people are carried rather than a count because a chip is drawn differently
// for the account that is in it, which is answered here rather than by folding
// the self ID into the conversion.
type Reaction struct {
	Emoji string
	Users []string
}

// By reports whether userID is among those who chose this reaction. Logged out
// is nobody, as it is for a mention.
func (r *Reaction) By(userID string) bool {
	return userID != "" && slices.Contains(r.Users, userID)
}

// Count is how many people chose it.
func (r *Reaction) Count() int { return len(r.Users) }

// MentionsUser reports whether the message pings userID, by name or by pinging
// everyone. Logged out is nobody rather than everybody — hence the guard first:
// an @everyone addresses every reader, and with no account there is none.
func (m *Message) MentionsUser(userID string) bool {
	if userID == "" {
		return false
	}

	return m.MentionsEveryone || slices.Contains(m.Mentions, userID)
}

// Webhook is the identity an integration posted under.
type Webhook struct {
	Name      string
	AvatarURL string
}

// SystemKind is Revolt's own vocabulary, carried verbatim so an event the
// platform adds later reads as unknown rather than as something else.
type SystemKind string

const (
	SystemUserAdded                 SystemKind = "user_added"
	SystemUserRemove                SystemKind = "user_remove"
	SystemUserJoined                SystemKind = "user_joined"
	SystemUserLeft                  SystemKind = "user_left"
	SystemUserKicked                SystemKind = "user_kicked"
	SystemUserBanned                SystemKind = "user_banned"
	SystemChannelRenamed            SystemKind = "channel_renamed"
	SystemChannelDescriptionChanged SystemKind = "channel_description_changed"
	SystemChannelIconChanged        SystemKind = "channel_icon_changed"
	SystemChannelOwnershipChanged   SystemKind = "channel_ownership_changed"
	SystemMessagePinned             SystemKind = "message_pinned"
	SystemMessageUnpinned           SystemKind = "message_unpinned"
	SystemCallStarted               SystemKind = "call_started"
)

// SystemMessage is a server-generated event. Target is what it is about — see
// TargetsUser, since that is not always somebody.
type SystemMessage struct {
	Kind   SystemKind
	Target string
}

// isPin reports whether the event's subject is a message rather than a user.
// Revolt files every system event's subject under one "id" field whatever kind
// of thing it is.
func (s *SystemMessage) isPin() bool {
	return s.Kind == SystemMessagePinned || s.Kind == SystemMessageUnpinned
}

// TargetsUser reports whether Target names an account, deciding whether
// resolving it is worth a fetch. Asked for a pinned message's ID as a user the
// server can only 404, and a failed author fetch drops its guard — so the
// request came back on every remount of the row.
func (s *SystemMessage) TargetsUser() bool {
	return s.Target != "" && !s.isPin()
}

// PinnedMessageID is the message a pin or unpin event announces, else "".
func (s *SystemMessage) PinnedMessageID() string {
	if !s.isPin() {
		return ""
	}

	return s.Target
}

// TextParts renders the event in the two pieces the client draws it in: the name
// it opens with, kept apart because it is tappable like a mention, and the rest
// of the sentence. who is Target's display name, resolved by the caller
// (Store.SystemTextParts) so the wording stays testable without one; an event
// about the channel names nobody and is all rest.
func (s *SystemMessage) TextParts(who string) (name, rest string) {
	if who == "" {
		who = "Someone"
	}

	switch s.Kind {
	case SystemUserAdded:
		return who, " added to group"
	case SystemUserRemove:
		return who, " removed from group"
	case SystemUserJoined:
		return who, " joined"
	case SystemUserLeft:
		return who, " left"
	case SystemUserKicked:
		return who, " was kicked"
	case SystemUserBanned:
		return who, " banned"
	case SystemChannelRenamed:
		return "", "Channel renamed"
	case SystemChannelDescriptionChanged:
		return "", "Channel description changed"
	case SystemChannelIconChanged:
		return "", "Channel icon changed"
	case SystemChannelOwnershipChanged:
		return "", "Channel ownership changed"
	case SystemMessagePinned:
		return "", "Message pinned"
	case SystemMessageUnpinned:
		return "", "Message unpinned"
	case SystemCallStarted:
		return "", "Call started"
	default:
		return "", "System event"
	}
}

/* Embeds */

// EmbedKind is what an embed came from: a link the server unfurled, a bare
// picture or video, or a card an integration composed itself.
type EmbedKind uint8

const (
	EmbedNone EmbedKind = iota
	EmbedWebsite
	EmbedImage
	EmbedVideo
	EmbedText
)

// Embed is a card drawn beneath a message. One shape covers every kind Revolt
// sends because they overlap almost entirely, so a renderer branches on what is
// filled in rather than on Kind — kept only for where two embeds carrying the
// same fields mean different things. Description is markdown, rendered as a
// message body is.
type Embed struct {
	Kind EmbedKind
	URL  string // where the title leads; "" leaves it plain text

	SiteName    string
	Title       string
	Description string
	IconURL     string // the site's own mark, drawn beside its name

	Image *File
	Color color.Color // the accent stripe; nil for the default
}

/* Permissions */

// Permission is a set of the things an account may do somewhere. The bit
// positions are Revolt's own, compared without a translation table in between.
// Only the bits the client asks about are named; the rest still survive a round
// trip, they simply have nothing here to ask them.
type Permission int64

// Channel-scoped permissions, asked of Store.Permissions.
const (
	// PermissionManageChannel is the whole of what editing a channel takes: Stoat's
	// channel_edit route checks it once and gates no field behind anything further.
	PermissionManageChannel Permission = 1 << 0

	PermissionViewChannel        Permission = 1 << 20
	PermissionReadMessageHistory Permission = 1 << 21
	PermissionSendMessage        Permission = 1 << 22
	PermissionManageMessages     Permission = 1 << 23
	PermissionInviteOthers       Permission = 1 << 25
	PermissionUploadFiles        Permission = 1 << 27
	PermissionReact              Permission = 1 << 29

	// PermissionBypassSlowmode is missing from revoltgo's constants, which stop at
	// MentionRoles — the reason every bit is named here rather than imported.
	PermissionBypassSlowmode Permission = 1 << 39
)

// Server-scoped permissions, asked of Store.ServerPermissions.
const (
	PermissionKickMembers Permission = 1 << 6
)

// Has reports whether every permission in want is held. Zero — which is what an
// unresolvable question answers with — holds nothing.
func (p Permission) Has(want Permission) bool { return p&want == want }

/* Channels */

// ChannelKind is what sort of channel this is.
type ChannelKind uint8

const (
	ChannelText ChannelKind = iota
	ChannelVoice
	ChannelDM
	ChannelGroup
	ChannelSavedMessages
)

// IsConversation reports whether the channel is one of the user's own — a DM, a
// group, or saved notes — rather than a server's. Those are the home view's
// rows, the only ones that can be closed, and are drawn as taller picture-led
// cards.
func (k ChannelKind) IsConversation() bool {
	return k == ChannelDM || k == ChannelGroup || k == ChannelSavedMessages
}

// Channel is a channel with everything a row or header needs resolved. Name and
// AvatarURL are that resolution: a DM has no name of its own and is titled after
// the other participant, saved notes after what they are.
type Channel struct {
	ID       string
	ServerID string // "" for a conversation
	Kind     ChannelKind

	Name      string
	AvatarURL string // the conversation's picture; "" for a server channel

	// Description is the channel's topic, drawn beside its name in the header.
	Description string

	// Slowmode is the wait between messages, 0 for none. Server text channels only.
	Slowmode time.Duration

	// UserLimit is how many may be in a voice channel at once, 0 for no cap.
	// Nothing draws it — the call itself is not built — but an edit prefills from
	// what the channel is now.
	UserLimit int

	Recipients    []string
	LastMessageID string
	Active        bool
	NSFW          bool
}

/* Servers */

// Server is a server and the shape of its channel list.
type Server struct {
	ID      string
	Name    string
	OwnerID string

	IconID  string
	IconURL string

	Channels   []string
	Categories []Category
}

// Category groups a server's channels in the sidebar.
type Category struct {
	ID       string
	Title    string
	Channels []string
}

// Invite is what an invite code opens, as Revolt describes it to a non-member.
//
// It is the one server-shaped value that cannot come from a Store: an invite is
// interesting precisely when it names a server the account has never seen, so
// only a request answers it. ServerID still matters — when the account *is*
// already in the server, it turns the card's action from joining into going.
type Invite struct {
	Code string

	ServerID   string
	ServerName string
	IconURL    string

	// Either may be missing, so neither is load-bearing.
	ChannelName string
	InviterName string

	MemberCount int
}

/* Emoji */

// Emoji is one custom emoji a server defines. The picture derives from the ID
// alone (Store.EmojiURL), so this carries only what a picker needs and a
// rendered message does not: the name it is searched by and where it is filed.
type Emoji struct {
	ID       string
	Name     string
	ServerID string // "" for a detached emoji, which belongs to no server
}

/* People */

// Presence is a user's availability, as the ring around their avatar reports it.
type Presence uint8

const (
	PresenceOffline Presence = iota
	PresenceOnline
	PresenceIdle
	PresenceFocus
	PresenceBusy
)

// IsOnline reports whether the presence is any of the ways of being here.
// Invisible is not one: toPresence resolves it to Offline, which is the point.
func (p Presence) IsOnline() bool { return p != PresenceOffline }

// Label names the presence in words.
func (p Presence) Label() string {
	switch p {
	case PresenceOnline:
		return "Online"
	case PresenceIdle:
		return "Idle"
	case PresenceFocus:
		return "Focus"
	case PresenceBusy:
		return "Busy"
	}

	return "Offline"
}

// Relationship is how this account stands with another one. Revolt files it on
// the *other* user rather than as an edge, and it is directional: Blocked and
// BlockedBy are one wall from either side, Outgoing and Incoming one request.
type Relationship uint8

const (
	RelationshipNone      Relationship = iota
	RelationshipSelf                   // this account
	RelationshipFriend                 //
	RelationshipOutgoing               // we asked them
	RelationshipIncoming               // they asked us
	RelationshipBlocked                // we blocked them
	RelationshipBlockedBy              // they blocked us
)

// Known reports whether this is a relationship at all. The account's own record
// is not one — it is how Revolt marks which user is you.
func (r Relationship) Known() bool {
	return r != RelationshipNone && r != RelationshipSelf
}

// Blocked reports whether messages cannot pass, whichever side put the wall up:
// Revolt leaves history readable and takes the rest away in both directions.
func (r Relationship) Blocked() bool {
	return r == RelationshipBlocked || r == RelationshipBlockedBy
}

// User is an account, resolved to what the client shows of one.
type User struct {
	ID       string
	Name     string // display name, falling back to the username
	Username string
	Handle   string // "@username#0001" — what tells two identical display names apart

	// DisplayName is the chosen name alone, empty where there is none — which Name
	// cannot say, having already fallen back to the username.
	DisplayName string

	AvatarURL  string
	Presence   Presence
	StatusText string
	Badges     []string

	// Relationship decides whether a profile offers to write to them or to ask to
	// be friends first.
	Relationship Relationship

	Online bool
	Bot    bool
}

// Member is a user's membership of one server, resolved the way the sidebar and
// a message header show them: nickname, per-server avatar and most-senior
// coloured role override the account's own.
//
// Roles are absent — only a profile draws them, and resolving every member's to
// build a sidebar would allocate a slice per row. Ask Store.MemberRoles instead.
type Member struct {
	ServerID string
	UserID   string

	Name      string
	Username  string // the account handle behind the nickname, for mention matching
	AvatarURL string
	Color     color.Color // most-senior coloured role; nil when none applies

	// HoistRoleID is the most senior *hoisted* role held, or "" — the section the
	// member sidebar files them under. One ID rather than the roles for the reason
	// above: the sidebar needs a bucket per member, not a slice per row.
	HoistRoleID string

	JoinedAt time.Time
	Presence Presence

	// HasRoles counts any role at all, including one the server has not published
	// to us — HoistRoleID is empty for a member whose only role is not hoisted.
	HasRoles bool
	Bot      bool
}

// Role is a server role as a profile card shows one: its name, in its own
// colour. The ID is carried because a chip offers it for copying.
//
// Rank and Hoist are the server's display rules rather than anything a chip
// draws: Revolt ranks the most senior lowest, and a hoisted role gets a section
// of its own in the member list.
type Role struct {
	ID    string
	Name  string
	Color color.Color // nil when the role has no colour, or none that parses

	Rank  int64
	Hoist bool
}

// Author bundles the display fields for a message's author — the one-pass
// channel to member to user walk, resolved.
type Author struct {
	Name      string
	AvatarURL string
	Color     color.Color // nil when no coloured role applies
}

// UserProfile is the half of a profile the client does not already hold: a
// request of its own, so it lands after the card is on screen.
type UserProfile struct {
	Bio           string
	BackgroundURL string // the profile banner; "" leaves the accent colour showing
}

// Mutual is what this account has in common with somebody else. Like
// UserProfile it is a request of its own, and IDs alone: naming them is a lookup
// the controller makes, since the account holds both sides already.
type Mutual struct {
	UserIDs   []string
	ServerIDs []string
}

// Profile is everything the two profile presentations draw, resolved in one
// pass — bar UserProfile, which arrives after the card is on screen.
type Profile struct {
	UserID string
	Name   string // the server nickname where there is one, else the display name
	Handle string
	Status string // the user's own status line

	AvatarURL  string
	Accent     color.Color // most-senior coloured role; nil for the neutral banner
	Presence   Presence
	ServerName string // the open server, for the joined date; "" in a conversation

	Badges []string
	Roles  []Role

	Created time.Time // account creation, from the ID
	Joined  time.Time // joined ServerName; zero outside a server

	Relationship Relationship

	Bot bool
}

/* Composing */

// Attachment is a local file queued in the composer, before it is uploaded.
type Attachment struct {
	Path string
	Name string
}

// Reply is a message the composer is answering, and whether sending it should
// ping the author.
type Reply struct {
	ID      string
	Mention bool
}
