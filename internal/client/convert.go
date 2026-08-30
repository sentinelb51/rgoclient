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
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// avatarSize is the rendition asked for of every avatar and icon the client
// draws. One size for all of them, so the same person in a message, the member
// list and a profile card costs one download and one decode — the cache keys a
// rendition separately from the file it is cut from.
const avatarSize = "256"

// iconSize is the rendition asked for of a server icon, which is never drawn
// larger than the sidebar's 40px.
const iconSize = "64"

// bannerSize is the rendition asked for of a profile background, which is never
// drawn wider than the profile dialog.
const bannerSize = "512"

// emojiSize is the rendition asked for of a custom emoji: one is drawn at a
// line's height and never larger, so the smallest the CDN offers is already
// generous. The bucket it is served from is revoltgo.FileTagEmojis.
const emojiSize = "128"

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
		ID:          file.ID,
		Name:        file.Filename,
		URL:         file.URL(""),
		Kind:        domain.FileKindOf(file.Filename),
		Size:        file.Size,
		ContentType: file.ContentType,
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

// toMessage converts a message. The masquerade's contents are dropped: nothing
// renders them, and carrying them would mean holding a second copy of every
// cached message's payload.
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
		Interactions:     toInteractions(message.Interactions),
	}

	if message.System != nil {
		out.System = &domain.SystemMessage{
			Kind:   domain.SystemKind(message.System.Type),
			Target: message.System.ID,
			By:     message.System.By,
			Name:   message.System.Name,
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
// toInteractions carries a message's own rules about reacting to it. Only the
// restricting half is kept: an unrestricted list is a suggestion of quick picks,
// which this client draws nowhere, and a restriction naming nothing is still a
// restriction — it is what refuses every reaction.
func toInteractions(interactions *revoltgo.MessageInteractions) *domain.Interactions {
	if interactions == nil || !interactions.RestrictReactions {
		return nil
	}

	return &domain.Interactions{
		Reactions:         interactions.Reactions,
		RestrictReactions: true,
	}
}

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

// toEmbed converts one embed, reporting nil only for an unfurl that came back
// with nothing to say — no title, no text, no picture and no video — which
// would otherwise draw an empty card under the link.
func toEmbed(embed *revoltgo.MessageEmbed) *domain.Embed {
	if embed == nil {
		return nil
	}

	kind := toEmbedKind(embed.Type)
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

	// A website's unfurl can carry a video beside everything else — a gifbox
	// page is one, its "GIF" being an MP4 and a poster. A *bare* video embed is
	// its URL and nothing more: Revolt puts its dimensions beside the type,
	// where revoltgo has no field for them, so the player's probe answers what
	// the wire did not.
	switch {
	case embed.Video != nil && embed.Video.URL != "":
		out.Video = &domain.File{
			Name:    nameFromURL(embed.Video.URL),
			URL:     embed.Video.URL,
			Kind:    domain.FileVideo,
			Width:   embed.Video.Width,
			Height:  embed.Video.Height,
			Foreign: true,
		}
	case kind == domain.EmbedVideo && out.URL != "":
		out.Video = &domain.File{
			Name: nameFromURL(out.URL), URL: out.URL, Kind: domain.FileVideo, Foreign: true,
		}
	}
	if embed.Special != nil && embed.Special.Type == revoltgo.MessageEmbedSpecialGIF {
		out.GIF = true
	}

	switch {
	case embed.Image != nil:
		out.Image = &domain.File{
			Name:    nameFromURL(embed.Image.URL),
			URL:     embed.Image.URL,
			Kind:    domain.FileImage,
			Width:   embed.Image.Width,
			Height:  embed.Image.Height,
			Foreign: true,
		}
	case embed.Media != nil:
		// An integration can attach anything to its card; a picture and a video
		// are what is drawn.
		switch media := toFile(embed.Media); media.Kind {
		case domain.FileImage:
			out.Image = media
		case domain.FileVideo:
			if out.Video == nil {
				out.Video = media
			}
		}
	case kind == domain.EmbedImage:
		// A bare image embed *is* its URL, and its dimensions sit beside the type
		// where revoltgo has no field for them — so it is drawn against the same
		// placeholder box an attachment with no metadata gets.
		out.Image = &domain.File{
			Name: nameFromURL(embed.URL), URL: embed.URL, Kind: domain.FileImage, Foreign: true,
		}
	}

	if out.Title == "" && out.Description == "" && out.Image == nil && out.Video == nil {
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
	if _, after, ok := strings.CutLast(raw, "/"); ok {
		raw = after
	}

	return raw
}

func toEmbeds(embeds []*revoltgo.MessageEmbed) []*domain.Embed {
	return convertAll(embeds, toEmbed)
}

/* Servers */

func toServer(server *revoltgo.Server) domain.Server {
	out := domain.Server{
		ID:                 server.ID,
		Name:               server.Name,
		OwnerID:            server.Owner,
		Description:        server.Description,
		DefaultPermissions: domain.Permission(server.DefaultPermissions),
		Channels:           server.Channels,
	}

	if server.Icon != nil {
		out.IconID, out.IconURL = server.Icon.ID, server.Icon.URL(iconSize)
	}
	if server.Banner != nil {
		out.BannerURL = server.Banner.URL(bannerSize)
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
		Kind:        toInviteKind(invite.Type),
		ServerID:    invite.ServerID,
		ServerName:  invite.ServerName,
		ChannelID:   invite.ChannelID,
		ChannelName: invite.ChannelName,
		InviterName: invite.UserName,
		MemberCount: int(invite.MemberCount),
	}

	if invite.ServerIcon != nil {
		out.IconURL = invite.ServerIcon.URL(iconSize)
	}
	if invite.ServerBanner != nil {
		out.BannerURL = invite.ServerBanner.URL(bannerSize)
	}

	return out
}

// toInviteKind reads which of the two shapes came back. Server is the default
// because it is what every field beyond the channel pair belongs to: a code
// answering with a type nothing recognises still names a server or it names
// nothing at all.
func toInviteKind(kind revoltgo.InviteType) domain.InviteKind {
	if kind == revoltgo.InviteTypeGroup {
		return domain.InviteGroup
	}

	return domain.InviteServer
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
	case revoltgo.UserRelationshipTypeUser:
		return domain.RelationshipSelf
	case revoltgo.UserRelationshipTypeFriend:
		return domain.RelationshipFriend
	case revoltgo.UserRelationshipTypeOutgoing:
		return domain.RelationshipOutgoing
	case revoltgo.UserRelationshipTypeIncoming:
		return domain.RelationshipIncoming
	case revoltgo.UserRelationshipTypeBlocked:
		return domain.RelationshipBlocked
	case revoltgo.UserRelationshipTypeBlockedOther:
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
// itself, and a value carrying no colour at all is ok=false, leaving the caller
// its own default.
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

// colorStops pulls the colours out of a CSS value, in the order written. Three
// spellings reach it — a hex run, an rgb()/hsl() call and a keyword — and a
// gradient's own words fall out of the same walk, what surrounds its stops being
// a keyword, an angle or a percentage and none of those a colour. A function
// this does not know is stepped *into* rather than over, since that is where a
// gradient keeps them.
func colorStops(s string) []color.Color {
	var stops []color.Color

	for i := 0; i < len(s); {
		switch {
		case s[i] == '#':
			end := i + 1
			for end < len(s) && isHexDigit(s[end]) {
				end++
			}
			if c, ok := parseHexTriple(s[i+1 : end]); ok {
				stops = append(stops, c)
			}
			i = end

		case isNameByte(s[i]):
			end := i
			for end < len(s) && isNameByte(s[end]) {
				end++
			}
			name := strings.ToLower(s[i:end])

			if end < len(s) && s[end] == '(' {
				args, after := callArgs(s, end)
				if c, ok := parseColorFunc(name, args); ok {
					stops = append(stops, c)
					i = after
					continue
				}

				i = end + 1
				continue
			}

			if rgb, ok := namedColors[name]; ok {
				stops = append(stops, color.NRGBA{
					R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255,
				})
			}
			i = end

		default:
			i++
		}
	}

	return stops
}

// isNameByte is what a CSS keyword is made of. The hyphen counts, so
// linear-gradient arrives as one word and cannot be mistaken for a colour named
// after either half of it.
func isNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '-'
}

// callArgs is the text between the bracket at open and its match, plus where the
// scan resumes. An unclosed bracket takes the rest of the value.
func callArgs(s string, open int) (args string, after int) {
	depth := 0

	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], i + 1
			}
		}
	}

	return s[open+1:], len(s)
}

