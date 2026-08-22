package ui

// A server's settings: the same surface the client's own are drawn on
// (settings_shell.go), filled with what one server holds rather than with what
// the client does. Almost everything here is a *list* — the channels, the roles,
// the invites, the bans — where the client's settings are switches, which is what
// entryRow exists for.
//
// Two of those lists are *fetched*, and nothing is held between opens. Invites
// and bans are a request made as their section is mounted and again after
// anything changes them: Revolt announces a new channel and nothing else, so a
// lifted ban or a revoked invite is only ever seen by asking again. A fetch that
// lands after the reader has moved on has nothing left to fill, hence the
// generation counter. The channels and the roles are read from the store, which
// the gateway keeps current, so neither costs a request at all.

import (
	"fmt"
	"image/color"
	"math/bits"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* Sections */

// ServerSettingsSection identifies one entry in a server settings rail.
type ServerSettingsSection int

const (
	ServerSectionOverview ServerSettingsSection = iota
	ServerSectionChannels
	ServerSectionRoles
	ServerSectionInvites
	ServerSectionBans
)

var serverRailEntries = []railEntry{
	{int(ServerSectionOverview), "Overview", assets.ServerOverviewIcon},
	{int(ServerSectionChannels), "Channels", assets.ServerChannelsIcon},
	{int(ServerSectionRoles), "Roles", assets.ServerRolesIcon},
	{int(ServerSectionInvites), "Invites", assets.ServerInvitesIcon},
	{int(ServerSectionBans), "Bans", assets.ServerBansIcon},
}

// permissionFor is what a section takes to be listed at all — **any one** of the
// bits it names rather than all of them, Roles being two routes under two
// permissions and either worth listing it for. Overview and Channels take
// nothing, both being a reading of what the sidebar already shows, so the one
// privileged row of the Channels section asks for its own
// (PermissionManageChannel, at newChannelRow).
func permissionFor(section ServerSettingsSection) domain.Permission {
	switch section {
	case ServerSectionRoles:
		return domain.PermissionManageRole | domain.PermissionManagePermissions
	case ServerSectionInvites:
		return domain.PermissionManageServer
	case ServerSectionBans:
		return domain.PermissionBanMembers
	}

	return 0
}

/* What the page is drawn from */

// ServerSummary is what the Overview section draws, all of it read from the
// store: what that section can change goes back as a ServerUpdate, so nothing
// here is written to on the way out.
//
// Deliberately not a member count. Store.Members answers with whoever has been
// resolved rather than with the membership, so the number would be somewhere
// between one and the truth and would climb as the reader scrolled.
type ServerSummary struct {
	ID      string
	Name    string
	IconURL string

	// BannerURL is not drawn anywhere in this client. It is here so the row that
	// sets one knows whether there is one to remove.
	BannerURL string

	Description string
	Owner       string
	Created     string

	Channels int
}

// ServerChannelEntry is one of a server's channels as the list draws it.
type ServerChannelEntry struct {
	ID          string
	Name        string
	Description string

	Kind domain.ChannelKind

	// Editable marks the ones this account may change, the "Edit" button being left
	// off the rest rather than drawn to be refused.
	Editable bool
}

// ServerCategoryEntry is one stretch of the Channels section: a category and the
// channels under it, in the order the sidebar draws them. The one with no ID is
// the channels no category claimed, and is always first — as it is in the
// sidebar, and as Revolt derives it.
type ServerCategoryEntry struct {
	ID    string
	Title string

	Channels []ServerChannelEntry
}

// ServerRoleEntry is one of a server's roles as the list and the editor draw it.
// Allow and Deny are the role's own override rather than what somebody holding it
// ends up with: a bit in neither is inherited, which is the third state the
// editor offers.
type ServerRoleEntry struct {
	ID    string
	Name  string
	Color color.Color // nil for a role with no colour, or none that parses

	// ColorText is the colour as Revolt stores it, which is any CSS colour rather
	// than the hex the editor writes.
	ColorText string

	Allow domain.Permission
	Deny  domain.Permission

	Hoist bool

	// Default marks the one entry that is not a role: what every member holds
	// before any of them applies. It has no name to change, no colour, no place in
	// the order and no way to be deleted, and its permissions are a plain set —
	// there being nothing beneath it to inherit from.
	Default bool

	// Editable marks a role this account outranks. Revolt refuses an edit to a role
	// at or above the editor's own rank, so the rest are listed and not opened.
	// Seniority itself is the order of the slice rather than a field: what the page
	// draws is a role's place among the others, never its rank.
	Editable bool
}

// ServerInviteEntry is one outstanding invite as the list draws it, and the whole
// of what Revolt keeps about one: a code, the channel it opens and who made it.
// There is no expiry, no use count and no date — the code is not a ULID either,
// so nothing here can say when an invite was made or how long it has left.
//
// The two names are the controller's to resolve, and either may come back empty:
// a channel deleted, or a creator whose account is gone. CreatorID outlives the
// name — it is what the chip opens, and a profile is worth offering for somebody
// this client could not name.
type ServerInviteEntry struct {
	Code    string
	Channel string

	Creator          string
	CreatorID        string
	CreatorAvatarURL string

	// CreatorColor is the colour this server's roles give them, nil where they have
	// none — the chip's own, exactly as a role chip's is.
	CreatorColor color.Color
}

/* The page */

// ServerSettingsHooks is everything the page needs from the controller — plain
// functions, as SettingsHooks is, nothing else in the client needing any of them.
type ServerSettingsHooks struct {
	Deps Deps

	Close   func()
	Confirm func(Confirm)

	// Server is what the page is about, re-read on every build rather than passed
	// once: a rename or a new icon arrives as a gateway event and the page stays
	// open through it. Answering false is a server left while its settings were up.
	Server func() (ServerSummary, bool)

	// Can reports whether the account holds one permission across this server.
	// Which section needs which is permissionFor's to say — a section the account
	// cannot reach is left off the rail rather than drawn empty.
	Can func(permission domain.Permission) bool

	// SetName and SetDescription publish the two things Overview can change, blank
	// clearing the description. Neither reports back: the edit returns as a
	// ServerUpdate the store answers for, exactly as this account's own display
	// name does. What is too short to send is Revolt's limit, so the controller
	// holds it and says so itself.
	SetName        func(name string)
	SetDescription func(description string)

	// The server's two pictures. Each asks for a file and uploads it, so unlike the
	// two setters above they take no argument: what was picked is the controller's,
	// the picker being the OS one.
	ChangeIcon   func()
	RemoveIcon   func()
	ChangeBanner func()
	RemoveBanner func()

	/* Channels */

	// Channels is the whole arrangement rather than a list: the uncategorised
	// entry first, then every category in the server's own order.
	Channels      func() []ServerCategoryEntry
	CreateChannel func()
	EditChannel   func(channelID string)

	// MoveChannel moves one channel one place, across a category boundary where
	// that is what the next place is. As with a role, the route takes the server's
	// whole arrangement and the controller composes it.
	MoveChannel func(channelID string, up bool)

	// The category actions. All four publish the same arrangement MoveChannel
	// does, and none of them reports back: the edit returns as a server update the
	// store answers for.
	CreateCategory func()
	RenameCategory func(categoryID string)
	MoveCategory   func(categoryID string, up bool)
	DeleteCategory func(categoryID string)

	/* Roles */

	// Roles is read from the store rather than fetched: Ready carries every role a
	// server has, and the three role events keep them current.
	Roles      func() []ServerRoleEntry
	CreateRole func()

	// None of the four setters reports back. Each returns as a role event the store
	// answers for, the way a server's own name does — so what took is drawn from
	// the store on the rebuild that follows rather than from what was sent.
	SetRoleName        func(roleID, name string)
	SetRoleColor       func(roleID, colour string)
	SetRoleHoist       func(roleID string, hoist bool)
	SetRolePermissions func(roleID string, allow, deny domain.Permission)

	// SetDefaultPermissions publishes what every member holds before any role. A
	// route of its own and a plain set rather than an override, which is why it is
	// not SetRolePermissions with a reserved ID.
	SetDefaultPermissions func(allow domain.Permission)

	// MoveRole swaps a role with its neighbour. The route takes the server's whole
	// order and refuses a partial one, so the controller composes it.
	MoveRole   func(roleID string, up bool)
	DeleteRole func(roleID string)

	/* Invites */

	// LoadInvites and LoadBans fetch. Both may answer long after the section they
	// were asked for has closed — see cachedList for what happens to one that does.
	LoadInvites  func(onLoaded func(invites []ServerInviteEntry, err error))
	CopyInvite   func(code string)
	RevokeInvite func(code string, onDone func(err error))

	// OpenProfile shows whoever made an invite, anchored on the chip naming them.
	// The card mounts on the modal layer, which is above this page.
	OpenProfile func(userID string, anchor fyne.CanvasObject)

	/* Bans */

	LoadBans func(onLoaded func(bans []domain.Ban, err error))
	LiftBan  func(userID string, onDone func(err error))
}

// listTTL is how long a fetched list is believed. Short on purpose: what it
// guards against is somebody else revoking an invite or lifting a ban while this
// page is open, which nothing announces — so the window is the price of not
// re-asking on every tap between two sections. Long enough that flicking between
// them is free, short enough that a list left on screen is not a lie.
//
// A const rather than a setting: what a reader would be choosing is how stale a
// list may be, which is not a preference so much as a bug they cannot see.
const listTTL = 30 * time.Second

// cachedList is one fetched section as the page last saw it, held for **the life
// of one opening and no longer**. Three rules in one type, and each of them was a
// request the page would otherwise have made:
//
//   - **Held.** Nothing announces a revoked invite or a lifted ban, so a second
//     fetch could only answer what the first did. Without this, tapping between
//     Bans and Invites was a request every time.
//   - **Single-flight.** `inflight` is claimed before the request goes out and
//     released when it answers, so re-mounting a section whose first answer is
//     still on its way waits for it rather than sending a second. It is a plain
//     bool, not a sync.Once: everything here is UI-thread confined, and the point
//     is not "exactly once" but "not again *yet*" — the entry is re-askable the
//     moment it goes stale.
//   - **Expiring.** `at` is when the answer landed, and one older than listTTL is
//     re-asked on the next mount.
//
// It is also what a late answer is written to. The reader may be two sections
// away by then, so *drawing* is guarded on the section still being mounted, while
// the answer itself is always recorded — which is what makes going back free
// rather than another request.
//
// err is held beside the entries rather than instead of them: a failure is a
// state the section draws, and re-mounting it must not read as "no bans".
type cachedList[T any] struct {
	entries []T
	err     error

	// at is when the answer landed; zero means none has. inflight is a request
	// already out, which is not the same as an answer held.
	at       time.Time
	inflight bool
}

// fresh reports whether the held answer is still worth drawing without asking
// again. A failure counts: re-asking a route that just refused, every time the
// reader taps back, is the loop this exists to stop.
func (c *cachedList[T]) fresh() bool {
	return !c.at.IsZero() && time.Since(c.at) < listTTL
}

// claim takes the right to make the request, reporting whether the caller should.
// False means one is already out, or the held answer is still good.
func (c *cachedList[T]) claim() bool {
	if c.inflight || c.fresh() {
		return false
	}
	c.inflight = true

	return true
}

// settle records an answer and releases the claim.
func (c *cachedList[T]) settle(entries []T, err error) {
	c.entries, c.err, c.at, c.inflight = entries, err, time.Now(), false
}

// pending reports that there is nothing to draw yet — no answer held, and either
// one on its way or one about to be asked for.
func (c *cachedList[T]) pending() bool { return c.at.IsZero() }

// ServerSettingsPage is a server's settings and their state. Unlike the client's
// own, everything it holds is per opening: it is about one server, and the one it
// is about can be left while it is up.
type ServerSettingsPage struct {
	settingsShell

	hooks ServerSettingsHooks

	section ServerSettingsSection

	// roleID is the role the Roles section is drilled into, "" for its list, and
	// roleName is what the pane's own title says it is. Both are held across a
	// rebuild so a role event — this account's own edit echoing back included —
	// leaves the reader where they were.
	//
	// roleAllow and roleDeny are that role's permissions as the editor last sent
	// them, re-read from the store on every build. A second change made before the
	// first has echoed back computes from these rather than from the role the row
	// was built with, which would still be the one before the first change.
	roleID    string
	roleName  string
	roleAllow domain.Permission
	roleDeny  domain.Permission

	// listBody is the open section's list, kept so an answer can refill it in place
	// rather than remount the section under a reader who has scrolled.
	listBody *fyne.Container

	// invites and bans are those two sections' answers for as long as the page is
	// open. Dropped by Close, so a second opening asks again — the page is a
	// snapshot of one visit, not a cache of the server.
	invites cachedList[ServerInviteEntry]
	bans    cachedList[domain.Ban]

	// visit counts openings, and every fetch captures it. An answer for a visit
	// that has ended must not be recorded either: the page can be closed and
	// reopened on a *different* server before one lands, and IsOpen alone cannot
	// tell those two apart. UI-thread only, so a plain counter is enough.
	visit uint64
}

// NewServerSettingsPage builds the page, hidden.
func NewServerSettingsPage(hooks ServerSettingsHooks) *ServerSettingsPage {
	p := &ServerSettingsPage{hooks: hooks, section: ServerSectionOverview}

	p.initShell(hooks.Close)

	return p
}

// Open builds the page on its first section and shows it. Call on the UI thread.
func (p *ServerSettingsPage) Open() {
	p.visit++
	p.section = ServerSectionOverview
	p.closeRole()
	p.mountSurface()
	p.Layer.Show()
}

// Close hides the page and drops what it built, so nothing it mounted keeps a
// widget or an image alive. Call on the UI thread.
func (p *ServerSettingsPage) Close() {
	p.resetShell()
	p.listBody = nil
	p.closeRole()
	p.visit++ // whatever is still in flight has nowhere to land, and nothing to be recorded in

	// The held answers go with the page. They are as good as a fetch only while
	// nobody has had time to change anything — which is the visit, not longer.
	p.invites = cachedList[ServerInviteEntry]{}
	p.bans = cachedList[domain.Ban]{}
}

// Rebuild constructs the page as the theme tables and the store now stand. Called
// on open, and by the controller after a restyle or a change to the server itself.
// Call on the UI thread.
func (p *ServerSettingsPage) Rebuild() {
	if !p.IsOpen() {
		return
	}

	p.mountSurface()
}

// mountSurface puts a freshly built surface in the layer. Split from Rebuild
// because Open has to reach it too, and Open builds *before* showing — a rebuild
// guarded on the layer being visible would find it hidden and do nothing.
func (p *ServerSettingsPage) mountSurface() {
	p.Layer.Objects = []fyne.CanvasObject{p.build(), p.popover}
	p.Layer.Refresh()
}

// build assembles the surface around whichever section is open.
func (p *ServerSettingsPage) build() fyne.CanvasObject {
	p.newSurface()
	p.showSection(p.section)

	// The server stands at the head of the rail where the client's settings puts
	// its search box: there is no second server to confuse this with, but several
	// ways to arrive here, and every section below is about this one.
	return p.buildSurface("Server", p.buildIdentity(), nil)
}

// buildIdentity is the server's own icon and name, pinned above the rail.
func (p *ServerSettingsPage) buildIdentity() fyne.CanvasObject {
	server, ok := p.hooks.Server()
	if !ok {
		return nil
	}

	side := theme.Sizes.SessionCardAvatarSize
	name := newBoldText(server.Name, theme.Colors.TextPrimary, theme.Sizes.SettingsRailTextSize)

	return NewFillRow(2,
		p.serverIcon(server, fyne.NewSize(side, side)),
		HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
		vcenter(NewEllipsisText(name)),
	)
}

// serverIcon is the server's picture over the letter it falls back to — the same
// circle the rail and an invite card draw, so one server reads the same wherever
// it is shown.
func (p *ServerSettingsPage) serverIcon(server ServerSummary, size fyne.Size) fyne.CanvasObject {
	background := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	slot := container.NewStack(background, container.NewCenter(newInitial(server.Name)))

	if server.IconURL != "" {
		p.hooks.Deps.Images.LoadIntoContainer(imageCacheID(server.IconURL), server.IconURL, size, slot, true, background)
	}

	return container.NewGridWrap(size, slot)
}

/* Moving between sections */

// showSection swaps the pane to one section's groups and re-heads the rail.
func (p *ServerSettingsPage) showSection(section ServerSettingsSection) {
	// A permission can be lost while the page is open — a role edited from another
	// client — and the rail is rebuilt from what is held now, so a section that has
	// just stopped being listed must not stay mounted either.
	if !p.allowed(section) {
		section = ServerSectionOverview
		p.closeRole()
	}

	p.section = section
	p.listBody = nil // set again by whichever section mounts a list

	// The groups are built first: a section that finds what it was drilled into
	// gone leaves the drilldown on the way past, and the title has to say where
	// the reader ended up rather than where they were.
	groups := p.sectionGroups(section)
	p.mountUnder(groups, p.paneTitle(section), p.paneBack(section))

	p.buildRail(p.railEntries(), int(section), func(picked int) {
		// A rail tap is the section itself, so it leaves whichever role the Roles
		// section was drilled into — including a tap on Roles from inside one.
		p.closeRole()
		p.showSection(ServerSettingsSection(picked))
	})
}

// paneTitle heads the pane with where the reader is standing: the section, or what
// it was drilled into. Which section that drilldown lives in is the back line's to
// say, so the title names one thing.
func (p *ServerSettingsPage) paneTitle(section ServerSettingsSection) string {
	if section == ServerSectionRoles && p.roleName != "" {
		return p.roleName
	}

	return serverRailTitle(section)
}

// paneBack is the way out of the one drilldown the page has, and nothing for a
// section, which the rail already reaches. Keyed on roleID rather than roleName:
// the ID is what being drilled in *is*, while the name is only what the title
// says and could be anything.
func (p *ServerSettingsPage) paneBack(section ServerSettingsSection) backLink {
	if section != ServerSectionRoles || p.roleID == "" {
		return backLink{}
	}

	return backLink{label: serverRailTitle(ServerSectionRoles), onTap: p.showRoles}
}

// showRole opens one role's editor inside the Roles section, and showRoles is the
// way back out of it. Call either on the UI thread.
func (p *ServerSettingsPage) showRole(role ServerRoleEntry) {
	p.roleID, p.roleName = role.ID, role.Name
	p.showSection(ServerSectionRoles)
}

func (p *ServerSettingsPage) showRoles() {
	p.closeRole()
	p.showSection(ServerSectionRoles)
}

// closeRole forgets which role was open without mounting anything, for a build
// that finds the role gone and for a rail tap leaving the section entirely.
func (p *ServerSettingsPage) closeRole() {
	p.roleID, p.roleName = "", ""
	p.roleAllow, p.roleDeny = 0, 0
}

// OpenRole drills into a role from outside the page — a role just created, once
// the event carrying it has landed and the store can answer for it. Call on the
// UI thread.
func (p *ServerSettingsPage) OpenRole(roleID string) {
	if !p.IsOpen() || !p.allowed(ServerSectionRoles) {
		return
	}

	role, ok := findRole(p.hooks.Roles(), roleID)
	if !ok {
		return
	}

	p.showRole(role)
}

// sectionGroups builds one section.
func (p *ServerSettingsPage) sectionGroups(section ServerSettingsSection) []settingsGroup {
	switch section {
	case ServerSectionOverview:
		return p.overviewSection()
	case ServerSectionChannels:
		return p.channelsSection()
	case ServerSectionRoles:
		return p.rolesSection()
	case ServerSectionInvites:
		return p.invitesSection()
	case ServerSectionBans:
		return p.bansSection()
	}

	return nil
}

// railEntries is the sections this account may reach. One it may not is left off
// rather than greyed: the rail is what says what can be done here.
func (p *ServerSettingsPage) railEntries() []railEntry {
	entries := make([]railEntry, 0, len(serverRailEntries))
	for _, entry := range serverRailEntries {
		if p.allowed(ServerSettingsSection(entry.section)) {
			entries = append(entries, entry)
		}
	}

	return entries
}

func (p *ServerSettingsPage) allowed(section ServerSettingsSection) bool {
	wanted := permissionFor(section)
	if wanted == 0 {
		return true
	}

	for bit := wanted; bit != 0; bit &= bit - 1 {
		if p.hooks.Can(bit & -bit) {
			return true
		}
	}

	return false
}

func serverRailTitle(section ServerSettingsSection) string {
	for _, entry := range serverRailEntries {
		if entry.section == int(section) {
			return entry.title
		}
	}

	return ""
}

/* Overview */

func (p *ServerSettingsPage) overviewSection() []settingsGroup {
	server, ok := p.hooks.Server()
	if !ok {
		return []settingsGroup{p.group("Server", "",
			p.note("This server is no longer one this account is in."))}
	}

	return []settingsGroup{
		p.group("Server", "",
			p.nameRow(server),
			p.descriptionRow(server),
			p.iconRow(server),
			p.bannerRow(server),
		),
		p.group("Facts", "What the store can say about this server. None of it is settable.",
			p.readOnlyRow("Owner", server.Owner),
			p.readOnlyRow("Created", server.Created),
			p.readOnlyRow("Channels", plural(server.Channels, "channel")),
		),
		p.group("Identifier", "The server's own ID, which every route naming it takes.",
			p.actionRow("Server ID", server.ID, "Copy", ToneInfo, func() {
				CopyToClipboard(server.ID)
			}),
		),
	}
}

// nameRow and descriptionRow are the two things this page can change. Neither
// writes back what it sent: the edit returns as a ServerUpdate and the store
// answers for it, exactly as this account's own display name does — so a rename
// the server refused is never left on screen as though it held.
//
// Both are read-only where the account may not manage the server. The section is
// listed for anybody, being most of what a reader opens this page to see.
func (p *ServerSettingsPage) nameRow(server ServerSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageServer) {
		return p.readOnlyRow("Name", server.Name)
	}

	return p.row("Name", "What the server is called everywhere it appears.",
		textField(newCommitEntry(server.Name, p.hooks.SetName)))
}

