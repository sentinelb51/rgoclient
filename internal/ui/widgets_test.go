package ui

import (
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
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