// parseColorFunc reads an rgb() or hsl() call in either spelling CSS accepts:
// comma-separated, or space-separated with the alpha behind a '/'. Both reach
// the same three or four fields, so every separator is read as one.
func parseColorFunc(name, args string) (color.Color, bool) {
	fields := strings.FieldsFunc(args, func(r rune) bool {
		return r == ',' || r == '/' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})

	if len(fields) < 3 || len(fields) > 4 {
		return nil, false
	}

	alpha := 1.0
	if len(fields) == 4 {
		a, ok := parseAlpha(fields[3])
		if !ok {
			return nil, false
		}
		alpha = a
	}

	switch name {
	case "rgb", "rgba":
		r, rOK := parseChannel(fields[0])
		g, gOK := parseChannel(fields[1])
		b, bOK := parseChannel(fields[2])
		if !rOK || !gOK || !bOK {
			return nil, false
		}

		return nrgba(r, g, b, alpha), true

	case "hsl", "hsla":
		h, hOK := parseAngle(fields[0])
		s, sOK := parsePercent(fields[1])
		l, lOK := parsePercent(fields[2])
		if !hOK || !sOK || !lOK {
			return nil, false
		}

		r, g, b := hslToRGB(h, s, l)

		return nrgba(r, g, b, alpha), true
	}

	return nil, false
}

