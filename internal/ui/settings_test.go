package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/ui/theme"
)

// TestStyleFieldsCoverTheTable is the test that earns its place here: the
// settings page is only "everything is configurable" if the curated groups and
// the generated Advanced list add up to the whole size table, with nothing named
// twice. It fails the day a size is added that no section can reach, and the day
// one is renamed out from under a curated group.
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
