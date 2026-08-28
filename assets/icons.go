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

//go:embed webhook.svg
var webhookSVG []byte

//go:embed masquerade.svg
var masqueradeSVG []byte

//go:embed notes.svg
var notesSVG []byte

//go:embed empty-channel.svg
var emptyChannelSVG []byte

//go:embed voice.svg
var voiceSVG []byte

//go:embed search.svg
var searchSVG []byte

//go:embed mic.svg
var micSVG []byte

//go:embed mic-off.svg
var micOffSVG []byte

//go:embed headphones.svg
var headphonesSVG []byte

//go:embed headphones-off.svg
var headphonesOffSVG []byte

//go:embed speaker-off.svg
var speakerOffSVG []byte

//go:embed speaker.svg
var speakerSVG []byte

//go:embed play.svg
var playSVG []byte

//go:embed pause.svg
var pauseSVG []byte

//go:embed call-end.svg
var callEndSVG []byte

//go:embed call-join.svg
var callJoinSVG []byte

//go:embed camera.svg
var cameraSVG []byte

//go:embed screenshare.svg
var screenshareSVG []byte

//go:embed forbidden.svg
var forbiddenSVG []byte

//go:embed cog.svg
var cogSVG []byte

//go:embed rgo.png
var appIconPNG []byte

// The settings rail's section marks. Fyne's own set has nothing for most of
// them, and the few it does have are filled where these are stroked, so the rail
// would read as two icon sets sitting together.
//
//go:embed account.svg security.svg interface.svg styles.svg behaviour.svg notify.svg cache.svg performance.svg updates.svg advanced.svg about.svg
//go:embed server-overview.svg server-channels.svg server-roles.svg server-invites.svg server-bans.svg
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

// The marks the channel-search island filters and labels a result by. A set of
// their own because they say what a message *carries* — a file, a picture, a
// link, a reaction — where every other set here says what something *is* or what
// tapping it would do. The three sort marks are in it for the same reason: they
// are read against the filters beside them, not against a settings rail.
//
//go:embed search-*.svg
var searchIcons embed.FS

// The marks on a profile's two dates. They are a set of their own rather than
// borrowed from the system events, because these say what the line beside them
// is *about* — the account, and the membership — where a system mark says which
// event a row announces.
//
//go:embed profile-*.svg
var profileIcons embed.FS

// The mark a notice wears, one per tone. Fyne's own set has all three, but they
// arrive as themed resources — a colour *name* rather than a colour — and the
// name a tone would have to borrow is mapped to something else here, so an info
// mark drawn through it disagreed with the card carrying it. Stroked, like the
// rest of the client's marks, which is what lets ui.tintedIcon give one the
// tone's exact colour.
//
//go:embed notice-*.svg
var noticeIcons embed.FS

