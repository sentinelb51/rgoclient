package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// stubActions satisfies MessageActions without doing anything. Deps is always
// fully populated in the app, so tests populate it too rather than relying on
// widgets tolerating a nil interface.
type stubActions struct{}

func (stubActions) OnUserTapped(string, fyne.CanvasObject)     {}
func (stubActions) OnChannelTapped(string)                     {}
func (stubActions) OnServerTapped(string)                      {}
func (stubActions) OnWatchShare(string, string)                {}
func (stubActions) OnJoinInvite(string)                        {}
func (stubActions) OnAttachmentTapped(*domain.File)            {}
func (stubActions) OnLinkTapped(_, _ string)                   {}
func (stubActions) OnReply(*domain.Message)                    {}
func (stubActions) OnEdit(*domain.Message)                     {}
func (stubActions) OnDelete(*domain.Message)                   {}
func (stubActions) OnPin(*domain.Message, bool)                {}
func (stubActions) OnReact(*domain.Message, string, bool)      {}
func (stubActions) OnClearReactions(*domain.Message)           {}
func (stubActions) OnSelectMessages(*domain.Message)           {}
func (stubActions) OnToggleSelected(*domain.Message, bool)     {}
func (stubActions) ResolveMessage(_, _ string) *domain.Message { return nil }
func (stubActions) OnJumpToMessage(_, _ string)                {}
func (stubActions) OnAttachFile(func(string))                  {}

// OnPickEmoji never opens anything: the picker is a pop-up on a canvas, which a
// widget test has no business raising. OnPickGIF is the same picker's neighbour,
// and would additionally put a request out.
func (stubActions) OnPickEmoji(fyne.CanvasObject, []string, func(EmojiChoice)) {}
func (stubActions) OnPickGIF(fyne.CanvasObject, func(string))                  {}

// ResolveInvite never answers, which leaves an invite card in the state it
// mounts in — the state every test that isn't about one wants it to stay in.
func (stubActions) ResolveInvite(string, func(domain.Invite, error)) {}

// The video actions do nothing, which leaves a video card resting on its
// placeholder — no poster resolves and no subprocess is anywhere near a test.
func (stubActions) OnVideoMounted(*domain.File, *VideoCard)       {}
func (stubActions) OnVideoTapped(*domain.File, *VideoCard)        {}
func (stubActions) OnVideoSeek(*domain.File, *VideoCard, float64) {}
func (stubActions) OnVideoMuted(*domain.File, *VideoCard, bool)   {}
func (stubActions) OnVideoOpen(*domain.File, *VideoCard)          {}

// TestAttachmentViewerFits checks that every attachment kind builds a card that
// stays inside the bounds it was given — the modal is centred at its MinSize, so
// a card that over-reports would push its own chrome off screen.
func TestAttachmentViewerFits(t *testing.T) {
	bounds := fyne.NewSize(800, 600)

	cases := []struct {
		name string
		file *domain.File
	}{
		{"image", &domain.File{
			ID:   "img",
			Name: "shot.png",
			Size: 2048,
			Kind: domain.FileImage, Width: 1920, Height: 1080,
		}},
		{"oversized image", &domain.File{
			ID:   "big",
			Name: "huge.png",
			Kind: domain.FileImage, Width: 6000, Height: 200,
		}},
		{"image without metadata", &domain.File{ID: "raw", Name: "mystery.png", Kind: domain.FileImage}},
		{"text", &domain.File{ID: "txt", Name: "notes.txt", Kind: domain.FileText, Size: 40}},
		{"unsupported", &domain.File{ID: "bin", Name: "archive.zip", Kind: domain.FileArchive, Size: 90}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := NewAttachmentViewer(testDeps(), tc.file, bounds, func() {}).Content

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

	card := NewProfileCard(testDeps(), domain.Profile{Name: "Someone"}, ProfileActions{})
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
