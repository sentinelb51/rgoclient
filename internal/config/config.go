// Package config is the client's persisted settings: what the user has changed
// away from the defaults, and the accessor every other package reads it through.
//
// It sits at the bottom of the dependency graph with domain and util, importing
// nothing internal, because everything above it needs to read a setting —
// internal/cache sizes its budgets from here, internal/client decides whether to
// sort members, internal/ui builds widgets from it and internal/app writes it.
//
// The current settings are held behind an atomic pointer and handed out as a
// snapshot. Update clones, mutates and republishes, so a reader never observes a
// half-written value and callers off the UI thread — Store.Members is one — need
// no lock.
package config

import (
	"encoding/json"
	"log"
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
	Cache         Cache         `json:"cache"`
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
	/* Members */

	SortMembers        bool `json:"sort_members"`
	GroupByPresence    bool `json:"group_by_presence"`
	HideOfflineMembers bool `json:"hide_offline_members"`

	/* Typing indicators */

	SendTyping       bool `json:"send_typing"`
	ShowTyping       bool `json:"show_typing"`
	TypingInChannels bool `json:"typing_in_channels"`

	/* Messages */

	GroupWindowSeconds int `json:"group_window_seconds"`
	InitialMountCount  int `json:"initial_mount_count"`
	MountedCap         int `json:"mounted_cap"`
	HistoryPageSize    int `json:"history_page_size"`

	/* Timing */

	AuthorFetchDelayMS int `json:"author_fetch_delay_ms"`
	AckDelayMS         int `json:"ack_delay_ms"`

	/* Input */

	ScrollSpeed int  `json:"scroll_speed"`
	EnterSends  bool `json:"enter_sends"`
}

// Notifications configures the transient notice layer.
type Notifications struct {
	LifetimeSeconds int `json:"lifetime_seconds"`
	MaxStacked      int `json:"max_stacked"`

	ShowInfo    bool `json:"show_info"`
	ShowWarning bool `json:"show_warning"`
	ShowDanger  bool `json:"show_danger"`
}

// Cache configures the on-disk and in-memory caches. ImageDir, TextPreviews,
// MessagesPerChannel and CachedChannels are read once while the caches are being
// built, so changing them takes a restart; the image budgets are held on the
// cache itself and apply as soon as they are set.
type Cache struct {
	ImageDir string `json:"image_dir"` // "" for the user's cache directory

	ImageDiskMiB   int `json:"image_disk_mib"`
	ImageMemoryMiB int `json:"image_memory_mib"`
	MaxImageEdge   int `json:"max_image_edge"`

	TextPreviews       int `json:"text_previews"`
	MessagesPerChannel int `json:"messages_per_channel"`
	CachedChannels     int `json:"cached_channels"`
}

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
			SendTyping:         true,
			ShowTyping:         true,
			GroupWindowSeconds: 420,
			InitialMountCount:  50,
			MountedCap:         250,
			HistoryPageSize:    50,
			AuthorFetchDelayMS: 50,
			AckDelayMS:         1000,
			ScrollSpeed:        4,
			EnterSends:         true,
		},
		Notifications: Notifications{
			LifetimeSeconds: 6,
			MaxStacked:      3,
			ShowInfo:        true,
			ShowWarning:     true,
			ShowDanger:      true,
		},
		Cache: Cache{
			ImageDiskMiB:       512,
			ImageMemoryMiB:     192,
			MaxImageEdge:       1600,
			TextPreviews:       100,
			MessagesPerChannel: 500,
			CachedChannels:     5,
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

// Lifetime is how long a notice stays on the layer.
func (n Notifications) Lifetime() time.Duration {
	return time.Duration(n.LifetimeSeconds) * time.Second
}

// ImageDiskBytes is the on-disk budget for cached images.
func (c Cache) ImageDiskBytes() int64 {
	return int64(c.ImageDiskMiB) * 1024 * 1024
}

// ImageMemoryBytes is the budget for decoded images held in memory.
func (c Cache) ImageMemoryBytes() int64 {
	return int64(c.ImageMemoryMiB) * 1024 * 1024
}

/* The current settings */

var (
	current atomic.Pointer[Settings]

	saveMu    sync.Mutex // guards saveTimer
	saveTimer *time.Timer
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
func Update(mutate func(*Settings)) {
	next := Current().clone()
	mutate(next)
	current.Store(next)

	scheduleSave()
}

// Load reads the settings file into the current settings. An absent file is not
// an error — it is what every first run looks like — and a malformed one is
// reported and ignored, since refusing to start over a settings file the user
// can no longer read would be worse than starting with the defaults.
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
	floor := func(value *int, min int) {
		if *value < min {
			*value = min
		}
	}

	floor(&s.Behaviour.GroupWindowSeconds, 0)
	floor(&s.Behaviour.InitialMountCount, 1)
	floor(&s.Behaviour.HistoryPageSize, 1)
	floor(&s.Behaviour.MountedCap, s.Behaviour.InitialMountCount)
	floor(&s.Behaviour.AuthorFetchDelayMS, 0)
	floor(&s.Behaviour.AckDelayMS, 0)
	floor(&s.Behaviour.ScrollSpeed, 1)

	floor(&s.Notifications.LifetimeSeconds, 1)
	floor(&s.Notifications.MaxStacked, 1)

	floor(&s.Cache.ImageDiskMiB, 1)
	floor(&s.Cache.ImageMemoryMiB, 1)
	floor(&s.Cache.MaxImageEdge, 64)
	floor(&s.Cache.TextPreviews, 1)
	floor(&s.Cache.MessagesPerChannel, 1)
	floor(&s.Cache.CachedChannels, 1)

	if s.Interface.FontSize < 1 {
		s.Interface.FontSize = Default().Interface.FontSize
	}
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

	return os.WriteFile(path, data, 0o600)
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
// shares nothing mutable with the one it replaced.
func (s *Settings) clone() *Settings {
	next := *s
	next.Styles.Sizes = cloneMap(s.Styles.Sizes)
	next.Styles.Colors = cloneMap(s.Styles.Colors)

	return &next
}

func cloneMap[V any](in map[string]V) map[string]V {
	if in == nil {
		return nil
	}

	out := make(map[string]V, len(in))
	for key, value := range in {
		out[key] = value
	}

	return out
}
