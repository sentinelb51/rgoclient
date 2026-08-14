package client

import (
	"image/color"
	"testing"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// TestToEmbedDropsWhatCannotBeDrawn covers the two embeds that reach the UI as
// nothing: a bare video, whose dimensions Revolt puts beside the type where
// revoltgo has no field for them and which nothing here could play anyway, and
// an unfurl that came back with neither text nor a picture. Both would otherwise
// draw an empty card under the link that produced them.
func TestToEmbedDropsWhatCannotBeDrawn(t *testing.T) {
	tests := []struct {
		name  string
		embed *revoltgo.MessageEmbed
	}{
		{"nil", nil},
		{"video", &revoltgo.MessageEmbed{Type: "Video", URL: "https://example.test/clip.mp4"}},
		{"nothing unfurled", &revoltgo.MessageEmbed{Type: "Website", SiteName: "example.test"}},
		{"non-image media", &revoltgo.MessageEmbed{
			Type:  "Text",
			Media: &revoltgo.File{ID: "aFile", Filename: "notes.pdf"},
		}},
	}

	for _, tt := range tests {
		if got := toEmbed(tt.embed); got != nil {
			t.Errorf("%s: got %+v, want nil", tt.name, got)
		}
	}
}

func TestToEmbedWebsite(t *testing.T) {
	got := toEmbed(&revoltgo.MessageEmbed{
		Type:        "Website",
		URL:         "https://example.test/article",
		SiteName:    "Example",
		Title:       "An article",
		Description: "What it **says**",
		IconURL:     "https://example.test/favicon.ico",
		Colour:      "#5B7CFA",
		Image: &revoltgo.MessageEmbedImage{
			URL:    "https://example.test/hero.png?w=1200",
			Width:  1200,
			Height: 630,
		},
	})

	if got == nil {
		t.Fatal("the unfurl was dropped")
	}
	if got.Kind != domain.EmbedWebsite {
		t.Errorf("kind = %v, want EmbedWebsite", got.Kind)
	}
	if got.Description != "What it **says**" {
		t.Errorf("description = %q, want the markdown source unchanged", got.Description)
	}
	if got.Color != (color.NRGBA{R: 0x5B, G: 0x7C, B: 0xFA, A: 255}) {
		t.Errorf("colour = %v, want the parsed accent", got.Color)
	}

	if got.Image == nil {
		t.Fatal("the preview image was dropped")
	}
	if got.Image.Kind != domain.FileImage {
		t.Errorf("image kind = %v, want FileImage", got.Image.Kind)
	}
	if got.Image.Width != 1200 || got.Image.Height != 630 {
		t.Errorf("image is %dx%d, want 1200x630", got.Image.Width, got.Image.Height)
	}
	// The lightbox titles itself with the name, and a preview has none of its own.
	if got.Image.Name != "hero.png" {
		t.Errorf("image name = %q, want the last path segment without the query", got.Image.Name)
	}
}

// TestToEmbedImageIsItsOwnURL covers the bare image kind, where the picture is
// the embed rather than something hanging off it, and no dimensions survive the
// wire type — so the renderer has to fall back to a placeholder box.
func TestToEmbedImageIsItsOwnURL(t *testing.T) {
	got := toEmbed(&revoltgo.MessageEmbed{Type: "Image", URL: "https://example.test/photo.jpg"})

	if got == nil {
		t.Fatal("the image embed was dropped")
	}
	if got.Image == nil {
		t.Fatal("no picture: an image embed is nothing else")
	}
	if got.Image.URL != "https://example.test/photo.jpg" {
		t.Errorf("image URL = %q, want the embed's own", got.Image.URL)
	}
	if got.Image.Width != 0 || got.Image.Height != 0 {
		t.Errorf("image is %dx%d, want no dimensions at all", got.Image.Width, got.Image.Height)
	}
}

// TestToEmbedTextFallsBackToOriginalURL covers an integration's own card: its
// media is the picture, and where it names no url the one it was resolved from
// is what the title leads to.
func TestToEmbedTextFallsBackToOriginalURL(t *testing.T) {
	got := toEmbed(&revoltgo.MessageEmbed{
		Type:        "Text",
		OriginalURL: "https://example.test/source",
		Title:       "About Embeds",
		Description: "A card an integration composed",
		Media:       &revoltgo.File{ID: "anImage", Filename: "banner.png"},
	})

	if got == nil {
		t.Fatal("the card was dropped")
	}
	if got.URL != "https://example.test/source" {
		t.Errorf("URL = %q, want the original", got.URL)
	}
	if got.Image == nil || got.Image.Name != "banner.png" {
		t.Errorf("image = %+v, want the attached media", got.Image)
	}
	if got.Color != nil {
		t.Errorf("colour = %v, want none — the card named one it could not parse", got.Color)
	}
}

// TestToEmbedsSkipsTheUndrawable checks the filtering happens on the way into
// the slice, so nothing above the boundary ever holds a nil embed.
func TestToEmbedsSkipsTheUndrawable(t *testing.T) {
	got := toEmbeds([]*revoltgo.MessageEmbed{
		{Type: "Video", URL: "https://example.test/clip.mp4"},
		{Type: "Website", Title: "Kept"},
		nil,
	})

	if len(got) != 1 {
		t.Fatalf("got %d embeds, want 1", len(got))
	}
	if got[0].Title != "Kept" {
		t.Errorf("kept %q, want the website embed", got[0].Title)
	}
}

// TestUserUpdateKindsSeparatesPresenceFromIdentity covers the classification the
// member sidebar's whole event budget rests on. A presence change reorders the
// list and is coalesced; a rename repaints one row. Getting the two the wrong way
// round means either a rebuild per status change on a thousand-member server, or
// somebody who never moves out of Offline.
func TestUserUpdateKindsSeparatesPresenceFromIdentity(t *testing.T) {
	online, name := true, "Ada"

	tests := []struct {
		label              string
		data               revoltgo.PartialUser
		clear              []string
		presence, identity bool
	}{
		{"coming online", revoltgo.PartialUser{Online: &online}, nil, true, false},
		{"a status", revoltgo.PartialUser{Status: &revoltgo.UserStatus{Text: "afk"}}, nil, true, false},
		{"a rename", revoltgo.PartialUser{DisplayName: &name}, nil, false, true},
		{"both at once", revoltgo.PartialUser{Online: &online, DisplayName: &name}, nil, true, true},
		{"a cleared avatar", revoltgo.PartialUser{}, []string{"Avatar"}, false, true},
		{"profile text nothing draws", revoltgo.PartialUser{}, []string{"ProfileContent"}, false, false},
		{"nothing at all", revoltgo.PartialUser{}, nil, false, false},
	}

	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			presence, identity := userUpdateKinds(test.data, test.clear)
			if presence != test.presence || identity != test.identity {
				t.Errorf("got (presence=%v, identity=%v), want (%v, %v)",
					presence, identity, test.presence, test.identity)
			}
		})
	}
}

