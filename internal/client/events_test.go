package client

import (
	"testing"

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

// pinEvent is the system message Revolt announces a pin or unpin with. The
// target is the *message* that moved, not a user.
func pinEvent(kind domain.SystemKind, target string) *domain.Message {
	return &domain.Message{
		ID:        "01SYSTEM",
		ChannelID: "01CHANNEL",
		System:    &domain.SystemMessage{Kind: kind, Target: target},
	}
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

// applyPinEvent is the only path that believes anything about pin state, the
// partial update Revolt sends alongside being unreadable for it. So it has to
// act on both directions, name the message that moved rather than the system
// line announcing it, and ignore every other kind of system event.
func TestApplyPinEvent(t *testing.T) {
	t.Run("a pin", func(t *testing.T) {
		c, _ := pinFixture(t)
		c.applyPinEvent(0, pinEvent(domain.SystemMessagePinned, "01MESSAGE"))

		updated, ok := (<-c.events).(MessageUpdated)
		if !ok || updated.MessageID != "01MESSAGE" {
			t.Fatalf("announced %+v, want an update naming the pinned message", updated)
		}
		if !c.messages.Find("01CHANNEL", "01MESSAGE").Pinned {
			t.Error("the message was not pinned")
		}
	})

	t.Run("an unpin", func(t *testing.T) {
		c, _ := pinFixture(t)
		c.markPinned("01CHANNEL", "01MESSAGE", true)

		c.applyPinEvent(0, pinEvent(domain.SystemMessageUnpinned, "01MESSAGE"))
		if c.messages.Find("01CHANNEL", "01MESSAGE").Pinned {
			t.Error("the message is still pinned")
		}
	})

	// Every other system event carries a user in the same field, and acting on one
	// would unpin whatever message happened to share the ID.
	t.Run("another kind of event", func(t *testing.T) {
		c, _ := pinFixture(t)
		c.markPinned("01CHANNEL", "01MESSAGE", true)

		c.applyPinEvent(0, pinEvent(domain.SystemUserJoined, "01MESSAGE"))
		if !c.messages.Find("01CHANNEL", "01MESSAGE").Pinned {
			t.Error("a join event changed a message's pin state")
		}
		select {
		case event := <-c.events:
			t.Errorf("announced %+v, want nothing", event)
		default:
		}
	})

	// An ordinary message is the common case and must not be looked at further.
	t.Run("not a system message", func(t *testing.T) {
		c, _ := pinFixture(t)
		c.applyPinEvent(0, &domain.Message{ID: "01OTHER", ChannelID: "01CHANNEL"})

		select {
		case event := <-c.events:
			t.Errorf("announced %+v, want nothing", event)
		default:
		}
	})
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
