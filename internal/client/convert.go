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
	"strconv"
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
	if len(files) == 0 {
		return nil
	}

	out := make([]*domain.File, 0, len(files))
	for _, file := range files {
		if converted := toFile(file); converted != nil {
			out = append(out, converted)
		}
	}

	return out
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
	out := make([]*domain.Message, 0, len(messages))
	for _, message := range messages {
		if converted := toMessage(message); converted != nil {
			out = append(out, converted)
		}
	}

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
	if len(embeds) == 0 {
		return nil
	}

	out := make([]*domain.Embed, 0, len(embeds))
	for _, embed := range embeds {
		if converted := toEmbed(embed); converted != nil {
			out = append(out, converted)
		}
	}

	return out
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

/* Channels */

func toChannelKind(kind revoltgo.ChannelType) domain.ChannelKind {
	switch kind {
	case revoltgo.ChannelTypeDM:
		return domain.ChannelDM
	case revoltgo.ChannelTypeGroup:
		return domain.ChannelGroup
	case revoltgo.ChannelTypeSavedMessages:
		return domain.ChannelSavedMessages
	case revoltgo.ChannelTypeVoice:
		return domain.ChannelVoice
	}

	return domain.ChannelText
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

// parseHexTriple parses the digits of "RGB" or "RRGGBB", without their '#'.
func parseHexTriple(hex string) (color.Color, bool) {
	parse := func(h string) (uint8, bool) {
		v, err := strconv.ParseUint(h, 16, 8)
		return uint8(v), err == nil
	}

	switch len(hex) {
	case 3: // #RGB -> #RRGGBB
		r, okR := parse(hex[0:1] + hex[0:1])
		g, okG := parse(hex[1:2] + hex[1:2])
		b, okB := parse(hex[2:3] + hex[2:3])
		if okR && okG && okB {
			return color.NRGBA{R: r, G: g, B: b, A: 255}, true
		}
	case 6: // #RRGGBB
		r, okR := parse(hex[0:2])
		g, okG := parse(hex[2:4])
		b, okB := parse(hex[4:6])
		if okR && okG && okB {
			return color.NRGBA{R: r, G: g, B: b, A: 255}, true
		}
	}

	return nil, false
}

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}