// TestToReactionsIsOrdered covers the one thing the wire format cannot supply.
// Reactions arrive as a JSON object, so revoltgo hands over a map: rendered as
// it iterates, the chips would come out in a different order on every repaint.
// The order also has to survive a count changing, or somebody joining a reaction
// would move the chip beside it out from under the pointer.
func TestToReactionsIsOrdered(t *testing.T) {
	reactions := map[string][]string{
		"\U0001F44D":                 {"01ALICE", "01BOB"},
		"01J9WN3PHX4ZQSNSZH10CK4RHS": {"01BOB"},
		"\U0001F389":                 {"01CAROL"},
	}

	var first []domain.Reaction
	for range 8 {
		got := toReactions(reactions)

		if first == nil {
			first = got
			continue
		}
		for i := range got {
			if got[i].Emoji != first[i].Emoji {
				t.Fatalf("two conversions of one map disagree at %d: %q then %q", i, first[i].Emoji, got[i].Emoji)
			}
		}
	}

	if len(first) != len(reactions) {
		t.Fatalf("converted %d reactions, want %d", len(first), len(reactions))
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Emoji >= first[i].Emoji {
			t.Errorf("%q is filed before %q", first[i-1].Emoji, first[i].Emoji)
		}
	}

	if toReactions(nil) != nil {
		t.Error("a message with no reactions converted to a slice")
	}
}

// A voice channel no longer says so in its type: Stoat dropped the VoiceChannel
// variant and a voice channel now arrives as a TextChannel carrying a `voice`
// object. Reading the type alone files every one of them under text, which is a
// wrong answer that looks like a right one — the channel works, it just stops
// being recognisable.
func TestToChannelKindReadsVoiceOffTheChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel *revoltgo.Channel
		want    domain.ChannelKind
	}{
		{"text", &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeText}, domain.ChannelText},
		{"voice by field", &revoltgo.Channel{
			ChannelType: revoltgo.ChannelTypeText,
			Voice:       &revoltgo.ChannelVoiceInformation{},
		}, domain.ChannelVoice},
		{"voice by type", &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeVoice}, domain.ChannelVoice},
		{"dm", &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeDM}, domain.ChannelDM},
		{"group", &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeGroup}, domain.ChannelGroup},
		{"notes", &revoltgo.Channel{ChannelType: revoltgo.ChannelTypeSavedMessages}, domain.ChannelSavedMessages},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := toChannelKind(test.channel); got != test.want {
				t.Errorf("kind is %v, want %v", got, test.want)
			}
		})
	}
}
