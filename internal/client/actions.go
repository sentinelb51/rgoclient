package client

// Every network action the user can take. Each one blocks, does the request and
// whatever cache update belongs with it, and returns — no goroutines, no
// callbacks, nothing that touches a widget. The caller owns the UI thread and so
// decides when to leave it.

import (
	"errors"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// ErrBusy reports that the same request is already in flight, so this one was
// not made. Rapid channel switching is what raises it.
var ErrBusy = errors.New("request already in flight")

// resolveWorkers bounds how many users are fetched at once, so a channel full of
// unseen people — or a long list of conversations — doesn't open dozens of
// connections.
const resolveWorkers = 4

// AvatarURL builds the URL an avatar is served from. It exists for the saved
// login cards, which persist an avatar ID from a session that no longer exists
// and so cannot ask the store for one.
func AvatarURL(avatarID string) string {
	if avatarID == "" {
		return ""
	}

	return revoltgo.EndpointAutumnFile("avatars", avatarID, avatarSize)
}

/* Messages */

// SendMessage uploads the composer's attachments and sends the message. An
// attachment that fails to open or upload is logged and skipped, so one bad file
// doesn't sink the whole message. The sent message reaches the UI through the
// gateway echo, not from here.
func (c *Client) SendMessage(channelID, content string, attachments []domain.Attachment, replies []domain.Reply) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	send := revoltgo.MessageSend{
		Content:     content,
		Attachments: uploadAttachments(session, attachments),
		Replies:     toMessageReplies(replies),
	}
	_, err := session.ChannelMessageSend(channelID, send)

	return err
}

func uploadAttachments(session *revoltgo.Session, attachments []domain.Attachment) []string {
	ids := make([]string, 0, len(attachments))

	for _, attachment := range attachments {
		file, err := os.Open(attachment.Path)
		if err != nil {
			log.Printf("open attachment %s: %v", attachment.Path, err)
			continue
		}

		uploaded, err := session.AttachmentUpload(&revoltgo.FileParams{Name: attachment.Name, Reader: file})
		_ = file.Close()
		if err != nil {
			log.Printf("upload attachment %s: %v", attachment.Name, err)
			continue
		}
		ids = append(ids, uploaded.ID)
	}

	return ids
}

func toMessageReplies(replies []domain.Reply) []*revoltgo.MessageReplies {
	out := make([]*revoltgo.MessageReplies, len(replies))
	for i, reply := range replies {
		out[i] = &revoltgo.MessageReplies{ID: reply.ID, Mention: reply.Mention}
	}

	return out
}

// EditMessage sends an edit. It deliberately leaves the cache alone: the caller
// applies the change optimistically and reverts on failure, because only the
// caller knows what is mounted. The authoritative version arrives as
// MessageUpdated.
func (c *Client) EditMessage(channelID, messageID, content string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	_, err := session.ChannelMessageEdit(channelID, messageID, revoltgo.MessageEditParams{Content: content})

	return err
}

// DeleteMessage deletes a message. Nothing is removed here — the message leaves
// the cache and the view through MessageDeleted, so a rejected delete leaves the
// client exactly as it was.
func (c *Client) DeleteMessage(channelID, messageID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ChannelMessageDelete(channelID, messageID)
}

// PinMessage pins or unpins a message and records the result, so the menu that
// raised it reads the other way round immediately.
//
// The cache update is done here rather than left to the gateway because the
// event Revolt sends alongside cannot carry the answer: see markPinned and the
// note in events.go. A rejected request leaves the flag exactly as it was, this
// being written only once the server has agreed.
func (c *Client) PinMessage(channelID, messageID string, pinned bool) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	pin := session.ChannelMessagePin
	if !pinned {
		pin = session.ChannelMessageUnpin
	}
	if err := pin(channelID, messageID); err != nil {
		return err
	}
	c.markPinned(channelID, messageID, pinned)

	return nil
}

// markPinned writes a message's pin state into the cache, reporting whether
// anything moved — an unpin of a message already known to be unpinned is what a
// caller drops rather than announcing.
//
// The entry is replaced with a copy: cached messages are read without the cache
// lock, so they stay immutable.
func (c *Client) markPinned(channelID, messageID string, pinned bool) bool {
	current := c.messages.Find(channelID, messageID)
	if current == nil || current.Pinned == pinned {
		return false
	}

	updated := *current
	updated.Pinned = pinned

	return c.messages.Replace(channelID, &updated)
}

