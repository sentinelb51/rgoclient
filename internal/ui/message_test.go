package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// styledApp mounts a temp app wearing the real theme. The message row's rhythm
// is measured in line heights, and those come from the font: under Fyne's test
// font the avatar's misalignment measured a third of what Montserrat gives.
func styledApp(t *testing.T) Deps {
	t.Helper()

	test.NewTempApp(t).Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	return testDeps()
}

func testMessage(id, content string) *domain.Message {
	return &domain.Message{ID: id, ChannelID: "01TESTCHANNEL0000000000000", AuthorID: "01TESTAUTHOR00000000000000", Content: content}
}

// firstLineTop returns where the body's first line of ink starts. The body is a
// RichText (a Selectable Label wraps one), which draws its text an InnerPadding
// in from its own top — the offset newFlushContainer cancels so the body lines
// up with the author name above it.
func firstLineTop(w *MessageWidget) (float32, bool) {
	var top float32
	found := false
	walkTree(w, func(obj fyne.CanvasObject, pos fyne.Position) {
		if _, ok := obj.(*widget.RichText); ok && !found {
			top, found = pos.Y+fynetheme.InnerPadding(), true
		}
	})

	return top, found
}

// TestAvatarCentredOnFirstLine covers the message row's vertical rhythm: the
// avatar is centred on the block a single-line message occupies — the author
// line plus one line of body — so its centre falls on the seam between the two.
// Everything else a row can carry (a day separator, a reply preview, a tightened
// group margin, a body of any length) moves the whole row and never the avatar
// within it, which is what keeps the avatar from appearing to drift.
func TestAvatarCentredOnFirstLine(t *testing.T) {
	deps := styledApp(t)

	replying := testMessage("01TESTMESSAGE00000000000R0", "hello")
	replying.Replies = []string{"01TESTMESSAGE00000000000R1"}

	cases := []struct {
		name string
		w    *MessageWidget
	}{
		{"plain", NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000M1", "hello"), "", false, false)},
		{"opening a day", NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000M2", "hello"), "Today", false, false)},
		{"heading a group", NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000M3", "hello"), "", false, true)},
		{"replying", NewMessageWidget(deps, replying, "", false, false)},
		{"multiple lines", NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000M4", "hello\nworld"), "", false, false)},
		{"taller first line", NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000M5", "# Heading"), "", false, false)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.w.Resize(fyne.NewSize(600, c.w.MinSize().Height))

			var avatar *canvas.Circle
			var avatarTop, nameTop float32
			walkTree(c.w, func(obj fyne.CanvasObject, pos fyne.Position) {
				switch v := obj.(type) {
				case *canvas.Circle:
					// A reply preview carries a smaller one of its own.
					if v.Size().Height == theme.Sizes.MessageAvatarSize && avatar == nil {
						avatar, avatarTop = v, pos.Y
					}
				case *AccentText:
					if v == c.w.authorText {
						nameTop = pos.Y
					}
				}
			})

			if avatar == nil {
				t.Fatal("the message drew no avatar")
			}

			bodyTop, ok := firstLineTop(c.w)
			if !ok {
				t.Fatal("the message drew no body")
			}

			// The author line is exactly one line tall, so the body opens where it
			// ends — and that seam is where the avatar's centre belongs.
			seam := nameTop + messageLineHeight()
			if diff := bodyTop - seam; diff > 0.01 || diff < -0.01 {
				t.Errorf("body opens at y=%v, one line under the name is %v", bodyTop, seam)
			}
			if centre := avatarTop + avatar.Size().Height/2; centre != seam {
				t.Errorf("avatar centred at y=%v, want the seam at %v", centre, seam)
			}
		})
	}
}