var (
	// MentionIcon marks the "also mention the author" toggle on a reply card.
	MentionIcon fyne.Resource = fyne.NewStaticResource("mention.svg", mentionSVG)

	// MembersIcon marks the message header's member-sidebar toggle.
	MembersIcon fyne.Resource = fyne.NewStaticResource("members.svg", membersSVG)

	// BotIcon follows the name of an account Revolt marks as a bot.
	BotIcon fyne.Resource = fyne.NewStaticResource("bot.svg", botSVG)

	// WebhookIcon follows the name a webhook posted under. Its own mark rather than
	// BotIcon: a bot is an account with a profile behind it, a webhook is nobody at
	// all — which is also why its avatar opens nothing.
	WebhookIcon fyne.Resource = fyne.NewStaticResource("webhook.svg", webhookSVG)

	// MasqueradeIcon follows a name posted under a mask. The client draws the
	// account behind it rather than the override, so the mark is what says the name
	// is not what the message was posted as.
	MasqueradeIcon fyne.Resource = fyne.NewStaticResource("masquerade.svg", masqueradeSVG)

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

	// CameraIcon and ScreenshareIcon mark what somebody in a voice channel is
	// sharing, on their row under that channel in the sidebar.
	CameraIcon      fyne.Resource = fyne.NewStaticResource("camera.svg", cameraSVG)
	ScreenshareIcon fyne.Resource = fyne.NewStaticResource("screenshare.svg", screenshareSVG)

	// The call dock's toggles, each in both of its states. Stroked outlines with
	// one stroke colour, like most of the marks here — tintedIcon rewrites that
	// colour wherever it appears, a fill included.
	MicIcon           fyne.Resource = fyne.NewStaticResource("mic.svg", micSVG)
	MicOffIcon        fyne.Resource = fyne.NewStaticResource("mic-off.svg", micOffSVG)
	HeadphonesIcon    fyne.Resource = fyne.NewStaticResource("headphones.svg", headphonesSVG)
	HeadphonesOffIcon fyne.Resource = fyne.NewStaticResource("headphones-off.svg", headphonesOffSVG)

	// SpeakerOffIcon is the third mark a voice row can wear, and the only one that
	// is about this machine rather than about the person: their volume is off here
	// and nowhere else. A speaker rather than a headphone, the pair above being
	// what *they* did — and struck through the way both of those are.
	SpeakerOffIcon fyne.Resource = fyne.NewStaticResource("speaker-off.svg", speakerOffSVG)

	// SpeakerIcon is SpeakerOffIcon without the strike — the video card's sound
	// toggle at rest, so the pair reads the way the mic and headphone pairs do.
	SpeakerIcon fyne.Resource = fyne.NewStaticResource("speaker.svg", speakerSVG)

	// PlayIcon and PauseIcon are the video card's transport. The triangle is
	// solid for the handsets' reason: outlined at badge size its three strokes
	// collapse into a smudge, and a play mark has one job.
	PlayIcon  fyne.Resource = fyne.NewStaticResource("play.svg", playSVG)
	PauseIcon fyne.Resource = fyne.NewStaticResource("pause.svg", pauseSVG)

	// The two ends of a call: hang up, and join the voice channel on screen. One
	// handset at two angles, set down and lifted, and the only *solid* marks in
	// the set — outlined, a handset at the 17 units one is drawn at reads as a
	// bitten crescent, its inner and outer curves arriving a pixel apart.
	CallEndIcon  fyne.Resource = fyne.NewStaticResource("call-end.svg", callEndSVG)
	CallJoinIcon fyne.Resource = fyne.NewStaticResource("call-join.svg", callJoinSVG)

	// ForbiddenIcon leads the line the composer draws in place of its entry where
	// the account may not write.
	ForbiddenIcon fyne.Resource = fyne.NewStaticResource("forbidden.svg", forbiddenSVG)

	// CogIcon opens a server's settings from the channel sidebar's header. Drawn
	// rather than taken from Fyne's set, which is filled where this row's other
	// marks are stroked.
	CogIcon fyne.Resource = fyne.NewStaticResource("cog.svg", cogSVG)

	// AppIcon is the window/taskbar icon.
	AppIcon fyne.Resource = fyne.NewStaticResource("rgo.png", appIconPNG)
)

// The settings sections, in rail order.
var (
	AccountIcon     = settingsIcon("account.svg")
	SecurityIcon    = settingsIcon("security.svg")
	InterfaceIcon   = settingsIcon("interface.svg")
	StylesIcon      = settingsIcon("styles.svg")
	BehaviourIcon   = settingsIcon("behaviour.svg")
	NotifyIcon      = settingsIcon("notify.svg")
	CacheIcon       = settingsIcon("cache.svg")
	PerformanceIcon = settingsIcon("performance.svg")
	UpdatesIcon     = settingsIcon("updates.svg")
	AdvancedIcon    = settingsIcon("advanced.svg")
	AboutIcon       = settingsIcon("about.svg")
)

