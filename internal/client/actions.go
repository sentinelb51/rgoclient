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
	"unicode"
	"unicode/utf8"

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

// maxSearchQuery is Revolt's own ceiling on a search query. Past it the route
// refuses the request outright, so a longer one is cut rather than sent.
const maxSearchQuery = 64

// AvatarURL builds the URL an avatar is served from. It exists for the saved
// login cards, which persist an avatar ID from a session that no longer exists
// and so cannot ask the store for one.
func AvatarURL(avatarID string) string {
	if avatarID == "" {
		return ""
	}

	return revoltgo.EndpointAutumnFile(revoltgo.FileTagAvatars, avatarID, avatarSize)
}

// trimTo trims s and shortens it to at most limit runes, trimming again in case
// the cut left a space on the end. Every ceiling Revolt puts on a field is
// counted in characters and refuses anything longer outright, so cutting here
// spares a round trip that could only come back a refusal.
func trimTo(s string, limit int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= limit {
		return s
	}

	return strings.TrimSpace(string([]rune(s)[:limit]))
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
		id, err := uploadFile(session, revoltgo.FileTagAttachments, attachment.Path, attachment.Name)
		if err != nil {
			log.Printf("upload attachment %s: %v", attachment.Name, err)
			continue
		}
		ids = append(ids, id)
	}

	return ids
}

