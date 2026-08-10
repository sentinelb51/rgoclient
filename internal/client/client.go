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
	"time"

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

	mu       sync.Mutex               // guards the three maps below
	fetching map[string]bool          // channelID -> a page request is already in flight
	slowmode map[string]time.Duration // channelID -> its send cooldown, once asked for

	// fetchingMembers is fetching's counterpart for FetchMembers. A map of its own
	// rather than a shared one because the key spaces are different — these are
	// server IDs — and a channel and a server sharing an ID is not a thing Revolt
	// promises will never happen.
	fetchingMembers map[string]bool
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
		slowmode:        make(map[string]time.Duration),
		fetchingMembers: make(map[string]bool),
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

// Login logs in with credentials and returns the token to persist. The token is
// returned rather than saved here because it is only worth keeping once Ready
// names the account it belongs to.
func (c *Client) Login(email, password string) (token string, err error) {
	session, resp, err := revoltgo.NewWithLogin(revoltgo.LoginParams{Email: email, Password: password})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	if err := c.start(session); err != nil {
		return "", err
	}

	return resp.Token, nil
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

// Close ends the session and clears everything belonging to it, leaving the
// client reusable for another login. Bumping the epoch is what silences handlers
// still in flight on the closed session.
func (c *Client) Close() {
	session := c.session.Swap(nil)
	c.epoch.Add(1)

	c.messages.Clear()

	c.mu.Lock()
	clear(c.fetching)
	clear(c.slowmode)
	clear(c.fetchingMembers)
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
