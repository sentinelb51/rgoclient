package client

// The gateway half. Every handler is registered in register() and does two
// things: keep the message cache in step, and emit one domain value describing
// what happened. Nothing here knows a widget exists.
//
// revoltgo's own default handlers run first and keep State in sync, so these
// only add what State does not cover.

import (
	"log"
	"slices"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

/* The event stream */

// Event is one thing the server told us. The set is closed — the marker method
// is unexported — so a reader's type switch is exhaustive by construction, the
// same idiom markdown.Block and markdown.Inline use.
type Event interface{ isEvent() }

// Ready is the initial gateway snapshot: the account's servers, in the order
// they should appear, which channels have unread messages, and which messages in
// them name the account. It arrives once per session and is the signal that
// Store can answer for this account.
type Ready struct {
	ServerIDs        []string
	UnreadChannelIDs []string

	// MentionIDs is the messages naming this account, by channel and in the order
	// Revolt kept them, which is oldest first. A channel with any is unread by
	// that alone.
	MentionIDs map[string][]string

	// RelatedIDs is everybody this account stands in some relationship to, taken
	// from the `relations` array on its own record — the whole graph, where
	// User.Relationship is one value per account Ready happened to send. An ID here
	// that State cannot name is somebody the friends list would otherwise never
	// show, so the controller fetches them.
	RelatedIDs []string
}

// Disconnected reports the session dropping. Fatal marks the credentials as
// dead rather than the connection as flaky, which is what sends the user back to
// the login screen.
type Disconnected struct {
	Fatal bool
}

// SessionsChanged reports that the account's own logins moved — one revoked from
// another client, or all of them at once. It names nothing: the list is a
// fetched snapshot with no store behind it, so the only thing to do about it is
// ask again.
type SessionsChanged struct{}

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
//
// ReactedBy names who, when the change was a reaction being *added* — the one
// thing about an update a reader might want announcing, and not something the
// reader could work out afterwards: the cache already holds the new state by the
// time this arrives, so an edit and a reaction are otherwise the same event.
// Empty for everything else, a reaction taken off included.
type MessageUpdated struct {
	ChannelID string
	MessageID string

	ReactedBy string

	// Pinning marks an update that moved the pin flag, which is what the pins panel
	// re-asks on. It is read off the wire rather than off the cache: a pinned
	// message is routinely older than anything cached, and for one of those nothing
	// else about this event says a pin was what changed. It says only that a pin
	// moved, never which way — an unpin carries no field at all (see pinnedAfter),
	// so the direction is only knowable for a message the cache holds.
	Pinning bool
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

// EmojisChanged reports the account's custom emoji set gaining or losing one.
//
// It names nothing, and cannot: a delete carries only the emoji's own ID, and
// revoltgo has already dropped it from State by the time this is emitted, so
// there is no server left to look it up from. Ask Store.Emojis afterwards.
type EmojisChanged struct{}

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
//
// MessageID is how far it was read. Revolt drops the mentions up to and
// including it rather than all of them, so a reader that clears the lot would
// disagree with the account's own record the moment a mention arrived out of
// order.
type ChannelRead struct {
	ChannelID string
	MessageID string
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

// RelationshipChanged names somebody this account now stands differently with.
// revoltgo registers no default handler and State's caches are unexported, so
// the client records the new value itself and this only names who it was about.
// Ask Store.User afterwards.
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

// VoiceChanged names a voice channel whose call gained or lost somebody, or
// whose participants changed what they are sharing. revoltgo's own handlers keep
// State's voice cache in step, so this only names the channel — ask
// Store.VoiceParticipants afterwards.
//
// A move arrives as one event naming two channels, so the reader is given both:
// the sidebar redraws whatever a rebuild covers, and neither ID being the open
// channel is still two rows that changed.
type VoiceChanged struct {
	ChannelID string

	// FromChannelID is the call somebody left on their way into ChannelID, "" for
	// anything that is not a move.
	FromChannelID string

	// UserID is who the event was about. "" means this account: the one event
	// that carries no user is a moderator move sent only to its subject. The
	// controller follows its own moves off this, so it must not be dropped.
	UserID string
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
func (EmojisChanged) isEvent()       {}
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
func (VoiceChanged) isEvent()        {}
func (SessionsChanged) isEvent()     {}

/* Registration */

// register wires every gateway handler for one session. epoch is captured by
// each closure so an event produced by a session that has since been replaced is
// dropped rather than delivered — see Client.emit.
func (c *Client) register(session *revoltgo.Session, epoch uint64) {
	c.registerSession(session, epoch)
	c.registerMessages(session, epoch)
	c.registerServers(session, epoch)
	c.registerChannels(session, epoch)
	c.registerMembers(session, epoch)
	c.registerUsers(session, epoch)
	c.registerVoice(session, epoch)
}

// registerSession wires what the connection itself reports: the opening
// snapshot, and the two ways a session ends under it.
func (c *Client) registerSession(session *revoltgo.Session, epoch uint64) {
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventReady) {
		log.Printf("ready: %d user(s), %d server(s)", len(event.Users), len(event.Servers))

		unread, mentions := readState(event)

		ready := Ready{
			ServerIDs:        make([]string, 0, len(event.Servers)),
			UnreadChannelIDs: unread,
			MentionIDs:       mentions,
			RelatedIDs:       c.recordRelations(event),
		}
		for _, server := range event.Servers {
			ready.ServerIDs = append(ready.ServerIDs, server.ID)
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

	// A session revoked from elsewhere — the session manager on another client, or
	// a password change. The token is dead, so this is the same fatal drop a
	// rejected authentication is.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, _ *revoltgo.EventLogout) {
		log.Print("session revoked")

		c.emit(epoch, Disconnected{Fatal: true})
	})

	// The multi-session counterpart, which nothing else announces — revoltgo
	// registers no default handler for it either. It reaches every session of the
	// account, so most of the time it is about somebody else's device and all it
	// means is that a fetched list has gone stale.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventAuth) {
		if c.revokedHere(event) {
			log.Printf("this session revoked (%s)", event.EventType)

			c.emit(epoch, Disconnected{Fatal: true})

			return
		}

		c.emit(epoch, SessionsChanged{})
	})
}

// recordRelations files the whole relationship graph and answers with who is in
// it. The graph is the `relations` array on the account's *own* record, which is
// the only complete statement of it Revolt makes: User.Relationship is filled
// per account, so a relationship is otherwise only known for the people Ready
// happened to send, and somebody befriended long ago whose account nothing has
// touched since is invisible.
//
// Filed into Client.relations rather than read at the point of use, since that
// is what relationshipWith consults first and it survives State never caching
// the account at all. The IDs go back to the controller, which fetches whoever
// cannot be named — a relationship with nobody attached draws no row.
func (c *Client) recordRelations(event *revoltgo.EventReady) []string {
	self := c.selfIn(event)
	if self == nil {
		return nil
	}

	related := make([]string, 0, len(self.Relations))

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, relation := range self.Relations {
		relationship := toRelationship(relation.Status)
		if relation.ID == "" || relation.ID == self.ID || !relationship.Known() {
			continue
		}

		c.relations[relation.ID] = relationship
		related = append(related, relation.ID)
	}

	return related
}

// selfIn finds the account's own record in a Ready. Read out of the event rather
// than out of State: revoltgo files the snapshot from a handler of its own, and
// which of the two runs first is not something to depend on.
//
// By ID where State has already been built, and otherwise by the relations array
// itself — Revolt sends one on the authenticated account's record and on no
// other, so it identifies the record when nothing else here can.
func (c *Client) selfIn(event *revoltgo.EventReady) *revoltgo.User {
	selfID := c.store.SelfID()

	var fallback *revoltgo.User
	for _, user := range event.Users {
		if user == nil {
			continue
		}
		if selfID != "" && user.ID == selfID {
			return user
		}
		if fallback == nil && len(user.Relations) > 0 {
			fallback = user
		}
	}

	return fallback
}

// revokedHere reports whether an Auth event names *this* session. The comparison
// is on the ID and nothing else: where this session's own is unknown — a token
// saved before the client began recording one — there is no way to tell "you
// were signed out" from "somebody else was", and guessing the wrong way is either
// a client that keeps running on a dead token or one that signs itself out for no
// reason. It says nothing instead, and the socket dying is what answers.
func (c *Client) revokedHere(event *revoltgo.EventAuth) bool {
	current := c.SessionID()
	if current == "" {
		return false
	}

	switch event.EventType {
	case revoltgo.EventTypeAuthDeleteSession:
		return event.SessionID == current
	case revoltgo.EventTypeAuthDeleteAllSessions:
		// An empty exclusion is "all of them", this one included.
		return event.ExcludeSessionID != current
	}

	return false
}

// readState derives from a Ready what the account has not caught up with: the
// channels with something new in them, and the messages in each that name it.
//
// EventReady.ChannelUnreads is not either list: it is the account's read
// *markers*, one row per channel it has ever acknowledged, each carrying the last
// message read there and the mentions still standing past it. Taking the rows as
// the unread set inverts the feature — acknowledging a channel is what creates
// its row — so a channel is unread when its newest message is past its marker,
// and one never acknowledged is unread outright. IDs are ULIDs, so newer sorts
// higher and the comparison is lexical.
//
// Both lists are taken from the channels the Ready carries rather than from the
// markers. A marker outlives the channel it is about — leaving a server deletes
// neither, and an ack is the only thing that ever prunes one — so a mention read
// off a row the account no longer has a channel for can never be opened,
// resolved or cleared, and would light the inbox for the rest of the session.
func readState(event *revoltgo.EventReady) (unread []string, mentions map[string][]string) {

	lastRead := make(map[string]string, len(event.ChannelUnreads))
	marked := make(map[string][]string, len(event.ChannelUnreads))

	for _, marker := range event.ChannelUnreads {
		if marker.LastMessageID != nil {
			lastRead[marker.ID.Channel] = *marker.LastMessageID
		}
		if len(marker.MentionIDs) > 0 {
			marked[marker.ID.Channel] = slices.Sorted(slices.Values(marker.MentionIDs))
		}
	}

	mentions = make(map[string][]string, len(marked))
	unread = make([]string, 0, len(event.ChannelUnreads))
	for _, channel := range event.Channels {
		if channel.ChannelType == revoltgo.ChannelTypeSavedMessages {
			continue
		}

		// A mention standing past the marker is unread on its own account: Revolt
		// prunes them as far as the channel was read, so one that is still here names
		// something the account has not seen.
		if ids := marked[channel.ID]; len(ids) > 0 {
			mentions[channel.ID] = ids
			unread = append(unread, channel.ID)
			continue
		}
		if channel.LastMessageID != nil && *channel.LastMessageID > lastRead[channel.ID] {
			unread = append(unread, channel.ID)
		}
	}

	return unread, mentions
}

// registerMessages wires the conversation itself. These are the only handlers
// that write to the message cache; the rest emit and nothing more.
func (c *Client) registerMessages(session *revoltgo.Session, epoch uint64) {
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessage) {
		message := toMessage(&event.Message)
		previous := c.messages.Append(event.Channel, message)

		c.emit(epoch, MessageCreated{Message: message, Previous: previous})
	})

	// EventMessageUpdate.Data is a PartialMessage, so a field left alone and a
	// field emptied are different things here. Three of Revolt's four writers
	// only became readable with that: an edit sends content, edited and embeds
	// together; a pin sends pinned alone; an unpin sends *nothing* and names
	// Pinned in clear; a bulk reaction clear sends an empty map and nothing else
	// (message_pin.rs, message_unpin.rs, message_edit.rs,
	// message_clear_reactions.rs). No writer sends two of those at once, so each
	// arm below stands on its own.
	//
	// The arms split on whether the field can echo a write this client already
	// made. Content, Edited and Embeds arrive only on somebody's edit, so they
	// are taken as sent; Pinned and Reactions echo PinMessage and ClearReactions,
	// which write the cache the moment the server agrees, so they report a change
	// only when there is one — else the chip repaints twice for one tap.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageUpdate) {
		changed := c.reviseMessage(event.Channel, event.ID, func(message *domain.Message) bool {
			moved := false

			if event.Data.Content != nil {
				message.Content = *event.Data.Content
				moved = true
			}
			if event.Data.Edited != nil {
				message.Edited = event.Data.Edited
				moved = true
			}
			if event.Data.Embeds != nil {
				message.Embeds = toEmbeds(event.Data.Embeds)
				moved = true
			}

			// Only ever the bulk clear, and always an empty map, so the count is
			// the whole of the difference. A non-empty one would be a shape this
			// does not model, and is applied as sent rather than merged.
			if event.Data.Reactions != nil {
				if reactions := toReactions(event.Data.Reactions); len(reactions) != len(message.Reactions) {
					message.Reactions = reactions
					moved = true
				}
			}

			if pinned := pinnedAfter(event, message.Pinned); pinned != message.Pinned {
				message.Pinned = pinned
				moved = true
			}

			return moved
		})

		// Whether a *pin* moved is answered off the event rather than off what the
		// cache did with it. A pinned message reaches as far back as anybody cared
		// to keep one, so the pins panel routinely holds messages the cache never
		// had — and for those reviseMessage finds nothing and reports no change, so
		// an unpin made elsewhere would announce nothing at all.
		pinning := event.Data.Pinned != nil || slices.Contains(event.Clear, revoltgo.MessageClearPinned)

		if changed || pinning {
			c.emit(epoch, MessageUpdated{
				ChannelID: event.Channel,
				MessageID: event.ID,
				Pinning:   pinning,
			})
		}
	})

	// A link is unfurled after the message carrying it has been delivered, and the
	// preview arrives as an append rather than an edit. Embeds are the only thing
	// Revolt appends, so that is the whole of this handler.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageAppend) {
		embeds := toEmbeds(event.Append.Embeds)
		if len(embeds) == 0 {
			return
		}

		changed := c.reviseMessage(event.Channel, event.ID, func(message *domain.Message) bool {
			// A new slice, so a reader holding the earlier message sees what it had.
			message.Embeds = slices.Concat(message.Embeds, embeds)

			return true
		})

		if changed {
			c.emit(epoch, MessageUpdated{ChannelID: event.Channel, MessageID: event.ID})
		}
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

	// Both halves are registered because EventMessageUnreact *embeds* the react
	// event rather than aliasing it, so one handler does not answer for the other.
	// ID is the message. Nothing is emitted for a reaction this account already
	// recorded — the echo is what applyReaction reports as nothing moved.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageReact) {
		if c.applyReaction(event.ChannelID, event.ID, event.EmojiID, event.UserID, true) {
			c.emit(epoch, MessageUpdated{ChannelID: event.ChannelID, MessageID: event.ID, ReactedBy: event.UserID})
		}
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageUnreact) {
		if c.applyReaction(event.ChannelID, event.ID, event.EmojiID, event.UserID, false) {
			c.emit(epoch, MessageUpdated{ChannelID: event.ChannelID, MessageID: event.ID})
		}
	})

	// One emoji taken off wholesale, whoever chose it. Taking off *every* reaction
	// is not this event — see Client.ClearReactions.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventMessageRemoveReaction) {
		if c.clearReaction(event.ChannelID, event.ID, event.EmojiID) {
			c.emit(epoch, MessageUpdated{ChannelID: event.ChannelID, MessageID: event.ID})
		}
	})
}

