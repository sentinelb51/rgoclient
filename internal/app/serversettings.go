package app

// The server settings page's controller half: the cog that opens it, what each
// of its sections is drawn from, and the four actions they offer — creating a
// channel, revoking an invite, lifting a ban, and the two fetches behind the last
// two.
//
// None of those four has a gateway event that would repaint anything. Creating a
// channel is the exception and needs no help: ChannelCreated already arrives and
// already rebuilds the sidebar. The other three are answered by asking again,
// which is why the page's lists are fetched per open rather than cached
// anywhere — see ui/settings_server.go.

import (
	"errors"
	"log"
	"slices"
	"strings"

	"RGOClient/internal/client"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

// manageable is what any of the page's privileged sections takes. The cog is
// offered where at least one is held: a page with only Overview and a channel
// list on it is a reading of the sidebar rather than settings.
const manageable = domain.PermissionManageChannel |
	domain.PermissionManageServer |
	domain.PermissionManageRole |
	domain.PermissionManagePermissions |
	domain.PermissionBanMembers

/* Opening and closing */

// canManageServer reports whether the open server has anything on its settings
// page for this account. The owner holds everything, so this is only ever a
// question about somebody else.
func (a *App) canManageServer(serverID string) bool {
	if serverID == "" {
		return false
	}

	return a.store.ServerPermissions(serverID)&manageable != 0
}

// syncServerCog shows or hides the cog for whatever is selected now. Called
// wherever the selection moves, and again when a role change could have moved
// what the account may do without the selection moving at all.
func (a *App) syncServerCog() {
	if a.serverCog == nil {
		return
	}

	if a.canManageServer(a.currentServerID) {
		a.serverCog.Show()
		return
	}

	a.serverCog.Hide()

	// A permission lost while the page was open takes the page with it: every
	// section it could still be showing is one this account may no longer reach.
	if a.serverSettings != nil && a.serverSettings.IsOpen() && a.serverSettingsID == a.currentServerID {
		a.closeServerSettings()
	}
}

// openServerSettings shows the open server's settings over the client. Call on
// the UI thread.
func (a *App) openServerSettings() {
	if a.serverSettings == nil || !a.canManageServer(a.currentServerID) {
		return
	}

	a.closeOverlay() // a lightbox left up would draw over the page it was opened from
	a.closeSettings()

	a.serverSettingsID = a.currentServerID
	a.serverSettings.Open()
	a.bindKeys()
}

// closeServerSettings takes the layer down. Call on the UI thread.
func (a *App) closeServerSettings() {
	if a.serverSettings == nil || !a.serverSettings.IsOpen() {
		return
	}

	a.serverSettings.Close()
	a.serverSettingsID = ""
	a.pendingRoleID = "" // a role created here has nowhere left to be opened
	a.bindKeys()
	a.focusInput()
}

// serverSettingsOpen reports whether the page is covering the client, for
// bindKeys and for the handlers that repaint it.
func (a *App) serverSettingsOpen() bool {
	return a.serverSettings != nil && a.serverSettings.IsOpen()
}

// refreshServerSettings repaints the page under a server that has changed — a
// rename, a new icon, a channel added or removed, a role that moved what this
// account may do. A server left while the page is up closes it instead: every
// section is about a server this account is no longer in.
func (a *App) refreshServerSettings() {
	if !a.serverSettingsOpen() {
		return
	}

	if _, ok := a.store.Server(a.serverSettingsID); !ok {
		a.closeServerSettings()
		return
	}

	a.serverSettings.RefreshFromStore()
}

/* The hooks */

func (a *App) serverSettingsHooks() ui.ServerSettingsHooks {
	return ui.ServerSettingsHooks{
		Deps:    a.deps(),
		Close:   a.closeServerSettings,
		Confirm: a.confirm,

		Server: a.serverSummary,
		Can:    a.canOnServerSettings,

		SetName:        a.setServerName,
		SetDescription: a.setServerDescription,

		ChangeIcon:   a.changeServerIcon,
		RemoveIcon:   a.removeServerIcon,
		ChangeBanner: a.changeServerBanner,
		RemoveBanner: a.removeServerBanner,

		Channels:      a.serverSettingsChannels,
		CreateChannel: a.promptCreateChannel,
		EditChannel:   a.editChannel,
		MoveChannel:   a.moveChannel,

		CreateCategory: a.promptCreateCategory,
		RenameCategory: a.promptRenameCategory,
		MoveCategory:   a.moveCategory,
		DeleteCategory: a.deleteCategory,

		Roles:      a.serverSettingsRoles,
		CreateRole: a.promptCreateRole,

		SetRoleName:        a.setRoleName,
		SetRoleColor:       a.setRoleColor,
		SetRoleHoist:       a.setRoleHoist,
		SetRolePermissions: a.setRolePermissions,

		SetDefaultPermissions: a.setDefaultPermissions,

		MoveRole:   a.moveRole,
		DeleteRole: a.deleteRole,

		LoadInvites:  a.loadServerInvites,
		CopyInvite:   a.copyInviteLink,
		RevokeInvite: a.revokeInvite,
		OpenProfile:  a.OnUserTapped,

		LoadBans: a.loadServerBans,
		LiftBan:  a.liftBan,
	}
}

// canOnServerSettings answers the page's permission questions against the server
// it was opened on rather than the one selected — the two are the same today,
// the page covering the sidebar that would change it, and reading the field is
// what keeps that true if it ever stops being.
func (a *App) canOnServerSettings(permission domain.Permission) bool {
	return a.store.ServerPermissions(a.serverSettingsID).Has(permission)
}

/* Overview */

// serverSummary is what the Overview section states. Every field is read from the
// store, so a server left while the page is open answers false and the section
// says so instead.
func (a *App) serverSummary() (ui.ServerSummary, bool) {
	server, ok := a.store.Server(a.serverSettingsID)
	if !ok {
		return ui.ServerSummary{}, false
	}

	summary := ui.ServerSummary{
		ID:          server.ID,
		Name:        server.Name,
		IconURL:     server.IconURL,
		BannerURL:   server.BannerURL,
		Description: server.Description,
		Owner:       a.store.UserName(server.OwnerID),
		Channels:    len(server.Channels),
	}

	if summary.Owner == "" {
		summary.Owner = "Unknown" // somebody who has never been fetched, which an owner rarely is
	}
	if created, err := util.Timestamp(server.ID); err == nil {
		summary.Created = util.FullDate(created)
	}

	return summary, true
}

// setServerName renames the server the page is open on. An empty name is the one
// refusal worth naming here: the request is never made, so "could not" would be
// untrue — the same reason setDisplayName says it rather than letting
// notifyFailure speak. What took is drawn by the ServerUpdate that follows.
func (a *App) setServerName(name string) {
	serverID := a.serverSettingsID

	if strings.TrimSpace(name) == "" {
		a.notify(ui.ToneWarning, "A server needs a name.")
		return
	}

	a.background(
		func() error { return a.client.SetServerName(serverID, name) },
		a.notifyFailure("rename server "+serverID, "Could not rename the server."),
	)
}

// setServerDescription publishes the blurb, blank removing it. Nothing to refuse:
// an empty description is a description taken off.
func (a *App) setServerDescription(description string) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.SetServerDescription(serverID, description) },
		a.notifyFailure("describe server "+serverID, "Could not save that description."),
	)
}

