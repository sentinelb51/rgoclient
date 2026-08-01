package app

import (
	"log"
	"slices"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// buildUI assembles the four-column layout: servers | channels | messages |
// members. The fill row keeps the sections flush for the flat look; only the
// message area (index 2) stretches, the rest stay at their fixed widths.
func (a *App) buildUI() fyne.CanvasObject {
	return ui.NewFillRow(2,
		a.buildServerList(),
		a.buildChannelList(),
		a.buildMessageArea(),
		a.buildMemberList(),
	)
}

/* Server sidebar */

// buildServerList builds the server icon sidebar: fixed home and settings buttons
// bookend the scrolling icons, each set off by a short separator. They sit
// outside the scroll, so they stay put when the list grows tall enough to scroll.
func (a *App) buildServerList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ServerListBackground)

	a.homeButton = ui.NewSidebarButton(fynetheme.HomeIcon(), a.selectHome)
	settings := ui.NewSidebarButton(fynetheme.SettingsIcon(), a.openSettings)

	top := container.NewVBox(
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		container.NewCenter(a.homeButton),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		ui.NewSidebarSeparator(),
	)
	bottom := container.NewVBox(
		ui.NewSidebarSeparator(),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
		container.NewCenter(settings),
		ui.VerticalSpacer(theme.Sizes.CategorySpacing),
	)

	a.refreshServerList()
	content := container.NewBorder(top, bottom, nil, nil, container.NewVScroll(a.serverList))

	return ui.NewFixedWidthContainer(theme.Sizes.ServerSidebarWidth, background, content)
}

// refreshServerList rebuilds the server icons from the current server list.
func (a *App) refreshServerList() {
	a.serverList.Objects = nil

	for _, serverID := range a.serverIDs {
		server := a.session.State.Server(serverID)
		if server == nil {
			continue
		}

		w := ui.NewServerWidget(a.images, server, func() { a.selectServer(serverID) })
		w.SetSelected(serverID == a.currentServerID)

		// Added bare, not wrapped in a Center: ServerWidget already centres its own
		// icon, and keeping the widget at the top level lets syncServerSelection
		// find it without unwrapping.
		a.serverList.Add(w)
	}

	// The join button reads as one more server icon at the end of the list, so it
	// lives inside the scroll rather than in the fixed bookends. Objects are
	// rebuilt wholesale here, hence re-adding it every time; the selection sync
	// skips it because it isn't a ServerWidget.
	a.serverList.Add(ui.NewSidebarButton(fynetheme.ContentAddIcon(), a.showJoinServer))
	a.serverList.Refresh()
}

// openSettings opens the settings window. A second open focuses the existing
// window rather than spawning another.
func (a *App) openSettings() {
	if a.settingsWindow != nil {
		a.settingsWindow.RequestFocus()
		return
	}

	window := a.fyne.NewWindow("Settings")
	a.settingsWindow = window
	window.SetOnClosed(func() { a.settingsWindow = nil })

	// todo: actual settings
	heading := widget.NewLabelWithStyle("Settings", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	note := widget.NewLabelWithStyle("Work in progress.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	window.SetContent(container.NewCenter(container.NewVBox(heading, note)))

	window.Resize(fyne.NewSize(420, 320))
	window.CenterOnScreen()
	a.styleNativeChrome(window)
	window.Show()
}

/* Channel sidebar */

// buildChannelList builds the channel sidebar with its server-name header.
func (a *App) buildChannelList() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.ChannelListBackground)

	name := "Server"
	if s := a.currentServer(); s != nil {
		name = s.Name
	}
	a.serverHeader = widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.serverHeader.Truncation = fyne.TextTruncateEllipsis

	a.refreshChannelList()

	pad := theme.Sizes.ChannelSidebarPadding
	scroll := container.NewBorder(nil, nil, ui.HorizontalSpacer(pad), ui.HorizontalSpacer(pad),
		container.NewVScroll(a.channelList))
	content := container.NewBorder(container.NewPadded(a.serverHeader), nil, nil, nil, scroll)

	return ui.NewFixedWidthContainer(theme.Sizes.ChannelSidebarWidth, background, content)
}

// refreshChannelList rebuilds the channel rows for the current server, grouping
// channels under their categories — or, in the home view, the flat list of
// cached direct messages and groups, which has no categories to group under.
func (a *App) refreshChannelList() {
	a.channelList.Objects = nil

	if a.homeSelected {
		for _, channelID := range a.dmChannels {
			if w := a.newChannelRow(channelID); w != nil {
				a.channelList.Add(w)
			}
		}
		a.channelList.Refresh()
		return
	}

	server := a.currentServer()
	if server == nil {
		a.channelList.Refresh()
		return
	}

	categorized := make(map[string]bool)
	for _, cat := range server.Categories {
		for _, id := range cat.Channels {
			categorized[id] = true
		}
	}

	// Uncategorized channels come first.
	for _, channelID := range server.Channels {
		if categorized[channelID] {
			continue
		}
		if w := a.newChannelRow(channelID); w != nil {
			a.channelList.Add(w)
		}
	}

	for i, cat := range server.Categories {
		key := server.ID + ":" + cat.ID
		category := ui.NewCategoryWidget(cat.Title, func(collapsed bool) {
			a.collapsedCategories[key] = collapsed
		})
		category.SetFirst(i == 0)

		var rows []fyne.CanvasObject
		for _, channelID := range cat.Channels {
			if w := a.newChannelRow(channelID); w != nil {
				rows = append(rows, w)
			}
		}

		a.channelList.Add(category)
		for _, row := range rows {
			a.channelList.Add(row)
		}

		category.SetChannels(rows, a.channelList)
		if a.collapsedCategories[key] {
			category.SetCollapsed(true)
		}
	}

	a.channelList.Refresh()
}