// registerServers wires a server's own details, its roles and its emoji.
func (c *Client) registerServers(session *revoltgo.Session, epoch uint64) {
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

	// Both collapse the same way roles do, and for a stronger reason: what an
	// emoji is worth to a reader is the whole set, which State already holds
	// correctly by the time either of these runs.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, _ *revoltgo.EventEmojiCreate) {
		c.emit(epoch, EmojisChanged{})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, _ *revoltgo.EventEmojiDelete) {
		c.emit(epoch, EmojisChanged{})
	})
}

// registerChannels wires a channel's existence, its read mark and who is
// composing in it — everything about a channel that is not a message in one.
func (c *Client) registerChannels(session *revoltgo.Session, epoch uint64) {
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
		c.emit(epoch, ChannelRead{ChannelID: event.ID, MessageID: event.MessageID})
	})

	// Both halves again — EventChannelStopTyping embeds the start event — and the
	// ID is the channel's. Neither is gated on the setting that draws them:
	// registering nothing would be cheaper, but there is no way to unregister and
	// the setting has to change without a reconnect.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelStartTyping) {
		c.emit(epoch, TypingChanged{ChannelID: event.ID, UserID: event.User, Typing: true})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventChannelStopTyping) {
		c.emit(epoch, TypingChanged{ChannelID: event.ID, UserID: event.User})
	})
}

