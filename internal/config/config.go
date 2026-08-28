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
	"math"
	"os"
	"path/filepath"
	"slices"
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
	Updates       Updates       `json:"updates"`
	State         State         `json:"state"`
}

// State is what the client remembers rather than what the reader has chosen.
// Nothing here is a setting and no row on the settings page writes to it — it is
// persisted because losing it would quietly undo a decision somebody made.
type State struct {
	// DismissedMentions is what the reader has waved off, oldest first, and
	// DismissedAccount is whose they are. Revolt has no route dropping a single
	// mention and hands the whole set back on every Ready, so without this a
	// restart brings back every mention dismissed in a channel never since opened.
	//
	// Bounded by MaxDismissedMentions: opening the channel is what drops one for
	// real, and one in a channel nobody returns to would otherwise be kept forever.
	// The account is stored beside them because another account's mentions are not
	// these — a different one signing in replaces the set rather than inheriting it.
	DismissedAccount  string   `json:"dismissed_account,omitempty"`
	DismissedMentions []string `json:"dismissed_mentions,omitempty"`
}

// MaxDismissedMentions is how many waved-off mentions are carried between runs.
// Past it the oldest goes, which is the one most likely to have been acknowledged
// by opening its channel since.
const MaxDismissedMentions = 500

// RememberDismissedMention records one so the next Ready cannot hand it back.
// account re-keys the set where it differs, another account's mentions not being
// these.
func RememberDismissedMention(account, messageID string) {
	if account == "" || messageID == "" {
		return
	}

	Update(func(s *Settings) {
		if s.State.DismissedAccount != account {
			s.State.DismissedAccount, s.State.DismissedMentions = account, nil
		}
		if slices.Contains(s.State.DismissedMentions, messageID) {
			return
		}

		s.State.DismissedMentions = append(s.State.DismissedMentions, messageID)
		if extra := len(s.State.DismissedMentions) - MaxDismissedMentions; extra > 0 {
			s.State.DismissedMentions = slices.Delete(s.State.DismissedMentions, 0, extra)
		}
	})
}

// DismissedMentions is what was waved off under this account, or nothing where
// the file belongs to another one.
func DismissedMentions(account string) []string {
	state := Current().State
	if account == "" || state.DismissedAccount != account {
		return nil
	}

	return state.DismissedMentions
}

