package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// avatarIn returns the avatar circle drawn in a laid-out row, with its centre,
// or nil when the row draws none. The placeholder circle is the avatar until an
// image lands over it, so it is what the row's geometry can be measured by.
func avatarIn(row fyne.CanvasObject) (*canvas.Circle, fyne.Position) {
	var found *canvas.Circle
	var centre fyne.Position
	walkTree(row, func(obj fyne.CanvasObject, pos fyne.Position) {
		if circle, ok := obj.(*canvas.Circle); ok && found == nil {
			found = circle
			centre = pos.Add(fyne.NewPos(circle.Size().Width/2, circle.Size().Height/2))
		}
	})

	return found, centre
}

// TestConversationRowIsAnAvatarCard covers the home view's rows: a conversation
// is a taller card led by a centred avatar, where a server channel stays a short
// row led by its type glyph. Deps are empty on purpose — a nil session leaves the
// avatar URL empty, so the row builds and lays out without touching the network.
func TestConversationRowIsAnAvatarCard(t *testing.T) {
	test.NewTempApp(t)

	t.Run("direct message", func(t *testing.T) {
		row := NewChannelWidget(testDeps(), domain.Channel{ID: "01DM", Kind: domain.ChannelDM}, nil)
		row.Resize(row.MinSize())

		if got, want := row.MinSize().Height, theme.Sizes.ConversationItemHeight; got != want {
			t.Errorf("conversation row is %vpx tall, want %v", got, want)
		}

		avatar, centre := avatarIn(row)
		if avatar == nil {
			t.Fatal("the conversation row drew no avatar")
		}
		if got, want := avatar.Size().Width, theme.Sizes.ConversationAvatarSize; got != want {
			t.Errorf("avatar is %vpx wide, want %v", got, want)
		}
		if want := row.Size().Height / 2; centre.Y != want {
			t.Errorf("avatar centred at y=%v in a %vpx row, want %v", centre.Y, row.Size().Height, want)
		}
	})

	t.Run("server channel", func(t *testing.T) {
		row := NewChannelWidget(testDeps(), domain.Channel{ID: "01TEXT", Kind: domain.ChannelText}, nil)
		row.Resize(row.MinSize())

		if got, want := row.MinSize().Height, theme.Sizes.ChannelItemHeight; got != want {
			t.Errorf("channel row is %vpx tall, want %v", got, want)
		}
		if avatar, _ := avatarIn(row); avatar != nil {
			t.Error("a server channel row drew an avatar instead of its glyph")
		}
	})
}
