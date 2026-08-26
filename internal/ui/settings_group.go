package ui

// One group conversation's settings, drawn on the same shell the client's own
// and a server's are (settings_shell.go). Two sections and no lists: everything
// here is either a field on the channel or a bit in one value, and both arrive
// through the gateway, so this page fetches nothing and holds nothing.
//
// It exists at all for the two things a group has that no other conversation
// does — a picture, and a say in what the people in it may do. Revolt keeps the
// second as a plain value on the channel rather than as an overwrite, so the
// grid here is switches where a server channel's is three-state.

import (
	"fmt"

	"fyne.io/fyne/v2"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

/* Sections */

type GroupSettingsSection int

const (
	GroupSectionOverview GroupSettingsSection = iota
	GroupSectionPermissions
)

// groupSettingsRail is the sections in the order the rail lists them. Permissions
// is drawn with the member mark rather than a role's: what it sets is what
// everybody in the group holds, there being no roles to hang it on.
var groupSettingsRail = []railEntry{
	{int(GroupSectionOverview), "Overview", assets.ServerOverviewIcon},
	{int(GroupSectionPermissions), "Permissions", assets.MembersIcon},
}

/* What the page is handed */

// GroupSummary is the group as this page draws it, resolved by the controller.
// Re-read on every build rather than passed once: a rename, a new picture or a
// permission change arrives as a channel update and the page stays open through
// it. Owner is a name where the store can give one and the raw ID otherwise, a
// group's owner being an account that need not be a friend.
type GroupSummary struct {
	ID          string
	Name        string
	Description string
	IconURL     string

	Owner   string
	Created string
	Members int

	// Permissions is what everybody in the group may do — the value Revolt stores
	// on the channel, not what this account resolves to. See domain.Channel.
	Permissions domain.Permission

	// Owned marks this account as the one that made the group, which is the only
	// thing Revolt files a group's own moderation on.
	Owned bool

	// NSFW is the age gate, the one thing on the channel edit card this page took
	// over: a group has one surface now, not a card and a page saying the same
	// things.
	NSFW bool
}

// GroupSettingsHooks is everything the page asks of the controller. Nothing here
// reports back: every change returns as a ChannelUpdate the store answers for,
// exactly as a server's name does.
type GroupSettingsHooks struct {
	Deps Deps

	Close   func()
	Confirm func(Confirm)

	// Group is what the page is about. Answering false is a group closed while its
	// settings were up.
	Group func() (GroupSummary, bool)

	// Can reports whether the account holds one permission *in this group*, which
	// for the owner is everything.
	Can func(permission domain.Permission) bool

	SetName        func(name string)
	SetDescription func(description string)
	SetNSFW        func(nsfw bool)

	// ChangeIcon asks for a file and uploads it, so it takes no argument: what was
	// picked is the controller's, the picker being the OS one.
	ChangeIcon func()
	RemoveIcon func()

	// SetPermissions publishes what everybody in the group may do, whole. Revolt
	// takes a plain value here rather than an overwrite, so there is no deny half
	// to send.
	SetPermissions func(permissions domain.Permission)
}

/* The page */

type GroupSettingsPage struct {
	settingsShell

	hooks   GroupSettingsHooks
	section GroupSettingsSection
}

// NewGroupSettingsPage builds the page, hidden.
func NewGroupSettingsPage(hooks GroupSettingsHooks) *GroupSettingsPage {
	p := &GroupSettingsPage{hooks: hooks, section: GroupSectionOverview}

	p.initShell(hooks.Close)

	return p
}

// Open builds the page on its first section and shows it. Call on the UI thread.
func (p *GroupSettingsPage) Open() {
	p.section = GroupSectionOverview
	p.mountSurface()
	p.Layer.Show()
}

// Close hides the page and drops what it built, so nothing it mounted keeps a
// widget or an image alive. Call on the UI thread.
func (p *GroupSettingsPage) Close() { p.resetShell() }

// Rebuild constructs the page as the theme tables and the store now stand —
// after a restyle, and after any channel update naming this group. Call on the
// UI thread.
func (p *GroupSettingsPage) Rebuild() {
	if !p.IsOpen() {
		return
	}

	p.mountSurface()
}

// mountSurface puts a freshly built surface in the layer. Split from Rebuild for
// the reason a server's is: Open builds *before* showing, so a rebuild guarded on
// the layer being visible would find it hidden and do nothing.
func (p *GroupSettingsPage) mountSurface() {
	p.Layer.Objects = []fyne.CanvasObject{p.build(), p.popover}
	p.Layer.Refresh()
}

func (p *GroupSettingsPage) build() fyne.CanvasObject {
	p.newSurface()
	p.showSection(p.section)

	return p.buildSurface("Group", p.buildIdentity(), nil)
}

// buildIdentity is the group's own picture and name, pinned above the rail — the
// same strip a server's settings wears, and the reason the icon row below states
// no picture read-only.
func (p *GroupSettingsPage) buildIdentity() fyne.CanvasObject {
	group, ok := p.hooks.Group()
	if !ok {
		return nil
	}

	side := theme.Sizes.SessionCardAvatarSize

	return p.identityStrip(p.groupIcon(group, fyne.NewSize(side, side)), group.Name)
}

// groupIcon is the circle the sidebar row already draws the group as.
func (p *GroupSettingsPage) groupIcon(group GroupSummary, size fyne.Size) fyne.CanvasObject {
	return newInitialIcon(p.hooks.Deps.Images, imageCacheID(group.IconURL), group.IconURL, group.Name, size)
}

// showSection swaps the pane to one section's groups and re-heads the rail.
func (p *GroupSettingsPage) showSection(section GroupSettingsSection) {
	p.section = section
	p.mount(p.sectionGroups(section), groupSectionTitle(section))

	p.buildRail(groupSettingsRail, int(section), func(picked int) {
		p.showSection(GroupSettingsSection(picked))
	})
}

func groupSectionTitle(section GroupSettingsSection) string {
	if section == GroupSectionPermissions {
		return "Permissions"
	}

	return "Overview"
}

func (p *GroupSettingsPage) sectionGroups(section GroupSettingsSection) []settingsGroup {
	group, ok := p.hooks.Group()
	if !ok {
		return []settingsGroup{p.group("Group", "",
			p.note("This conversation is no longer one this account is in."))}
	}

	if section == GroupSectionPermissions {
		return p.permissionSection(group)
	}

	return p.overviewSection(group)
}

/* Overview */

func (p *GroupSettingsPage) overviewSection(group GroupSummary) []settingsGroup {
	return []settingsGroup{
		p.group("Group", "",
			p.nameRow(group),
			p.descriptionRow(group),
			p.iconRow(group),
			p.nsfwRow(group),
		),
		p.group("Facts", "Information about this group. None of it can be changed.",
			p.readOnlyRow("Owner", group.Owner),
			p.readOnlyRow("Created", group.Created),
			p.readOnlyRow("People", peopleCount(group.Members)),
		),
		p.group("Identifier", "The group's ID, which the API and bots identify it by.",
			p.actionRow("Group ID", group.ID, "Copy", ToneInfo, func() {
				CopyToClipboard(group.ID)
			}),
		),
	}
}

// peopleCount is the one count on this page, and the one word `plural` cannot
// make: it appends an s.
func peopleCount(n int) string {
	if n == 1 {
		return "1 person"
	}

	return fmt.Sprintf("%d people", n)
}

// The three things Overview can change, each read-only without ManageChannel —
// which for a group is a bit anybody in it may hold, the owner having it
// unconditionally. Neither field writes back what it sent: the edit returns as a
// channel update the store answers for.
func (p *GroupSettingsPage) nameRow(group GroupSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageChannel) {
		return p.readOnlyRow("Name", group.Name)
	}

	return p.row("Name", "What this conversation is called in the sidebar.",
		textField(newCommitEntry(group.Name, p.hooks.SetName)))
}

