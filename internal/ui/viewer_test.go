package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/cache"
	"RGOClient/internal/ui/theme"
)

// viewerDeps is the minimum a viewer card needs: an image cache to load from.
// Actions are nil — the card only calls back through onClose.
func viewerDeps() Deps {
	return Deps{Images: cache.NewImageCache()}
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
