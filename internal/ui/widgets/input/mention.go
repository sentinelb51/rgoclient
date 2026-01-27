package input

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	appTheme "RGOClient/internal/ui/theme"
	"RGOClient/internal/ui/widgets"
)

// mentionToggleButton previews toggled state on hover.
// Rendered state = active XOR hovered.
// Hover when off  => highlight
// Hover when on   => unhighlight.
//
// Note: Positioning uses layout offsets; Move() gets overridden by container layouts.
type mentionToggleButton struct {
	widget.BaseWidget
	active  bool
	hovered bool
	onTap   func()

	bg   *canvas.Rectangle
	text *canvas.Text

	content *fyne.Container
}

var _ fyne.Widget = (*mentionToggleButton)(nil)
var _ fyne.Tappable = (*mentionToggleButton)(nil)
var _ desktop.Hoverable = (*mentionToggleButton)(nil)

func newMentionToggleButton(active bool, onTap func()) *mentionToggleButton {
	btnSize := fyne.NewSize(20, 20)

	bg := canvas.NewRectangle(appTheme.Colors.SwiftActionBg)
	bg.SetMinSize(btnSize)

	text := canvas.NewText("@", appTheme.Colors.TimestampText)
	text.TextSize = 20
	text.TextStyle = fyne.TextStyle{Bold: true}

	// Offset glyph; Move() gets overridden by layouts.
	textOffset := container.New(&widgets.OverlayLayout{YOffset: -15, RightOffset: 0}, text)
	centeredText := container.NewCenter(textOffset)

	content := container.NewStack(bg, centeredText)
	content.Resize(btnSize)

	b := &mentionToggleButton{
		active:  active,
		onTap:   onTap,
		bg:      bg,
		text:    text,
		content: content,
	}
	b.ExtendBaseWidget(b)
	b.applyState()
	return b
}

func (b *mentionToggleButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

func (b *mentionToggleButton) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *mentionToggleButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.applyState()
}

func (b *mentionToggleButton) MouseOut() {
	b.hovered = false
	b.applyState()
}

func (b *mentionToggleButton) MouseMoved(*desktop.MouseEvent) {}

func (b *mentionToggleButton) SetActive(active bool) {
	b.active = active
	b.applyState()
}

func (b *mentionToggleButton) applyState() {
	if b.active != b.hovered {
		b.text.Color = appTheme.Colors.TextPrimary
	} else {
		b.text.Color = appTheme.Colors.TimestampText
	}
	b.text.Refresh()
}
