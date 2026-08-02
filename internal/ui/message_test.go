package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/theme"
)

// TestHeaderTimestampCentredWithName guards the alignment of the smaller
// timestamp against the bold author name: the header used to drop it by a fixed
// offset, which left it sitting low on the name line. Both texts are stretched to
// the line's height and canvas.Text centres its glyphs in the height it is given,
// so comparing the two boxes' centres compares what is drawn.
func TestHeaderTimestampCentredWithName(t *testing.T) {
	test.NewTempApp(t)

	const (
		name  = "chickensoup"
		stamp = "2 days ago, 5:20 PM"
	)

	author := canvas.NewText(name, theme.Colors.TextPrimary)
	author.TextStyle = fyne.TextStyle{Bold: true}

	header := buildMessageHeader(author, stamp, widget.NewLabel("body"))
	header.Resize(header.MinSize())

	var timestamp *canvas.Text
	var namePos, stampPos fyne.Position
	walkTree(header, func(obj fyne.CanvasObject, pos fyne.Position) {
		text, ok := obj.(*canvas.Text)
		if !ok {
			return
		}

		switch text.Text {
		case name:
			namePos = pos
		case stamp:
			timestamp, stampPos = text, pos
		}
	})

	if timestamp == nil {
		t.Fatal("the header drew no timestamp")
	}

	nameCentre := namePos.Y + author.Size().Height/2
	stampCentre := stampPos.Y + timestamp.Size().Height/2
	if diff := nameCentre - stampCentre; diff > 0.5 || diff < -0.5 {
		t.Errorf("timestamp centred at y=%v, name at y=%v", stampCentre, nameCentre)
	}
}

/* Vertical alignment */

// styledApp mounts a temp app wearing the real theme. The message row's rhythm
// is measured in line heights, and those come from the font: under Fyne's test
// font the avatar's misalignment measured a third of what Montserrat gives.
func styledApp(t *testing.T) Deps {
	t.Helper()

	test.NewTempApp(t).Settings().SetTheme(theme.NewAppTheme(fynetheme.DefaultTheme()))

	return viewerDeps()
}

