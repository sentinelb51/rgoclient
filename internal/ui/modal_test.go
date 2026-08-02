package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// stubActions satisfies MessageActions without doing anything. Deps is always
// fully populated in the app, so tests populate it too rather than relying on
// widgets tolerating a nil interface.
type stubActions struct{}

func (stubActions) OnUserTapped(string, fyne.CanvasObject)       {}
func (stubActions) OnAttachmentTapped(*revoltgo.File)            {}
func (stubActions) OnReply(*revoltgo.Message)                    {}
func (stubActions) OnEdit(*revoltgo.Message)                     {}
func (stubActions) OnDelete(*revoltgo.Message)                   {}
func (stubActions) ResolveMessage(_, _ string) *revoltgo.Message { return nil }

// viewerDeps is what a viewer card needs: an image cache to load from, plus the
// stub actions every widget expects to be present.
func viewerDeps() Deps {
	return Deps{
		Images:  cache.NewImageCache(),
		Texts:   cache.NewTextCache(8),
		Actions: stubActions{},
	}
}

// TestAttachmentViewerFits checks that every attachment kind builds a card that
// stays inside the bounds it was given — the modal is centred at its MinSize, so
// a card that over-reports would push its own chrome off screen.
func TestAttachmentViewerFits(t *testing.T) {
	bounds := fyne.NewSize(800, 600)

	cases := []struct {
		name string
		file *revoltgo.File
	}{
		{"image", &revoltgo.File{
			ID:       "img",
			Filename: "shot.png",
			Size:     2048,
			Metadata: &revoltgo.AttachmentMetadata{Type: revoltgo.FileMetadataTypeImage, Width: 1920, Height: 1080},
		}},
		{"oversized image", &revoltgo.File{
			ID:       "big",
			Filename: "huge.png",
			Metadata: &revoltgo.AttachmentMetadata{Type: revoltgo.FileMetadataTypeImage, Width: 6000, Height: 200},
		}},
		{"image without metadata", &revoltgo.File{ID: "raw", Filename: "mystery.png"}},
		{"text", &revoltgo.File{ID: "txt", Filename: "notes.txt", Size: 40}},
		{"unsupported", &revoltgo.File{ID: "bin", Filename: "archive.zip", Size: 90}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := NewAttachmentViewer(viewerDeps(), tc.file, bounds, func() {})

			win := test.NewWindow(card)
			t.Cleanup(win.Close)

			size := card.MinSize()
			if size.Width > bounds.Width || size.Height > bounds.Height {
				t.Errorf("card min size %v exceeds bounds %v", size, bounds)
			}
			if size.Width < theme.Sizes.ViewerMinWidth {
				t.Errorf("card width %v is below the minimum %v", size.Width, theme.Sizes.ViewerMinWidth)
			}
		})
	}
}

// TestOverlayDismiss checks that a tap on the backdrop dismisses the overlay
// while a tap on the content does not — the content is wrapped in a tap sink
// precisely so clicking the image doesn't close the viewer.
func TestOverlayDismiss(t *testing.T) {
	content := newTapSink(NewMinHeightContainer(50))

	dismissed := 0
	overlay := NewOverlay(content, func() { dismissed++ })
	win := test.NewWindow(overlay)
	t.Cleanup(win.Close)

	content.Tapped(&fyne.PointEvent{})
	if dismissed != 0 {
		t.Errorf("tapping the content dismissed the overlay")
	}

	overlay.Tapped(&fyne.PointEvent{})
	if dismissed != 1 {
		t.Errorf("tapping the backdrop dismissed %d times, want 1", dismissed)
	}
}

// TestPopoverMountsBesideItsAnchor covers the anchored overlay end to end: the
// card is sized from its own minimum and placed clear of the anchor's right
// edge. The placement arithmetic itself is TestPlaceBesideStaysOnScreen's; what
// this checks is that a mounted layer measures the anchor against the same
// origin it positions within.
func TestPopoverMountsBesideItsAnchor(t *testing.T) {
	test.NewTempApp(t)

	anchor := canvas.NewRectangle(color.Transparent)
	anchor.SetMinSize(fyne.NewSize(40, 40))

	win := test.NewWindow(container.NewWithoutLayout(anchor))
	t.Cleanup(win.Close)
	win.Resize(fyne.NewSize(900, 600))
	anchor.Resize(anchor.MinSize())
	anchor.Move(fyne.NewPos(60, 300))

	card := NewProfileCard(viewerDeps(), Profile{Name: "Someone"}, ProfileActions{})
	win.Canvas().Overlays().Add(NewPopover(card.Content, anchor, func() {}))

	driver := fyne.CurrentApp().Driver()
	right := driver.AbsolutePositionForObject(anchor).X + anchor.Size().Width

	if got := driver.AbsolutePositionForObject(card.Content).X; got <= right {
		t.Errorf("card starts at x=%v, want it clear of the anchor's edge at %v", got, right)
	}
	if got := card.Content.Size(); got != card.Content.MinSize() {
		t.Errorf("card was mounted at %v, want its minimum %v", got, card.Content.MinSize())
	}
}