// parseChannel reads one of rgb()'s three as the fraction the arithmetic wants:
// a percentage of full, or a number out of 255.
func parseChannel(field string) (float64, bool) {
	if pct, isPct := strings.CutSuffix(field, "%"); isPct {
		v, err := strconv.ParseFloat(pct, 64)

		return clamp01(v / 100), err == nil
	}

	v, err := strconv.ParseFloat(field, 64)

	return clamp01(v / 255), err == nil
}

// parseAlpha reads the fourth field, which is a fraction already unless it wears
// a '%'.
func parseAlpha(field string) (float64, bool) {
	if pct, isPct := strings.CutSuffix(field, "%"); isPct {
		v, err := strconv.ParseFloat(pct, 64)

		return clamp01(v / 100), err == nil
	}

	v, err := strconv.ParseFloat(field, 64)

	return clamp01(v), err == nil
}

// parsePercent reads hsl()'s saturation and lightness. The '%' is required by
// the legacy spelling and optional in the modern one, so it is simply dropped.
func parsePercent(field string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(field, "%"), 64)

	return clamp01(v / 100), err == nil
}

// parseAngle reads hsl()'s hue in degrees. All four of CSS's angle units reach
// it, and a bare number is degrees.
func parseAngle(field string) (float64, bool) {
	scale := 1.0

	switch {
	case strings.HasSuffix(field, "turn"):
		field, scale = strings.TrimSuffix(field, "turn"), 360
	case strings.HasSuffix(field, "grad"):
		field, scale = strings.TrimSuffix(field, "grad"), 0.9
	case strings.HasSuffix(field, "rad"):
		field, scale = strings.TrimSuffix(field, "rad"), 180/math.Pi
	case strings.HasSuffix(field, "deg"):
		field = strings.TrimSuffix(field, "deg")
	}

	v, err := strconv.ParseFloat(field, 64)

	return v * scale, err == nil
}

