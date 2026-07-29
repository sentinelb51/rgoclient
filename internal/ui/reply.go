package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

const (
	maxReplyUsernameLength = 16
	maxReplyPreviewLength  = 80
	replyPreviewAvatarSize = 16
	replyPreviewTextSize   = 12
)

// buildReplyPreview renders the small quoted line shown above a message that
// replies to another.
func buildReplyPreview(deps Deps, channelID, messageID string) fyne.CanvasObject {
	author, content, avatarURL, _ := resolveReply(deps, channelID, messageID)

	size := fyne.NewSize(replyPreviewAvatarSize, replyPreviewAvatarSize)
	avatar := circularAvatar(deps.Images, avatarURL, size)

	authorLabel := canvas.NewText(author, theme.Colors.TextPrimary)
	authorLabel.TextStyle.Bold = true
	authorLabel.TextSize = replyPreviewTextSize

	contentLabel := canvas.NewText(content, theme.Colors.TimestampText)
	contentLabel.TextSize = replyPreviewTextSize

	row := HBoxNoSpacing(
		container.NewCenter(avatar),
		HorizontalSpacer(8),
		container.NewCenter(authorLabel),
		HorizontalSpacer(5),
		container.NewCenter(contentLabel),
	)
	padded := container.NewBorder(VerticalSpacer(3), VerticalSpacer(3), HorizontalSpacer(3), HorizontalSpacer(3), row)

	// TODO: navigate to the referenced message on tap.
	// Indent to the message content column so the quoted line sits directly
	// above the message body rather than under the avatar gutter.
	indent := theme.Sizes.MessageHorizontalPadding + theme.Sizes.MessageAvatarColumnWidth + theme.Sizes.MessageContentPadding
	tappable := NewTappableContainer(padded, func() {})
	return container.NewHBox(HorizontalSpacer(indent), tappable)
}

// resolveReply looks up a referenced message and returns its author, truncated
// content, avatar URL, and the author's role colour (nil when none). Missing
// references yield a placeholder.
func resolveReply(deps Deps, channelID, messageID string) (author, content, avatarURL string, accent color.Color) {
	if deps.Actions == nil {
		return "", "Unknown message reference", "", nil
	}
	msg := deps.Actions.ResolveMessage(channelID, messageID)
	if msg == nil {
		return "", "Unknown message reference", "", nil
	}

	a := util.MessageAuthor(deps.Session, msg)
	author = util.Truncate(a.Name, maxReplyUsernameLength)
	content = util.Truncate(msg.Content, maxReplyPreviewLength)
	return author, content, a.AvatarURL, a.Color
}

// circularAvatar builds a circular avatar of the given size, loading the image
// from avatarURL when present.
func circularAvatar(images *cache.ImageCache, avatarURL string, size fyne.Size) *fyne.Container {
	placeholder := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	avatar := container.NewGridWrap(size, placeholder)

	if avatarURL != "" {
		id := util.IDFromAttachmentURL(avatarURL)
		if id == "" {
			id = avatarURL
		}
		images.LoadIntoContainer(id, avatarURL, size, avatar, true, nil)
	}
	return avatar
}
