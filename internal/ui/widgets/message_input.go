package widgets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sentinelb51/revoltgo"
	"golang.design/x/clipboard"

	"RGOClient/internal/cache"
	appTheme "RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// Constants for message input configuration and layout.
const (
	maxMessageInputLines  = 8
	maxReplyCount         = 5
	maxReplyPreviewLength = 60
	truncateIndicator     = "..."

	attBarHeight         = float32(28)
	attNameTextSize      = float32(12)
	attSizeTextSize      = float32(12)
	attPreviewWidth      = float32(200)
	attPreviewImgHeight  = float32(150)
	attPreviewFileHeight = float32(64)
	attSpacerSize        = float32(8)
)

// Compile-time interface assertion.
var _ desktop.Keyable = (*MessageInput)(nil)

// Attachment represents a file attached to the message.
type Attachment struct {
	Path string
	Name string
}

// Reply represents a message being replied to.
type Reply struct {
	ID      string
	Author  string
	Content string
	Avatar  string
	Mention bool
}

// MessageInput is a custom Entry widget that supports shift-enter for newlines.
type MessageInput struct {
	widget.Entry
	OnSubmit            func(string)
	shiftPressed        bool
	Attachments         []Attachment
	AttachmentContainer *fyne.Container

	Replies        []Reply
	ReplyContainer *fyne.Container
}

// NewMessageInput creates a new MessageInput widget.
func NewMessageInput() *MessageInput {
	m := &MessageInput{}
	m.ExtendBaseWidget(m)
	m.MultiLine = true
	m.Wrapping = fyne.TextWrapWord
	m.AttachmentContainer = container.NewHBox()
	m.ReplyContainer = container.NewVBox()
	m.Replies = []Reply{}
	return m
}

// AddAttachment adds a file to the attachment list and updates the UI.
func (m *MessageInput) AddAttachment(path string) {
	name := filepath.Base(path)
	att := Attachment{Path: path, Name: name}
	m.Attachments = append(m.Attachments, att)
	m.rebuildAttachmentUI()
}

// RemoveAttachment removes a file from the attachment list.
func (m *MessageInput) RemoveAttachment(path string) {
	for i, a := range m.Attachments {
		if a.Path == path {
			// Remove from slice
			m.Attachments = append(m.Attachments[:i], m.Attachments[i+1:]...)
			m.rebuildAttachmentUI()
			return
		}
	}
}

// AddReply adds a message to the reply list.
func (m *MessageInput) AddReply(msg *revoltgo.Message, authorName string, avatarURL string) {
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
		ID:      msg.ID,
		Author:  authorName,
		Content: msg.Content,
		Avatar:  avatarURL,
		Mention: false, // Default false
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
	m.Replies = []Reply{}
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
	m.Refresh() // Trigger layout update
}

// mentionToggleButton previews toggled state on hover.
// Rendered state = active XOR hovered.
// Hover when off  => highlight
// Hover when on   => unhighlight.
//
// Note: Positioning uses layout offsets; Move() gets overridden by container layouts.
type mentionToggleButton struct {
	widget.BaseWidget
	active  bool
	hovered bool
	onTap   func()

	bg   *canvas.Rectangle
	text *canvas.Text

	content *fyne.Container
}

var _ fyne.Widget = (*mentionToggleButton)(nil)
var _ fyne.Tappable = (*mentionToggleButton)(nil)
var _ desktop.Hoverable = (*mentionToggleButton)(nil)

func newMentionToggleButton(active bool, onTap func()) *mentionToggleButton {
	btnSize := fyne.NewSize(20, 20)

	bg := canvas.NewRectangle(appTheme.Colors.SwiftActionBg)
	bg.SetMinSize(btnSize)

	text := canvas.NewText("@", appTheme.Colors.TimestampText)
	text.TextSize = 20
	text.TextStyle = fyne.TextStyle{Bold: true}

	// Offset glyph; Move() gets overridden by layouts.
	textOffset := container.New(&SwiftActionsLayout{YOffset: -15, RightOffset: 0}, text)
	centeredText := container.NewCenter(textOffset)

	content := container.NewStack(bg, centeredText)
	content.Resize(btnSize)

	b := &mentionToggleButton{
		active:  active,
		onTap:   onTap,
		bg:      bg,
		text:    text,
		content: content,
	}
	b.ExtendBaseWidget(b)
	b.applyState()
	return b
}

func (b *mentionToggleButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

func (b *mentionToggleButton) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *mentionToggleButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.applyState()
}