// hslToRGB is CSS Color 4's own conversion: hue in degrees, the other two as
// fractions, and the result three more.
func hslToRGB(h, s, l float64) (r, g, b float64) {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	a := s * math.Min(l, 1-l)

	channel := func(n float64) float64 {
		k := math.Mod(n+h/30, 12)

		return l - a*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1))
	}

	return channel(0), channel(8), channel(4)
}

// nrgba rounds four fractions into the colour the rest of the client draws.
func nrgba(r, g, b, a float64) color.NRGBA {
	round := func(v float64) uint8 { return uint8(math.Round(clamp01(v) * 255)) }

	return color.NRGBA{R: round(r), G: round(g), B: round(b), A: round(a)}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// parseHexTriple parses the digits of a hex colour, without their '#'. All four
// CSS lengths are read, the two carrying an alpha included, and nibbles are read
// directly rather than through strconv: every role colour in a server passes
// through here on each Members walk.
func parseHexTriple(hex string) (color.Color, bool) {
	r, g, b, a := 0, 0, 0, 255

	switch len(hex) {
	case 4: // #RGBA, each nibble doubled as below
		a = hexNibble(hex[3]) * 17
		fallthrough
	case 3: // #RGB -> #RRGGBB
		r, g, b = hexNibble(hex[0])*17, hexNibble(hex[1])*17, hexNibble(hex[2])*17
	case 8: // #RRGGBBAA
		a = hexNibble(hex[6])<<4 | hexNibble(hex[7])
		fallthrough
	case 6:
		r = hexNibble(hex[0])<<4 | hexNibble(hex[1])
		g = hexNibble(hex[2])<<4 | hexNibble(hex[3])
		b = hexNibble(hex[4])<<4 | hexNibble(hex[5])
	default:
		return nil, false
	}

	if r < 0 || g < 0 || b < 0 || a < 0 {
		return nil, false
	}

	return color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}, true
}

// hexNibble is a hex digit's value, or -1. A negative propagates through the
// shifts above, so one test covers every digit.
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

