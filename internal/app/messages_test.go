package app

import (
	"testing"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

func TestSortConversations(t *testing.T) {
	channels := []domain.Channel{
		{ID: "quiet", Kind: domain.ChannelDM, Active: true}, // never used
		{ID: "old", Kind: domain.ChannelDM, Active: true, LastMessageID: "01AAA"},
		{ID: "closed", Kind: domain.ChannelDM, LastMessageID: "01ZZZ"},   // inactive: dropped
		{ID: "group", Kind: domain.ChannelGroup, LastMessageID: "01MMM"}, // groups have no Active flag
		{ID: "recent", Kind: domain.ChannelDM, Active: true, LastMessageID: "01YYY"},
	}

	got := sortConversations(channels)
	want := []string{"recent", "group", "old", "quiet"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (newest first, empty conversations last)", got, want)
		}
	}
}

// A jump mounts a window around its target, and the one thing it cannot get
// wrong is losing the target out of it — at either end, where the window is
// clamped, and at every span the settings allow including zero.
func TestWindowAround(t *testing.T) {
	const n = 10

	for _, span := range []int{0, 1, 4, 25} {
		for i := range n {
			from, to := windowAround(n, i, span)

			switch {
			case from < 0 || to > n || from >= to:
				t.Fatalf("span %d, target %d: window [%d:%d] is not inside [0:%d]", span, i, from, to, n)
			case i < from || i >= to:
				t.Fatalf("span %d: window [%d:%d] does not contain the target", span, from, to)
			case to-from > 2*span+1:
				t.Fatalf("span %d, target %d: window [%d:%d] is wider than asked for", span, i, from, to)
			}
		}
	}
}

func TestToReplies(t *testing.T) {
	pending := []ui.Reply{{ID: "01A", ChannelID: "01CH", Mention: true}, {ID: "01B", ChannelID: "01CH"}}

	replies := toReplies(pending)
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	if replies[0].ID != "01A" || !replies[0].Mention {
		t.Errorf("first reply = %+v, want 01A with a mention", replies[0])
	}
	if replies[1].ID != "01B" || replies[1].Mention {
		t.Errorf("second reply = %+v, want 01B without one", replies[1])
	}
}
