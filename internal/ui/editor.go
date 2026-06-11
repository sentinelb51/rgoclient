package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var _ desktop.Keyable = (*EditEntry)(nil)

// EditEntry is the multi-line entry used for in-place message editing: Enter
// saves, Shift+Enter inserts a newline, and Escape cancels. It grows with its
// content like the main composer.
type EditEntry struct {
	widget.Entry
	OnSave   func()
	OnCancel func()

	shiftPressed bool
	cursorPlaced bool
}

// NewEditEntry creates an edit entry pre-filled with the message's current
// content, with the cursor placed at the end on the first layout.
func NewEditEntry(content string) *EditEntry {
	e := &EditEntry{}
	e.ExtendBaseWidget(e)
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.SetText(content)
	return e
}

// MinSize grows the entry up to maxInputLines as the user types, matching the
// main composer's behaviour.
func (e *EditEntry) MinSize() fyne.Size {
	return composerMinSize(&e.Entry)
}

// Resize places the caret at the end of the text the first time the entry gets
// a real (non-zero) size. Setting the caret in the constructor positions it
// against zero-width word-wrapped row bounds (one rune per visual row), which
// clamps it a character in from the start; deferring to the first real layout
// and refreshing makes the entry recompute the caret against correct bounds.
func (e *EditEntry) Resize(size fyne.Size) {
	e.Entry.Resize(size)
	if !e.cursorPlaced && size.Width > 0 && size.Height > 0 {
		e.cursorPlaced = true
		content := e.Text
		e.CursorRow = strings.Count(content, "\n")
		e.CursorColumn = len([]rune(content[strings.LastIndexByte(content, '\n')+1:]))
		e.Refresh()
	}
}

func (e *EditEntry) FocusLost() {
	e.shiftPressed = false
	e.Entry.FocusLost()
}

func (e *EditEntry) KeyDown(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.shiftPressed = true
	}
}

func (e *EditEntry) KeyUp(key *fyne.KeyEvent) {
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.shiftPressed = false
	}
}

// TypedKey saves on Enter, inserts a newline on Shift+Enter, cancels on
// Escape, and otherwise defers to the embedded entry (refreshing so MinSize
// recomputes).
func (e *EditEntry) TypedKey(key *fyne.KeyEvent) {
	switch {
	case key.Name == fyne.KeyEscape:
		if e.OnCancel != nil {
			e.OnCancel()
		}
	case key.Name != fyne.KeyReturn && key.Name != fyne.KeyEnter:
		e.Entry.TypedKey(key)
		e.Refresh()
	case e.shiftPressed:
		e.TypedRune('\n')
	default:
		if e.OnSave != nil {
			e.OnSave()
		}
	}
}

func (e *EditEntry) TypedRune(r rune) {
	e.Entry.TypedRune(r)
	e.Refresh()
}