// React adds or removes this account's reaction to a message. Which of the two
// is asked for rather than toggled: the chip that raised it has already read the
// state to draw itself, and re-deriving it here could only disagree.
//
// The cache is written once the server has agreed, exactly as a pin is, and for
// a related reason: the gateway does echo a reaction back, but nothing here may
// depend on that. A chip the user has just clicked has to answer immediately,
// and applyReaction reports "nothing moved" when the echo lands on what this
// already holds, so the round trip costs one repaint rather than two.
func (c *Client) React(channelID, messageID, emoji string, add bool) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	self := session.State.Self()
	if self == nil {
		return ErrNoSession
	}

	react := session.ChannelMessageReactionCreate
	if !add {
		react = session.ChannelMessageReactionDelete
	}
	if err := react(channelID, messageID, emoji); err != nil {
		return err
	}
	c.applyReaction(channelID, messageID, emoji, self.ID, add)

	return nil
}

// applyReaction records one person joining or leaving one reaction, reporting
// whether anything moved — a caller drops what did not rather than announcing it.
//
// The entry, its reaction slice and the user list inside it are all replaced
// rather than written into: cached messages are read without the cache lock, so
// everything reachable from one stays immutable.
func (c *Client) applyReaction(channelID, messageID, emoji, userID string, add bool) bool {
	current := c.messages.Find(channelID, messageID)
	if current == nil {
		return false
	}

	index := slices.IndexFunc(current.Reactions, func(r domain.Reaction) bool { return r.Emoji == emoji })

	var reactions []domain.Reaction
	switch {
	case index == -1 && !add:
		return false

	case index == -1:
		// A reaction nobody had chosen yet, filed where toReactions would have put it.
		reactions = slices.Insert(slices.Clone(current.Reactions), sortedIndex(current.Reactions, emoji),
			domain.Reaction{Emoji: emoji, Users: []string{userID}})

	default:
		users, changed := withUser(current.Reactions[index].Users, userID, add)
		if !changed {
			return false
		}

		reactions = slices.Clone(current.Reactions)
		if len(users) == 0 {
			// The last person left it: a chip reading zero is not a chip.
			reactions = slices.Delete(reactions, index, index+1)
		} else {
			reactions[index].Users = users
		}
	}

	updated := *current
	updated.Reactions = reactions

	return c.messages.Replace(channelID, &updated)
}

// clearReaction drops one emoji from a message entirely, for the bulk removal a
// moderator makes.
func (c *Client) clearReaction(channelID, messageID, emoji string) bool {
	current := c.messages.Find(channelID, messageID)
	if current == nil {
		return false
	}

	index := slices.IndexFunc(current.Reactions, func(r domain.Reaction) bool { return r.Emoji == emoji })
	if index == -1 {
		return false
	}

	updated := *current
	updated.Reactions = slices.Delete(slices.Clone(current.Reactions), index, index+1)

	return c.messages.Replace(channelID, &updated)
}

// withUser adds or removes one user from a reaction's list, reporting whether it
// had to. The list is copied only when it changes, so an echo of something
// already recorded allocates nothing.
func withUser(users []string, userID string, add bool) ([]string, bool) {
	index := slices.Index(users, userID)

	switch {
	case add && index != -1, !add && index == -1:
		return users, false
	case add:
		return append(slices.Clone(users), userID), true
	default:
		return slices.Delete(slices.Clone(users), index, index+1), true
	}
}

// sortedIndex is where emoji belongs in an already-sorted reaction slice, so a
// reaction arriving on the gateway lands where a re-fetch of the whole message
// would have put it — see toReactions.
func sortedIndex(reactions []domain.Reaction, emoji string) int {
	index, _ := slices.BinarySearchFunc(reactions, emoji,
		func(r domain.Reaction, want string) int { return strings.Compare(r.Emoji, want) })

	return index
}

// AckMessage marks a channel read up to a message.
func (c *Client) AckMessage(channelID, messageID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.MessageAck(channelID, messageID)
}

// AckServer marks every channel of a server read in one request.
func (c *Client) AckServer(serverID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServerAck(serverID)
}

/* Typing */

