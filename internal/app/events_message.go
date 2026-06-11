package app

import "github.com/sentinelb51/revoltgo"

// onMessage caches an incoming message, resolves its author for display, and
// either appends it to the open channel or marks the channel unread.
func (a *App) onMessage(_ *revoltgo.Session, event *revoltgo.EventMessage) {
	// Capture the predecessor at cache time: under a burst, several messages can
	// be cached before their UI callbacks run, so deriving it later would group
	// against the wrong message.
	prev := a.messageCache.Append(event.Channel, &event.Message)

	a.doOnUI(func() {

		// If message was sent by a user, resolve them
		if event.System == nil && event.Webhook == nil {
			a.ensureAuthor(a.channelServerID(event.Channel), event.Author)

		}

		if event.Channel == a.currentChannelID {
			a.appendMessage(&event.Message, prev)
			return
		}
		a.unreadChannels[event.Channel] = true
		a.refreshChannelRow(event.Channel)
	}, false)
}