// ForgetDismissedMentions drops the lot, for a sign-out: the next account to use
// this file starts with nobody else's decisions.
func ForgetDismissedMentions() {
	Update(func(s *Settings) {
		s.State.DismissedAccount, s.State.DismissedMentions = "", nil
	})
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

	// A deleted message's row is marked and left standing for a moment before the
	// column takes it out, so the conversation does not jump out from under
	// somebody mid-sentence and a burst of deletions costs one pass rather than
	// one each. Nothing about the *deletion* waits on this — it has already
	// happened, here and everywhere else.
	//
	// DeletedHoldStepMS widens the hold per row already standing and
	// DeletedHoldCapMS is the ceiling on that. The hold is measured from the
	// **first** row of a batch, so the cap bounds how long any one of them stands
	// rather than a steady trickle pushing the whole set out indefinitely.
	//
	// At a DeletedHoldMS of zero a row goes the moment its deletion arrives.
	DeletedHoldMS     int `json:"deleted_hold_ms"`
	DeletedHoldStepMS int `json:"deleted_hold_step_ms"`
	DeletedHoldCapMS  int `json:"deleted_hold_cap_ms"`

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

	// CursorBlink fades the caret of whichever entry has focus. Off is not the
	// toolkit's own default: a blink is a repaint twice a second for as long as a
	// box is focused, and the composer is focused most of the time the client is
	// open.
	CursorBlink bool `json:"cursor_blink"`
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

	// ShowMention and ShowDirect put the message itself on that layer — the sender's
	// face, their name and the line they wrote, tapping through to it. The three
	// switches above name which *outcomes* are worth reporting and do not cover
	// these: a message somebody else sent is not an outcome of anything the reader
	// did. Neither fires for the channel already on screen, which is showing it.
	ShowMention bool `json:"show_mention"`
	ShowDirect  bool `json:"show_direct"`

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

	// TypingProfile is which board the built-in keystrokes are synthesised from,
	// named by one of `internal/audio`'s profile keys. Not validated here — config
	// is a leaf and does not import audio — so the name is resolved where it is
	// used, and one nothing recognises falls back rather than going silent. A file
	// under SoundFiles still outranks it: a sound somebody chose is not a board.
	TypingProfile string `json:"typing_profile"`

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

	// VideoDiskMiB bounds the folder of fetched video originals — its own
	// budget, apart from the pictures', so an afternoon of videos cannot evict
	// every avatar. Videos are only ever on disk; nothing holds one decoded.
	VideoDiskMiB int `json:"video_disk_mib"`

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

	// BackgroundFrameRate stands in for FrameRate while another window has the
	// focus. It may sit far below FrameRate's own floor: input is noticed whatever
	// the rate and regaining focus restores FrameRate at once, so all a low value
	// paces is drawing nobody is watching — at 1, an animation is frozen in all
	// but name.
	BackgroundFrameRate int `json:"background_frame_rate"`

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

// Updates is what the client does about a newer build of itself. Nothing is ever
// downloaded here: a check reads the repository's newest release and the reader
// is handed the link to it.
type Updates struct {
	// Check asks GitHub once per run, after the account's own session has landed.
	// Off, the Updates section still checks when asked to.
	Check bool `json:"check"`

	// Announced is the version the startup modal has already been raised for. A
	// release interrupts once; after that the Updates section is where it is, which
	// is the difference between telling somebody and nagging them.
	Announced string `json:"announced"`
}

// Voice is the call: which devices it uses, what it does to the microphone on
// the way out, and what state it joins in.
//
// Every field here is read at its use site rather than captured. A device name
// is read once when the device is opened; the gate reads SensitivityDB from
// config.Current per frame, which is an atomic snapshot rather than a lock.
type Voice struct {
	/* Devices */

	// An empty identifier is the system default, which is also what a device that
	// has since been unplugged falls back to.
	InputDevice  string `json:"input_device"`
	OutputDevice string `json:"output_device"`

	/* Capture */

	Mode string `json:"mode"` // VoiceModeActivity or VoiceModePush

	// SensitivityDB is where the gate opens, in dBFS: VoiceGateQuietestDB passes
	// almost anything, VoiceGateLoudestDB takes a raised voice. The same unit the
	// meter beside it is drawn in, so the number and the bar say one thing.
	SensitivityDB int `json:"sensitivity_db"`

	// Sensitivity is what that used to be stored as — an arbitrary 0-100 onto the
	// very range above — and is kept for one purpose: reading a file written
	// before it moved to decibels. sanitise converts it and clears it, and
	// omitempty is what stops a nil one being written back, so the key leaves the
	// file the first time the client saves. Nothing else may read it.
	Sensitivity *int `json:"sensitivity,omitempty"`

	// HighPass runs the ~90 Hz filter in front of the rest of the chain: what is
	// below speech, cut before it can hold the gate open. The gate runs either
	// way — it is what SensitivityDB means.
	HighPass bool `json:"high_pass"`

	// NoiseSuppression runs RNNoise between that filter and the gate, which is
	// what removes noise *inside* the voice range while somebody is talking —
	// hiss, fans, keyboard — where the gate can only silence the gaps between
	// words.
	NoiseSuppression bool `json:"noise_suppression"`

	// NoiseSuppressionDB is how much of it, 0 to VoiceSuppressionMaxDB: the most
	// the suppressor may take out of the signal. The top of the range is the
	// model uncapped; lower keeps some of the room, for a voice full suppression
	// hollows out.
	NoiseSuppressionDB int `json:"noise_suppression_db"`

	// VADThreshold is a second condition on the gate, 0-100: the suppressor's
	// own speech detector must be at least this sure before loudness may open
	// the microphone, which is what keeps a keyboard or a door that clears the
	// sensitivity from opening it. 0 leaves the gate to loudness alone, and it
	// only runs while NoiseSuppression does — the model is what answers.
	VADThreshold int `json:"vad_threshold"`

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

	// SpeakingRing draws a ring round whoever is talking, on their row under the
	// voice channel in the sidebar. It is the one thing here worth being able to
	// turn off: Fyne's dirty flag is one bool for the whole canvas, so every
	// transition is a repaint of the entire window, and a busy call is one per
	// person per sentence. Off, the rows are still there and still say who is in
	// the call — only the ring stops moving.
	SpeakingRing bool `json:"speaking_ring"`

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

	// Node names the media server to join calls through. Empty is the default and
	// means measure it — every node the instance offers is dialled and the first
	// handshake wins, the coordinates each carries needing the reader's own
	// position to be worth anything. A name the instance no longer offers falls
	// back to that same measurement rather than failing the join.
	Node string `json:"node"`
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

// VoiceSuppressionMaxDB is the top of the noise suppression strength range and
// means uncapped — RNNoise measures −33 to −36 dB on the noises it is for, so
// a cap at the ceiling never binds and the top of the dial is the stock model.
const VoiceSuppressionMaxDB = 40

// Where the gate's threshold may be set, in dBFS. They bound a slider rather
// than describe a room — a reader whose microphone is quiet turns the input
// volume up rather than the gate learning the floor, an adaptive one that
// guesses wrong being a gate nobody can reason about.
//
// `audio` clamps to the same two numbers and does not read them from here: it
// is a leaf that has to build in a test with no settings file anywhere, which
// is the same split `maxGain` and VoiceGainMaxDB already have.
const (
	VoiceGateQuietestDB = -70
	VoiceGateLoudestDB  = -20
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

			// Long enough to look up at a row that has just gone red and read it,
			// short enough that a marked message is not mistaken for one still there.
			// The step keeps a run of half a dozen inside the cap, and the cap is what
			// no row stands longer than.
			DeletedHoldMS:     5000,
			DeletedHoldStepMS: 500,
			DeletedHoldCapMS:  8000,

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
			ShowMention:     true,
			ShowDirect:      true,
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

			// Empty rather than a name spelled twice: audio resolves it, and the default
			// board is its to choose.
			TypingProfile: "",
		},
		Voice: Voice{
			// Voice activity rather than push-to-talk: it is what a client with no
			// key captured yet can actually do, and the gate is what the sensitivity
			// slider is for. Both gains at unity — 0 dB — and joining neither muted
			// nor deafened: a reader who wants either can say so, and a client that
			// joins silent with no obvious reason reads as broken.
			// The threshold is where the old arbitrary 0-100 default of 35 always
			// landed on this scale, to the nearest whole decibel: ordinary speech
			// clears it comfortably and a fan in the same room does not.
			Mode:          VoiceModeActivity,
			SensitivityDB: -52,
			HighPass:      true,

			// On: what it costs is ~0.5 % of one core while capturing, and the
			// microphone that does not want it — a studio interface in a treated
			// room — is the rare one. Full strength and no speech veto is the
			// stage exactly as it behaved before either dial existed.
			NoiseSuppression:   true,
			NoiseSuppressionDB: VoiceSuppressionMaxDB,
			VADThreshold:       0,

			InputGainDB:  0,
			OutputGainDB: 0,

			// On: it costs a branch a sample on everything below the knee, and it is
			// what makes a gain past a few decibels sound like a louder voice rather
			// than a broken one.
			SoftClip: true,

			// On: a call is a handful of people, so the repaints are a handful a
			// sentence, and knowing who is talking is most of what the rows are for.
			SpeakingRing: true,

			// On: a clean stream costs about a quarter more per frame (the model is
			// fed either way) and concealment ~13× — 1 % of realtime per concealed
			// stream — which a machine already losing packets is glad to pay.
			// Measured in the retired docs/voice-chat-todo.md §5 (git history).
			DeepPLC: true,
		},
		Cache: Cache{
			ImageDiskMiB:       512,
			ImageMemoryMiB:     192,
			VideoDiskMiB:       1024,
			MaxImageEdge:       1600,
			ImageLoaders:       8,
			TextPreviews:       100,
			MessagesPerChannel: 500,
			CachedChannels:     5,
		},
		Performance: Performance{
			FrameRate:           120,
			BackgroundFrameRate: 10,
			VSync:               true,
			PartialRepaint:      true,
		},
		Updates: Updates{
			// On: one request per run, and a client nobody updates is one running
			// against a backend that has moved on.
			Check: true,
		},
	}
}