// The server settings sections, in rail order. The same set as the client's own
// — one rail is drawn by both pages — so they are read the same way.
var (
	ServerOverviewIcon = settingsIcon("server-overview.svg")
	ServerChannelsIcon = settingsIcon("server-channels.svg")
	ServerRolesIcon    = settingsIcon("server-roles.svg")
	ServerInvitesIcon  = settingsIcon("server-invites.svg")
	ServerBansIcon     = settingsIcon("server-bans.svg")
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

	// ActionOpenIcon is a box with the arrow leaving it — the video card's
	// "open in your player", something handed out of the client rather than
	// saved into it.
	ActionOpenIcon = actionIcon("action-open.svg")

	// The pair a list row offers its place in an order with — a server settings
	// list rather than a message, but the same set: an action is drawn the same
	// way wherever it is offered.
	ActionUpIcon   = actionIcon("action-up.svg")
	ActionDownIcon = actionIcon("action-down.svg")

	// ActionUnblockIcon is ForbiddenIcon's answer — the same circle with a tick
	// where the bar was — so the two read as one pair. It is the friends page's
	// only new mark: accepting a request is ActionSaveIcon, declining one
	// ActionCancelIcon, and adding or removing somebody is the pair the system
	// messages already draw.
	ActionUnblockIcon = actionIcon("action-unblock.svg")

	// ActionEmojiIcon opens the emoji picker from the composer. It is in this set
	// rather than beside the reaction chip's mark because both open the same
	// picker. Alone among the marks it is a filled silhouette rather than an
	// outline — a stoat at 20 units has no room for a stroke to describe it.
	ActionEmojiIcon = actionIcon("action-emoji.svg")

	// ActionGIFIcon opens the GIF picker from the composer, beside it. Its play
	// mark is filled for the reason the two handsets are: a triangle 5 units on a
	// side has no room for a stroke and its own hole.
	ActionGIFIcon = actionIcon("action-gif.svg")
)

// A profile's dates: when the account was made, and when it joined the server
// the profile was opened in.
var (
	ProfileCreatedIcon = profileIcon("profile-created.svg")
	ProfileJoinedIcon  = profileIcon("profile-joined.svg")
)

// The channel-search island's marks: what a result carries, where a row leads,
// and the three orders a search can be asked for. Mentions, authorship and pins
// are not here — MentionIcon, AccountIcon and SystemPinnedIcon already say those
// three, and a second drawing of each would be the same thing twice in one row.
var (
	SearchAttachmentIcon = searchIcon("search-attachment.svg")
	SearchImageIcon      = searchIcon("search-image.svg")
	SearchLinkIcon       = searchIcon("search-link.svg")
	SearchReactionIcon   = searchIcon("search-reaction.svg")
	SearchJumpIcon       = searchIcon("search-jump.svg")
	SearchDateIcon       = searchIcon("search-date.svg")

	SearchRelevanceIcon = searchIcon("search-relevance.svg")
	SearchNewestIcon    = searchIcon("search-newest.svg")
	SearchOldestIcon    = searchIcon("search-oldest.svg")
)

// The three tones a notice or a confirmation is drawn in, one mark each.
var (
	NoticeInfoIcon    = noticeIcon("notice-info.svg")
	NoticeWarningIcon = noticeIcon("notice-warning.svg")
	NoticeDangerIcon  = noticeIcon("notice-danger.svg")
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

// searchIcon reads one of the embedded channel-search marks.
func searchIcon(name string) fyne.Resource {
	return embedded(searchIcons, name)
}

// actionIcon reads one of the embedded message-action marks.
func actionIcon(name string) fyne.Resource {
	return embedded(actionIcons, name)
}

// profileIcon reads one of the embedded profile marks.
func profileIcon(name string) fyne.Resource {
	return embedded(profileIcons, name)
}

// noticeIcon reads one of the embedded tone marks.
func noticeIcon(name string) fyne.Resource {
	return embedded(noticeIcons, name)
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
