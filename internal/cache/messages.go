package cache

import (
	"slices"
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// MessageCache provides an in-memory cache for channel messages.
// Messages are kept in memory for fast access.
type MessageCache struct {
	mutex    sync.RWMutex
	messages map[string][]*revoltgo.Message // channelID → messages (sorted oldest to newest)
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
func (cache *MessageCache) Get(channelID string) []*revoltgo.Message {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	return cache.messages[channelID]
}

// Set stores messages for a channel (replaces existing).
// Reverses API response (newest→oldest) to store as oldest→newest.
func (cache *MessageCache) Set(cID string, messages []*revoltgo.Message) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	// Reverse in place: API returns newest→oldest, store oldest→newest
	slices.Reverse(messages)

	// Trim oldest (front) if over limit
	if len(messages) > cache.limit {
		messages = messages[len(messages)-cache.limit:]
	}
	cache.messages[cID] = messages
}

// Append adds a new message to end of channel's cache.
// O(1) amortized - Go slices grow efficiently.
func (cache *MessageCache) Append(channelID string, message *revoltgo.Message) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	messages := cache.messages[channelID]
	messages = append(messages, message)

	// Trim oldest (front) if over limit
	if len(messages) > cache.limit {
		messages = messages[1:]
	}
	cache.messages[channelID] = messages
}

// Clear removes all messages for a channel.
func (cache *MessageCache) Clear(channelID string) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	delete(cache.messages, channelID)
}