// uploadFile puts a file in one of Autumn's buckets and hands back the ID Revolt
// will take for it. Everything uploaded from this client goes through here: the
// path is opened and closed in one place, and the *tag* is named at the call
// site, because a file is looked up by ID **and** tag when it is used — an
// attachment's ID offered as an avatar is a file that does not exist.
func uploadFile(session *revoltgo.Session, tag revoltgo.FileTag, path, name string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	uploaded, err := session.Upload(tag, &revoltgo.FileParams{Name: name, Reader: file})
	if err != nil {
		return "", err
	}

	return uploaded.ID, nil
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
// raised it reads the other way round immediately. The cache update is done here
// because the event Revolt sends alongside cannot carry the answer — see
// events.go. Writing only once the server agrees leaves a rejection a no-op.
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

// reviseMessage replaces a cached message with a copy that change has been
// applied to, reporting whether anything moved — a change reporting false, and
// an echo of something already recorded, are both dropped rather than announced.
//
// A copy rather than a write in place: cached messages are read without the
// cache lock, so everything reachable from one stays immutable.
func (c *Client) reviseMessage(channelID, messageID string, change func(*domain.Message) bool) bool {
	current := c.messages.Find(channelID, messageID)
	if current == nil {
		return false
	}

	updated := *current
	if !change(&updated) {
		return false
	}

	return c.messages.Replace(channelID, &updated)
}

// markPinned writes a message's pin state into the cache.
func (c *Client) markPinned(channelID, messageID string, pinned bool) bool {
	return c.reviseMessage(channelID, messageID, func(message *domain.Message) bool {
		if message.Pinned == pinned {
			return false
		}
		message.Pinned = pinned

		return true
	})
}

// React adds or removes this account's reaction. Which of the two is asked for
// rather than toggled: the chip that raised it has already read the state to
// draw itself, and re-deriving it here could only disagree.
//
// The cache is written once the server agrees, as a pin is: a clicked chip has
// to answer immediately, and applyReaction reports nothing moved when the
// gateway echo lands on what this already holds — one repaint, not two.
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

// applyReaction records one person joining or leaving one reaction.
func (c *Client) applyReaction(channelID, messageID, emoji, userID string, add bool) bool {
	return c.reviseMessage(channelID, messageID, func(message *domain.Message) bool {
		index := reactionIndex(message.Reactions, emoji)

		switch {
		case index == -1 && !add:
			return false

		case index == -1:
			// One nobody had chosen yet, filed where toReactions would have put it.
			message.Reactions = slices.Insert(slices.Clone(message.Reactions),
				sortedIndex(message.Reactions, emoji),
				domain.Reaction{Emoji: emoji, Users: []string{userID}})

			return true
		}

		users, changed := withUser(message.Reactions[index].Users, userID, add)
		if !changed {
			return false
		}

		message.Reactions = slices.Clone(message.Reactions)
		if len(users) == 0 {
			// The last person left it: a chip reading zero is not a chip.
			message.Reactions = slices.Delete(message.Reactions, index, index+1)
		} else {
			message.Reactions[index].Users = users
		}

		return true
	})
}

// clearReaction drops one emoji from a message entirely, for the bulk removal a
// moderator makes.
func (c *Client) clearReaction(channelID, messageID, emoji string) bool {
	return c.reviseMessage(channelID, messageID, func(message *domain.Message) bool {
		index := reactionIndex(message.Reactions, emoji)
		if index == -1 {
			return false
		}
		message.Reactions = slices.Delete(slices.Clone(message.Reactions), index, index+1)

		return true
	})
}

// reactionIndex is where emoji sits among a message's reactions, or -1.
func reactionIndex(reactions []domain.Reaction, emoji string) int {
	return slices.IndexFunc(reactions, func(r domain.Reaction) bool { return r.Emoji == emoji })
}

// ClearReactions takes every reaction off a message at once, which Revolt allows
// only with ManageMessages. The cache is written here because Revolt announces
// it as an ordinary message update carrying an empty reaction map — which is
// also what every edit carries — so a clear made elsewhere never lands.
func (c *Client) ClearReactions(channelID, messageID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	if err := session.ChannelMessageReactionClear(channelID, messageID); err != nil {
		return err
	}
	c.clearAllReactions(channelID, messageID)

	return nil
}

// clearAllReactions empties a cached message's reactions.
func (c *Client) clearAllReactions(channelID, messageID string) bool {
	return c.reviseMessage(channelID, messageID, func(message *domain.Message) bool {
		if len(message.Reactions) == 0 {
			return false
		}
		message.Reactions = nil

		return true
	})
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
// it back. Both are websocket frames rather than requests — a write, no rate
// limiter, nothing to wait for.
//
// The socket is the second guard: a session is published before its websocket
// opens and closing one leaves the field stale, so Session.WS is nil either side
// of a login and revoltgo writes through it without looking.
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

// MessagesAround fetches a page centred on one message, for a jump to something
// too far back to be cached. Revolt takes half the limit either side and
// includes the message itself, so a page asked for as n comes back as n+2.
func (c *Client) MessagesAround(channelID, messageID string, limit int) ([]*domain.Message, error) {
	return c.messagePage(channelID, revoltgo.ChannelMessagesParams{
		Nearby:       messageID,
		Limit:        limit,
		IncludeUsers: true,
	})
}

// MessagesBefore and MessagesAfter extend such a window in either direction.
// After sorts oldest-first deliberately: Revolt's default is newest-first, which
// with an anchor asks for the newest messages that happen to be after it — the
// live tail rather than what follows the window.
func (c *Client) MessagesBefore(channelID, beforeID string, limit int) ([]*domain.Message, error) {
	return c.messagePage(channelID, revoltgo.ChannelMessagesParams{
		Before:       beforeID,
		Limit:        limit,
		IncludeUsers: true,
	})
}

func (c *Client) MessagesAfter(channelID, afterID string, limit int) ([]*domain.Message, error) {
	return c.messagePage(channelID, revoltgo.ChannelMessagesParams{
		After:        afterID,
		Sort:         revoltgo.ChannelMessagesParamsSortTypeOldest,
		Limit:        limit,
		IncludeUsers: true,
	})
}

// messagePage is the request those three share, and none of them writes to the
// cache: that cache is a channel's contiguous tail, and a page from wherever
// somebody was quoted would leave a hole scrollback would mount as history. The
// caller holds what comes back for as long as it is on screen.
//
// Sorted rather than reversed — only Before and After promise an order, and the
// one Nearby answers in is written down nowhere.
func (c *Client) messagePage(channelID string, params revoltgo.ChannelMessagesParams) ([]*domain.Message, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}
	if !c.claim(c.fetching, channelID) {
		return nil, ErrBusy
	}
	defer c.release(c.fetching, channelID)

	page, err := session.ChannelMessages(channelID, params)
	if err != nil {
		return nil, err
	}

	messages := toMessages(page.Messages)
	slices.SortFunc(messages, oldestFirst)

	return messages, nil
}

// oldestFirst orders messages chronologically. IDs are ULIDs, so ordering them
// is ordering them by time.
func oldestFirst(a, b *domain.Message) int { return strings.Compare(a.ID, b.ID) }

// PinnedMessages lists what is pinned in a channel, newest first. A pin is a
// flag on the message and Revolt publishes no collection of them, so the search
// route asked with pinned and no query is the only enumeration there is.
//
// Nothing is written to the message cache, for the reason messagePage gives.
func (c *Client) PinnedMessages(channelID string, limit int) ([]*domain.Message, error) {
	return c.search(channelID, revoltgo.ChannelSearchParams{
		Limit:  limit,
		Pinned: true,
	})
}

// SearchMessages is the same route asked the other way it can be — Revolt
// refuses a query and pinned together — and the only way to reach a message by
// what it says. An empty query is a request nothing comes back from, so it is
// not made. Cache and authors are as PinnedMessages leaves them.
func (c *Client) SearchMessages(channelID, query string, limit int) ([]*domain.Message, error) {
	query = trimTo(query, maxSearchQuery)
	if query == "" {
		return nil, nil
	}

	return c.search(channelID, revoltgo.ChannelSearchParams{
		Limit: limit,
		Query: query,
	})
}

// search is the request both share, newest first. Sort is named here because the
// route's default is Relevance, which for a list read as a channel's history —
// and with no query, relevant to nothing — is an order nobody chose. IncludeUsers
// is named here for the same reason it is on a history page: the users come back
// with the messages and land in State, so the caller's author resolution is left
// with only what no response carries.
//
// Unguarded by c.fetching: a search comes from a keystroke rather than a scroll,
// and writes nothing two answers could interleave in.
func (c *Client) search(channelID string, params revoltgo.ChannelSearchParams) ([]*domain.Message, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}
	params.Sort = revoltgo.ChannelMessagesParamsSortTypeLatest
	params.IncludeUsers = true

	page, err := session.ChannelSearch(channelID, params)
	if err != nil {
		return nil, err
	}

	messages := toMessages(page.Messages)
	slices.SortFunc(messages, func(a, b *domain.Message) int { return oldestFirst(b, a) })

	return messages, nil
}

