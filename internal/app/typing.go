package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"RGOClient/internal/client"
	"RGOClient/internal/config"
	"RGOClient/internal/util"
)

// The typing indicator, both halves of it: who else is composing, and telling
// everyone else that we are.
//
// The state lives here rather than in internal/client because revoltgo's State
// does not model typing — there is nothing for domain.Store to answer from, and
// nothing worth widening that interface for. What it looks like instead is
// slowmode: maps confined to the UI thread and one timer re-armed only while
// there is something to wait for.

const (
	// typingLifetime is how long one gateway event vouches for somebody. Revolt
	// sends no heartbeat of its own and a client that goes away sends no stop, so
	// every entry expires on its own and a live typist is carried by the events
	// that keep arriving.
	typingLifetime = 10 * time.Second

	// typingSendInterval is the most often this account announces itself, and
	// typingIdleTimeout how long the composer may sit untouched before it takes
	// that back. Both are well inside a reader's typingLifetime, so a lost frame
	// costs a few seconds of staleness rather than a mark that never leaves.
	typingSendInterval = 3 * time.Second
	typingIdleTimeout  = 5 * time.Second

	// typingNameMaxRunes caps one name in the sentence. The line shares its row
	// with the slowmode chip, which is pinned to the far edge and would be pushed
	// off it by a long enough line — and five unabridged display names is long
	// enough on a narrow window.
	typingNameMaxRunes = 20
)

/* Receiving */

// typingNames is how many people the indicator names before it starts counting
// them instead. Zero is the feature's off switch: no line, no channel mark, and
// no work done for an event that arrives.
func typingNames() int { return config.Current().Behaviour.TypingNames }

// onTypingChanged records that somebody started or stopped composing, and
// repaints whichever surface shows it. Call on the UI thread.
//
// Typing is tracked for every channel rather than only the open one — that is
// what feeds the sidebar. It cannot grow without bound: nothing outlives
// typingLifetime.
func (a *App) onTypingChanged(event client.TypingChanged) {
	if typingNames() == 0 {
		return
	}

	// This account's own composing is recorded by noteSelfTyping rather than waited
	// for, so an event naming us is either an echo of that or the same account
	// typing from another client. Either way it is only wanted by somebody who has
	// asked to be shown.
	if event.UserID == a.store.SelfID() && !config.Current().Behaviour.TypingShowSelf {
		return
	}

	if !event.Typing {
		a.forgetTyping(event.ChannelID, event.UserID)
		return
	}

	// A name the client has never resolved would read as an ID, so ask for it. The
	// line redraws when the answer lands; until then that person is counted rather
	// than named.
	if event.ChannelID == a.currentChannelID {
		a.ensureAuthor(a.currentServerID, event.UserID)
	}

	if a.typing[event.ChannelID] == nil {
		a.typing[event.ChannelID] = make(map[string]time.Time)
	}
	a.typing[event.ChannelID][event.UserID] = time.Now().Add(typingLifetime)

	a.showTyping(event.ChannelID)
	a.armTypingTimer()
}

// forgetTyping drops one person from a channel, for a stop event or a message
// they have just sent. Call on the UI thread.
func (a *App) forgetTyping(channelID, userID string) {
	typists, ok := a.typing[channelID]
	if !ok {
		return
	}
	if _, ok := typists[userID]; !ok {
		return
	}

	delete(typists, userID)
	if len(typists) == 0 {
		delete(a.typing, channelID)
	}

	a.showTyping(channelID)
}

// forgetSelfTyping drops this account from wherever it is shown, for the setting
// being turned off mid-sentence. Every channel is walked rather than only the one
// being composed in, since an echo from another client can file us anywhere. Call
// on the UI thread.
func (a *App) forgetSelfTyping() {
	selfID := a.store.SelfID()

	for channelID := range a.typing {
		a.forgetTyping(channelID, selfID)
	}
}

// showTyping repaints the one surface a channel is shown on: the line above the
// composer while it is open, its sidebar row otherwise. Call on the UI thread.
// A background channel is found by walking the sidebar, and typing arrives for
// every channel the account can see — including those of servers the sidebar is
// not currently showing. So the setting is asked before the walk, not after it.
func (a *App) showTyping(channelID string) {
	if channelID == a.currentChannelID {
		a.refreshTyping()
		return
	}

	if config.Current().Behaviour.TypingInChannels {
		a.refreshChannelRow(channelID)
	}
}

