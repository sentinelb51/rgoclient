package app

// The two typing rules a reader cannot check by eye: what the line names, and
// when an entry lapses. Both are pure — the map and the phrase builder need no session,
// no widgets and no clock of their own, which is the whole reason the state
// lives on App rather than behind the store.

import (
	"testing"
	"time"
)

func TestTypingPhrase(t *testing.T) {
	tests := []struct {
		name   string
		names  []string
		hidden int
		self   bool
		want   string
	}{
		{"nobody", nil, 0, false, ""},
		{"one, unnamed", nil, 1, false, "Someone"},
		{"several, none named", nil, 3, false, "3 people"},
		{"one", []string{"Alice"}, 0, false, "Alice"},
		{"two", []string{"Alice", "Bob"}, 0, false, "Alice, Bob"},
		{"three", []string{"Alice", "Bob", "Carol"}, 0, false, "Alice, Bob, Carol"},
		{"one named, one over", []string{"Alice"}, 1, false, "Alice +1"},
		{"three named, four over", []string{"Alice", "Bob", "Carol"}, 4, false, "Alice, Bob, Carol +4"},

		// This account leads however the rest sort.
		{"self alone", nil, 0, true, "You"},
		{"self and one", []string{"Alice"}, 0, true, "You, Alice"},
		{"self and two", []string{"Alice", "Bob"}, 0, true, "You, Alice, Bob"},
		{"self and one over", nil, 1, true, "You +1"},
		{"self, one named, two over", []string{"Alice"}, 2, true, "You, Alice +2"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := typingPhrase(test.names, test.hidden, test.self); got != test.want {
				t.Errorf("typingPhrase(%q, %d, %v) = %q, want %q", test.names, test.hidden, test.self, got, test.want)
			}
		})
	}
}

// TestPruneTyping covers the rule the expiry timer is armed against: an entry is
// alive right up to its moment and gone on it, and a channel that empties leaves
// no map behind.
func TestPruneTyping(t *testing.T) {
	now := time.Now()
	a := &App{typing: map[string]map[string]time.Time{
		"general": {"alice": now.Add(time.Second), "bob": now.Add(time.Minute)},
		"random":  {"carol": now.Add(time.Second)},
	}}

	if changed := a.pruneTyping(now); len(changed) != 0 {
		t.Fatalf("pruned %v before anything lapsed", changed)
	}

	changed := a.pruneTyping(now.Add(time.Second))
	if len(changed) != 2 {
		t.Fatalf("pruned %v, want both channels", changed)
	}

	if _, ok := a.typing["general"]["alice"]; ok {
		t.Error("alice survived her own expiry")
	}
	if _, ok := a.typing["general"]["bob"]; !ok {
		t.Error("bob lapsed a minute early")
	}
	if _, ok := a.typing["random"]; ok {
		t.Error("an emptied channel kept its map")
	}
}

// TestTypistsIn covers the ordering the sentence depends on: a map's own order is
// not one, so a second person starting must not reshuffle the first.
func TestTypistsIn(t *testing.T) {
	a := &App{typing: map[string]map[string]time.Time{
		"general": {"02bob": {}, "01alice": {}, "03carol": {}},
	}}

	got := a.typistsIn("general")
	want := []string{"01alice", "02bob", "03carol"}

	if len(got) != len(want) {
		t.Fatalf("typistsIn = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("typistsIn = %v, want %v", got, want)
		}
	}

	if a.typistsIn("empty") != nil {
		t.Error("a channel nobody types in reported typists")
	}
}