// claim reserves key in one of the in-flight guards, reporting false when
// another request already holds it; release gives it back. They turn a
// superseded request into ErrBusy — revoltgo's REST layer takes no context, so a
// request cannot be cancelled, only not made twice.
//
// The guard is passed in because the two key different things — channels and
// servers — and no ID is promised unique across both. Reading the field to pass
// it needs no lock: the maps are built in New and only ever cleared.
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
// so the caller can drop its guard and let a later message retry. It does not
// say whether a *member* was fetched: resolving the account behind an already
// cached membership is what gives that membership a name, so the sidebar and its
// mention candidates change either way.
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

// ResolveMessages fetches a batch of messages by ID, bounded by resolveWorkers.
// It fills in a reply preview whose target is not cached: Revolt offers no route
// taking a list of IDs, so a batch is only a batch in that the caller gets one
// answer for it.
//
// Nothing failed is reported because nothing retries — the usual reason is that
// the message was deleted, and a quoted line remounts on every scroll past it.
// Nothing is written to the message cache, for the reason messagePage gives.
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

/* Editing a channel */

// Revolt's ceilings on what a channel carries — the name and the description
// bound making one as much as editing it. A name is cut to fit; an empty one has
// no repair, so it is the one case reported back.
const (
	MaxChannelName        = 32
	MaxChannelDescription = 1024
	MaxChannelSlowmode    = 6 * time.Hour
)

var ErrChannelNameEmpty = errors.New("channel name is empty")

// ChannelEdit is a channel as an edit would leave it, not the difference from
// what it is now: the card is answered whole, and Revolt applies a partial, so
// sending every field every time costs nothing and needs no diff.
//
// A nil Slowmode or UserLimit is a field the channel does not have — a group has
// no cooldown, a channel that is not a voice channel no user limit — and is left
// out of the request. For the limit that is not tidiness: `voice` is the field
// that *makes* a channel a voice channel, so sending it to a text channel would
// convert it.
type ChannelEdit struct {
	Name        string
	Description string

	Slowmode  *time.Duration
	UserLimit *int

	NSFW bool
}

// EditChannel changes what a channel is: its name, topic, age gate, send cooldown
// and — for a voice channel — how many may be in it. All of it rides one
// permission (see domain.PermissionManageChannel), which the route checks once
// for the whole edit, so a caller allowed to raise the card may send every field
// on it.
//
// What took comes back as ChannelUpdate on the gateway, which is what repaints
// the sidebar and the header.
func (c *Client) EditChannel(channelID string, edit ChannelEdit) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	name := trimTo(edit.Name, MaxChannelName)
	if name == "" {
		return ErrChannelNameEmpty
	}

	params := revoltgo.ChannelEditParams{
		Name:        name,
		Description: trimTo(edit.Description, MaxChannelDescription),
		NSFW:        &edit.NSFW,
	}
	if params.Description == "" {
		params.Remove = append(params.Remove, revoltgo.ChannelClearDescription)
	}

	if edit.Slowmode != nil {
		slowmode := min(max(*edit.Slowmode, 0), MaxChannelSlowmode).Truncate(time.Second)

		seconds := int(slowmode / time.Second)
		if seconds > 0 {
			params.Slowmode = &seconds
		} else {
			params.Remove = append(params.Remove, revoltgo.ChannelClearSlowmode)
		}
	}

	// An absent max_users is Revolt's own way of saying "no cap", so an empty
	// object is how one is taken off.
	if edit.UserLimit != nil {
		params.Voice = &revoltgo.ChannelVoiceInformation{}
		if limit := *edit.UserLimit; limit > 0 {
			params.Voice.MaxUsers = &limit
		}
	}

	_, err := session.ChannelEdit(channelID, params)

	return err
}

/* Servers and members */

// MaxServerName is Revolt's ceiling on a server name; ErrServerNameEmpty is the
// other end of the same rule, the route taking nothing shorter than a character.
// MaxServerDescription is the blurb's, which has no lower bound — an empty one is
// a description removed.
const (
	MaxServerName        = 32
	MaxServerDescription = 1024
)

var ErrServerNameEmpty = errors.New("server name is empty")

// CreateServer makes a server owned by this account. A long name is cut rather
// than refused; an empty one has no repair, so it is the one case reported back.
//
// The response carries the server and its default channels, but the created
// server reaches the client the way a joined one does, as ServerCreate on the
// gateway: one path into the sidebar rather than two.
func (c *Client) CreateServer(name string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	name = trimTo(name, MaxServerName)
	if name == "" {
		return ErrServerNameEmpty
	}

	_, err := session.ServerCreate(revoltgo.ServerCreateParams{Name: name})

	return err
}

