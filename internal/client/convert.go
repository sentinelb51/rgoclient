package client

// The one place revoltgo's wire types become domain values. Everything above
// this package is written against the domain, so a field the API renames or a
// pointer it makes optional is a change here and nowhere else.
//
// Messages are converted once, on the way into the cache — off the gateway or
// off a fetched page — rather than per mount, so the per-widget cost of the seam
// is zero.

import (
	"image/color"
	"slices"
	"strings"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// avatarSize is the rendition asked for of every avatar and icon the client
// draws. One size for all of them, because the image cache is keyed by file ID
// alone: asking for a small one somewhere would decide what every larger
// requester got.
const avatarSize = "256"

// iconSize is the rendition asked for of a server icon, which is never drawn
// larger than the sidebar's 40px.
const iconSize = "64"

// bannerSize is the rendition asked for of a profile background, which is never
// drawn wider than the profile dialog.
const bannerSize = "512"

// emojiTag is the Autumn bucket custom emoji are served from, and emojiSize the
// rendition asked for: one is drawn at a line's height and never larger, so the
// smallest rendition the CDN offers is already generous.
const (
	emojiTag  = "emojis"
	emojiSize = "128"
)

// convertAll maps a slice of wire values through convert, dropping the ones it
// refuses — a nil entry, a video embed with no player to hand it to, an unfurl
// that came back with nothing to say. Every list crossing the boundary is
// converted this way, so "nothing in, nothing out" is one rule rather than three.
func convertAll[In, Out any](in []In, convert func(In) *Out) []*Out {
	if len(in) == 0 {
		return nil
	}

	out := make([]*Out, 0, len(in))
	for _, value := range in {
		if converted := convert(value); converted != nil {
			out = append(out, converted)
		}
	}

	return out
}

/* Files */

// toFile converts an uploaded file, resolving the URL it is served from. Revolt
// makes metadata optional, so a file it could not introspect yields zero
// dimensions and a kind guessed from the filename.
func toFile(file *revoltgo.File) *domain.File {
	if file == nil {
		return nil
	}

	out := &domain.File{
		ID:   file.ID,
		Name: file.Filename,
		URL:  file.URL(""),
		Kind: domain.FileKindOf(file.Filename),
		Size: file.Size,
	}

	if file.Metadata == nil {
		return out
	}
	out.Width, out.Height = file.Metadata.Width, file.Metadata.Height

	// The server's own classification wins where it has one: an image with no
	// extension is still an image, and it is the metadata that says so.
	switch file.Metadata.Type {
	case revoltgo.FileMetadataTypeImage:
		out.Kind = domain.FileImage
	case revoltgo.FileMetadataTypeVideo:
		out.Kind = domain.FileVideo
	case revoltgo.FileMetadataTypeAudio:
		out.Kind = domain.FileAudio
	case revoltgo.FileMetadataTypeText:
		out.Kind = domain.FileText
	}

	return out
}

func toFiles(files []*revoltgo.File) []*domain.File {
	return convertAll(files, toFile)
}

/* Messages */

// Revolt's message flags are a bitfield, and revoltgo numbers them 1, 2, 3 —
// positions rather than bits, so its MentionsOnline collides with
// SuppressNotifications|MentionsEveryone and can never be read for what it is.
// The two the client cares about are named here instead.
const (
	flagMentionsEveryone uint32 = 1 << 1
	flagMentionsOnline   uint32 = 1 << 2
)

// toMessage converts a message. Reactions and the masquerade's contents are
// dropped: nothing renders them, and carrying them would mean holding a second
// copy of every cached message's payload.
//
// What warms a row is kept. Mentions shares the decoder's slice rather than
// copying it, the wire message being discarded here; the channel-wide pings are
// a flag with nobody named in Mentions, and @everyone and @online collapse to one
// answer because the reader is addressed either way.
func toMessage(message *revoltgo.Message) *domain.Message {
	if message == nil {
		return nil
	}

	out := &domain.Message{
		ID:               message.ID,
		ChannelID:        message.Channel,
		AuthorID:         message.Author,
		Content:          message.Content,
		Attachments:      toFiles(message.Attachments),
		Embeds:           toEmbeds(message.Embeds),
		Replies:          message.Replies,
		Mentions:         message.Mentions,
		MentionsEveryone: uint32(message.Flags)&(flagMentionsEveryone|flagMentionsOnline) != 0,
		Edited:           message.Edited,
		Masquerade:       message.Masquerade != nil,
		Pinned:           message.Pinned,
		Reactions:        toReactions(message.Reactions),
	}

	if message.System != nil {
		out.System = &domain.SystemMessage{
			Kind:   domain.SystemKind(message.System.Type),
			Target: message.System.ID,
		}
	}
	if message.Webhook != nil {
		out.Webhook = &domain.Webhook{
			Name:      message.Webhook.Name,
			AvatarURL: message.Webhook.AvatarURL(avatarSize),
		}
	}

	return out
}

func toMessages(messages []*revoltgo.Message) []*domain.Message {
	return convertAll(messages, toMessage)
}

// toReactions orders a message's reactions by the emoji itself.
//
// An order has to be chosen here, and it cannot be Revolt's: reactions arrive as
// a JSON object, which revoltgo decodes into a map, and a map has no order at
// all — rendered as it iterates, the chips would deal themselves a fresh hand on
// every repaint. Sorting by the emoji is the one order that survives a count
// changing, which is what a chip has to do: somebody joining a reaction must not
// move the one beside it out from under the pointer.
//
// The cost is that the chips are not in the order people chose them, which is
// what other clients show. Nothing in the payload records that order.
func toReactions(reactions map[string][]string) []domain.Reaction {
	if len(reactions) == 0 {
		return nil
	}

	out := make([]domain.Reaction, 0, len(reactions))
	for emoji, users := range reactions {
		out = append(out, domain.Reaction{Emoji: emoji, Users: users})
	}
	slices.SortFunc(out, func(a, b domain.Reaction) int { return strings.Compare(a.Emoji, b.Emoji) })

	return out
}

/* Embeds */

func toEmbedKind(kind string) domain.EmbedKind {
	switch kind {
	case "Website":
		return domain.EmbedWebsite
	case "Image":
		return domain.EmbedImage
	case "Video":
		return domain.EmbedVideo
	case "Text":
		return domain.EmbedText
	}

	return domain.EmbedNone
}

// toEmbed converts one embed, reporting nil for the ones nothing can draw.
//
// A video is one of them: Revolt puts a bare video's dimensions beside its type
// rather than under a field of their own, so revoltgo carries only the URL, and
// there is no player here to hand it to. The other is an unfurl that came back
// with nothing to say — no title, no text and no picture — which would otherwise
// draw an empty card under the link.
func toEmbed(embed *revoltgo.MessageEmbed) *domain.Embed {
	if embed == nil {
		return nil
	}

	kind := toEmbedKind(embed.Type)
	if kind == domain.EmbedVideo {
		return nil
	}

	out := &domain.Embed{
		Kind:        kind,
		URL:         embed.URL,
		SiteName:    embed.SiteName,
		Title:       embed.Title,
		Description: embed.Description,
		IconURL:     embed.IconURL,
	}
	if out.URL == "" {
		out.URL = embed.OriginalURL
	}
	if colour, ok := parseColor(embed.Colour); ok {
		out.Color = colour
	}

	switch {
	case embed.Image != nil:
		out.Image = &domain.File{
			Name:   nameFromURL(embed.Image.URL),
			URL:    embed.Image.URL,
			Kind:   domain.FileImage,
			Width:  embed.Image.Width,
			Height: embed.Image.Height,
		}
	case embed.Media != nil:
		// An integration can attach anything to its card; only a picture is drawn.
		if media := toFile(embed.Media); media.Kind == domain.FileImage {
			out.Image = media
		}
	case kind == domain.EmbedImage:
		// A bare image embed *is* its URL, and its dimensions sit beside the type
		// where revoltgo has no field for them — so it is drawn against the same
		// placeholder box an attachment with no metadata gets.
		out.Image = &domain.File{Name: nameFromURL(embed.URL), URL: embed.URL, Kind: domain.FileImage}
	}

	if out.Title == "" && out.Description == "" && out.Image == nil {
		return nil
	}

	return out
}

// nameFromURL is what an embed's picture is called. Unlike an attachment it has
// no name of its own, and the last segment of the path is a filename often
// enough to be worth titling the lightbox with.
func nameFromURL(raw string) string {
	if query := strings.IndexByte(raw, '?'); query != -1 {
		raw = raw[:query]
	}
	if slash := strings.LastIndexByte(raw, '/'); slash != -1 {
		raw = raw[slash+1:]
	}

	return raw
}

func toEmbeds(embeds []*revoltgo.MessageEmbed) []*domain.Embed {
	return convertAll(embeds, toEmbed)
}

/* Servers */

func toServer(server *revoltgo.Server) domain.Server {
	out := domain.Server{
		ID:       server.ID,
		Name:     server.Name,
		OwnerID:  server.Owner,
		Channels: server.Channels,
	}

	if server.Icon != nil {
		out.IconID, out.IconURL = server.Icon.ID, server.Icon.URL(iconSize)
	}

	out.Categories = make([]domain.Category, 0, len(server.Categories))
	for _, category := range server.Categories {
		if category == nil {
			continue
		}
		out.Categories = append(out.Categories, domain.Category{
			ID:       category.ID,
			Title:    category.Title,
			Channels: category.Channels,
		})
	}

	return out
}

// toInvite converts a resolved invite. MemberCount is widened from revoltgo's
// uint64 rather than kept as one: nothing counts members in a way that could
// overflow an int, and every other count in the domain is one.
func toInvite(code string, invite *revoltgo.Invite) domain.Invite {
	out := domain.Invite{
		Code:        code,
		ServerID:    invite.ServerID,
		ServerName:  invite.ServerName,
		ChannelName: invite.ChannelName,
		InviterName: invite.UserName,
		MemberCount: int(invite.MemberCount),
	}

	if invite.ServerIcon != nil {
		out.IconURL = invite.ServerIcon.URL(iconSize)
	}

	return out
}

/* Channels */

// toChannelKind reads a channel's kind off the whole channel rather than off its
// type, because "VoiceChannel" is no longer how a voice channel says so. Stoat
// dropped the variant — voice is now a *text* channel carrying a `voice` object,
// which is the only thing separating the two — so the type alone answers every
// kind except the one that has to be recognised.
func toChannelKind(channel *revoltgo.Channel) domain.ChannelKind {
	switch channel.ChannelType {
	case revoltgo.ChannelTypeDM:
		return domain.ChannelDM
	case revoltgo.ChannelTypeGroup:
		return domain.ChannelGroup
	case revoltgo.ChannelTypeSavedMessages:
		return domain.ChannelSavedMessages
	case revoltgo.ChannelTypeVoice:
		return domain.ChannelVoice
	}

	if channel.Voice != nil {
		return domain.ChannelVoice
	}

	return domain.ChannelText
}

/* Relationships */

// toRelationship converts Revolt's own vocabulary. "User" is the account itself
// and "None" is a stranger, which are different answers to the same question and
// so are named apart.
func toRelationship(kind revoltgo.UserRelationshipType) domain.Relationship {
	switch kind {
	case revoltgo.UserRelationsTypeUser:
		return domain.RelationshipSelf
	case revoltgo.UserRelationsTypeFriend:
		return domain.RelationshipFriend
	case revoltgo.UserRelationsTypeOutgoing:
		return domain.RelationshipOutgoing
	case revoltgo.UserRelationsTypeIncoming:
		return domain.RelationshipIncoming
	case revoltgo.UserRelationsTypeBlocked:
		return domain.RelationshipBlocked
	case revoltgo.UserRelationsTypeBlockedOther:
		return domain.RelationshipBlockedBy
	}

	return domain.RelationshipNone
}

/* Presence and badges */

func toPresence(user *revoltgo.User) domain.Presence {
	if user == nil || !user.Online {
		return domain.PresenceOffline
	}
	if user.Status == nil {
		return domain.PresenceOnline
	}

	switch user.Status.Presence {
	case revoltgo.UserStatusPresenceIdle:
		return domain.PresenceIdle
	case revoltgo.UserStatusPresenceFocus:
		return domain.PresenceFocus
	case revoltgo.UserStatusPresenceBusy:
		return domain.PresenceBusy
	case revoltgo.UserStatusPresenceInvisible:
		// Deliberately indistinguishable from offline — that is what it is for.
		return domain.PresenceOffline
	}

	return domain.PresenceOnline
}

// fromPresence is toPresence backwards, for setting this account's own.
//
// Offline maps to *Invisible*, which is the whole of what choosing it means:
// Revolt has no way to declare yourself offline while connected, and appearing
// offline is what somebody picking it is asking for. toPresence resolves the bit
// back to Offline on the way in, so the two agree and the picker shows what was
// chosen.
func fromPresence(presence domain.Presence) revoltgo.UserStatusPresence {
	switch presence {
	case domain.PresenceIdle:
		return revoltgo.UserStatusPresenceIdle
	case domain.PresenceFocus:
		return revoltgo.UserStatusPresenceFocus
	case domain.PresenceBusy:
		return revoltgo.UserStatusPresenceBusy
	case domain.PresenceOffline:
		return revoltgo.UserStatusPresenceInvisible
	}

	return revoltgo.UserStatusPresenceOnline
}

// badges maps Revolt's badge bits to what each is called, in the order a profile
// lists them. Bits the platform adds later are ignored rather than shown as a
// number nobody can read.
var badges = []struct {
	bit  uint32
	name string
}{
	{1, "Developer"},
	{2, "Translator"},
	{4, "Supporter"},
	{8, "Responsible Disclosure"},
	{16, "Founder"},
	{32, "Moderation"},
	{64, "Active Supporter"},
	{128, "Paw"},
	{256, "Early Adopter"},
}

func toBadges(user *revoltgo.User) []string {
	if user.Badges == 0 {
		return nil
	}

	var names []string
	for _, badge := range badges {
		if user.Badges&badge.bit != 0 {
			names = append(names, badge.name)
		}
	}

	return names
}

/* Colours */

// parseColor reads a Revolt colour. Revolt takes any CSS value for a role or an
// embed accent, and the role presets it offers are as often a gradient as a bare
// triple — every flag and the rainbow among them. So every stop in the value is
// read, in the order written: several become a domain.Gradient, one collapses to
// itself, and a value carrying no hex triple at all — a CSS name, an rgb() — is
// ok=false, leaving the caller its own default.
func parseColor(s string) (color.Color, bool) {
	stops := colorStops(s)

	switch len(stops) {
	case 0:
		return nil, false
	case 1:
		return stops[0], true
	}

	return domain.Gradient(stops), true
}

// colorStops pulls the hex triples out of a CSS value. Nothing else in a
// gradient can be mistaken for one: what precedes the stops is a keyword or an
// angle, and neither carries a '#'.
func colorStops(s string) []color.Color {
	var stops []color.Color

	for i := 0; i < len(s); i++ {
		if s[i] != '#' {
			continue
		}

		end := i + 1
		for end < len(s) && isHexDigit(s[end]) {
			end++
		}
		if c, ok := parseHexTriple(s[i+1 : end]); ok {
			stops = append(stops, c)
		}
		i = end - 1
	}

	return stops
}

// parseHexTriple parses the digits of "RGB" or "RRGGBB", without their '#'. It
// reads nibbles directly rather than going through strconv: every role colour in
// a server passes through here on each Members walk.
func parseHexTriple(hex string) (color.Color, bool) {
	var r, g, b int

	switch len(hex) {
	case 3: // #RGB -> #RRGGBB, each nibble doubled
		r, g, b = hexNibble(hex[0])*17, hexNibble(hex[1])*17, hexNibble(hex[2])*17
	case 6:
		r = hexNibble(hex[0])<<4 | hexNibble(hex[1])
		g = hexNibble(hex[2])<<4 | hexNibble(hex[3])
		b = hexNibble(hex[4])<<4 | hexNibble(hex[5])
	default:
		return nil, false
	}

	if r < 0 || g < 0 || b < 0 {
		return nil, false
	}

	return color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}, true
}

// hexNibble is a hex digit's value, or -1. A negative propagates through the
// shifts above, so one test covers all six digits.
func hexNibble(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}

	return -1
}

func isHexDigit(b byte) bool { return hexNibble(b) >= 0 }
