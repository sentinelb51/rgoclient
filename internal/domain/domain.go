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
	_, ext, ok := strings.CutLast(filename, ".")
	if !ok || ext == "" {
		return FileUnknown
	}

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

	// ContentType is the raw MIME type the server recorded at upload, empty
	// where none was. Kind is the classification to branch on; this is for the
	// finer questions a kind cannot answer — an image being "image/gif" is what
	// offers it a player.
	ContentType string

	// Foreign marks a URL served by somebody other than the instance's own CDN —
	// an embed's picture, fetched from whatever host the unfurl named. It is not
	// about trust in the picture but in the *name*: a CDN URL's file ID is what
	// every cache entry is keyed by, and a foreign path shaped like one would
	// otherwise be filed under an ID it does not own, so one message's embed could
	// replace an avatar or a server icon everywhere it is drawn.
	Foreign bool
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

	// Pinned changes without an edit, and is announced twice: as a partial update
	// carrying the flag, and as a system line in the channel. The update is what
	// client/events.go reads — the line is a message like any other, and deriving
	// state from one is deriving it from a rendering.
	Pinned bool

	// Reactions are in the order client/convert.go put them in; Revolt has no
	// opinion about it — see toReactions.
	Reactions []Reaction

	// Interactions is what the message allows to be done with it, and is nil for
	// almost every message: only one posted by a bot carries one.
	Interactions *Interactions
}

// Interactions is a message's own rules about reacting to it. Reactions is the
// emoji it names and RestrictReactions is whether that list is the whole of what
// may be added — Revolt refuses anything outside it, so the client offers the
// list rather than a pick the server would reject.
//
// The two are independent: an unrestricted list is a suggestion, which this
// client does not draw, so only the restricted case reaches a widget.
type Interactions struct {
	Reactions         []string
	RestrictReactions bool
}

// ReactionsAllowed is the emoji this message may be reacted with. restricted is
// false when anything goes, which is every message but a bot's; a restricted
// message naming nothing allows no reaction at all, and the surfaces offering
// one leave it off rather than opening a picker with nothing in it.
func (m *Message) ReactionsAllowed() (emoji []string, restricted bool) {
	if m.Interactions == nil || !m.Interactions.RestrictReactions {
		return nil, false
	}

	return m.Interactions.Reactions, true
}

// MaxBulkDelete is how many messages one bulk delete may name, and
// MaxBulkDeleteAge how old the oldest of them may be. Both are Revolt's, and
// both live here for the reason MessageSort does: the surface offering the
// selection has to refuse exactly what the request would, and neither end may
// name the other's type.
//
// The route walks every ID before it looks at a permission and refuses the whole
// batch over one that is too old, so a message a week and a minute past would
// cost the ninety-nine beside it. Hence the age is a rule the client applies
// rather than a rejection it reports.
const (
	MaxBulkDelete    = 100
	MaxBulkDeleteAge = 7 * 24 * time.Hour
)

// MessageSort is the order a channel search asks its answer back in. It lives
// here rather than in the client because both ends need it: the widget offering
// the choice and the request carrying it, and neither may name the other's type.
//
// Relevance is the route's own ranking and cannot be reproduced from what comes
// back, which is why the sort is part of the request rather than something the
// controller does to the answer.
type MessageSort int

const (
	SortRelevance MessageSort = iota
	SortNewest
	SortOldest
)

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

/* GIFs */

// GIF is one result from the GIF service. What a message carries is PageURL —
// the page is sent and Revolt unfurls it into an embed — where Formats are
// renditions of the picture itself, for choosing one by. They are keyed by the
// service's own names because nothing here defines them, and a service that
// grows a rendition should not need a field.
type GIF struct {
	ID      string
	PageURL string

	Formats map[string]GIFFormat
}

// GIFFormat is one rendition. A zero dimension is one the service left out.
type GIFFormat struct {
	URL    string
	Width  int
	Height int
}

// GIFCategory is a browsable heading, and is searched for by its own Title.
type GIFCategory struct {
	Title    string
	ImageURL string
}

// GIFPage is a page of results. Next is what the page after it is asked for
// with, and is empty on the last.
type GIFPage struct {
	Results []GIF
	Next    string
}

/* Permissions */

