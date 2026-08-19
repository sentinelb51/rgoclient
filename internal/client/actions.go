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

// Autumn's buckets. A file is looked up by its ID *and* the bucket it was put
// in at the moment it is used, so which one a picture is uploaded to is what
// decides whether Revolt will take it as an avatar, as a banner or as an
// attachment — see uploadTo.
const (
	bucketAttachments = "attachments"
	bucketAvatars     = "avatars"
	bucketBackgrounds = "backgrounds"
)

// AvatarURL builds the URL an avatar is served from. It exists for the saved
// login cards, which persist an avatar ID from a session that no longer exists
// and so cannot ask the store for one.
func AvatarURL(avatarID string) string {
	if avatarID == "" {
		return ""
	}

	return revoltgo.EndpointAutumnFile(bucketAvatars, avatarID, avatarSize)
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
		id, err := uploadFile(session, bucketAttachments, attachment.Path, attachment.Name)
		if err != nil {
			log.Printf("upload attachment %s: %v", attachment.Name, err)
			continue
		}
		ids = append(ids, id)
	}

	return ids
}

// uploadFile puts a file in one of Autumn's buckets and hands back the ID Revolt
// will take for it. It goes round Session.AttachmentUpload, which names the
// attachments bucket and nothing else — Revolt looks a file up by ID *and*
// bucket, so an attachment's ID offered as an avatar does not exist.
func uploadFile(session *revoltgo.Session, bucket, path, name string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	var uploaded revoltgo.FileParamsData
	if err := session.HTTP.Request(http.MethodPost, revoltgo.EndpointAutumn(bucket),
		&revoltgo.FileParams{Name: name, Reader: file}, &uploaded); err != nil {
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

/* Slowmode */

// FetchSlowmode reads a channel's send cooldown and records it, so the store can
// answer for it without going back to the network. revoltgo models neither the
// field nor its ChannelUpdate, so asking for the raw channel is the only route
// to it; when it grows the field this becomes a line in store.go.
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
// Nothing is written to the message cache, for the reason messagePage gives, and
// nothing may ask for the users: with include_users the route answers with an
// object rather than an array, a shape ChannelSearch cannot decode. The caller
// resolves the authors it does not already know.
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
// and with no query, relevant to nothing — is an order nobody chose.
//
// Unguarded by c.fetching: a search comes from a keystroke rather than a scroll,
// and writes nothing two answers could interleave in.
func (c *Client) search(channelID string, params revoltgo.ChannelSearchParams) ([]*domain.Message, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}
	params.Sort = revoltgo.ChannelMessagesParamsSortTypeLatest

	page, err := session.ChannelSearch(channelID, params)
	if err != nil {
		return nil, err
	}

	messages := toMessages(page)
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

// Revolt's ceilings on what a channel edit carries. A name is cut to fit; an
// empty one has no repair, so it is the one case reported back.
const (
	MaxChannelName        = 32
	MaxChannelDescription = 1024
	MaxChannelSlowmode    = 6 * time.Hour
)

// The names a cleared field is removed under. Neither an empty string nor a zero
// reaches the wire — both are dropped by omitempty and read as "leave it alone" —
// so clearing anything is a name rather than a blank, as it is for a user edit.
const (
	fieldChannelDescription = "Description"
	fieldChannelSlowmode    = "Slowmode"
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

// channelEditBody is the route's DataEditChannel. revoltgo's ChannelEditParams
// carries neither slowmode nor voice, so the whole edit is sent by hand rather
// than half of it through the typed API and half beside it.
type channelEditBody struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	NSFW        *bool             `json:"nsfw,omitempty"`
	Slowmode    *int              `json:"slowmode,omitempty"`
	Voice       *channelVoiceEdit `json:"voice,omitempty"`
	Remove      []string          `json:"remove,omitempty"`
}

// channelVoiceEdit is VoiceInformation. An absent max_users is Revolt's own way
// of saying "no cap", so an empty object is how one is taken off.
type channelVoiceEdit struct {
	MaxUsers *int `json:"max_users,omitempty"`
}

// EditChannel changes what a channel is: its name, topic, age gate, send cooldown
// and — for a voice channel — how many may be in it. All of it rides one
// permission (see domain.PermissionManageChannel), which the route checks once
// for the whole edit, so a caller allowed to raise the card may send every field
// on it.
//
// What took comes back as ChannelUpdate on the gateway, which is what repaints
// the sidebar and the header. The slowmode is the exception: revoltgo drops the
// field from the channel and from the event alike, so what was just set is
// recorded here or the store goes on missing it.
func (c *Client) EditChannel(channelID string, edit ChannelEdit) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	name := trimTo(edit.Name, MaxChannelName)
	if name == "" {
		return ErrChannelNameEmpty
	}

	body := channelEditBody{
		Name:        name,
		Description: trimTo(edit.Description, MaxChannelDescription),
		NSFW:        &edit.NSFW,
	}
	if body.Description == "" {
		body.Remove = append(body.Remove, fieldChannelDescription)
	}

	var slowmode time.Duration
	if edit.Slowmode != nil {
		slowmode = min(max(*edit.Slowmode, 0), MaxChannelSlowmode).Truncate(time.Second)

		seconds := int(slowmode / time.Second)
		if seconds > 0 {
			body.Slowmode = &seconds
		} else {
			body.Remove = append(body.Remove, fieldChannelSlowmode)
		}
	}

	if edit.UserLimit != nil {
		body.Voice = &channelVoiceEdit{}
		if limit := *edit.UserLimit; limit > 0 {
			body.Voice.MaxUsers = &limit
		}
	}

	if err := session.HTTP.Request(http.MethodPatch, revoltgo.EndpointChannel(channelID), body, nil); err != nil {
		return err
	}

	if edit.Slowmode != nil {
		c.mu.Lock()
		c.slowmode[channelID] = slowmode
		c.mu.Unlock()
	}

	return nil
}

/* Servers and members */

// MaxServerName is Revolt's ceiling on a server name; ErrServerNameEmpty is the
// other end of the same rule, the route taking nothing shorter than a character.
const MaxServerName = 32

var ErrServerNameEmpty = errors.New("server name is empty")

// CreateServer makes a server owned by this account. A long name is cut rather
// than refused; an empty one has no repair, so it is the one case reported back.
//
// Nothing comes back that can be believed — revoltgo decodes the response into a
// bare Server no field of which matches — so the created server reaches the
// client the way a joined one does, as ServerCreate on the gateway.
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

// JoinInvite redeems an invite code. The joined server reaches the caller
// through ServerJoined rather than this response, whose ServerID revoltgo reads
// off a "server_id" field the join payload never sets.
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

// The fields a user edit may name in its remove list. A value it is meant to
// clear is *omitted* from the request when it is empty, so removing anything
// here is a name rather than a blank.
const (
	fieldStatusText        = "StatusText"
	fieldDisplayName       = "DisplayName"
	fieldAvatar            = "Avatar"
	fieldProfileContent    = "ProfileContent"
	fieldProfileBackground = "ProfileBackground"
)

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
		params.Remove = []string{fieldDisplayName}
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
		return c.editStatus(func(status *revoltgo.UserStatus) { status.Text = "" }, fieldStatusText)
	}

	return c.editStatus(func(status *revoltgo.UserStatus) { status.Text = text })
}

