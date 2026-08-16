package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// TestStyleFieldsCoverTheTable is the test that earns its place here: the
// settings page is only "everything is configurable" if the curated groups and
// the generated Advanced list add up to the whole size table, with nothing named
// twice. It fails the day a size is added that no section can reach, and the day
// one is renamed out from under a curated group.
//
// Reachable means reachable with advanced mode on — the tables and several of the
// curated groups are what that mode reveals. This walks the definitions, not the
// rendered rows, so it says nothing about which of them are drawn.
func TestStyleFieldsCoverTheTable(t *testing.T) {
	known := make(map[string]bool)
	for _, name := range theme.SizeFields() {
		known[name] = true
	}

	seen := make(map[string]bool)
	for _, group := range styleGroups {
		for _, field := range group.fields {
			if !known[field.name] {
				t.Errorf("%q is in the %q group but not in the size table", field.name, group.caption)
			}
			if seen[field.name] {
				t.Errorf("%q appears in more than one group", field.name)
			}
			seen[field.name] = true
		}
	}

	for _, name := range uncuratedSizeFields() {
		if seen[name] {
			t.Errorf("%q is both curated and listed under Advanced", name)
		}
		seen[name] = true
	}

	for name := range known {
		if !seen[name] {
			t.Errorf("%q cannot be reached from any section", name)
		}
	}
}

// TestBasicModeShowsWholeGroups covers what advanced mode can quietly break.
// Hiding a row is one line, and hiding the last row of a group leaves a captioned
// card with nothing in it — which the rail then offers as somewhere to go. The
// caption is load-bearing for the same reason: it is what a sub-entry is named
// after. Neither is visible until somebody opens the section that broke.
func TestBasicModeShowsWholeGroups(t *testing.T) {
	test.NewTempApp(t)

	counts := make(map[bool]map[SettingsSection]int)

	for _, advanced := range []bool{false, true} {
		counts[advanced] = make(map[SettingsSection]int)

		page := newTestSettingsPage()
		page.advanced = advanced

		for _, entry := range visibleRailEntries(advanced) {
			page.showSection(entry.section)

			if len(page.groups) == 0 {
				t.Errorf("advanced=%v: the %q section is empty", advanced, entry.title)
			}

			for _, group := range page.groups {
				if group.object == nil {
					t.Errorf("advanced=%v: %q kept a group that built nothing", advanced, entry.title)
				}
				if group.caption == "" && group.object == nil {
					t.Errorf("advanced=%v: %q has a card the rail cannot name", advanced, entry.title)
				}
			}

			counts[advanced][entry.section] = len(page.groups)
		}
	}

	// The mode has to actually withhold something, or the pass above is vacuous.
	for _, section := range []SettingsSection{SectionBehaviour, SectionCache, SectionStyles} {
		if counts[false][section] >= counts[true][section] {
			t.Errorf("%q shows %d groups in basic mode and %d in advanced — the gate is doing nothing",
				railTitle(section), counts[false][section], counts[true][section])
		}
	}
}

// TestAdvancedSectionFallsBack covers the other half: the raw tables are listed
// only in advanced mode, and the mode can be turned off while they are open —
// from the rail's own switch, or from About's reset. Landing on a section the
// rail does not list would leave the page showing nothing it could navigate back
// from.
func TestAdvancedSectionFallsBack(t *testing.T) {
	test.NewTempApp(t)

	page := newTestSettingsPage()
	page.showSection(SectionAdvanced)

	if page.section == SectionAdvanced {
		t.Fatal("Advanced opened with advanced mode off")
	}

	listed := false
	for _, entry := range visibleRailEntries(false) {
		if entry.section == page.section {
			listed = true
		}
	}
	if !listed {
		t.Errorf("fell back to a section the rail does not list")
	}
}

// newTestSettingsPage is a page whose hooks answer without a controller. Every
// one is filled in: SettingsHooks promises that in the app, and a section is
// entitled to call any of them while it builds.
func newTestSettingsPage() *SettingsPage {
	page := NewSettingsPage(SettingsHooks{
		Deps:             testDeps(),
		Update:           func(func(*config.Settings)) {},
		Restyle:          func() {},
		Close:            func() {},
		Confirm:          func(Confirm) {},
		Sessions:         func() []SettingsSession { return nil },
		ForgetSession:    func(string) {},
		LogOut:           func() {},
		LogOutEverywhere: func() {},
		SetPresence:      func(domain.Presence) {},
		SetStatusText:    func(string) {},
		SetDisplayName:   func(string) {},
		ChangeUsername:   func() {},
		ChangeAvatar:     func() {},
		ChangeBanner:     func() {},
		RemoveAvatar:     func() {},
		RemoveBanner:     func() {},
		SetBio:           func(string) {},
		LoadProfile:      func(func(domain.UserProfile)) {},

		Sounds: func() []SettingsSound {
			return []SettingsSound{
				{Key: "mention", Title: "Mention", Summary: "Somebody named you."},
				{Key: "key", Title: "Key", Summary: "An ordinary character.", Typing: true},
			}
		},
		ChooseSound: func(string, func()) {},
		ResetSound:  func(string) {},
		PlaySound:   func(string) {},

		CacheDir:       func() string { return "" },
		ChooseCacheDir: func(func(string)) {},
		CacheStats:     func(func(cache.ImageStats)) {},
		ClearCache:     func() {},
		ConfigPath:     func() string { return "" },
		OpenPath:       func(string) {},
	})

	// The rail and pane are built by build(), which mounts widgets a section test
	// has no window for.
	page.rail = container.NewVBox()
	page.pane = VBoxNoSpacing()
	page.title = canvas.NewText("", theme.Colors.TextPrimary)

	return page
}

