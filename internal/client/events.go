package client

// The gateway half. Every handler is registered in register() and does two
// things: keep the message cache in step, and emit one domain value describing
// what happened. Nothing here knows a widget exists.
//
// revoltgo's own default handlers run first and keep State in sync, so these
// only add what State does not cover.

import (
	"log"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

/* The event stream */

// Event is one thing the server told us. The set is closed — the marker method
// is unexported — so a reader's type switch is exhaustive by construction, the
// same idiom markdown.Block and markdown.Inline use.
type Event interface{ isEvent() }

// Ready is the initial gateway snapshot: the account's servers, in the order
// they should appear, and which channels have unread messages. It arrives once
// per session and is the signal that Store can answer for this account.
type Ready struct {
	ServerIDs        []string
	UnreadChannelIDs []string
}

// Disconnected reports the session dropping. Fatal marks the credentials as
// dead rather than the connection as flaky, which is what sends the user back to
// the login screen.
type Disconnected struct {
	Fatal bool
}

// MessageCreated is a new message, already cached.
//
// Previous is the message it landed after, captured under the cache lock:
// several messages can be cached before the reader gets to any of them, so
// deriving the predecessor later would group a burst against the wrong message.
type MessageCreated struct {
	Message  *domain.Message
	Previous *domain.Message
}

// MessageUpdated names a message whose cached copy has been replaced with the
// edited one.
type MessageUpdated struct {
	ChannelID string
	MessageID string
}

// MessageDeleted names messages already dropped from the cache. A moderation
// sweep arrives as one event rather than one per ID.
type MessageDeleted struct {
	ChannelID  string
	MessageIDs []string
}

// ServerJoined is a server the account is now in — joined, or created elsewhere
// and pushed to us.
type ServerJoined struct {
	ServerID string
}

// ServerLeft is a server the account is no longer in, whether they left it, were
// removed from it, or it was deleted outright. The gateway sends the same event
// for all three.
type ServerLeft struct {
	ServerID string
}

// ChannelClosed is a conversation the user closed or a server channel that was
// deleted. Both arrive the same way.
type ChannelClosed struct {
	ChannelID string
}

// MembersChanged reports that a server's membership has changed — someone joined
// or left.
type MembersChanged struct {
	ServerID string
}

// MemberUpdated reports one member's nickname, roles or avatar changing.
type MemberUpdated struct {
	ServerID string
	UserID   string
}

func (Ready) isEvent()          {}
func (Disconnected) isEvent()   {}
func (MessageCreated) isEvent() {}
func (MessageUpdated) isEvent() {}
func (MessageDeleted) isEvent() {}
func (ServerJoined) isEvent()   {}
func (ServerLeft) isEvent()     {}
func (ChannelClosed) isEvent()  {}
func (MembersChanged) isEvent() {}
func (MemberUpdated) isEvent()  {}

/* Registration */

// register wires every gateway handler for one session. epoch is captured by
// each closure so an event produced by a session that has since been replaced is
// dropped rather than delivered — see Client.emit.
func (c *Client) register(session *revoltgo.Session, epoch uint64) {
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventReady) {
		log.Printf("ready: %d user(s), %d server(s)", len(event.Users), len(event.Servers))

		ready := Ready{
			ServerIDs:        make([]string, 0, len(event.Servers)),
			UnreadChannelIDs: make([]string, 0, len(event.ChannelUnreads)),
		}
		for _, server := range event.Servers {
			ready.ServerIDs = append(ready.ServerIDs, server.ID)
		}
		for _, unread := range event.ChannelUnreads {
			ready.UnreadChannelIDs = append(ready.UnreadChannelIDs, unread.ID.Channel)
		}

		c.emit(epoch, ready)
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventError) {
		log.Printf("gateway error: %s", event.Data.Type)

		fatal := event.Data.Type == revoltgo.EventErrorInvalidSession ||
			event.Data.Type == revoltgo.EventErrorInternalError
		if !fatal {
			return
		}

		c.emit(epoch, Disconnected{Fatal: true})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessage) {
		message := toMessage(&event.Message)
		previous := c.messages.Append(event.Channel, message)

		c.emit(epoch, MessageCreated{Message: message, Previous: previous})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageUpdate) {
		current := c.messages.Find(event.Channel, event.ID)
		if current == nil {
			return
		}

		// Cached messages are read without the cache lock, so they stay immutable:
		// the edit goes onto a copy that replaces the original.
		updated := *current
		if event.Data.Content != "" {
			updated.Content = event.Data.Content
		}
		if event.Data.Edited != nil {
			updated.Edited = event.Data.Edited
		}
		c.messages.Replace(event.Channel, &updated)

		c.emit(epoch, MessageUpdated{ChannelID: event.Channel, MessageID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageDelete) {
		c.messages.Remove(event.Channel, event.ID)

		c.emit(epoch, MessageDeleted{ChannelID: event.Channel, MessageIDs: []string{event.ID}})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventBulkMessageDelete) {
		for _, id := range event.IDs {
			c.messages.Remove(event.Channel, id)
		}

		c.emit(epoch, MessageDeleted{ChannelID: event.Channel, MessageIDs: event.IDs})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerCreate) {
		log.Printf("joined server %s", event.ID)

		c.emit(epoch, ServerJoined{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerDelete) {
		log.Printf("left server %s", event.ID)

		c.emit(epoch, ServerLeft{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelDelete) {
		log.Printf("channel %s closed", event.ID)

		c.emit(epoch, ChannelClosed{ChannelID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerMemberJoin) {
		c.emit(epoch, MembersChanged{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerMemberLeave) {
		c.emit(epoch, MembersChanged{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerMemberUpdate) {
		c.emit(epoch, MemberUpdated{ServerID: event.ID.Server, UserID: event.ID.User})
	})
}
