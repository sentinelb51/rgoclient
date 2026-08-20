// Package client owns everything that talks to Revolt: the session, the message
// cache, the gateway handlers, and every network action the user can take.
//
// It is the only package that imports revoltgo. What it hands upwards is domain
// values — through Store for what is already known, through Events for what the
// server pushes, and through the action methods for what the user asks for — so
// internal/app and internal/ui are written against the domain and can be
// exercised without a session.
//
// # Threading
//
// The split is deliberate and is the whole contract of the package:
//
//   - Reads (Store, Messages) are safe from anywhere. The session is held in an
//     atomic pointer and the message cache has its own lock.
//   - Actions block. They do the request and the cache update, and return; they
//     never touch a widget and never spawn a goroutine of their own. The caller
//     owns the UI thread, so the caller decides when to leave it.
//   - Events arrive on one buffered channel, in the order the gateway produced
//     them. A single reader linearises them, which is what makes the whole
//     backend drivable without a UI.
//
// A session is not required to exist. Being logged out is a valid state: reads
// report nothing known and actions return ErrNoSession.
package client

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
)

const (
	// eventBuffer is how far the gateway may run ahead of the reader. Emission
	// blocks once it is full rather than dropping: a dropped MessageCreate is a
	// message the user never sees, and the gateway reader stalling for a moment
	// costs nothing.
	eventBuffer = 64
)

// ErrNoSession is returned by every action when nobody is logged in.
var ErrNoSession = errors.New("no session")

// Client is the Revolt half of the application.
type Client struct {
	// session is swapped on login and cleared on logout. It is atomic because
	// actions read it from worker goroutines while the controller writes it from
	// the UI thread — the one piece of shared mutable state in the package.
	session atomic.Pointer[revoltgo.Session]

	// epoch counts sessions. A gateway handler captures it at registration and
	// drops what it was about to emit if it no longer matches, so a login that
	// lands mid-event cannot paint the previous account's data into the new one.
	epoch atomic.Uint64

	store    *store
	messages *cache.MessageCache

	events chan Event
	done   chan struct{} // closed by Shutdown; unblocks a stalled emit

	mu       sync.Mutex      // guards the three maps below
	fetching map[string]bool // channelID -> a page request is already in flight

	// fetchingMembers is fetching's counterpart for FetchMembers. A map of its own
	// rather than a shared one because the key spaces are different — these are
	// server IDs — and a channel and a server sharing an ID is not a thing Revolt
	// promises will never happen.
	fetchingMembers map[string]bool

	// relations is what the client knows about a relationship that State does not.
	// Ready fills User.Relationship for everybody it names, but nothing keeps it
	// current afterwards: revoltgo registers no default handler for
	// EventUserRelationship and State's caches are unexported, so there is no way
	// to write the change back where the store would read it. This is that write.
	relations map[string]domain.Relationship
}

// New returns a client with no session. Every read reports nothing known and
// every action fails with ErrNoSession, which is what being logged out is —
// so the controller can hand widgets a Store before anyone has logged in.
//
// The message cache is sized here and never resized, which is why the settings
// page marks its two entries as needing a restart: a live change would mean
// rebuilding the cache under readers who are holding slices out of it.
func New() *Client {
	settings := config.Current().Cache
	c := &Client{
		messages:        cache.NewMessageCache(settings.MessagesPerChannel, settings.CachedChannels),
		events:          make(chan Event, eventBuffer),
		done:            make(chan struct{}),
		fetching:        make(map[string]bool),
		fetchingMembers: make(map[string]bool),
		relations:       make(map[string]domain.Relationship),
	}
	c.store = &store{client: c}

	return c
}

// Store is the read side: what the client already knows, resolved into domain
// values. Safe from any goroutine.
func (c *Client) Store() domain.Store { return c.store }

// Messages is the per-channel message cache. Safe from any goroutine.
func (c *Client) Messages() *cache.MessageCache { return c.messages }

// Events is the stream of everything the server pushes. Exactly one reader is
// expected; the channel is never closed, so ranging it is the reader's whole
// lifetime.
func (c *Client) Events() <-chan Event { return c.events }

// Connected reports whether a session is open.
func (c *Client) Connected() bool { return c.session.Load() != nil }

/* Lifecycle */

// Open logs in with an existing token and opens the gateway. It returns once the
// websocket is up; the account itself is not known until Ready arrives on the
// event stream.
func (c *Client) Open(token string) error {
	return c.start(revoltgo.New(token))
}

// start attaches a session: it drops whatever came before, registers the gateway
// handlers against this session's epoch, and opens the websocket.
func (c *Client) start(session *revoltgo.Session) error {
	c.Close()

	epoch := c.epoch.Add(1)
	c.register(session, epoch)
	c.session.Store(session)

	if err := session.Open(); err != nil {
		c.session.Store(nil)
		return fmt.Errorf("open session: %w", err)
	}

	return nil
}

// Logout drops the session and revokes its token, which is what signing out
// means: Close alone only forgets the token locally, leaving it valid for
// anyone holding it — and the client writes it to disk, so "logged out" and
// "still usable" was a real difference.
//
// The drop comes first and the revocation second, in that order deliberately.
// Being logged out is a local fact and must not wait on the network or depend on
// it succeeding: a token the server has already forgotten fails here and is no
// less revoked for it, and one that could not be reached is no reason to leave
// somebody's messages on screen. So the error is the caller's to report, never
// to act on — by the time it is returned the client is logged out either way.
//
// Revoking through the captured session is what makes that ordering possible.
// Close only takes the websocket down; the token and the HTTP client it is sent
// with belong to the session value, which stays usable for exactly this.
//
// It blocks, like every other action.
func (c *Client) Logout() error {
	return c.revoke(func(session *revoltgo.Session) error { return session.Logout() })
}

// LogoutEverywhere revokes every session the account has, this one included —
// the answer to a token that has been used somewhere it shouldn't have been.
//
// revokeSelf is true because the alternative is signing every other device out
// and staying signed in here, which is not what the words mean.
func (c *Client) LogoutEverywhere() error {
	return c.revoke(func(session *revoltgo.Session) error { return session.SessionsDeleteAll(true) })
}

// revoke is the shared half of the two above: drop the session, then spend it on
// the request that invalidates it.
func (c *Client) revoke(request func(*revoltgo.Session) error) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}
	c.Close()

	return request(session)
}

// Close ends the session and clears everything belonging to it, leaving the
// client reusable for another login. Bumping the epoch is what silences handlers
// still in flight on the closed session.
func (c *Client) Close() {
	session := c.session.Swap(nil)
	c.epoch.Add(1)

	c.messages.Clear()

	c.mu.Lock()
	clear(c.fetching)
	clear(c.fetchingMembers)
	clear(c.relations)
	c.mu.Unlock()

	if session != nil {
		_ = session.Close()
	}
}

// Shutdown closes the session for good and releases anything blocked on the
// event stream. The client is not usable afterwards.
func (c *Client) Shutdown() {
	c.Close()

	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

/* Internals */

// emit hands an event to the reader, dropping it when the session that produced
// it has since been replaced. It blocks while the buffer is full — see
// eventBuffer — but never past Shutdown.
func (c *Client) emit(epoch uint64, event Event) {
	if c.epoch.Load() != epoch {
		return
	}

	select {
	case c.events <- event:
	case <-c.done:
	}
}
