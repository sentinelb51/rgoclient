package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// NewMemberWidget builds a member row: a small circular avatar and the member's
// display name, left-aligned. The whole row is tappable (opening the user's
// profile). Offline members (online=false) get dimmed name text.
func NewMemberWidget(deps Deps, member *revoltgo.ServerMember, online bool) fyne.CanvasObject {
	avatarURL := util.MemberAvatarURL(deps.Session, member)
	name := util.MemberName(deps.Session, member)

	textColor := theme.Colors.TextPrimary
	if !online {
		textColor = theme.Colors.CategoryText
	}
	label := canvas.NewText(name, textColor)
	label.TextSize = theme.Sizes.MessageTimestampSize + 1

	avatarSize := fyne.NewSize(theme.Sizes.MemberAvatarSize, theme.Sizes.MemberAvatarSize)
	row := container.NewHBox(
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		container.NewCenter(circularAvatar(deps.Images, avatarURL, avatarSize)),
		HorizontalSpacer(theme.Sizes.ChannelLeftPadding),
		container.NewCenter(label),
	)

	userID := member.ID.User
	content := NewMinHeightContainer(theme.Sizes.MemberRowHeight, row)
	return NewTappableContainer(content, func() {
		if deps.Actions != nil {
			deps.Actions.OnAvatarTapped(userID)
		}
	})
}

// NewMemberSection is a small, bold section header (e.g. "Online — 5") used to
// group members in the list.
func NewMemberSection(title string) fyne.CanvasObject {
	text := canvas.NewText(title, theme.Colors.CategoryText)
	text.TextStyle = fyne.TextStyle{Bold: true}
	text.TextSize = 12
	return container.NewHBox(HorizontalSpacer(theme.Sizes.ChannelLeftPadding), container.NewPadded(text))
}
