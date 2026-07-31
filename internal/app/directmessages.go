package app

// The home view: the fixed home button at the top of the server sidebar swaps
// the channel list from a server's channels to the user's direct messages and
// groups. Everything downstream of the sidebar — message loading, acks,
// composing, scrollback — is channel-keyed and needs no special case; only the
// list of channels differs, plus the member sidebar, which stays empty because
// a DM has no server members to show.

import (
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/sentinelb51/revoltgo"
)

// homeHeader titles the channel sidebar while the home view is open, standing
// in for the server name.
const homeHeader = "Direct Messages"

// selectHome opens the home view. The cached DM list paints immediately and a
// refresh is fired regardless: the list is a fetched snapshot with no gateway
// event behind it, so re-opening home is the natural moment to re-ask for it.
// Re-clicking home is a no-op — it would otherwise yank the view back to the
// first conversation.
func (a *App) selectHome() {
	if a.homeSelected {
		return
	}
	a.homeSelected = true
	a.currentServerID = ""

	a.syncServerSelection("")
	a.setHeader(a.serverHeader, homeHeader)
	a.refreshChannelList()
	a.refreshMemberList()

	if len(a.dmChannels) > 0 {
		a.selectChannel(a.dmChannels[0])
	} else {
		a.clearChannelSelection()
		a.showStatus("Loading direct messages...")
	}
	a.loadDirectMessages()
}

// loadDirectMessages refreshes the cached DM/group list from the API. It is
// stale-while-revalidate: whatever is already cached stays on screen until the
// response lands, so re-opening home never blanks the sidebar. Recipients
// missing from State are resolved in the same pass, because a DM has no name of
// its own — the row is titled after the other participant.
func (a *App) loadDirectMessages() {
	session := a.session
	if session == nil || a.loadingDMs {
		return
	}
	a.loadingDMs = true

	go func() {
		// Every hop back to the UI thread re-checks that this is still the open
		// session: a logout and re-login can land mid-request, and the previous
		// account's conversations must not be painted into the new one's sidebar.
		defer a.doOnUI(func() {
			if a.session == session {
				a.loadingDMs = false
			}
		}, false)

		channels, err := session.DirectMessages()
		if err != nil {
			log.Printf("fetch direct messages: %v", err)
			a.doOnUI(func() {
				if a.session == session && a.homeSelected && len(a.dmChannels) == 0 {
					a.showStatus("Failed to load direct messages")
				}
			}, false)
			return
		}

		resolveRecipients(session, channels)
		a.doOnUI(func() {
			if a.session == session {
				a.setDirectMessages(channels)
			}
		}, false)
	}()
}

// setDirectMessages records the sidebar order and repaints the home view when
// it's open, selecting the first conversation if none is. Call on the UI thread.
func (a *App) setDirectMessages(channels []*revoltgo.Channel) {
	a.dmChannels = sortConversations(channels)

	if !a.homeSelected {
		return
	}
	a.refreshChannelList()

	switch {
	case len(a.dmChannels) == 0:
		a.clearChannelSelection()
		a.showStatus("No direct messages yet")
	case a.currentChannelID == "":
		a.selectChannel(a.dmChannels[0])
	default:
		a.syncChannelList()
	}
}

// sortConversations drops closed DMs, orders the rest by most recent activity,
// and returns only their IDs: DirectMessages() feeds the channels themselves
// into State, so holding a second copy here would be a cache of a cache — the
// sidebar needs nothing from this but the order. Ordering compares
// LastMessageID directly, those being ULIDs, which sort chronologically as
// strings, so nothing has to be parsed. The order is a snapshot — an incoming
// message marks its row unread but doesn't re-sort the sidebar under the user
// mid-read; the next refresh picks the new order up.
func sortConversations(channels []*revoltgo.Channel) []string {
	channels = slices.DeleteFunc(channels, func(channel *revoltgo.Channel) bool {
		return channel == nil || (channel.ChannelType == revoltgo.ChannelTypeDM && !channel.Active)
	})
	slices.SortStableFunc(channels, func(x, y *revoltgo.Channel) int {
		return strings.Compare(lastActivity(y), lastActivity(x))
	})

	ids := make([]string, len(channels))
	for i, channel := range channels {
		ids[i] = channel.ID
	}
	return ids
}

// lastActivity returns a channel's newest message ID, or "" when it has none —
// which sorts an empty conversation to the bottom.
func lastActivity(channel *revoltgo.Channel) string {
	if channel.LastMessageID != nil {
		return *channel.LastMessageID
	}
	return ""
}

// resolveRecipients pulls the users behind a DM list into State so each row can
// be titled. Runs off the UI thread, bounded by authorFetchWorkers so a long DM
// list doesn't open a connection per conversation. Failures are logged and left
// alone: the row falls back to a generic title rather than going missing.
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

	var wg sync.WaitGroup
	slots := make(chan struct{}, authorFetchWorkers)
	for _, id := range missing {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			if _, err := session.User(id); err != nil {
				log.Printf("fetch dm recipient %s: %v", id, err)
			}
		}()
	}
	wg.Wait()
}