// ChannelCreate is what creating a channel takes. Voice is a flag rather than a
// kind because it is one on the way out only: the route takes a channel type,
// while what comes back says so with a voice object instead (see toChannelKind).
type ChannelCreate struct {
	Name        string
	Description string

	Voice bool
	NSFW  bool
}

// CreateChannel makes a channel in a server and returns its ID. As with a server
// name, a long one is cut and an empty one is the one case reported back.
//
// The channel itself reaches the sidebar as ChannelCreated on the gateway, which
// is handled already — the response is read for its ID alone, so the caller can
// select what it has just made.
func (c *Client) CreateChannel(serverID string, create ChannelCreate) (channelID string, err error) {
	session := c.session.Load()
	if session == nil {
		return "", ErrNoSession
	}

	name := trimTo(create.Name, MaxChannelName)
	if name == "" {
		return "", ErrChannelNameEmpty
	}

	kind := revoltgo.ServerChannelCreateDataTypeText
	if create.Voice {
		kind = revoltgo.ServerChannelCreateDataTypeVoice
	}

	channel, err := session.ServerChannelCreate(serverID, revoltgo.ServerChannelCreateParams{
		Type:        kind,
		Name:        name,
		Description: trimTo(create.Description, MaxChannelDescription),
		NSFW:        create.NSFW,
	})
	if err != nil {
		return "", err
	}
	if channel == nil {
		return "", errors.New("no channel returned")
	}

	return channel.ID, nil
}

// FetchMembers pulls a server's whole membership into the local cache, the
// accounts behind it included, so the store can answer for everybody without
// going back to the network. Concurrent calls for one server return ErrBusy.
//
// The endpoint has no pagination and no search, so a server is one request or
// nothing. exclude_offline is declined: the offline half is most of what a
// member list is for.
//
// The users matter as much as the memberships — State silently drops an update
// for an account it has never cached, so somebody nobody fetched could never be
// seen to come online. That write is gated on revoltgo's TrackBulkAPICalls,
// which the client leaves at the default.
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
// redeeming it. It is asked for every distinct code in a channel, so the caller
// is expected to remember the answer rather than ask again on every scroll.
//
// It cannot go through Store: State caches only what the account is a member of,
// and an invite's whole purpose is naming a server it is not.
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

// CreateInvite makes an invite to a channel and returns its code, which
// util.InviteLink turns into something shareable. Revolt offers no expiry and no
// use count, so one made here is permanent until somebody deletes it.
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

// ServerInvites lists the invites a server has outstanding, grouped by the
// channel they land in so a re-fetch reads the same way twice.
//
// There is no expiry, no use count and no creation time on a stored invite, so
// domain.ServerInvite is the whole of one.
func (c *Client) ServerInvites(serverID string) ([]domain.ServerInvite, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}

	response, err := session.ServerInvites(serverID)
	if err != nil {
		return nil, err
	}

	invites := make([]domain.ServerInvite, len(response))
	for i, raw := range response {
		invites[i] = domain.ServerInvite{Code: raw.ID, ChannelID: raw.Channel, CreatorID: raw.Creator}
	}
	slices.SortFunc(invites, func(x, y domain.ServerInvite) int {
		if by := strings.Compare(x.ChannelID, y.ChannelID); by != 0 {
			return by
		}

		return strings.Compare(x.Code, y.Code)
	})

	return invites, nil
}

// DeleteInvite revokes an invite. Nothing announces it, so a caller showing the
// list re-fetches rather than waiting to be told.
func (c *Client) DeleteInvite(code string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.InviteDelete(code)
}

// JoinInvite redeems an invite code. The joined server reaches the caller
// through ServerJoined rather than this response, the same one path CreateServer
// takes.
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

// SetServerName renames a server. The new name reaches the client as
// ServerUpdated, which revoltgo files into State on the way past, so nothing is
// recorded here — the settings page and the sidebar both re-read the store.
func (c *Client) SetServerName(serverID, name string) error {
	name = trimTo(name, MaxServerName)
	if name == "" {
		return ErrServerNameEmpty
	}

	return c.editServer(serverID, revoltgo.ServerEditParams{Name: name})
}

// SetServerDescription publishes the blurb, blank removing it. As with every
// other field Revolt models as `omitzero`, an empty string is read as "leave it
// alone" — so clearing one is a name in `remove`, the shape a display name and a
// status line already use.
func (c *Client) SetServerDescription(serverID, description string) error {
	description = trimTo(description, MaxServerDescription)
	if description == "" {
		return c.editServer(serverID, revoltgo.ServerEditParams{
			Remove: []revoltgo.ServerEditParamsRemove{revoltgo.ServerEditDataRemoveDescription},
		})
	}

	return c.editServer(serverID, revoltgo.ServerEditParams{Description: description})
}

