package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/domain"
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

	input := NewMessageInput(testDeps(), window)
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

func channelCandidates() []MentionCandidate {
	return []MentionCandidate{
		NewChannelCandidate(domain.Channel{ID: "01GENERAL", Name: "general"}),
		NewChannelCandidate(domain.Channel{ID: "01VOICE", Name: "lounge", Kind: domain.ChannelVoice}),
	}
}

// TestMentionQuery covers when the picker may open and on which pool: after
// whitespace or at the start of the message, never inside a word (an email
// address must stay an email address), with '@' naming people and '#' channels.
func TestMentionQuery(t *testing.T) {
	input, _, _ := newTestComposer(t)

	cases := []struct {
		text  string
		start int
		kind  MentionKind
		query string
		ok    bool
	}{
		{"@", 0, MentionUser, "", true},
		{"@el", 0, MentionUser, "el", true},
		{"hey @sar", 4, MentionUser, "sar", true},
		{"hey @", 4, MentionUser, "", true},
		{"#", 0, MentionChannel, "", true},
		{"see #gen", 4, MentionChannel, "gen", true},
		{"mail@example", 0, MentionUser, "", false},
		{"c#sharp", 0, MentionUser, "", false},        // a marker mid-word is a character
		{"hey @sar there", 0, MentionUser, "", false}, // caret is past the mention
		{"plain text", 0, MentionUser, "", false},
	}
	for _, tc := range cases {
		input.SetText(tc.text)
		input.CursorRow, input.CursorColumn = cursorPosition(tc.text, len(tc.text))

		start, kind, query, ok := input.mentionQuery()
		if ok != tc.ok || (ok && (start != tc.start || kind != tc.kind || query != tc.query)) {
			t.Errorf("mentionQuery(%q) = (%d, %d, %q, %v), want (%d, %d, %q, %v)",
				tc.text, start, kind, query, ok, tc.start, tc.kind, tc.query, tc.ok)
		}
	}
}

// TestMentionPoolsAreSeparate: the two markers filter two lists. A '#' must
// never offer a person, nor '@' a channel, however well the query matches the
// other pool.
func TestMentionPoolsAreSeparate(t *testing.T) {
	picker := NewMentionPicker(nil, nil)
	picker.SetCandidates(MentionUser, candidates())
	picker.SetCandidates(MentionChannel, channelCandidates())

	if picker.Update(MentionChannel, "el") {
		t.Error("a channel query matched a person (\"Elynn\")")
	}
	if picker.Update(MentionUser, "gen") {
		t.Error("a user query matched a channel (\"general\")")
	}
	if !picker.Update(MentionChannel, "gen") || picker.matches[0].Name != "general" {
		t.Errorf("channel query = %v, want [general]", picker.matches)
	}
}

// TestAcceptChannelInsertsToken pins the wire form of the other mention: a
// channel is <#id>, not <@id>, and inserting one leaves the same trailing space
// a person's does.
func TestAcceptChannelInsertsToken(t *testing.T) {
	input, _, _ := newTestComposer(t)
	input.Mentions.SetCandidates(MentionChannel, channelCandidates())

	input.SetText("see #gen")
	input.CursorRow, input.CursorColumn = cursorPosition(input.Text, 8)
	input.syncMentions()

	if !input.Mentions.Visible() {
		t.Fatal("picker did not open on a matching channel")
	}
	input.Mentions.Accept()

	if want := "see <#01GENERAL> "; input.Text != want {
		t.Errorf("text = %q, want %q", input.Text, want)
	}
}

// TestMentionPickerRanking checks that prefix matches outrank substring matches
// and that either the display name or the handle can find someone.
func TestMentionPickerRanking(t *testing.T) {
	picker := NewMentionPicker(nil, nil)
	picker.SetCandidates(MentionUser, candidates())

	if !picker.Update(MentionUser, "sar") {
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

	if !picker.Update(MentionUser, "lyn") { // substring of "Elynn" only
		t.Fatal("substring query matched nobody")
	}
	if len(picker.matches) != 1 || picker.matches[0].Name != "Elynn" {
		t.Errorf("substring query = %v, want [Elynn]", picker.matches)
	}

	if picker.Update(MentionUser, "zzz") {
		t.Error("an unmatched query should report no candidates")
	}
}

// TestAcceptMentionInsertsToken checks the whole round trip: the typed "@que"
// is replaced by the wire-format mention token, a trailing space is left for
// the next word, and the caret ends up after it.
func TestAcceptMentionInsertsToken(t *testing.T) {
	input, _, _ := newTestComposer(t)
	input.Mentions.SetCandidates(MentionUser, candidates())

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
	if input.cursorOffset() != len(input.Text) {
		t.Errorf("caret at %d, want end of text (%d)", input.cursorOffset(), len(input.Text))
	}
	if input.Mentions.Visible() {
		t.Error("picker stayed open after accepting")
	}
}

// TestPickerSurvivesBlur pins the rule the whole mouse path rests on. Fyne
// unfocuses on the mouse *press* and only decides where the tap lands by
// hit-testing again on the release, so a picker that closed itself on blur
// resized the composer out from under the click: the first click on anything —
// a picker row included — was spent dismissing the picker and never arrived.
func TestPickerSurvivesBlur(t *testing.T) {
	input, _, _ := newTestComposer(t)
	input.Mentions.SetCandidates(MentionUser, candidates())

	input.SetText("hey @el")
	input.CursorRow, input.CursorColumn = cursorPosition(input.Text, 7)
	input.syncMentions()
	if !input.Mentions.Visible() {
		t.Fatal("picker did not open on a matching mention")
	}

	input.FocusLost()
	if !input.Mentions.Visible() {
		t.Fatal("blurring the composer closed the picker")
	}

	// The blur left the caret alone, so the row tapped after it still resolves.
	input.Mentions.Accept()
	if want := "hey <@01ELYNN> "; input.Text != want {
		t.Errorf("text after a tap that blurred the entry = %q, want %q", input.Text, want)
	}
}

// TestPickerRefiltersOnNewCandidates: an open picker now outlives the entry's
// focus, so it can outlive the channel it was opened in. Replacing the pool has
// to re-run the query rather than leave rows offering people who are no longer
// in the list.
func TestPickerRefiltersOnNewCandidates(t *testing.T) {
	input, _, _ := newTestComposer(t)
	input.Mentions.SetCandidates(MentionUser, candidates())

	input.SetText("@el")
	input.CursorRow, input.CursorColumn = cursorPosition(input.Text, 3)
	input.syncMentions()
	if !input.Mentions.Visible() {
		t.Fatal("picker did not open on a matching mention")
	}

	input.Mentions.SetCandidates(MentionUser, nil)
	if input.Mentions.Visible() {
		t.Error("picker stayed open over a candidate list that matches nobody")
	}
}

// TestPickerGrowsComposer confirms the picker is actually laid out when it
// opens. It lives inside the composer card, so showing it has to push the card
// taller — if the surrounding layout didn't re-measure, the picker would be
// present but zero-height, which looks exactly like it never opened.
func TestPickerGrowsComposer(t *testing.T) {
	input, window, composer := newTestComposer(t)
	input.Mentions.SetCandidates(MentionUser, candidates())

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
