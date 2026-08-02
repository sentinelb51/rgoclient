package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"RGOClient/internal/ui/theme"
)

// TestRelayoutReclaimsHiddenSpace covers what the member-sidebar toggle relies
// on: hiding a column neither re-lays out its row nor repaints it, so the slot
// stays reserved until Relayout runs.
func TestRelayoutReclaimsHiddenSpace(t *testing.T) {
	test.NewTempApp(t)

	const (
		rowWidth   = 400
		sidebarMin = 100
	)

	sidebar := NewFixedWidthContainer(sidebarMin, canvas.NewRectangle(color.Transparent))
	fill := canvas.NewRectangle(color.Transparent)

	row := NewFillRow(0, fill, sidebar)
	row.Resize(fyne.NewSize(rowWidth, 50))

	if got := fill.Size().Width; got != rowWidth-sidebarMin {
		t.Fatalf("filling child took %v, want %v", got, rowWidth-sidebarMin)
	}

	sidebar.Hide()
	if got := fill.Size().Width; got != rowWidth-sidebarMin {
		t.Fatalf("hiding a child laid the row out by itself: filling child took %v", got)
	}

	Relayout(row)
	if got := fill.Size().Width; got != rowWidth {
		t.Fatalf("filling child took %v after the sidebar was hidden, want %v", got, rowWidth)
	}

	sidebar.Show()
	Relayout(row)
	if got := fill.Size().Width; got != rowWidth-sidebarMin {
		t.Fatalf("filling child took %v after the sidebar came back, want %v", got, rowWidth-sidebarMin)
	}
}

// TestPlaceBesideStaysOnScreen covers the popover placement: a card goes to the
// anchor's right and centred on it, flips to the left when the right edge is too
// close, and is pulled back inside when centring would hang it off an edge.
func TestPlaceBesideStaysOnScreen(t *testing.T) {
	var (
		bounds = fyne.NewSize(1000, 600)
		anchor = fyne.NewSize(40, 40)
		card   = fyne.NewSize(320, 400)
		gap    = theme.Sizes.PopoverGap
		margin = theme.Sizes.PopoverMargin
	)

	cases := []struct {
		name string
		at   fyne.Position
		want fyne.Position
	}{
		{"beside", fyne.NewPos(100, 300), fyne.NewPos(100+anchor.Width+gap, 300+anchor.Height/2-card.Height/2)},
		{"flipped at the right edge", fyne.NewPos(900, 300), fyne.NewPos(900-card.Width-gap, 300+anchor.Height/2-card.Height/2)},
		{"pulled down from the top", fyne.NewPos(100, 0), fyne.NewPos(100+anchor.Width+gap, margin)},
		{"pulled up from the bottom", fyne.NewPos(100, 580), fyne.NewPos(100+anchor.Width+gap, bounds.Height-card.Height-margin)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := placeBeside(tc.at, anchor, card, bounds)
			if got != tc.want {
				t.Errorf("card placed at %v, want %v", got, tc.want)
			}
			if got.X < margin || got.Y < margin ||
				got.X+card.Width > bounds.Width-margin || got.Y+card.Height > bounds.Height-margin {
				t.Errorf("card at %v of size %v left the %v layer", got, card, bounds)
			}
		})
	}
}

// TestFlowWrapsIntoRows covers what the chip rows need from the flow layout: it
// wraps at the width it was given, and measures the same arrangement it draws —
// a MinSize reporting one row for content that lays out as two would let the
// chips draw over whatever follows them.
func TestFlowWrapsIntoRows(t *testing.T) {
	const (
		width   = 100
		spacing = 4
		chip    = 40
		height  = 20
	)

	chips := make([]fyne.CanvasObject, 4)
	for i := range chips {
		box := canvas.NewRectangle(color.Transparent)
		box.SetMinSize(fyne.NewSize(chip, height))
		chips[i] = box
	}

	flow := NewFlow(width, spacing, chips...)
	flow.Resize(flow.MinSize())

	// Two per row: a third would start at 88 and end past 100.
	if got, want := flow.MinSize().Height, float32(2*height+spacing); got != want {
		t.Errorf("four chips measure %vpx tall, want %v over two rows", got, want)
	}
	if got, want := chips[2].Position().Y, float32(height+spacing); got != want {
		t.Errorf("the third chip is at y=%v, want %v on the second row", got, want)
	}
	if got := chips[1].Position(); got != (fyne.NewPos(chip+spacing, 0)) {
		t.Errorf("the second chip is at %v, want it beside the first", got)
	}
}