// SetServerIcon and SetServerBanner hang a picture on a server. Revolt takes an
// ID rather than the picture, so the file is uploaded first — into the bucket
// that makes the ID usable as that kind, exactly as an avatar is. The two are
// separate buckets and separate fields: an icon offered as a banner does not
// exist.
//
// Nothing is recorded here either. The new picture arrives as a ServerUpdate,
// which is also what makes one set from another client appear.
func (c *Client) SetServerIcon(serverID, path, name string) error {
	return c.uploadServerPicture(serverID, revoltgo.FileTagIcons, path, name,
		func(id string) revoltgo.ServerEditParams { return revoltgo.ServerEditParams{Icon: id} })
}

func (c *Client) SetServerBanner(serverID, path, name string) error {
	return c.uploadServerPicture(serverID, revoltgo.FileTagBanners, path, name,
		func(id string) revoltgo.ServerEditParams { return revoltgo.ServerEditParams{Banner: id} })
}

// RemoveServerIcon and RemoveServerBanner take one off again. As with a
// description, an empty string is read as "leave it alone", so clearing one is a
// name in `remove`.
func (c *Client) RemoveServerIcon(serverID string) error {
	return c.editServer(serverID, revoltgo.ServerEditParams{
		Remove: []revoltgo.ServerEditParamsRemove{revoltgo.ServerEditDataRemoveIcon},
	})
}

func (c *Client) RemoveServerBanner(serverID string) error {
	return c.editServer(serverID, revoltgo.ServerEditParams{
		Remove: []revoltgo.ServerEditParamsRemove{revoltgo.ServerEditDataRemoveBanner},
	})
}

// uploadServerPicture is the half the two setters share: the upload, then the
// edit naming what was uploaded. Which field the ID lands in is the caller's,
// the bucket and the field having to agree.
func (c *Client) uploadServerPicture(serverID string, tag revoltgo.FileTag, path, name string, edit func(id string) revoltgo.ServerEditParams) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	id, err := uploadFile(session, tag, path, name)
	if err != nil {
		return err
	}

	return c.editServer(serverID, edit(id))
}

// editServer is the one request both setters make. Each field is sent alone:
// Revolt applies the edit as a partial, so a setter that read the other half back
// out of State and sent it again could only ever lose a race with somebody else's
// change.
func (c *Client) editServer(serverID string, edit revoltgo.ServerEditParams) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	_, err := session.ServerEdit(serverID, edit)

	return err
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

// BanOptions is everything a ban takes beyond the two IDs, all of it optional: a
// reason kept with the ban, and how much of what the member said recently goes
// with them. Zero values are a plain ban.
type BanOptions struct {
	Reason         string
	DeleteMessages time.Duration
}

// What the ban route will take. Revolt refuses anything past either outright, so
// both are clamped rather than spending a round trip finding out.
const (
	MaxBanReason         = 1024
	MaxBanDeleteMessages = 7 * 24 * time.Hour
)

// BanMember bans a user from a server. As with a kick, the departure arrives as
// MembersChanged.
func (c *Client) BanMember(serverID, userID string, options BanOptions) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServerMemberBan(serverID, userID, revoltgo.ServerMemberBanParams{
		Reason:               trimTo(options.Reason, MaxBanReason),
		DeleteMessageSeconds: int64(max(min(options.DeleteMessages, MaxBanDeleteMessages), 0) / time.Second),
	})
}

// UnbanMember lifts a ban. Nothing on the gateway announces one being lifted, so
// a caller showing the list re-fetches rather than waiting to be told.
func (c *Client) UnbanMember(serverID, userID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServerMemberUnban(serverID, userID)
}

// ServerBans lists who is banned from a server. Each entry carries the name and
// the picture as well as the ID: the route answers with a reduced user of its
// own, and a banned account is no longer a member, so there is nothing left in
// the store to resolve one against. Those four fields are four of revoltgo's
// User, which is what keeps the default-avatar fallback the same one every other
// row gets.
func (c *Client) ServerBans(serverID string) ([]domain.Ban, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}

	response, err := session.ServerBans(serverID)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, nil
	}

	users := make(map[string]*revoltgo.User, len(response.Users))
	for _, user := range response.Users {
		if user != nil {
			users[user.ID] = user
		}
	}

	entries := make([]keyed[domain.Ban], 0, len(response.Bans))
	for _, ban := range response.Bans {
		if ban == nil {
			continue
		}

		// A ban naming somebody the response left out is still a ban, so the ID
		// stands in for the name rather than the row going missing.
		out := domain.Ban{UserID: ban.ID.User, Username: ban.ID.User, Reason: ban.Reason}
		if user := users[ban.ID.User]; user != nil {
			out.Username, out.AvatarURL = user.Username, user.AvatarURL(avatarSize)
		}
		entries = append(entries, keyed[domain.Ban]{out, strings.ToLower(out.Username), out.UserID})
	}

	return sortedByName(entries), nil
}

/* Editing a member */

// What a member and a role edit will take, from DataMemberEdit and DataEditRole:
// https://developers.stoat.chat/api-reference. A timeout is bounded nowhere, so
// how long one may be is the menu's business rather than a limit to clamp to.
const (
	MaxNickname   = 32
	MaxRoleName   = 32
	MaxRoleColour = 128
)