// armTypingTimer wakes the client at the next moment somebody lapses, and not
// before. One timer covers every channel, re-armed rather than left running: a
// tick a second would repaint nothing for nine of the ten seconds an entry lives.
// Call on the UI thread.
func (a *App) armTypingTimer() {
	if a.typingTimer != nil {
		a.typingTimer.Stop()
		a.typingTimer = nil
	}

	var next time.Time
	for _, typists := range a.typing {
		for _, expiry := range typists {
			if next.IsZero() || expiry.Before(next) {
				next = expiry
			}
		}
	}
	if next.IsZero() {
		return
	}

	epoch := a.epoch
	a.typingTimer = time.AfterFunc(max(time.Until(next), 0), func() {
		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			a.typingTimer = nil
			for _, channelID := range a.pruneTyping(time.Now()) {
				a.showTyping(channelID)
			}
			a.armTypingTimer()
		}, false)
	})
}

// pruneTyping drops everybody whose entry has lapsed, reporting the channels that
// changed so only those repaint. Call on the UI thread.
func (a *App) pruneTyping(now time.Time) []string {
	var changed []string

	for channelID, typists := range a.typing {
		before := len(typists)
		for userID, expiry := range typists {
			if !expiry.After(now) {
				delete(typists, userID)
			}
		}

		if len(typists) == before {
			continue
		}
		if len(typists) == 0 {
			delete(a.typing, channelID)
		}
		changed = append(changed, channelID)
	}

	return changed
}

// typistsIn is who is composing in a channel, in a stable order. Sorted by ID
// rather than by name so the sentence does not reshuffle itself when somebody
// two names along stops: the order a map hands back is not one.
func (a *App) typistsIn(channelID string) []string {
	typists := a.typing[channelID]
	if len(typists) == 0 {
		return nil
	}

	userIDs := make([]string, 0, len(typists))
	for userID := range typists {
		userIDs = append(userIDs, userID)
	}
	slices.Sort(userIDs)

	return userIDs
}

// isTypingIn reports whether a channel's sidebar row should carry the mark.
func (a *App) isTypingIn(channelID string) bool {
	return config.Current().Behaviour.TypingInChannels && len(a.typing[channelID]) > 0
}

// refreshTyping redraws the line above the composer for the open channel. Call on
// the UI thread.
func (a *App) refreshTyping() {
	if a.typingIndicator == nil {
		return
	}

	limit := typingNames()
	behaviour := config.Current().Behaviour

	shown := a.typingIndicator.Visible()
	names, avatars, hidden, self := a.resolveTypists(a.currentChannelID, limit, behaviour.TypingAvatars)
	a.typingIndicator.Set(typingPhrase(names, hidden, self), avatars, behaviour.TypingAnimation)

	// The line is part of the dock's height, and Fyne reclaims nothing for a
	// shrinking minimum — so appearing or leaving has to re-hang the whole stack,
	// as the slowmode chip does.
	if a.typingIndicator.Visible() != shown {
		a.resizeDock()
	}
}

// resolveTypists turns a channel's typists into what the line draws: the names it
// can, up to limit, the avatars beside them, how many are left over, and whether
// this account is among them. Somebody the client cannot name yet is counted
// rather than named — ensureAuthor has already been asked for them, and the line
// redraws when the answer lands.
func (a *App) resolveTypists(channelID string, limit int, wantAvatars bool) (names, avatars []string, hidden int, self bool) {
	if limit == 0 {
		return nil, nil, 0, false
	}

	// This account is drawn first and out of the sorted order, because "You and
	// Bob" is one sentence where "Bob and You" is two names, one of them wrong. It
	// takes a slot like anybody else, so the limit still bounds the whole line.
	selfID := a.store.SelfID()
	room := limit

	if _, ok := a.typing[channelID][selfID]; ok && selfID != "" {
		self, room = true, limit-1
		if wantAvatars {
			_, avatarURL := a.typistIdentity(selfID)
			avatars = append(avatars, avatarURL)
		}
	}

	for _, userID := range a.typistsIn(channelID) {
		if userID == selfID {
			continue
		}

		name, avatarURL := a.typistIdentity(userID)
		if name == "" || len(names) == room {
			hidden++
			continue
		}

		names = append(names, util.Truncate(name, typingNameMaxRunes))
		if wantAvatars {
			avatars = append(avatars, avatarURL)
		}
	}

	return names, avatars, hidden, self
}

// typistIdentity is how somebody is named in the open channel — their nickname
// and per-server picture where there is one — or "" when they are not yet known.
func (a *App) typistIdentity(userID string) (name, avatarURL string) {
	if member, ok := a.store.Member(a.currentServerID, userID); ok {
		return member.Name, member.AvatarURL
	}
	if user, ok := a.store.User(userID); ok {
		return user.Name, user.AvatarURL
	}

	return "", ""
}

