// Package domain holds the value types the client is written in terms of, plus
// the one interface at the boundary between talking to Revolt and drawing it.
//
// Nothing here imports revoltgo or fyne. internal/client converts the wire types
// into these once, on the way in; internal/ui draws them without ever seeing
// what they came from. That is the point of the package rather than a tidiness
// exercise: revoltgo.State's caches are unexported and its constructor is
// package-private, so nothing holding a Session can be built in a test. A value
// can, and so can anything written against Store.
package domain

import (
	"image/color"
	"slices"
	"strings"
	"time"
)

/* Colours */

// Gradient is a colour of more than one stop. A Revolt role colour is a CSS
// colour value, and the presets the server itself offers include gradients, so
// what arrives for a role is not always one colour.
//
// It is a color.Color in its own right — the mean of its stops — so everything
// filling a single shape with a role's colour (a chip's dot, a reply's accent
// bar, a picker row) keeps working without knowing gradients exist. Only what can
// spread one, a run of text, asks for the stops.
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
// falls between. Stops are evenly spaced: Revolt's own presets place none of
// them, and a stop position nothing sends is not worth carrying.
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

	// RGBA reports 16-bit premultiplied channels; color.RGBA holds 8-bit
	// premultiplied ones, so the mix is taken wide and narrowed once.
	channel := func(x, y uint32) uint8 {
		return uint8(uint32(float64(x)*(1-t)+float64(y)*t) >> 8)
	}

	return color.RGBA{
		R: channel(fr, sr),
		G: channel(fg, sg),
		B: channel(fb, sb),
		A: channel(fa, sa),
	}
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

// FileKindOf classifies a filename by its extension, avoiding an allocation when
// the extension is already lowercase (the common case for web content). It is
// what a locally picked file is classified by; one that came from Revolt carries
// the server's own answer, which the conversion prefers.
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
// resolved to the URL it is served from.
//
// Width and Height are zero when Revolt could not introspect the file: its
// metadata is optional, and that is the whole of the difference here. Callers
// test the dimensions rather than a pointer, which is what the old
// AttachmentDimensions helper existed to spare them.
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

	// Mentions is who this message pings, as Revolt resolved it — a reply with its
	// mention toggle on lands here too, and a <@id> the author typed for somebody
	// who cannot see the channel does not. So the client asks this rather than
	// re-reading the content.
	//
	// MentionsEveryone is the channel-wide ping — Revolt's @everyone and @online,
	// which arrive as a flag with nobody named in Mentions at all.
	Mentions         []string
	MentionsEveryone bool

	Edited *time.Time

	// System is set when the server generated the message rather than anyone
	// typing it, and Webhook when an integration posted it. Masquerade only
	// records that the message carries one: the client does not render a
	// masquerade, but a masqueraded message must never group under the account
	// behind it.
	System     *SystemMessage
	Webhook    *Webhook
	Masquerade bool
}

// MentionsUser reports whether the message pings userID, by name or by pinging
// everyone who can see the channel. Logged out — no self ID — is nobody, not
// everybody, which is why the guard comes first: an @everyone addresses every
// reader, and with no account there is no reader to address.
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

// SystemKind is Revolt's own vocabulary for what a system message announces,
// carried verbatim so an event the platform adds later reads as unknown rather
// than as something else.
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

// SystemMessage is a server-generated event. Target is whoever it is about,
// where it is about someone.
type SystemMessage struct {
	Kind   SystemKind
	Target string
}

// TextParts renders the event as a line of prose, in the two pieces the client
// draws it in: the name it opens with, and the rest of the sentence. The name is
// kept apart because it is tappable, exactly as a mention in a message body is.
//
// who is Target's display name, which the caller resolves —
// Store.SystemTextParts is the one that does, and this stays pure so the wording
// can be tested without one. An event about the channel rather than about
// somebody names nobody: the name is empty and the whole sentence is the rest.
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
// sends, because they overlap almost entirely: a link preview names the site and
// quotes the page, an integration's card sets its own title, colour and picture,
// and a bare image carries nothing but the picture. So a renderer branches on
// what is filled in rather than on Kind, which is kept only for the cases where
// two embeds carrying the same fields mean different things.
//
// Description is markdown and is rendered exactly as a message body is.
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
// positions are Revolt's own, carried verbatim the way SystemKind carries its
// vocabulary: they arrive from the server as one number and are compared here
// without a translation table in between.
//
// Only the bits the client actually asks about are named. The rest still survive
// a round trip — a Permission holds whatever the server sent — they simply have
// nothing here to ask them.
type Permission int64