// changeServerIcon and changeServerBanner each ask for a picture and hang it on
// the server the page is open on. Both come back as a ServerUpdate, which is what
// repaints the rail, the strip above the page's own rail and the row that offers
// to remove one — so nothing is recorded here, exactly as this account's own
// avatar does nothing on the way out.
func (a *App) changeServerIcon() {
	serverID := a.serverSettingsID

	a.choosePicture("Choose a server icon", func(path, name string) {
		a.background(
			func() error { return a.client.SetServerIcon(serverID, path, name) },
			a.notifyFailure("set icon on server "+serverID, "Could not change the icon. It may be too large."),
		)
	})
}

func (a *App) removeServerIcon() {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.RemoveServerIcon(serverID) },
		a.notifyFailure("remove icon on server "+serverID, "Could not remove the icon."),
	)
}

func (a *App) changeServerBanner() {
	serverID := a.serverSettingsID

	a.choosePicture("Choose a server banner", func(path, name string) {
		a.background(
			func() error { return a.client.SetServerBanner(serverID, path, name) },
			a.notifyFailure("set banner on server "+serverID, "Could not change the banner. It may be too large."),
		)
	})
}

func (a *App) removeServerBanner() {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.RemoveServerBanner(serverID) },
		a.notifyFailure("remove banner on server "+serverID, "Could not remove the banner."),
	)
}