func (p *GroupSettingsPage) descriptionRow(group GroupSummary) fyne.CanvasObject {
	return p.descriptionRowOf(group.Description, "What this group is for",
		"Shown beside the name in the message header. Saved when you click away.",
		p.hooks.Can(domain.PermissionManageChannel), p.hooks.SetDescription)
}

// iconRow is the one picture a group has, and the one thing the create card
// cannot ask for: an icon is an upload into a bucket of its own, where everything
// else about a new group is a field on one request. Left out entirely without the
// permission — the strip above the rail already draws it.
func (p *GroupSettingsPage) iconRow(group GroupSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageChannel) {
		return nil
	}

	remove := newRowButton("Remove", ToneWarning, p.hooks.RemoveIcon)
	enableIf(remove, group.IconURL != "")

	side := theme.Sizes.SessionCardAvatarSize

	return p.pictureRow("Icon", "Shown wherever this conversation is named.",
		p.groupIcon(group, fyne.NewSize(side, side)), remove, p.hooks.ChangeIcon)
}

// nsfwRow is the age gate, stated read-only without the permission for the reason
// the two fields are: it is a fact about the group either way.
func (p *GroupSettingsPage) nsfwRow(group GroupSummary) fyne.CanvasObject {
	if !p.hooks.Can(domain.PermissionManageChannel) {
		return p.readOnlyRow("Age-restricted", yesNo(group.NSFW))
	}

	return p.boolRow("Age-restricted",
		"Marks the conversation as adult content.", group.NSFW, p.hooks.SetNSFW)
}