// registerVoice wires who is in which call. revoltgo tracks the participants
// itself — State's voice cache is seeded from Ready and kept in step by these
// same events — so every handler here does nothing but name the channel that
// moved.
func (c *Client) registerVoice(session *revoltgo.Session, epoch uint64) {
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventVoiceChannelJoin) {
		c.emit(epoch, VoiceChanged{ChannelID: event.ID, UserID: event.State.ID})
	})

	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventVoiceChannelLeave) {
		c.emit(epoch, VoiceChanged{ChannelID: event.ID, UserID: event.User})
	})

	// A move is sent instead of a leave/join pair, so both ends are one event.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventVoiceChannelMove) {
		c.emit(epoch, VoiceChanged{ChannelID: event.To, FromChannelID: event.From, UserID: event.User})
	})

	// A camera or a screen share going on or off. Whether the participant is
	// publishing audio arrives the same way and is not drawn — see
	// domain.VoiceParticipant.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserVoiceStateUpdate) {
		c.emit(epoch, VoiceChanged{ChannelID: event.ChannelID, UserID: event.ID})
	})

	// The account itself being moved by a moderator. Everyone else sees the move
	// above; this session is told separately, and with a voice token it has
	// nothing to do with — the call is not joinable here.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserMoveVoiceChannel) {
		c.emit(epoch, VoiceChanged{ChannelID: event.To, FromChannelID: event.From})
	})
}

