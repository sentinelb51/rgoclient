package widgets

import (
	"RGOClient/internal/util"
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// MessageActions defines interactions for message elements.
type MessageActions interface {
	OnAvatarTapped(userID string)
	OnImageTapped(attachment *revoltgo.Attachment)
	OnReply(messageID string)
	OnDelete(messageID string)
	OnEdit(messageID string)
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
		content = formatSystemMessage(session, message.System)
	}

	// Build timestamp
	var timestamp string
	if t, err := util.Timestamp(message.ID); err == nil {
		timestamp = formatMessageTimestamp(t)
	}

	// Actions row (Hidden by default)
	// Hover callbacks to keep the widget active
	onActionHover := func(hovering bool) {
		w.hoveringAction = hovering
		w.updateHoverState()
	}

	replyBtn := newSwiftActionButton("<", func() {
		if actions != nil {
			actions.OnReply(message.ID)
		}
	}, onActionHover)
	editBtn := newSwiftActionButton("E", func() {
		if actions != nil {
			actions.OnEdit(message.ID)
		}
	}, onActionHover)
	deleteBtn := newSwiftActionButton("X", func() {
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
	avatarColumn := container.New(&centeredAvatarLayout{width: theme.Sizes.MessageAvatarColumnWidth}, avatar)

	// Build content widget
	contentWidget := buildMessageContent(message, displayName, timestamp, content, actions)

	// Wrap content - 0 vertical padding here as requested "Remove any spacing"
	paddedContent := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageContentPadding), nil, contentWidget)

	main := container.NewBorder(nil, nil, avatarColumn, nil, paddedContent)

	// Outer padding settings - Reduced to near zero
	vPad := float32(0)
	hPad := theme.Sizes.MessageHorizontalPadding

	innerContainer := container.NewBorder(
		newHeightSpacer(vPad), newHeightSpacer(vPad),
		newWidthSpacer(hPad), newWidthSpacer(hPad),
		main,
	)

	// Overlay actions top-right with negative offset
	// Using TopRightOffsetLayout from layout.go
	// YOffset: -16 (upwards)
	// RightOffset: 16 (padding from right)
	topRightActions := container.New(
		&TopRightOffsetLayout{YOffset: -16, RightOffset: 16},
		actionsGroup,
	)

	finalLayout := container.NewStack(innerContainer, topRightActions)

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

// centeredAvatarLayout centers avatar vertically within available space.
type centeredAvatarLayout struct {
	width float32
}

func (l *centeredAvatarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	for _, obj := range objects {
		if obj.Visible() {
			if h := obj.MinSize().Height; h > height {
				height = h
			}
		}
	}
	return fyne.NewSize(l.width, height)
}

func (l *centeredAvatarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objects {
		if obj.Visible() {
			objSize := obj.MinSize()
			y := (size.Height - objSize.Height) / 2
			obj.Resize(objSize)
			obj.Move(fyne.NewPos(0, y))
		}
	}
}

// buildMessageContent creates the message content with username, text, and attachments.
func buildMessageContent(
	message *revoltgo.Message,
	username, timestamp, messageText string,
	actions MessageActions,
) fyne.CanvasObject {
	text := createFormattedMessage(username, messageText)

	tsText := canvas.NewText(timestamp, theme.Colors.TimestampText)
	tsText.TextSize = theme.Sizes.MessageTimestampSize

	// Overlay timestamp in top-right
	timestampOverlay := container.NewVBox(
		newHeightSpacer(theme.Sizes.MessageTimestampTopOffset),
		container.NewHBox(layout.NewSpacer(), tsText),
	)

	textWithTimestamp := container.NewStack(text, timestampOverlay)

	// Check if message has image attachments
	if message.Attachments == nil || len(message.Attachments) == 0 {
		return textWithTimestamp
	}

	images := container.NewVBox()
	firstImage := true
	for _, attachment := range message.Attachments {
		if attachment.Metadata.Type != revoltgo.AttachmentMetadataTypeImage {
			continue
		}

		size := calculateImageSize(attachment.Metadata.Width, attachment.Metadata.Height)
		placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
		placeholder.SetMinSize(size)
		imgContainer := container.NewGridWrap(size, placeholder)

		url := attachment.URL("")
		if url != "" && attachment.ID != "" {
			cache.GetImageCache().LoadImageToContainer(attachment.ID, url, size, imgContainer, false, nil)
		}

		captured := attachment
		clickable := NewClickableImage(imgContainer, size, func() {
			if actions != nil {
				actions.OnImageTapped(captured)
			}
		})

		if !firstImage {
			images.Add(newHeightSpacer(theme.Sizes.MessageAttachmentSpacing))
		}
		firstImage = false

		paddedImg := container.NewBorder(nil, nil, newWidthSpacer(theme.Sizes.MessageTextLeftPadding), nil, clickable)
		images.Add(paddedImg)
	}

	// Only add images container if we actually added images
	if firstImage {
		return textWithTimestamp
	}

	return container.NewVBox(textWithTimestamp, images)
}

// createFormattedMessage creates a RichText widget with bold username and formatted content.
func createFormattedMessage(username, message string) *widget.RichText {
	content := fmt.Sprintf("**%s**\n\n%s", username, message)
	rt := widget.NewRichTextFromMarkdown(content)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// formatMessageTimestamp formats time relative to now.
func formatMessageTimestamp(t time.Time) string {
	t = t.Local()
	now := time.Now()

	// Normalise to start of day for accurate day calculation
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	days := int(nowDate.Sub(tDate).Hours() / 24)

	// Same day (Today)
	if days == 0 {
		return fmt.Sprintf("Today, %s", t.Format("3:04 PM"))
	}

	// Previous day (Yesterday)
	if days == 1 {
		return fmt.Sprintf("Yesterday, %s", t.Format("3:04 PM"))
	}

	if days < 30 {
		return fmt.Sprintf("%d days ago, %s", days, t.Format("3:04 PM"))
	}

	if days < 365 {
		months := days / 30
		return fmt.Sprintf("%d month(s) ago", months)
	}

	years := days / 365
	return fmt.Sprintf("%d year(s) ago", years)
}

// newWidthSpacer creates a transparent spacer with the given width.
func newWidthSpacer(width float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 0))
	return spacer
}

