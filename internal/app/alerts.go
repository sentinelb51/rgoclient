package app

// Alerts: everything the client does about something the reader did not ask to
// be told — a sound, the taskbar button flashing, and a card in the corner
// carrying the message itself where it was addressed to them.
//
// Revolt's own push notifications are out of reach and always will be:
// /push/subscribe takes a Web Push subscription, which is a browser's service
// worker and a push service to deliver through. This client holds an open
// gateway instead, so a ping is already here — what was missing is only the
// noticing of it.

import (
	"log"

	"RGOClient/internal/audio"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

/* The catalogue */

// soundEntry binds a sound to the setting that turns it on and the copy the
// settings page draws it under. One table rather than a question answered three
// ways: playing one, listing them all and pointing one at a file are three walks
// of the same set, and a sound added to only two of them is a sound that is
// heard and cannot be turned off.
type soundEntry struct {
	key     string
	title   string
	summary string

	// enabled is the flag this sound answers to. Several share one — the four
	// keystrokes are a single choice, and so are the two connection sounds.
	enabled func(config.Notifications) bool

	// gated marks a sound that obeys "play sounds while the window is in focus".
	// Only what somebody *else* caused is gated: a send, a failure and a lost
	// connection are answers to the user's own action or to the client's state, and
	// they happen while the window is in front by definition.
	gated bool
}

// soundCatalogue is every sound, in the order the settings page lists them.
var soundCatalogue = []soundEntry{
	{
		key: audio.Mention, title: "Mention",
		summary: "Somebody named you, or addressed everyone in a channel.",
		enabled: func(n config.Notifications) bool { return n.PlayMention }, gated: true,
	},
	{
		key: audio.Direct, title: "Direct message",
		summary: "A message in a conversation you aren't reading.",
		enabled: func(n config.Notifications) bool { return n.PlayDirect }, gated: true,
	},
	{
		key: audio.Message, title: "Message",
		summary: "Any other message, in a channel you aren't reading.",
		enabled: func(n config.Notifications) bool { return n.PlayMessage }, gated: true,
	},
	{
		key: audio.Ambient, title: "Message here",
		summary: "A message in the channel that's open.",
		enabled: func(n config.Notifications) bool { return n.PlayAmbient }, gated: true,
	},
	{
		key: audio.Send, title: "Sent",
		summary: "A message of yours going out.",
		enabled: func(n config.Notifications) bool { return n.PlaySend },
	},
	{
		key: audio.Friend, title: "Friend request",
		summary: "Somebody asked to be friends, or accepted.",
		enabled: func(n config.Notifications) bool { return n.PlayFriend }, gated: true,
	},
	{
		key: audio.Reaction, title: "Reaction",
		summary: "Somebody reacted to a message of yours.",
		enabled: func(n config.Notifications) bool { return n.PlayReaction }, gated: true,
	},
	{
		key: audio.Error, title: "Something failed",
		summary: "An action of yours was refused.",
		enabled: func(n config.Notifications) bool { return n.PlayError },
	},
	{
		key: audio.Offline, title: "Disconnected",
		summary: "The session ended.",
		enabled: func(n config.Notifications) bool { return n.PlayConnection },
	},
	{
		key: audio.Online, title: "Reconnected",
		summary: "A session came back after one dropped.",
		enabled: func(n config.Notifications) bool { return n.PlayConnection },
	},

	{
		key: audio.KeyPress, title: "Key",
		summary: "An ordinary character in the composer.",
		enabled: typingEnabled,
	},
	{
		key: audio.KeySpace, title: "Space",
		summary: "The space bar, deeper than the rest.",
		enabled: typingEnabled,
	},
	{
		key: audio.KeyBackspace, title: "Backspace",
		summary: "Backspace and delete.",
		enabled: typingEnabled,
	},
	{
		key: audio.KeyEnter, title: "Enter",
		summary: "The keystroke that sends.",
		enabled: typingEnabled,
	},
}

func typingEnabled(n config.Notifications) bool { return n.TypingSounds }

// soundOf finds a sound's entry. A key not in the table is not played rather
// than played under a default: the table is what says a sound can be turned off.
func soundOf(key string) (soundEntry, bool) {
	for _, entry := range soundCatalogue {
		if entry.key == key {
			return entry, true
		}
	}

	return soundEntry{}, false
}

/* Starting up */

// startAlerts starts following focus and loads every sound. The engine itself is
// built in New — a nil one is a panic on the first notice, and the settings are
// read on the way to it rather than around it — and it opens no device until a
// sound is actually played, so a client with sounds off never takes one.
//
// Focus comes from Fyne's foreground hooks, which the desktop driver fires from
// its own goroutine, hence the atomic. It is the only part of App that is not
// UI-thread confined.
func (a *App) startAlerts() {
	// The frame rate follows the focus (config.Performance.BackgroundFrameRate)
	// and so does what the OS is asked to spend on the process, so the hooks
	// re-apply it. Fyne keeps one callback per hook: anything else that needs to
	// follow focus joins these closures rather than registering. a.focused itself
	// starts true in Run, ahead of the first thing to read it.
	lifecycle := a.fyne.Lifecycle()
	lifecycle.SetOnEnteredForeground(func() { a.focused.Store(true); a.applyFrameRate() })
	lifecycle.SetOnExitedForeground(func() { a.focused.Store(false); a.applyFrameRate() })

	// The mixer's own atomic starts false, and applyVoiceSettings only runs when
	// something is changed: without this a client that never joins a call and
	// never opens the page clips its notification sounds hard whatever the setting
	// says. Everything else the mixer holds is set by a join.
	a.sounds.SetSoftClip(config.Current().Voice.SoftClip)

	// Before loadSounds, not after: the board decides what a built-in keystroke is
	// synthesised as, so naming it second would install four clicks from the
	// default and replace them a moment later.
	a.sounds.SetTypingProfile(config.Current().Notifications.TypingProfile)

	go a.loadSounds()
}

// applyTypingProfile rebuilds the keystrokes from another board and plays one.
// Only the four typing sounds, and only those still on a built-in — a file the
// reader pointed a key at outranks a board they picked, and loadSounds' own
// fallback covers the file that has since gone away.
//
// Off the UI thread: a board is two dozen renders, which is not something to do
// between two frames.
func (a *App) applyTypingProfile(name string) {
	a.sounds.SetTypingProfile(name)

	files := config.Current().Notifications.SoundFiles
	epoch := a.epoch

	go func() {
		for _, key := range audio.Keys {
			if !audio.IsTyping(key) || files[key] != "" {
				continue
			}

			if err := a.sounds.Set(key, ""); err != nil {
				log.Printf("load built-in sound %s: %v", key, err)
			}
		}

		a.doOnUI(func() {
			if !a.stale(epoch) {
				a.previewSound(audio.KeyPress)
			}
		}, false)
	}()
}

// loadSounds installs every sound, each from its own file where one is set.
// Called off the UI thread: a custom file is decoded here, and the built-ins are
// synthesised rather than read, which is not free either.
//
// A file that cannot be read falls back to the built-in rather than leaving the
// sound silent, and says so once — a client that quietly stops pinging is a
// client the user believes is broken.
func (a *App) loadSounds() {
	files := config.Current().Notifications.SoundFiles

	for _, entry := range soundCatalogue {
		path := files[entry.key]
		if err := a.sounds.Set(entry.key, path); err != nil {
			log.Printf("load sound %s from %s: %v", entry.key, path, err)

			if err := a.sounds.Set(entry.key, ""); err != nil {
				log.Printf("load built-in sound %s: %v", entry.key, err)
			}
		}
	}
}

// reloadSound installs one sound after its file has changed, reports a file it
// cannot read, and plays the result — pointing a setting at a file is the one
// change whose whole point is what it sounds like.
func (a *App) reloadSound(key string) {
	path := config.Current().Notifications.SoundFiles[key]
	epoch := a.epoch

	go func() {
		err := a.sounds.Set(key, path)
		if err != nil {
			log.Printf("load sound %s from %s: %v", key, path, err)

			if fallback := a.sounds.Set(key, ""); fallback != nil {
				log.Printf("load built-in sound %s: %v", key, fallback)
			}
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				a.notify(ui.ToneDanger, "Could not read that sound file. %s is back to the built-in.", soundTitle(key))
				return
			}

			a.previewSound(key)
		}, false)
	}()
}

