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

	"RGOClient/internal/ui/theme"
)

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