// SetMemberNickname renames a member in one server, an empty name removing the
// nickname. Revolt takes both through the same route under different permissions
// — ChangeNickname for this account's own, ManageNicknames for anybody else's —
// so which is being asked for is the caller's to have established.
func (c *Client) SetMemberNickname(serverID, userID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return c.editMember(serverID, userID, revoltgo.ServerMemberEditParams{
			Remove: []revoltgo.ServerMemberClearType{revoltgo.ServerMemberClearNickname},
		})
	}

	return c.editMember(serverID, userID, revoltgo.ServerMemberEditParams{
		Nickname: trimTo(name, MaxNickname),
	})
}

// TimeoutMember silences a member until a moment that must be in the future:
// Revolt bounds a timeout nowhere, so one sent in the past is stored and read as
// expired, which is a timeout that never happened.
func (c *Client) TimeoutMember(serverID, userID string, until time.Time) error {
	if !until.After(time.Now()) {
		return c.RemoveTimeout(serverID, userID)
	}

	return c.editMember(serverID, userID, revoltgo.ServerMemberEditParams{Timeout: &until})
}

// RemoveTimeout lets a member speak again before their timeout was due to end.
func (c *Client) RemoveTimeout(serverID, userID string) error {
	return c.editMember(serverID, userID, revoltgo.ServerMemberEditParams{
		Remove: []revoltgo.ServerMemberClearType{revoltgo.ServerMemberClearTimeout},
	})
}

// SetMemberRoles replaces the whole set of roles a member holds — the route takes
// the set rather than a change to it, so a caller adding one sends the others
// back with it. An empty set is a removal, an empty array being omitted from the
// request.
func (c *Client) SetMemberRoles(serverID, userID string, roleIDs []string) error {
	if len(roleIDs) == 0 {
		return c.editMember(serverID, userID, revoltgo.ServerMemberEditParams{
			Remove: []revoltgo.ServerMemberClearType{revoltgo.ServerMemberClearRoles},
		})
	}

	return c.editMember(serverID, userID, revoltgo.ServerMemberEditParams{Roles: roleIDs})
}

// editMember is the one request behind the four above. The member that comes back
// is dropped: the change returns as ServerMemberUpdate, which revoltgo files into
// State before the handler here sees it.
func (c *Client) editMember(serverID, userID string, edit revoltgo.ServerMemberEditParams) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	_, err := session.ServerMemberEdit(serverID, userID, edit)

	return err
}

/* Roles */

// ErrRoleNameEmpty is a role named with nothing, refused here rather than by the
// server: Revolt's minimum is one character.
var ErrRoleNameEmpty = errors.New("role name is empty")

// CreateRole adds a role to a server. Revolt assigns its rank — the rank a
// creation carries has no effect, ranks being the other route's business — and
// the role reaches the client as a role update for one State has never heard of.
// The ID is handed back so the caller can open what it just made.
func (c *Client) CreateRole(serverID, name string) (roleID string, err error) {
	session := c.session.Load()
	if session == nil {
		return "", ErrNoSession
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrRoleNameEmpty
	}

	role, err := session.ServersRoleCreate(serverID, revoltgo.ServerRoleCreateParams{
		Name: trimTo(name, MaxRoleName),
	})
	if err != nil {
		return "", err
	}
	if role == nil {
		return "", nil
	}

	return role.ID, nil
}

// SetRoleName renames a role.
func (c *Client) SetRoleName(serverID, roleID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrRoleNameEmpty
	}

	return c.editRole(serverID, roleID, revoltgo.ServerRoleEditParams{Name: trimTo(name, MaxRoleName)})
}

// SetRoleColor colours a role, an empty colour taking the colour off. Revolt
// takes any CSS colour here — a gradient included — which is why what is sent is
// not parsed back into the hex the picker offered.
func (c *Client) SetRoleColor(serverID, roleID, colour string) error {
	colour = strings.TrimSpace(colour)
	if colour == "" {
		return c.editRole(serverID, roleID, revoltgo.ServerRoleEditParams{
			Remove: []revoltgo.ServerRoleClearType{revoltgo.ServerRoleClearColour},
		})
	}

	return c.editRole(serverID, roleID, revoltgo.ServerRoleEditParams{Colour: trimTo(colour, MaxRoleColour)})
}

// SetRoleHoist decides whether a role gets a section of its own in the member
// list.
func (c *Client) SetRoleHoist(serverID, roleID string, hoist bool) error {
	return c.editRole(serverID, roleID, revoltgo.ServerRoleEditParams{Hoist: &hoist})
}

// DeleteRole removes a role from a server and from everybody holding it.
func (c *Client) DeleteRole(serverID, roleID string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServerRoleDelete(serverID, roleID)
}

// SetRolePermissions publishes a role's whole override. Revolt takes both halves
// together, so a caller changing one bit sends the rest back unchanged.
func (c *Client) SetRolePermissions(serverID, roleID string, allow, deny domain.Permission) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.PermissionsSet(serverID, roleID, revoltgo.PermissionOverwrite{
		Allow: int64(allow),
		Deny:  int64(deny),
	})
}