// BeginTyping announces this account as typing in a channel, and EndTyping takes
// it back. Both are websocket frames rather than requests: they cost a write, no
// rate limiter sees them, and there is nothing to wait for.
//
// The socket is the reason for the second guard. A session is published before
// its websocket is opened, and closing one leaves the field pointing at a socket
// that has gone — so Session.WS is nil in the window either side of a login, and
// revoltgo writes through it without looking.
func (c *Client) BeginTyping(channelID string) error {
	session := c.session.Load()
	if session == nil || session.WS == nil {
		return ErrNoSession
	}

	return session.ChannelBeginTyping(channelID)
}

func (c *Client) EndTyping(channelID string) error {
	session := c.session.Load()
	if session == nil || session.WS == nil {
		return ErrNoSession
	}

	return session.ChannelEndTyping(channelID)
}

/* Slowmode */

// FetchSlowmode reads a channel's send cooldown and records it, so the store can
// answer for it afterwards without going back to the network.
//
// It is the one action that goes round revoltgo's typed API. Revolt carries
// slowmode on a text channel and announces a changed one through ChannelUpdate,
// but revoltgo models neither field — so the number never arrives with the
// channel and nothing ever says it moved. Asking for the raw channel is the only
// route to it. When revoltgo grows the field this becomes a line in store.go and
// the request goes away.
func (c *Client) FetchSlowmode(channelID string) (time.Duration, error) {
	session := c.session.Load()
	if session == nil {
		return 0, ErrNoSession
	}

	var channel struct {
		Slowmode int `json:"slowmode"`
	}
	if err := session.HTTP.Request(http.MethodGet, revoltgo.EndpointChannel(channelID), nil, &channel); err != nil {
		return 0, err
	}
	slowmode := time.Duration(channel.Slowmode) * time.Second

	c.mu.Lock()
	c.slowmode[channelID] = slowmode
	c.mu.Unlock()

	return slowmode, nil
}

// slowmodeOf is a channel's recorded cooldown, or 0 when it has none or has not
// been asked about. Safe from any goroutine — the store reads it.
func (c *Client) slowmodeOf(channelID string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.slowmode[channelID]
}

/* History */

// LatestMessages fetches the newest page of a channel into the cache and reports
// how many messages came back. An empty channel marks itself depleted, so
// scrolling up never asks again.
//
// Concurrent calls for the same channel return ErrBusy: rapid switching used to
// fire one request per switch with nothing deduplicating them, unlike the
// scrollback path, which has always guarded.
func (c *Client) LatestMessages(channelID string, limit int) (int, error) {
	session := c.session.Load()
	if session == nil {
		return 0, ErrNoSession
	}
	if !c.claim(c.fetching, channelID) {
		return 0, ErrBusy
	}
	defer c.release(c.fetching, channelID)

	c.messages.SetDepleted(channelID, false)

	page, err := session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
		IncludeUsers: true,
		Limit:        limit,
	})
	if err != nil {
		return 0, err
	}
	if len(page.Messages) == 0 {
		c.messages.SetDepleted(channelID, true)
		return 0, nil
	}

	c.messages.Set(channelID, toMessages(page.Messages))

	return len(page.Messages), nil
}

// HistoryBefore fetches an older page and prepends it to the cache, returning
// what the cache actually took. An empty page marks the channel depleted.
func (c *Client) HistoryBefore(channelID, beforeID string, limit int) ([]*domain.Message, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}
	if !c.claim(c.fetching, channelID) {
		return nil, ErrBusy
	}
	defer c.release(c.fetching, channelID)

	page, err := session.ChannelMessages(channelID, revoltgo.ChannelMessagesParams{
		Before:       beforeID,
		Limit:        limit,
		IncludeUsers: true,
	})
	if err != nil {
		return nil, err
	}
	if len(page.Messages) == 0 {
		c.messages.SetDepleted(channelID, true)
		return nil, nil
	}

	return c.messages.Prepend(channelID, toMessages(page.Messages)), nil
}

// claim reserves key in one of the in-flight guards, reporting false when another
// request already holds it; release gives it back. They are what turns a
// superseded request into ErrBusy — revoltgo's REST layer takes no context, so a
// request cannot be cancelled, only not made twice.
//
// The guard is passed in rather than named because the two of them key different
// things — channels and servers — and Revolt does not promise an ID is unique
// across both. Reading the field to pass it is safe without the lock: the maps
// are built once in New and only ever cleared, never replaced.
func (c *Client) claim(guard map[string]bool, key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if guard[key] {
		return false
	}
	guard[key] = true

	return true
}

