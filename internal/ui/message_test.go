package ui

import (
	"image/color"
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

// TestMentionHighlight covers the rule the warm background stands on: a row is
// tinted because Revolt named *this* account in the message, and hovering one
// lifts that colour rather than swapping it for the ordinary hover — the wash has
// to survive being read past. A channel-wide ping names nobody at all and still
// addresses the reader; logged out there is no reader to address, which is the
// case a bare slices.Contains would get wrong.
func TestMentionHighlight(t *testing.T) {
	deps := styledApp(t)
	self := "01TESTSELF0000000000000000"

	mentioned := testMessage("01TESTMESSAGE0000000000M0", "hello")
	mentioned.Mentions = []string{"01TESTOTHER000000000000000", self}
	plain := testMessage("01TESTMESSAGE0000000000M1", "hello")
	everyone := testMessage("01TESTMESSAGE0000000000M2", "hello")
	everyone.MentionsEveryone = true

	cases := []struct {
		name    string
		selfID  string
		message *domain.Message
		rest    color.Color
		hovered color.Color
	}{
		{"named", self, mentioned, theme.Colors.MessageMentionBackground, theme.Colors.MessageMentionHoverBackground},
		{"not named", self, plain, color.Transparent, theme.Colors.MessageHoverBackground},
		{"the whole channel", self, everyone, theme.Colors.MessageMentionBackground, theme.Colors.MessageMentionHoverBackground},
		{"logged out", "", mentioned, color.Transparent, theme.Colors.MessageHoverBackground},
		{"logged out, whole channel", "", everyone, color.Transparent, theme.Colors.MessageHoverBackground},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps.Store = &fakeStore{self: domain.User{ID: c.selfID}}
			w := NewMessageWidget(deps, c.message, "", false, false)

			if got := w.background.FillColor; got != c.rest {
				t.Errorf("resting fill = %v, want %v", got, c.rest)
			}
			w.setHighlighted(true)
			if got := w.background.FillColor; got != c.hovered {
				t.Errorf("hovered fill = %v, want %v", got, c.hovered)
			}
		})
	}
}

/* Tapping a mention */

// tapRecorder answers the two navigations a mention can make, recording what it
// was asked to open.
type tapRecorder struct {
	stubActions
	user    string
	channel string
}

func (r *tapRecorder) OnUserTapped(userID string, _ fyne.CanvasObject) { r.user = userID }
func (r *tapRecorder) OnChannelTapped(channelID string)                { r.channel = channelID }

// mentionsIn collects the rendered mentions of a mounted row, in reading order.
func mentionsIn(w *MessageWidget) []*mentionText {
	w.Resize(fyne.NewSize(600, w.MinSize().Height))

	var found []*mentionText
	walkTree(w, func(obj fyne.CanvasObject, _ fyne.Position) {
		if m, ok := obj.(*mentionText); ok {
			found = append(found, m)
		}
	})

	return found
}

// TestMentionsOpenWhatTheyName covers the wiring a rendered mention stands on:
// the two markers reach two different actions, each carrying the ID of the one it
// names — a body holding several is a loop, and the obvious way to write it hands
// every mention the last ID. It also pins the right-click: the driver gives a
// click to the innermost object accepting one and does not walk back up, so a
// mention that did not carry the message's own menu would be a hole in it.
func TestMentionsOpenWhatTheyName(t *testing.T) {
	deps := styledApp(t)
	recorder := &tapRecorder{}
	deps.Actions = recorder

	const userID, channelID = "01ELYNN", "01GENERAL"
	deps.Store = &fakeStore{
		users:    map[string]domain.User{userID: {ID: userID, Name: "Elynn"}},
		channels: map[string]domain.Channel{channelID: {ID: channelID, Name: "general"}},
	}

	message := testMessage("01TESTMESSAGE0000000000M0", "ask <@"+userID+"> in <#"+channelID+">")
	mentions := mentionsIn(NewMessageWidget(deps, message, "", false, false))
	if len(mentions) != 2 {
		t.Fatalf("the body drew %d mentions, want 2", len(mentions))
	}

	for _, m := range mentions {
		if m.onMenu == nil {
			t.Errorf("mention %q answers no right-click", m.textObj.Text)
		}
	}

	mentions[0].Tapped(&fyne.PointEvent{})
	if recorder.user != userID {
		t.Errorf("tapping %q opened user %q, want %q", mentions[0].textObj.Text, recorder.user, userID)
	}

	mentions[1].Tapped(&fyne.PointEvent{})
	if recorder.channel != channelID {
		t.Errorf("tapping %q opened channel %q, want %q", mentions[1].textObj.Text, recorder.channel, channelID)
	}
}

// TestSystemLineNamesAreMentions covers the one thing in a system line that
// answers a pointer. The name is a mention of whoever the event is about — the
// row has no author for the reader to click instead — and an event about the
// channel names nobody, so there is nothing there to tap.
func TestSystemLineNamesAreMentions(t *testing.T) {
	deps := styledApp(t)
	recorder := &tapRecorder{}
	deps.Actions = recorder

	const target = "01ELYNN"
	deps.Store = &fakeStore{users: map[string]domain.User{target: {ID: target, Name: "Elynn"}}}

	joined := testMessage("01TESTMESSAGE0000000000S0", "")
	joined.System = &domain.SystemMessage{Kind: domain.SystemUserJoined, Target: target}

	mentions := mentionsIn(NewMessageWidget(deps, joined, "", false, false))
	if len(mentions) != 1 {
		t.Fatalf("the system line drew %d mentions, want 1", len(mentions))
	}
	if got := mentions[0].textObj.Text; got != "Elynn" {
		t.Errorf("the line names %q, want Elynn", got)
	}

	mentions[0].Tapped(&fyne.PointEvent{})
	if recorder.user != target {
		t.Errorf("tapping the name opened %q, want %q", recorder.user, target)
	}

	renamed := testMessage("01TESTMESSAGE0000000000S1", "")
	renamed.System = &domain.SystemMessage{Kind: domain.SystemChannelRenamed}
	if mentions := mentionsIn(NewMessageWidget(deps, renamed, "", false, false)); len(mentions) != 0 {
		t.Errorf("an event about the channel drew %d mentions, want none", len(mentions))
	}
}