// typingPhrase is the sentence the line draws. hidden counts everyone past the
// limit as well as everyone the client cannot yet name, which is why it can be
// non-zero with no names at all. self puts this account at the head of it.
func typingPhrase(names []string, hidden int, self bool) string {
	if self {
		names = append([]string{"You"}, names...)
	}

	if len(names) == 0 {
		switch {
		case hidden == 0:
			return ""
		case hidden == 1:
			return "Someone is typing…"
		default:
			return "Several people are typing…"
		}
	}

	// "You" takes a plural verb however alone it is, so a line this account is in is
	// never singular — which is the whole of what naming ourselves changes.
	verb := " are typing…"
	if len(names)+hidden == 1 && !self {
		verb = " is typing…"
	}

	if hidden > 0 {
		others := fmt.Sprintf("%d others", hidden)
		if hidden == 1 {
			others = "1 other"
		}
		return strings.Join(names, ", ") + " and " + others + verb
	}
	if len(names) == 1 {
		return names[0] + verb
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + verb
}

/* Sending */

// noteTyping is every keystroke in the composer, with whether anything survived
// it. Call on the UI thread.
//
// Announcing is throttled to typingSendInterval rather than sent per keystroke:
// a reader's entry lives far longer than that, so re-stating it more often buys
// nothing. Emptying the composer is taken as stopping outright — it is the same
// gesture as never having started.
//
// typingChannelID marks where this account counts as composing whether or not it
// is being announced, since the local preview has to be taken back in the same
// places the announcement is. sentTypingAt zero means nothing was ever said out
// loud, and so nothing is owed a retraction.
func (a *App) noteTyping(typing bool) {
	behaviour := config.Current().Behaviour
	if a.currentChannelID == "" || (!behaviour.SendTyping && !behaviour.TypingShowSelf) {
		return
	}

	if !typing {
		a.stopTyping(a.currentChannelID)
		return
	}

	channelID := a.currentChannelID
	a.armTypingIdle()
	a.noteSelfTyping(channelID)

	if a.typingChannelID != channelID {
		a.typingChannelID, a.sentTypingAt = channelID, time.Time{}
	}

	if !behaviour.SendTyping || time.Since(a.sentTypingAt) < typingSendInterval {
		return
	}

	a.sentTypingAt = time.Now()
	a.background(func() error { return a.client.BeginTyping(channelID) }, func(error) {})
}

// noteSelfTyping files this account among the channel's typists, so the line
// shows what everyone else is being shown. It is a local echo rather than a
// reflected event: nothing guarantees Revolt sends our own typing back to us, and
// the preview is wanted whether or not we are announcing at all.
//
// The repaint is only on the way in. Every later keystroke moves the moment the
// entry lapses and nothing else, and a timer left armed at the older moment costs
// one wake that prunes nothing and re-arms itself. Call on the UI thread.
func (a *App) noteSelfTyping(channelID string) {
	selfID := a.store.SelfID()
	if !config.Current().Behaviour.TypingShowSelf || typingNames() == 0 || selfID == "" {
		return
	}

	typists := a.typing[channelID]
	if typists == nil {
		typists = make(map[string]time.Time)
		a.typing[channelID] = typists
	}

	_, already := typists[selfID]
	typists[selfID] = time.Now().Add(typingLifetime)

	if already {
		return
	}

	a.showTyping(channelID)
	a.armTypingTimer()
}

// stopTyping takes back what this account is counted as composing in a channel,
// locally and on the wire. Nothing is said when the retraction fails: the entry
// lapses on its own at the other end, which is what typingLifetime is for. Call
// on the UI thread.
func (a *App) stopTyping(channelID string) {
	a.cancelTypingIdle()

	if channelID == "" || a.typingChannelID != channelID {
		return
	}

	announced := !a.sentTypingAt.IsZero()
	a.typingChannelID, a.sentTypingAt = "", time.Time{}

	a.forgetTyping(channelID, a.store.SelfID())
	a.armTypingTimer()

	if announced {
		a.background(func() error { return a.client.EndTyping(channelID) }, func(error) {})
	}
}

// armTypingIdle restarts the quiet period after which a composer nobody has
// touched stops counting as being typed in. Call on the UI thread.
func (a *App) armTypingIdle() {
	if a.typingIdleTimer != nil {
		a.typingIdleTimer.Reset(typingIdleTimeout)
		return
	}

	epoch := a.epoch
	a.typingIdleTimer = time.AfterFunc(typingIdleTimeout, func() {
		a.doOnUI(func() {
			if !a.stale(epoch) {
				a.stopTyping(a.typingChannelID)
			}
		}, false)
	})
}

func (a *App) cancelTypingIdle() {
	if a.typingIdleTimer == nil {
		return
	}

	a.typingIdleTimer.Stop()
	a.typingIdleTimer = nil
}

// resetTyping drops everything typing owns, for a session that has ended. Call on
// the UI thread.
func (a *App) resetTyping() {
	if a.typingTimer != nil {
		a.typingTimer.Stop()
		a.typingTimer = nil
	}
	a.cancelTypingIdle()

	clear(a.typing)
	a.typingChannelID, a.sentTypingAt = "", time.Time{}
}