/* Channels */

// channelBucket is one stretch of the sidebar: a category and the channels filed
// under it, or — for the one with no ID, which is always first — the channels no
// category claimed.
//
// The IDs are the server's own, visible or not. Publishing an arrangement
// replaces the whole of it, so a channel hidden from this account has to be
// carried through a move that passes it: what is left out is filed out.
type channelBucket struct {
	ID    string
	Title string

	Channels []string
}

// channelBuckets is the server's channels arranged as the sidebar draws them:
// whatever no category claimed first, then each category in the server's own
// order — the walk refreshChannelList makes.
func (a *App) channelBuckets(serverID string) ([]channelBucket, bool) {
	server, ok := a.store.Server(serverID)
	if !ok {
		return nil, false
	}

	claimed := make(map[string]bool)
	for _, category := range server.Categories {
		for _, channelID := range category.Channels {
			claimed[channelID] = true
		}
	}

	loose := make([]string, 0, len(server.Channels))
	for _, channelID := range server.Channels {
		if !claimed[channelID] {
			loose = append(loose, channelID)
		}
	}

	buckets := make([]channelBucket, 0, len(server.Categories)+1)
	buckets = append(buckets, channelBucket{Channels: loose})

	for _, category := range server.Categories {
		buckets = append(buckets, channelBucket{
			ID:       category.ID,
			Title:    category.Title,
			Channels: slices.Clone(category.Channels),
		})
	}

	return buckets, true
}

// serverSettingsChannels is that arrangement as the section draws it, so the page
// agrees with the column it covers. A channel the account cannot see is left out
// of both — but only of what is *drawn*: it keeps its place in what is sent.
//
// The first entry is the uncategorised one and is returned even when it is empty:
// it is where a channel taken out of every category lands, and the section counts
// on the positions being the arrangement's own.
func (a *App) serverSettingsChannels() []ui.ServerCategoryEntry {
	buckets, ok := a.channelBuckets(a.serverSettingsID)
	if !ok {
		return nil
	}

	entries := make([]ui.ServerCategoryEntry, 0, len(buckets))
	for _, bucket := range buckets {
		entry := ui.ServerCategoryEntry{ID: bucket.ID, Title: bucket.Title}

		for _, channelID := range bucket.Channels {
			channel, ok := a.store.Channel(channelID)
			if !ok || !a.canViewChannel(channel) {
				continue
			}

			entry.Channels = append(entry.Channels, ui.ServerChannelEntry{
				ID:          channel.ID,
				Name:        channel.Name,
				Description: channel.Description,
				Kind:        channel.Kind,
				Editable:    a.canEditChannel(channel.ID),
			})
		}

		entries = append(entries, entry)
	}

	return entries
}

/* Categories */