func (c *Client) release(guard map[string]bool, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(guard, key)
}

// inParallel runs fn over every item, at most resolveWorkers at a time, and
// returns once they have all finished. A slot is taken before the goroutine
// starts rather than inside it, so the fan-out is bounded by the semaphore and
// not merely throttled by it.
func inParallel[T any](items []T, fn func(T)) {
	var wg sync.WaitGroup

	slots := make(chan struct{}, resolveWorkers)
	for _, item := range items {
		wg.Add(1)
		slots <- struct{}{}

		go func() {
			defer func() { <-slots; wg.Done() }()
			fn(item)
		}()
	}
	wg.Wait()
}

/* Authors */

// AuthorRef identifies one author to resolve: the user, plus the server whose
// member record carries their nickname and role colour ("" in a conversation).
type AuthorRef struct {
	ServerID string
	UserID   string
}

// AuthorResolution is what one batch came back with. Failed refs are handed back
// so the caller can drop its guard and let a later message retry.
//
// It does not report whether a *member* record in particular was fetched. It used
// to, so the caller could skip rebuilding the member sidebar for a pure user
// fetch — but resolving the account behind an already-cached membership is what
// gives that membership a name, so the sidebar and its mention candidates change
// either way.
type AuthorResolution struct {
	Resolved []string
	Failed   []AuthorRef
}

// ResolveAuthors pulls a batch of message authors into State, bounded by
// resolveWorkers. It is the lazy counterpart to a bulk member fetch: Revolt's
// members endpoint has no pagination, so pulling every member of a large server
// would flood memory.
func (c *Client) ResolveAuthors(targets []AuthorRef) AuthorResolution {
	session := c.session.Load()
	if session == nil || len(targets) == 0 {
		return AuthorResolution{Failed: targets}
	}

	var (
		mu     sync.Mutex
		result AuthorResolution
	)

	inParallel(targets, func(target AuthorRef) {
		ok := resolveAuthor(session, target)

		mu.Lock()
		defer mu.Unlock()

		if ok {
			result.Resolved = append(result.Resolved, target.UserID)
		} else {
			result.Failed = append(result.Failed, target)
		}
	})

	return result
}

// resolveAuthor fetches the user and, in a server, the member record behind one
// author, pulling both into State. It reports whether the author is now
// resolvable.
func resolveAuthor(session *revoltgo.Session, target AuthorRef) bool {
	if session.State.User(target.UserID) == nil {
		if _, err := session.User(target.UserID); err != nil {
			log.Printf("fetch user %s: %v", target.UserID, err)
			return false
		}
	}

	// A missing member is only worth asking for in a server channel.
	if target.ServerID == "" || session.State.Member(target.ServerID, target.UserID) != nil {
		return true
	}

	if _, err := session.ServerMember(target.ServerID, target.UserID); err != nil {
		log.Printf("fetch member %s in server %s: %v", target.UserID, target.ServerID, err)
		return false
	}

	return true
}

// MessageRef names one message to fetch, a channel being what a message ID is
// addressable through.
type MessageRef struct {
	ChannelID string
	MessageID string
}

// ResolveMessages fetches a batch of messages by ID, bounded by resolveWorkers,
// and reports what came back. It is what a reply preview whose target is not in
// the cache is filled in from: Revolt offers no route taking a list of IDs, so a
// batch is only a batch in that the caller gets one answer for it.
//
// Unlike ResolveAuthors it does not report what failed, because nothing retries.
// The usual reason a message cannot be fetched is that it was deleted, and a
// quoted line remounts on every scroll past it.
//
// Nothing is written to the message cache. That cache is the contiguous tail of a
// channel, and a reply reaches as far back as somebody cares to answer — dropping
// one into the middle would leave a hole that loadMoreHistory would mount as
// though it were history.
func (c *Client) ResolveMessages(targets []MessageRef) []*domain.Message {
	session := c.session.Load()
	if session == nil || len(targets) == 0 {
		return nil
	}

	var (
		mu       sync.Mutex
		resolved []*domain.Message
	)

	inParallel(targets, func(target MessageRef) {
		message, err := session.ChannelMessage(target.ChannelID, target.MessageID)
		if err != nil {
			log.Printf("fetch message %s: %v", target.MessageID, err)
			return
		}

		mu.Lock()
		defer mu.Unlock()

		if message := toMessage(message); message != nil {
			resolved = append(resolved, message)
		}
	})

	return resolved
}

