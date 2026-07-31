package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// ellipsis marks a shortened label. One glyph rather than three dots, so the
// text it replaces stays as long as possible.
const ellipsis = "…"

// NewEllipsisText wraps a single-line canvas.Text in a container that shortens
// the text to whatever width it is given, ending it in an ellipsis. Its minimum
// width is zero, which is the whole point: a sidebar row must not be able to
// widen its column just because someone has a long name.
//
// It only works in a slot that hands its child real width — a Border's centre,
// not an HBox, which would give a zero-minimum child zero width.
func NewEllipsisText(text *canvas.Text) *fyne.Container {
	return container.New(&ellipsisLayout{text: text, full: text.Text}, text)
}

// ellipsisLayout re-fits its text to the width it is handed and centres it
// vertically. Rewriting the text during Layout is safe because the reported
// minimum size doesn't depend on the content — the width is fixed at zero and
// the height is the font's — so a shortened string can't trigger another layout.
type ellipsisLayout struct {
	text *canvas.Text
	full string
}

func (l *ellipsisLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	if fitted := TruncateToWidth(l.full, size.Width, l.text.TextSize, l.text.TextStyle); fitted != l.text.Text {
		l.text.Text = fitted
		l.text.Refresh()
	}

	height := l.lineHeight()
	l.text.Resize(fyne.NewSize(size.Width, height))
	l.text.Move(fyne.NewPos(0, (size.Height-height)/2))
}

func (l *ellipsisLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, l.lineHeight())
}

// lineHeight measures a sample glyph rather than the text itself, so a row is
// the same height whether its name is long, short, or not yet resolved.
func (l *ellipsisLayout) lineHeight() float32 {
	return fyne.MeasureText("W", l.text.TextSize, l.text.TextStyle).Height
}

// TruncateToWidth shortens text until it fits inside width when rendered at the
// given size and style, appending an ellipsis when anything was dropped. Unlike
// util.Truncate, which counts runes, this measures the rendered result — in a
// proportional font the same rune count is a different width. The binary search
// keeps it to a handful of measurements, which matters because it runs on every
// layout pass.
func TruncateToWidth(text string, width, size float32, style fyne.TextStyle) string {
	if width <= 0 {
		return ""
	}
	if fyne.MeasureText(text, size, style).Width <= width {
		return text
	}

	// Longest prefix that still fits once the ellipsis is added.
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if fyne.MeasureText(string(runes[:mid])+ellipsis, size, style).Width <= width {
			low = mid
		} else {
			high = mid - 1
		}
	}

	if low == 0 {
		return ""
	}
	return string(runes[:low]) + ellipsis
}