// moveChannel moves a channel one place through the arrangement, which is also
// how it changes category: one at the head of a category moving up leaves it for
// the end of whatever is above, and one at the end of the last category has
// nowhere below to go.
//
// The step is taken past what is *visible* rather than past the next ID. Moving a
// channel over a neighbour the reader cannot see would read as the button doing
// nothing, twice.
//
// A channel no category claims cannot be reordered among the others — Revolt
// stores no order for those, only the categories' own — so from there the only
// move is down, into the first category. Call on the UI thread.
func (a *App) moveChannel(channelID string, up bool) {
	serverID := a.serverSettingsID

	buckets, ok := a.channelBuckets(serverID)
	if !ok {
		return
	}

	at, index := findChannel(buckets, channelID)
	if at == -1 {
		return
	}

	target, position := at, -1
	switch {
	case up && at == 0:
		return
	case up:
		if to := a.nearestVisible(buckets[at].Channels, index, -1); to != -1 {
			position = to
			break
		}
		target, position = at-1, len(buckets[at-1].Channels)
	case at == 0:
		if len(buckets) < 2 {
			return
		}
		target, position = 1, 0
	default:
		if to := a.nearestVisible(buckets[at].Channels, index, 1); to != -1 {
			// After the removal below the neighbour has shifted down one, so its old
			// index is the position *after* it.
			position = to
			break
		}
		if at == len(buckets)-1 {
			return
		}
		target, position = at+1, 0
	}

	buckets[at].Channels = slices.Delete(buckets[at].Channels, index, index+1)
	buckets[target].Channels = slices.Insert(buckets[target].Channels, position, channelID)

	a.publishCategories(serverID, buckets, "Could not move that channel.")
}

// moveCategory swaps a category with the neighbour above or below it. The
// uncategorised bucket is not one and is never swapped: it is first because it is
// what no category claimed, not because of where it sits. Call on the UI thread.
func (a *App) moveCategory(categoryID string, up bool) {
	serverID := a.serverSettingsID

	buckets, ok := a.channelBuckets(serverID)
	if !ok {
		return
	}

	at := slices.IndexFunc(buckets, func(bucket channelBucket) bool { return bucket.ID == categoryID })
	if at <= 0 {
		return
	}

	swap := at + 1
	if up {
		swap = at - 1
	}
	if swap < 1 || swap >= len(buckets) {
		return
	}

	buckets[at], buckets[swap] = buckets[swap], buckets[at]

	a.publishCategories(serverID, buckets, "Could not move that category.")
}

