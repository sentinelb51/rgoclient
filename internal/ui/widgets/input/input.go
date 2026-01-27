package input

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.design/x/clipboard"

	"RGOClient/internal/interfaces"
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

// MessageInput is a custom Entry widget that supports shift-enter for newlines.
type MessageInput struct {
	widget.Entry
	OnSubmit            func(string)
	shiftPressed        bool
	Actions             interfaces.MessageActions // For resolving messages
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

// RegisterDropHandler registers this MessageInput as the drop target for the given window.
func (m *MessageInput) RegisterDropHandler(window fyne.Window) {
	window.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		for _, u := range uris {
			// Most local files have file:// scheme
			if u.Scheme() == "file" {
				m.AddAttachment(u.Path())
			}
		}
	})
}
