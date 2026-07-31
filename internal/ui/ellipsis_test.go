package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// widthOf measures a string the same way TruncateToWidth does, so the tests can
// express their bounds in real rendered widths rather than guessed pixels.
func widthOf(text string) float32 {
	return fyne.MeasureText(text, 14, fyne.TextStyle{}).Width
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