// SetDefaultPermissions publishes what every member of a server holds before any
// role is applied. A plain set rather than an override, there being nothing under
// it to inherit from — which is also why it is a different route from a role's.
func (c *Client) SetDefaultPermissions(serverID string, allow domain.Permission) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.PermissionsSetDefault(serverID, revoltgo.PermissionsSetDefaultParams{
		Permissions: int64(allow),
	})
}

// SetRoleRanks reorders a server's roles, most senior first. The route takes
// **every** role the server has and refuses a partial list, so the caller sends
// the whole order rather than the one role that moved:
// https://github.com/stoatchat/stoatchat/blob/main/crates/delta/src/routes/servers/roles_edit_positions.rs
func (c *Client) SetRoleRanks(serverID string, roleIDs []string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.ServersRoleRanksEdit(serverID, roleIDs)
}

// editRole is the one request behind the three setters above. As with a server's
// own name, what took is drawn by the role update that follows rather than by
// what came back.
func (c *Client) editRole(serverID, roleID string, edit revoltgo.ServerRoleEditParams) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	_, err := session.ServerRoleEdit(serverID, roleID, edit)

	return err
}

/* This account */

// account is the session and this account's own user record. Every edit here
// needs both, and a client missing either is one that is logged out.
func (c *Client) account() (*revoltgo.Session, *revoltgo.User, error) {
	session := c.session.Load()
	if session == nil {
		return nil, nil, ErrNoSession
	}

	self := session.State.Self()
	if self == nil {
		return nil, nil, ErrNoSession
	}

	return session, self, nil
}

// MaxStatusText is how long a status line may be. Revolt refuses a longer one
// outright, so the client clamps rather than spending a round trip finding out.
const MaxStatusText = 128

// The display name's bounds are Revolt's, which refuses anything outside them.
const (
	MinDisplayName = 2
	MaxDisplayName = 32
)

// ErrDisplayNameShort reports a name too short for Revolt to take. Empty is not
// short — it is the removal — so the two cannot share an answer.
var ErrDisplayNameShort = errors.New("display name too short")

// SetDisplayName publishes the name shown wherever this account is named. Blank
// removes it, leaving the username to stand for the account.
//
// Unlike a status line the name is a single field, so nothing has to be read back
// and sent again with it: Revolt applies the edit as a partial.
func (c *Client) SetDisplayName(name string) error {
	session, self, err := c.account()
	if err != nil {
		return err
	}

	name, ok := cleanDisplayName(name)
	if !ok {
		return ErrDisplayNameShort
	}

	params := revoltgo.UserEditParams{DisplayName: name}
	if name == "" {
		params.Remove = []revoltgo.UserRemoveField{revoltgo.UserRemoveDisplayName}
	}

	_, err = session.UserEdit(self.ID, params)

	return err
}

// cleanDisplayName is what Revolt will take of a typed name, and whether it can
// be sent at all. Forbidden characters are dropped rather than refused — a
// newline arriving with a pasted name is nobody's decision — and a long name is
// cut. Too short has no honest repair; empty is the removal and always allowed.
func cleanDisplayName(name string) (string, bool) {
	name = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\u200b' { // the zero-width space Revolt's pattern forbids
			return -1
		}

		return r
	}, name)

	name = trimTo(name, MaxDisplayName)

	return name, name == "" || utf8.RuneCountInString(name) >= MinDisplayName
}

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
	text = trimTo(text, MaxStatusText)
	if text == "" {
		return c.editStatus(func(status *revoltgo.UserStatus) { status.Text = "" }, revoltgo.UserRemoveStatusText)
	}

	return c.editStatus(func(status *revoltgo.UserStatus) { status.Text = text })
}

// editStatus rewrites this account's status. Revolt models the presence and the
// line beside it as one object and takes the whole of it, so whichever half is
// not being changed has to be read back out of State and sent again unchanged —
// either caller omitting the other's half would silently destroy it.
func (c *Client) editStatus(change func(*revoltgo.UserStatus), remove ...revoltgo.UserRemoveField) error {
	session, self, err := c.account()
	if err != nil {
		return err
	}

	var status revoltgo.UserStatus
	if self.Status != nil {
		status = *self.Status
	}
	change(&status)

	_, err = session.UserEdit(self.ID, revoltgo.UserEditParams{Status: &status, Remove: remove})

	return err
}

/* This account's pictures and profile */

// SetAvatar hangs a picture on this account. Revolt takes an ID rather than the
// picture, so the file is uploaded first — into the avatars bucket, which is
// what makes the ID usable as one.
//
// Nothing is recorded here: the new avatar comes back as an ordinary user
// update, which is also what makes one set from another client arrive.
func (c *Client) SetAvatar(path, name string) error {
	session, self, err := c.account()
	if err != nil {
		return err
	}

	id, err := uploadFile(session, revoltgo.FileTagAvatars, path, name)
	if err != nil {
		return err
	}

	_, err = session.UserEdit(self.ID, revoltgo.UserEditParams{Avatar: id})

	return err
}

