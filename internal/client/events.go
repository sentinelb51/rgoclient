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

// ServerUpdated names a server whose own details changed: its name, its icon,
// its categories or the channels filed under them.
type ServerUpdated struct {
	ServerID string
}

// RolesChanged names a server whose roles moved — one edited, one deleted, or
// the ranks reordered. It does not say which: a role's colour and its rank are
// what the member list sorts and colours by, so any of the three is one walk of
// the membership either way.
type RolesChanged struct {
	ServerID string
}

// ChannelCreated is a channel that now exists for this account: one added to a
// server, or a conversation opened from somewhere else.
type ChannelCreated struct {
	ChannelID string
	ServerID  string // "" for a conversation
}

// ChannelUpdated names a channel whose own details changed — its name, icon,
// description, or the permission overwrites that decide who can see it.
type ChannelUpdated struct {
	ChannelID string
}

// ChannelClosed is a conversation the user closed or a server channel that was
// deleted. Both arrive the same way.
type ChannelClosed struct {
	ChannelID string
}

// ChannelRead names a channel this account acknowledged somewhere else. It is
// the one event that arrives *because* of another client, which is why it exists
// at all: without it a conversation read on a phone stays bold here for the life
// of the session.
type ChannelRead struct {
	ChannelID string
}

// MembersChanged reports that a server's membership has changed — someone joined
// or left. The user is named because a join arrives before anything knows the
// account behind it.
type MembersChanged struct {
	ServerID string
	UserID   string
}

// MemberUpdated reports one member's nickname, roles or avatar changing.
type MemberUpdated struct {
	ServerID string
	UserID   string
}

// RecipientsChanged reports somebody joining or leaving a group conversation.
// It is a server's MembersChanged for the one kind of channel that has a
// membership of its own, and it is named separately because a group has no
// server: the ID is the channel's.
type RecipientsChanged struct {
	ChannelID string
	UserID    string

	Joined bool
}

// UserRemoved is an account taken off the platform. Everything of theirs goes
// with it — their conversations, every group they were in and every membership —
// so unlike UserUpdated there is nothing left to re-read: this names what has
// already stopped existing.
type UserRemoved struct {
	UserID string
}

// UserUpdated names a user whose account changed in a way that is drawn but
// moves nothing: their name, avatar or badges.
//
// Neither this nor PresenceChanged carries the new value. An event names what
// moved and the store answers what things now are, which is the whole contract
// here — and it is what keeps a coalesced burst correct, the last read winning.
type UserUpdated struct {
	UserID string
}

// RelationshipChanged names somebody this account now stands differently with:
// a friend request either way, an unfriending, a block or an unblock.
//
// Like TypingChanged it exists because no store answers for it on its own —
// revoltgo registers no default handler for the event and State's caches are
// unexported, so the client records the new value itself and this only names who
// it was about. Ask Store.User afterwards.
type RelationshipChanged struct {
	UserID string
}

// PresenceChanged names a user who came online, went offline, or changed status.
// It is separate from UserUpdated because it is the one user change that
// *reorders* the member list, and so the one worth coalescing.
type PresenceChanged struct {
	UserID string
}

// TypingChanged names somebody who started or stopped typing in a channel.
//
// It is the one event here that carries its value rather than naming what moved,
// because there is nothing to ask afterwards: revoltgo's State does not model
// typing, so no store answers who is typing where. The reader keeps that itself.
type TypingChanged struct {
	ChannelID string
	UserID    string

	Typing bool
}

