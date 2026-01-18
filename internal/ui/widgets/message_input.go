package widgets

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// MessageInput input constants.
const (
	messageInputMaxLines = 8
)

// Compile-time interface assertion.
var _ desktop.Keyable = (*MessageInput)(nil)

// MessageInput is a custom Entry widget that supports shift-enter for newlines.
type MessageInput struct {
	widget.Entry
	OnSubmit     func(string)
	shiftPressed bool
}

// NewMessageInput creates a new MessageInput widget.
func NewMessageInput() *MessageInput {
	m := &MessageInput{}
	m.ExtendBaseWidget(m)
	m.MultiLine = true
	m.Wrapping = fyne.TextWrapWord
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
	m.Entry.TypedShortcut(s)
	m.Refresh()
}
