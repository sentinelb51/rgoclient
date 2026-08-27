package cache

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"RGOClient/internal/domain"
)

// page builds an API-order (newest first) page of messages with the given IDs.
func page(ids ...string) []*domain.Message {
	messages := make([]*domain.Message, len(ids))
	for i, id := range ids {
		messages[i] = &domain.Message{ID: id}
	}
	return messages
}

// ids extracts message IDs for easy comparison.
func ids(messages []*domain.Message) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = m.ID
	}
	return out
}

func assertIDs(t *testing.T, got []*domain.Message, want ...string) {
	t.Helper()
	gotIDs := ids(got)
	if len(gotIDs) != len(want) {
		t.Fatalf("got %v, want %v", gotIDs, want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Fatalf("got %v, want %v", gotIDs, want)
		}
	}
}

func TestSetReversesAndCaps(t *testing.T) {
	c := NewMessageCache(3, 5)

	stored := c.Set("ch", page("e", "d", "c", "b", "a"))
	assertIDs(t, stored, "c", "d", "e") // chronological, oldest two dropped
	assertIDs(t, c.Get("ch"), "c", "d", "e")
}

func TestAppendTrimsAndReturnsPrev(t *testing.T) {
	c := NewMessageCache(3, 5)

	if prev := c.Append("ch", &domain.Message{ID: "a"}); prev != nil {
		t.Fatalf("first append: prev = %v, want nil", prev.ID)
	}
	if prev := c.Append("ch", &domain.Message{ID: "b"}); prev == nil || prev.ID != "a" {
		t.Fatalf("second append: prev = %v, want a", prev)
	}
	c.Append("ch", &domain.Message{ID: "c"})
	if prev := c.Append("ch", &domain.Message{ID: "d"}); prev == nil || prev.ID != "c" {
		t.Fatalf("append at cap: prev = %v, want c", prev)
	}
	assertIDs(t, c.Get("ch"), "b", "c", "d") // oldest trimmed
}

func TestPrependOrdersAndTrims(t *testing.T) {
	c := NewMessageCache(4, 5)
	c.Set("ch", page("e", "d"))

	older := c.Prepend("ch", page("c", "b", "a"))
	assertIDs(t, older, "a", "b", "c") // returned chronologically for mounting
	// Cap is 4: the oldest overflow ("a") is dropped from the cache.
	assertIDs(t, c.Get("ch"), "b", "c", "d", "e")
}

func TestChannelLRUEviction(t *testing.T) {
	c := NewMessageCache(10, 2)
	c.Set("a", page("1"))
	c.SetDepleted("a", true)
	c.Set("b", page("2"))
	c.Set("a", page("3")) // touch a so b is now least recently used
	c.Set("c", page("4")) // evicts b

	if got := c.Get("b"); got != nil {
		t.Fatalf("b should be evicted, got %v", ids(got))
	}
	if got := c.Get("a"); got == nil {
		t.Fatal("a should survive eviction")
	}
	if !c.IsDepleted("a") {
		t.Fatal("a's depleted flag should survive eviction of others")
	}

	c.Set("d", page("5")) // evicts a, along with its depleted flag
	if c.IsDepleted("a") {
		t.Fatal("a's depleted flag should be dropped on eviction")
	}
}

func TestSetDoesNotMutateInput(t *testing.T) {
	c := NewMessageCache(10, 2)
	in := page("b", "a")
	c.Set("ch", in)
	if in[0].ID != "b" || in[1].ID != "a" {
		t.Fatalf("input page mutated: %v", ids(in))
	}
}

func TestFindRemoveReplace(t *testing.T) {
	c := NewMessageCache(10, 5)
	c.Set("ch", page("d", "c", "b", "a"))

	if got := c.Find("ch", "c"); got == nil || got.ID != "c" {
		t.Fatalf("Find(c) = %v, want c", got)
	}
	if got := c.Find("ch", "x"); got != nil {
		t.Fatalf("Find(x) = %v, want nil", got.ID)
	}
	if got := c.Find("other", "c"); got != nil {
		t.Fatalf("Find in unknown channel = %v, want nil", got.ID)
	}

	if !c.Replace("ch", &domain.Message{ID: "c", Content: "edited"}) {
		t.Fatal("Replace(c) = false, want true")
	}
	if got := c.Find("ch", "c"); got == nil || got.Content != "edited" {
		t.Fatalf("Find after Replace = %v, want edited content", got)
	}
	if c.Replace("ch", &domain.Message{ID: "x"}) {
		t.Fatal("Replace of uncached message should report false")
	}

	if !c.Remove("ch", "b") {
		t.Fatal("Remove(b) = false, want true")
	}
	assertIDs(t, c.Get("ch"), "a", "c", "d")
	if c.Remove("ch", "b") {
		t.Fatal("second Remove(b) should report false")
	}
}

func TestClear(t *testing.T) {
	c := NewMessageCache(10, 5)
	c.Set("ch", page("a"))
	c.SetDepleted("ch", true)

	c.Clear()
	if c.Get("ch") != nil {
		t.Fatal("Get after Clear should be nil")
	}
	if c.IsDepleted("ch") {
		t.Fatal("depleted flag should not survive Clear")
	}

	// The cache must stay usable after Clear.
	c.Set("ch", page("b"))
	assertIDs(t, c.Get("ch"), "b")
}

func TestEvictionUnderManyChannels(t *testing.T) {
	c := NewMessageCache(5, 3)
	for i := range 10 {
		c.Set(fmt.Sprintf("ch%d", i), page("m"))
	}
	kept := 0
	for i := range 10 {
		if c.Get(fmt.Sprintf("ch%d", i)) != nil {
			kept++
		}
	}
	if kept != 3 {
		t.Fatalf("kept %d channels, want 3", kept)
	}
}

// TestUpdateKeepsEveryConcurrentChange is the one thing Update exists for. A
// gateway echo and a background worker write the same message at the same
// moment; find-then-replace lets both read it, both apply their own change to
// their own copy, and the second store discard the first — silently, since a
// reaction that answered and then was not there looks like nothing at all.
//
// Every writer adds a reaction of its own, so a lost update is a missing one.
func TestUpdateKeepsEveryConcurrentChange(t *testing.T) {
	const writers = 64

	c := NewMessageCache(100, 10)
	c.Set("ch", page("m1"))

	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			emoji := fmt.Sprintf("e%02d", i)
			c.Update("ch", "m1", func(message *domain.Message) bool {
				message.Reactions = append(slices.Clone(message.Reactions),
					domain.Reaction{Emoji: emoji})

				return true
			})
		}()
	}
	wg.Wait()

	if got := len(c.Find("ch", "m1").Reactions); got != writers {
		t.Fatalf("kept %d of %d concurrent changes", got, writers)
	}
}
