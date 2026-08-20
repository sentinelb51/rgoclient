package assets

import (
	"embed"

	"fyne.io/fyne/v2"
)

// Icons are embedded rather than read from disk so the client runs correctly
// from any working directory and never pays file I/O while building widgets.
// Everything else the UI draws comes from Fyne's own theme icon set.

//go:embed mention.svg
var mentionSVG []byte

//go:embed members.svg
var membersSVG []byte

//go:embed bot.svg
var botSVG []byte

//go:embed notes.svg
var notesSVG []byte

//go:embed empty-channel.svg
var emptyChannelSVG []byte

//go:embed voice.svg
var voiceSVG []byte

//go:embed search.svg
var searchSVG []byte

//go:embed forbidden.svg
var forbiddenSVG []byte

//go:embed rgo.png
var appIconPNG []byte

// The settings rail's section marks. Fyne's own set has nothing for most of
// them, and the few it does have are filled where these are stroked, so the rail
// would read as two icon sets sitting together.
//
//go:embed account.svg interface.svg styles.svg behaviour.svg notify.svg cache.svg performance.svg advanced.svg about.svg
var settingsIcons embed.FS

// The marks a system message is announced by, one per event Revolt names. They
// replace the avatar of a message nobody wrote, so the event is read off the
// glyph rather than off the sentence.
//
//go:embed system-*.svg
var systemIcons embed.FS

// The marks on a message's own actions — the hover buttons, its context menu and
// the save/cancel pair an edit puts in their place. Fyne's set has all of these,
// filled where the rest of the client is stroked; more to the point they arrive
// as themed resources, so the one thing they can't be given is a colour of their
// own, and delete reading as delete is the point of this set.
//
//go:embed action-*.svg
var actionIcons embed.FS

// The marks on a profile's two dates. They are a set of their own rather than
// borrowed from the system events, because these say what the line beside them
// is *about* — the account, and the membership — where a system mark says which
// event a row announces.
//
//go:embed profile-*.svg
var profileIcons embed.FS

var (
	// MentionIcon marks the "also mention the author" toggle on a reply card.
	MentionIcon fyne.Resource = fyne.NewStaticResource("mention.svg", mentionSVG)

	// MembersIcon marks the message header's member-sidebar toggle.
	MembersIcon fyne.Resource = fyne.NewStaticResource("members.svg", membersSVG)

	// BotIcon follows the name of an account Revolt marks as a bot.
	BotIcon fyne.Resource = fyne.NewStaticResource("bot.svg", botSVG)

	// NotesIcon prefixes Saved Notes, the one conversation that is nobody else —
	// so the avatar every other conversation is led by would be this account's own
	// picture standing in for a notepad.
	NotesIcon fyne.Resource = fyne.NewStaticResource("notes.svg", notesSVG)

	// EmptyChannelIcon leads the line the message column draws when a channel has
	// nothing in it.
	EmptyChannelIcon fyne.Resource = fyne.NewStaticResource("empty-channel.svg", emptyChannelSVG)

	// VoiceIcon prefixes a server's voice channels and leads the note saying the
	// client can only type in one.
	VoiceIcon fyne.Resource = fyne.NewStaticResource("voice.svg", voiceSVG)

	// SearchIcon opens the channel-search panel from the message header.
	SearchIcon fyne.Resource = fyne.NewStaticResource("search.svg", searchSVG)

	// ForbiddenIcon leads the line the composer draws in place of its entry where
	// the account may not write.
	ForbiddenIcon fyne.Resource = fyne.NewStaticResource("forbidden.svg", forbiddenSVG)

	// AppIcon is the window/taskbar icon.
	AppIcon fyne.Resource = fyne.NewStaticResource("rgo.png", appIconPNG)
)

