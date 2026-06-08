package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
)

// scrollAmplify multiplies wheel deltas so scrolling feels message-by-message.
const scrollAmplify = 4

// ObservableScroll is a vertical scroll container that reports offset changes
// and supports middle-mouse-button panning.
type ObservableScroll struct {
	container.Scroll
	OnScroll func(offset fyne.Position)
	panning  bool
}

// NewObservableVScroll creates an observable vertical scroll container.
func NewObservableVScroll(content fyne.CanvasObject) *ObservableScroll {
	s := &ObservableScroll{}
	s.Direction = container.ScrollVerticalOnly
	s.Content = content
	s.ExtendBaseWidget(s)
	return s
}

func (s *ObservableScroll) notify() {
	if s.OnScroll != nil {
		s.OnScroll(s.Offset)
	}
}

// Scrolled amplifies the wheel delta and notifies listeners.
func (s *ObservableScroll) Scrolled(ev *fyne.ScrollEvent) {
	amplified := *ev
	amplified.Scrolled.DX *= scrollAmplify
	amplified.Scrolled.DY *= scrollAmplify
	s.Scroll.Scrolled(&amplified)
	s.notify()
}

// MouseDown begins panning on a middle-button press.
func (s *ObservableScroll) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		s.panning = true
	}
}

// MouseUp ends panning on a middle-button release.
func (s *ObservableScroll) MouseUp(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		s.panning = false
	}
}

// Dragged pans the view while the middle button is held.
func (s *ObservableScroll) Dragged(ev *fyne.DragEvent) {
	if !s.panning {
		return
	}
	s.Offset.X -= ev.Dragged.DX
	s.Offset.Y -= ev.Dragged.DY
	s.Refresh()
	s.notify()
}