// RemoveAvatar takes it off again, leaving the default Revolt draws from the ID.
func (c *Client) RemoveAvatar() error {
	session, self, err := c.account()
	if err != nil {
		return err
	}

	_, err = session.UserEdit(self.ID, revoltgo.UserEditParams{Remove: []revoltgo.UserRemoveField{revoltgo.UserRemoveAvatar}})

	return err
}

// MaxBio is how long the description on a profile may be. Revolt refuses a
// longer one, so it is cut here rather than spent on a round trip.
const MaxBio = 2000

// SetBio publishes the description under this account's name on its profile.
// Blank removes it.
func (c *Client) SetBio(text string) error {
	text = trimTo(text, MaxBio)
	if text == "" {
		return c.editProfile(nil, revoltgo.UserRemoveProfileContent)
	}

	return c.editProfile(&revoltgo.UserProfileParams{Content: &text})
}

// SetBanner puts a picture behind the profile, and RemoveBanner takes it away
// again, leaving the role colour showing.
func (c *Client) SetBanner(path, name string) error {
	session, _, err := c.account()
	if err != nil {
		return err
	}

	id, err := uploadFile(session, revoltgo.FileTagBackgrounds, path, name)
	if err != nil {
		return err
	}

	return c.editProfile(&revoltgo.UserProfileParams{Background: id})
}

func (c *Client) RemoveBanner() error {
	return c.editProfile(nil, revoltgo.UserRemoveProfileBackground)
}

// editProfile sends one partial profile. Revolt applies it as a partial, so the
// half not named is left alone, and a nil edit is a removal on its own.
//
// Nothing about a profile is recorded anywhere: it is not on the user record,
// State has no room for it, and no gateway event announces a change — so a
// caller drawing what took has to ask for it again.
func (c *Client) editProfile(edit *revoltgo.UserProfileParams, remove ...revoltgo.UserRemoveField) error {
	session, self, err := c.account()
	if err != nil {
		return err
	}

	_, err = session.UserEdit(self.ID, revoltgo.UserEditParams{Profile: edit, Remove: remove})

	return err
}

/* This account's username */

// Revolt's bounds on a username, which it refuses anything outside of.
const (
	MinUsername = 2
	MaxUsername = 32
)

// ErrUsernameInvalid reports a username Revolt's pattern will not take. Refused
// here rather than sent because the server answers a malformed name and a taken
// one alike, and only one of the two can be explained to whoever typed it.
var ErrUsernameInvalid = errors.New("username has characters Revolt will not take")

// SetUsername changes the handle that tells two identical display names apart.
// It is the one edit of this account that re-authenticates: Revolt takes the
// account password with the new name.
//
// Like a display name the change comes back as a user update, so nothing is
// recorded here either.
func (c *Client) SetUsername(name, password string) error {
	session, _, err := c.account()
	if err != nil {
		return err
	}

	name = strings.TrimSpace(name)
	if !validUsername(name) {
		return ErrUsernameInvalid
	}

	_, err = session.SetUsername(revoltgo.UsernameParams{Username: name, Password: password})

	return err
}

// validUsername is Revolt's pattern spelled out: letters, digits and the three
// marks it allows, between the bounds above. Unlike a display name there is
// nothing here to repair — a username with a space in it is not one somebody
// nearly typed, and a silent repair would take an account name they never chose.
func validUsername(name string) bool {
	if count := utf8.RuneCountInString(name); count < MinUsername || count > MaxUsername {
		return false
	}

	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '.', r == '-':
		default:
			return false
		}
	}

	return true
}

/* Relationships */

// relationshipWith is how this account stands with a user: what the client has
// recorded since Ready, falling back to what Ready itself said. Somebody State
// cannot name is a stranger, as is anybody to a logged-out client. The overlay
// is what makes a relationship survive past the opening snapshot — see
// Client.relations. Safe from any goroutine; the store reads it.
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

// AddFriend sends a friend request. A missing route rather than a missing field:
// Revolt *sends* one at POST /users/friend, naming the person by handle, where
// revoltgo's FriendAdd is PUT /users/{id}/friend, which accepts one that has
// already arrived. Asking the wrong one of a stranger is a refusal with nothing
// to say why. The handle comes out of State, the client having it for anybody it
// can draw.
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
// record what it says the relationship now is. Each answers with the whole user,
// which is the only reason the client is told anything — neither these nor the
// gateway's EventUserRelationship write to State.
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
// somebody, a request of its own made once the dialog is up.
//
// Session.UserMutual decodes one object into a *slice* of them, a shape the
// response can never take, so the call could only ever fail — hence sending it
// by hand.
func (c *Client) Mutual(userID string) (domain.Mutual, error) {
	session := c.session.Load()
	if session == nil {
		return domain.Mutual{}, ErrNoSession
	}

	response, err := session.UserMutual(userID)
	if err != nil {
		return domain.Mutual{}, err
	}

	return domain.Mutual{UserIDs: response.Users, ServerIDs: response.Servers}, nil
}