// promptCreateCategory raises the card that names a new category. Call on the UI
// thread.
func (a *App) promptCreateCategory() {
	serverID := a.serverSettingsID
	if !a.canOnServerSettings(domain.PermissionManageChannel) {
		return
	}

	dialog := ui.NewPromptDialog(ui.Prompt{
		Title:  "Create a category",
		Action: "Create",
		Busy:   "Creating...",
		Fields: []ui.PromptField{{Label: "Category name", Placeholder: "Text channels"}},
		OnSubmit: func(values []string) {
			a.closeOverlay()
			a.createCategory(serverID, values[0])
		},
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.window.Canvas().Focus(dialog.Entry)
}

// createCategory adds an empty category at the end of the order. It carries no
// ID: which one it gets is the client's to mint, Revolt naming only the channels
// it holds.
func (a *App) createCategory(serverID, title string) {
	if strings.TrimSpace(title) == "" {
		a.notify(ui.ToneWarning, "A category needs a name.")
		return
	}

	buckets, ok := a.channelBuckets(serverID)
	if !ok {
		return
	}

	buckets = append(buckets, channelBucket{Title: title})

	a.publishCategories(serverID, buckets, "Could not create that category.")
}

// promptRenameCategory raises the card that renames one, opened on what it is
// called now. Call on the UI thread.
func (a *App) promptRenameCategory(categoryID string) {
	serverID := a.serverSettingsID
	if !a.canOnServerSettings(domain.PermissionManageChannel) {
		return
	}

	buckets, ok := a.channelBuckets(serverID)
	if !ok {
		return
	}

	at := slices.IndexFunc(buckets, func(bucket channelBucket) bool { return bucket.ID == categoryID })
	if at <= 0 {
		return
	}

	dialog := ui.NewPromptDialog(ui.Prompt{
		Title:  "Rename category",
		Action: "Rename",
		Busy:   "Renaming...",
		Fields: []ui.PromptField{{Label: "Category name", Value: buckets[at].Title}},
		OnSubmit: func(values []string) {
			a.closeOverlay()
			a.renameCategory(serverID, categoryID, values[0])
		},
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.window.Canvas().Focus(dialog.Entry)
}

func (a *App) renameCategory(serverID, categoryID, title string) {
	if strings.TrimSpace(title) == "" {
		a.notify(ui.ToneWarning, "A category needs a name.")
		return
	}

	buckets, ok := a.channelBuckets(serverID)
	if !ok {
		return
	}

	at := slices.IndexFunc(buckets, func(bucket channelBucket) bool { return bucket.ID == categoryID })
	if at <= 0 {
		return
	}

	buckets[at].Title = title

	a.publishCategories(serverID, buckets, "Could not rename that category.")
}

// deleteCategory drops one. Its channels are not touched: a channel no category
// claims is uncategorised by that fact alone, so they arrive back at the top of
// the sidebar rather than having to be moved out first. Call on the UI thread.
func (a *App) deleteCategory(categoryID string) {
	serverID := a.serverSettingsID

	buckets, ok := a.channelBuckets(serverID)
	if !ok {
		return
	}

	at := slices.IndexFunc(buckets, func(bucket channelBucket) bool { return bucket.ID == categoryID })
	if at <= 0 {
		return
	}

	buckets = slices.Delete(buckets, at, at+1)

	a.publishCategories(serverID, buckets, "Could not delete that category.")
}

// publishCategories sends an arrangement. The uncategorised bucket is left out of
// what is sent: it is not a category but what no category claimed, and Revolt
// derives it the same way this does.
//
// Nothing is recorded here. The edit returns as a ServerUpdate carrying the whole
// structure, which repaints the sidebar and this page from the store — the path a
// server's own name already takes.
func (a *App) publishCategories(serverID string, buckets []channelBucket, failure string) {
	categories := make([]domain.Category, 0, len(buckets))
	for _, bucket := range buckets[1:] {
		categories = append(categories, domain.Category{
			ID:       bucket.ID,
			Title:    bucket.Title,
			Channels: bucket.Channels,
		})
	}

	a.background(
		func() error { return a.client.SetServerCategories(serverID, categories) },
		a.notifyFailure("set categories on server "+serverID, "%s", failure),
	)
}

// findChannel locates a channel in the arrangement, as the bucket holding it and
// its place in that bucket.
func findChannel(buckets []channelBucket, channelID string) (int, int) {
	for at, bucket := range buckets {
		if index := slices.Index(bucket.Channels, channelID); index != -1 {
			return at, index
		}
	}

	return -1, -1
}

// nearestVisible walks a bucket from index in the given direction and reports the
// first channel this account can see, or -1 for none — which is what makes a move
// step over the hidden ones rather than trade places with one.
func (a *App) nearestVisible(channels []string, index, step int) int {
	for at := index + step; at >= 0 && at < len(channels); at += step {
		if channel, ok := a.store.Channel(channels[at]); ok && a.canViewChannel(channel) {
			return at
		}
	}

	return -1
}

// promptCreateChannel raises the card that names a new channel. The same card the
// sidebar's edit uses, asked from empty and with the one row an edit cannot
// offer — see ui.NewChannelCreateDialog. Call on the UI thread.
func (a *App) promptCreateChannel() {
	serverID := a.serverSettingsID
	if !a.canOnServerSettings(domain.PermissionManageChannel) {
		return
	}

	dialog := ui.NewChannelCreateDialog(
		func(created ui.ChannelSettings) { a.submitChannelCreate(serverID, created) },
		a.closeOverlay,
	)

	a.showOverlay(dialog.Content)
	a.channelDialog = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// submitChannelCreate sends the request and leaves the card up until it takes, so
// a refusal can be corrected in the fields it came from. The channel itself
// arrives as ChannelCreated and is filed by the handler that already exists;
// what the response is needed for is selecting the one just made.
func (a *App) submitChannelCreate(serverID string, created ui.ChannelSettings) {
	epoch := a.epoch

	go func() {
		channelID, err := a.client.CreateChannel(serverID, client.ChannelCreate{
			Name:        created.Name,
			Description: created.Description,
			Voice:       created.Voice != nil && *created.Voice,
			NSFW:        created.NSFW,
		})

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("create channel in server %s: %v", serverID, err)
				if a.channelDialog != nil {
					a.channelDialog.Fail(createChannelFailure(err))
				}
				return
			}

			a.closeOverlay()
			a.notify(ui.ToneInfo, "#%s was created.", created.Name)

			// The sidebar is the point of having made one, and it is behind the page —
			// so the page comes down and the new channel is opened, rather than the
			// reader being left on a list with a row added to it. The ChannelCreated
			// event puts it in the sidebar; this only picks it.
			a.closeServerSettings()
			a.selectChannel(channelID)
		}, false)
	}()
}

// createChannelFailure is what the card says about a refusal. As with an edit, an
// empty name is the only one the reader can act on.
func createChannelFailure(err error) string {
	if errors.Is(err, client.ErrChannelNameEmpty) {
		return "Give the channel a name."
	}

	return "Could not create that channel."
}

/* Roles */

// serverSettingsRoles is every role the server defines, most senior first, each
// marked with whether this account outranks it — Revolt refuses an edit to a role
// at or above the editor's own rank, so the rest are listed and not opened.
//
// The default role is appended last and is not a role: it is what everybody holds
// before any of them applies, so nothing outranks it and the only thing it takes
// to edit is ManagePermissions.
func (a *App) serverSettingsRoles() []ui.ServerRoleEntry {
	serverID := a.serverSettingsID

	roles := a.store.ServerRoles(serverID)
	ranking := a.serverRanking(serverID)

	entries := make([]ui.ServerRoleEntry, 0, len(roles)+1)
	for _, role := range roles {
		entries = append(entries, ui.ServerRoleEntry{
			ID:        role.ID,
			Name:      role.Name,
			Color:     role.Color,
			ColorText: role.ColorText,
			Allow:     role.Allow,
			Deny:      role.Deny,
			Hoist:     role.Hoist,
			Editable:  role.Rank > ranking,
		})
	}

	server, ok := a.store.Server(serverID)
	if !ok {
		return entries
	}

	return append(entries, ui.ServerRoleEntry{
		ID:       defaultRoleID,
		Name:     "Everyone",
		Allow:    server.DefaultPermissions,
		Default:  true,
		Editable: a.canOnServerSettings(domain.PermissionManagePermissions),
	})
}

// defaultRoleID names the default role where a role ID is wanted. It is Revolt's
// own spelling for it in the permission routes, and no role can collide with it:
// a role ID is a ULID.
const defaultRoleID = "default"

// setDefaultPermissions publishes what everybody holds. Like a role's own, it
// comes back as an event the store answers for — a server update rather than a
// role one, the default living on the server itself.
func (a *App) setDefaultPermissions(allow domain.Permission) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.SetDefaultPermissions(serverID, allow) },
		a.notifyFailure("set default permissions on server "+serverID,
			"Could not change what everybody may do."),
	)
}

