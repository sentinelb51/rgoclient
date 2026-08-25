// Package config is the client's persisted settings and the accessor every other
// package reads them through. It imports nothing internal so everything above it
// can read a setting.
//
// The current settings live behind an atomic pointer and are handed out as a
// snapshot. Update clones, mutates and republishes, so a reader never observes a
// half-written value and callers off the UI thread need no lock.
package config

import (
	"encoding/json"
	"log"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// settingsFile is the settings store, beside the saved-session store in the home
// directory.
const settingsFile = ".rgoclient_settings.json"

// saveDelay is how long a change waits for another before it is written. A hex
// field or a dragged slider produces a change per keystroke or per frame, and
// none of them is worth a file write of its own.
const saveDelay = time.Second

/* The settings tree */

// Settings is the whole of what the client persists between runs.
type Settings struct {
	Interface     Interface     `json:"interface"`
	Styles        Styles        `json:"styles"`
	Behaviour     Behaviour     `json:"behaviour"`
	Notifications Notifications `json:"notifications"`
	Voice         Voice         `json:"voice"`
	Cache         Cache         `json:"cache"`
	Performance   Performance   `json:"performance"`
}

// Interface is the friendly layer: choices made in the user's terms, each of
// which the app turns into one or more of the numbers in Styles.
type Interface struct {
	/* Appearance */

	Accent  string `json:"accent"`  // "#RRGGBB", "" for the palette's own
	Density string `json:"density"` // DensityCosy, DensityCompact, DensityTiny
	// FontSize is what Fyne's built-in widgets draw at; the client's own text
	// sizes are named entries in the size table.
	FontSize float32 `json:"font_size"`

	/* Formatting */

	TimeFormat  string `json:"time_format"` // TimeFormat12 or TimeFormat24
	ShowSeconds bool   `json:"show_seconds"`

	/* Layout */

	GroupMessages     bool `json:"group_messages"`
	ShowMemberSidebar bool `json:"show_member_sidebar"`
	ThemeTitleBar     bool `json:"theme_title_bar"`

	/* Disclosure */

	// AdvancedMode reveals the settings that tune the client rather than describe
	// it: timings, mount caps, cache budgets and the raw size and colour tables.
	AdvancedMode bool `json:"advanced_mode"`
}

// Styles holds only what differs from the defaults, keyed by the exact field
// name in the theme's size and colour tables. Storing overrides rather than the
// whole table is what lets a newly named size arrive with its default intact,
// and it keeps a hand-edited file short enough to read.
type Styles struct {
	Sizes  map[string]float32 `json:"sizes,omitempty"`
	Colors map[string]string  `json:"colors,omitempty"` // "#RRGGBB" or "#RRGGBBAA"
}

// Behaviour is what the client does rather than how it looks: the work it takes
// on per event, and how much of the conversation it keeps mounted.
type Behaviour struct {
	// Members. The one part of the client whose cost scales with somebody else's
	// server rather than with anything the user did: what is fetched, what is
	// drawn, and how often a list of thousands is rebuilt.

	SortMembers         bool `json:"sort_members"`
	GroupByPresence     bool `json:"group_by_presence"`
	HoistRoles          bool `json:"hoist_roles"`
	HideOfflineMembers  bool `json:"hide_offline_members"`
	HideRolelessMembers bool `json:"hide_roleless_members"`
	FetchAllMembers     bool `json:"fetch_all_members"`
	LiveMemberPresence  bool `json:"live_member_presence"`
	ShowSelfFirst       bool `json:"show_self_first"`

	// MemberListFallback shows the whole membership when the two hiding settings
	// have left nothing to draw. A sidebar that is empty because of a setting
	// reads exactly like one that is empty because the fetch failed.
	MemberListFallback bool `json:"member_list_fallback"`

	MemberOverscan int `json:"member_overscan"`

	// Typing indicators. TypingNames is the master switch as well as the limit:
	// at zero nothing is drawn on either surface and the gateway event is dropped
	// where it arrives, so the whole receiving half costs nothing.

	SendTyping       bool `json:"send_typing"`
	TypingNames      int  `json:"typing_names"`
	TypingShowSelf   bool `json:"typing_show_self"`
	TypingInChannels bool `json:"typing_in_channels"`
	TypingAvatars    bool `json:"typing_avatars"`
	TypingAnimation  bool `json:"typing_animation"`

	/* Messages */

	GroupWindowSeconds int `json:"group_window_seconds"`
	InitialMountCount  int `json:"initial_mount_count"`
	MountedCap         int `json:"mounted_cap"`
	HistoryPageSize    int `json:"history_page_size"`
	MessageOverscan    int `json:"message_overscan"`

	/* Timing */

	AuthorFetchDelayMS int `json:"author_fetch_delay_ms"`
	AckDelayMS         int `json:"ack_delay_ms"`

	// RefreshDelayMS is the settling window every sidebar-invalidating gateway
	// event is coalesced over. One knob rather than one per surface: the user is
	// choosing how long a burst may gather, not which burst.
	RefreshDelayMS int `json:"refresh_delay_ms"`

	/* Input */

	ScrollSpeed int  `json:"scroll_speed"`
	EnterSends  bool `json:"enter_sends"`
}

// Notifications configures the transient notice layer, what the client does
// about a message the reader is not looking at, and every sound it makes.
type Notifications struct {
	/* The notice layer */

	LifetimeSeconds int `json:"lifetime_seconds"`
	MaxStacked      int `json:"max_stacked"`

	ShowInfo    bool `json:"show_info"`
	ShowWarning bool `json:"show_warning"`
	ShowDanger  bool `json:"show_danger"`

	// ModalSeconds is how long the centred notice holds the middle of the window.
	// Shorter than a corner card's lifetime by default: that one waits to be read
	// beside what it is about, where this one is already in front of the reader.
	ModalSeconds int `json:"modal_seconds"`

	/* Reaching the user outside the window */

	// FlashTaskbar flashes the window's taskbar button. It is the whole of the
	// out-of-app half — see docs/known-gaps.md on why there is no toast — and does
	// nothing while the window already has focus, so the two flags below decide
	// only what is worth a flash when it does not.
	FlashTaskbar   bool `json:"flash_taskbar"`
	AlertOnMention bool `json:"alert_on_mention"`
	AlertOnDirect  bool `json:"alert_on_direct"`

	/* Sounds */

	// Sounds is the master switch and the off switch both: with it off no sound is
	// asked for, so the client never opens an audio device at all.
	Sounds bool `json:"sounds"`

	// SoundVolume scales everything but a keystroke, 0-100. Typing has its own
	// because it plays hundreds of times a minute against a ping's handful.
	SoundVolume int `json:"sound_volume"`

	// SoundsWhenFocused plays them while the window is in front. Off makes the
	// whole set an away-from-keyboard signal.
	SoundsWhenFocused bool `json:"sounds_when_focused"`

	PlayMention    bool `json:"play_mention"`
	PlayDirect     bool `json:"play_direct"`
	PlayMessage    bool `json:"play_message"`
	PlayAmbient    bool `json:"play_ambient"`
	PlaySend       bool `json:"play_send"`
	PlayFriend     bool `json:"play_friend"`
	PlayReaction   bool `json:"play_reaction"`
	PlayError      bool `json:"play_error"`
	PlayConnection bool `json:"play_connection"`

	/* Typing */

	TypingSounds bool `json:"typing_sounds"`
	TypingVolume int  `json:"typing_volume"`

	// SoundFiles holds only the sounds pointed at a file of their own, keyed by the
	// sound's name — the same reason Styles stores overrides rather than the whole
	// table. An absent key is the built-in, which is synthesised rather than read,
	// so a sound named in a later version arrives audible instead of silent.
	SoundFiles map[string]string `json:"sound_files,omitempty"`
}

// Cache configures the on-disk and in-memory caches. AssetDir, TextPreviews,
// MessagesPerChannel and CachedChannels are read once while the caches are being
// built, so changing them takes a restart; the image budgets are held on the
// cache itself and apply as soon as they are set.
type Cache struct {
	// AssetDir is the root the picture caches keep their folders under, one per
	// class of picture — "" for the user's cache directory.
	AssetDir string `json:"asset_dir"`

	ImageDiskMiB   int `json:"image_disk_mib"`
	ImageMemoryMiB int `json:"image_memory_mib"`
	MaxImageEdge   int `json:"max_image_edge"`

	// ImageLoaders bounds how many pictures are downloaded at once. A member list
	// scrolled quickly asks for a picture per row it passes, and without a bound
	// that is one goroutine and one connection each.
	ImageLoaders int `json:"image_loaders"`

	TextPreviews       int `json:"text_previews"`
	MessagesPerChannel int `json:"messages_per_channel"`
	CachedChannels     int `json:"cached_channels"`
}

// Performance is what the client asks of the toolkit rather than of itself: how
// often the window may draw, whether it waits for the display before showing
// what it drew, and whether it redraws only what changed. Stock Fyne draws at
// 60, leaves vsync to the driver and repaints the whole window, with no way to
// say otherwise, so these reach the toolkit only through the patched copy —
// see github.com/sentinelb51/rgoclient-fyne.
type Performance struct {
	// FrameRate is a ceiling rather than a rate. The driver wakes this many times
	// a second to poll input, advance animations and consider a repaint; a window
	// with nothing to redraw wakes as often and draws nothing, so what raising it
	// costs is paid while something is moving.
	FrameRate int `json:"frame_rate"`

	// VSync waits for the display's next refresh before presenting. On, the rate
	// above cannot exceed the monitor's and the wait blocks the whole driver loop,
	// input and queued work included. Off, a frame is shown as soon as it is drawn
	// and a fast scroll can tear.
	VSync bool `json:"vsync"`

	// PartialRepaint redraws only the regions that changed since the previous
	// frame, restoring the rest from a snapshot of it. Off is upstream Fyne's
	// behaviour — every change clears and redraws the whole window — kept as the
	// escape hatch for a stale-pixel artifact and the baseline for measuring.
	PartialRepaint bool `json:"partial_repaint"`

	// Cores is which of the machine's cores the client is allowed to run on:
	// CoresAll, plus CoresEfficiency / CoresPerformance on a hybrid part and
	// CoresCCD0 / CoresCCD1 on a chiplet one. Empty means not yet chosen — the
	// first run on a machine with a split writes that machine's own default, so
	// the file always names an actual set. It reaches the process rather than the
	// toolkit — everything the client does moves with it, the call's audio
	// included — and it is inert on a machine whose cores are all alike.
	Cores string `json:"cores"`
}

// Voice is the call: which devices it uses, what it does to the microphone on
// the way out, and what state it joins in.
//
// Every field here is read at its use site rather than captured. A device name
// is read once when the device is opened; the gate reads Sensitivity from
// config.Current per frame, which is an atomic snapshot rather than a lock.
type Voice struct {
	/* Devices */

	// An empty identifier is the system default, which is also what a device that
	// has since been unplugged falls back to.
	InputDevice  string `json:"input_device"`
	OutputDevice string `json:"output_device"`

	/* Capture */

	Mode string `json:"mode"` // VoiceModeActivity or VoiceModePush

	// Sensitivity is where the gate opens, 0-100: 0 passes almost anything, 100
	// takes a raised voice.
	Sensitivity int `json:"sensitivity"`

	// HighPass runs the ~90 Hz filter in front of the rest of the chain: what is
	// below speech, cut before it can hold the gate open. The gate runs either
	// way — it is what Sensitivity means.
	HighPass bool `json:"high_pass"`

	// NoiseSuppression runs RNNoise between that filter and the gate, which is
	// what removes noise *inside* the voice range while somebody is talking —
	// hiss, fans, keyboard — where the gate can only silence the gaps between
	// words.
	NoiseSuppression bool `json:"noise_suppression"`

	// InputGainDB amplifies the microphone, VoiceGainOffDB to VoiceGainMaxDB. It
	// runs in front of the gate rather than after it, so raising it on a quiet
	// microphone is also what lets the gate hear one.
	InputGainDB int `json:"input_gain_db"`

	// PushToTalkKey is the key held to speak in VoiceModePush.
	PushToTalkKey string `json:"push_to_talk_key"`

	/* Playback */

	// OutputGainDB scales every remote participant, VoiceGainOffDB to
	// VoiceGainMaxDB. Notification sounds have their own and are deliberately not
	// scaled by it.
	OutputGainDB int `json:"output_gain_db"`

	// UserGainsDB is how loud one participant is heard, keyed by user ID, in the
	// same decibels and on top of OutputGainDB. Unity is not stored — SetUserGain
	// drops it — so the map is only ever as long as the list of people actually
	// moved, and somebody set back to normal leaves no record behind.
	UserGainsDB map[string]int `json:"user_gains_db"`

	// DeepPLC reconstructs a lost packet with libopus's neural model instead of
	// extrapolating the last pitch period. It costs nothing on a clean stream —
	// the work happens only while something is actually being concealed — so it is
	// on by default, and it is a setting because it is the reader's machine.
	DeepPLC bool `json:"deep_plc"`

	/* Both directions */

	// SoftClip rounds a peak that overshoots the ceiling instead of slicing it
	// flat, on the microphone and on the call's playback alike. Without it the top
	// of the gain range is distortion rather than loudness, which is the whole
	// reason the range can go as high as it does.
	SoftClip bool `json:"soft_clip"`

	/* On joining */

	JoinMuted    bool `json:"join_muted"`
	JoinDeafened bool `json:"join_deafened"`
}

// What a voice gain may be set to, in decibels. Decibels rather than a
// percentage because a percentage is linear on amplitude — half of one is -6 dB,
// so the whole of the useful boost crowds into the top of the scale — and
// because a threshold, a meter and a gain that share one unit cannot disagree
// about what a number means.
//
// VoiceGainOffDB is the bottom of the range and means silence: no decibel figure
// does, so the end of the scale has to.
const (
	VoiceGainOffDB = -40
	VoiceGainMaxDB = 20
)

/* Enumerated values */

// Density presets. Each names a bundle of size overrides the Interface section
// writes; DensityCustom is what the client reports once those sizes have been
// edited individually and no longer match any bundle.
const (
	DensityCosy    = "cosy"
	DensityCompact = "compact"
	DensityTiny    = "tiny"
	DensityCustom  = "custom"
)

// Clock formats.
const (
	TimeFormat12 = "12h"
	TimeFormat24 = "24h"
)

// How the microphone decides it is being spoken into. Activity is the gate
// deciding; Push is a key being held, and the gate then only shapes what it
// passes.
const (
	VoiceModeActivity = "activity"
	VoiceModePush     = "push"
)

// Which cores the client runs on. The first two name Intel's hybrid split; the
// last two name AMD's chiplets by the machine's own numbering. There is
// deliberately no "automatic" among them: the default is resolved against the
// machine once and written (app.resolveCores) — CoresEfficiency on a hybrid
// part, where the point is spending less power on a client that is idle most
// of the time, and CoresCCD1 on a chiplet one, CCD0 being the chiplet that
// preferred-core scheduling and a game's cache steering usually favour — so
// what runs is always exactly what the file says.
const (
	CoresAll         = "all"
	CoresEfficiency  = "efficiency"
	CoresPerformance = "performance"
	CoresCCD0        = "ccd0"
	CoresCCD1        = "ccd1"
)

/* Defaults */

// Default returns the settings a fresh install runs with. Every value here is
// what the corresponding constant was before it became configurable, so a client
// with no settings file behaves exactly as it did.
func Default() Settings {
	return Settings{
		Interface: Interface{
			Density:           DensityCosy,
			FontSize:          14,
			TimeFormat:        TimeFormat12,
			GroupMessages:     true,
			ShowMemberSidebar: true,
			ThemeTitleBar:     true,
		},
		Behaviour: Behaviour{
			SortMembers:        true,
			GroupByPresence:    true,
			HoistRoles:         true,
			FetchAllMembers:    true,
			LiveMemberPresence: true,
			ShowSelfFirst:      true,
			MemberListFallback: true,
			MemberOverscan:     6,

			SendTyping:         true,
			TypingNames:        3,
			TypingAnimation:    true,
			GroupWindowSeconds: 420,
			InitialMountCount:  50,
			MountedCap:         250,
			HistoryPageSize:    50,
			MessageOverscan:    8,
			AuthorFetchDelayMS: 50,
			AckDelayMS:         1000,
			RefreshDelayMS:     250,
			ScrollSpeed:        4,
			EnterSends:         true,
		},
		Notifications: Notifications{
			LifetimeSeconds: 6,
			MaxStacked:      3,
			ShowInfo:        true,
			ShowWarning:     true,
			ShowDanger:      true,
			ModalSeconds:    3,

			FlashTaskbar:   true,
			AlertOnMention: true,
			AlertOnDirect:  true,

			// What is on by default is what the user did not choose to hear: being
			// named, being written to directly, an action of theirs failing, and the
			// connection going. Every sound that fires for somebody else's ordinary
			// message is off — a client that chimes at every message in every server is
			// one whose sounds get turned off wholesale.
			Sounds:            true,
			SoundVolume:       70,
			SoundsWhenFocused: true,
			PlayMention:       true,
			PlayDirect:        true,
			PlayFriend:        true,
			PlayError:         true,
			PlayConnection:    true,

			TypingVolume: 45,
		},
		Voice: Voice{
			// Voice activity rather than push-to-talk: it is what a client with no
			// key captured yet can actually do, and the gate is what the sensitivity
			// slider is for. Both gains at unity — 0 dB — and joining neither muted
			// nor deafened: a reader who wants either can say so, and a client that
			// joins silent with no obvious reason reads as broken.
			Mode:        VoiceModeActivity,
			Sensitivity: 35,
			HighPass:    true,

			// On: what it costs is ~0.5 % of one core while capturing, and the
			// microphone that does not want it — a studio interface in a treated
			// room — is the rare one.
			NoiseSuppression: true,

			InputGainDB:  0,
			OutputGainDB: 0,

			// On: it costs a branch a sample on everything below the knee, and it is
			// what makes a gain past a few decibels sound like a louder voice rather
			// than a broken one.
			SoftClip: true,

			// On: a clean stream decodes at exactly the same price either way, so
			// the only machine that pays is one already losing packets, which is the
			// machine that wants it.
			DeepPLC: true,
		},
		Cache: Cache{
			ImageDiskMiB:       512,
			ImageMemoryMiB:     192,
			MaxImageEdge:       1600,
			ImageLoaders:       8,
			TextPreviews:       100,
			MessagesPerChannel: 500,
			CachedChannels:     5,
		},
		Performance: Performance{
			FrameRate:      120,
			VSync:          true,
			PartialRepaint: true,
		},
	}
}

/* Derived values */

// GroupWindow is the longest gap a message may follow the previous one by and
// still group under it.
func (b Behaviour) GroupWindow() time.Duration {
	return time.Duration(b.GroupWindowSeconds) * time.Second
}

// AuthorFetchDelay is how long author resolution waits for more authors before
// going to the network.
func (b Behaviour) AuthorFetchDelay() time.Duration {
	return time.Duration(b.AuthorFetchDelayMS) * time.Millisecond
}

// AckDelay is how long a read acknowledgement is held back to coalesce with the
// ones behind it.
func (b Behaviour) AckDelay() time.Duration {
	return time.Duration(b.AckDelayMS) * time.Millisecond
}

// RefreshDelay is how long a queued sidebar rebuild waits for more changes. A
// busy server reorders its member list on every presence change, so this is the
// difference between one rebuild and hundreds.
func (b Behaviour) RefreshDelay() time.Duration {
	return time.Duration(b.RefreshDelayMS) * time.Millisecond
}

// Lifetime is how long a notice stays on the layer.
func (n Notifications) Lifetime() time.Duration {
	return time.Duration(n.LifetimeSeconds) * time.Second
}

// ModalLifetime is how long the centred notice holds the middle of the window.
func (n Notifications) ModalLifetime() time.Duration {
	return time.Duration(n.ModalSeconds) * time.Second
}

// ImageDiskBytes is the on-disk budget for cached pictures — the whole of it,
// which the picture caches divide between them.
func (c Cache) ImageDiskBytes() int64 {
	return int64(c.ImageDiskMiB) * 1024 * 1024
}

// ImageMemoryBytes is the budget for decoded pictures held in memory, divided
// the same way.
func (c Cache) ImageMemoryBytes() int64 {
	return int64(c.ImageMemoryMiB) * 1024 * 1024
}

/* The current settings */

var (
	current  atomic.Pointer[Settings]
	updateMu sync.Mutex // guards the read-modify-write in Update

	saveMu    sync.Mutex // guards saveTimer
	saveTimer *time.Timer

	writeMu sync.Mutex // serialises the settings file write
)

func init() {
	defaults := Default()
	current.Store(&defaults)
}

// Current is the settings as they stand. The value is shared and must be treated
// as read-only; Update is the only way to change anything. Safe from any
// goroutine.
func Current() *Settings {
	return current.Load()
}

// Update applies mutate to a copy of the current settings, publishes it, and
// schedules a write. The copy is deep enough that a reader holding the previous
// snapshot keeps seeing it unchanged, maps included.
//
// mutate must not call Update: the read-modify-write is serialised, so a nested
// call deadlocks rather than clobbering.
func Update(mutate func(*Settings)) {
	updateMu.Lock()

	next := Current().clone()
	mutate(next)
	// On the clone, whose maps were just cloned, so a floor cannot reach a
	// snapshot a reader is still holding.
	next.sanitise()
	current.Store(next)

	updateMu.Unlock()

	scheduleSave()
}

// SetUserGain records how loud one participant is heard at, in decibels. Unity
// is a deletion rather than an entry: it is what everybody is at already, and
// storing it would keep a row for every person ever nudged and put back.
func SetUserGain(userID string, db int) {
	Update(func(s *Settings) {
		if userID == "" || db == 0 {
			delete(s.Voice.UserGainsDB, userID)
			return
		}

		if s.Voice.UserGainsDB == nil {
			s.Voice.UserGainsDB = make(map[string]int)
		}

		s.Voice.UserGainsDB[userID] = clamp(db, VoiceGainOffDB, VoiceGainMaxDB)
	})
}

// Load reads the settings file into the current settings. An absent file is not
// an error — it is what every first run looks like — and a malformed one is
// reported and ignored, since refusing to start over a settings file the user
// can no longer read would be worse than starting with the defaults. It is moved
// aside first, so the defaults are not written over it.
func Load() error {
	path, err := Path()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// Unmarshalling over the defaults rather than over a zero value is what makes
	// a partial file work: a key the file does not mention keeps its default
	// instead of becoming false or zero.
	settings := Default()
	if err := json.Unmarshal(data, &settings); err != nil {
		// The caller starts anyway on the defaults, and the first change written
		// would put them over the only copy of what the user had. Keep it aside:
		// unreadable to the client is not unreadable to the user.
		if renameErr := os.Rename(path, path+".bad"); renameErr != nil {
			log.Printf("keep unreadable settings aside: %v", renameErr)
		}

		return err
	}

	settings.sanitise()
	current.Store(settings.clone())

	return nil
}

// sanitise floors the values that must not be zero. The file is meant to be
// hand-editable, so a mount cap of nothing or a cache of no channels has to fail
// safe rather than leave the client unable to draw a conversation.
func (s *Settings) sanitise() {
	floor := func(value *int, lowest int) {
		*value = max(*value, lowest)
	}

	floor(&s.Behaviour.GroupWindowSeconds, 0)
	floor(&s.Behaviour.InitialMountCount, 1)
	floor(&s.Behaviour.HistoryPageSize, 1)
	floor(&s.Behaviour.MountedCap, s.Behaviour.InitialMountCount)
	floor(&s.Behaviour.MessageOverscan, 0)
	floor(&s.Behaviour.AuthorFetchDelayMS, 0)
	floor(&s.Behaviour.AckDelayMS, 0)
	floor(&s.Behaviour.RefreshDelayMS, 0)
	floor(&s.Behaviour.ScrollSpeed, 1)
	floor(&s.Behaviour.MemberOverscan, 0)

	floor(&s.Notifications.LifetimeSeconds, 1)
	floor(&s.Notifications.MaxStacked, 1)
	floor(&s.Notifications.ModalSeconds, 1)

	// A volume is a percentage the file may name anything at, and a negative one
	// would be silence reported as a number.
	s.Notifications.SoundVolume = clamp(s.Notifications.SoundVolume, 0, 100)
	s.Notifications.TypingVolume = clamp(s.Notifications.TypingVolume, 0, 100)

	// A gain the file may name anything at. The ceiling is the one the mixer
	// enforces anyway; clamping here is what keeps the slider and the file
	// agreeing about where the range ends.
	s.Voice.Sensitivity = clamp(s.Voice.Sensitivity, 0, 100)
	s.Voice.InputGainDB = clamp(s.Voice.InputGainDB, VoiceGainOffDB, VoiceGainMaxDB)
	s.Voice.OutputGainDB = clamp(s.Voice.OutputGainDB, VoiceGainOffDB, VoiceGainMaxDB)

	for id, db := range s.Voice.UserGainsDB {
		if db == 0 {
			delete(s.Voice.UserGainsDB, id)
			continue
		}

		s.Voice.UserGainsDB[id] = clamp(db, VoiceGainOffDB, VoiceGainMaxDB)
	}

	floor(&s.Cache.ImageDiskMiB, 1)
	floor(&s.Cache.ImageMemoryMiB, 1)
	floor(&s.Cache.MaxImageEdge, 64)
	floor(&s.Cache.ImageLoaders, 1)
	floor(&s.Cache.TextPreviews, 1)
	floor(&s.Cache.MessagesPerChannel, 1)
	floor(&s.Cache.CachedChannels, 1)

	// A frame rate of zero is a client that never wakes: the toolkit clamps it to
	// 1, which is indistinguishable from one that has hung.
	s.Performance.FrameRate = clamp(s.Performance.FrameRate, 15, 1000)

	if s.Interface.FontSize < 1 {
		s.Interface.FontSize = Default().Interface.FontSize
	}
}

// clamp holds a value inside a range, for the settings whose floor is not zero.
func clamp(value, lowest, highest int) int {
	return min(max(value, lowest), highest)
}

// Save writes the current settings immediately, cancelling any pending delayed
// write.
func Save() error {
	saveMu.Lock()
	if saveTimer != nil {
		saveTimer.Stop()
		saveTimer = nil
	}
	saveMu.Unlock()

	path, err := Path()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(Current(), "", "  ")
	if err != nil {
		return err
	}

	return writeFile(path, data)
}

// writeFile puts data at path through a temp file renamed into place, so a crash
// part-way through leaves the previous settings rather than a truncated file that
// Load would report and ignore — which would be the whole tree gone. The Sync is
// what carries that past a lost machine and not only a lost process; a settings
// write is one debounced file every second at worst, so it is affordable here.
func writeFile(path string, data []byte) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	tmp := path + ".tmp"

	// Not os.Create: its 0o666 would widen the mode this file is written at.
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}

	if err != nil {
		_ = os.Remove(tmp)
	}

	return err
}

// Path returns the settings file's location.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, settingsFile), nil
}

// scheduleSave defers a write by saveDelay, restarting the wait each time it is
// called so a run of changes writes once.
func scheduleSave() {
	saveMu.Lock()
	defer saveMu.Unlock()

	if saveTimer != nil {
		saveTimer.Stop()
	}

	saveTimer = time.AfterFunc(saveDelay, func() {
		if err := Save(); err != nil {
			log.Printf("save settings: %v", err)
		}
	})
}

// clone copies the settings, including the override maps, so the published value
// shares nothing mutable with the one it replaced. maps.Clone keeps a nil map
// nil, which is what an untouched Styles section is and what omitempty writes out.
func (s *Settings) clone() *Settings {
	next := *s
	next.Styles.Sizes = maps.Clone(s.Styles.Sizes)
	next.Styles.Colors = maps.Clone(s.Styles.Colors)
	next.Notifications.SoundFiles = maps.Clone(s.Notifications.SoundFiles)
	next.Voice.UserGainsDB = maps.Clone(s.Voice.UserGainsDB)

	return &next
}