// TestDensityBundlesNameRealSizes covers the presets by the same rule: a bundle
// naming a size that no longer exists would silently stop applying.
func TestDensityBundlesNameRealSizes(t *testing.T) {
	for density, bundle := range densityBundles {
		for name := range bundle {
			if _, ok := theme.DefaultSize(name); !ok {
				t.Errorf("the %q preset names %q, which is not in the size table", density, name)
			}
		}
	}
}

// TestNumberBoxEdits covers the way a value is set exactly rather than dragged:
// clicking the number has to actually put the cursor in a field — the focus
// manager only finds widgets already in the visible tree, and the field is built
// on the click — and what is typed has to be read the same way whatever it says.
func TestNumberBoxEdits(t *testing.T) {
	test.NewTempApp(t)

	var committed []float64
	box := newNumberBox(50, 0, 100, "px", func(v float64) { committed = append(committed, v) })

	win := test.NewWindow(box)
	t.Cleanup(win.Close)

	box.Tapped(&fyne.PointEvent{})
	if box.entry == nil {
		t.Fatal("clicking the value did not open a field")
	}
	if win.Canvas().Focused() != box.entry {
		t.Error("the field opened without the cursor in it")
	}

	cases := []struct {
		typed string
		want  float64
		note  string
	}{
		{"75", 75, "a value in range"},
		{"500", 100, "a value past the top of the range"},
		{"-9", 0, "a value below the bottom"},
	}

	for _, c := range cases {
		box.Tapped(&fyne.PointEvent{})
		box.entry.SetText(c.typed)
		box.commit(box.entry)

		if box.value != c.want {
			t.Errorf("%s: typing %q left the box at %v, want %v", c.note, c.typed, box.value, c.want)
		}
	}

	// Half-finished typing is not a request for zero.
	before := box.value
	box.Tapped(&fyne.PointEvent{})
	box.entry.SetText("")
	box.commit(box.entry)

	if box.value != before {
		t.Errorf("an unparseable field committed %v, want the previous %v", box.value, before)
	}
	if len(committed) != len(cases) {
		t.Errorf("%d values were reported, want %d", len(committed), len(cases))
	}
}

// TestSliderQuantises covers the arithmetic behind a drag: the pointer lands
// somewhere along the widget, and what comes out has to be a step of the range
// and never outside it.
func TestSliderQuantises(t *testing.T) {
	test.NewTempApp(t)

	var last float64
	slider := NewSlider(0, 100, 5, 0, func(v float64) { last = v })
	slider.Resize(fyne.NewSize(theme.Sizes.SettingsSliderKnob+100, theme.Sizes.SettingsSliderHeight))

	// The travel is the width less the knob, which the resize above made exactly
	// 100 wide, so a position is a percentage.
	slider.Tapped(&fyne.PointEvent{Position: fyne.NewPos(theme.Sizes.SettingsSliderKnob/2+43, 0)})
	if last != 45 {
		t.Errorf("a tap at 43%% gave %v, want the nearest step of 45", last)
	}

	slider.Tapped(&fyne.PointEvent{Position: fyne.NewPos(-50, 0)})
	if last != 0 || slider.Value() != 0 {
		t.Errorf("a tap left of the track gave %v, want the low bound", slider.Value())
	}

	slider.Tapped(&fyne.PointEvent{Position: fyne.NewPos(9999, 0)})
	if slider.Value() != 100 {
		t.Errorf("a tap past the track gave %v, want the high bound", slider.Value())
	}
}

// TestAccentOverridesNameRealColours covers the accent's expansion the same way.
func TestAccentOverridesNameRealColours(t *testing.T) {
	overrides := theme.AccentOverrides("#5B7CFA")
	if len(overrides) == 0 {
		t.Fatal("an accent expanded to nothing")
	}

	for name := range overrides {
		if _, ok := theme.DefaultColor(name); !ok {
			t.Errorf("the accent writes %q, which is not in the palette", name)
		}
	}
}

// TestCommitEntryReportsOnlyRealChanges covers the two rules a status field
// rests on: a report is a request, so an untouched field must make none however
// often the focus crosses it, and Escape must be swallowed — the settings page
// reads the key the canvas gets as "close".
func TestCommitEntryReportsOnlyRealChanges(t *testing.T) {
	var reported []string
	entry := newCommitEntry("here", func(text string) { reported = append(reported, text) })

	entry.FocusLost()
	if len(reported) != 0 {
		t.Errorf("an untouched field reported %v", reported)
	}

	entry.SetText("away")
	entry.FocusLost()
	entry.FocusLost()

	if len(reported) != 1 || reported[0] != "away" {
		t.Errorf("reported %v, want one report of the new value", reported)
	}

	entry.SetText("half-typed")
	entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})

	if entry.Text != "away" {
		t.Errorf("Escape left %q, want the committed value back", entry.Text)
	}

	entry.FocusLost()
	if len(reported) != 1 {
		t.Errorf("reported %v, want the reverted field to have said nothing more", reported)
	}
}