// promptCreateRole raises the card that names a new role. Call on the UI thread.
func (a *App) promptCreateRole() {
	serverID := a.serverSettingsID
	if !a.canOnServerSettings(domain.PermissionManageRole) {
		return
	}

	dialog := ui.NewPromptDialog(ui.Prompt{
		Title:  "Create a role",
		Action: "Create",
		Busy:   "Creating...",
		Fields: []ui.PromptField{{Label: "Role name", Placeholder: "Moderator"}},
		OnSubmit: func(values []string) {
			a.closeOverlay()
			a.createRole(serverID, values[0])
		},
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.window.Canvas().Focus(dialog.Entry)
}

// createRole makes the role and marks it to be opened. The role reaches the page
// as a role update for one State has never heard of, which is also when it can
// first be drawn — so the ID the response carries is held rather than acted on,
// exactly as a join holds pendingJoin.
func (a *App) createRole(serverID, name string) {
	epoch := a.epoch

	go func() {
		roleID, err := a.client.CreateRole(serverID, name)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("create role in server %s: %v", serverID, err)
				a.notify(ui.ToneDanger, "Could not create that role.")
				return
			}

			a.pendingRoleID = roleID
			a.notify(ui.ToneInfo, "%s was created.", name)
		}, false)
	}()
}