// Permission is a set of the things an account may do somewhere. The bit
// positions are Revolt's own, compared without a translation table in between.
// Only the bits the client asks about are named; the rest still survive a round
// trip, they simply have nothing here to ask them.
type Permission int64

// Permissions named for a channel, asked of Store.Permissions.
const (
	// PermissionManageChannel is the whole of what editing a channel takes: Stoat's
	// channel_edit route checks it once and gates no field behind anything further.
	// It is also the bit *making* a channel takes, so it is asked of a server as
	// well (Store.ServerPermissions) and is not channel-only.
	PermissionManageChannel Permission = 1 << 0

	PermissionViewChannel        Permission = 1 << 20
	PermissionReadMessageHistory Permission = 1 << 21
	PermissionSendMessage        Permission = 1 << 22
	PermissionManageMessages     Permission = 1 << 23
	PermissionInviteOthers       Permission = 1 << 25
	PermissionUploadFiles        Permission = 1 << 27
	PermissionReact              Permission = 1 << 29
	PermissionBypassSlowmode     Permission = 1 << 39

	// Joining a call asks Connect and publishing a microphone asks Speak. Video
	// and Listen are the same scope and are named beside them, though nothing
	// asks either yet: this client has no camera, and a subscriber that may not
	// listen is refused by the voice server rather than by a button here.
	PermissionConnect Permission = 1 << 30
	PermissionSpeak   Permission = 1 << 31
	PermissionVideo   Permission = 1 << 32
	PermissionListen  Permission = 1 << 36
)

// Server-scoped permissions, asked of Store.ServerPermissions.
const (
	PermissionManageServer      Permission = 1 << 1
	PermissionManagePermissions Permission = 1 << 2
	PermissionManageRole        Permission = 1 << 3
	PermissionKickMembers       Permission = 1 << 6
	PermissionBanMembers        Permission = 1 << 7
	PermissionTimeoutMembers    Permission = 1 << 8
	PermissionAssignRoles       Permission = 1 << 9
	PermissionChangeNickname    Permission = 1 << 10
	PermissionManageNicknames   Permission = 1 << 11

	// Voice moderation. Revolt files these in the same bit space as the channel
	// permissions above, but each is asked *of a member* from the member menu,
	// which is a server question.
	PermissionMuteMembers   Permission = 1 << 33
	PermissionDeafenMembers Permission = 1 << 34
	PermissionMoveMembers   Permission = 1 << 35
)

// The rest of what Revolt defines, which the role editor lists rather than asks.
// Positions from ChannelPermission in
// https://github.com/stoatchat/stoatchat/blob/main/crates/core/permissions/src/models/channel.rs
const (
	PermissionManageCustomisation Permission = 1 << 4
	PermissionChangeAvatar        Permission = 1 << 12
	PermissionRemoveAvatars       Permission = 1 << 13
	PermissionManageWebhooks      Permission = 1 << 24
	PermissionSendEmbeds          Permission = 1 << 26
	PermissionMasquerade          Permission = 1 << 28
	PermissionMentionEveryone     Permission = 1 << 37
	PermissionMentionRoles        Permission = 1 << 38
	PermissionViewAuditLogs       Permission = 1 << 40
)

// Has reports whether every permission in want is held. Zero — which is what an
// unresolvable question answers with — holds nothing.
func (p Permission) Has(want Permission) bool { return p&want == want }

// PermissionOverride is what one role is granted and what it is refused
// somewhere. A bit in neither half is inherited from whatever else has an
// opinion, which is what makes an override three states per bit where a plain
// set is two.
type PermissionOverride struct {
	Allow Permission
	Deny  Permission
}

