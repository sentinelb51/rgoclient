package cache

import (
	"slices"
	"strings"
	"sync"

	"RGOClient/internal/domain"
)

// MessageCache holds recently viewed messages per channel, oldest to newest, for
// instant channel switching. The API returns messages newest first; the cache
// reverses them on the way in and always stores its own slices.
//
// Both the messages and the slices Get returns are immutable once published: the
// UI thread reads them without the lock, so Remove and Replace publish a rebuilt
// slice rather than editing one a reader may still hold. Append is the exception
// — it only writes past the end of what an earlier reader can see.
type MessageCache struct {
	mu        sync.RWMutex // guards byChannel, depleted and recency
	byChannel map[string][]*domain.Message
	depleted  map[string]bool // channels whose full history has been loaded
	recency   *LRU            // channel IDs by recency

	maxMessages int // cap per channel
	maxChannels int // cap on cached channels
}

// NewMessageCache creates a cache holding up to maxMessages per channel across
// at most maxChannels channels.
func NewMessageCache(maxMessages, maxChannels int) *MessageCache {
	return &MessageCache{
		byChannel:   make(map[string][]*domain.Message),
		depleted:    make(map[string]bool),
		recency:     newLRU(),
		maxMessages: maxMessages,
		maxChannels: maxChannels,
	}
}

// CompareMessageID orders a message against an ID. Message IDs are ULIDs, whose
// lexical order is chronological — the order the cache keeps per channel — so
// this backs the binary searches here and in the app's message mounting.
func CompareMessageID(m *domain.Message, id string) int {
	return strings.Compare(m.ID, id)
}

// touch marks a channel most recently used, evicting the least recently used
// past capacity. Callers must hold the write lock.
func (c *MessageCache) touch(channelID string) {
	c.recency.Touch(channelID)

	for c.recency.Len() > c.maxChannels {
		evicted := c.recency.EvictOldest()
		delete(c.byChannel, evicted)
		delete(c.depleted, evicted)
	}
}

// Get returns a channel's cached messages, oldest to newest, or nil.
func (c *MessageCache) Get(channelID string) []*domain.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.byChannel[channelID]
}

// Find returns the cached message with the given ID, or nil.
func (c *MessageCache) Find(channelID, messageID string) *domain.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	messages := c.byChannel[channelID]
	i, ok := slices.BinarySearchFunc(messages, messageID, CompareMessageID)
	if !ok {
		return nil
	}

	return messages[i]
}

// Set replaces a channel's messages with an API page (newest first) and returns
// the stored, chronologically ordered slice.
func (c *MessageCache) Set(channelID string, page []*domain.Message) []*domain.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	messages := c.trimmed(chronological(page))

	c.byChannel[channelID] = messages
	c.touch(channelID)

	return messages
}

// Prepend inserts an older API page (newest first) before a channel's existing
// messages, dropping the oldest overflow past the cap. Scrollback past the cap
// is served from the network, anchored on the caller's oldest mounted message,
// so trimming here cannot cause refetch loops. The page comes back in
// chronological order for mounting.
func (c *MessageCache) Prepend(channelID string, page []*domain.Message) []*domain.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	older := chronological(page)

	c.byChannel[channelID] = c.trimmed(slices.Concat(older, c.byChannel[channelID]))
	c.touch(channelID)

	return older
}

// Append adds a newly received message to the end of a channel, trimming the
// oldest at capacity. It returns the message that preceded the new one, captured
// under the same lock so bursts still see their true predecessor for grouping.
func (c *MessageCache) Append(channelID string, message *domain.Message) *domain.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	var prev *domain.Message
	if existing := c.byChannel[channelID]; len(existing) > 0 {
		prev = existing[len(existing)-1]
	}

	c.byChannel[channelID] = c.trimmed(append(c.byChannel[channelID], message))
	c.touch(channelID)

	return prev
}

// trimmed drops the oldest messages past the per-channel cap. It copies rather
// than re-slicing: a sub-slice keeps the whole backing array alive, so the
// dropped prefix would stay reachable and the channel would hold about twice the
// cap. The copy is what releases it, and is paid once per message past the cap.
func (c *MessageCache) trimmed(messages []*domain.Message) []*domain.Message {
	if len(messages) <= c.maxMessages {
		return messages
	}

	return slices.Clone(messages[len(messages)-c.maxMessages:])
}

// Remove deletes a message from a channel, reporting whether it was present. The
// slice is rebuilt rather than compacted in place: a slice handed out by Get may
// still be in use on the UI thread, and shifting under it would be a data race.
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
// whether it was present. Like Remove it writes into a fresh slice.
func (c *MessageCache) Replace(channelID string, updated *domain.Message) bool {
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

// Update applies change to a copy of the cached message and stores it, reporting
// whether anything moved — a change reporting false, and a message the cache
// does not hold, are both nothing rather than a write.
//
// The search, the copy, the change and the store are one held lock. Find then
// Replace is the same work across two, and between them a gateway echo and a
// background worker each read the same message, each apply their own change to
// their own copy, and the second store silently discards the first — a reaction
// or a pin that answered and then was not there.
func (c *MessageCache) Update(channelID, messageID string, change func(*domain.Message) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	messages := c.byChannel[channelID]
	i, ok := slices.BinarySearchFunc(messages, messageID, CompareMessageID)
	if !ok {
		return false
	}

	updated := *messages[i]
	if !change(&updated) {
		return false
	}

	revised := slices.Clone(messages)
	revised[i] = &updated
	c.byChannel[channelID] = revised

	return true
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

// Clear drops every cached channel, so the next login starts clean.
func (c *MessageCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byChannel = make(map[string][]*domain.Message)
	c.depleted = make(map[string]bool)
	c.recency = newLRU()
}

// chronological reverses an API page (newest first) into a new oldest-first
// slice, leaving the input untouched.
func chronological(page []*domain.Message) []*domain.Message {
	messages := slices.Clone(page)
	slices.Reverse(messages)

	return messages
}