// openPendingRole drills the page into a role just created, once the event
// carrying it has landed and the store can answer for it. Call on the UI thread.
func (a *App) openPendingRole(serverID string) {
	if a.pendingRoleID == "" || !a.serverSettingsOpen() || a.serverSettingsID != serverID {
		return
	}

	roleID := a.pendingRoleID
	a.pendingRoleID = ""
	a.serverSettings.OpenRole(roleID)
}

// The four setters. None of them reports back: each returns as a role event, and
// the page is rebuilt from the store on the way past — the same path a server's
// own name takes.
func (a *App) setRoleName(roleID, name string) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.SetRoleName(serverID, roleID, name) },
		a.notifyFailure("rename role "+roleID, "Could not rename that role."),
	)
}

func (a *App) setRoleColor(roleID, colour string) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.SetRoleColor(serverID, roleID, colour) },
		a.notifyFailure("colour role "+roleID, "Could not change that role's colour."),
	)
}

func (a *App) setRoleHoist(roleID string, hoist bool) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.SetRoleHoist(serverID, roleID, hoist) },
		a.notifyFailure("hoist role "+roleID, "Could not change how that role is listed."),
	)
}

func (a *App) setRolePermissions(roleID string, allow, deny domain.Permission) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.SetRolePermissions(serverID, roleID, allow, deny) },
		a.notifyFailure("set permissions on role "+roleID, "Could not change what that role may do."),
	)
}

// moveRole swaps a role with the neighbour above or below it. The route takes the
// server's whole order and refuses a partial one, so the swap is made here and
// the entire list sent.
func (a *App) moveRole(roleID string, up bool) {
	serverID := a.serverSettingsID

	roles := a.store.ServerRoles(serverID)
	at := slices.IndexFunc(roles, func(role domain.Role) bool { return role.ID == roleID })
	if at == -1 {
		return
	}

	swap := at + 1
	if up {
		swap = at - 1
	}
	if swap < 0 || swap >= len(roles) {
		return
	}

	order := make([]string, len(roles))
	for i, role := range roles {
		order[i] = role.ID
	}
	order[at], order[swap] = order[swap], order[at]

	a.background(
		func() error { return a.client.SetRoleRanks(serverID, order) },
		a.notifyFailure("reorder roles in server "+serverID, "Could not move that role."),
	)
}

// deleteRole removes one. The page has already gone back to the list, the role
// event only confirming what it did.
func (a *App) deleteRole(roleID string) {
	serverID := a.serverSettingsID

	a.background(
		func() error { return a.client.DeleteRole(serverID, roleID) },
		a.notifyFailure("delete role "+roleID, "Could not delete that role."),
	)
}

/* Invites */

// loadServerInvites fetches the server's invites and resolves what each row shows
// around the code: the channel it opens, and the account that made it. The
// resolution happens on the worker with the fetch — the store is safe off the UI
// thread — so the callback lands with nothing left to do.
//
// An invite outlives the reason anybody looked at its creator, so most of them
// name somebody this client has never drawn: they are fetched here rather than
// left blank, which is what the lazy author path does for a message nobody has
// scrolled to. One batch, because the fetch it joins is single-flighted already.
func (a *App) loadServerInvites(onLoaded func([]ui.ServerInviteEntry, error)) {
	serverID, epoch := a.serverSettingsID, a.epoch

	go func() {
		invites, err := a.client.ServerInvites(serverID)

		var entries []ui.ServerInviteEntry
		if err == nil {
			a.resolveInviteCreators(serverID, invites)

			entries = make([]ui.ServerInviteEntry, len(invites))
			for i, invite := range invites {
				creator := a.inviteCreator(serverID, invite.CreatorID)
				creator.Code = invite.Code
				creator.Channel = a.store.ChannelName(invite.ChannelID)
				creator.CreatorID = invite.CreatorID
				entries[i] = creator
			}
		} else {
			log.Printf("fetch invites for server %s: %v", serverID, err)
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			onLoaded(entries, err)
		}, false)
	}()
}

