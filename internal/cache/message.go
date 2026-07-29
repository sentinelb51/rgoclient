package cache

import (
	"slices"
	"strings"
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// MessageCache holds recently viewed messages per channel for instant channel
// switching. Within a channel, messages are ordered oldest to newest.
//
// The Revolt API returns messages newest first; this cache reverses them on the
// way in and always works on its own copies, so callers may reuse the slices
// they pass in.
//
// Both the *revoltgo.Message values and the slices Get returns are treated as
// immutable once published: the UI thread reads them without holding the lock,
// so mutating writers (Remove, Replace) publish a rebuilt slice instead of
// editing the one readers may still hold. Append is the exception — it only ever
// writes past the end of what any earlier reader can see.
type MessageCache struct {
	mu          sync.RWMutex
	byChannel   map[string][]*revoltgo.Message
	depleted    map[string]bool // channels whose full history has been loaded
	recency     *LRU            // channel IDs by recency
	maxMessages int             // cap per channel
	maxChannels int             // cap on cached channels
}

// NewMessageCache creates a cache holding up to maxMessages per channel across
// at most maxChannels channels.
func NewMessageCache(maxMessages, maxChannels int) *MessageCache {
	return &MessageCache{
		byChannel:   make(map[string][]*revoltgo.Message),
		depleted:    make(map[string]bool),
		recency:     NewLRU(),
		maxMessages: maxMessages,
		maxChannels: maxChannels,
	}
}

// touch marks a channel as most recently used, evicting the least recently used
// channel if the cache is over capacity. Callers must hold the write lock.
func (c *MessageCache) touch(channelID string) {
	c.recency.Touch(channelID)
	for c.recency.Len() > c.maxChannels {
		evicted := c.recency.EvictOldest()
		delete(c.byChannel, evicted)
		delete(c.depleted, evicted)
	}
}

// Get returns the cached messages for a channel (oldest to newest), or nil.
func (c *MessageCache) Get(channelID string) []*revoltgo.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.byChannel[channelID]
}

// CompareMessageID orders a message against an ID. Message IDs are ULIDs,
// whose lexical order is chronological — the same order the cache keeps per
// channel — so it backs the binary searches here and in the app's message
// mounting.
func CompareMessageID(m *revoltgo.Message, id string) int {
	return strings.Compare(m.ID, id)
}

// Find returns the cached message with the given ID, or nil.
func (c *MessageCache) Find(channelID, messageID string) *revoltgo.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	messages := c.byChannel[channelID]
	i, ok := slices.BinarySearchFunc(messages, messageID, CompareMessageID)
	if !ok {
		return nil
	}
	return messages[i]
}

// Remove deletes a message from a channel's cache, reporting whether it was
// present. The channel's slice is rebuilt rather than compacted in place: a
// slice handed out by Get may still be in use on the UI thread (see the
// immutability note on the type), and shifting elements under it would be a
// data race.
func (c *MessageCache) Remove(channelID, messageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	messages := c.byChannel[channelID]
	i, ok := slices.BinarySearchFunc(messages, messageID, CompareMessageID)
	if !ok {
		return false
	}
	c.byChannel[channelID] = slices.Delete(slices.Clone(messages), i, i+1)
	return true
}

// Replace swaps the cached message sharing updated's ID for updated, reporting
// whether it was present. Like Remove, it writes into a fresh slice so readers
// holding an earlier one are unaffected.
func (c *MessageCache) Replace(channelID string, updated *revoltgo.Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	messages := c.byChannel[channelID]
	i, ok := slices.BinarySearchFunc(messages, updated.ID, CompareMessageID)
	if !ok {
		return false
	}
	replaced := slices.Clone(messages)
	replaced[i] = updated
	c.byChannel[channelID] = replaced
	return true
}

// Clear drops every cached channel, used when a session ends so the next login
// (possibly another account) starts clean.
func (c *MessageCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byChannel = make(map[string][]*revoltgo.Message)
	c.depleted = make(map[string]bool)
	c.recency = NewLRU()
}

// Set replaces a channel's messages with an API page (newest first) and returns
// the stored, chronologically ordered slice.
func (c *MessageCache) Set(channelID string, page []*revoltgo.Message) []*revoltgo.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	messages := chronological(page)
	if len(messages) > c.maxMessages {
		messages = messages[len(messages)-c.maxMessages:]
	}

	c.byChannel[channelID] = messages
	c.touch(channelID)
	return messages
}

// Prepend inserts an older API page (newest first) before a channel's existing
// messages, trimming to the per-channel cap by dropping the oldest overflow.
// Scrollback past the cap is served by the network, anchored on the caller's
// oldest mounted message, so trimming here cannot cause refetch loops. The
// page is returned in chronological order for mounting.
func (c *MessageCache) Prepend(channelID string, page []*revoltgo.Message) []*revoltgo.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	older := chronological(page)
	messages := append(older, c.byChannel[channelID]...)
	if len(messages) > c.maxMessages {
		messages = messages[len(messages)-c.maxMessages:]
	}
	c.byChannel[channelID] = messages
	c.touch(channelID)
	return older
}

// Append adds a newly received message to the end of a channel, trimming the
// oldest message if the channel is at capacity. It returns the message that
// preceded the new one (or nil), captured under the same lock so bursts of
// messages still see their true predecessor for grouping.
func (c *MessageCache) Append(channelID string, message *revoltgo.Message) *revoltgo.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	var prev *revoltgo.Message
	if existing := c.byChannel[channelID]; len(existing) > 0 {
		prev = existing[len(existing)-1]
	}

	messages := append(c.byChannel[channelID], message)
	if len(messages) > c.maxMessages {
		messages = messages[1:]
	}
	c.byChannel[channelID] = messages
	c.touch(channelID)
	return prev
}

// IsDepleted reports whether a channel's full history has been loaded.
func (c *MessageCache) IsDepleted(channelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.depleted[channelID]
}

// SetDepleted records whether a channel's full history has been loaded.
func (c *MessageCache) SetDepleted(channelID string, depleted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.depleted[channelID] = depleted
}

// chronological returns a new slice holding page (newest first) reversed to
// oldest first, without mutating the input.
func chronological(page []*revoltgo.Message) []*revoltgo.Message {
	messages := make([]*revoltgo.Message, len(page))
	for i, m := range page {
		messages[len(page)-1-i] = m
	}
	return messages
}
