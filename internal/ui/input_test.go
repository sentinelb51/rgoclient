package ui

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// newTestComposer builds the composer the way app.buildMessageArea does, so the
// layout assertions below hold against the real arrangement rather than a
// simplified stand-in.
func newTestComposer(t *testing.T) (*MessageInput, fyne.Window, *fyne.Container) {
	t.Helper()
	a := test.NewApp()
	a.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	window := test.NewWindow(nil)
	t.Cleanup(window.Close)

	input := NewMessageInput(Deps{}, window)
	input.SetPlaceHolder("Send a message...")

	background := canvas.NewRectangle(theme.Colors.ComposerBg)
	inner := VBoxNoSpacing(
		input.Mentions,
		input.ReplyContainer,
		input.AttachmentContainer,
		WithCaret(input),
	)
	padV, padH := theme.Sizes.ComposerPaddingV, theme.Sizes.ComposerPaddingH
	composer := container.NewPadded(container.NewStack(background, NewInset(inner, padV, padV, padH, padH)))

	window.SetContent(container.NewBorder(nil, composer, nil, nil, widget.NewLabel("messages")))
	window.Resize(fyne.NewSize(600, 400))
	return input, window, composer
}

// entryText finds the canvas.Text the entry actually draws, and its absolute
// position, by walking the rendered tree.
func entryText(t *testing.T, root fyne.CanvasObject) (*canvas.Text, fyne.Position) {
	t.Helper()

	var found *canvas.Text
	var at fyne.Position
	var walk func(obj fyne.CanvasObject, origin fyne.Position)
	walk = func(obj fyne.CanvasObject, origin fyne.Position) {
		pos := origin.Add(obj.Position())
		switch v := obj.(type) {
		case *canvas.Text:
			if found == nil && v.Text != "" {
				found, at = v, pos
			}
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

	if found == nil {
		t.Fatal("no text found in the composer")
	}
	return found, at
}

// TestComposerTextIsVerticallyCentred guards the original defect: composerMinSize
// added the input border on top of the entry's inner padding, but Fyne pays for
// that border out of the same padding. The four surplus pixels all landed below
// the caret, because the entry top-aligns its text inside its scroller — which
// is what made the composer look like it had slack space in it.
func TestComposerTextIsVerticallyCentred(t *testing.T) {
	input, _, composer := newTestComposer(t)
	input.SetText("A")

	text, pos := entryText(t, composer)
	above := pos.Y - composer.Position().Y
	below := (composer.Position().Y + composer.Size().Height) - (pos.Y + text.MinSize().Height)

	if diff := above - below; diff > 1 || diff < -1 {
		t.Errorf("composer text is not vertically centred: %.2fpx above, %.2fpx below", above, below)
	}
}

// TestComposerGrowsByWholeLines checks that each newline adds exactly one line
// of height and nothing else, so a multi-line composer stays as tight as a
// single-line one.
func TestComposerGrowsByWholeLines(t *testing.T) {
	input, _, _ := newTestComposer(t)

	input.SetText("one")
	single := input.MinSize().Height
	input.SetText("one\ntwo")
	double := input.MinSize().Height

	line := lineHeight(input.Theme().Size(fynetheme.SizeNameText))
	if diff := double - single - line; diff > 0.01 || diff < -0.01 {
		t.Errorf("second line added %.2fpx, want one line of %.2fpx", double-single, line)
	}
}

func candidates() []MentionCandidate {
	return []MentionCandidate{
		NewMentionCandidate("01CAESAR", "Caesar", "caesar", "", nil), // "sar" only as a substring
		NewMentionCandidate("01ELYNN", "Elynn", "elynn", "", nil),
		NewMentionCandidate("01MOON", "moonlit", "sarenity", "", nil), // "sar" prefixes the handle
		NewMentionCandidate("01SAREN", "Saren", "saren", "", nil),     // "sar" prefixes the name
	}
}

// TestMentionQuery covers when the picker may open: after whitespace or at the
// start of the message, never inside a word (an email address must stay an
// email address).
func TestMentionQuery(t *testing.T) {
	input, _, _ := newTestComposer(t)

	cases := []struct {
		text  string
		start int
		query string
		ok    bool
	}{
		{"@", 0, "", true},
		{"@el", 0, "el", true},
		{"hey @sar", 4, "sar", true},
		{"hey @", 4, "", true},
		{"mail@example", 0, "", false},
		{"hey @sar there", 0, "", false}, // caret is past the mention
		{"plain text", 0, "", false},
	}
	for _, tc := range cases {
		input.SetText(tc.text)
		input.CursorRow, input.CursorColumn = cursorPosition(tc.text, len([]rune(tc.text)))

		start, query, ok := input.mentionQuery()
		if ok != tc.ok || (ok && (start != tc.start || query != tc.query)) {
			t.Errorf("mentionQuery(%q) = (%d, %q, %v), want (%d, %q, %v)",
				tc.text, start, query, ok, tc.start, tc.query, tc.ok)
		}
	}
}

// TestMentionPickerRanking checks that prefix matches outrank substring matches
// and that either the display name or the handle can find someone.
func TestMentionPickerRanking(t *testing.T) {
	picker := NewMentionPicker(nil, nil)
	picker.SetCandidates(candidates())

	if !picker.Update("sar") {
		t.Fatal("query \"sar\" matched nobody")
	}
	var got []string
	for _, match := range picker.matches {
		got = append(got, match.Name)
	}
	// Prefix hits (on either the name or the handle) come first, in list order;
	// the substring-only hit is pushed to the back.
	if len(got) != 3 || got[0] != "moonlit" || got[1] != "Saren" || got[2] != "Caesar" {
		t.Errorf("ranking = %v, want [moonlit Saren Caesar]", got)
	}

	if !picker.Update("lyn") { // substring of "Elynn" only
		t.Fatal("substring query matched nobody")
	}
	if len(picker.matches) != 1 || picker.matches[0].Name != "Elynn" {
		t.Errorf("substring query = %v, want [Elynn]", picker.matches)
	}

	if picker.Update("zzz") {
		t.Error("an unmatched query should report no candidates")
	}
}

// TestAcceptMentionInsertsToken checks the whole round trip: the typed "@que"
// is replaced by the wire-format mention token, a trailing space is left for
// the next word, and the caret ends up after it.
func TestAcceptMentionInsertsToken(t *testing.T) {
	input, _, _ := newTestComposer(t)
	input.Mentions.SetCandidates(candidates())

	input.SetText("hey @el")
	input.CursorRow, input.CursorColumn = cursorPosition(input.Text, 7)
	input.syncMentions()

	if !input.Mentions.Visible() {
		t.Fatal("picker did not open on a matching mention")
	}
	input.Mentions.Accept()

	if want := "hey <@01ELYNN> "; input.Text != want {
		t.Errorf("text = %q, want %q", input.Text, want)
	}
	if input.cursorIndex() != len([]rune(input.Text)) {
		t.Errorf("caret at %d, want end of text (%d)", input.cursorIndex(), len([]rune(input.Text)))
	}
	if input.Mentions.Visible() {
		t.Error("picker stayed open after accepting")
	}
}

// TestPickerGrowsComposer confirms the picker is actually laid out when it
// opens. It lives inside the composer card, so showing it has to push the card
// taller — if the surrounding layout didn't re-measure, the picker would be
// present but zero-height, which looks exactly like it never opened.
func TestPickerGrowsComposer(t *testing.T) {
	input, window, composer := newTestComposer(t)
	input.Mentions.SetCandidates(candidates())

	closed := composer.MinSize().Height

	input.SetText("@")
	input.CursorRow, input.CursorColumn = cursorPosition(input.Text, 1)
	input.syncMentions()
	window.Resize(window.Content().Size()) // force a layout pass

	open := composer.MinSize().Height
	if open <= closed {
		t.Errorf("composer height %.2f did not grow when the picker opened (was %.2f)", open, closed)
	}
	if input.Mentions.Size().Height <= 0 {
		t.Errorf("picker was shown but laid out at zero height")
	}
}

// TestRenderComposerPreview writes a PNG of the composer for eyeballing. It
// only runs when RGO_PREVIEW names an output path.
func TestRenderComposerPreview(t *testing.T) {
	out := os.Getenv("RGO_PREVIEW")
	if out == "" {
		t.Skip("set RGO_PREVIEW=<path.png> to render")
	}

	a := test.NewApp()
	a.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	window := test.NewWindow(nil)
	defer window.Close()

	input := NewMessageInput(Deps{}, window)
	input.SetPlaceHolder("Send a message...")
	input.Mentions.SetCandidates([]MentionCandidate{
		NewMentionCandidate("01A", "Elynn", "elynn", "", nil),
		NewMentionCandidate("01B", "Saren", "saren", "", nil),
		NewMentionCandidate("01C", "elysia", "ely_flowers", "", nil),
	})

	background := canvas.NewRectangle(theme.Colors.ComposerBg)
	background.CornerRadius = theme.Sizes.ComposerRadius
	background.StrokeColor = theme.Colors.ComposerBorderFocus
	background.StrokeWidth = 1

	inner := VBoxNoSpacing(input.Mentions, input.ReplyContainer, input.AttachmentContainer, WithCaret(input))
	padV, padH := theme.Sizes.ComposerPaddingV, theme.Sizes.ComposerPaddingH
	composer := container.NewPadded(container.NewStack(background, NewInset(inner, padV, padV, padH, padH)))

	page := container.NewStack(
		canvas.NewRectangle(theme.Colors.MessageAreaBackground),
		container.NewBorder(nil, composer, nil, nil, nil),
	)
	window.SetContent(page)
	window.Resize(fyne.NewSize(660, 260))

	input.SetText("@el")
	input.CursorRow, input.CursorColumn = cursorPosition(input.Text, 3)
	input.syncMentions()
	// Opening the picker changes the composer's minimum height. The real driver
	// notices that through Canvas.EnsureMinSize on its next repaint; the test
	// canvas doesn't, so a second resize is what re-runs the layout here.
	window.Resize(fyne.NewSize(660, 261))

	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, window.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