func testMessage(id, content string) *revoltgo.Message {
	return &revoltgo.Message{ID: id, Channel: "01TESTCHANNEL0000000000000", Author: "01TESTAUTHOR00000000000000", Content: content}
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
				case *canvas.Text:
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

// TestGutterTimestampCentredOnItsLine covers the other half of the gutter: a
// grouped continuation shows a hover timestamp where the avatar would be, and it
// is smaller text, so it takes an offset of its own to share the body line's
// centre rather than sitting low against it.
func TestGutterTimestampCentredOnItsLine(t *testing.T) {
	deps := styledApp(t)

	w := NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000G1", "hello"), "", true, false)
	w.Resize(fyne.NewSize(600, w.MinSize().Height))

	var stampTop float32
	walkTree(w, func(obj fyne.CanvasObject, pos fyne.Position) {
		if text, ok := obj.(*canvas.Text); ok && text == w.gutterTimestamp {
			stampTop = pos.Y
		}
	})

	bodyTop, ok := firstLineTop(w)
	if !ok {
		t.Fatal("the continuation drew no body")
	}

	stampCentre := stampTop + w.gutterTimestamp.Size().Height/2
	lineCentre := bodyTop + messageLineHeight()/2
	if diff := stampCentre - lineCentre; diff > 0.01 || diff < -0.01 {
		t.Errorf("gutter timestamp centred at y=%v, body line at %v", stampCentre, lineCentre)
	}
}

// TestReplyPreviewLeavesTheRowInPlace covers the spacing a reply used to steal:
// the quoted line sits inside the row's margins, indented to the body's column,
// so a replying message opens the same distance from its own top edge as one
// without a reply and its avatar lands under, not beside, the quote.
func TestReplyPreviewLeavesTheRowInPlace(t *testing.T) {
	deps := styledApp(t)

	replying := testMessage("01TESTMESSAGE00000000000R2", "hello")
	replying.Replies = []string{"01TESTMESSAGE00000000000R3"}

	plain := NewMessageWidget(deps, testMessage("01TESTMESSAGE00000000000P1", "hello"), "", false, false)
	quoted := NewMessageWidget(deps, replying, "", false, false)
	for _, w := range []*MessageWidget{plain, quoted} {
		w.Resize(fyne.NewSize(600, w.MinSize().Height))
	}

	// What each row opens with: the plain one its author name, the quoted one the
	// top of its preview. Both are the first ink under the row's top margin.
	var plainTop, quoteTop, quoteX, bodyX float32
	walkTree(plain, func(obj fyne.CanvasObject, pos fyne.Position) {
		if text, ok := obj.(*canvas.Text); ok && text == plain.authorText {
			plainTop = pos.Y
		}
	})
	walkTree(quoted, func(obj fyne.CanvasObject, pos fyne.Position) {
		if circle, ok := obj.(*canvas.Circle); ok {
			switch circle.Size().Height {
			case replyPreviewAvatarSize:
				quoteTop, quoteX = pos.Y, pos.X
			case theme.Sizes.MessageAvatarSize:
				bodyX = pos.X + circle.Size().Width // the gutter the body clears
			}
		}
	})

	if quoteTop != plainTop {
		t.Errorf("a quoted row opens at y=%v, a plain one at %v", quoteTop, plainTop)
	}
	if quoteX <= bodyX {
		t.Errorf("the quote starts at x=%v, inside the avatar gutter ending at %v", quoteX, bodyX)
	}
}

// TestReplyLineDrawsAnElbowPerReply covers the line tying a quoted line to the
// message answering it: one elbow per reply, cornered square, its arm resting on
// the quote's centre and its leg hanging below toward the message. Stacked
// replies each get their own rather than sharing one spine, so two answers read
// as two.
func TestReplyLineDrawsAnElbowPerReply(t *testing.T) {
	deps := styledApp(t)

	message := testMessage("01TESTMESSAGE00000000000R4", "hello")
	message.Replies = []string{"01TESTMESSAGE00000000000R5", "01TESTMESSAGE00000000000R6"}

	w := NewMessageWidget(deps, message, "", false, false)
	w.Resize(fyne.NewSize(600, w.MinSize().Height))

	type stroke struct {
		pos  fyne.Position
		size fyne.Size
	}

	// The elbow is laid out before the quote it leads, so the three slices stay in
	// step: index i is the i'th reply, top to bottom.
	var arms, legs []stroke
	var quotes []fyne.Position
	walkTree(w, func(obj fyne.CanvasObject, pos fyne.Position) {
		switch v := obj.(type) {
		case *canvas.Rectangle:
			if v.FillColor != theme.Colors.ReplyLine {
				return
			}
			if v.CornerRadius != 0 {
				t.Errorf("the reply line is rounded by %v", v.CornerRadius)
			}
			if s := (stroke{pos, v.Size()}); s.size.Width > s.size.Height {
				arms = append(arms, s)
			} else {
				legs = append(legs, s)
			}
		case *canvas.Circle:
			if v.Size().Height == replyPreviewAvatarSize {
				quotes = append(quotes, pos.Add(fyne.NewPos(0, v.Size().Height/2)))
			}
		}
	})

	if len(arms) != len(message.Replies) || len(legs) != len(message.Replies) || len(quotes) != len(message.Replies) {
		t.Fatalf("%d replies drew %d arms, %d legs and %d quotes", len(message.Replies), len(arms), len(legs), len(quotes))
	}

	for i := range arms {
		arm, leg, quote := arms[i], legs[i], quotes[i]

		if arm.pos != leg.pos {
			t.Errorf("reply %d: the arm starts at %v and the leg at %v, so the corner is broken", i, arm.pos, leg.pos)
		}
		if centre := arm.pos.Y + arm.size.Height/2; centre != quote.Y {
			t.Errorf("reply %d: the arm rests at y=%v, the quote's centre is %v", i, centre, quote.Y)
		}
		if leg.size.Height <= arm.size.Height {
			t.Errorf("reply %d: the leg is %vpx tall, so it never leaves the arm", i, leg.size.Height)
		}
		if end, want := arm.pos.X+arm.size.Width+theme.Sizes.MessageReplyLineGap, quote.X; end != want {
			t.Errorf("reply %d: the arm reaches x=%v, want it to stop %vpx short of the quote at %v",
				i, arm.pos.X+arm.size.Width, theme.Sizes.MessageReplyLineGap, want)
		}
		if i > 0 {
			// Each elbow stands alone: the one above ends before this one begins.
			if foot := legs[i-1].pos.Y + legs[i-1].size.Height; foot > arm.pos.Y {
				t.Errorf("reply %d: the previous leg runs to y=%v, into this arm at %v", i, foot, arm.pos.Y)
			}
			if arm.pos.X != arms[i-1].pos.X {
				t.Errorf("reply %d: the elbow stands at x=%v, the one above at %v", i, arm.pos.X, arms[i-1].pos.X)
			}
		}
	}
}
