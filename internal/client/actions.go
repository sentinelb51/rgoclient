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
