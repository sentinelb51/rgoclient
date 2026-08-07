package domain

import (
	"image/color"
	"testing"
)

func TestFileKindOf(t *testing.T) {
	cases := []struct {
		name string
		want FileKind
	}{
		{"photo.PNG", FileImage}, // uppercase extensions are the same file
		{"clip.webm", FileVideo},
		{"notes.md", FileText},
		{"song.flac", FileAudio},
		{"bundle.tar", FileArchive},
		{"paper.pdf", FilePDF},
		{"README", FileUnknown},    // no extension at all
		{"archive.", FileUnknown},  // a trailing dot is not an extension
		{"thing.qqq", FileUnknown}, // an extension nobody knows
		{"a.b.jpeg", FileImage},    // only the last one counts
		{"", FileUnknown},
	}

	for _, tc := range cases {
		if got := FileKindOf(tc.name); got != tc.want {
			t.Errorf("FileKindOf(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The name comes back apart from the sentence because the client draws it as a
// mention, so what matters is both that the two halves read as one line and that
// an event naming nobody yields no name to make tappable.
func TestSystemMessageTextParts(t *testing.T) {
	cases := []struct {
		kind SystemKind
		who  string
		name string
		rest string
	}{
		{SystemUserJoined, "Elynn", "Elynn", " joined"},
		{SystemUserKicked, "Saren", "Saren", " was kicked"},
		{SystemUserLeft, "", "Someone", " left"},           // unresolved target
		{SystemChannelRenamed, "", "", "Channel renamed"},  // about the channel, nobody to name
		{SystemKind("teleported"), "", "", "System event"}, // a kind added after this client
	}

	for _, tc := range cases {
		system := &SystemMessage{Kind: tc.kind, Target: "01USER"}
		name, rest := system.TextParts(tc.who)
		if name != tc.name || rest != tc.rest {
			t.Errorf("%q.TextParts(%q) = %q + %q, want %q + %q", tc.kind, tc.who, name, rest, tc.name, tc.rest)
		}
	}
}

func TestChannelKindIsConversation(t *testing.T) {
	conversations := []ChannelKind{ChannelDM, ChannelGroup, ChannelSavedMessages}
	for _, kind := range conversations {
		if !kind.IsConversation() {
			t.Errorf("kind %d should be a conversation", kind)
		}
	}

	for _, kind := range []ChannelKind{ChannelText, ChannelVoice} {
		if kind.IsConversation() {
			t.Errorf("kind %d is a server channel, not a conversation", kind)
		}
	}
}

// TestGradientAt covers the segment a sample falls in: the stops are evenly
// spaced, so which pair is mixed and how far between them is arithmetic that can
// be off by one at either end.
func TestGradientAt(t *testing.T) {
	first := color.NRGBA{R: 0xFF, A: 255}
	middle := color.NRGBA{G: 0xFF, A: 255}
	last := color.NRGBA{B: 0xFF, A: 255}
	gradient := Gradient{first, middle, last}

	cases := []struct {
		at   float64
		want color.Color
	}{
		{0, first},
		{0.5, middle},
		{1, last},
		{-1, first}, // out of range clamps rather than wrapping
		{2, last},
		{0.25, color.RGBA{R: 0x7F, G: 0x7F, A: 255}}, // halfway into the first pair
	}

	for _, c := range cases {
		got := gradient.At(c.at)

		r, g, b, a := got.RGBA()
		wantR, wantG, wantB, wantA := c.want.RGBA()
		if r != wantR || g != wantG || b != wantB || a != wantA {
			t.Errorf("At(%v) = %v, want %v", c.at, got, c.want)
		}
	}
}

// A gradient stands in for a single colour wherever one fill is all that can be
// drawn, so it has to answer as the mean of its stops rather than as its first.
func TestGradientAveragesItsStops(t *testing.T) {
	gradient := Gradient{color.NRGBA{R: 0xFF, A: 255}, color.NRGBA{R: 0x00, A: 255}}

	r, _, _, a := gradient.RGBA()
	if want := uint32(0xFFFF / 2); r != want {
		t.Errorf("red = %v, want %v", r, want)
	}
	if a != 0xFFFF {
		t.Errorf("alpha = %v, want opaque", a)
	}
}