/* Conversations */

// Conversations fetches the account's direct messages and groups, resolving the
// recipients behind them in the same pass — a direct message has no name of its
// own, so its row is titled and pictured after the other participant.
//
// The channels themselves are fed into State on the way through, so the caller
// keeps only the order and looks each one up through the store like any other.
func (c *Client) Conversations() ([]domain.Channel, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}

	raw, err := session.DirectMessages()
	if err != nil {
		return nil, err
	}
	resolveRecipients(session, raw)

	channels := make([]domain.Channel, 0, len(raw))
	for _, channel := range raw {
		if channel == nil {
			continue
		}
		if resolved, ok := c.store.Channel(channel.ID); ok {
			channels = append(channels, resolved)
		}
	}

	return channels, nil
}

// resolveRecipients pulls the users behind a conversation list into State.
// Failures are logged and left alone: the row falls back to a generic title
// rather than going missing.
func resolveRecipients(session *revoltgo.Session, channels []*revoltgo.Channel) {
	var missing []string
	queued := make(map[string]bool)
	for _, channel := range channels {
		if channel == nil || channel.ChannelType != revoltgo.ChannelTypeDM {
			continue
		}
		for _, id := range channel.Recipients {
			if queued[id] || session.State.User(id) != nil {
				continue
			}
			queued[id] = true
			missing = append(missing, id)
		}
	}

	inParallel(missing, func(id string) {
		if _, err := session.User(id); err != nil {
			log.Printf("fetch dm recipient %s: %v", id, err)
		}
	})
}

// OpenConversation returns the direct message with a user, asking the server to
// open one when there isn't yet.
//
// Unlike the conversation list, DirectMessageCreate does not feed its channel
// into State, and every channel-keyed path downstream looks a channel up there.
// Asking for it once is what puts it in.
func (c *Client) OpenConversation(userID string) (channelID string, err error) {
	session := c.session.Load()
	if session == nil {
		return "", ErrNoSession
	}

	channel, err := session.DirectMessageCreate(userID)
	if err != nil {
		return "", err
	}

	if session.State.Channel(channel.ID) == nil {
		if _, err := session.Channel(channel.ID); err != nil {
			log.Printf("fetch channel %s: %v", channel.ID, err)
		}
	}

	return channel.ID, nil
}

// CloseChannel closes a direct message or leaves a group. The sidebar is updated
// by ChannelClosed rather than here.
func (c *Client) CloseChannel(channelID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ChannelDelete(channelID)
}

/* Servers and members */

// FetchMembers pulls a server's whole membership into the local cache, the
// accounts behind it included, so the store can answer for everybody afterwards
// without going back to the network. Concurrent calls for the same server return
// ErrBusy.
//
// Revolt's members endpoint has no pagination and no search, so a server is one
// request or nothing at all. exclude_offline is the only lever it offers and the
// client declines it: the offline half is most of what a member list is for, and
// asking without it is the only way to know the membership at all.
//
// The users matter as much as the memberships. revoltgo's State silently drops
// an update for an account it has never cached, so somebody nobody had fetched
// could never be seen to come online — this is what puts them there.
//
// The State write happens inside revoltgo and is gated on its TrackBulkAPICalls
// option, which the client leaves at the default. Turn that off and this
// succeeds while quietly recording nothing.
func (c *Client) FetchMembers(serverID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}
	if !c.claim(c.fetchingMembers, serverID) {
		return ErrBusy
	}
	defer c.release(c.fetchingMembers, serverID)

	_, err := session.ServerMembers(serverID, false)

	return err
}

// FetchInvite resolves an invite code into the server it opens, without
// redeeming it. It is what an invite card is drawn from, so it is asked for
// every distinct code that appears in a channel — the caller is expected to
// remember the answer rather than ask again on every scroll.
//
// Unlike the rest of the read side this cannot go through Store: State caches
// only what the account is a member of, and an invite's whole purpose is naming
// a server it is not.
func (c *Client) FetchInvite(code string) (domain.Invite, error) {
	session := c.session.Load()
	if session == nil {
		return domain.Invite{}, ErrNoSession
	}

	invite, err := session.Invite(code)
	if err != nil {
		return domain.Invite{}, err
	}

	return toInvite(code, invite), nil
}

