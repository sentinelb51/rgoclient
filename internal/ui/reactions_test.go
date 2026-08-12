package ui

import (
	"testing"

	"fyne.io/fyne/v2"

	"RGOClient/internal/domain"
)

// reactionRecorder answers OnReact and nothing else, so a tap can be read back
// for what it asked for.
type reactionRecorder struct {
	stubActions

	emoji string
	add   bool
	calls int
}

func (r *reactionRecorder) OnReact(_ *domain.Message, emoji string, add bool) {
	r.emoji, r.add, r.calls = emoji, add, r.calls+1
}

// TestReactionChipTogglesAwayFromItsState covers what a chip is for: it draws
// whether this account is in a reaction, and tapping it has to ask for the other
// thing. Getting this backwards is invisible until a click does the opposite of
// what the chip showed.
func TestReactionChipTogglesAwayFromItsState(t *testing.T) {
	deps := styledApp(t)
	store := deps.Store.(*fakeStore)
	store.self = domain.User{ID: "01SELF", Name: "You"}
	store.permissions = domain.PermissionReact

	recorder := &reactionRecorder{}
	deps.Actions = recorder

	message := testMessage("01TESTMESSAGE00000000000A0", "hello")
	message.Reactions = []domain.Reaction{
		{Emoji: "\U0001F44D", Users: []string{"01SELF", "01OTHER"}},
		{Emoji: "\U0001F389", Users: []string{"01OTHER"}},
	}

	chips := reactionChipsIn(buildReactions(deps, message, true, nil, nil))
	if len(chips) != 3 {
		t.Fatalf("built %d chips, want one per reaction plus the one that adds another", len(chips))
	}

	t.Run("a reaction this account is in", func(t *testing.T) {
		if !chips[0].mine {
			t.Error("the chip does not know the account is in it")
		}

		chips[0].Tapped(&fyne.PointEvent{})
		if recorder.emoji != "\U0001F44D" || recorder.add {
			t.Errorf("asked to add %q, want to remove the thumb", recorder.emoji)
		}
	})

	t.Run("a reaction it is not", func(t *testing.T) {
		if chips[1].mine {
			t.Error("the chip claims the account is in a reaction it is not")
		}

		chips[1].Tapped(&fyne.PointEvent{})
		if recorder.emoji != "\U0001F389" || !recorder.add {
			t.Errorf("asked to remove %q, want to add the party popper", recorder.emoji)
		}
	})
}

// A row nobody may react in still says who chose what — that is what it is for —
// but nothing in it answers a click, and it does not offer to add one.
func TestReactionRowWithoutPermission(t *testing.T) {
	deps := styledApp(t)
	recorder := &reactionRecorder{}
	deps.Actions = recorder

	message := testMessage("01TESTMESSAGE00000000000A0", "hello")
	message.Reactions = []domain.Reaction{{Emoji: "\U0001F44D", Users: []string{"01OTHER"}}}

	chips := reactionChipsIn(buildReactions(deps, message, false, nil, nil))
	if len(chips) != 1 {
		t.Fatalf("built %d chips, want the one reaction and nothing to add another with", len(chips))
	}

	chips[0].Tapped(&fyne.PointEvent{})
	if recorder.calls != 0 {
		t.Error("a chip in a channel the account cannot react in answered a tap")
	}

	// A message with neither reactions nor the permission has no row at all.
	if row := buildReactions(deps, testMessage("01TESTMESSAGE00000000000A1", "hi"), false, nil, nil); row != nil {
		t.Error("a message with nothing to show built a row anyway")
	}
}

func reactionChipsIn(row fyne.CanvasObject) []*reactionChip {
	var chips []*reactionChip
	walkTree(row, func(obj fyne.CanvasObject, _ fyne.Position) {
		if chip, ok := obj.(*reactionChip); ok {
			chips = append(chips, chip)
		}
	})

	return chips
}