// editStatus rewrites this account's status. Revolt models the presence and the
// line beside it as one object and takes the whole of it, so whichever half is
// not being changed has to be read back out of State and sent again unchanged —
// either caller omitting the other's half would silently destroy it.
func (c *Client) editStatus(change func(*revoltgo.UserStatus), remove ...string) error {
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

	id, err := uploadFile(session, bucketAvatars, path, name)
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

	_, err = session.UserEdit(self.ID, revoltgo.UserEditParams{Remove: []string{fieldAvatar}})

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
		return c.editProfile(nil, fieldProfileContent)
	}

	return c.editProfile(&profileEdit{Content: &text})
}

// SetBanner puts a picture behind the profile, and RemoveBanner takes it away
// again, leaving the role colour showing.
func (c *Client) SetBanner(path, name string) error {
	session, _, err := c.account()
	if err != nil {
		return err
	}

	id, err := uploadFile(session, bucketBackgrounds, path, name)
	if err != nil {
		return err
	}

	return c.editProfile(&profileEdit{Background: &id})
}

func (c *Client) RemoveBanner() error {
	return c.editProfile(nil, fieldProfileBackground)
}

// profileEdit is the profile half of a user edit, sent round revoltgo's typed
// API: UserEditParams models Profile as a *UserProfile whose Background is a
// *File — the shape a profile is *read* in — where the route takes an attachment
// ID. The bio could go through it and does not: one field pair sent two
// different ways is worth less than the one shape.
type profileEdit struct {
	Content    *string `json:"content,omitempty"`
	Background *string `json:"background,omitempty"`
}

// editProfile sends one partial profile. Revolt applies it as a partial, so the
// half not named is left alone, and a nil edit is a removal on its own.
//
// Nothing about a profile is recorded anywhere: it is not on the user record,
// State has no room for it, and no gateway event announces a change — so a
// caller drawing what took has to ask for it again.
func (c *Client) editProfile(edit *profileEdit, remove ...string) error {
	session, self, err := c.account()
	if err != nil {
		return err
	}

	body := struct {
		Profile *profileEdit `json:"profile,omitempty"`
		Remove  []string     `json:"remove,omitempty"`
	}{Profile: edit, Remove: remove}

	return session.HTTP.Request(http.MethodPatch, revoltgo.EndpointUser(self.ID), body, nil)
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

	var response struct {
		Users   []string `json:"users"`
		Servers []string `json:"servers"`
	}
	if err := session.HTTP.Request(http.MethodGet, revoltgo.EndpointUserMutual(userID), nil, &response); err != nil {
		return domain.Mutual{}, err
	}

	return domain.Mutual{UserIDs: response.Users, ServerIDs: response.Servers}, nil
}
