package cache

import (
	"slices"
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// MessageCache provides an in-memory cache for channel messages.
// Messages are kept in memory for fast access.
type MessageCache struct {
	mutex       sync.RWMutex
	messages    map[string][]*revoltgo.Message // channelID → messages (sorted oldest to newest)
	depleted    map[string]bool                // channelID -> history depleted
	maxMessages int
	maxChannels int
}

// NewMessageCache creates a new message cache with the specified maxMessages per channel.
func NewMessageCache(limitPerChannel, maxChannels int) *MessageCache {
	return &MessageCache{
		messages:    make(map[string][]*revoltgo.Message),
		depleted:    make(map[string]bool),
		maxMessages: limitPerChannel,
		maxChannels: maxChannels,
	}
}

// enforceLimit enforces maxChannels and randomly evicts a channel from cache.
// Call before adding the new channel, and with a Lock held.
func (cache *MessageCache) enforceLimit(newChannelID string) {

	// If channel already exists, ignore
	if _, exists := cache.messages[newChannelID]; exists {
		return
	}

	if len(cache.messages) < cache.maxChannels {
		return
	}

	// Evict one random entry (iteration order is random)
	for key := range cache.messages {
		delete(cache.messages, key)
		delete(cache.depleted, key)
		break
	}
}

// IsDepleted returns true if the channel history is fully loaded.
func (cache *MessageCache) IsDepleted(channelID string) bool {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	return cache.depleted[channelID]
}

// SetDepleted marks the channel history as depleted or not.
func (cache *MessageCache) SetDepleted(channelID string, depleted bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.depleted[channelID] = depleted
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

	cache.enforceLimit(cID)

	// Reverse in place: API returns newest→oldest, store oldest→newest
	slices.Reverse(messages)

	// Trim oldest (front) if over maxMessages
	if len(messages) > cache.maxMessages {
		messages = messages[len(messages)-cache.maxMessages:]
	}
	cache.messages[cID] = messages
}

// Prepend adds messages to the beginning of the channel's cache.
// Used for loading history.
func (cache *MessageCache) Prepend(cID string, messages []*revoltgo.Message) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	cache.enforceLimit(cID)

	// API returns newest→oldest.
	// We want to prepend them such that they remain chronologically before the existing ones.
	// existing: [M100, M101, M102]
	// new (API): [M99, M98]
	// reversed new: [M98, M99]
	// result: [M98, M99, M100, ... ]
	slices.Reverse(messages)

	current := cache.messages[cID]
	cache.messages[cID] = append(messages, current...)
	// We do not trim on prepend to allow history browsing
}

// Append adds a new message to end of channel's cache.
// O(1) amortized - Go slices grow efficiently.
func (cache *MessageCache) Append(channelID string, message *revoltgo.Message) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	cache.enforceLimit(channelID)

	messages := cache.messages[channelID]
	messages = append(messages, message)

	// Trim oldest (front) if over maxMessages
	if len(messages) > cache.maxMessages {
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
