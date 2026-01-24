package widgets

import (
	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"
)

// MessageActions defines interactions for message elements.
type MessageActions interface {
	OnAvatarTapped(userID string)
	OnImageTapped(attachment *revoltgo.Attachment)
	OnReply(message *revoltgo.Message)
	OnDelete(messageID string)
	OnEdit(messageID string)
	ResolveMessage(channelID, messageID string) *revoltgo.Message
}

// Compile-time interface assertions.
var _ fyne.Widget = (*MessageWidget)(nil)
var _ desktop.Hoverable = (*MessageWidget)(nil)
var _ fyne.Tappable = (*swiftActionButton)(nil)
var _ desktop.Hoverable = (*swiftActionButton)(nil)

// MessageWidget displays a chat message with hover effects.
type MessageWidget struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	background *canvas.Rectangle
	actionsRow *fyne.Container

	// Hover state management to prevent flicker
	hoveringMessage bool
	hoveringAction  bool
	hideTimer       *time.Timer
}

// NewMessageWidget creates a message widget with author, content, and optional attachments.
// actions handles user interactions (avatar/image taps).
func NewMessageWidget(
	message *revoltgo.Message,
	session *revoltgo.Session,
	actions MessageActions,
) *MessageWidget {

	if session == nil {
		return nil
	}

	w := &MessageWidget{
		background: canvas.NewRectangle(color.Transparent),
	}

	var (
		displayName      = util.DisplayName(session, message)
		displayAvatarURL = util.DisplayAvatarURL(session, message)
		displayAvatarID  = util.IDFromAttachmentURL(displayAvatarURL)
	)

	// Determine content text
	content := message.Content
	if message.System != nil {
		content = util.FormatSystemMessage(session, message.System)
	}

	// Build timestamp
	var timestamp string
	if t, err := util.Timestamp(message.ID); err == nil {
		timestamp = util.NiceTime(t)
	}

	// Actions row (Hidden by default)
	// Hover callbacks to keep the widget active
	onActionHover := func(hovering bool) {
		w.hoveringAction = hovering
		w.updateHoverState()
	}

	replyBtn := newSwiftActionButton("assets/reply.svg", func() {
		if actions != nil {
			actions.OnReply(message)
		}
	}, onActionHover)

	editBtn := newSwiftActionButton("assets/edit.svg", func() {
		if actions != nil {
			actions.OnEdit(message.ID)
		}
	}, onActionHover)

	deleteBtn := newSwiftActionButton("assets/trash.svg", func() {
		if actions != nil {
			actions.OnDelete(message.ID)
		}
	}, onActionHover)

	actionsContainer := NewHorizontalNoSpacingContainer(replyBtn, editBtn, deleteBtn)

	// Rounded background for the action group
	actionsBg := canvas.NewRectangle(theme.Colors.SwiftActionBg)
	actionsBg.CornerRadius = 8                                // somewhat rounded
	actionsBg.StrokeColor = theme.Colors.ServerListBackground // Dark contrasting border
	actionsBg.StrokeWidth = 1

	actionsGroup := container.NewStack(actionsBg, actionsContainer)
	actionsGroup.Hide()
	w.actionsRow = actionsGroup

	// Build avatar column
	avatar := NewClickableAvatar(displayAvatarID, displayAvatarURL, message.Author, func() {
		if actions != nil {
			actions.OnAvatarTapped(message.Author)
		}
	})
	avatarColumn := container.New(&VerticalCenterFixedWidthLayout{Width: theme.Sizes.MessageAvatarColumnWidth}, avatar)

	// Build content widget
	contentWidget := buildMessageContent(message, displayName, timestamp, content, actions)

	// Wrap content - 0 vertical padding here as requested "Remove any spacing"
	paddedContent := container.NewBorder(nil, nil, NewHSpacer(theme.Sizes.MessageContentPadding), nil, contentWidget)

	main := container.NewBorder(nil, nil, avatarColumn, nil, paddedContent)

	// Outer padding settings - Reduced to near zero
	vPad := float32(0)
	hPad := theme.Sizes.MessageHorizontalPadding

	innerContainer := container.NewBorder(
		NewVSpacer(vPad), NewVSpacer(vPad),
		NewHSpacer(hPad), NewHSpacer(hPad),
		main,
	)

	// Overlay actions top-right with negative offset
	// Using SwiftActionsLayout from layout.go
	swiftActions := container.New(
		&SwiftActionsLayout{YOffset: -16, RightOffset: 6},
		actionsGroup,
	)

	messageRow := container.NewStack(innerContainer, swiftActions)

	var finalLayout fyne.CanvasObject
	if len(message.Replies) > 0 {
		repliesContainer := container.NewVBox()
		for _, replyID := range message.Replies {
			repliesContainer.Add(buildReplyPreview(replyID, message.Channel, session, actions))
			repliesContainer.Add(NewVSpacer(-15))
		}
		// No extra padding here to keep it close to the message
		finalLayout = container.NewVBox(repliesContainer, messageRow)
	} else {
		finalLayout = messageRow
	}

	w.content = finalLayout
	w.ExtendBaseWidget(w)
	return w
}

