package ui

// The day separator: the dated hairline the message list draws where consecutive
// messages fall on different calendar days. It is not a list entry of its own —
// MessageWidget renders it above the first message of each day, so it travels
// with that message through every mount, trim, and re-render path.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/internal/ui/theme"
)

// NewDaySeparator builds the divider announcing a new day of messages: the day's
// name at the left, a hairline running from it out to the right edge, inset to
// the same horizontal padding as a message row.
func NewDaySeparator(label string) fyne.CanvasObject {
	text := canvas.NewText(label, theme.Colors.DaySeparatorText)
	text.TextSize = theme.Sizes.DaySeparatorTextSize
	text.TextStyle = fyne.TextStyle{Bold: true}

	rule := canvas.NewRectangle(theme.Colors.DaySeparatorLine)
	rule.SetMinSize(fyne.NewSize(0, theme.Sizes.DaySeparatorThickness))

	row := container.New(&daySeparatorLayout{gap: theme.Sizes.DaySeparatorGap}, text, rule)
	hPad := theme.Sizes.MessageHorizontalPadding
	return container.NewBorder(
		VerticalSpacer(theme.Sizes.DaySeparatorTopPadding),
		VerticalSpacer(theme.Sizes.DaySeparatorBottomPadding),
		HorizontalSpacer(hPad), HorizontalSpacer(hPad),
		row,
	)
}

// daySeparatorLayout lays out the label and the rule that trails it: the label
// keeps its minimum size, the rule takes the leftover width, and both are
// vertically centred so the hairline meets the middle of the text. It expects
// exactly two children, label first.
type daySeparatorLayout struct{ gap float32 }

func (l *daySeparatorLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}
	label, rule := objects[0], objects[1]

	lm := label.MinSize()
	label.Resize(lm)
	label.Move(fyne.NewPos(0, (size.Height-lm.Height)/2))

	x := lm.Width + l.gap
	height := rule.MinSize().Height
	rule.Resize(fyne.NewSize(max(size.Width-x, 0), height))
	rule.Move(fyne.NewPos(x, (size.Height-height)/2))
}

func (l *daySeparatorLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, child := range objects {
		m := child.MinSize()
		w += m.Width
		h = max(h, m.Height)
	}
	return fyne.NewSize(w+l.gap, h)
}
