package main

import (
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// MessageCache provides an in-memory cache for channel messages.
// Messages are kept in memory for fast access.
type MessageCache struct {
	mu       sync.RWMutex
	messages map[string][]*revoltgo.Message // channelID -> messages (sorted oldest to newest)
	limit    int                            // max messages per channel
}

// NewMessageCache creates a new message cache with the specified limit per channel.
func NewMessageCache(limitPerChannel int) *MessageCache {
	return &MessageCache{
		messages: make(map[string][]*revoltgo.Message),
		limit:    limitPerChannel,
	}
}

// Get returns messages for a channel from memory.
func (mc *MessageCache) Get(channelID string) []*revoltgo.Message {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.messages[channelID]
}

// Set stores messages for a channel (replaces existing).
func (mc *MessageCache) Set(channelID string, messages []*revoltgo.Message) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Ensure we don't exceed the limit
	if len(messages) > mc.limit {
		messages = messages[len(messages)-mc.limit:]
	}
	mc.messages[channelID] = messages
}

// Append adds a new message to a channel's cache.
func (mc *MessageCache) Append(channelID string, message *revoltgo.Message) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	msgs := mc.messages[channelID]
	msgs = append(msgs, message)

	// Trim if over limit
	if len(msgs) > mc.limit {
		msgs = msgs[len(msgs)-mc.limit:]
	}
	mc.messages[channelID] = msgs
}

// Clear removes all messages for a channel.
func (mc *MessageCache) Clear(channelID string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	delete(mc.messages, channelID)
}