// CreateInvite makes an invite to a channel and returns its code. The client
// only ever consumed invites before this; the code it hands back is what
// util.InviteLink turns into something shareable.
//
// Revolt has no way to ask for a limited one — no expiry, no use count — so an
// invite made here is permanent until somebody deletes it.
func (c *Client) CreateInvite(channelID string) (code string, err error) {
	session := c.session.Load()
	if session == nil {
		return "", ErrNoSession
	}

	invite, err := session.ChannelInviteCreate(channelID)
	if err != nil {
		return "", err
	}
	if invite == nil {
		return "", errors.New("no invite returned")
	}

	return invite.ID, nil
}

// JoinInvite redeems an invite code.
//
// The joined server reaches the caller through ServerJoined rather than this
// response: the join payload carries the server as an object, and revoltgo
// decodes it into an Invite whose ServerID comes from a "server_id" field that
// payload never sets.
func (c *Client) JoinInvite(code string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	_, err := session.InviteJoin(code)

	return err
}

// LeaveServer leaves a server. As with closing a conversation, the sidebar is
// updated by the gateway event — ServerLeft — which is also what covers being
// kicked or the server being deleted out from under us.
func (c *Client) LeaveServer(serverID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServerDelete(serverID)
}

// KickMember removes a member from a server. The sidebar is repainted by
// MembersChanged, which arrives for any departure however it was caused.
func (c *Client) KickMember(serverID, userID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServerMemberDelete(serverID, userID)
}

/* This account */

// MaxStatusText is how long a status line may be. Revolt refuses a longer one
// outright, so the client clamps rather than spending a round trip finding out.
const MaxStatusText = 128

// fieldStatusText names the status line in Revolt's list of fields an edit may
// remove. An empty Text is *omitted* from the request rather than sent, so
// clearing the line is the one change that cannot be expressed as a value.
const fieldStatusText = "StatusText"

// SetPresence publishes how this account should appear to everybody else.
//
// The change comes back as EventUserUpdate like anybody else's, so nothing is
// recorded here — the store answers from State once it lands.
func (c *Client) SetPresence(presence domain.Presence) error {
	return c.editStatus(func(status *revoltgo.UserStatus) {
		status.Presence = fromPresence(presence)
	})
}

// SetStatusText publishes the line beside this account's name. Blank clears it.
//
// Longer than MaxStatusText is truncated by rune rather than refused: the limit
// is Revolt's and the difference between "too long" and "as much of it as fits"
// is not worth a failed send to the person who typed it.
func (c *Client) SetStatusText(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return c.editStatus(func(status *revoltgo.UserStatus) { status.Text = "" }, fieldStatusText)
	}

	if runes := []rune(text); len(runes) > MaxStatusText {
		text = string(runes[:MaxStatusText])
	}

	return c.editStatus(func(status *revoltgo.UserStatus) { status.Text = text })
}

// editStatus rewrites this account's status. Revolt models the presence and the
// line beside it as one object and takes the whole of it, so whichever half is
// not being changed has to be read back out of State and sent again unchanged —
// either caller omitting the other's half would silently destroy it.
func (c *Client) editStatus(change func(*revoltgo.UserStatus), remove ...string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	self := session.State.Self()
	if self == nil {
		return ErrNoSession
	}

	var status revoltgo.UserStatus
	if self.Status != nil {
		status = *self.Status
	}
	change(&status)

	_, err := session.UserEdit(self.ID, revoltgo.UserEditParams{Status: &status, Remove: remove})

	return err
}

/* Relationships */

