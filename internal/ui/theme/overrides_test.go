package theme

import (
	"image/color"
	"testing"
)

// TestApplyRestoresDefaults is the one that would rot silently: Apply resets
// before it writes, so clearing an override has to leave the table exactly as it
// was compiled. Both tables are compared whole, so a field Apply forgets to
// reset is caught whichever one it is in.
func TestApplyRestoresDefaults(t *testing.T) {
	t.Cleanup(func() { Apply(nil, nil) })

	sizes, colors := Sizes, Colors

	Apply(map[string]string{"TextPrimary": "#FF0000"}, map[string]float32{"MessageAvatarSize": 12})
	if Sizes.MessageAvatarSize != 12 {
		t.Fatalf("MessageAvatarSize = %v, want the override", Sizes.MessageAvatarSize)
	}

	Apply(nil, nil)
	if Sizes != sizes {
		t.Error("the size table did not come back to its defaults")
	}
	if Colors != colors {
		t.Error("the palette did not come back to its defaults")
	}
}

// TestApplyIgnoresUnknown covers a hand-edited file naming something the client
// no longer has. It must not stop the client, and must not cost its neighbours.
func TestApplyIgnoresUnknown(t *testing.T) {
	t.Cleanup(func() { Apply(nil, nil) })

	Apply(
		map[string]string{"NoSuchColour": "#FF0000", "TextPrimary": "not a colour"},
		map[string]float32{"NoSuchSize": 12, "MessageAvatarSize": 30},
	)

	if Sizes.MessageAvatarSize != 30 {
		t.Errorf("MessageAvatarSize = %v, want the valid override alongside the bad one", Sizes.MessageAvatarSize)
	}
	if Colors.TextPrimary != defaultColors.TextPrimary {
		t.Error("an unparseable colour was written rather than skipped")
	}
}

// TestSelectionFollowsAccent covers the one palette entry that is derived rather
// than named: text selection is the accent, so changing the accent has to move
// it too.
func TestSelectionFollowsAccent(t *testing.T) {
	t.Cleanup(func() { Apply(nil, nil) })

	Apply(map[string]string{"ServerSelectedBg": "#FF0000"}, nil)

	tint, ok := selectionTint.(color.RGBA)
	if !ok {
		t.Fatalf("selection tint is %T, want a colour the alpha could be set on", selectionTint)
	}
	if tint.R != 0xFF || tint.G != 0 || tint.B != 0 {
		t.Errorf("selection tint = %v, want the new accent", tint)
	}
	if tint.A != selectionAlpha {
		t.Errorf("selection alpha = %d, want %d — glyphs under it must stay legible", tint.A, selectionAlpha)
	}
}

// TestHexRoundTrip covers the translucent entries. Reading a colour through the
// interface gives alpha-premultiplied channels, which would come back darker
// than they were written every time the file was rewritten.
func TestHexRoundTrip(t *testing.T) {
	cases := []color.RGBA{
		{R: 91, G: 124, B: 250, A: 255},
		{R: 91, G: 124, B: 250, A: 70},
		{A: 90},
	}

	for _, want := range cases {
		parsed, ok := ParseHex(Hex(want))
		if !ok {
			t.Fatalf("ParseHex(%q) failed", Hex(want))
		}
		if parsed != color.Color(want) {
			t.Errorf("%v round-tripped to %v through %q", want, parsed, Hex(want))
		}
	}
}
