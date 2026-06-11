package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// ShowContextMenu pops a reusable menu up at pos (canvas coordinates) on the
// canvas that owns anchor. Callers assemble the *fyne.MenuItem set for their own
// context — a message, a server, a user, a channel — while the popup mechanics
// are shared here. An empty item set or an unattached anchor is a no-op.
//
// pos comes straight from a right-click event's AbsolutePosition, or from
// AnchorBelow when opening beneath an overflow button.
func ShowContextMenu(anchor fyne.CanvasObject, items []*fyne.MenuItem, pos fyne.Position) {
	if len(items) == 0 {
		return
	}
	canvas := fyne.CurrentApp().Driver().CanvasForObject(anchor)
	if canvas == nil {
		return
	}
	widget.NewPopUpMenu(fyne.NewMenu("", items...), canvas).ShowAtPosition(pos)
}

// AnchorBelow returns the canvas position directly below obj, used to drop a
// context menu beneath an overflow ("more") button rather than at the cursor.
func AnchorBelow(obj fyne.CanvasObject) fyne.Position {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
	return fyne.NewPos(pos.X, pos.Y+obj.Size().Height)
}

// copyToClipboard puts text on the system clipboard. A small wrapper so menu
// item actions read cleanly and there's one place that reaches for the app.
func copyToClipboard(text string) {
	fyne.CurrentApp().Clipboard().SetContent(text)
}