func (p *ServerSettingsPage) descriptionRow(server ServerSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageServer) {
		description := server.Description
		if description == "" {
			description = "None"
		}

		return p.readOnlyRow("Description", description)
	}

	entry := newCommitArea(server.Description, p.hooks.SetDescription)
	entry.PlaceHolder = "What this server is for"

	return p.stackedRow("Description", "Shown on the invite people join through. Clear it to remove it.",
		wideField(entry))
}

// iconRow and bannerRow are the server's two pictures. Both are left out
// entirely where the account may not manage the server: the strip above the rail
// already draws the icon, and a read-only row would state it twice, while a
// banner nothing in this client draws has nothing to say read-only at all.
//
// Remove is drawn disabled rather than left out, so neither row changes shape the
// moment a picture lands.
func (p *ServerSettingsPage) iconRow(server ServerSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageServer) {
		return nil
	}

	remove := newRowButton("Remove", ToneWarning, p.hooks.RemoveIcon)
	enableIf(remove, server.IconURL != "")

	side := theme.Sizes.SessionCardAvatarSize
	control := HBoxNoSpacing(
		container.NewCenter(p.serverIcon(server, fyne.NewSize(side, side))),
		HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
		container.NewCenter(newRowButton("Change", ToneInfo, p.hooks.ChangeIcon)),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(remove),
	)

	return p.row("Icon", "Shown wherever the server is named.", control)
}

