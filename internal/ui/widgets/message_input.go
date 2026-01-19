package widgets

import (
	"fmt"
	"image/color"
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

	"golang.design/x/clipboard"
)

// MessageInput input constants.
const (
	messageInputMaxLines = 8
)

// Compile-time interface assertion.
var _ desktop.Keyable = (*MessageInput)(nil)

// Attachment represents a file attached to the message.
type Attachment struct {
	Path string
	Name string
}

// MessageInput is a custom Entry widget that supports shift-enter for newlines.
type MessageInput struct {
	widget.Entry
	OnSubmit            func(string)
	shiftPressed        bool
	Attachments         []Attachment
	AttachmentContainer *fyne.Container
}

// NewMessageInput creates a new MessageInput widget.
func NewMessageInput() *MessageInput {
	m := &MessageInput{}
	m.ExtendBaseWidget(m)
	m.MultiLine = true
	m.Wrapping = fyne.TextWrapWord
	m.AttachmentContainer = container.NewHBox()
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

func (m *MessageInput) rebuildAttachmentUI() {
	m.AttachmentContainer.Objects = nil
	for _, a := range m.Attachments {
		path := a.Path // Capture
		name := a.Name
		label := widget.NewLabel(name)

		xBtn := NewXButton(func() {
			m.RemoveAttachment(path)
		})

		content := container.NewHBox(label, xBtn)
		padded := container.NewPadded(content)

		// Create a grey outline rectangle
		outline := canvas.NewRectangle(color.Transparent)
		outline.StrokeColor = theme.DisabledButtonColor()
		outline.StrokeWidth = 1

		stack := container.NewStack(outline, padded)
		m.AttachmentContainer.Add(stack)
	}
	m.AttachmentContainer.Refresh()
	m.Refresh() // Trigger layout update
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
	if lines > messageInputMaxLines {
		lines = messageInputMaxLines
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