// registerMembers wires who belongs where: a server's membership, and a group's
// participants — the two lists the client draws people out of.
func (c *Client) registerMembers(session *revoltgo.Session, epoch uint64) {
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
}

// registerUsers wires the accounts behind those lists: a change to one, and how
// this account stands with it.
func (c *Client) registerUsers(session *revoltgo.Session, epoch uint64) {
	// An account removed from the platform outright. revoltgo's own handler has
	// already dropped the user, their conversations, groups and memberships, so
	// this only names who it was.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserPlatformWipe) {
		log.Printf("user %s removed from the platform", event.UserID)

		c.emit(epoch, UserRemoved{UserID: event.UserID})
	})

	// revoltgo's default handler has already applied the change by the time this
	// runs; all that is decided here is which of the two kinds it was.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserUpdate) {
		presence, identity := userUpdateKinds(event.Data, event.Clear)
		if presence {
			c.emit(epoch, PresenceChanged{UserID: event.ID})
		}
		if identity {
			c.emit(epoch, UserUpdated{UserID: event.ID})
		}
	})

	// Nothing else keeps a relationship current past Ready: without the recording
	// below, a friend added on a phone stays a stranger for the life of the
	// session and a block leaves the composer open on a dead conversation. The ID
	// on the event is *this* account; the user it carries is the other half.
	revoltgo.AddHandler(session, func(_ *revoltgo.Session, event *revoltgo.EventUserRelationship) {
		if event.User == nil {
			return
		}
		c.setRelationship(event.User.ID, toRelationship(event.User.Relationship))

		c.emit(epoch, RelationshipChanged{UserID: event.User.ID})
	})
}