// ChannelOverrides is what one channel changes about its server's permissions.
// Both halves are overrides — a channel stands under the server, so even its
// default has something beneath it to inherit from, unlike the server's own.
//
// An absent entry and an empty one mean the same thing, so nothing here
// distinguishes them: a role changing nothing in a channel is a role with no
// override there.
type ChannelOverrides struct {
	// Default applies to everybody in the channel before any role does.
	Default PermissionOverride

	// Roles is the override per role ID, holding only the roles that change
	// something here. Nil where none do.
	Roles map[string]PermissionOverride
}

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

	// OwnerID is who made a group, and "" for every other kind. Revolt files a
	// group's own moderation on that one account: whoever owns it is the only one
	// who may put somebody out of it.
	OwnerID string

	// Permissions is what everybody in a **group** may do, and is meaningless for
	// every other kind. Revolt resolves a group as a view-only floor *or*ed with
	// this, so it is an allow set rather than an overwrite and cannot take away
	// seeing the group; the owner holds everything whatever it says. A group that
	// has never been given one reads as Revolt's own conversation preset rather
	// than as zero — nobody set "deny everything" by not answering.
	Permissions Permission

	Recipients    []string
	LastMessageID string
	Active        bool
	NSFW          bool
}

// VoiceParticipant is somebody connected to a voice channel's call, resolved the
// way the sidebar draws them under that channel's row: the per-server nickname,
// avatar and role colour a Member carries, plus what their end of the call is
// sharing and what has been held against it.
//
// Revolt's `is_publishing` / `is_receiving` are deliberately *not* here. They
// are the media session's own bookkeeping — a microphone track existing at all,
// which a self-mute does not take away — so neither means "muted" or
// "deafened", and a mark that says the wrong thing is worse than no mark.
// ServerMuted and ServerDeafened come from the membership instead, which is
// where Revolt actually files a moderator's hold.
type VoiceParticipant struct {
	UserID string

	Name      string
	AvatarURL string
	Color     color.Color // most-senior coloured role; nil when none applies

	/* Flags */

	Bot           bool
	Camera        bool
	Screensharing bool

	// Held server-wide by a moderator, and true wherever the membership is known
	// — unlike a self-mute, which only somebody in the same call can see.
	ServerMuted    bool
	ServerDeafened bool
}

// CallCredentials is what the join_call route answers with: the voice node to
// dial and the token that authorises this account on it. Not "ticket" — that
// already means the MFA login ticket.
//
// The token is short-lived and minted against one session, so it is never
// stored: a call is rejoined by asking again.
type CallCredentials struct {
	URL   string
	Token string
}

/* Servers */

// Server is a server and the shape of its channel list.
type Server struct {
	ID      string
	Name    string
	OwnerID string

	// Description is the server's own blurb. Only its settings page states it —
	// nothing in the sidebar has room — and it is routinely empty.
	Description string

	IconID  string
	IconURL string

	// BannerURL is the picture behind the channel list in clients that draw one.
	// Nothing here does; it is kept so the settings page knows whether there is one
	// to remove.
	BannerURL string

	// DefaultPermissions is what every member holds before any role adds to it or
	// takes from it — a plain set rather than an override, there being nothing
	// under it to inherit from.
	DefaultPermissions Permission

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
	Kind InviteKind

	ServerID   string
	ServerName string
	IconURL    string
	BannerURL  string

	// ChannelID is where a server invite lands and what a group invite *is* — for
	// a group it is the only ID there is, the account being in the channel or not
	// rather than in a server.
	ChannelID string

	// Either may be missing, so neither is load-bearing.
	ChannelName string
	InviterName string

	MemberCount int
}

// InviteKind is what a code opens. Revolt serves both through one route and
// describes a group with the channel fields alone — no server, no icon and no
// count — so a card that assumed a server would name it wrongly and then quote
// its own channel line back at it.
type InviteKind uint8

const (
	InviteServer InviteKind = iota
	InviteGroup
)

/* Server administration */

// Ban is one entry of a server's ban list.
//
// It carries a name and a picture rather than only an ID to look up: the ban
// list answers with a reduced user of its own, and a banned account is no longer
// a member, so there would be nothing in the Store to resolve it against.
type Ban struct {
	UserID    string
	Username  string
	AvatarURL string

	Reason string
}

