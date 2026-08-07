package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withHome points the settings file at a temporary directory, so a test never
// reads or writes the real one.
func withHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this one on Windows

	defaults := Default()
	current.Store(&defaults)
	t.Cleanup(func() {
		reset := Default()
		current.Store(&reset)
	})

	return filepath.Join(home, settingsFile)
}

// TestLoadAbsent covers the first run: no file at all is what every install
// starts as, and it has to be indistinguishable from a file of defaults.
func TestLoadAbsent(t *testing.T) {
	withHome(t)

	if err := Load(); err != nil {
		t.Fatalf("Load with no file: %v", err)
	}
	if got := Current().Behaviour.MountedCap; got != Default().Behaviour.MountedCap {
		t.Errorf("MountedCap = %d, want the default", got)
	}
}

// TestLoadPartial is the property that makes the file hand-editable: a key it
// does not mention keeps its default rather than becoming the zero value. Every
// boolean that defaults to true depends on this.
func TestLoadPartial(t *testing.T) {
	path := withHome(t)

	if err := os.WriteFile(path, []byte(`{"behaviour":{"sort_members":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	settings := Current()
	if settings.Behaviour.SortMembers {
		t.Error("sort_members was not read")
	}
	if !settings.Behaviour.EnterSends {
		t.Error("enter_sends lost its default because another key was set")
	}
	if got := settings.Cache.ImageDiskMiB; got != Default().Cache.ImageDiskMiB {
		t.Errorf("ImageDiskMiB = %d, want the default", got)
	}
}

// TestLoadSanitises covers a hand-edited file that would leave the client unable
// to draw a conversation.
func TestLoadSanitises(t *testing.T) {
	path := withHome(t)

	body := `{"behaviour":{"mounted_cap":0,"initial_mount_count":0,"history_page_size":0},
	          "cache":{"cached_channels":0}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	settings := Current()
	if settings.Behaviour.InitialMountCount < 1 || settings.Behaviour.HistoryPageSize < 1 {
		t.Error("a zero page size survived")
	}
	if settings.Behaviour.MountedCap < settings.Behaviour.InitialMountCount {
		t.Error("the mounted ceiling is below what a channel switch mounts")
	}
	if settings.Cache.CachedChannels < 1 {
		t.Error("a cache of no channels survived")
	}
}

// TestSaveRoundTrip checks that what is written comes back, and that the style
// overrides carry only what was changed — the file is meant to be readable, and
// a full copy of the tables would go stale the moment one gained an entry.
func TestSaveRoundTrip(t *testing.T) {
	path := withHome(t)

	Update(func(s *Settings) {
		s.Interface.Accent = "#123456"
		s.Behaviour.ScrollSpeed = 9
		s.Styles.Sizes = map[string]float32{"MessageAvatarSize": 24}
	})
	if err := Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var raw struct {
		Styles struct {
			Sizes  map[string]float32 `json:"sizes"`
			Colors map[string]string  `json:"colors"`
		} `json:"styles"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Styles.Sizes) != 1 {
		t.Errorf("%d size overrides were written, want only the one that changed", len(raw.Styles.Sizes))
	}
	if len(raw.Styles.Colors) != 0 {
		t.Errorf("%d colour overrides were written, want none", len(raw.Styles.Colors))
	}

	reset := Default()
	current.Store(&reset)
	if err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	settings := Current()
	if settings.Interface.Accent != "#123456" || settings.Behaviour.ScrollSpeed != 9 {
		t.Errorf("settings did not survive the round trip: %+v", settings.Interface)
	}
	if got := settings.Styles.Sizes["MessageAvatarSize"]; got != 24 {
		t.Errorf("MessageAvatarSize = %v, want 24", got)
	}
}

// TestUpdateSnapshotIsolated is why Current hands back a pointer at all: a reader
// holding one must not see a later Update, maps included.
func TestUpdateSnapshotIsolated(t *testing.T) {
	withHome(t)

	Update(func(s *Settings) { s.Styles.Sizes = map[string]float32{"MessageAvatarSize": 40} })
	before := Current()

	Update(func(s *Settings) { s.Styles.Sizes["MessageAvatarSize"] = 12 })

	if got := before.Styles.Sizes["MessageAvatarSize"]; got != 40 {
		t.Errorf("the earlier snapshot now reads %v; Update wrote through it", got)
	}
}
