package ui

import (
	"testing"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
)

// The regression the plan calls most likely to ship silently: typing in the
// settings search box builds every section twice, and the Voice section owns a
// microphone.
func TestIndexPassOpensNoDevice(t *testing.T) {
	var (
		enumerated int
		opened     int
	)

	hooks := SettingsHooks{
		Deps:    Deps{Store: &fakeStore{}},
		Version: "test",
		Build:   "test",

		LoadProfile: func(func(domain.UserProfile)) { t.Error("index pass fetched the profile") },
		CacheStats:  func(func(cache.ImageStats)) { t.Error("index pass walked the cache") },

		// Everything the walk reaches has to answer; the point of the test is the
		// three below it, which must never be reached at all.
		Sessions:   func() []SettingsSession { return nil },
		Sounds:     func() []SettingsSound { return nil },
		CacheDir:   func() string { return "" },
		ConfigPath: func() string { return "" },

		InputDevices:  func() []AudioDevice { enumerated++; return nil },
		OutputDevices: func() []AudioDevice { enumerated++; return nil },

		StartInputMonitor: func(func(float32)) { opened++ },
		StopInputMonitor:  func() {},
	}

	index := buildSettingsIndex(hooks)

	if opened != 0 {
		t.Fatalf("the index pass opened a capture device %d times", opened)
	}
	if enumerated != 0 {
		t.Fatalf("the index pass enumerated devices %d times", enumerated)
	}

	// It still has to find the section, or the stubbing has hidden it.
	var found int
	for _, hit := range index {
		if hit.section == SectionVoice {
			found++
		}
	}
	if found == 0 {
		t.Fatal("the Voice section is not searchable")
	}
	t.Logf("Voice contributes %d searchable rows and opened nothing", found)
}
