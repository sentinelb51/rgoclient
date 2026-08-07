package ui

import (
	"image/color"
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
)

// widthOf measures a string the same way TruncateToWidth does, so the tests can
// express their bounds in real rendered widths rather than guessed pixels.
func widthOf(text string) float32 {
	return fyne.MeasureText(text, 14, fyne.TextStyle{}).Width
}

// walkTree visits every object under root — through containers and through
// widgets' renderers — reporting each with its position relative to root, so a
// test can assert on what a laid-out tree actually draws and where.
func walkTree(root fyne.CanvasObject, visit func(obj fyne.CanvasObject, pos fyne.Position)) {
	var walk func(obj fyne.CanvasObject, origin fyne.Position)
	walk = func(obj fyne.CanvasObject, origin fyne.Position) {
		pos := origin.Add(obj.Position())
		visit(obj, pos)

		switch v := obj.(type) {
		case *fyne.Container:
			for _, child := range v.Objects {
				walk(child, pos)
			}
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(v).Objects() {
				walk(child, pos)
			}
		}
	}

	walk(root, fyne.NewPos(0, 0))
}

// TestTooltipSitsBesideItsAnchor covers the placement a server icon's tooltip
// depends on: the label clears the icon's right edge — so it can overhang the
// column the icon lives in — and is centred on it. The layer is mounted at the
// same origin as the icon's row, so the two positions are directly comparable.
func TestTooltipSitsBesideItsAnchor(t *testing.T) {
	test.NewTempApp(t)

	const side = 40
	anchor := canvas.NewRectangle(color.Transparent)

	tip := NewTooltip()
	win := test.NewWindow(container.NewStack(container.NewWithoutLayout(anchor), tip.Layer))
	t.Cleanup(win.Close)
	win.Resize(fyne.NewSize(300, 200))

	// Placed by hand, so the anchor is a known rectangle rather than whatever a
	// layout would have made of it.
	anchor.Resize(fyne.NewSize(side, side))
	anchor.Move(fyne.NewPos(10, 100))

	if tip.card.Visible() {
		t.Fatal("a fresh tooltip is already showing")
	}

	tip.Show("Server name", anchor)
	if !tip.card.Visible() {
		t.Fatal("Show left the tooltip hidden")
	}

	if got, want := tip.card.Position().X, anchor.Position().X+anchor.Size().Width; got < want {
		t.Errorf("tooltip starts at x=%v, over an anchor ending at %v", got, want)
	}

	centre := tip.card.Position().Y + tip.card.Size().Height/2
	if want := anchor.Position().Y + side/2; centre != want {
		t.Errorf("tooltip centred at y=%v, want %v", centre, want)
	}

	tip.Hide()
	if tip.card.Visible() {
		t.Error("Hide left the tooltip showing")
	}
}

func TestTruncateToWidth(t *testing.T) {
	test.NewTempApp(t)

	const name = "a-rather-long-conversation-name"
	style := fyne.TextStyle{}

	t.Run("fits untouched", func(t *testing.T) {
		if got := TruncateToWidth(name, widthOf(name)+1, 14, style); got != name {
			t.Fatalf("text that fits was altered: %q", got)
		}
	})

	t.Run("shortened text ends in an ellipsis and fits", func(t *testing.T) {
		width := widthOf(name) / 2
		got := TruncateToWidth(name, width, 14, style)

		if !strings.HasSuffix(got, ellipsis) {
			t.Fatalf("shortened text %q lacks the ellipsis", got)
		}
		if !strings.HasPrefix(name, strings.TrimSuffix(got, ellipsis)) {
			t.Fatalf("shortened text %q is not a prefix of %q", got, name)
		}
		if w := widthOf(got); w > width {
			t.Fatalf("shortened text %q measures %v, over the %v budget", got, w, width)
		}
	})

	t.Run("longest prefix that fits", func(t *testing.T) {
		// One rune more than the result must not fit, or the search stopped short.
		width := widthOf(name) / 2
		got := []rune(TruncateToWidth(name, width, 14, style))
		kept := len(got) - len([]rune(ellipsis))

		if next := string([]rune(name)[:kept+1]) + ellipsis; widthOf(next) <= width {
			t.Fatalf("%q also fits in %v, so %q dropped a rune it could have kept", next, width, string(got))
		}
	})

	t.Run("no room at all", func(t *testing.T) {
		for _, width := range []float32{0, -5} {
			if got := TruncateToWidth(name, width, 14, style); got != "" {
				t.Fatalf("width %v yielded %q, want empty", width, got)
			}
		}
	})

	t.Run("empty text", func(t *testing.T) {
		if got := TruncateToWidth("", 100, 14, style); got != "" {
			t.Fatalf("empty text yielded %q", got)
		}
	})
}