// ServerInvite is one invite a server has outstanding.
//
// The whole of what Revolt stores is the code, who made it and where it lands —
// there is no expiry, no use count and no creation time to list beside them.
type ServerInvite struct {
	Code      string
	ChannelID string
	CreatorID string
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
	Nickname  string // the per-server name where there is one, kept apart from Name's fallback
	Username  string // the account handle behind the nickname, for mention matching
	AvatarURL string
	Color     color.Color // most-senior coloured role; nil when none applies

	// HoistRoleID is the most senior *hoisted* role held, or "" — the section the
	// member sidebar files them under. One ID rather than the roles for the reason
	// above: the sidebar needs a bucket per member, not a slice per row.
	HoistRoleID string

	JoinedAt time.Time

	// Timeout is when a server-side timeout expires; zero for none, and one in the
	// past has expired — no gateway event announces the expiry.
	Timeout time.Time

	Presence Presence

	// HasRoles counts any role at all, including one the server has not published
	// to us — HoistRoleID is empty for a member whose only role is not hoisted.
	HasRoles bool
	Bot      bool

	// ServerMuted and ServerDeafened are a moderator's hold on this member's
	// voice, server-wide: Revolt spells them can_publish and can_receive, and
	// stores them on the membership rather than on the call, so they are known
	// whether or not the member is in one. What the menu offers is read from
	// these — an item that can only ever mute somebody already muted is an item
	// with no way back.
	ServerMuted    bool
	ServerDeafened bool
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

	// ColorText is the colour as Revolt holds it, which is any CSS colour — a
	// gradient included — rather than the hex Color was parsed out of.
	ColorText string

	Allow Permission
	Deny  Permission

	Rank  int64
	Hoist bool
}

// Author bundles the display fields for a message's author — the one-pass
// channel to member to user walk, resolved.
type Author struct {
	Name      string
	AvatarURL string
	Color     color.Color // nil when no coloured role applies

	// Mark is what the surfaces drawing this name have to say about it beyond the
	// name itself — see AuthorMark.
	Mark AuthorMark
}

// AuthorMark is what a row says about who posted, beyond the name: nothing for a
// person, and otherwise the one thing the reader needs in order to read the name
// correctly. A bot is an account like any other; a webhook is no account at all,
// so nothing opens; a masquerade is somebody posting under a name and picture the
// client does not draw — the account behind the mask is what it shows.
//
// A message can be more than one of these at once, so the order is a precedence:
// a webhook's identity is the whole of what it is, and a mask is more surprising
// than the bot usually wearing it. Store.MessageAuthor is where it is decided,
// so a header, a quoted line and a summary card cannot disagree.
type AuthorMark int

const (
	AuthorPerson AuthorMark = iota
	AuthorBot
	AuthorWebhook
	AuthorMasquerade
)

// UserProfile is the half of a profile the client does not already hold: a
// request of its own, so it lands after the card is on screen.
type UserProfile struct {
	Bio           string
	BackgroundURL string // the profile banner; "" leaves the accent colour showing
}

// VoiceNode is one media server the instance offers calls through. The name is
// what join_call is asked for; the URL is what a probe dials and what tells two
// nodes apart on a page, an instance being free to name them anything.
type VoiceNode struct {
	Name string
	URL  string
}

// Mutual is what this account has in common with somebody else. Like
// UserProfile it is a request of its own, and IDs alone: naming them is a lookup
// the controller makes, since the account holds both sides already.
type Mutual struct {
	UserIDs   []string
	ServerIDs []string

	// ChannelIDs is the groups the two are both in — the third thing two accounts
	// can share, and one Revolt answers with beside the other two.
	ChannelIDs []string
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

/* This account's own security */

// AccountSession is one login Revolt is holding open for this account, on any
// device. Not a saved session: that is a token this computer has kept so the
// login screen can offer it, where this is the login itself, and revoking one
// signs that device out wherever it is.
//
// The name is whatever the client that signed in called itself, and is the only
// thing telling two apart — so it is editable, and this client composes its own
// out of the machine's hostname.
type AccountSession struct {
	ID   string
	Name string

	// Current marks the session this client is using. Knowable only where its ID
	// was recorded at sign-in: no route answers "which of these am I", so a login
	// restored from a token saved before that was recorded marks nothing rather
	// than guessing at the name.
	Current bool
}

// MFAStatus is which second factors an account has, as `GET /auth/mfa` reports
// them. Only the first two are actionable from here — Revolt defines the rest
// and this client can answer none of them, having no WebAuthn and no way to read
// an email.
type MFAStatus struct {
	TOTP     bool // an authenticator app is set up
	Recovery bool // recovery codes have been generated

	EmailOTP    bool
	SecurityKey bool
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