// soundTitle names a sound for a notice. The key is what the settings file holds
// and not something to show anybody.
func soundTitle(key string) string {
	if entry, ok := soundOf(key); ok {
		return entry.title
	}

	return "That sound"
}

/* Playing */

// playSound sounds a key if its settings allow it. Every decision about whether
// a sound is heard is here rather than at the call sites: a handler should say
// what happened, not work out whether it is audible.
func (a *App) playSound(key string) {
	entry, ok := soundOf(key)
	if !ok {
		return
	}

	notifications := config.Current().Notifications
	if !notifications.Sounds || !entry.enabled(notifications) {
		return
	}
	if entry.gated && !notifications.SoundsWhenFocused && a.focused.Load() {
		return
	}

	a.sounds.Play(key, volumeFor(key, notifications))
}

// previewSound plays one whatever the settings say about it, for the settings
// page's own button: a sound is chosen by hearing it, which a switch that is
// still off would otherwise prevent. The volume is still the configured one —
// that is part of what is being judged.
func (a *App) previewSound(key string) {
	a.sounds.Play(key, volumeFor(key, config.Current().Notifications))
}

// volumeFor is the level a sound plays at, 0 to 1. Typing has its own because it
// plays hundreds of times a minute against a ping's handful, and one slider for
// both would be set by whichever the user minded more.
func volumeFor(key string, n config.Notifications) float64 {
	if audio.IsTyping(key) {
		return float64(n.TypingVolume) / 100
	}

	return float64(n.SoundVolume) / 100
}

