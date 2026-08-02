package domain

import "testing"

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

func TestSystemMessageText(t *testing.T) {
	cases := []struct {
		kind SystemKind
		who  string
		want string
	}{
		{SystemUserJoined, "Elynn", "Elynn joined"},
		{SystemUserKicked, "Saren", "Saren was kicked"},
		{SystemUserLeft, "", "Someone left"}, // unresolved target
		{SystemChannelRenamed, "", "Channel renamed"},
		{SystemKind("teleported"), "", "System event"}, // a kind added after this client
	}

	for _, tc := range cases {
		system := &SystemMessage{Kind: tc.kind, Target: "01USER"}
		if got := system.Text(tc.who); got != tc.want {
			t.Errorf("%q.Text(%q) = %q, want %q", tc.kind, tc.who, got, tc.want)
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