// The settings sections, in rail order.
var (
	AccountIcon     = settingsIcon("account.svg")
	InterfaceIcon   = settingsIcon("interface.svg")
	StylesIcon      = settingsIcon("styles.svg")
	BehaviourIcon   = settingsIcon("behaviour.svg")
	NotifyIcon      = settingsIcon("notify.svg")
	CacheIcon       = settingsIcon("cache.svg")
	PerformanceIcon = settingsIcon("performance.svg")
	AdvancedIcon    = settingsIcon("advanced.svg")
	AboutIcon       = settingsIcon("about.svg")
)

// The system events, in the order domain names them. SystemEventIcon is what an
// event the client does not know draws: Revolt's vocabulary is carried verbatim,
// so a kind added later has to land somewhere.
var (
	SystemJoinedIcon      = systemIcon("system-joined.svg")
	SystemLeftIcon        = systemIcon("system-left.svg")
	SystemAddedIcon       = systemIcon("system-added.svg")
	SystemRemovedIcon     = systemIcon("system-removed.svg")
	SystemKickedIcon      = systemIcon("system-kicked.svg")
	SystemBannedIcon      = systemIcon("system-banned.svg")
	SystemRenamedIcon     = systemIcon("system-renamed.svg")
	SystemDescriptionIcon = systemIcon("system-description.svg")
	SystemPictureIcon     = systemIcon("system-picture.svg")
	SystemOwnerIcon       = systemIcon("system-owner.svg")
	SystemPinnedIcon      = systemIcon("system-pinned.svg")
	SystemUnpinnedIcon    = systemIcon("system-unpinned.svg")
	SystemCallIcon        = systemIcon("system-call.svg")
	SystemEventIcon       = systemIcon("system-event.svg")
)

// A message's actions. Copying the author's ID is the one that already had a
// mark in the house style — the settings rail's — so it borrows AccountIcon
// rather than carrying the same person twice.
var (
	ActionReplyIcon  = actionIcon("action-reply.svg")
	ActionEditIcon   = actionIcon("action-edit.svg")
	ActionDeleteIcon = actionIcon("action-delete.svg")
	ActionMoreIcon   = actionIcon("action-more.svg")
	ActionCopyIcon   = actionIcon("action-copy.svg")
	ActionSaveIcon   = actionIcon("action-save.svg")
	ActionCancelIcon = actionIcon("action-cancel.svg")
	ActionAddIcon    = actionIcon("action-add.svg")

	// ActionEmojiIcon opens the emoji picker from the composer. It is in this set
	// rather than beside the reaction chip's mark because both open the same
	// picker. Alone among the marks it is a filled silhouette rather than an
	// outline — a stoat at 20 units has no room for a stroke to describe it.
	ActionEmojiIcon = actionIcon("action-emoji.svg")
)

// A profile's dates: when the account was made, and when it joined the server
// the profile was opened in.
var (
	ProfileCreatedIcon = profileIcon("profile-created.svg")
	ProfileJoinedIcon  = profileIcon("profile-joined.svg")
)

// settingsIcon reads one of the embedded section marks. The file list is a
// compile-time embed, so a name that is not there is a build error rather than
// something to report at runtime.
func settingsIcon(name string) fyne.Resource {
	return embedded(settingsIcons, name)
}

// systemIcon reads one of the embedded system-event marks.
func systemIcon(name string) fyne.Resource {
	return embedded(systemIcons, name)
}

// actionIcon reads one of the embedded message-action marks.
func actionIcon(name string) fyne.Resource {
	return embedded(actionIcons, name)
}

// profileIcon reads one of the embedded profile marks.
func profileIcon(name string) fyne.Resource {
	return embedded(profileIcons, name)
}

// embedded reads a file out of one of the icon sets. Every set is a compile-time
// embed, so a name that is not there is a build error rather than something to
// report at runtime — the panic only covers a set whose pattern stopped matching.
func embedded(set embed.FS, name string) fyne.Resource {
	data, err := set.ReadFile(name)
	if err != nil {
		panic(err)
	}

	return fyne.NewStaticResource(name, data)
}