// CreateRenderer returns the widget renderer.
func (w *MessageWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(w.background, w.content))
}

// updateHoverState updates visibility based on hover flags with debounce.
func (w *MessageWidget) updateHoverState() {
	if w.hoveringMessage || w.hoveringAction {
		// Active state
		if w.hideTimer != nil {
			w.hideTimer.Stop()
			w.hideTimer = nil
		}
		w.background.FillColor = theme.Colors.MessageHoverBackground
		w.background.Refresh()
		if w.actionsRow != nil {
			w.actionsRow.Show()
		}
	} else {
		// Inactive state - allow grace period for moving between elements
		if w.hideTimer == nil {
			w.hideTimer = time.AfterFunc(50*time.Millisecond, func() {
				// Ensure UI updates happen on main thread
				fyne.CurrentApp().Driver().DoFromGoroutine(func() {
					// Re-check state in case it changed
					if !w.hoveringMessage && !w.hoveringAction {
						w.background.FillColor = color.Transparent
						w.background.Refresh()
						if w.actionsRow != nil {
							w.actionsRow.Hide()
						}
						w.hideTimer = nil
					}
				}, false)
			})
		}
	}
}

// MouseIn handles mouse enter.
func (w *MessageWidget) MouseIn(_ *desktop.MouseEvent) {
	w.hoveringMessage = true
	w.updateHoverState()
}

// MouseMoved handles mouse movement.
func (w *MessageWidget) MouseMoved(_ *desktop.MouseEvent) {}

// MouseOut handles mouse exit.
func (w *MessageWidget) MouseOut() {
	w.hoveringMessage = false
	w.updateHoverState()
}

func buildReplyPreview(replyID string, channelID string, session *revoltgo.Session, actions MessageActions) fyne.CanvasObject {
	var authorName, content, avatarURL string

	if actions != nil {
		msg := actions.ResolveMessage(channelID, replyID)
		if msg != nil {
			authorName = util.DisplayName(session, msg)
			content = msg.Content
			avatarURL = util.DisplayAvatarURL(session, msg)
		} else {
			content = "Unknown message reference"
		}
	} else {
		content = "Unknown message reference"
	}

	if len(content) > maxReplyPreviewLength {
		content = content[:maxReplyPreviewLength-3] + "..."
	}

	// 2. Avatar
	avatarSize := fyne.NewSize(16, 16)
	avatarPlaceholder := canvas.NewCircle(theme.Colors.ServerDefaultBg)
	avatarContainer := container.NewGridWrap(avatarSize, avatarPlaceholder)

	if avatarURL != "" {
		avatarID := util.IDFromAttachmentURL(avatarURL)
		if avatarID == "" {
			avatarID = avatarURL
		}
		cache.GetImageCache().LoadImageToContainer(avatarID, avatarURL, avatarSize, avatarContainer, true, nil)
	}

	// 3. Text
	userLabel := canvas.NewText(authorName, theme.Colors.TextPrimary)
	userLabel.TextStyle.Bold = true
	userLabel.TextSize = 12

	msgLabel := canvas.NewText(content, theme.Colors.TimestampText)
	msgLabel.TextSize = 12

	// Use Center layout for text to ensure it aligns with avatar/icon vertically
	replyRow := NewHorizontalNoSpacingContainer(
		container.NewCenter(avatarContainer),
		NewHSpacer(8),
		container.NewCenter(userLabel),
		NewHSpacer(5),
		container.NewCenter(msgLabel),
	)

	paddedRow := container.NewBorder(NewVSpacer(3), NewVSpacer(3), NewHSpacer(3), NewHSpacer(3), replyRow)

	tappableReply := NewTappableContainer(paddedRow, func() {
		// TODO: Navigate to message
	})

	return container.NewHBox(NewHSpacer(40), tappableReply)
}