func (p *ServerSettingsPage) bannerRow(server ServerSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageServer) {
		return nil
	}

	remove := newRowButton("Remove", ToneWarning, p.hooks.RemoveBanner)
	enableIf(remove, server.BannerURL != "")

	control := HBoxNoSpacing(
		container.NewCenter(newRowButton("Change", ToneInfo, p.hooks.ChangeBanner)),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(remove),
	)

	return p.row("Banner", "Drawn by clients that show one. Here it appears on an invite card.", control)
}

// plural counts something and names it, so a row says "1 channel" rather than
// "1" under a label that has to be read to know what was counted.
func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}

	return fmt.Sprintf("%d %ss", count, noun)
}

/* Channels */

const channelsDetail = "Every channel this account can see, in the order the sidebar draws them. " +
	"A category names the ones under it, and moving a channel past the end of one is how it joins the next."

// channelsSection is the sidebar's own arrangement as a list: the channels no
// category claimed, then each category and what it holds. One list rather than a
// card per category — what the section is about is the order, and an order split
// across cards reads as several.
func (p *ServerSettingsPage) channelsSection() []settingsGroup {
	groups := []settingsGroup{}

	// A category is the server's structure rather than one channel's, so the row
	// that makes one and every move below ask for the server-wide permission — the
	// one Revolt itself checks for the route that publishes an arrangement.
	manage := p.hooks.Can(domain.PermissionManageChannel)
	if manage {
		groups = append(groups, p.group("Add", "",
			p.actionRow("New channel",
				"Text or voice. It joins the sidebar as soon as Revolt confirms it.",
				"Create", ToneInfo, p.hooks.CreateChannel),
			p.actionRow("New category",
				"A heading in the sidebar. It arrives empty, at the end of the order.",
				"Create", ToneInfo, p.hooks.CreateCategory),
		))
	}

	entries := p.hooks.Channels()
	if len(entries) == 0 || (len(entries) == 1 && len(entries[0].Channels) == 0) {
		return append(groups, p.group("Channels", "", p.note("This server has no channels.")))
	}

	var rows []fyne.CanvasObject
	for at, entry := range entries {
		if entry.ID != "" {
			rows = append(rows, p.categoryRow(entry, at, len(entries), manage))
		}
		for index, channel := range entry.Channels {
			rows = append(rows, p.channelRow(channel, entries, at, index, manage))
		}
	}

	return append(groups, p.group("Channels", channelsDetail, rows...))
}

