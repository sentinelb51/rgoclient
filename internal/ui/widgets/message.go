package widgets

import (
	"RGOClient/internal/util"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"

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
		timestamp = util.NiceTime(t)
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
