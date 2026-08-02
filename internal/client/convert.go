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

// toMessage converts a message. Embeds, reactions, flags and the masquerade's
// contents are dropped: nothing renders them, and carrying them would mean
// holding a second copy of every cached message's payload.
func toMessage(message *revoltgo.Message) *domain.Message {
	if message == nil {
		return nil
	}

	out := &domain.Message{
		ID:          message.ID,
		ChannelID:   message.Channel,
		AuthorID:    message.Author,
		Content:     message.Content,
		Attachments: toFiles(message.Attachments),
		Replies:     message.Replies,
		Edited:      message.Edited,
		Masquerade:  message.Masquerade != nil,
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

// parseHexColor parses "#RGB" and "#RRGGBB". Anything else — a gradient, a CSS
// name — yields ok=false, and the caller falls back to its own default.
func parseHexColor(s string) (color.Color, bool) {
	if len(s) == 0 || s[0] != '#' {
		return nil, false
	}
	hex := s[1:]

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