// categoryRow is the heading the channels under it are listed against. It carries
// no glyph where a channel carries one, which is what indents them beneath it:
// the two are not the same kind of row, and the eye should not have to read them
// to find that out.
func (p *ServerSettingsPage) categoryRow(entry ServerCategoryEntry, at, count int, manage bool) fyne.CanvasObject {
	var controls []fyne.CanvasObject
	if manage {
		// The uncategorised stretch is at 0 and is not a category, so the first real
		// one has nothing above it to trade places with.
		controls = append(controls, moveButtons(at > 1, at < count-1,
			func(up bool) { p.hooks.MoveCategory(entry.ID, up) })...)

		controls = append(controls,
			HorizontalSpacer(theme.Sizes.ChipSpacing),
			editButton(func() { p.hooks.RenameCategory(entry.ID) }),
			HorizontalSpacer(theme.Sizes.ChipSpacing),
			deleteButton(func() { p.confirmDeleteCategory(entry) }),
		)
	}

	return p.entryRow(nil, entry.Title, plural(len(entry.Channels), "channel"), controls...)
}

func (p *ServerSettingsPage) channelRow(channel ServerChannelEntry,
	entries []ServerCategoryEntry, at, index int, manage bool) fyne.CanvasObject {

	var controls []fyne.CanvasObject
	if manage {
		controls = append(controls, moveButtons(
			channelCanMove(entries, at, index, true),
			channelCanMove(entries, at, index, false),
			func(up bool) { p.hooks.MoveChannel(channel.ID, up) })...)
	}
	if channel.Editable {
		controls = append(controls, spaced(controls, editButton(func() { p.hooks.EditChannel(channel.ID) }))...)
	}

	return p.entryRow(ChannelGlyph(channel.Kind), channel.Name, channel.Description, controls...)
}

// channelCanMove reports whether a channel has anywhere to go. Leaving a category
// counts: the move that takes a channel out of one is the move past its end.
//
// Up out of the uncategorised stretch is the one direction that is never
// anything. Revolt keeps an order for the channels a category claims and none for
// the rest, so from there the only move is down, into the first category — and
// coming back up out of it lands wherever the server already had it.
func channelCanMove(entries []ServerCategoryEntry, at, index int, up bool) bool {
	if up {
		return at > 0
	}
	if at == 0 {
		return len(entries) > 1
	}

	return index < len(entries[at].Channels)-1 || at < len(entries)-1
}

// confirmDeleteCategory asks before dropping one, and says the whole of what
// happens: a category is a heading, so deleting it keeps every channel in it and
// takes away only where they were filed.
func (p *ServerSettingsPage) confirmDeleteCategory(entry ServerCategoryEntry) {
	p.hooks.Confirm(Confirm{
		Title: "Delete category",
		Body: fmt.Sprintf("%s goes away and the %s in it move to the top of the sidebar. No channel is deleted.",
			entry.Title, plural(len(entry.Channels), "channel")),
		Action:    "Delete",
		Tone:      ToneDanger,
		OnConfirm: func() { p.hooks.DeleteCategory(entry.ID) },
	})
}

/* Roles */

const rolesDetail = "Every role this server defines, most senior first. A member wears the colour " +
	"of the most senior coloured role they hold, and is listed under the most senior hoisted one."

// rolesSection is the list of roles, or the editor for the one it is drilled
// into. The role is looked up on every build rather than held: a rebuild is how
// an edit made here comes back, and the role may have been deleted or moved out
// of reach while the editor was open.
func (p *ServerSettingsPage) rolesSection() []settingsGroup {
	roles := p.hooks.Roles()

	if p.roleID != "" {
		if role, ok := findRole(roles, p.roleID); ok && role.Editable {
			return p.roleGroups(role, roles)
		}
		p.closeRole()
	}

	var groups []settingsGroup
	if p.hooks.Can(domain.PermissionManageRole) {
		groups = append(groups, p.group("Add", "",
			p.actionRow("New role",
				"It starts with no colour and nothing allowed, at the bottom of the order.",
				"Create", ToneInfo, p.hooks.CreateRole),
		))
	}

	// The default is drawn apart from the list rather than at the end of it: it is
	// not a role, cannot be reordered or deleted, and a row among the others would
	// be offering all of that.
	ranked := rankedRoles(roles)
	rows := make([]fyne.CanvasObject, 0, len(ranked))
	var everyone fyne.CanvasObject

	// ranked is this list with the default taken out and in the same order, so a
	// row's place in the order is the number of rows already made.
	for _, role := range roles {
		if role.Default {
			everyone = p.roleRow(role, nil, -1)
			continue
		}
		rows = append(rows, p.roleRow(role, ranked, len(rows)))
	}

	if len(rows) == 0 {
		rows = append(rows, p.note("This server has no roles."))
	}
	groups = append(groups, p.group("Roles", rolesDetail, rows...))

	if everyone == nil {
		return groups
	}

	return append(groups, p.group("Everyone",
		"What every member holds before any role adds to it — and what a role's denial takes back.",
		everyone))
}