// noteKeystroke sounds the click under a keystroke. It is the one alert on a
// path that runs per character, so it does nothing but pick a key — the engine
// drops what it cannot keep up with, and the settings are read there.
func (a *App) noteKeystroke(kind ui.Keystroke) {
	switch kind {
	case ui.KeystrokeSpace:
		a.playSound(audio.KeySpace)
	case ui.KeystrokeErase:
		a.playSound(audio.KeyBackspace)
	case ui.KeystrokeSend:
		a.playSound(audio.KeyEnter)
	default:
		a.playSound(audio.KeyPress)
	}
}

/* The settings page */

// settingsSounds lists the sounds for the Notifications section. The file each
// is pointed at is read here rather than held: the page is rebuilt after every
// change to one, and a copy kept beside the settings is a second thing to keep
// true.
// settingsTypingProfiles is the boards on offer, converted at the seam the way a
// device list is. `ui` does not import `audio`, and what a board is made of is
// no business of the row that names it.
func settingsTypingProfiles() []ui.TypingProfile {
	boards := audio.TypingProfiles()

	profiles := make([]ui.TypingProfile, len(boards))
	for i, board := range boards {
		profiles[i] = ui.TypingProfile{Value: board.Value, Label: board.Label}
	}

	return profiles
}

func settingsSounds() []ui.SettingsSound {
	files := config.Current().Notifications.SoundFiles

	sounds := make([]ui.SettingsSound, len(soundCatalogue))
	for i, entry := range soundCatalogue {
		sounds[i] = ui.SettingsSound{
			Key:     entry.key,
			Title:   entry.title,
			Summary: entry.summary,
			File:    files[entry.key],
			Typing:  audio.IsTyping(entry.key),
		}
	}

	return sounds
}

// chooseSound points one sound at a file of the user's own. onPicked redraws the
// row — the file it now names, and the way back to the built-in it did not offer
// before — and the sound itself is loaded and played by reloadSound.
func (a *App) chooseSound(key string, onPicked func()) {
	a.chooseFile("Choose a sound", soundFilter, func(path, _ string) {
		a.updateSettings(func(s *config.Settings) {
			if s.Notifications.SoundFiles == nil {
				s.Notifications.SoundFiles = make(map[string]string)
			}
			s.Notifications.SoundFiles[key] = path
		})

		a.reloadSound(key)
		onPicked()
	})
}

// resetSound puts the built-in back. The key is deleted rather than emptied, so
// the settings file keeps only what has actually been chosen.
func (a *App) resetSound(key string) {
	a.updateSettings(func(s *config.Settings) {
		delete(s.Notifications.SoundFiles, key)
	})

	a.reloadSound(key)
}

// soundFilter is what the decoder reads. The filter is a courtesy — the file is
// sniffed by content, not by its name — so a renamed file still works and a
// picker that hid it would be the only thing in the way.
var soundFilter = ui.FileFilter{
	Label:      "Sounds",
	Extensions: []string{".wav", ".mp3"},
}

/* What a message is worth */

// noticePreviewRunes is how much of a message its notice carries. Shorter than
// an island card's line: the card wraps to as many lines as it is given, and a
// corner notice standing over what is being read has to stay a glance.
const noticePreviewRunes = 80

