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
// mention, so what matters is that the two halves read as one line, that an event
// naming nobody yields no name to make tappable, and that an actor Revolt did not
// send leaves the sentence impersonal rather than blaming Someone.
func TestSystemMessageTextParts(t *testing.T) {
	cases := []struct {
		kind   SystemKind
		who    string
		by     string
		byID   string
		rename string
		name   string
		rest   string
	}{
		{kind: SystemUserJoined, who: "Elynn", name: "Elynn", rest: " joined"},
		{kind: SystemUserKicked, who: "Saren", name: "Saren", rest: " was kicked"},
		{kind: SystemUserLeft, name: "Someone", rest: " left"}, // unresolved target

		// Revolt sends no actor for a kick or a ban, so neither can name one.
		{kind: SystemUserKicked, who: "Saren", by: "Elynn", name: "Saren", rest: " was kicked"},

		// An actor at the end of a sentence its subject leads.
		{kind: SystemUserAdded, who: "Saren", by: "Elynn", byID: "01BY", name: "Saren", rest: " added to group by Elynn"},
		{kind: SystemUserAdded, who: "Saren", byID: "01BY", name: "Saren", rest: " added to group by Someone"},
		{kind: SystemUserAdded, who: "Saren", name: "Saren", rest: " added to group"},

		// An actor leading one of its own, with and without the new name.
		{kind: SystemChannelRenamed, by: "Elynn", byID: "01BY", rename: "general", name: "Elynn", rest: " renamed the channel to general"},
		{kind: SystemChannelRenamed, by: "Elynn", byID: "01BY", name: "Elynn", rest: " renamed the channel"},
		{kind: SystemChannelRenamed, rename: "general", rest: "Channel renamed to general"}, // no actor sent
		{kind: SystemMessagePinned, by: "Elynn", byID: "01BY", name: "Elynn", rest: " pinned a message"},

		{kind: SystemKind("teleported"), rest: "System event"}, // a kind added after this client
	}

	for _, tc := range cases {
		system := &SystemMessage{Kind: tc.kind, Target: "01USER", By: tc.byID, Name: tc.rename}
		name, rest := system.TextParts(tc.who, tc.by)
		if name != tc.name || rest != tc.rest {
			t.Errorf("%q.TextParts(%q, %q) = %q + %q, want %q + %q",
				tc.kind, tc.who, tc.by, name, rest, tc.name, tc.rest)
		}
	}
}

// Revolt files every system event's subject under one "id", so the field alone
// does not say what kind of thing is in it. Reading a pin's as a user is a fetch
// the server can only refuse — and one that came back on every remount, a failed
// author fetch being deliberately retryable.
func TestSystemMessageTargetKind(t *testing.T) {
	cases := []struct {
		kind   SystemKind
		target string
		user   bool
	}{
		{SystemUserJoined, "01USER", true},
		{SystemUserKicked, "01USER", true},
		{SystemMessagePinned, "01MESSAGE", false},
		{SystemMessageUnpinned, "01MESSAGE", false},
		{SystemChannelRenamed, "", false},          // names nothing at all
		{SystemKind("teleported"), "01USER", true}, // a kind added after this client
	}

	for _, tc := range cases {
		system := &SystemMessage{Kind: tc.kind, Target: tc.target}
		if got := system.TargetsUser(); got != tc.user {
			t.Errorf("%q.TargetsUser() = %v, want %v", tc.kind, got, tc.user)
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