// Channel-scoped permissions, asked of Store.Permissions.
const (
	PermissionViewChannel        Permission = 1 << 20
	PermissionReadMessageHistory Permission = 1 << 21
	PermissionSendMessage        Permission = 1 << 22
	PermissionManageMessages     Permission = 1 << 23
	PermissionUploadFiles        Permission = 1 << 27

	// PermissionBypassSlowmode is missing from revoltgo's constants — they stop at
	// MentionRoles — which is the reason every bit is named here rather than
	// imported from there.
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

// IsConversation reports whether the channel is one of the user's own — a direct
// message, a group, or their saved notes — as opposed to a channel belonging to
// a server. Those are the home view's rows, they are the only ones that can be
// closed, and they are drawn as taller cards led by a picture rather than a
// glyph.
func (k ChannelKind) IsConversation() bool {
	return k == ChannelDM || k == ChannelGroup || k == ChannelSavedMessages
}

// Channel is a channel with everything a row or a header needs already
// resolved. Name and AvatarURL are the resolution: a direct message has no name
// of its own — it is titled after the other participant — and saved notes are
// titled for what they are rather than after the account reading them.
type Channel struct {
	ID       string
	ServerID string // "" for a conversation
	Kind     ChannelKind

	Name      string
	AvatarURL string // the conversation's picture; "" for a server channel

	// Slowmode is how long a member must wait between messages here, 0 when the
	// channel has none. Only a server's text channels carry one.
	Slowmode time.Duration

	Recipients    []string
	LastMessageID string
	Active        bool
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

// Invite is what an invite code opens, described as Revolt describes it to
// someone who is not a member yet.
//
// It is the one server-shaped value that cannot come from a Store: an invite is
// interesting precisely when it names a server the account has never seen, so
// there is nothing local to resolve it against and it only ever arrives from a
// request. ServerID is still worth carrying — when the account *is* already in
// the server, that is what turns the card's action from joining into going there.
type Invite struct {
	Code string

	ServerID   string
	ServerName string
	IconURL    string

	// ChannelName is where the code lands and InviterName who created it. Revolt
	// sends both and either may be missing, so neither is load-bearing.
	ChannelName string
	InviterName string

	MemberCount int
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
// Invisible is deliberately not one of them — toPresence resolves it to Offline,
// which is what it is for.
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

// User is an account, resolved to what the client shows of one.
type User struct {
	ID       string
	Name     string // display name, falling back to the username
	Username string
	Handle   string // "@username#0001" — what tells two identical display names apart

	AvatarURL  string
	Presence   Presence
	StatusText string
	Badges     []string

	Online bool
	Bot    bool
}

// Member is a user's membership of one server, resolved the way the sidebar and
// a message header show them: the nickname, the per-server avatar and the
// most-senior coloured role override the account's own.
//
// Roles are deliberately absent — only a profile draws them, and resolving every
// member's roles to build a sidebar would allocate a slice per row. Ask
// Store.MemberRoles for the one member whose profile is open.
type Member struct {
	ServerID string
	UserID   string

	Name      string
	Username  string // the account handle behind the nickname, for mention matching
	AvatarURL string
	Color     color.Color // most-senior coloured role; nil when none applies

	// HoistRoleID is the most senior *hoisted* role the member holds, or "" — the
	// section the member sidebar files them under. One ID rather than the roles
	// themselves for the same reason the roles are absent: the sidebar needs a
	// bucket per member, and a slice per row is what that costs.
	HoistRoleID string

	JoinedAt time.Time
	Presence Presence

	// HasRoles is whether the member holds any role at all, counting one the
	// server has not published to us — HoistRoleID answers a narrower question
	// and is empty for a member whose only role is not hoisted.
	HasRoles bool
	Bot      bool
}

// Role is a server role the way a profile card shows one: its name, in its own
// colour. The ID is carried because a chip offers it for copying — nothing else
// resolves a role by it.
//
// Rank and Hoist are the server's own display rules rather than anything a chip
// draws: Revolt ranks the most senior lowest, and a hoisted role is one the
// member list gives a section of its own.
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

// Profile is everything the two profile presentations draw, resolved in one
// pass. UserProfile is the exception: it is a request of its own, so it arrives
// after the card is already on screen.
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
