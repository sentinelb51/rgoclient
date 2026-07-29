package app

import (
	"log"
	"time"

	"github.com/sentinelb51/revoltgo"
)

// ackDelay is the coalescing window for read acknowledgements of the open
// channel: a burst of incoming messages produces one MessageAck for the newest
// of them instead of one request per message.
const ackDelay = time.Second

// onMessage caches an incoming message and either appends it to the open
// channel (acknowledging it as read) or marks the channel unread.
func (a *App) onMessage(_ *revoltgo.Session, event *revoltgo.EventMessage) {
	// Capture the predecessor at cache time: under a burst, several messages can
	// be cached before their UI callbacks run, so deriving it later would group
	// against the wrong message.
	prev := a.messageCache.Append(event.Channel, &event.Message)

	a.doOnUI(func() {
		if event.Channel == a.currentChannelID {
			// The author is resolved by newMessageWidget on the way in.
			a.appendMessage(&event.Message, prev)
			a.scheduleAck(event.Channel, event.Message.ID)
			return
		}
		a.unreadChannels[event.Channel] = true
		a.refreshChannelRow(event.Channel)
	}, false)
}

// onMessageUpdate applies an edit to the cached message and rebuilds its
// mounted widget. Cached messages are immutable (the UI reads them without the
// cache lock), so the edit goes onto a copy that replaces the original.
func (a *App) onMessageUpdate(_ *revoltgo.Session, event *revoltgo.EventMessageUpdate) {
	current := a.messageCache.Find(event.Channel, event.ID)
	if current == nil {
		return
	}

	updated := *current
	if event.Data.Content != "" {
		updated.Content = event.Data.Content
	}
	if event.Data.Edited != nil {
		updated.Edited = event.Data.Edited
	}
	if len(event.Data.Embeds) > 0 {
		updated.Embeds = event.Data.Embeds
	}
	a.messageCache.Replace(event.Channel, &updated)

	a.doOnUI(func() { a.refreshMessage(event.Channel, event.ID) }, false)
}

// onMessageDelete drops a deleted message from the cache and unmounts its
// widget when the channel is open.
func (a *App) onMessageDelete(_ *revoltgo.Session, event *revoltgo.EventMessageDelete) {
	a.messageCache.Remove(event.Channel, event.ID)
	a.doOnUI(func() { a.removeMessage(event.Channel, event.ID) }, false)
}

// onBulkMessageDelete handles moderation sweeps deleting several messages at
// once.
func (a *App) onBulkMessageDelete(_ *revoltgo.Session, event *revoltgo.EventBulkMessageDelete) {
	for _, id := range event.IDs {
		a.messageCache.Remove(event.Channel, id)
	}
	a.doOnUI(func() {
		for _, id := range event.IDs {
			a.removeMessage(event.Channel, id)
		}
	}, false)
}

// scheduleAck records messageID as the newest seen message of channelID and
// acknowledges it after ackDelay, coalescing bursts into a single request. An
// ack still pending for a *different* channel (the user switched channels
// inside the window) is flushed immediately so it isn't lost to the overwrite.
// Call on the UI thread.
func (a *App) scheduleAck(channelID, messageID string) {
	if a.ackTimer != nil && a.ackChannelID != channelID {
		a.ackTimer.Stop()
		a.ackTimer = nil
		a.sendAck(a.ackChannelID, a.ackMessageID)
	}

	a.ackChannelID, a.ackMessageID = channelID, messageID
	if a.ackTimer != nil {
		return
	}
	a.ackTimer = time.AfterFunc(ackDelay, func() {
		a.doOnUI(func() {
			a.ackTimer = nil
			a.sendAck(a.ackChannelID, a.ackMessageID)
		}, false)
	})
}

// sendAck fires a MessageAck request in the background. Call on the UI thread.
func (a *App) sendAck(channelID, messageID string) {
	session := a.session
	if session == nil {
		return
	}
	go func() {
		if err := session.MessageAck(channelID, messageID); err != nil {
			log.Printf("ack channel %s: %v", channelID, err)
		}
	}()
}
