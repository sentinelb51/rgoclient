package cache

import (
	"fmt"
	"testing"

	"github.com/sentinelb51/revoltgo"
)

// page builds an API-order (newest first) page of messages with the given IDs.
func page(ids ...string) []*revoltgo.Message {
	messages := make([]*revoltgo.Message, len(ids))
	for i, id := range ids {
		messages[i] = &revoltgo.Message{ID: id}
	}
	return messages
}

// ids extracts message IDs for easy comparison.
func ids(messages []*revoltgo.Message) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = m.ID
	}
	return out
}

func assertIDs(t *testing.T, got []*revoltgo.Message, want ...string) {
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

	if prev := c.Append("ch", &revoltgo.Message{ID: "a"}); prev != nil {
		t.Fatalf("first append: prev = %v, want nil", prev.ID)
	}
	if prev := c.Append("ch", &revoltgo.Message{ID: "b"}); prev == nil || prev.ID != "a" {
		t.Fatalf("second append: prev = %v, want a", prev)
	}
	c.Append("ch", &revoltgo.Message{ID: "c"})
	if prev := c.Append("ch", &revoltgo.Message{ID: "d"}); prev == nil || prev.ID != "c" {
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