// roleRow is one role in the list, offered its place in the order as well as its
// editor: seniority is most of what a list of roles is read for, and a move that
// could only be made two taps in is a move made against a list you cannot see.
// at is its place among ranked, or -1 for the default, which has none.
func (p *ServerSettingsPage) roleRow(role ServerRoleEntry, ranked []ServerRoleEntry, at int) fyne.CanvasObject {
	var controls []fyne.CanvasObject

	// A role this account does not outrank cannot be moved, and neither can one be
	// moved above a role that outranks it — which is what the neighbour is asked.
	if at != -1 && role.Editable && p.hooks.Can(domain.PermissionManageRole) {
		controls = append(controls, moveButtons(at > 0 && ranked[at-1].Editable, at < len(ranked)-1,
			func(up bool) { p.hooks.MoveRole(role.ID, up) })...)
	}
	if role.Editable {
		controls = append(controls, spaced(controls, editButton(func() { p.showRole(role) }))...)
	}

	return p.entryRow(roleDot(role), role.Name, roleSummary(role), controls...)
}

// roleSummary is what a role does rather than what it is called: how much it
// allows, and whether it files the members holding it under its own name. A
// denial is not counted — it takes nothing away that the role itself gave.
func roleSummary(role ServerRoleEntry) string {
	summary := plural(bits.OnesCount64(uint64(role.Allow)), "permission")

	switch {
	case role.Default:
		return summary // the card it is drawn in says whose they are
	case !role.Editable:
		// Why the row offers nothing, which is otherwise a question the reader is
		// left holding: Revolt refuses an edit to a role at or above their own.
		return summary + " · more senior than yours"
	case role.Hoist:
		return summary + " · its own section"
	}

	return summary
}

// roleDot is the colour a role paints a name in, or the neutral one a role
// without a colour leaves that name in.
func roleDot(role ServerRoleEntry) fyne.CanvasObject {
	fill := role.Color
	if fill == nil {
		fill = theme.Colors.CategoryText
	}

	side := theme.Sizes.SettingsIconSize

	return container.NewCenter(newSwatchRect(fill, side, side/2))
}

// deleteButton is the same target in the danger tone, wearing the mark and the
// tint the invite list revokes with — a destructive row action reads the same
// wherever one is offered.
func deleteButton(onTap func()) *OutlinedIconButton {
	tint := theme.Colors.SwiftActionDanger

	return NewOutlinedIconButton(tintedIcon(assets.ActionDeleteIcon, tint), tint, onTap)
}

// moveButtons is how a list row offers its place in an order. Both are drawn
// whatever they can do: a row at either end wears the pair with one of them dead
// rather than a shape of its own, a list being read down its right-hand edge as
// much as down its left.
func moveButtons(canUp, canDown bool, onMove func(up bool)) []fyne.CanvasObject {
	return []fyne.CanvasObject{
		moveButton(assets.ActionUpIcon, canUp, func() { onMove(true) }),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		moveButton(assets.ActionDownIcon, canDown, func() { onMove(false) }),
	}
}

// moveButton is one of that pair. Which tint it wears is decided here rather than
// switched afterwards: the mark carries its colour in the resource, baked in at
// construction so the raster is cached under it.
func moveButton(mark fyne.Resource, available bool, onTap func()) *OutlinedIconButton {
	if !available {
		tint := theme.Colors.ButtonDisabledText

		return NewOutlinedIconButton(tintedIcon(mark, tint), tint, nil).disabled()
	}

	tint := theme.Colors.SwiftActionIcon

	return NewOutlinedIconButton(tintedIcon(mark, tint), tint, onTap)
}

// spaced is one more control for a row that may already have some. The gap
// between two belongs to the second, so a row drawing a single button pays
// nothing for the ones it left off.
func spaced(existing []fyne.CanvasObject, control fyne.CanvasObject) []fyne.CanvasObject {
	if len(existing) == 0 {
		return []fyne.CanvasObject{control}
	}

	return []fyne.CanvasObject{HorizontalSpacer(theme.Sizes.ChipSpacing), control}
}

// editButton is how a list row offers the editor behind it: the same outlined
// target the invite list's own actions are, in the icon's own tint. A filled
// button per row would be the loudest thing on a page that is mostly reading.
func editButton(onTap func()) *OutlinedIconButton {
	tint := theme.Colors.SwiftActionIcon

	return NewOutlinedIconButton(tintedIcon(assets.ActionEditIcon, tint), tint, onTap)
}

func findRole(roles []ServerRoleEntry, roleID string) (ServerRoleEntry, bool) {
	for _, role := range roles {
		if role.ID == roleID {
			return role, true
		}
	}

	return ServerRoleEntry{}, false
}

// rankedRoles is the roles seniority applies to, which is all of them but the
// default: everybody holds that one and nothing is above or below it.
func rankedRoles(roles []ServerRoleEntry) []ServerRoleEntry {
	ranked := make([]ServerRoleEntry, 0, len(roles))
	for _, role := range roles {
		if !role.Default {
			ranked = append(ranked, role)
		}
	}

	return ranked
}

/* One role */

// roleGroups is the editor: what the role is, then its permissions a category at
// a time, then the one way to be rid of it. Two permissions decide what of it can
// be changed and they are not the same — `ManageRole` covers the name, the colour,
// the hoist, the order and the delete, while the grid is `ManagePermissions` — so
// a reader holding one and not the other gets the other half read-only rather
// than a page that lies about what it will accept.
func (p *ServerSettingsPage) roleGroups(role ServerRoleEntry, roles []ServerRoleEntry) []settingsGroup {
	p.roleAllow, p.roleDeny = role.Allow, role.Deny

	groups := []settingsGroup{p.group(roleCaption(role), "", p.roleRows(role, roles)...)}

	for _, category := range permissionCategories {
		rows := make([]fyne.CanvasObject, 0, len(category.entries))
		for _, entry := range category.entries {
			rows = append(rows, p.permissionRow(role, entry))
		}

		groups = append(groups, p.group(category.caption, category.detail, rows...))
	}

	if role.Default || !p.hooks.Can(domain.PermissionManageRole) {
		return groups
	}

	return append(groups, p.group("Delete", "",
		p.actionRow("Delete role",
			"Everybody holding it loses it, and nothing else about them changes.",
			"Delete", ToneDanger, func() { p.confirmDeleteRole(role) }),
	))
}

// roleCaption heads the editor's first card. The default is captioned for what it
// is rather than as a role, the rail listing this caption under the section.
func roleCaption(role ServerRoleEntry) string {
	if role.Default {
		return "Everyone"
	}

	return "Role"
}

// roleRows is what the role is. The way out is the header's line rather than a row
// here: a row among Name and Colour reads as something to set, and it scrolls away
// with them. The default role is none of these things — it has no name, no colour
// and no place in the order — so it says what it is instead.
func (p *ServerSettingsPage) roleRows(role ServerRoleEntry, roles []ServerRoleEntry) []fyne.CanvasObject {
	if role.Default {
		return []fyne.CanvasObject{p.note(
			"Everybody in the server holds these. A role adds to them, and a role can take them " +
				"away again — which is what a denial is for.")}
	}

	return []fyne.CanvasObject{
		p.roleNameRow(role),
		p.roleColorRow(role),
		p.roleHoistRow(role),
		p.roleOrderRow(role, rankedRoles(roles)),
	}
}

func (p *ServerSettingsPage) roleNameRow(role ServerRoleEntry) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageRole) {
		return p.readOnlyRow("Name", role.Name)
	}

	return p.row("Name", "What the role is called wherever it is listed.",
		textField(newCommitEntry(role.Name, func(name string) { p.hooks.SetRoleName(role.ID, name) })))
}

// roleColorRow sets the colour a member's name is drawn in. The field is the hex
// the swatch and the palette agree on, which is less than Revolt takes — see
// docs/known-gaps.md.
func (p *ServerSettingsPage) roleColorRow(role ServerRoleEntry) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageRole) {
		return p.row("Colour", "", roleColorState(role))
	}

	remove := newRowButton("Remove", ToneWarning, func() { p.hooks.SetRoleColor(role.ID, "") })
	enableIf(remove, role.ColorText != "")

	control := HBoxNoSpacing(
		container.NewCenter(p.colorControl(roleHex(role), theme.Colors.CategoryText,
			func(hex string) { p.hooks.SetRoleColor(role.ID, hex) })),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(remove),
	)

	return p.row("Colour", "The colour every name holding this role is drawn in.", control)
}