// TestGradientNameKeepsItsWidth covers the per-rune split AccentText makes for a
// gradient: the letters are drawn one object at a time, and the run has to
// measure exactly what the same name measures as one — anything else would move
// the timestamp beside it — while still reaching both ends of the gradient.
func TestGradientNameKeepsItsWidth(t *testing.T) {
	test.NewTempApp(t)

	stops := domain.Gradient{
		color.NRGBA{R: 0xD5, G: 0x2D, B: 0x00, A: 255},
		color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		color.NRGBA{R: 0xA3, G: 0x02, B: 0x62, A: 255},
	}
	style := fyne.TextStyle{Bold: true}

	for _, name := range []string{"Amelia", "Wu", "someone with a much longer name"} {
		flat := NewAccentText(name, color.White, 0, style)
		gradient := NewAccentText(name, stops, 0, style)

		if flat.MinSize() != gradient.MinSize() {
			t.Errorf("%q measures %v as a gradient and %v flat", name, gradient.MinSize(), flat.MinSize())
		}

		runes := gradient.content.Objects
		if len(runes) != len([]rune(name)) {
			t.Fatalf("%q drew %d objects, want one per rune", name, len(runes))
		}

		first := runes[0].(*canvas.Text)
		last := runes[len(runes)-1].(*canvas.Text)
		if !sameColor(first.Color, stops[0]) {
			t.Errorf("%q opens in %v, want the first stop %v", name, first.Color, stops[0])
		}
		if !sameColor(last.Color, stops[len(stops)-1]) {
			t.Errorf("%q ends in %v, want the last stop %v", name, last.Color, stops[len(stops)-1])
		}
	}
}

// TestNoGradientReachesATextObject covers the one thing a role colour may not do.
// Fyne caches a rendered glyph run in a map keyed by the text object's own
// fields, colour among them, so a canvas.Text filled with a domain.Gradient — a
// slice, and so not a valid map key — panics the painter on the frame it is first
// drawn, in a goroutine no recover of ours is on. Nothing short of painting it
// notices, and the software painter used by the render tests takes a different
// path, so the invariant is asserted over the tree instead.
func TestNoGradientReachesATextObject(t *testing.T) {
	test.NewTempApp(t)

	stops := domain.Gradient{
		color.NRGBA{R: 0xD5, G: 0x2D, B: 0x00, A: 255},
		color.NRGBA{R: 0xA3, G: 0x02, B: 0x62, A: 255},
	}

	profile := crowdedProfile()
	profile.Accent = stops
	profile.Roles = append(profile.Roles, domain.Role{Name: "Ops", Color: stops})

	dialog := NewProfileDialog(testDeps(), profile, ProfileActions{OnMessage: func() {}, OnClose: func() {}})
	dialog.SetProfile(domain.UserProfile{Bio: "a bio of no particular length"})

	picker := NewMentionPicker(testDeps().Images, func(MentionCandidate) {})
	picker.SetCandidates(MentionUser, []MentionCandidate{NewMentionCandidate("01U", "Amelia", "amelia", "", stops)})
	if !picker.Update(MentionUser, "am") {
		t.Fatal("the picker matched nobody, so no row was ever filled in")
	}

	cases := []struct {
		name string
		root fyne.CanvasObject
	}{
		{"profile dialog", dialog.Content},
		{"role chip", NewRoleChip(domain.Role{Name: "Ops", Color: stops})},
		{"mention picker", picker},
		// A gradient of one stop has no run to spread over, so AccentText draws it
		// as a single object — the path where the fill goes through untouched.
		{"single-stop accent text", NewAccentText("Amelia", domain.Gradient{stops[0]}, 0, fyne.TextStyle{})},
	}

	for _, tc := range cases {
		walkTree(tc.root, func(obj fyne.CanvasObject, _ fyne.Position) {
			text, ok := obj.(*canvas.Text)
			if !ok || text.Color == nil {
				return
			}
			if !reflect.TypeOf(text.Color).Comparable() {
				t.Errorf("%s: %q is filled with %T, which cannot be a map key", tc.name, text.Text, text.Color)
			}
		})
	}
}
