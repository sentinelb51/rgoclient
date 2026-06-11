package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/markdown"
)

// layoutMessage renders a message body at the given width and returns the laid
// out canvas objects, so tests can assert on their positions and sizes.
func layoutMessage(t *testing.T, body string, width float32) []fyne.CanvasObject {
	t.Helper()
	rt, ok := renderMessageBody(body).(fyne.Widget)
	if !ok {
		t.Fatalf("%q: rendered body is not a widget", body)
	}
	win := test.NewWindow(rt)
	t.Cleanup(win.Close)
	rt.Resize(fyne.NewSize(width, 400))
	rt.Refresh()
	r := test.WidgetRenderer(rt)
	r.Layout(fyne.NewSize(width, 400))
	return r.Objects()
}

// firstDecorated returns the first decoratedText visual in a rendered message.
func firstDecorated(objs []fyne.CanvasObject) *decoratedText {
	for _, o := range objs {
		if d, ok := o.(*decoratedText); ok {
			return d
		}
	}
	return nil
}

// TestUniformBodySelectable verifies that bodies whose whole content shares
// one style — plain, all-bold, a lone code block, a heading — render as a
// selectable Label carrying that style, while mixed-style bodies keep the
// RichText renderer and an empty body stays a zero-height RichText.
func TestUniformBodySelectable(t *testing.T) {
	label := func(body string) *widget.Label {
		t.Helper()
		l, ok := renderMessageBody(body).(*widget.Label)
		if !ok || !l.Selectable {
			t.Fatalf("%q did not render as a selectable label", body)
		}
		return l
	}

	label("hello world\nsecond line")
	if l := label("**all bold message**"); !l.TextStyle.Bold {
		t.Error("all-bold body lost its bold style")
	}
	if l := label("```go\nx := 1\n```"); !l.TextStyle.Monospace || l.Text != "x := 1" {
		t.Errorf("code block label = %+v, want monospace 'x := 1'", l)
	}
	if l := label("# Big news"); !l.TextStyle.Bold || l.SizeName == "" {
		t.Error("heading label lost its bold style or heading size")
	}
	label("- one\n- two")
	label("snake_case_name stays plain")

	for _, body := range []string{"", "**bold** mixed", "~~struck~~", "> quoted"} {
		if _, ok := renderMessageBody(body).(*widget.RichText); !ok {
			t.Errorf("%q should render as RichText", body)
		}
	}
}

// TestDecorationNotStretched guards the original bug: a decoration at the start
// of a line was stretched to fill the whole row. The visual must keep its
// intrinsic (text) width.
func TestDecorationNotStretched(t *testing.T) {
	const width = 300
	for _, body := range []string{"~~struck~~", "||hidden||", "__underline__"} {
		d := firstDecorated(layoutMessage(t, body, width))
		if d == nil {
			t.Fatalf("%q: no decorated segment rendered", body)
		}
		if d.Size().Width > width/2 {
			t.Errorf("%q: width = %v, want intrinsic (≪ %v)", body, d.Size().Width, width)
		}
	}
}

// TestUnderlineRenders verifies underline produces a drawn line (Fyne ignores
// TextStyle.Underline, so it must be a decoratedText with an underLine).
func TestUnderlineRenders(t *testing.T) {
	d := firstDecorated(layoutMessage(t, "__underlined__", 300))
	if d == nil {
		t.Fatal("no decorated segment rendered for underline")
	}
	if d.underLine == nil {
		t.Fatal("underline word has no underline visual")
	}
}

// TestDecorationWraps verifies a long decorated span breaks across rows like
// ordinary text instead of overflowing a single line.
func TestDecorationWraps(t *testing.T) {
	objs := layoutMessage(t, "before ~~one two three four five six seven eight~~ after", 160)

	rows := map[float32]int{}
	for _, o := range objs {
		if _, ok := o.(*decoratedText); ok {
			rows[o.Position().Y]++
		}
	}
	if len(rows) < 2 {
		t.Fatalf("decorated words occupy %d row(s), want them to wrap across ≥2", len(rows))
	}
}

// TestSpoilerSharedReveal verifies every word of one spoiler span shares a single
// reveal state, so tapping one word reveals the whole span.
func TestSpoilerSharedReveal(t *testing.T) {
	b := &mdBuilder{}
	b.text("two words", emphasis{}, widget.RichTextStyle{}, &spoilerState{})

	var states []*spoilerState
	for _, seg := range b.segs {
		if d, ok := seg.(*decoratedSegment); ok {
			states = append(states, d.state)
		}
	}
	if len(states) != 2 {
		t.Fatalf("got %d spoiler word segments, want 2", len(states))
	}
	if states[0] == nil || states[0] != states[1] {
		t.Fatal("spoiler words do not share reveal state")
	}
}

// TestNestedFormattingInDecoration verifies bold survives inside a strikethrough
// span (the struck word should carry the bold text style).
func TestNestedFormattingInDecoration(t *testing.T) {
	b := &mdBuilder{}
	for _, block := range markdown.Parse("~~a **bold** c~~").Blocks {
		b.block(block)
	}

	var sawBold bool
	for _, seg := range b.segs {
		if d, ok := seg.(*decoratedSegment); ok && d.text == "bold" {
			sawBold = d.style.Bold
		}
	}
	if !sawBold {
		t.Fatal("bold inside strikethrough was lost")
	}
}