// alertMessage sounds an incoming message, flashes the taskbar for one worth
// coming back for, and puts the message itself on the notice layer where it is
// addressed to the reader. Our own messages are not announced to us — the send
// sound is what answers those, and it plays where the message is sent rather than
// when it echoes back.
//
// Call on the UI thread.
func (a *App) alertMessage(message *domain.Message) {
	if message == nil || message.AuthorID == a.store.SelfID() {
		return
	}

	notifications := config.Current().Notifications
	sound, worthFlashing := a.messageAlert(message, notifications)

	a.playSound(sound)

	if worthFlashing && notifications.FlashTaskbar {
		ui.FlashTaskbar(a.window)
	}

	a.noticeMessage(message, notifications)
}

// noticeMessage puts an incoming message on the notice layer: who sent it, the
// line they wrote, their face, and a tap that goes to the message. Only the two
// kinds addressed to the reader, each answering to its own switch — anything
// wider is a card in the corner for every message in every server.
//
// Never for the channel on screen, which is already showing it, and never gated
// by the tone switches: those name which outcomes are worth reporting, and this
// is not an outcome of anything the reader did. Call on the UI thread.
func (a *App) noticeMessage(message *domain.Message, n config.Notifications) {
	if message.ChannelID == a.currentChannelID {
		return
	}

	channel, ok := a.store.Channel(message.ChannelID)
	if !ok {
		return
	}

	// Either switch is enough on its own: a direct message that also names the
	// reader is both, and neither flag should be the one that swallows it.
	mentioned := message.MentionsUser(a.store.SelfID())
	direct := channel.Kind == domain.ChannelDM || channel.Kind == domain.ChannelGroup

	if !(mentioned && n.ShowMention) && !(direct && n.ShowDirect) {
		return
	}

	author := a.store.MessageAuthor(message)
	channelID, messageID := message.ChannelID, message.ID

	a.notices.PushNotice(ui.Notice{
		Tone:  ui.ToneInfo,
		Title: noticeHeading(channel, author.Name),
		Body:  util.Truncate(a.messagePreview(message), noticePreviewRunes),

		AvatarURL: author.AvatarURL,
		Initial:   author.Name,

		OnTap:      func() { a.jumpToMessageIn(channelID, messageID) },
		Unfiltered: true,
	})
}

// noticeHeading names a message's sender and, where that alone would not say
// which conversation it belongs to, where they said it. A direct message is only
// ever from the one person, so their name is the whole of it; anywhere else the
// same name arrives from several places at once.
func noticeHeading(channel domain.Channel, name string) string {
	if channel.Kind == domain.ChannelDM {
		return name
	}

	where := channel.Name
	if channel.ServerID != "" {
		where = "#" + channel.Name
	}

	return name + " in " + where
}

// messageAlert decides which sound a message is and whether it is worth pulling
// somebody back to the window for. Ordered by how much the message is *about*
// the reader: being named outranks the conversation it arrived in, which
// outranks it being the channel on screen.
func (a *App) messageAlert(message *domain.Message, n config.Notifications) (string, bool) {
	if message.MentionsUser(a.store.SelfID()) {
		return audio.Mention, n.AlertOnMention
	}

	if channel, ok := a.store.Channel(message.ChannelID); ok && channel.ServerID == "" {
		return audio.Direct, n.AlertOnDirect
	}

	if message.ChannelID == a.currentChannelID {
		return audio.Ambient, false
	}

	return audio.Message, false
}

// alertReaction sounds a reaction somebody else put on a message of ours. The
// message is looked up rather than carried: the client has already written the
// new state by the time the event arrives, so the only question left is whose
// message it was.
func (a *App) alertReaction(channelID, messageID, byUserID string) {
	self := a.store.SelfID()
	if byUserID == "" || byUserID == self || self == "" {
		return
	}

	message := a.ResolveMessage(channelID, messageID)
	if message == nil || message.AuthorID != self {
		return
	}

	a.playSound(audio.Reaction)
}

// alertRelationship sounds a friend request arriving or being accepted. The
// other four states are not announced: asking somebody, being blocked and
// unblocking are the user's own doing or somebody else's silence, and neither is
// something to look up for.
func (a *App) alertRelationship(userID string) {
	user, ok := a.store.User(userID)
	if !ok {
		return
	}

	switch user.Relationship {
	case domain.RelationshipIncoming, domain.RelationshipFriend:
		a.playSound(audio.Friend)

		if config.Current().Notifications.FlashTaskbar {
			ui.FlashTaskbar(a.window)
		}
	}
}