// resolveInviteCreators pulls in the accounts behind a list of invites that this
// client cannot name yet, one batch for the whole list. Blocking, and called from
// the worker the fetch runs on.
//
// A creator already known is skipped whether or not this server has a membership
// for them: somebody who made an invite and left is the common case, and asking
// for a membership that does not exist is a request per row that answers 404.
func (a *App) resolveInviteCreators(serverID string, invites []domain.ServerInvite) {
	var refs []client.AuthorRef

	seen := make(map[string]bool, len(invites))
	for _, invite := range invites {
		if invite.CreatorID == "" || seen[invite.CreatorID] || a.store.HasUser(invite.CreatorID) {
			continue
		}

		seen[invite.CreatorID] = true
		refs = append(refs, client.AuthorRef{ServerID: serverID, UserID: invite.CreatorID})
	}

	if len(refs) == 0 {
		return
	}

	a.client.ResolveAuthors(refs)
}

// inviteCreator fills in whoever made an invite, preferring the membership — a
// nickname, a per-server avatar and a role colour are what this server knows
// somebody by, and none of the three exists outside it. An account that could not
// be fetched answers empty, and the row drops the chip rather than drawing a
// nameless one.
func (a *App) inviteCreator(serverID, userID string) ui.ServerInviteEntry {
	if member, ok := a.store.Member(serverID, userID); ok {
		return ui.ServerInviteEntry{
			Creator:          member.Name,
			CreatorAvatarURL: member.AvatarURL,
			CreatorColor:     member.Color,
		}
	}
	if user, ok := a.store.User(userID); ok {
		return ui.ServerInviteEntry{Creator: user.Name, CreatorAvatarURL: user.AvatarURL}
	}

	return ui.ServerInviteEntry{}
}

// copyInviteLink puts one on the clipboard, the link rather than the code —
// which is what somebody pastes.
func (a *App) copyInviteLink(code string) {
	ui.CopyToClipboard(util.InviteLink(code))
	a.notify(ui.ToneInfo, "A link to that invite is on your clipboard.")
}

// revokeInvite deletes one. Nothing announces it, so the page re-asks: onDone is
// what tells it to.
func (a *App) revokeInvite(code string, onDone func(error)) {
	epoch := a.epoch

	go func() {
		err := a.client.DeleteInvite(code)
		if err != nil {
			log.Printf("delete invite %s: %v", code, err)
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err == nil {
				a.notify(ui.ToneInfo, "Invite %s was revoked.", code)
			}

			onDone(err)
		}, false)
	}()
}

/* Bans */

// loadServerBans fetches the server's ban list. Unlike an invite there is nothing
// to resolve: the route answers with the name and the picture, a banned account
// generally being one the store has never heard of.
func (a *App) loadServerBans(onLoaded func([]domain.Ban, error)) {
	serverID, epoch := a.serverSettingsID, a.epoch

	go func() {
		bans, err := a.client.ServerBans(serverID)
		if err != nil {
			log.Printf("fetch bans for server %s: %v", serverID, err)
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			onLoaded(bans, err)
		}, false)
	}()
}

// liftBan unbans somebody. As with a revoked invite, nothing announces it.
func (a *App) liftBan(userID string, onDone func(error)) {
	serverID, epoch := a.serverSettingsID, a.epoch

	go func() {
		err := a.client.UnbanMember(serverID, userID)
		if err != nil {
			log.Printf("unban %s from server %s: %v", userID, serverID, err)
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err == nil {
				a.notify(ui.ToneInfo, "That ban was lifted.")
			}

			onDone(err)
		}, false)
	}()
}
