package cache

import (
	"slices"
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// MessageCache holds recently viewed messages per channel for instant channel
// switching. Within a channel, messages are ordered oldest to newest.
//
// The Revolt API returns messages newest first; this cache reverses them on the
// way in and always works on its own copies, so callers may reuse the slices
// they pass in.
type MessageCache struct {
	mu          sync.RWMutex
	byChannel   map[string][]*revoltgo.Message
	depleted    map[string]bool // channels whose full history has been loaded
	recency     []string        // channel IDs, least-recently-used first
	maxMessages int             // cap per channel
	maxChannels int             // cap on cached channels
}

// NewMessageCache creates a cache holding up to maxMessages per channel across
// at most maxChannels channels.
func NewMessageCache(maxMessages, maxChannels int) *MessageCache {
	return &MessageCache{
		byChannel:   make(map[string][]*revoltgo.Message),
		depleted:    make(map[string]bool),
		maxMessages: maxMessages,
		maxChannels: maxChannels,
	}
}

// touch marks a channel as most recently used, evicting the least recently used
// channel if the cache is over capacity. Callers must hold the write lock.
func (c *MessageCache) touch(channelID string) {
	if i := slices.Index(c.recency, channelID); i != -1 {
		c.recency = append(c.recency[:i], c.recency[i+1:]...)
	}
	c.recency = append(c.recency, channelID)

	for len(c.recency) > c.maxChannels {
		evicted := c.recency[0]
		c.recency = c.recency[1:]
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