// newChannelRow builds a channel row reflecting its current state, or nil when
// State doesn't know the channel.
func (a *App) newChannelRow(channelID string) *ui.ChannelWidget {
	channel := a.stateChannel(channelID)
	if channel == nil {
		return nil
	}

	w := ui.NewChannelWidget(a.deps(), channel, func() { a.selectChannel(channelID) })
	w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])

	return w
}

/* Selection */

// selectServer switches to a server and selects its first channel. Re-clicking
// the current server is a no-op, which would otherwise rebuild both sidebars and
// yank the view to the first channel.
func (a *App) selectServer(serverID string) {
	if a.currentServerID == serverID && !a.homeSelected {
		return
	}

	a.homeSelected = false
	a.currentServerID = serverID
	server := a.currentServer()
	if server == nil {
		return
	}

	a.syncServerSelection(serverID)
	a.setHeader(a.serverHeader, server.Name)
	a.refreshChannelList()
	a.refreshMemberList()

	if len(server.Channels) > 0 {
		a.selectChannel(server.Channels[0])
		return
	}
	a.clearChannelSelection()
}

// selectChannel switches to a channel, acknowledging unreads and showing its
// messages from cache when available.
func (a *App) selectChannel(channelID string) {
	if a.currentChannelID == channelID {
		return
	}

	unread := a.unreadChannels[channelID]
	a.currentChannelID = channelID

	channel := a.currentChannel()
	a.setChannelGlyph(channel)
	if channel != nil {
		a.setHeader(a.channelHeader, util.ChannelName(a.session, channel))
		if unread && channel.LastMessageID != nil {
			delete(a.unreadChannels, channelID)
			a.scheduleAck(channelID, *channel.LastMessageID)
		}
	}

	a.syncChannelList()
	a.refreshMentionCandidates()
	a.focusInput() // so the user can type straight away

	if cached := a.messages.Get(channelID); len(cached) > 0 {
		a.displayMessages(cached)
		return
	}

	a.showStatus("Loading messages...")
	a.loadChannelMessages(channelID)
}

// clearChannelSelection deselects the current channel and clears the view.
func (a *App) clearChannelSelection() {
	a.currentChannelID = ""
	a.clearMessages()
	a.setHeader(a.channelHeader, "")
	a.setChannelGlyph(nil)
	a.syncChannelList()
}

// syncServerSelection updates the highlighted server icon. The home button is
// part of the same one-of-N selection, so it is cleared or lit here too rather
// than by whichever handler happens to run.
func (a *App) syncServerSelection(selectedID string) {
	if a.homeButton != nil {
		a.homeButton.SetSelected(a.homeSelected)
	}
	for _, obj := range a.serverList.Objects {
		if w, ok := obj.(*ui.ServerWidget); ok {
			w.SetSelected(w.Server.ID == selectedID)
		}
	}
}

// syncChannelList refreshes the selection and unread state of every channel row.
func (a *App) syncChannelList() {
	for _, obj := range a.channelList.Objects {
		if w, ok := obj.(*ui.ChannelWidget); ok {
			w.SetState(w.Channel.ID == a.currentChannelID, a.unreadChannels[w.Channel.ID])
		}
	}
}

// refreshChannelRow updates a single channel row, repainting only that widget.
// Used on the per-message hot path, so an incoming message in a background
// channel doesn't refresh the entire sidebar.
func (a *App) refreshChannelRow(channelID string) {
	for _, obj := range a.channelList.Objects {
		if w, ok := obj.(*ui.ChannelWidget); ok && w.Channel.ID == channelID {
			w.SetState(channelID == a.currentChannelID, a.unreadChannels[channelID])
			return
		}
	}
}

// setHeader updates a header label if it exists.
func (a *App) setHeader(label *widget.Label, text string) {
	if label != nil {
		label.SetText(text)
	}
}

// channelName returns the current channel's display name, or a fallback.
func (a *App) channelName() string {
	if channel := a.currentChannel(); channel != nil {
		return util.ChannelName(a.session, channel)
	}

	return "channel"
}

/* The home view */

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
// and returns only their IDs: DirectMessages() feeds the channels themselves into
// State, so keeping a second copy would be a cache of a cache. LastMessageID is
// compared directly, ULIDs sorting chronologically as strings. The order is a
// snapshot — a new message marks its row unread but doesn't re-sort the sidebar
// under the user mid-read; the next refresh picks the new order up.
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