// newHeightSpacer creates a transparent spacer with the given height.
func newHeightSpacer(height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, height))
	return spacer
}

// calculateImageSize calculates display size respecting max dimensions.
func calculateImageSize(width, height int) fyne.Size {
	maxW := theme.Sizes.MessageImageMaxWidth
	maxH := theme.Sizes.MessageImageMaxHeight

	if width == 0 || height == 0 {
		return fyne.NewSize(maxW, maxH/2)
	}

	w := float32(width)
	h := float32(height)

	if w > maxW {
		h = h * (maxW / w)
		w = maxW
	}
	if h > maxH {
		w = w * (maxH / h)
		h = maxH
	}

	return fyne.NewSize(w, h)
}

// formatSystemMessage converts system message to readable text.
func formatSystemMessage(session *revoltgo.Session, message *revoltgo.MessageSystem) string {
	switch message.Type {
	case revoltgo.MessageSystemUserAdded:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s added to group", user.Username)
	case revoltgo.MessageSystemUserRemove:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s removed from group", user.Username)
	case revoltgo.MessageSystemUserJoined:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s joined", user.Username)
	case revoltgo.MessageSystemUserLeft:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s left", user.Username)
	case revoltgo.MessageSystemUserKicked:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s was kicked", user.Username)
	case revoltgo.MessageSystemUserBanned:
		user := session.State.User(message.ID)
		return fmt.Sprintf("%s banned", user.Username)
	case revoltgo.MessageSystemChannelRenamed:
		return "Channel renamed"
	case revoltgo.MessageSystemChannelDescriptionChanged:
		return "Channel description changed"
	case revoltgo.MessageSystemChannelIconChanged:
		return "Channel icon changed"
	case revoltgo.MessageSystemChannelOwnershipChanged:
		return "Channel ownership changed"
	case revoltgo.MessageSystemMessagePinned:
		return "Message pinned"
	case revoltgo.MessageSystemMessageUnpinned:
		return "Message unpinned"
	case revoltgo.MessageSystemCallStarted:
		return "Call started"
	case revoltgo.MessageSystemText:
		return "System message"
	default:
		return "System event"
	}
}

// swiftActionButton is a simple widget for swift actions (Reply, Delete, Edit).
type swiftActionButton struct {
	widget.BaseWidget
	label   string
	onTap   func()
	onHover func(bool)
	bg      *canvas.Rectangle
	text    *canvas.Text
}

func newSwiftActionButton(label string, onTap func(), onHover func(bool)) *swiftActionButton {
	bg := canvas.NewRectangle(color.Transparent)
	// Make slightly thinner vertically (80% of width)
	height := theme.Sizes.SwiftActionSize * 0.8
	bg.SetMinSize(fyne.NewSize(theme.Sizes.SwiftActionSize, height))

	text := canvas.NewText(label, theme.Colors.SwiftActionText)
	text.Alignment = fyne.TextAlignCenter
	text.TextSize = 14

	b := &swiftActionButton{
		label:   label,
		onTap:   onTap,
		onHover: onHover,
		bg:      bg,
		text:    text,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *swiftActionButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.bg, container.NewCenter(b.text)))
}

func (b *swiftActionButton) Tapped(_ *fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *swiftActionButton) MouseIn(_ *desktop.MouseEvent) {
	b.bg.FillColor = theme.Colors.SwiftActionHoverBg
	b.bg.Refresh()
	if b.onHover != nil {
		b.onHover(true)
	}
}

func (b *swiftActionButton) MouseMoved(_ *desktop.MouseEvent) {}

func (b *swiftActionButton) MouseOut() {
	b.bg.FillColor = color.Transparent
	b.bg.Refresh()
	if b.onHover != nil {
		b.onHover(false)
	}
}