// roleColorState is the colour where it cannot be changed: the sample the list
// draws, beside what Revolt is holding — which may be a name or a gradient, and
// is worth stating exactly where nothing here can edit it.
func roleColorState(role ServerRoleEntry) fyne.CanvasObject {
	value := role.ColorText
	if value == "" {
		value = "None"
	}

	return HBoxNoSpacing(
		container.NewCenter(roleDot(role)),
		HorizontalSpacer(theme.Sizes.ChipDotGap),
		vcenter(stateText(value)),
	)
}

// roleHex is the colour the field opens on: what Revolt holds where that is a
// hex, and the parsed colour's own where it is a name or a gradient the field
// cannot express.
func roleHex(role ServerRoleEntry) string {
	if _, ok := theme.ParseHex(role.ColorText); ok {
		return role.ColorText
	}
	if role.Color != nil {
		return theme.Hex(role.Color)
	}

	return ""
}

func (p *ServerSettingsPage) roleHoistRow(role ServerRoleEntry) fyne.CanvasObject {
	const detail = "Members whose most senior hoisted role this is are listed under its name."

	if !p.hooks.Can(domain.PermissionManageRole) {
		row, marker := p.markedRow("Its own section", detail, stateText(yesNo(role.Hoist)))
		markRow(marker, role.Hoist)

		return row
	}

	return p.boolRow("Its own section", detail, role.Hoist,
		func(on bool) { p.hooks.SetRoleHoist(role.ID, on) })
}

// roleOrderRow moves a role past its neighbour, and says where it stands while it
// is at it — the order is the whole of what seniority is, and a role in the
// middle of a long list cannot be placed by looking at the buttons. Both are
// drawn whatever they can do: a role at an end, or one whose neighbour outranks
// this account, leaves the button disabled rather than the row a different shape.
func (p *ServerSettingsPage) roleOrderRow(role ServerRoleEntry, ranked []ServerRoleEntry) fyne.CanvasObject {
	at := slices.IndexFunc(ranked, func(other ServerRoleEntry) bool { return other.ID == role.ID })
	if at == -1 {
		return nil
	}

	detail := fmt.Sprintf("%d of %d. A more senior role decides the colour, the section and what a "+
		"denial overrules.", at+1, len(ranked))

	if !p.hooks.Can(domain.PermissionManageRole) {
		return p.readOnlyRow("Seniority", fmt.Sprintf("%d of %d", at+1, len(ranked)))
	}

	up := NewButton("Up", func() { p.hooks.MoveRole(role.ID, true) })
	enableIf(up, at > 0 && ranked[at-1].Editable)

	down := NewButton("Down", func() { p.hooks.MoveRole(role.ID, false) })
	enableIf(down, at < len(ranked)-1)

	control := HBoxNoSpacing(
		container.NewCenter(up),
		HorizontalSpacer(theme.Sizes.ChipSpacing),
		container.NewCenter(down),
	)

	return p.row("Seniority", detail, control)
}

func (p *ServerSettingsPage) confirmDeleteRole(role ServerRoleEntry) {
	p.hooks.Confirm(Confirm{
		Title:  "Delete role",
		Body:   fmt.Sprintf("%s will be taken off everybody holding it. This cannot be undone.", role.Name),
		Action: "Delete",
		Tone:   ToneDanger,
		OnConfirm: func() {
			p.hooks.DeleteRole(role.ID)
			p.showRoles() // the list is what is left; the role event only confirms it
		},
	})
}

/* A role's permissions */

// permissionRow is one bit as this role holds it: three states for a role, whose
// permissions are an override, and two for the default, which is a plain set with
// nothing under it to inherit from.
//
// The row computes from what the page holds rather than from the role it was
// built with: a second change made before the first has echoed back would
// otherwise send the first one's absence. A bit this account cannot set is drawn
// as the state in words — Revolt refuses a change to a bit the actor does not
// hold themselves, so a grid drawn from `ManagePermissions` alone would offer
// picks the server would only refuse.
func (p *ServerSettingsPage) permissionRow(role ServerRoleEntry, entry permissionEntry) fyne.CanvasObject {
	if role.Default {
		return p.defaultPermissionRow(entry)
	}

	state := permissionState(p.roleAllow, p.roleDeny, entry.permission)

	if !p.canSetPermission(entry.permission) {
		row, marker := p.markedRow(entry.label, entry.detail, stateText(optionLabel(permissionStates, state)))
		markPermission(marker, state)

		return row
	}

	var (
		control *optionControl
		marker  *canvas.Rectangle
	)

	control = newOptionControl(state, permissionStates, func(picked string) {
		p.roleAllow, p.roleDeny = setPermissionState(p.roleAllow, p.roleDeny, entry.permission, picked)
		p.hooks.SetRolePermissions(role.ID, p.roleAllow, p.roleDeny)
		control.set(picked)
		markPermission(marker, picked)
	})

	row, rowMarker := p.markedRow(entry.label, entry.detail, control)
	marker = rowMarker
	markPermission(marker, state)

	return row
}

// defaultPermissionRow is the same bit for the default role, which is a switch
// rather than a choice: there is nothing beneath it to inherit from, so a bit is
// held or it is not.
func (p *ServerSettingsPage) defaultPermissionRow(entry permissionEntry) fyne.CanvasObject {
	held := p.roleAllow.Has(entry.permission)

	if !p.canSetPermission(entry.permission) {
		row, marker := p.markedRow(entry.label, entry.detail, stateText(yesNo(held)))
		markRow(marker, held)

		return row
	}

	return p.boolRow(entry.label, entry.detail, held, func(on bool) {
		p.roleAllow = p.roleAllow &^ entry.permission
		if on {
			p.roleAllow |= entry.permission
		}

		p.hooks.SetDefaultPermissions(p.roleAllow)
	})
}

// canSetPermission is both halves of what setting one takes: the permission to
// manage permissions at all, and holding the bit being handed out — Revolt
// refuses a role's grant of anything the actor does not have.
// https://github.com/stoatchat/stoatchat/blob/main/crates/delta/src/routes/servers/permissions_set.rs
func (p *ServerSettingsPage) canSetPermission(permission domain.Permission) bool {
	return p.hooks.Can(domain.PermissionManagePermissions) && p.hooks.Can(permission)
}

// The three states one permission can be in for one role. Inherited is the
// absence of both bits, which is what leaves a more senior role or the server's
// own default to decide.
const (
	permissionAllow   = "allow"
	permissionInherit = "inherit"
	permissionDeny    = "deny"
)

var permissionStates = []settingsOption{
	{Label: "Allow", Value: permissionAllow},
	{Label: "Inherit", Value: permissionInherit},
	{Label: "Deny", Value: permissionDeny},
}

func permissionState(allow, deny, permission domain.Permission) string {
	switch {
	case allow.Has(permission):
		return permissionAllow
	case deny.Has(permission):
		return permissionDeny
	}

	return permissionInherit
}

func setPermissionState(allow, deny, permission domain.Permission, state string) (domain.Permission, domain.Permission) {
	allow, deny = allow&^permission, deny&^permission

	switch state {
	case permissionAllow:
		allow |= permission
	case permissionDeny:
		deny |= permission
	}

	return allow, deny
}

// markPermission colours the bar a permission row wears, which is what makes a
// card of thirty-four rows skimmable: the accent for what a role grants, the
// danger tone for what it takes back, and nothing at all for what it leaves to
// whoever else has an opinion.
func markPermission(marker *canvas.Rectangle, state string) {
	switch state {
	case permissionAllow:
		marker.FillColor = theme.Colors.ServerSelectedBg
	case permissionDeny:
		marker.FillColor = theme.Colors.SwiftActionDanger
	default:
		marker.FillColor = color.Transparent
	}

	marker.Refresh()
}

