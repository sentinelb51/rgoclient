package app

import (
	"fyne.io/fyne/v2"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
)

// showAttachmentViewer opens an attachment in a modal lightbox over the main
// window. A separate window was the obvious alternative, but it drags in native
// chrome that has to be recoloured per platform, opens off-centre, and outlives
// the click that opened it; the overlay is dismissed by Esc, the close button,
// or a tap on the dimmed backdrop.
func (a *App) showAttachmentViewer(attachment *revoltgo.File) {
	card := ui.NewAttachmentViewer(a.deps(), attachment, a.viewerBounds(), a.closeOverlay)
	a.showOverlay(card)
}

// viewerBounds is the largest card the viewer may build: the window minus a
// margin, capped so the modal still reads as a card on a maximised window.
func (a *App) viewerBounds() fyne.Size {
	size := a.window.Canvas().Size()
	margin := 2 * theme.Sizes.ViewerMargin

	return fyne.NewSize(
		max(min(size.Width-margin, theme.Sizes.ViewerMaxWidth), theme.Sizes.ViewerMinWidth),
		max(min(size.Height-margin, theme.Sizes.ViewerMaxHeight), theme.Sizes.ViewerMinHeight),
	)
}

// showOverlay puts content on the modal layer, replacing whatever was there.
// While it is up, Esc closes it: Fyne gives each overlay its own focus manager,
// so with nothing in the overlay focused, key events reach the canvas handler
// instead of the composer underneath. Overlay content that *does* take focus
// (the invite dialog's entry) has to handle Esc itself — Fyne routes keys to
// the focused widget and never calls this handler. Call on the UI thread.
func (a *App) showOverlay(content fyne.CanvasObject) {
	a.closeOverlay()

	canvas := a.window.Canvas()
	a.overlay = ui.NewOverlay(content, a.closeOverlay)
	canvas.Overlays().Add(a.overlay)
	canvas.SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			a.closeOverlay()
		}
	})
}

// closeOverlay dismisses the modal layer and hands the keyboard back. Safe to
// call when nothing is showing. Call on the UI thread.
func (a *App) closeOverlay() {
	if a.overlay == nil {
		return
	}
	canvas := a.window.Canvas()
	canvas.Overlays().Remove(a.overlay)
	canvas.SetOnTypedKey(nil)
	a.overlay = nil
	a.joinDialog = nil
	a.focusInput()
}