func (Ready) isEvent()               {}
func (Disconnected) isEvent()        {}
func (MessageCreated) isEvent()      {}
func (MessageUpdated) isEvent()      {}
func (MessageDeleted) isEvent()      {}
func (ServerJoined) isEvent()        {}
func (ServerLeft) isEvent()          {}
func (ServerUpdated) isEvent()       {}
func (RolesChanged) isEvent()        {}
func (ChannelCreated) isEvent()      {}
func (ChannelUpdated) isEvent()      {}
func (ChannelClosed) isEvent()       {}
func (ChannelRead) isEvent()         {}
func (MembersChanged) isEvent()      {}
func (MemberUpdated) isEvent()       {}
func (RecipientsChanged) isEvent()   {}
func (UserRemoved) isEvent()         {}
func (UserUpdated) isEvent()         {}
func (RelationshipChanged) isEvent() {}
func (PresenceChanged) isEvent()     {}
func (TypingChanged) isEvent()       {}

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
		c.applyPinEvent(epoch, message)
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
		if event.Data.Embeds != nil {
			updated.Embeds = toEmbeds(event.Data.Embeds)
		}
		c.messages.Replace(event.Channel, &updated)

		c.emit(epoch, MessageUpdated{ChannelID: event.Channel, MessageID: event.ID})
	})

	// A link is unfurled after the message carrying it has been delivered, and the
	// preview arrives as an append rather than an edit. Embeds are the only thing
	// Revolt appends, so that is the whole of this handler.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageAppend) {
		embeds := toEmbeds(event.Append.Embeds)
		if len(embeds) == 0 {
			return
		}

		current := c.messages.Find(event.Channel, event.ID)
		if current == nil {
			return
		}

		// As above: the append goes onto a copy, and the slice is a new one, so a
		// reader holding the earlier message keeps seeing what it already had.
		updated := *current
		updated.Embeds = append(append(make([]*domain.Embed, 0, len(current.Embeds)+len(embeds)), current.Embeds...), embeds...)
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

	// Both halves are registered for the reason the typing pair is: EventMessageUnreact
	// *embeds* EventMessageReact rather than aliasing it, so the fields are promoted
	// but one handler does not answer for the other. ID is the message.
	//
	// Nothing is emitted for a reaction this account has already recorded — our own
	// goes into the cache when the server accepts it, so the echo that follows is
	// what applyReaction reports as nothing moved.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageReact) {
		if c.applyReaction(event.ChannelID, event.ID, event.EmojiID, event.UserID, true) {
			c.emit(epoch, MessageUpdated{ChannelID: event.ChannelID, MessageID: event.ID})
		}
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageUnreact) {
		if c.applyReaction(event.ChannelID, event.ID, event.EmojiID, event.UserID, false) {
			c.emit(epoch, MessageUpdated{ChannelID: event.ChannelID, MessageID: event.ID})
		}
	})

	// One emoji taken off a message wholesale, whoever had chosen it. Taking off
	// *every* reaction is not this event: Revolt sends that as a message update
	// carrying an empty map, which cannot be told from an update that never
	// mentioned reactions — see Client.ClearReactions.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageRemoveReaction) {
		if c.clearReaction(event.ChannelID, event.ID, event.EmojiID) {
			c.emit(epoch, MessageUpdated{ChannelID: event.ChannelID, MessageID: event.ID})
		}
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerCreate) {
		log.Printf("joined server %s", event.ID)

		c.emit(epoch, ServerJoined{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerDelete) {
		log.Printf("left server %s", event.ID)

		c.emit(epoch, ServerLeft{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerUpdate) {
		c.emit(epoch, ServerUpdated{ServerID: event.ID})
	})

	// All three role events collapse to one: what a reader does about a role is
	// re-read the members it colours and orders, and that is the same walk whether
	// the role was edited, deleted or merely re-ranked.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerRoleUpdate) {
		c.emit(epoch, RolesChanged{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerRoleDelete) {
		c.emit(epoch, RolesChanged{ServerID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerRoleRanksUpdate) {
		c.emit(epoch, RolesChanged{ServerID: event.ID})
	})

	// The channel is promoted into the event rather than named by it, so unlike
	// every other create this one arrives with everything about it — and revoltgo's
	// own handler has already filed it in State by the time this runs.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelCreate) {
		// Server is nilable because a conversation has none, which is exactly the
		// distinction the reader needs — hence "" rather than a second field.
		var serverID string
		if event.Server != nil {
			serverID = *event.Server
		}

		c.emit(epoch, ChannelCreated{ChannelID: event.ID, ServerID: serverID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelUpdate) {
		c.emit(epoch, ChannelUpdated{ChannelID: event.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelDelete) {
		log.Printf("channel %s closed", event.ID)

		c.emit(epoch, ChannelClosed{ChannelID: event.ID})
	})

	// Revolt sends this to every session of the account, the one that asked
	// included, so it covers a channel read on another client and echoes our own
	// acks back. The reader treats it as "no longer unread" either way, which our
	// own ack has already made true.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelAck) {
		c.emit(epoch, ChannelRead{ChannelID: event.ID})
	})

	// A session revoked from elsewhere — the session manager on another client, or
	// a password change. The token is dead, so this is the same fatal drop a
	// rejected authentication is.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, _ *revoltgo.EventLogout) {
		log.Print("session revoked")

		c.emit(epoch, Disconnected{Fatal: true})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerMemberJoin) {
		c.emit(epoch, MembersChanged{ServerID: event.ID, UserID: event.User})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerMemberLeave) {
		c.emit(epoch, MembersChanged{ServerID: event.ID, UserID: event.User})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventServerMemberUpdate) {
		c.emit(epoch, MemberUpdated{ServerID: event.ID.Server, UserID: event.ID.User})
	})

	// A group's own membership. Both halves are registered because
	// EventChannelGroupLeave *embeds* the join event rather than aliasing it —
	// the third pair in this file to do so — and the ID on either is the channel.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelGroupJoin) {
		c.emit(epoch, RecipientsChanged{ChannelID: event.ID, UserID: event.User, Joined: true})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelGroupLeave) {
		c.emit(epoch, RecipientsChanged{ChannelID: event.ID, UserID: event.User})
	})

	// An account removed from the platform outright. revoltgo's own handler has
	// already dropped the user, every conversation and group they were in, and
	// every membership — ignoring the tracking flags, since the account is gone
	// rather than merely unwatched — so this only names who it was.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserPlatformWipe) {
		log.Printf("user %s removed from the platform", event.UserID)

		c.emit(epoch, UserRemoved{UserID: event.UserID})
	})

	// Nothing subscribed to this before, so somebody's avatar or display name
	// changing stayed invisible for the life of the session — and presence never
	// moved anybody between the member list's sections at all, State's own copy
	// being kept current with nothing to announce it. revoltgo's default handler
	// has already applied the change by the time this runs; all that is decided
	// here is which of the two kinds it was.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserUpdate) {
		presence, identity := userUpdateKinds(event.Data, event.Clear)
		if presence {
			c.emit(epoch, PresenceChanged{UserID: event.ID})
		}
		if identity {
			c.emit(epoch, UserUpdated{UserID: event.ID})
		}
	})

	// A relationship this account is one half of, changed here or anywhere else.
	// Nothing else keeps one current: Ready fills User.Relationship for everybody it
	// names and revoltgo registers no default handler for this, so without the
	// recording below a friend added on a phone stays a stranger here for the life
	// of the session — and a block would leave the composer open on a conversation
	// that can no longer be written to.
	//
	// The ID on the event is *this* account; the user it carries is the other half.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserRelationship) {
		if event.User == nil {
			return
		}
		c.setRelationship(event.User.ID, toRelationship(event.User.Relationship))

		c.emit(epoch, RelationshipChanged{UserID: event.User.ID})
	})

	// Both halves are registered because EventChannelStopTyping *embeds* the start
	// event rather than aliasing it: the fields are promoted, but the handler is
	// keyed on the concrete type and one does not answer for the other. The ID is
	// the channel's.
	//
	// Neither is gated on the setting that draws them. revoltgo drops an event
	// before decoding it when nothing is registered for its type, so registering
	// nothing would be the cheaper answer — but there is no way to unregister
	// afterwards, and the setting has to be able to change without a reconnect.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelStartTyping) {
		c.emit(epoch, TypingChanged{ChannelID: event.ID, UserID: event.User, Typing: true})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelStopTyping) {
		c.emit(epoch, TypingChanged{ChannelID: event.ID, UserID: event.User})
	})
}