// pinnedAfter is a message's pin state once an update has been applied, given
// what it was before.
//
// The two directions arrive differently. A pin sets the flag; an **unpin** sends
// an empty partial and names Pinned in clear (message_unpin.rs), there being no
// field to carry false — so a client reading Data alone sees an unpin as an
// update that says nothing at all.
func pinnedAfter(event *revoltgo.EventMessageUpdate, pinned bool) bool {
	if slices.Contains(event.Clear, revoltgo.MessageClearPinned) {
		return false
	}

	if event.Data.Pinned != nil {
		return *event.Data.Pinned
	}

	return pinned
}

// userUpdateKinds classifies a partial user update. Both may be true, and two
// events are emitted rather than one carrying a flag: a reader that has to
// remember which bits applied to it will one day read the wrong one.
//
// PartialUser is what makes telling them apart possible — every field nilable,
// Online separate from Status — so a presence change is recognisable without
// diffing against what was there. Clear names what the update *removes*, and a
// cleared avatar or display name is as much a change as a set one.
func userUpdateKinds(data revoltgo.PartialUser, clear []revoltgo.UserRemoveField) (presence, identity bool) {
	// Status is taken as presence whatever moved inside it. It carries the
	// presence and the status line together, and re-reading is cheap where
	// guessing wrong leaves somebody in the wrong section.
	presence = data.Online != nil || data.Status != nil
	identity = data.Username != nil || data.DisplayName != nil ||
		data.Discriminator != nil || data.Avatar != nil || data.Badges != nil

	for _, field := range clear {
		if field == revoltgo.UserRemoveAvatar || field == revoltgo.UserRemoveDisplayName {
			identity = true
		}
	}

	return presence, identity
}