func (b *mentionToggleButton) MouseOut() {
	b.hovered = false
	b.applyState()
}

func (b *mentionToggleButton) MouseMoved(*desktop.MouseEvent) {}

func (b *mentionToggleButton) SetActive(active bool) {
	b.active = active
	b.applyState()
}

func (b *mentionToggleButton) applyState() {
	if b.active != b.hovered {
		b.text.Color = appTheme.Colors.TextPrimary
	} else {
		b.text.Color = appTheme.Colors.TimestampText
	}
	b.text.Refresh()
}

// buildReplyCard creates the UI for a single reply.
func (m *MessageInput) buildReplyCard(r *Reply) fyne.CanvasObject {
	bg := canvas.NewRectangle(appTheme.Colors.SwiftActionBg)
	bg.CornerRadius = 8

	avatarSize := fyne.NewSize(22, 22)
	placeholder := canvas.NewCircle(appTheme.Colors.ServerDefaultBg)
	avatarContainer := container.NewGridWrap(avatarSize, placeholder)

	if r.Avatar != "" {
		avatarID := util.IDFromAttachmentURL(r.Avatar)
		if avatarID == "" {
			avatarID = r.Avatar
		}
		cache.GetImageCache().LoadImageToContainer(avatarID, r.Avatar, avatarSize, avatarContainer, true, nil)
	}

	centeredAvatar := container.NewCenter(avatarContainer)

	content := r.Content
	if len(content) > maxReplyPreviewLength {
		content = content[:maxReplyPreviewLength-len(truncateIndicator)] + truncateIndicator
	}

	usernameLabel := canvas.NewText(r.Author, appTheme.Colors.TextPrimary)
	usernameLabel.TextSize = 14
	usernameLabel.TextStyle = fyne.TextStyle{Bold: true}

	contentLabel := canvas.NewText(content, appTheme.Colors.TimestampText)
	contentLabel.TextSize = 14

	textContainer := NewHorizontalNoSpacingContainer(
		usernameLabel,
		newWidthSpacer(10),
		contentLabel,
	)

	var mentionBtn *mentionToggleButton
	mentionBtn = newMentionToggleButton(r.Mention, func() {
		r.Mention = !r.Mention
		mentionBtn.SetActive(r.Mention)
		m.ReplyContainer.Refresh()
	})

	closeBtn := NewXButton(func() {
		m.RemoveReply(r.ID)
	})

	rightControls := container.NewHBox(mentionBtn, closeBtn)

	leftContent := NewHorizontalNoSpacingContainer(
		newWidthSpacer(12),
		centeredAvatar,
		newWidthSpacer(4),
		textContainer,
	)

	layoutContent := container.NewBorder(
		nil, nil,
		leftContent,
		rightControls,
	)

	layoutContentPadded := container.NewBorder(
		newHeightSpacer(2), newHeightSpacer(2),
		newWidthSpacer(4), newWidthSpacer(4),
		layoutContent,
	)
	return container.NewStack(bg, layoutContentPadded)
}

func (m *MessageInput) createAttachmentMetadataBar(name string, size int, onRemove func()) fyne.CanvasObject {
	barBg := canvas.NewRectangle(appTheme.Colors.SwiftActionBg)
	barBg.SetMinSize(fyne.NewSize(0, attBarHeight))

	nameLabel := canvas.NewText(name, appTheme.Colors.TextPrimary)
	nameLabel.TextSize = attNameTextSize
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	nameLabel.Alignment = fyne.TextAlignLeading

	sizeLabel := canvas.NewText(formatFileSize(size), appTheme.Colors.TimestampText)
	sizeLabel.TextSize = attSizeTextSize
	sizeLabel.Alignment = fyne.TextAlignTrailing

	closeBtn := NewXButton(onRemove)

	barContent := container.NewBorder(nil, nil,
		container.NewHBox(newWidthSpacer(attSpacerSize), nameLabel),
		container.NewHBox(sizeLabel, container.NewPadded(closeBtn), newWidthSpacer(attSpacerSize)),
	)

	return container.NewStack(barBg, barContent)
}

func (m *MessageInput) createAttachmentPreview(path string) fyne.CanvasObject {

	if util.Filetype(path) == util.FileTypeImage {
		return m.createImagePreview(path)
	}

	return m.createGenericPreview()
}

func (m *MessageInput) createImagePreview(path string) fyne.CanvasObject {
	img := canvas.NewImageFromFile(path)
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScaleFastest

	img.SetMinSize(fyne.NewSize(attPreviewWidth, attPreviewImgHeight))
	return img
}