// relationshipWith is how this account stands with a user: what the client has
// recorded since Ready, falling back to what Ready itself said. Somebody State
// cannot name is a stranger, which is also what a logged-out client answers.
//
// The overlay is what makes a relationship survive at all past the opening
// snapshot — see Client.relations. Safe from any goroutine; the store reads it.
func (c *Client) relationshipWith(user *revoltgo.User) domain.Relationship {
	if user == nil {
		return domain.RelationshipNone
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if known, ok := c.relations[user.ID]; ok {
		return known
	}

	return toRelationship(user.Relationship)
}

// setRelationship records a relationship, for the gateway handler and for an
// action the server has just agreed to.
func (c *Client) setRelationship(userID string, relationship domain.Relationship) {
	if userID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.relations[userID] = relationship
}

// AddFriend sends a friend request.
//
// It is the second action to go round revoltgo's typed API, and unlike
// FetchSlowmode it is not a missing field but a missing route: Revolt takes a
// *sent* request at POST /users/friend, naming the person by handle, where
// PUT /users/{id}/friend — which is what revoltgo calls FriendAdd — accepts one
// that has already arrived. The two are not interchangeable, and asking the
// wrong one of a stranger is a refusal with nothing to say why.
//
// The handle is read out of State rather than taken from the caller: it is
// "username#0001", and the client has it for anybody it can draw.
func (c *Client) AddFriend(userID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	user := session.State.User(userID)
	if user == nil || user.Username == "" {
		return errors.New("nothing known about that account")
	}

	name := user.Username
	if user.Discriminator != "" {
		name += "#" + user.Discriminator
	}

	body := struct {
		Username string `json:"username"`
	}{Username: name}

	var updated revoltgo.User
	if err := session.HTTP.Request(http.MethodPost, revoltgo.EndpointUserFriend(""), body, &updated); err != nil {
		return err
	}
	c.setRelationship(userID, toRelationship(updated.Relationship))

	return nil
}

// AcceptFriend accepts a request that has already arrived.
func (c *Client) AcceptFriend(userID string) error {
	return c.editRelationship(userID, func(session *revoltgo.Session) (*revoltgo.User, error) {
		return session.FriendAdd(userID)
	})
}

// RemoveFriend unfriends somebody, declines their request, or withdraws ours.
// Revolt spends one route on all three, and so does the client: what it means is
// decided by where the relationship stood, which the caller has already read to
// label the button.
func (c *Client) RemoveFriend(userID string) error {
	return c.editRelationship(userID, func(session *revoltgo.Session) (*revoltgo.User, error) {
		return session.FriendDelete(userID)
	})
}

// BlockUser blocks somebody, and UnblockUser takes it back.
func (c *Client) BlockUser(userID string) error {
	return c.editRelationship(userID, func(session *revoltgo.Session) (*revoltgo.User, error) {
		return session.UserBlock(userID)
	})
}

func (c *Client) UnblockUser(userID string) error {
	return c.editRelationship(userID, func(session *revoltgo.Session) (*revoltgo.User, error) {
		return session.UserUnblock(userID)
	})
}

// editRelationship is the shape the four typed routes share: make the request,
// and record what it says the relationship now is.
//
// Every one of them answers with the whole user, which is the only reason the
// client is told anything at all — the gateway's own EventUserRelationship is
// what covers a change made somewhere else, and neither writes to State.
func (c *Client) editRelationship(userID string, request func(*revoltgo.Session) (*revoltgo.User, error)) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	user, err := request(session)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("no account returned")
	}
	c.setRelationship(userID, toRelationship(user.Relationship))

	return nil
}

/* Profiles */

// UserProfile fetches the bio and the banner behind a user. They are the two
// things a profile shows that the client does not already hold, which is why
// they are a request of their own made after the card is already up.
func (c *Client) UserProfile(userID string) (domain.UserProfile, error) {
	session := c.session.Load()
	if session == nil {
		return domain.UserProfile{}, ErrNoSession
	}

	profile, err := session.UserProfile(userID)
	if err != nil {
		return domain.UserProfile{}, err
	}
	if profile == nil {
		return domain.UserProfile{}, nil
	}

	out := domain.UserProfile{Bio: profile.Content}
	if profile.Background != nil {
		out.BackgroundURL = profile.Background.URL(bannerSize)
	}

	return out, nil
}

// Mutual fetches the servers and friends this account has in common with
// somebody. Like the profile it is a request of its own, made once the dialog is
// already up.
//
// It is the third thing to go round revoltgo's typed API, and the plainest: the
// route answers with one object, and Session.UserMutual decodes into a *slice* of
// them — a shape the response can never take, so the call could only ever fail.
// Its struct also drops `channels`, which is the groups and conversations both
// are in. Neither is a field this needs, so what goes round it is the whole call.
func (c *Client) Mutual(userID string) (domain.Mutual, error) {
	session := c.session.Load()
	if session == nil {
		return domain.Mutual{}, ErrNoSession
	}

	var response struct {
		Users   []string `json:"users"`
		Servers []string `json:"servers"`
	}
	if err := session.HTTP.Request(http.MethodGet, revoltgo.EndpointUserMutual(userID), nil, &response); err != nil {
		return domain.Mutual{}, err
	}

	return domain.Mutual{UserIDs: response.Users, ServerIDs: response.Servers}, nil
}
