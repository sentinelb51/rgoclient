package input

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/context"
	appTheme "RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
	"RGOClient/internal/util"
)

// Reply represents a message being replied to.
type Reply struct {
	ID        string
	ChannelID string
	Mention   bool
}

// AddReply adds a message to the reply list.
func (m *MessageInput) AddReply(msg *revoltgo.Message) {
	if len(m.Replies) >= maxReplyCount {
		return
	}

	// Check if already replying to this message
	for _, r := range m.Replies {
		if r.ID == msg.ID {
			return
		}
	}

	reply := Reply{
		ID:        msg.ID,
		ChannelID: msg.Channel,
		Mention:   false,
	}

	m.Replies = append(m.Replies, reply)
	m.rebuildReplyUI()
}

// RemoveReply removes a reply by message ID.
func (m *MessageInput) RemoveReply(messageID string) {
	for i, r := range m.Replies {
		if r.ID == messageID {
			m.Replies = append(m.Replies[:i], m.Replies[i+1:]...)
			m.rebuildReplyUI()
			return
		}
	}
}

// ClearReplies clears all replies.
func (m *MessageInput) ClearReplies() {
	m.Replies = make([]Reply, 0, maxReplyCount)
	m.rebuildReplyUI()
}

// rebuildReplyUI rebuilds the reply container.
func (m *MessageInput) rebuildReplyUI() {
	m.ReplyContainer.Objects = nil
	for i := range m.Replies {
		replyInfo := &m.Replies[i]
		card := m.buildReplyCard(replyInfo)
		m.ReplyContainer.Add(card)
	}
	m.ReplyContainer.Refresh()
	m.Refresh()
}

// buildReplyCard creates the UI for a single reply.
func (m *MessageInput) buildReplyCard(r *Reply) fyne.CanvasObject {
	bg := canvas.NewRectangle(appTheme.Colors.SwiftActionBg)
	bg.CornerRadius = 8

	// Fetch message data using global session
	var authorName, avatarURL, content string
	if m.Actions != nil {
		session := context.Session()
		if session != nil {
			msg := m.Actions.ResolveMessage(r.ChannelID, r.ID)
			if msg != nil {
				authorName = util.DisplayName(msg)
				avatarURL = util.DisplayAvatarURL(msg)
				content = msg.Content
			} else {
				authorName = "Unknown"
				content = "[Message not found]"
			}
		}
	}

	avatarSize := fyne.NewSize(22, 22)
	placeholder := canvas.NewCircle(appTheme.Colors.ServerDefaultBg)
	avatarContainer := container.NewGridWrap(avatarSize, placeholder)

	if avatarURL != "" {
		avatarID := util.IDFromAttachmentURL(avatarURL)
		if avatarID == "" {
			avatarID = avatarURL
		}
		cache.GetImageCache().LoadImageToContainer(avatarID, avatarURL, avatarSize, avatarContainer, true, nil)
	}

	centeredAvatar := container.NewCenter(avatarContainer)

	if len(content) > maxReplyPreviewLength {
		content = content[:maxReplyPreviewLength-len(truncateIndicator)] + truncateIndicator
	}

	usernameLabel := canvas.NewText(authorName, appTheme.Colors.TextPrimary)
	usernameLabel.TextSize = 14
	usernameLabel.TextStyle = fyne.TextStyle{Bold: true}

	contentLabel := canvas.NewText(content, appTheme.Colors.TimestampText)
	contentLabel.TextSize = 14

	textContainer := widgets.HBoxNoSpacing(
		usernameLabel,
		widgets.HorizontalSpacer(10),
		contentLabel,
	)

	var mentionBtn *mentionToggleButton
	mentionBtn = newMentionToggleButton(r.Mention, func() {
		r.Mention = !r.Mention
		mentionBtn.SetActive(r.Mention)
		m.ReplyContainer.Refresh()
	})

	closeBtn := widgets.NewCloseButton(func() {
		m.RemoveReply(r.ID)
	})

	rightControls := container.NewHBox(mentionBtn, closeBtn)

	leftContent := widgets.HBoxNoSpacing(
		widgets.HorizontalSpacer(12),
		centeredAvatar,
		widgets.HorizontalSpacer(4),
		textContainer,
	)

	layoutContent := container.NewBorder(
		nil, nil,
		leftContent,
		rightControls,
	)

	layoutContentPadded := container.NewBorder(
		widgets.VerticalSpacer(2), widgets.VerticalSpacer(2),
		widgets.HorizontalSpacer(4), widgets.HorizontalSpacer(4),
		layoutContent,
	)
	return container.NewStack(bg, layoutContentPadded)
}