// applyPinEvent records a pin or unpin announced by an incoming system message,
// and names the message it moved so a mounted row can redraw. Anything else is a
// no-op, as is a pin of a message no longer cached.
//
// A pin is announced twice over — as this system message, and as a MessageUpdate
// carrying the new flag — and only this one can be believed.
// EventMessageUpdate.Data is a whole Message rather than a partial one, so Pinned
// arrives as a plain bool with no way to tell "now false" from "not mentioned in
// this update": read there, every ordinary content edit would land as an unpin.
// The system event names the message and says which of the two happened, which is
// the whole of what a reader needs.
func (c *Client) applyPinEvent(epoch uint64, message *domain.Message) {
	if message.System == nil {
		return
	}

	target := message.System.PinnedMessageID()
	if target == "" {
		return
	}

	pinned := message.System.Kind == domain.SystemMessagePinned
	if !c.markPinned(message.ChannelID, target, pinned) {
		return
	}

	c.emit(epoch, MessageUpdated{ChannelID: message.ChannelID, MessageID: target})
}

// userUpdateKinds classifies a partial user update. Both may be true, and two
// events are emitted rather than one carrying a flag: a reader that has to
// remember which bits of an event applied to it is a reader that will one day
// read the wrong one.
//
// Telling the two apart at all is what PartialUser makes possible — every field
// is nilable and Online is separate from Status, so a presence change is
// recognisable without comparing the result against what was there before. Clear
// names the fields the update *removes*, and a cleared avatar or display name is
// as much a change as a set one; the rest of what Clear can carry is profile
// text, which nothing mounted draws.
func userUpdateKinds(data revoltgo.PartialUser, clear []string) (presence, identity bool) {
	// Status is taken as presence whatever moved inside it. It carries the
	// presence and the status line together, and re-reading is cheap where
	// guessing wrong leaves somebody in the wrong section.
	presence = data.Online != nil || data.Status != nil
	identity = data.Username != nil || data.DisplayName != nil ||
		data.Discriminator != nil || data.Avatar != nil || data.Badges != nil

	for _, field := range clear {
		if field == "Avatar" || field == "DisplayName" {
			identity = true
		}
	}

	return presence, identity
}