// stateText is what a row says where it cannot offer the control: the value in
// the same colour every read-only row states one in.
func stateText(value string) fyne.CanvasObject {
	return newText(value, theme.Colors.TimestampText, theme.Sizes.SettingsLabelSize)
}

func yesNo(on bool) string {
	if on {
		return "Yes"
	}

	return "No"
}

// permissionCategory is one card of the editor: the bits that are about the same
// thing, so a card can be read for what it decides rather than a list of forty
// switches being read in order.
type permissionCategory struct {
	caption string
	detail  string
	entries []permissionEntry
}

type permissionEntry struct {
	label      string
	detail     string
	permission domain.Permission
}

// permissionCategories is every bit Revolt defines, in the order the client
// groups them. What each one covers is
// https://github.com/stoatchat/stoatchat/blob/main/crates/core/permissions/src/models/channel.rs
var permissionCategories = []permissionCategory{
	{
		caption: "Server",
		detail:  "What can be changed about the server itself.",
		entries: []permissionEntry{
			{"Manage channels", "Create, rename and delete channels.", domain.PermissionManageChannel},
			{"Manage server", "The name, the description and the pictures.", domain.PermissionManageServer},
			{"Manage permissions", "Change what any role may do, here and per channel.", domain.PermissionManagePermissions},
			{"Manage roles", "Create, edit and delete roles below their own.", domain.PermissionManageRole},
			{"Manage customisation", "The server's emoji.", domain.PermissionManageCustomisation},
			{"View audit log", "Read what other moderators have done.", domain.PermissionViewAuditLogs},
		},
	},
	{
		caption: "Members",
		detail:  "What can be done to the people in the server.",
		entries: []permissionEntry{
			{"Kick members", "Remove somebody, who may rejoin with a new invite.", domain.PermissionKickMembers},
			{"Ban members", "Remove somebody until the ban is lifted.", domain.PermissionBanMembers},
			{"Time out members", "Silence somebody for a while without removing them.", domain.PermissionTimeoutMembers},
			{"Assign roles", "Give and take roles below their own.", domain.PermissionAssignRoles},
			{"Change nickname", "Rename themselves in this server.", domain.PermissionChangeNickname},
			{"Manage nicknames", "Rename anybody in this server.", domain.PermissionManageNicknames},
			{"Change avatar", "Set a picture of their own for this server.", domain.PermissionChangeAvatar},
			{"Remove avatars", "Take anybody's server picture off.", domain.PermissionRemoveAvatars},
		},
	},
	{
		caption: "Channels",
		detail:  "What can be reached, before any one channel narrows it.",
		entries: []permissionEntry{
			{"View channels", "See a channel at all. Without it, nothing else here applies.", domain.PermissionViewChannel},
			{"Read history", "Read what was said before they arrived.", domain.PermissionReadMessageHistory},
			{"Invite others", "Make an invite to a channel.", domain.PermissionInviteOthers},
			{"Manage webhooks", "Add and remove the hooks that post from elsewhere.", domain.PermissionManageWebhooks},
		},
	},
	{
		caption: "Messages",
		detail:  "What can be said, and what can be done to what was said.",
		entries: []permissionEntry{
			{"Send messages", "Write in a channel.", domain.PermissionSendMessage},
			{"Manage messages", "Delete and pin anybody's messages.", domain.PermissionManageMessages},
			{"Send embeds", "Have links they post unfurled into cards.", domain.PermissionSendEmbeds},
			{"Upload files", "Attach pictures and files.", domain.PermissionUploadFiles},
			{"Masquerade", "Post under another name and picture.", domain.PermissionMasquerade},
			{"React", "Add a reaction to a message.", domain.PermissionReact},
			{"Mention everyone", "Warn the whole channel at once.", domain.PermissionMentionEveryone},
			{"Mention roles", "Warn everybody holding a role.", domain.PermissionMentionRoles},
			{"Bypass slowmode", "Write past a channel's cooldown.", domain.PermissionBypassSlowmode},
		},
	},
	{
		caption: "Voice",
		detail:  "What can be done in a voice channel. This client joins none of them yet.",
		entries: []permissionEntry{
			{"Connect", "Join a voice channel.", domain.PermissionConnect},
			{"Speak", "Be heard once connected.", domain.PermissionSpeak},
			{"Video", "Share a camera or a screen.", domain.PermissionVideo},
			{"Listen", "Hear what is said.", domain.PermissionListen},
			{"Mute members", "Silence somebody else's microphone.", domain.PermissionMuteMembers},
			{"Deafen members", "Stop somebody else hearing.", domain.PermissionDeafenMembers},
			{"Move members", "Pull somebody into another voice channel.", domain.PermissionMoveMembers},
		},
	},
}

/* Invites */

const invitesDetail = "Every invite to this server that still stands. Revolt sets no expiry and " +
	"counts no uses, so one lasts until it is revoked — and is made from a channel's own menu, " +
	"there being no server-wide invite."

func (p *ServerSettingsPage) invitesSection() []settingsGroup {
	group, _ := p.islandGroup("Invites", invitesDetail, p.inviteRows())
	p.loadInvites()

	return []settingsGroup{group}
}

// inviteRows is the held answer drawn, or the one line standing for whichever
// state it is not in.
func (p *ServerSettingsPage) inviteRows() []fyne.CanvasObject {
	// The line standing for a list gets a surface of its own too: there is no card
	// behind this section, so a note left bare would be a sentence floating on the
	// page rather than the list saying it is empty.
	switch {
	case p.invites.pending():
		return []fyne.CanvasObject{newIsland(p.note("Fetching…"))}
	case p.invites.err != nil:
		return []fyne.CanvasObject{newIsland(p.note("Could not fetch this server's invites."))}
	case len(p.invites.entries) == 0:
		return []fyne.CanvasObject{newIsland(p.note("There are no invites to this server."))}
	}

	cells := make([]fyne.CanvasObject, 0, len(p.invites.entries))
	for _, invite := range p.invites.entries {
		cells = append(cells, newIsland(p.inviteRow(invite)))
	}

	return pairedRows(cells)
}

// loadInvites asks for the list, unless one is already out or the held answer is
// still fresh. The answer is recorded whatever the reader has done meanwhile and
// drawn into whichever card is mounted *now* — never the one this was asked from,
// which a remount will already have replaced.
func (p *ServerSettingsPage) loadInvites() {
	if !p.invites.claim() {
		return
	}

	visit := p.visit

	p.hooks.LoadInvites(func(invites []ServerInviteEntry, err error) {
		if p.visit != visit {
			return // the page closed; there is nothing left to record this in
		}
		p.invites.settle(invites, err)
		p.redraw(ServerSectionInvites, p.inviteRows)
	})
}