/* Permissions */

// groupPermissions is what a group's own value can usefully decide. Three bits
// come off the channel set:
//
// ViewChannel and ReadMessageHistory are in the floor Revolt ORs this value with
// (`DEFAULT_PERMISSION_VIEW_ONLY | permissions` in calculate_channel_permissions),
// so a switch for either would be a control that cannot be turned off.
// MentionRoles names something a group has not got.
const groupPermissions = channelPermissions &^
	(domain.PermissionViewChannel |
		domain.PermissionReadMessageHistory |
		domain.PermissionMentionRoles)

func (p *GroupSettingsPage) permissionSection(group GroupSummary) []settingsGroup {
	p.gridAllow, p.gridDeny = group.Permissions, 0

	head := p.group("Everybody here",
		"What each person in this group may do. Seeing the group and reading it back "+
			"cannot be taken away, and whoever made it holds everything regardless.",
		p.permissionNote(group))

	return append([]settingsGroup{head}, p.permissionGroups(p.groupScope())...)
}

// permissionNote says why the grid is read-only where it is, rather than leaving
// a page of stated values with nothing explaining them.
func (p *GroupSettingsPage) permissionNote(group GroupSummary) fyne.CanvasObject {
	if p.hooks.Can(domain.PermissionManagePermissions) {
		if group.Owned {
			return p.note("You made this group, so everything here is yours to set.")
		}

		return p.note("These apply to everybody in the group, yourself included.")
	}

	return p.note("Only somebody who may manage permissions here can change these.")
}

// groupScope is the grid aimed at a group. Two things separate it from every
// other scope:
//
// It is a plain **set**, not an overwrite — Revolt stores one value on the
// channel and ORs it with the view-only floor — so the rows are switches and the
// deny half is never sent.
//
// And `can` asks for ManagePermissions and nothing else. Every other scope also
// requires holding the bit being handed out, because `permissions_set.rs` refuses
// to grant what the actor lacks; the group arm of `permissions_set_default.rs`
// makes no such check, so a grid gated on it here would refuse what the server
// would accept.
func (p *GroupSettingsPage) groupScope() permissionScope {
	return permissionScope{
		mask:    groupPermissions,
		channel: true,
		group:   true,
		can: func(domain.Permission) bool {
			return p.hooks.Can(domain.PermissionManagePermissions)
		},
		send: func(allow, _ domain.Permission) { p.hooks.SetPermissions(allow) },
	}
}

// groupDetails rewords the entries whose explanation names something a group has
// not got. A group is a channel, so nearly every line in the grid stands as
// written; only the one naming *roles* has nothing behind it here.
//
// A map rather than a field on permissionEntry: the table is thirty-odd
// positional literals, and a fourth field filled in once would have to be written
// out in every one of them.
var groupDetails = map[domain.Permission]string{
	domain.PermissionManagePermissions: "Change what everybody in this group may do.",
}

// detailFor is the line under a permission's label, reworded where the scope asks
// for it. Every scope but a group's answers with the entry as written.
func detailFor(scope permissionScope, entry permissionEntry) string {
	if scope.group {
		if detail, ok := groupDetails[entry.permission]; ok {
			return detail
		}
	}

	return entry.detail
}
