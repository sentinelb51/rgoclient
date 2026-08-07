package app

// The grouping and ordering rules, exercised as the pure functions they are.
// None of this was reachable from a test before internal/client took the session
// away: every one of these used to take a *revoltgo.Message, and building the
// State they resolved against is not possible from outside that package.

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// at builds a message ID whose ULID timestamp is t — which is where every
// grouping and day decision below actually comes from, messages carrying no
// timestamp of their own.
func at(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), ulid.DefaultEntropy()).String()
}

// message is a plain message by author, sent at t.
func message(author string, t time.Time) *domain.Message {
	return &domain.Message{ID: at(t), ChannelID: "01CHANNEL", AuthorID: author, Content: "hi"}
}

func TestContinuesGroup(t *testing.T) {
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.Local)
	prev := message("01ELYNN", base)
	window := messageGroupWindow()

	cases := []struct {
		name string
		curr *domain.Message
		want bool
	}{
		{"same author, moments later", message("01ELYNN", base.Add(time.Minute)), true},
		{"same author, at the window's edge", message("01ELYNN", base.Add(window)), true},
		{"same author, past the window", message("01ELYNN", base.Add(window+time.Minute)), false},
		{"a different author", message("01SAREN", base.Add(time.Minute)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := continuesGroup(prev, tc.curr); got != tc.want {
				t.Errorf("continuesGroup = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("nothing above", func(t *testing.T) {
		if continuesGroup(nil, message("01ELYNN", base)) {
			t.Error("a message with no predecessor cannot continue a group")
		}
	})

	// A reply always opens a group: it carries a quote above it, which a headerless
	// continuation has nowhere to put.
	t.Run("a reply", func(t *testing.T) {
		reply := message("01ELYNN", base.Add(time.Minute))
		reply.Replies = []string{"01OTHER"}
		if continuesGroup(prev, reply) {
			t.Error("a reply should start its own group")
		}
	})

	// Neither side may be anything but a person: a system line, a webhook post and
	// a masquerade are all somebody else wearing the author's ID.
	t.Run("not a person", func(t *testing.T) {
		system := message("01ELYNN", base.Add(time.Minute))
		system.System = &domain.SystemMessage{Kind: domain.SystemUserJoined}

		webhook := message("01ELYNN", base.Add(time.Minute))
		webhook.Webhook = &domain.Webhook{Name: "CI"}

		masked := message("01ELYNN", base.Add(time.Minute))
		masked.Masquerade = true

		for name, curr := range map[string]*domain.Message{
			"system": system, "webhook": webhook, "masquerade": masked,
		} {
			if continuesGroup(prev, curr) {
				t.Errorf("%s should start its own group", name)
			}
			if continuesGroup(curr, message("01ELYNN", base.Add(2*time.Minute))) {
				t.Errorf("nothing should continue a %s", name)
			}
		}
	})

	// Midnight breaks the group even inside the time window, because the day
	// separator is drawn between the two and a headerless block cannot straddle it.
	t.Run("across midnight", func(t *testing.T) {
		before := message("01ELYNN", time.Date(2026, 7, 14, 23, 59, 0, 0, time.Local))
		after := message("01ELYNN", time.Date(2026, 7, 15, 0, 1, 0, 0, time.Local))
		if continuesGroup(before, after) {
			t.Error("a day separator must break the group")
		}
	})
}

func TestDayLabel(t *testing.T) {
	noon := time.Date(2026, 7, 14, 12, 0, 0, 0, time.Local)
	first := message("01ELYNN", noon)

	if dayLabel(nil, first) == "" {
		t.Error("a message with no predecessor opens its day and must be dated")
	}
	if got := dayLabel(first, message("01ELYNN", noon.Add(time.Hour))); got != "" {
		t.Errorf("same day got the label %q, want none", got)
	}

	next := message("01ELYNN", time.Date(2026, 7, 15, 9, 0, 0, 0, time.Local))
	if dayLabel(first, next) == "" {
		t.Error("a new calendar day must be dated")
	}
}

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