/* Derived values */

// GroupWindow is the longest gap a message may follow the previous one by and
// still group under it.
func (b Behaviour) GroupWindow() time.Duration {
	return time.Duration(b.GroupWindowSeconds) * time.Second
}

// DeletedHold is how long a batch of deleted rows stands before the column takes
// it out, standing being how many rows are in it. It widens with each addition
// rather than restarting, so it is measured from the first of them and the cap
// bounds how long any one row stands; zero says to take them out at once.
func (b Behaviour) DeletedHold(standing int) time.Duration {
	if b.DeletedHoldMS <= 0 || standing <= 0 {
		return 0
	}

	ms := b.DeletedHoldMS + b.DeletedHoldStepMS*(standing-1)

	return time.Duration(min(ms, max(b.DeletedHoldCapMS, b.DeletedHoldMS))) * time.Millisecond
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

// VideoDiskBytes is the on-disk budget for fetched video originals.
func (c Cache) VideoDiskBytes() int64 {
	return int64(c.VideoDiskMiB) * 1024 * 1024
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
	floor(&s.Behaviour.DeletedHoldMS, 0)
	floor(&s.Behaviour.DeletedHoldStepMS, 0)
	floor(&s.Behaviour.DeletedHoldCapMS, s.Behaviour.DeletedHoldMS)
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

	// The gate's threshold moved from an arbitrary 0-100 to the decibels it
	// always mapped onto, so a file written before that carries the old key and
	// none of the new one. Converting it here is the whole migration: read once,
	// cleared, and never written back — a reader who had tuned the gate keeps
	// what they tuned rather than silently getting the default.
	if s.Voice.Sensitivity != nil {
		s.Voice.SensitivityDB = gateDBFromLegacy(*s.Voice.Sensitivity)
		s.Voice.Sensitivity = nil
	}

	// A gain the file may name anything at. The ceiling is the one the mixer
	// enforces anyway; clamping here is what keeps the slider and the file
	// agreeing about where the range ends.
	s.Voice.SensitivityDB = clamp(s.Voice.SensitivityDB, VoiceGateQuietestDB, VoiceGateLoudestDB)
	s.Voice.NoiseSuppressionDB = clamp(s.Voice.NoiseSuppressionDB, 0, VoiceSuppressionMaxDB)
	s.Voice.VADThreshold = clamp(s.Voice.VADThreshold, 0, 100)
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
	floor(&s.Cache.VideoDiskMiB, 1)
	floor(&s.Cache.MaxImageEdge, 64)
	floor(&s.Cache.ImageLoaders, 1)
	floor(&s.Cache.TextPreviews, 1)
	floor(&s.Cache.MessagesPerChannel, 1)
	floor(&s.Cache.CachedChannels, 1)

	// A frame rate of zero is a client that never wakes: the toolkit clamps it to
	// 1, which is indistinguishable from one that has hung. The background rate
	// has no such floor — it only ever paces a window nothing is watching.
	s.Performance.FrameRate = clamp(s.Performance.FrameRate, 15, 1000)
	s.Performance.BackgroundFrameRate = clamp(s.Performance.BackgroundFrameRate, 1, 1000)

	if s.Interface.FontSize < 1 {
		s.Interface.FontSize = Default().Interface.FontSize
	}
}

// clamp holds a value inside a range, for the settings whose floor is not zero.
func clamp(value, lowest, highest int) int {
	return min(max(value, lowest), highest)
}

// gateDBFromLegacy is the mapping the 0-100 sensitivity always had, kept for the
// one job of reading a file written before the setting moved to decibels. It is
// deliberately not exported and has no other caller: the arbitrary scale is
// gone, and this is the last thing that knows what a number on it meant.
func gateDBFromLegacy(sensitivity int) int {
	fraction := float64(clamp(sensitivity, 0, 100)) / 100

	return int(math.Round(VoiceGateQuietestDB + (VoiceGateLoudestDB-VoiceGateQuietestDB)*fraction))
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

	return WriteAtomic(path, data)
}

// WriteAtomic puts data at path through a temp file renamed into place, at a
// mode only this account can read, so a crash part-way through leaves the
// previous file rather than a truncated one — which for the settings would be
// the whole tree gone and for the saved logins every login at once. The Sync is
// what carries that past a lost machine and not only a lost process; both
// callers write a small file rarely, so it is affordable.
//
// Exported for the saved-session store, which is the client's other file of
// record and wants exactly these guarantees. Everything else here is settings.
func WriteAtomic(path string, data []byte) error {
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

	// A slice rather than a map, and the one here that is trimmed from the front:
	// slices.Delete shifts in place, which without this would rewrite the elements
	// a reader is still holding the previous snapshot for.
	next.State.DismissedMentions = slices.Clone(s.State.DismissedMentions)

	return &next
}