func (m *MessageInput) createGenericPreview() fyne.CanvasObject {
	placeholder := canvas.NewRectangle(appTheme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(fyne.NewSize(attPreviewWidth, attPreviewFileHeight))

	return placeholder
}

// ClearAttachments clears all attachments.
func (m *MessageInput) ClearAttachments() {
	m.Attachments = []Attachment{}
	m.AttachmentContainer.Objects = nil
	m.AttachmentContainer.Refresh()
}

func (m *MessageInput) lineHeight() float32 {
	return fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{}).Height
}

func (m *MessageInput) currentLineCount() int {
	if m.Text == "" {
		return 1
	}
	return strings.Count(m.Text, "\n") + 1
}

// MinSize returns one line by default, grows up to max lines.
func (m *MessageInput) MinSize() fyne.Size {
	s := m.Entry.MinSize()

	lines := m.currentLineCount()
	if lines < 1 {
		lines = 1
	}

	if lines > maxMessageInputLines {
		lines = maxMessageInputLines
	}

	lh := m.lineHeight()
	pad := theme.InnerPadding()
	s.Height = lh*float32(lines) + pad*2
	return s
}

// FocusLost resets the shift state when focus is lost.
func (m *MessageInput) FocusLost() {
	m.shiftPressed = false
	m.Entry.FocusLost()
}

// KeyDown captures modifier state for desktop keyboards.
func (m *MessageInput) KeyDown(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		m.shiftPressed = true
	}
}

// KeyUp captures modifier state for desktop keyboards.
func (m *MessageInput) KeyUp(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		m.shiftPressed = false
	}
}

// TypedKey handles key events for the MessageInput.
func (m *MessageInput) TypedKey(key *fyne.KeyEvent) {
	// Force size recalculation for deletion keys
	if key.Name == fyne.KeyBackspace || key.Name == fyne.KeyDelete {
		m.Entry.TypedKey(key)
		m.Refresh()
		return
	}

	if key.Name != fyne.KeyReturn && key.Name != fyne.KeyEnter {
		m.Entry.TypedKey(key)
		return
	}

	if m.shiftPressed {
		m.TypedRune('\n')
		m.Refresh()
		return
	}

	if m.OnSubmit != nil {
		m.OnSubmit(m.Text)
	}
	m.Refresh()
}

// TypedRune ensures size recalculation after edits.
func (m *MessageInput) TypedRune(r rune) {
	m.Entry.TypedRune(r)
	m.Refresh()
}

// TypedShortcut ensures size recalculation after paste/cut.
func (m *MessageInput) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutPaste); ok {
		// Try to read image from clipboard first
		err := clipboard.Init()
		if err == nil {
			imageBytes := clipboard.Read(clipboard.FmtImage)
			if len(imageBytes) > 0 {
				// Save to temporary file
				tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("%d.png", time.Now().UnixNano()))
				if err := os.WriteFile(tmpFile, imageBytes, 0644); err == nil {
					m.AddAttachment(tmpFile)
					m.Refresh()
					return
				}
			}
		}

		// Fallback to text content (file paths or text)
		cb := fyne.CurrentApp().Clipboard()
		content := cb.Content()

		if content != "" {
			// Check if it is a file path
			if _, err := os.Stat(content); err == nil {
				m.AddAttachment(content)
				m.Refresh()
				return
			}
		}
	}

	m.Entry.TypedShortcut(s)
	m.Refresh()
}

// rebuildAttachmentUI rebuilds the attachment UI.
func (m *MessageInput) rebuildAttachmentUI() {
	m.AttachmentContainer.Objects = nil
	for _, att := range m.Attachments {

		// File size
		size := 0
		if info, err := os.Stat(att.Path); err == nil {
			size = int(info.Size())
		}

		capturedPath := att.Path
		onRemove := func() {
			m.RemoveAttachment(capturedPath)
		}

		preview := m.createAttachmentPreview(att.Path)
		bar := m.createAttachmentMetadataBar(att.Name, size, onRemove)

		main := container.NewBorder(nil, bar, nil, nil, preview)

		bg := canvas.NewRectangle(appTheme.Colors.ServerDefaultBg)
		bg.CornerRadius = 8
		card := container.NewStack(bg, container.NewPadded(main))

		m.AttachmentContainer.Add(container.NewPadded(card))
	}
	m.AttachmentContainer.Refresh()
	m.Refresh()
}
