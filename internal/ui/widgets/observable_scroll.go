package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
)

// ObservableScroll is a scroll container that notifies on scroll events.
type ObservableScroll struct {
	container.Scroll
	OnScroll func(offset fyne.Position)
	panning  bool
}

// NewObservableVScroll creates a vertical scroll container that can be observed.
func NewObservableVScroll(content fyne.CanvasObject) *ObservableScroll {
	s := &ObservableScroll{}
	s.Direction = container.ScrollVerticalOnly
	s.Content = content
	s.ExtendBaseWidget(s)
	return s
}

// Scrolled overrides the default scroll handling to notify listeners.
func (s *ObservableScroll) Scrolled(ev *fyne.ScrollEvent) {
	// Amplify scroll to simulate "message-by-message" scrolling (approx 4x default)
	newEv := *ev
	newEv.Scrolled.DY *= 4
	newEv.Scrolled.DX *= 4
	s.Scroll.Scrolled(&newEv)
	if s.OnScroll != nil {
		s.OnScroll(s.Offset)
	}
}

// MouseDown handles mouse button press events.
func (s *ObservableScroll) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		s.panning = true
	}
}

// MouseUp handles mouse button release events.
func (s *ObservableScroll) MouseUp(ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonTertiary {
		s.panning = false
	}
}

// Dragged handles drag events for panning.
func (s *ObservableScroll) Dragged(ev *fyne.DragEvent) {
	if s.panning {
		// Pan the view: Drag UP -> View DOWN (Offset increases)
		s.Offset.Y -= ev.Dragged.DY
		s.Offset.X -= ev.Dragged.DX
		s.Refresh()
		if s.OnScroll != nil {
			s.OnScroll(s.Offset)
		}
	}
	// If not panning, we do nothing, ignoring other drags (like selection drags which are handled by children usually).
	// container.Scroll does not publicly expose Dragged, so we can't delegate.
}
