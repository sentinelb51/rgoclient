package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/markdown"
)

// layoutMessage renders a message body at the given width and returns the laid
// out canvas objects, so tests can assert on their positions and sizes.
func layoutMessage(t *testing.T, body string, width float32) []fyne.CanvasObject {
	t.Helper()
	rt, ok := renderMessageBody(testDeps(), body, nil).(fyne.Widget)
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
// one style — plain, all-bold, a heading — render as a selectable Label carrying
// that style, while mixed-style bodies keep the RichText renderer and an empty
// body stays a zero-height RichText.
func TestUniformBodySelectable(t *testing.T) {
	label := func(body string) *widget.Label {
		t.Helper()
		b, ok := renderMessageBody(testDeps(), body, nil).(*bodyText)
		if !ok || !b.Selectable {
			t.Fatalf("%q did not render as a selectable label", body)
		}
		return &b.Label
	}

	label("hello world\nsecond line")
	if l := label("**all bold message**"); !l.TextStyle.Bold {
		t.Error("all-bold body lost its bold style")
	}
	if l := label("# Big news"); !l.TextStyle.Bold || l.SizeName == "" {
		t.Error("heading label lost its bold style or heading size")
	}
	label("- one\n- two")
	label("snake_case_name stays plain")

	for _, body := range []string{"", "**bold** mixed", "~~struck~~", "> quoted"} {
		if _, ok := renderMessageBody(testDeps(), body, nil).(*widget.RichText); !ok {
			t.Errorf("%q should render as RichText", body)
		}
	}
}

// TestEmojiSitsOnItsLine covers the three rules a custom emoji brings with it. It
// is a picture, so a body carrying one can never flatten to the selectable Label
// its text alone would have been. RichText cannot break a row before a segment it
// cannot measure as text, so the body keeps a gutter at least as wide as the
// emoji — without it, one landing at a line end is cut off by the message column.
// And it must be exactly a line tall: RichText baseline-aligns a row whose objects
// differ in height and reads this one's baseline as zero, which drops it into the
// line below.
func TestEmojiSitsOnItsLine(t *testing.T) {
	test.NewTempApp(t)

	const body = "look :01J9WN3PHX4ZQSNSZH10CK4RHS:"

	rendered := renderMessageBody(testDeps(), body, nil)
	row, ok := rendered.(*fyne.Container)
	if !ok {
		t.Fatalf("a body with an emoji rendered as %T, want the row holding the reserve", rendered)
	}
	if len(row.Objects) != 2 {
		t.Fatalf("the row holds %d objects, want the text and the reserve", len(row.Objects))
	}

	side := emojiSide("")
	if got := row.Objects[1].MinSize().Width; got < side {
		t.Errorf("the gutter is %v wide, want at least the emoji's %v", got, side)
	}

	rt := row.Objects[0].(*widget.RichText)
	rt.Resize(fyne.NewSize(400, 100))
	objects := test.WidgetRenderer(rt).Objects()

	emoji, text := objects[1], objects[0]
	if emoji.Size() != fyne.NewSize(side, side) {
		t.Errorf("the emoji is %v, want the square %v the line is tall", emoji.Size(), side)
	}
	if emoji.Position().Y != text.Position().Y {
		t.Errorf("the emoji sits at y=%v and the words beside it at y=%v", emoji.Position().Y, text.Position().Y)
	}
}

// bodyCatcher renders a selectable body at the given width and returns the
// catcher covering its selection overlay.
func bodyCatcher(t *testing.T, onMenu func(*fyne.PointEvent)) (*bodyText, *selectionCatcher) {
	t.Helper()

	body, ok := renderMessageBody(testDeps(), "hello world", onMenu).(*bodyText)
	if !ok {
		t.Fatal("plain body did not render as a selectable label")
	}

	win := test.NewWindow(body)
	t.Cleanup(win.Close)
	body.Resize(fyne.NewSize(200, 40))

	objects := test.WidgetRenderer(body).Objects()
	catcher, ok := objects[len(objects)-1].(*selectionCatcher)
	if !ok {
		t.Fatalf("last renderer object is %T, want the catcher on top of the selection", objects[len(objects)-1])
	}

	return body, catcher
}

// TestSelectableBodyCatchesRightClick covers the reason bodyText exists: Fyne's
// selection overlay would otherwise take the right-click and answer it with its
// own "Copy" menu. The catcher has to sit above it and hand the click on, while
// still driving the overlay for everything selection needs.
func TestSelectableBodyCatchesRightClick(t *testing.T) {
	test.NewTempApp(t)

	var menued int
	body, catcher := bodyCatcher(t, func(*fyne.PointEvent) { menued++ })

	catcher.TappedSecondary(&fyne.PointEvent{})
	if menued != 1 {
		t.Fatalf("right-click raised the message menu %d times, want once", menued)
	}

	// Selecting is a press and a drag across the text; the overlay only answers if
	// the catcher really is forwarding to it. The drag has to carry its delta —
	// the overlay takes the span's start to be where the pointer came from.
	catcher.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	catcher.Dragged(&fyne.DragEvent{
		Position: fyne.NewPos(180, 10),
		Dragged:  fyne.NewDelta(180, 0),
	})
	catcher.DragEnd()

	if body.SelectedText() == "" {
		t.Error("dragging across the body selected nothing, so the catcher is swallowing selection")
	}
}

// TestSelectableBodyWithoutMenu covers the fallback: with no menu to raise there
// is nothing to catch for, and the body stays an ordinary selectable Label.
func TestSelectableBodyWithoutMenu(t *testing.T) {
	test.NewTempApp(t)

	body, ok := renderMessageBody(testDeps(), "hello world", nil).(*bodyText)
	if !ok {
		t.Fatal("plain body did not render as a selectable label")
	}

	win := test.NewWindow(body)
	t.Cleanup(win.Close)

	for _, object := range test.WidgetRenderer(body).Objects() {
		if _, ok := object.(*selectionCatcher); ok {
			t.Fatal("a body with no context menu mounted a catcher anyway")
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
		b.block(block, widget.RichTextStyle{})
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