func (p *ServerSettingsPage) inviteRow(invite ServerInviteEntry) fyne.CanvasObject {
	// Icons rather than words: the row's own text is a code and where it leads, and
	// two labelled buttons beside that read as more to take in than there is. Both
	// marks are the ones a message's own actions use, so copy and delete mean the
	// same thing wherever they appear — and each wears its own edge, an icon with
	// nothing round it reading as decoration where it is the only thing offering
	// the action.
	copyTint := theme.Colors.SwiftActionIcon
	copyInvite := NewOutlinedIconButton(tintedIcon(assets.ActionCopyIcon, copyTint), copyTint,
		func() { p.hooks.CopyInvite(invite.Code) })

	revokeTint := theme.Colors.SwiftActionDanger
	revoke := NewOutlinedIconButton(tintedIcon(assets.ActionDeleteIcon, revokeTint), revokeTint, func() {
		p.hooks.Confirm(Confirm{
			Title:  "Revoke invite",
			Body:   fmt.Sprintf("Anybody holding %s will no longer be able to join with it. Nobody who has already joined is unaffected.", invite.Code),
			Action: "Revoke",
			Tone:   ToneDanger,
			OnConfirm: func() {
				p.hooks.RevokeInvite(invite.Code, func(err error) { p.reloadList(err, "Could not revoke that invite.") })
			},
		})
	})

	if p.indexing {
		return newIndexRow(invite.Code)
	}

	// Not entryRow's shape, and the one list here that is not: an invite has no lead
	// glyph — two share a row, and the same mark drawn twice a line is decoration
	// where there is no width for any — and its second line is a chip rather than a
	// sentence. What leads an invite is its code.
	gap := theme.Sizes.SettingsRowPaddingH
	buttons := HBoxNoSpacing(copyInvite, HorizontalSpacer(theme.Sizes.IconButtonGap), revoke)
	width := halfCardWidth() - gap - buttons.MinSize().Width - gap

	lines := []fyne.CanvasObject{p.inviteWhere(invite, width)}
	if invite.Creator != "" {
		// In an HBox so the chip keeps its own width: a column would stretch it to the
		// row and a pill the width of the card reads as a button.
		lines = append(lines, VerticalSpacer(theme.Sizes.SettingsEntryLineGap),
			HBoxNoSpacing(NewUserChip(p.hooks.Deps.Images, invite.Creator, invite.CreatorAvatarURL,
				invite.CreatorColor,
				func(anchor fyne.CanvasObject) { p.hooks.OpenProfile(invite.CreatorID, anchor) })))
	}

	row, _ := p.frame(NewFillRow(0,
		vcenter(VBoxNoSpacing(lines...)),
		HorizontalSpacer(gap),
		vcenter(buttons),
	))

	return row
}

// inviteWhere is an invite's first line: the code, and beside it the channel the
// invite opens. The code sits in a column, so the channels line up down the list
// however wide a code draws — and so does the chip on the line below it, which
// starts where the code does. Alignment is the whole point of the column: two
// invites share a row, and a field that begins wherever the one before it ended
// reads as a sentence per cell rather than a list.
//
// The channel is dropped rather than explained where the store could not name it:
// a channel deleted is not what the reader is here to be told.
func (p *ServerSettingsPage) inviteWhere(invite ServerInviteEntry, width float32) fyne.CanvasObject {
	code := entryColumn(NewEllipsisText(
		newText(invite.Code, theme.Colors.TextPrimary, theme.Sizes.SettingsLabelSize)), width)

	if invite.Channel == "" {
		return HBoxNoSpacing(code)
	}

	channel := NewEllipsisText(
		newText("#"+invite.Channel, theme.Colors.TimestampText, theme.Sizes.SettingsDetailSize))

	return HBoxNoSpacing(code, NewFixedWidthContainer(width-entryColumnWidth(width), vcenter(channel)))
}

/* Bans */

const bansDetail = "Everybody barred from this server. Lifting a ban lets them back in with a " +
	"new invite — it does not send one."

func (p *ServerSettingsPage) bansSection() []settingsGroup {
	group, _ := p.listGroup("Bans", bansDetail, p.banRows())
	p.loadBans()

	return []settingsGroup{group}
}

func (p *ServerSettingsPage) banRows() []fyne.CanvasObject {
	switch {
	case p.bans.pending():
		return []fyne.CanvasObject{p.note("Fetching…")}
	case p.bans.err != nil:
		return []fyne.CanvasObject{p.note("Could not fetch this server's bans.")}
	case len(p.bans.entries) == 0:
		return []fyne.CanvasObject{p.note("Nobody is banned from this server.")}
	}

	rows := make([]fyne.CanvasObject, 0, len(p.bans.entries))
	for _, ban := range p.bans.entries {
		rows = append(rows, p.banRow(ban))
	}

	return rows
}

func (p *ServerSettingsPage) loadBans() {
	if !p.bans.claim() {
		return
	}

	visit := p.visit

	p.hooks.LoadBans(func(bans []domain.Ban, err error) {
		if p.visit != visit {
			return
		}
		p.bans.settle(bans, err)
		p.redraw(ServerSectionBans, p.banRows)
	})
}

// redraw puts a landed answer on screen, and only if the section it belongs to is
// the one mounted. It refills the *current* body rather than one captured when
// the request went out: a section re-mounted while its first answer was still on
// its way has a different card by now, and single-flight means no second request
// will come along to fill it.
func (p *ServerSettingsPage) redraw(section ServerSettingsSection, rows func() []fyne.CanvasObject) {
	if p.section != section || p.listBody == nil {
		return
	}

	p.refill(p.listBody, rows())
}

func (p *ServerSettingsPage) banRow(ban domain.Ban) fyne.CanvasObject {
	lift := newRowButton("Lift", ToneWarning, func() {
		p.hooks.Confirm(Confirm{
			Title:     "Lift ban",
			Body:      fmt.Sprintf("%s will be able to rejoin this server with a new invite.", ban.Username),
			Action:    "Lift",
			Tone:      ToneWarning,
			OnConfirm: func() { p.hooks.LiftBan(ban.UserID, func(err error) { p.reloadList(err, "Could not lift that ban.") }) },
		})
	})

	reason := ban.Reason
	if reason == "" {
		reason = "No reason was given."
	}

	side := theme.Sizes.SessionCardAvatarSize

	return p.entryRow(circularAvatar(p.hooks.Deps.Images, ban.AvatarURL, fyne.NewSize(side, side)),
		ban.Username, reason, lift)
}

/* Lists */

// listGroup is the card a fetched list is drawn in, handed back beside its body
// so a later answer can refill it in place.
func (p *ServerSettingsPage) listGroup(caption, detail string, rows []fyne.CanvasObject) (settingsGroup, *fyne.Container) {
	p.islands = false

	body := VBoxNoSpacing(p.spaceRows(rows)...)
	p.listBody = body

	return p.groupOf(caption, detail, body), body
}

// islandGroup is listGroup for a list whose entries carry their own surfaces:
// there is no card around them, and what separates two rows is space rather than
// a hairline.
func (p *ServerSettingsPage) islandGroup(caption, detail string, rows []fyne.CanvasObject) (settingsGroup, *fyne.Container) {
	p.islands = true

	body := VBoxNoSpacing(p.spaceRows(rows)...)
	p.listBody = body

	return p.bareGroupOf(caption, detail, body), body
}

// reloadList re-asks for whatever the open section lists, once something here has
// changed it. This is the one thing that can make a held answer wrong, so it
// clears that answer before asking — otherwise the next visit to the section
// would draw the revoked invite back.
func (p *ServerSettingsPage) reloadList(err error, failure string) {
	if err != nil {
		p.hooks.Confirm(Confirm{Title: "That did not work", Body: failure, Action: "Close", Tone: ToneDanger})
		return
	}

	body := p.listBody
	if body == nil {
		return
	}

	switch p.section {
	case ServerSectionInvites:
		p.invites = cachedList[ServerInviteEntry]{}
		p.refill(body, p.inviteRows())
		p.loadInvites()
	case ServerSectionBans:
		p.bans = cachedList[domain.Ban]{}
		p.refill(body, p.banRows())
		p.loadBans()
	}
}

// RefreshFromStore rebuilds the page under a server that has changed — a rename,
// a new icon, a channel added or removed. A rebuild rather than a setter: every
// section re-reads on the way past, so there is nothing to hand it.
//
// A fetched section is rebuilt too, and costs no request: it draws from what the
// page is already holding, so a burst of channel events repaints the rail's name
// rather than re-asking the network per event. It does return the reader to the
// top of that list, which is why a *lost permission* is the one thing this cannot
// answer for — showSection redirects, and the rail is rebuilt from what is held
// now. Call on the UI thread.
func (p *ServerSettingsPage) RefreshFromStore() {
	if !p.IsOpen() {
		return
	}

	p.Rebuild()
}
