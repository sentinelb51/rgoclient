package app

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// TestWindowMinimumIgnoresContent covers the one rule that keeps the window
// still: Fyne grows a window to its content's minimum size the frame that
// minimum outgrows it, and never gives the room back, so a section reporting
// what it happens to be holding drags the window about as things arrive. Every
// surface below reached the window's minimum before it was a layer or a floor —
// a wide attachment, a fourth notice, a hovered server with a long name and the
// settings page each resized the window from under the reader.
func TestWindowMinimumIgnoresContent(t *testing.T) {
	fyneApp := test.NewTempApp(t)
	fyneApp.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	a := New(fyneApp, Info{})
	root := a.buildUI()
	baseline := root.MinSize()

	long := strings.Repeat("a-very-long-file-name", 6)

	cases := []struct {
		name string
		grow func()
	}{
		{"a message wider than the column", func() {
			message := &domain.Message{ID: "01TESTMESSAGE00000000000M1", ChannelID: "01CH", AuthorID: "01AUTHOR"}
			message.Attachments = []*domain.File{{ID: "01F", Name: long + ".txt", Kind: domain.FileText, Size: 4096}}
			a.messages.SetMessages([]*domain.Message{message})
		}},
		{"a stack of notices", func() {
			for range 5 {
				a.notices.Push(ui.ToneWarning, strings.Repeat("something went wrong ", 4))
			}
		}},
		{"a tooltip naming a long server", func() {
			a.tooltip.Show(strings.Repeat("Server Name ", 6), a.homeButton)
		}},
		{"the mention picker over the composer", func() {
			a.input.Mentions.SetCandidates(ui.MentionUser, []ui.MentionCandidate{
				{ID: "01U", Name: strings.Repeat("Long Name ", 5), Username: long},
			})
			a.input.Mentions.Update(ui.MentionUser, "")
			a.input.Mentions.Show()
		}},
		{"the settings page", func() {
			a.settings.Rebuild()
			a.settings.Layer.Show()
		}},
	}

	for _, c := range cases {
		c.grow()
		if got := root.MinSize(); got != baseline {
			t.Errorf("%s moved the window's minimum to %v, want %v", c.name, got, baseline)
		}
	}
}

// TestMessageColumnReportsItsFloor covers the other half: the column may not
// report its contents, but it must still ask for the room a conversation needs,
// or the window could be dragged down to nothing.
func TestMessageColumnReportsItsFloor(t *testing.T) {
	fyneApp := test.NewTempApp(t)
	fyneApp.Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	a := New(fyneApp, Info{})
	a.buildUI()

	want := fyne.NewSize(theme.Sizes.MessageAreaMinWidth, theme.Sizes.MessageAreaMinHeight)
	if got := a.mainRow.Objects[2].MinSize(); got != want {
		t.Errorf("the message column asks for %v, want its floor of %v", got, want)
	}
}
