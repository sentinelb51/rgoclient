package client

import (
	"testing"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// pinFixture is a client holding one cached message in one channel, which is all
// either test below needs to move.
func pinFixture(t *testing.T) (*Client, *domain.Message) {
	t.Helper()

	c := New()
	message := &domain.Message{ID: "01MESSAGE", ChannelID: "01CHANNEL", Content: "hi"}
	c.messages.Set("01CHANNEL", []*domain.Message{message})

	return c, message
}

// pinUpdate is an update event carrying a partial and a clear array.
func pinUpdate(data revoltgo.PartialMessage, clear ...revoltgo.MessageClearType) *revoltgo.EventMessageUpdate {
	return &revoltgo.EventMessageUpdate{ID: "01MESSAGE", Channel: "01CHANNEL", Data: data, Clear: clear}
}

// A pin must reach the cache as a replacement rather than a write, since cached
// messages are read on the UI thread without the cache lock. The original value
// a reader may still be holding has to come through unchanged.
func TestMarkPinnedReplacesRatherThanMutates(t *testing.T) {
	c, original := pinFixture(t)

	if !c.markPinned("01CHANNEL", "01MESSAGE", true) {
		t.Fatal("markPinned reported no change")
	}
	if original.Pinned {
		t.Error("the message a reader was already holding was mutated in place")
	}

	stored := c.messages.Find("01CHANNEL", "01MESSAGE")
	if stored == original {
		t.Fatal("the cache still holds the original message")
	}
	if !stored.Pinned {
		t.Error("the replacement is not pinned")
	}
}

// Nothing moving is reported as such, which is what stops a pin this client made
// itself repainting twice: the action has already written the flag, so the
// gateway's echo of it finds the state it was about to set.
func TestMarkPinnedReportsNoChange(t *testing.T) {
	c, _ := pinFixture(t)

	if c.markPinned("01CHANNEL", "01MESSAGE", false) {
		t.Error("unpinning an unpinned message reported a change")
	}
	if c.markPinned("01CHANNEL", "01MISSING", true) {
		t.Error("pinning a message that is not cached reported a change")
	}
}

// The two directions of a pin do not arrive the same way, and only one of them
// puts anything in Data: an unpin sends an *empty* partial and names Pinned in
// the clear array (message_unpin.rs), there being no field that carries false.
// Reading Data alone therefore sees an unpin as an update that says nothing, and
// the message stays pinned on screen with nothing to suggest why.
func TestPinnedAfter(t *testing.T) {
	pinned, unpinned := true, false

	cases := []struct {
		name  string
		event *revoltgo.EventMessageUpdate
		was   bool
		want  bool
	}{
		{"a pin", pinUpdate(revoltgo.PartialMessage{Pinned: &pinned}), false, true},
		{"an unpin", pinUpdate(revoltgo.PartialMessage{}, revoltgo.MessageClearPinned), true, false},
		{"an unpin spelled as a flag", pinUpdate(revoltgo.PartialMessage{Pinned: &unpinned}), true, false},

		// A content edit mentions neither, and must leave a pinned message pinned.
		{"an edit", pinUpdate(revoltgo.PartialMessage{Content: new(string)}), true, true},
	}

	for _, tc := range cases {
		if got := pinnedAfter(tc.event, tc.was); got != tc.want {
			t.Errorf("%s: pinnedAfter(was %v) = %v, want %v", tc.name, tc.was, got, tc.want)
		}
	}
}

/* Reactions */

// reactionFixture is a client holding one cached message carrying one reaction.
func reactionFixture(t *testing.T) (*Client, *domain.Message) {
	t.Helper()

	c := New()
	message := &domain.Message{
		ID:        "01MESSAGE",
		ChannelID: "01CHANNEL",
		Reactions: []domain.Reaction{{Emoji: "b", Users: []string{"01ALICE"}}},
	}
	c.messages.Set("01CHANNEL", []*domain.Message{message})

	return c, message
}

func storedReactions(c *Client) []domain.Reaction {
	return c.messages.Find("01CHANNEL", "01MESSAGE").Reactions
}

// A reaction must reach the cache as a replacement all the way down: cached
// messages are read on the UI thread without the cache lock, so the message, its
// reaction slice and the user list inside it are all things a reader may still be
// holding when the next event lands.
func TestApplyReactionReplacesRatherThanMutates(t *testing.T) {
	c, original := reactionFixture(t)
	users := original.Reactions[0].Users

	if !c.applyReaction("01CHANNEL", "01MESSAGE", "b", "01BOB", true) {
		t.Fatal("applyReaction reported no change")
	}

	if len(original.Reactions[0].Users) != 1 {
		t.Error("the reaction a reader was already holding gained a user")
	}
	if &users[0] == &storedReactions(c)[0].Users[0] {
		t.Error("the stored reaction shares its user list with the original")
	}
	if got := storedReactions(c)[0].Users; len(got) != 2 || got[1] != "01BOB" {
		t.Errorf("the replacement holds %v, want Alice and Bob", got)
	}
}

// The insertion point matters: a reaction arriving on the gateway has to land
// where a re-fetch of the whole message would have put it, or the chips would
// reorder themselves the next time the channel is opened.
func TestApplyReactionKeepsTheOrder(t *testing.T) {
	c, _ := reactionFixture(t)

	c.applyReaction("01CHANNEL", "01MESSAGE", "c", "01ALICE", true)
	c.applyReaction("01CHANNEL", "01MESSAGE", "a", "01ALICE", true)

	var got []string
	for _, reaction := range storedReactions(c) {
		got = append(got, reaction.Emoji)
	}

	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("reactions are ordered %v, want a, b, c", got)
	}
}

