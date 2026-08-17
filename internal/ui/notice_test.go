package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/config"
)

// walk visits obj and everything it contains, descending through both plain
// containers and the objects a widget's renderer mounts.
func walk(obj fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	visit(obj)

	switch o := obj.(type) {
	case *fyne.Container:
		for _, child := range o.Objects {
			walk(child, visit)
		}
	case fyne.Widget:
		for _, child := range test.WidgetRenderer(o).Objects() {
			walk(child, visit)
		}
	}
}

// buttonNamed finds the button labelled text somewhere inside root.
func buttonNamed(t *testing.T, root fyne.CanvasObject, text string) *Button {
	t.Helper()

	var found *Button
	walk(root, func(obj fyne.CanvasObject) {
		if button, ok := obj.(*Button); ok && button.Text() == text {
			found = button
		}
	})

	if found == nil {
		t.Fatalf("no %q button in the dialog", text)
	}

	return found
}

// TestConfirmDialogActs covers the contract the destructive menu items rely on:
// the action runs exactly once and only when confirmed, the dialog closes
// whichever way it is answered, and the tone decides how the action is painted.
func TestConfirmDialogActs(t *testing.T) {
	test.NewTempApp(t)

	newDialog := func(confirmed, closed *int) fyne.CanvasObject {
		return NewConfirmDialog(Confirm{
			Title:     "Leave server",
			Body:      "You'll need a new invite to come back.",
			Action:    "Leave",
			Tone:      ToneDanger,
			OnConfirm: func() { *confirmed++ },
		}, func() { *closed++ })
	}

	t.Run("confirming acts and closes", func(t *testing.T) {
		var confirmed, closed int
		dialog := newDialog(&confirmed, &closed)
		win := test.NewWindow(dialog)
		t.Cleanup(win.Close)

		action := buttonNamed(t, dialog, "Leave")
		if action.weight != ButtonDanger {
			t.Errorf("a danger confirmation paints its action %v, want ButtonDanger", action.weight)
		}

		test.Tap(action)
		if confirmed != 1 {
			t.Errorf("the action ran %d times, want once", confirmed)
		}
		if closed != 1 {
			t.Errorf("the dialog closed %d times, want once", closed)
		}
	})

	t.Run("cancelling closes without acting", func(t *testing.T) {
		var confirmed, closed int
		dialog := newDialog(&confirmed, &closed)
		win := test.NewWindow(dialog)
		t.Cleanup(win.Close)

		test.Tap(buttonNamed(t, dialog, "Cancel"))
		if confirmed != 0 {
			t.Error("cancelling ran the action anyway")
		}
		if closed != 1 {
			t.Errorf("the dialog closed %d times, want once", closed)
		}
	})
}

// TestNoticeStackBounded covers the two ways a notice goes away other than its
// own timer: pushed off the bottom by newer ones, and clicked.
func TestNoticeStackBounded(t *testing.T) {
	test.NewTempApp(t)

	stack := NewNoticeStack()
	win := test.NewWindow(stack.Layer)
	t.Cleanup(win.Close)

	stack.Push(ToneInfo, "")
	if len(stack.list.Objects) != 0 {
		t.Fatal("an empty notice was posted")
	}

	cap := config.Current().Notifications.MaxStacked
	for range cap + 2 {
		stack.Push(ToneDanger, "something went wrong")
	}
	if got := len(stack.list.Objects); got != cap {
		t.Fatalf("%d notices are up, want the cap of %d", got, cap)
	}

	// Tapping any part of a card dismisses it.
	card, ok := stack.list.Objects[len(stack.list.Objects)-1].(*noticeCard)
	if !ok {
		t.Fatal("a notice card is not tappable, so it cannot be dismissed")
	}

	card.Tapped(&fyne.PointEvent{})
	if got := len(stack.list.Objects); got != cap-1 {
		t.Fatalf("%d notices are up after dismissing one, want %d", got, cap-1)
	}

	stack.Clear()
	if len(stack.list.Objects) != 0 {
		t.Fatal("Clear left notices up")
	}
}