// namedColors is CSS's colour keywords, packed as 0xRRGGBB. Revolt's own presets
// are hexes and gradients, but the field is free text and a name typed into
// another client arrives here as one, so the whole list is carried rather than
// the few somebody might guess. 'transparent' and 'currentcolor' are deliberately
// out: both name a colour a role cannot be read against.
var namedColors = map[string]uint32{
	"aliceblue": 0xF0F8FF, "antiquewhite": 0xFAEBD7, "aqua": 0x00FFFF,
	"aquamarine": 0x7FFFD4, "azure": 0xF0FFFF, "beige": 0xF5F5DC,
	"bisque": 0xFFE4C4, "black": 0x000000, "blanchedalmond": 0xFFEBCD,
	"blue": 0x0000FF, "blueviolet": 0x8A2BE2, "brown": 0xA52A2A,
	"burlywood": 0xDEB887, "cadetblue": 0x5F9EA0, "chartreuse": 0x7FFF00,
	"chocolate": 0xD2691E, "coral": 0xFF7F50, "cornflowerblue": 0x6495ED,
	"cornsilk": 0xFFF8DC, "crimson": 0xDC143C, "cyan": 0x00FFFF,
	"darkblue": 0x00008B, "darkcyan": 0x008B8B, "darkgoldenrod": 0xB8860B,
	"darkgray": 0xA9A9A9, "darkgreen": 0x006400, "darkgrey": 0xA9A9A9,
	"darkkhaki": 0xBDB76B, "darkmagenta": 0x8B008B, "darkolivegreen": 0x556B2F,
	"darkorange": 0xFF8C00, "darkorchid": 0x9932CC, "darkred": 0x8B0000,
	"darksalmon": 0xE9967A, "darkseagreen": 0x8FBC8F, "darkslateblue": 0x483D8B,
	"darkslategray": 0x2F4F4F, "darkslategrey": 0x2F4F4F, "darkturquoise": 0x00CED1,
	"darkviolet": 0x9400D3, "deeppink": 0xFF1493, "deepskyblue": 0x00BFFF,
	"dimgray": 0x696969, "dimgrey": 0x696969, "dodgerblue": 0x1E90FF,
	"firebrick": 0xB22222, "floralwhite": 0xFFFAF0, "forestgreen": 0x228B22,
	"fuchsia": 0xFF00FF, "gainsboro": 0xDCDCDC, "ghostwhite": 0xF8F8FF,
	"gold": 0xFFD700, "goldenrod": 0xDAA520, "gray": 0x808080,
	"green": 0x008000, "greenyellow": 0xADFF2F, "grey": 0x808080,
	"honeydew": 0xF0FFF0, "hotpink": 0xFF69B4, "indianred": 0xCD5C5C,
	"indigo": 0x4B0082, "ivory": 0xFFFFF0, "khaki": 0xF0E68C,
	"lavender": 0xE6E6FA, "lavenderblush": 0xFFF0F5, "lawngreen": 0x7CFC00,
	"lemonchiffon": 0xFFFACD, "lightblue": 0xADD8E6, "lightcoral": 0xF08080,
	"lightcyan": 0xE0FFFF, "lightgoldenrodyellow": 0xFAFAD2, "lightgray": 0xD3D3D3,
	"lightgreen": 0x90EE90, "lightgrey": 0xD3D3D3, "lightpink": 0xFFB6C1,
	"lightsalmon": 0xFFA07A, "lightseagreen": 0x20B2AA, "lightskyblue": 0x87CEFA,
	"lightslategray": 0x778899, "lightslategrey": 0x778899, "lightsteelblue": 0xB0C4DE,
	"lightyellow": 0xFFFFE0, "lime": 0x00FF00, "limegreen": 0x32CD32,
	"linen": 0xFAF0E6, "magenta": 0xFF00FF, "maroon": 0x800000,
	"mediumaquamarine": 0x66CDAA, "mediumblue": 0x0000CD, "mediumorchid": 0xBA55D3,
	"mediumpurple": 0x9370DB, "mediumseagreen": 0x3CB371, "mediumslateblue": 0x7B68EE,
	"mediumspringgreen": 0x00FA9A, "mediumturquoise": 0x48D1CC, "mediumvioletred": 0xC71585,
	"midnightblue": 0x191970, "mintcream": 0xF5FFFA, "mistyrose": 0xFFE4E1,
	"moccasin": 0xFFE4B5, "navajowhite": 0xFFDEAD, "navy": 0x000080,
	"oldlace": 0xFDF5E6, "olive": 0x808000, "olivedrab": 0x6B8E23,
	"orange": 0xFFA500, "orangered": 0xFF4500, "orchid": 0xDA70D6,
	"palegoldenrod": 0xEEE8AA, "palegreen": 0x98FB98, "paleturquoise": 0xAFEEEE,
	"palevioletred": 0xDB7093, "papayawhip": 0xFFEFD5, "peachpuff": 0xFFDAB9,
	"peru": 0xCD853F, "pink": 0xFFC0CB, "plum": 0xDDA0DD,
	"powderblue": 0xB0E0E6, "purple": 0x800080, "rebeccapurple": 0x663399,
	"red": 0xFF0000, "rosybrown": 0xBC8F8F, "royalblue": 0x4169E1,
	"saddlebrown": 0x8B4513, "salmon": 0xFA8072, "sandybrown": 0xF4A460,
	"seagreen": 0x2E8B57, "seashell": 0xFFF5EE, "sienna": 0xA0522D,
	"silver": 0xC0C0C0, "skyblue": 0x87CEEB, "slateblue": 0x6A5ACD,
	"slategray": 0x708090, "slategrey": 0x708090, "snow": 0xFFFAFA,
	"springgreen": 0x00FF7F, "steelblue": 0x4682B4, "tan": 0xD2B48C,
	"teal": 0x008080, "thistle": 0xD8BFD8, "tomato": 0xFF6347,
	"turquoise": 0x40E0D0, "violet": 0xEE82EE, "wheat": 0xF5DEB3,
	"white": 0xFFFFFF, "whitesmoke": 0xF5F5F5, "yellow": 0xFFFF00,
	"yellowgreen": 0x9ACD32,
}