// The last person leaving a reaction takes the chip with them: one reading zero
// is not a chip.
func TestApplyReactionDropsAnEmptyOne(t *testing.T) {
	c, _ := reactionFixture(t)

	if !c.applyReaction("01CHANNEL", "01MESSAGE", "b", "01ALICE", false) {
		t.Fatal("removing the only reaction reported no change")
	}
	if got := storedReactions(c); len(got) != 0 {
		t.Errorf("the message still carries %v", got)
	}
}

// Nothing moving is reported as such, which is what stops a reaction this client
// made itself repainting twice — the action has already written it, so the
// gateway's echo finds the state it was about to set. The same answer covers a
// message that is not cached at all.
func TestApplyReactionReportsNoChange(t *testing.T) {
	c, _ := reactionFixture(t)

	cases := []struct {
		name                 string
		channelID, messageID string
		emoji, userID        string
		add                  bool
	}{
		{"an echo of a reaction already recorded", "01CHANNEL", "01MESSAGE", "b", "01ALICE", true},
		{"removing one nobody made", "01CHANNEL", "01MESSAGE", "z", "01ALICE", false},
		{"removing one this user is not in", "01CHANNEL", "01MESSAGE", "b", "01BOB", false},
		{"a message that is not cached", "01CHANNEL", "01MISSING", "b", "01ALICE", true},
	}

	for _, c2 := range cases {
		if c.applyReaction(c2.channelID, c2.messageID, c2.emoji, c2.userID, c2.add) {
			t.Errorf("%s reported a change", c2.name)
		}
	}
}

// clearReaction is the bulk removal, which takes an emoji off whoever chose it.
func TestClearReaction(t *testing.T) {
	c, _ := reactionFixture(t)
	c.applyReaction("01CHANNEL", "01MESSAGE", "b", "01BOB", true)

	if !c.clearReaction("01CHANNEL", "01MESSAGE", "b") {
		t.Fatal("clearing a reaction two people were in reported no change")
	}
	if got := storedReactions(c); len(got) != 0 {
		t.Errorf("the message still carries %v", got)
	}
	if c.clearReaction("01CHANNEL", "01MESSAGE", "b") {
		t.Error("clearing a reaction that is already gone reported a change")
	}
}

// clearAllReactions is the moderator's clear, and the one write the gateway can
// never correct — the update announcing it cannot be told from an edit, so what
// this leaves is what the client believes until the channel is re-fetched. The
// message a reader is already holding must survive it, as everywhere else here.
func TestClearAllReactions(t *testing.T) {
	c, original := reactionFixture(t)
	c.applyReaction("01CHANNEL", "01MESSAGE", "a", "01BOB", true)

	if !c.clearAllReactions("01CHANNEL", "01MESSAGE") {
		t.Fatal("clearing two reactions reported no change")
	}
	if got := storedReactions(c); len(got) != 0 {
		t.Errorf("the message still carries %v", got)
	}
	if len(original.Reactions) != 1 {
		t.Error("the message a reader was already holding lost its reactions")
	}

	if c.clearAllReactions("01CHANNEL", "01MESSAGE") {
		t.Error("clearing a message that carries none reported a change")
	}
	if c.clearAllReactions("01CHANNEL", "01MISSING") {
		t.Error("clearing a message that is not cached reported a change")
	}
}
