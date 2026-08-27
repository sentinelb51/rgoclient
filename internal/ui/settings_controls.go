package ui

// The settings page's controls. None is a Fyne form widget: AppTheme zeroes
// SizeNameInputBorder and answers SizeNameInputBackground with the very colour a
// group is a card of, so Check, Select and Slider arrive flat, invisible, or — the
// slider — as a bare thumb swelling into a grey disc under the pointer. Rather
// than a theme override per widget, each control here is canvas objects and a
// layout drawn from the client's own palette.
//
// All of them are one shape: a fixed-height box the row hands to its control
// slot, so a switch, a dropdown and a slider all leave the row the same height.

import (
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

// paletteColumns is how many swatches a row of the colour picker holds. The
// preset list below is a multiple of it, so the grid comes out square-edged.
const paletteColumns = 8

/* Shared surfaces */

// newFieldBackground is the surface every control holding a *value* is drawn on:
// the composer's input fill, the hairline, the page's radius. It is what makes a
// dropdown read as something to open rather than a label sitting on the right.
func newFieldBackground() *canvas.Rectangle {
	field := canvas.NewRectangle(theme.Colors.ComposerBg)
	field.CornerRadius = theme.Sizes.SettingsInputRadius
	Outline(field)

	return field
}

// fixedControl pins a control to the standard slot, so every row's trailing edge
// lines up and nothing moves when a control grows on interaction.
func fixedControl(width float32, content fyne.CanvasObject) fyne.CanvasObject {
	return NewFixedWidthContainer(width,
		NewFixedHeightContainer(theme.Sizes.SettingsInputHeight, content))
}

// newSettingsCard is the outlined surface a group of rows — and the colour picker
// floating over them — is drawn on.
func newSettingsCard() *canvas.Rectangle {
	card := canvas.NewRectangle(theme.Colors.SessionCardBg)
	card.CornerRadius = theme.Sizes.SettingsGroupRadius
	Outline(card)

	return card
}

/* Toggle */

// Toggle is the on/off switch every boolean setting uses: a pill filling with the
// accent as its knob slides across. Not a widget.Check — a checkbox reads as part
// of a form, where these rows are a list of states.
type Toggle struct {
	tapBase

	// OnChanged is called with the new state, on the UI thread.
	OnChanged func(bool)

	on      bool
	track   *canvas.Rectangle
	knob    *canvas.Circle
	content *fyne.Container
}

var _ fyne.Tappable = (*Toggle)(nil)

// NewToggle builds a switch in the given state.
func NewToggle(on bool, onChanged func(bool)) *Toggle {
	t := &Toggle{
		OnChanged: onChanged,
		on:        on,
		track:     canvas.NewRectangle(nil),
		knob:      canvas.NewCircle(theme.Colors.TextPrimary),
	}
	t.content = container.New(&toggleLayout{toggle: t}, t.track, t.knob)
	t.onTap = t.flip
	t.paint()
	t.ExtendBaseWidget(t)

	return t
}

func (t *Toggle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

// On reports the current state.
func (t *Toggle) On() bool { return t.on }

// Set moves the switch without calling back, for a state something else changed.
func (t *Toggle) Set(on bool) {
	if t.on == on {
		return
	}

	t.on = on
	t.paint()
	Relayout(t.content)
}

// flip is the tap: state, then knob, then the callback — so a handler that
// rebuilds the section sees the switch already moved.
func (t *Toggle) flip() {
	t.on = !t.on
	t.paint()
	Relayout(t.content)

	if t.OnChanged != nil {
		t.OnChanged(t.on)
	}
}

func (t *Toggle) paint() {
	t.track.FillColor = theme.Colors.ChannelSelectedBg
	if t.on {
		t.track.FillColor = theme.Colors.ServerSelectedBg
	}
	t.track.CornerRadius = theme.Sizes.SettingsToggleHeight / 2
	t.track.Refresh()
}

// toggleLayout places the knob at one end of the track or the other.
type toggleLayout struct {
	toggle *Toggle
}

func (l *toggleLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(theme.Sizes.SettingsToggleWidth, theme.Sizes.SettingsToggleHeight)
}

func (l *toggleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}

	track, knob := objects[0], objects[1]
	track.Resize(size)
	track.Move(fyne.NewPos(0, 0))

	inset := theme.Sizes.SettingsToggleInset
	side := size.Height - inset*2

	x := inset
	if l.toggle.on {
		x = size.Width - side - inset
	}

	knob.Resize(fyne.NewSize(side, side))
	knob.Move(fyne.NewPos(x, inset))
}

/* Slider */

// Slider is a value dragged along a track. Fyne's own was tried and abandoned:
// its thickness is two SizeNameInputBorders, which AppTheme zeroes, and its fill
// is SizeNameInputBackground, which AppTheme answers with the card's own colour —
// so it needed a scoped override to be visible at all, and still grew a grey
// hover disc that swallowed the thumb. This is a track, a fill and a knob.
type Slider struct {
	tapBase

	// OnChanged is called with each new value, on the UI thread. A drag calls it
	// per frame, so it is the caller's business to keep the work small.
	OnChanged func(float64)

	low, high, step float64
	value           float64

	// pivot is the value pinned to the middle of the travel, hasPivot whether
	// there is one. Without it the scale is linear and a range that is not
	// symmetric puts its natural resting point wherever the arithmetic lands.
	pivot    float64
	hasPivot bool

	track   *canvas.Rectangle
	fill    *canvas.Rectangle
	detent  *canvas.Rectangle
	knob    *canvas.Circle
	content *fyne.Container
}

var (
	_ fyne.Tappable     = (*Slider)(nil)
	_ fyne.Draggable    = (*Slider)(nil)
	_ desktop.Hoverable = (*Slider)(nil)
)

// NewSlider builds a slider over [low, high], quantised to step.
func NewSlider(low, high, step, value float64, onChanged func(float64)) *Slider {
	s := &Slider{
		OnChanged: onChanged,
		low:       low,
		high:      high,
		step:      step,
		value:     clamp(value, low, high),
		track:     canvas.NewRectangle(theme.Colors.ChannelSelectedBg),
		fill:      canvas.NewRectangle(theme.Colors.ServerSelectedBg),
		detent:    canvas.NewRectangle(theme.Colors.SliderDetent),
		knob:      canvas.NewCircle(theme.Colors.TextPrimary),
	}

	radius := theme.Sizes.SettingsSliderTrack / 2
	s.track.CornerRadius = radius
	s.fill.CornerRadius = radius
	s.detent.CornerRadius = theme.Sizes.SettingsSliderDetentWidth / 2
	s.detent.Hide()
	s.knob.StrokeColor = theme.Colors.Outline
	s.knob.StrokeWidth = theme.Sizes.OutlineWidth

	// The detent is over the fill and under the knob: the fill crosses it once the
	// value passes the pivot, and the knob covers it while it sits there.
	s.content = container.New(&sliderLayout{slider: s}, s.track, s.fill, s.detent, s.knob)
	s.ExtendBaseWidget(s)

	return s
}

func (s *Slider) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

// Value reports where the slider stands.
func (s *Slider) Value() float64 { return s.value }

// SetTrack recolours the unfilled travel, for a slider mounted on a surface its
// default track cannot be seen against. Call before the first draw.
func (s *Slider) SetTrack(fill color.Color) { s.track.FillColor = fill }

// SetPivot pins one value to the middle of the travel, each side of the range
// then scaled to the half it gets, and marks it with a notch a drag lands on
// from within half a knob. A gain runs -40 dB to +20, so unity — where everyone
// is until somebody is moved — otherwise sits two thirds along and can only be
// found by reading the number. Call before the first draw.
func (s *Slider) SetPivot(value float64) {
	s.pivot, s.hasPivot = clamp(value, s.low, s.high), true
	s.detent.Show()
}

// SetValue moves the slider without calling back, for a value something else
// changed — the field beside it, or a section resetting a whole group.
func (s *Slider) SetValue(value float64) {
	value = clamp(value, s.low, s.high)
	if value == s.value {
		return
	}

	s.value = value
	Relayout(s.content)
}

func (s *Slider) Tapped(event *fyne.PointEvent) { s.moveTo(event.Position.X) }

func (s *Slider) Dragged(event *fyne.DragEvent) { s.moveTo(event.Position.X) }

// DragEnd completes fyne.Draggable. Without it the driver never recognises the
// slider as draggable and Dragged is never called at all.
func (s *Slider) DragEnd() {}

// MouseIn lights the knob's edge with the accent. The knob itself keeps its size:
// a control that grows under the pointer covers the track it is measured against.
func (s *Slider) MouseIn(*desktop.MouseEvent) {
	s.knob.StrokeColor = theme.Colors.ServerSelectedBg
	s.knob.Refresh()
}

func (s *Slider) MouseOut() {
	s.knob.StrokeColor = theme.Colors.Outline
	s.knob.Refresh()
}

// moveTo sets the value from a pointer position along the widget. The travel is
// the width less the knob, because the knob is placed by its left edge — without
// that the two ends of the track would be unreachable.
func (s *Slider) moveTo(x float32) {
	knob := theme.Sizes.SettingsSliderKnob
	travel := s.Size().Width - knob
	if travel <= 0 || s.high <= s.low {
		return
	}

	ratio := float64(clamp((x-knob/2)/travel, 0, 1))
	value := s.valueAt(ratio)
	if s.step > 0 {
		value = math.Round(value/s.step) * s.step
	}

	// The detent. Half a knob of slack either side, so the pivot is where a drag
	// released near the middle lands rather than something to be aimed at.
	if s.hasPivot && math.Abs(ratio-0.5)*float64(travel) <= float64(knob)/2 {
		value = s.pivot
	}

	value = clamp(value, s.low, s.high)
	if value == s.value {
		return
	}

	s.value = value
	Relayout(s.content)

	if s.OnChanged != nil {
		s.OnChanged(value)
	}
}

// ratio is how far along the track the value stands.
func (s *Slider) ratio() float32 { return s.ratioOf(s.value) }

// ratioOf places any value on the track, and is where a pivot is honoured: each
// side of it takes half the travel, so a range far from symmetric still reads
// against its own middle. Pivoted or not, the two ends stay at 0 and 1.
func (s *Slider) ratioOf(value float64) float32 {
	if s.high <= s.low {
		return 0
	}

	if !s.pivoted() {
		return float32((value - s.low) / (s.high - s.low))
	}

	if value <= s.pivot {
		return float32((value - s.low) / (s.pivot - s.low) / 2)
	}

	return float32(0.5 + (value-s.pivot)/(s.high-s.pivot)/2)
}

// valueAt is the inverse, for a pointer that landed somewhere along the track.
func (s *Slider) valueAt(ratio float64) float64 {
	if !s.pivoted() {
		return s.low + ratio*(s.high-s.low)
	}

	if ratio <= 0.5 {
		return s.low + ratio*2*(s.pivot-s.low)
	}

	return s.pivot + (ratio-0.5)*2*(s.high-s.pivot)
}

// pivoted reports whether the split scale applies. A pivot at either end would
// give one side of the range the whole travel and the other none of it.
func (s *Slider) pivoted() bool {
	return s.hasPivot && s.pivot > s.low && s.pivot < s.high
}

// sliderLayout centres the track on the widget's height and places the knob
// along it; objects are track, fill then knob.
type sliderLayout struct{ slider *Slider }

func (l *sliderLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 4 {
		return
	}

	track, fill, detent, knob := objects[0], objects[1], objects[2], objects[3]
	thickness := theme.Sizes.SettingsSliderTrack
	side := theme.Sizes.SettingsSliderKnob
	travel := max(size.Width-side, 0)

	y := (size.Height - thickness) / 2
	track.Resize(fyne.NewSize(size.Width, thickness))
	track.Move(fyne.NewPos(0, y))

	x := travel * l.slider.ratio()
	fill.Resize(fyne.NewSize(x+side/2, thickness))
	fill.Move(fyne.NewPos(0, y))

	// The notch stands under the knob's centre at the pivot, not its left edge,
	// which is where the knob is placed from.
	notch := fyne.NewSize(theme.Sizes.SettingsSliderDetentWidth, theme.Sizes.SettingsSliderDetentHeight)
	detent.Resize(notch)
	detent.Move(fyne.NewPos(travel*l.slider.ratioOf(l.slider.pivot)+(side-notch.Width)/2,
		(size.Height-notch.Height)/2))

	knob.Resize(fyne.NewSize(side, side))
	knob.Move(fyne.NewPos(x, (size.Height-side)/2))
}

func (l *sliderLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(theme.Sizes.SettingsSliderKnob, theme.Sizes.SettingsSliderHeight)
}

// newNumberBody is a slider and the exact value beside it, each moving the other,
// without the slot around them. The value becomes a field when clicked: a slider
// is how a size is *found*, typing how one already known is set. The slider takes
// the fill index, so widening the body lengthens it and leaves the value put.
func newNumberBody(value, low, high, step float64, unit string, onChanged func(float64)) fyne.CanvasObject {
	var slider *Slider

	box := newNumberBox(value, low, high, unit, func(typed float64) {
		slider.SetValue(typed)
		onChanged(typed)
	})
	slider = NewSlider(low, high, step, value, func(dragged float64) {
		box.SetValue(dragged)
		onChanged(dragged)
	})

	return NewFillRow(0,
		slider,
		HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
		NewFixedWidthContainer(theme.Sizes.SettingsValueWidth, box),
	)
}

// newNumberControl fits the pair into a row's trailing control slot.
func newNumberControl(value, low, high, step float64, unit string, onChanged func(float64)) fyne.CanvasObject {
	return fixedControl(theme.Sizes.SettingsControlWidth,
		newNumberBody(value, low, high, step, unit, onChanged))
}

// newWideNumberControl is the pair on a line of its own, pinned to the height of
// a control slot but given the row's whole width — which is the room a slider
// needs to be aimed with.
func newWideNumberControl(value, low, high, step float64, unit string, onChanged func(float64)) fyne.CanvasObject {
	return NewFixedHeightContainer(theme.Sizes.SettingsInputHeight,
		newNumberBody(value, low, high, step, unit, onChanged))
}

/* The number beside a slider */

// numberBox shows a slider's exact value and becomes the field it can be typed
// into. A slider alone cannot be aimed at a particular number, and one whose
// range is thousands of pixels cannot be aimed at all. The entry is built on
// demand rather than kept hidden: the Advanced section mounts a hundred of these.
type numberBox struct {
	tapBase

	unit      string
	low, high float64
	value     float64

	// onCommit is called with a typed value, never with a value SetValue was
	// given — the slider that drives this is already telling its own caller.
	onCommit func(float64)

	text       *canvas.Text
	background *canvas.Rectangle
	content    *fyne.Container
	entry      *numberEntry
}

var (
	_ fyne.Tappable     = (*numberBox)(nil)
	_ desktop.Hoverable = (*numberBox)(nil)
)

func newNumberBox(value, low, high float64, unit string, onCommit func(float64)) *numberBox {
	b := &numberBox{
		unit:       unit,
		low:        low,
		high:       high,
		value:      value,
		onCommit:   onCommit,
		background: canvas.NewRectangle(color.Transparent),
	}
	b.background.CornerRadius = theme.Sizes.SettingsInputRadius

	b.text = newText(b.valueText(), theme.Colors.TextPrimary, theme.Sizes.SettingsLabelSize)

	b.content = container.NewStack(b.background, container.NewCenter(b.text))
	b.onTap = b.beginEdit
	b.ExtendBaseWidget(b)

	return b
}

func (b *numberBox) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

// SetValue shows a different number without reporting it.
func (b *numberBox) SetValue(value float64) {
	if value == b.value {
		return
	}

	b.value = value
	if b.entry == nil {
		b.text.Text = b.valueText()
		b.text.Refresh()
	}
}

func (b *numberBox) MouseIn(*desktop.MouseEvent) {
	if b.entry != nil {
		return
	}

	b.background.FillColor = theme.Colors.TappableHoverBg
	b.background.Refresh()
}

func (b *numberBox) MouseOut() {
	if b.entry != nil {
		return
	}

	b.background.FillColor = color.Transparent
	b.background.Refresh()
}

// beginEdit swaps the number for a field and puts the cursor in it.
func (b *numberBox) beginEdit() {
	if b.entry != nil {
		return
	}

	b.entry = newNumberEntry(strconv.Itoa(int(b.value)), b)

	b.background.FillColor = theme.Colors.ComposerBg
	Outline(b.background)
	b.content.Objects = []fyne.CanvasObject{b.background, WithCaret(b.entry)}
	b.content.Refresh()

	if canvas := fyne.CurrentApp().Driver().CanvasForObject(b); canvas != nil {
		canvas.Focus(b.entry)
	}
}

// submit answers Enter by giving the focus up; commit runs on the way out, so
// there is one path that reads the field and one only.
func (b *numberBox) submit() { b.unfocus() }

// cancel answers Escape by putting the current value back before giving the
// focus up, so the commit that follows is a no-op.
func (b *numberBox) cancel() {
	if b.entry == nil {
		return
	}

	b.entry.SetText(strconv.Itoa(int(b.value)))
	b.unfocus()
}

// commit reads the field, clamps it to the slider's range, and returns the box to
// showing a number. Anything unparseable is discarded — an empty field is
// half-finished typing, not a request for zero. Only the field currently open is
// read: focusing a second box makes the first report its loss *after* the second
// has installed its own, so an unguarded commit would close the new one.
func (b *numberBox) commit(entry *numberEntry) {
	if entry == nil || b.entry != entry {
		return
	}
	b.entry = nil

	if value, err := strconv.ParseFloat(strings.TrimSpace(entry.Text), 64); err == nil {
		value = clamp(value, b.low, b.high)
		if value != b.value {
			b.value = value
			if b.onCommit != nil {
				b.onCommit(value)
			}
		}
	}

	b.text.Text = b.valueText()
	b.text.Refresh()

	b.background.FillColor = color.Transparent
	b.background.StrokeWidth = 0
	b.content.Objects = []fyne.CanvasObject{b.background, container.NewCenter(b.text)}
	b.content.Refresh()
}

func (b *numberBox) unfocus() {
	if canvas := fyne.CurrentApp().Driver().CanvasForObject(b); canvas != nil {
		canvas.Unfocus()
	}
}

// valueText is the number and its unit. A percent sign is set tight against the
// figure and a unit word is not — the typographic convention, and what the
// meters' own readouts say beside these rows.
func (b *numberBox) valueText() string {
	number := strconv.Itoa(int(b.value))

	switch b.unit {
	case "":
		return number
	case "%":
		return number + b.unit
	}

	return number + " " + b.unit
}

// numberEntry is the field a numberBox becomes, for two things widget.Entry
// offers no hook for: Escape, which it hands to the canvas where the page reads
// it as "close", and losing focus, the one place the typed value is read.
type numberEntry struct {
	widget.Entry

	box *numberBox
}

var _ fyne.Focusable = (*numberEntry)(nil)

func newNumberEntry(text string, box *numberBox) *numberEntry {
	e := &numberEntry{box: box}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(string) { box.submit() }
	e.SetText(text)

	return e
}

func (e *numberEntry) FocusLost() {
	e.Entry.FocusLost()
	e.box.commit(e)
}

func (e *numberEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		e.box.cancel()
		return
	}

	e.Entry.TypedKey(key)
}

/* Text fields */

var _ fyne.Focusable = (*commitEntry)(nil)

// commitEntry reports its value once it has settled — on Enter and on losing
// focus — rather than per keystroke, every report being a request. Escape puts
// back what was there and is swallowed, widget.Entry otherwise handing the key to
// the canvas where the page reads it as "close".
type commitEntry struct {
	widget.Entry

	// committed is the last value handed on, so a field left alone reports
	// nothing when the focus moves off it.
	committed string
	onCommit  func(string)

	// area is set on a prose field, which grows with what is typed; wrap is the
	// row count that growth is measured by, and is unused on a one-line field.
	area bool
	wrap wrapMeter
}

func newCommitEntry(text string, onCommit func(string)) *commitEntry {
	e := &commitEntry{committed: text, onCommit: onCommit}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(string) { e.commit() }
	e.SetText(text)

	return e
}

// newCommitArea is a commitEntry for prose. Enter puts in a newline rather than
// submitting once an entry is multi-line, so what it reports on is losing the
// focus alone. It grows with what is typed the way the composer does, between
// SettingsAreaMinLines and SettingsAreaMaxLines: a paragraph read back through a
// fixed four-row box is read a sentence at a time, and nothing on the box says
// there is more of it below.
func newCommitArea(text string, onCommit func(string)) *commitEntry {
	e := newCommitEntry(text, onCommit)
	e.area = true
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord

	return e
}

// MinSize grows a prose field with its text. A one-line field answers as
// widget.Entry does.
func (e *commitEntry) MinSize() fyne.Size {
	if !e.area {
		return e.Entry.MinSize()
	}

	return growingMinSize(&e.Entry, &e.wrap,
		int(theme.Sizes.SettingsAreaMinLines), int(theme.Sizes.SettingsAreaMaxLines))
}

// Fill puts in a value that arrived after the field was built — a bio is fetched,
// not held. It reports nothing, and is a no-op once there is anything in the
// field: an answer landing late must not take back what is being written.
func (e *commitEntry) Fill(text string) {
	if e.Text != "" || e.committed != "" {
		return
	}

	e.committed = text
	e.SetText(text)
}

func (e *commitEntry) FocusLost() {
	e.Entry.FocusLost()
	e.commit()
}

func (e *commitEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		e.SetText(e.committed)
		return
	}

	e.Entry.TypedKey(key)
}

func (e *commitEntry) commit() {
	if e.Text == e.committed {
		return
	}

	e.committed = e.Text
	if e.onCommit != nil {
		e.onCommit(e.Text)
	}
}

/* Option rows */

// settingsOption is one entry of an option row's menu: what the user reads and
// what is stored.
type settingsOption struct {
	Label string
	Value string
}

// optionControl is the page's dropdown: the chosen value in a field of its own,
// opening the rest as a menu beneath. A field rather than bare text — Fyne's
// Select draws one but AppTheme flattens its border away, and the value alone
// reads as part of the row's description rather than as the control.
type optionControl struct {
	tapBase

	label      *fyne.Container
	background *canvas.Rectangle
	options    []settingsOption
	onPick     func(string)
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*optionControl)(nil)
	_ desktop.Hoverable = (*optionControl)(nil)
)

func newOptionControl(value string, options []settingsOption, onPick func(string)) *optionControl {
	// Shortened rather than clipped: the slot is fixed and a device name is longer
	// than it, and a canvas.Text draws its whole width whatever it is resized to —
	// over the chevron and out through the field's right edge.
	label := NewEllipsisText(newText(optionLabel(options, value),
		theme.Colors.TextPrimary, theme.Sizes.SettingsLabelSize))

	chevron := newScaledIcon(fynetheme.MenuDropDownIcon(), theme.Sizes.SettingsIconSize)

	c := &optionControl{label: label, background: newFieldBackground(), options: options, onPick: onPick}

	padding := theme.Sizes.SettingsRowPaddingH
	row := NewFillRow(0,
		label,
		HorizontalSpacer(theme.Sizes.ChipDotGap),
		container.NewCenter(chevron),
	)

	c.content = fixedControl(theme.Sizes.SettingsControlWidth,
		container.NewStack(c.background, NewInset(row, 0, 0, padding, padding)))
	c.onTap = c.open
	c.ExtendBaseWidget(c)

	return c
}

func (c *optionControl) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

// set shows a different option as chosen.
func (c *optionControl) set(value string) {
	SetEllipsisText(c.label, optionLabel(c.options, value))
}

func (c *optionControl) MouseIn(*desktop.MouseEvent) {
	c.background.FillColor = theme.Colors.ChannelSelectedBg
	c.background.Refresh()
}

func (c *optionControl) MouseOut() {
	c.background.FillColor = theme.Colors.ComposerBg
	c.background.Refresh()
}

func (c *optionControl) open() {
	target := fyne.CurrentApp().Driver().CanvasForObject(c)
	if target == nil || len(c.options) == 0 {
		return
	}

	list := newDropdownList(c.options, target, c.onPick)
	list.minWidth = c.Size().Width
	list.ShowAtPosition(AnchorBelow(c))
}

// optionLabel is what an option's stored value reads as. An unrecognised value —
// a hand-edited file naming something the client dropped — shows itself, rather
// than an empty control that looks broken.
func optionLabel(options []settingsOption, value string) string {
	at := slices.IndexFunc(options, func(option settingsOption) bool { return option.Value == value })
	if at < 0 {
		return value
	}

	return options[at].Label
}

// dropdownList is what an option control opens: the client's own list rather
// than Fyne's menu. widget.Menu lays its items out at their own minimum whatever
// the pop-up around them is resized to — the flag that would stretch them is
// unexported and set only by NewPopUpMenu — so a menu held open to a wider
// control drew a row the width of its longest label, adrift inside the box.
// Drawing the rows is also what lets them alternate: a list of near-identical
// labels is a set of rows before the pointer is anywhere near it.
type dropdownList struct {
	widget.BaseWidget

	rows    []*dropdownRow
	popUp   *widget.PopUp
	canvas  fyne.Canvas
	content fyne.CanvasObject

	// active is the row the keyboard is on, -1 until an arrow key moves it. The
	// pointer does not set it: a list is driven by one or the other at a time.
	active int

	// minWidth holds the list open to the control it drops from. Zero is a list
	// sized by its own longest label.
	minWidth float32
}

var _ fyne.Focusable = (*dropdownList)(nil)

func newDropdownList(options []settingsOption, c fyne.Canvas, onPick func(string)) *dropdownList {
	l := &dropdownList{canvas: c, active: -1}

	// The pop-up paints its own rounded surface under this one, so the rows take
	// its radius rather than the field's: a corner of row fill outside that curve
	// is a square nub the border then draws around.
	radius := fynetheme.Size(fynetheme.SizeNamePopupRadius)

	rows := make([]fyne.CanvasObject, len(options))
	for i, option := range options {
		row := newDropdownRow(option.Label, i%2 == 1)
		row.onTap = func() {
			l.popUp.Hide()
			onPick(option.Value)
		}

		if i == 0 {
			row.background.TopLeftCornerRadius = radius
			row.background.TopRightCornerRadius = radius
		}
		if i == len(options)-1 {
			row.background.BottomLeftCornerRadius = radius
			row.background.BottomRightCornerRadius = radius
		}

		l.rows = append(l.rows, row)
		rows[i] = NewFixedHeightContainer(theme.Sizes.SettingsInputHeight, row)
	}

	// The border is stacked over the rows rather than under them: the rows reach
	// the list's edge, and a rectangle behind them would be painted out.
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = theme.Colors.Outline
	border.StrokeWidth = theme.Sizes.OutlineWidth
	border.CornerRadius = radius

	l.content = container.NewStack(VBoxNoSpacing(rows...), border)
	l.ExtendBaseWidget(l)
	l.popUp = widget.NewPopUp(l, c)

	return l
}

func (l *dropdownList) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.content)
}

// MinSize widens the list to minWidth. The pop-up takes its size from what it
// holds, so this is the only place a dropdown can be held to its control's width.
func (l *dropdownList) MinSize() fyne.Size {
	size := l.BaseWidget.MinSize()
	size.Width = max(size.Width, l.minWidth)

	return size
}

// ShowAtPosition drops the list at pos, pulled back inside the canvas where it
// would otherwise hang off the right or bottom edge.
func (l *dropdownList) ShowAtPosition(pos fyne.Position) {
	l.popUp.ShowAtPosition(keepInside(pos, l.MinSize(), l.canvas))
	l.canvas.Focus(l)
}

func (l *dropdownList) FocusGained()   {}
func (l *dropdownList) FocusLost()     {}
func (l *dropdownList) TypedRune(rune) {}

// TypedKey drives the list from the keyboard. It holds focus while it is open, so
// Escape closes it rather than reaching the handler left on the canvas for
// whatever is open behind it.
func (l *dropdownList) TypedKey(event *fyne.KeyEvent) {
	switch event.Name {
	case fyne.KeyDown:
		l.activate(l.active + 1)
	case fyne.KeyUp:
		l.activate(l.active - 1)
	case fyne.KeyEnter, fyne.KeyReturn, fyne.KeySpace:
		if l.active >= 0 {
			l.rows[l.active].Tapped(nil)
		}
	case fyne.KeyEscape:
		l.popUp.Hide()
	}
}

// activate moves the keyboard cursor, wrapping at either end.
func (l *dropdownList) activate(index int) {
	if len(l.rows) == 0 {
		return
	}

	index = (index + len(l.rows)) % len(l.rows)
	if l.active >= 0 {
		l.rows[l.active].setActive(false)
	}

	l.active = index
	l.rows[index].setActive(true)
}

// dropdownRow is one option: a fill that reaches both edges of the list, and a
// rest colour alternating down it.
type dropdownRow struct {
	tapBase

	background *canvas.Rectangle
	rest       color.Color
	content    fyne.CanvasObject
}

var (
	_ fyne.Tappable     = (*dropdownRow)(nil)
	_ desktop.Hoverable = (*dropdownRow)(nil)
)

func newDropdownRow(label string, alternate bool) *dropdownRow {
	rest := theme.Colors.ChannelListBackground
	if alternate {
		rest = theme.Colors.MenuStripeBg
	}

	r := &dropdownRow{background: canvas.NewRectangle(rest), rest: rest}
	text := newText(label, theme.Colors.TextPrimary, theme.Sizes.SettingsLabelSize)
	padding := theme.Sizes.SettingsRowPaddingH

	// Not NewEllipsisText, whose minimum width is zero: the list is sized by its
	// longest label, so a device name too long for the control still reads whole
	// once the list is open.
	r.content = container.NewStack(r.background,
		NewInset(NewFillRow(0, text), 0, 0, padding, padding))
	r.ExtendBaseWidget(r)

	return r
}

func (r *dropdownRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (r *dropdownRow) MouseIn(*desktop.MouseEvent) { r.setActive(true) }
func (r *dropdownRow) MouseOut()                   { r.setActive(false) }

// setActive lifts the row, for the pointer and for the keyboard alike.
func (r *dropdownRow) setActive(active bool) {
	r.background.FillColor = r.rest
	if active {
		r.background.FillColor = theme.Colors.MenuHoverBg
	}
	r.background.Refresh()
}

/* Colours */

// newSwatchRect is a sample of one colour, carrying the shared hairline so a
// saturated fill lifts off the surface behind it and a dark one is still a shape.
func newSwatchRect(fill color.Color, side, radius float32) *canvas.Rectangle {
	swatch := canvas.NewRectangle(fill)
	swatch.SetMinSize(fyne.NewSize(side, side))
	swatch.CornerRadius = radius
	Outline(swatch)

	return swatch
}

// swatchButton is the round sample beside a hex field, opening the palette. The
// palette is what makes a colour something to choose rather than to know.
type swatchButton struct {
	tapBase

	swatch  *canvas.Rectangle
	content fyne.CanvasObject
}

var _ fyne.Tappable = (*swatchButton)(nil)

func newSwatchButton(fill color.Color, onTap func()) *swatchButton {
	side := theme.Sizes.SettingsSwatchSize

	b := &swatchButton{swatch: newSwatchRect(fill, side, side/2)}
	b.content = container.NewCenter(b.swatch)
	b.onTap = onTap
	b.ExtendBaseWidget(b)

	return b
}

func (b *swatchButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

// SetColor repaints the sample, for a colour typed into the field beside it.
func (b *swatchButton) SetColor(fill color.Color) {
	b.swatch.FillColor = fill
	b.swatch.Refresh()
}

// palettePresets are the colours the picker offers: a neutral ramp, then two
// rows of hues. They are a starting point, not a limit — the hex field beside
// the picker takes anything.
var palettePresets = []string{
	"#FFFFFF", "#D1D5DB", "#9CA3AF", "#6B7280", "#4B5563", "#2B3142", "#1F2330", "#0F1117",
	"#5B7CFA", "#3B82F6", "#06B6D4", "#10B981", "#22C55E", "#EAB308", "#F97316", "#EF4444",
	"#8B5CF6", "#A855F7", "#EC4899", "#F43F5E", "#14B8A6", "#84CC16", "#C98A2A", "#C73E42",
}

// newPaletteCard is the picker itself: the presets as a grid, on the same card
// the rest of the page is made of.
func newPaletteCard(onPick func(hex string)) fyne.CanvasObject {
	side, gap := theme.Sizes.SettingsPaletteSize, theme.Sizes.SettingsPaletteGap

	cells := make([]fyne.CanvasObject, 0, len(palettePresets))
	for _, hex := range palettePresets {
		fill, ok := theme.ParseHex(hex)
		if !ok {
			continue
		}
		cells = append(cells, newPaletteCell(fill, hex, onPick))
	}

	grid := NewFlow(float32(paletteColumns)*(side+gap), gap, cells...)
	padding := theme.Sizes.SettingsPreviewGap

	return container.NewStack(newSettingsCard(), NewInset(grid, padding, padding, padding, padding))
}

// newPaletteCell is one preset. TappableContainer would give the hover for free,
// but it highlights by filling a rectangle *behind* its content — which a swatch
// covers completely.
func newPaletteCell(fill color.Color, hex string, onPick func(string)) fyne.CanvasObject {
	side := theme.Sizes.SettingsPaletteSize

	cell := &swatchButton{swatch: newSwatchRect(fill, side, theme.Sizes.SettingsInputRadius)}
	cell.content = cell.swatch
	cell.onTap = func() { onPick(hex) }
	cell.ExtendBaseWidget(cell)

	return cell
}

/* Usage meters */

// newUsageBar is the how-full-is-it bar the cache section draws, and the setter
// filling it. A figure beside a budget is two numbers to divide; the bar is the
// answer, and it turns as it runs out.
func newUsageBar() (*fyne.Container, func(ratio float32)) {
	height := theme.Sizes.SettingsUsageHeight

	track := canvas.NewRectangle(theme.Colors.ChannelSelectedBg)
	track.CornerRadius = height / 2

	fill := canvas.NewRectangle(theme.Colors.ServerSelectedBg)
	fill.CornerRadius = height / 2

	layout := &usageBarLayout{}
	bar := container.New(layout, track, fill)

	return bar, func(ratio float32) {
		layout.ratio = clamp(ratio, 0, 1)
		fill.FillColor = usageTint(ratio)
		fill.Refresh()
		Relayout(bar)
	}
}

// usageTint warns as a budget fills — the cache trims silently, so a bar sitting
// at the line is the only sign that images are being thrown away as fast as they
// arrive.
func usageTint(ratio float32) color.Color {
	switch {
	case ratio >= 1:
		return theme.Colors.NoticeDanger
	case ratio >= 0.85:
		return theme.Colors.NoticeWarning
	}

	return theme.Colors.ServerSelectedBg
}

/* The input level meter */

// newLevelBar is the bar itself: a fill for what is being measured and a marker
// for the threshold it is being compared against, on one scale so the two can be
// compared by eye. That comparison is the whole point of either voice meter — a
// threshold says nothing on its own about whether your voice clears it.
//
// The fill turns as it crosses the marker, which is the answer to the only
// question being asked: am I over the line right now.
//
// It hands back a setter for each, because they move for different reasons and
// at very different rates — the level on every sample the controller reports,
// the threshold only when the slider is dragged. newMeterBar is what the page
// actually mounts; this is the half of it with no words.
func newLevelBar() (bar *fyne.Container, setLevel, setThreshold func(ratio float32)) {
	height := theme.Sizes.SettingsLevelHeight

	track := canvas.NewRectangle(theme.Colors.ChannelSelectedBg)
	track.CornerRadius = height / 2

	fill := canvas.NewRectangle(theme.Colors.EmbedAccent)
	fill.CornerRadius = height / 2

	marker := canvas.NewRectangle(theme.Colors.TextPrimary)

	layout := &levelBarLayout{}
	bar = container.New(layout, track, fill, marker)

	// Redrawn from both setters, since either can change which side of the
	// threshold the level is on.
	paint := func() {
		open := layout.level >= layout.threshold && layout.level > 0
		if open {
			fill.FillColor = theme.Colors.PresenceOnline
		} else {
			fill.FillColor = theme.Colors.EmbedAccent
		}
		fill.Refresh()
		Relayout(bar)
	}

	setLevel = func(ratio float32) {
		layout.level = clamp(ratio, 0, 1)
		paint()
	}

	setThreshold = func(ratio float32) {
		layout.threshold = clamp(ratio, 0, 1)
		paint()
	}

	return bar, setLevel, setThreshold
}

// meterNoFigure is what a readout says where there is nothing to measure. Only
// the speech bar reaches it — the model runs solely while noise suppression
// does, and a percentage there would be a number for something nothing is
// computing.
const meterNoFigure = "—"

// newMeterBar is a diagnostic bar and the figure it is saying in words: the
// level bar above, plus a readout at the row's trailing end.
//
// The number is not decoration, and both voice meters now carry one. The bar
// answers "over the line right now", which is what aiming a threshold needs;
// the readout answers "by how much", which a strip a few pixels wide cannot
// say and which is the whole of what somebody watching a fan or a keyboard
// through one wants to know. The caller formats it, the wording being the row's
// rather than this control's.
func newMeterBar() (block *fyne.Container, set func(ratio float32, figure string), setThreshold func(ratio float32)) {
	bar, setLevel, setMark := newLevelBar()

	readout := newText(meterNoFigure, theme.Colors.TextPrimary, theme.Sizes.SettingsDetailSize)

	// The bar takes the fill slot and the readout sits at the row's trailing end,
	// so a figure growing a digit narrows the bar rather than moving it.
	block = NewFillRow(0, bar, HorizontalSpacer(theme.Sizes.ChipSpacing), vcenter(readout))

	set = func(ratio float32, figure string) {
		setLevel(ratio)

		// Guarded because this arrives at the sampling rate and most samples round
		// to the figure the last one did: a canvas.Text Refresh dirties the whole
		// window, and each distinct string is a glyph texture of its own.
		if readout.Text != figure {
			readout.Text = figure
			readout.Refresh()
			Relayout(block)
		}
	}

	return block, set, setMark
}

// meterDecibels and meterPercent are how the two bars say their figure. Both
// take what InputMeter reported and neither converts anything: the scales are
// the audio package's, and a second copy of either up here would be free to
// drift from the one the gate decides by.
func meterDecibels(db int) string { return strconv.Itoa(db) + " dB" }

func meterPercent(ratio float32) string {
	if ratio < 0 {
		return meterNoFigure
	}

	return strconv.Itoa(int(clamp(ratio, 0, 1)*100+0.5)) + "%"
}

// levelBarLayout stretches the track across the slot, the fill across the level,
// and stands the marker at the threshold.
type levelBarLayout struct{ level, threshold float32 }

func (l *levelBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 3 {
		return
	}

	height := theme.Sizes.SettingsLevelHeight
	y := (size.Height - height) / 2

	objects[0].Resize(fyne.NewSize(size.Width, height))
	objects[0].Move(fyne.NewPos(0, y))

	// No floor under the fill, unlike the usage bar: silence is a real reading
	// here and drawing a stub for it would say the microphone is hearing
	// something.
	objects[1].Resize(fyne.NewSize(size.Width*l.level, height))
	objects[1].Move(fyne.NewPos(0, y))

	width := theme.Sizes.SettingsLevelMarker
	objects[2].Resize(fyne.NewSize(width, height))
	objects[2].Move(fyne.NewPos(min(size.Width*l.threshold, size.Width-width), y))
}

func (l *levelBarLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, theme.Sizes.SettingsLevelHeight)
}

// usageBarLayout stretches the track across the slot and the fill across its
// share of it.
type usageBarLayout struct{ ratio float32 }

func (l *usageBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}

	height := theme.Sizes.SettingsUsageHeight
	y := (size.Height - height) / 2

	objects[0].Resize(fyne.NewSize(size.Width, height))
	objects[0].Move(fyne.NewPos(0, y))

	// A sliver of fill is still a shape: a cache holding a handful of avatars
	// against a half-gigabyte budget would otherwise draw as an empty track.
	width := max(size.Width*l.ratio, height)
	objects[1].Resize(fyne.NewSize(min(width, size.Width), height))
	objects[1].Move(fyne.NewPos(0, y))
}

func (l *usageBarLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, theme.Sizes.SettingsUsageHeight)
}

/* The rail */

// newSettingsMarker is the bar saying a rail entry is open or a setting is on:
// transparent until something fills it, flush with the left edge of what it is
// stacked over. It hands back the rectangle to fill *and* the wrapper pinning it
// left — the caller keeps the first and mounts the second. Inset vertically
// rather than full height: everything it is laid over has rounded corners, and a
// bar reaching into one squares it off.
func newSettingsMarker() (*canvas.Rectangle, fyne.CanvasObject) {
	marker := canvas.NewRectangle(color.Transparent)
	marker.SetMinSize(fyne.NewSize(theme.Sizes.SelectionMarkerWidth, 0))
	marker.CornerRadius = theme.Sizes.SelectionMarkerWidth / 2

	inset := theme.Sizes.SettingsGroupRadius

	return marker, HBoxNoSpacing(NewInset(marker, inset, inset, 0, 0))
}

// settingsRailButton is one entry in the rail — a section, or one group of the
// open one — as its mark, its name, a fill and the bar saying it is open.
// TappableContainer gives the hover for free but not the selection, which has to
// survive the pointer leaving.
type settingsRailButton struct {
	tapBase

	background *canvas.Rectangle
	marker     *canvas.Rectangle
	label      *canvas.Text
	content    fyne.CanvasObject
	selected   bool
}

var (
	_ fyne.Tappable     = (*settingsRailButton)(nil)
	_ desktop.Hoverable = (*settingsRailButton)(nil)
)

// newSettingsRailButton is a section: its icon, then its name.
func newSettingsRailButton(entry railEntry, selected bool, onTap func()) *settingsRailButton {
	return newRailButton(entry.title, entry.icon, selected, onTap)
}

// newSettingsSubButton is one group of the open section, listed under it. No icon
// — a group has no mark of its own — so the space one would take is the indent.
func newSettingsSubButton(title string, selected bool, onTap func()) *settingsRailButton {
	return newRailButton(title, nil, selected, onTap)
}

func newRailButton(title string, icon fyne.Resource, selected bool, onTap func()) *settingsRailButton {
	b := &settingsRailButton{background: canvas.NewRectangle(color.Transparent)}
	b.onTap = onTap
	b.background.CornerRadius = theme.Sizes.SettingsGroupRadius

	b.label = newText(title, theme.Colors.CategoryText, theme.Sizes.SettingsRailTextSize)

	// A sub-entry keeps the width an icon would take and is indented past it, so
	// its name starts clear of the section names rather than in the same column.
	indent := theme.Sizes.ChipPaddingH
	var lead fyne.CanvasObject = HorizontalSpacer(theme.Sizes.SettingsIconSize)
	if icon != nil {
		lead = container.NewCenter(newScaledIcon(icon, theme.Sizes.SettingsIconSize))
	} else {
		indent += theme.Sizes.SettingsPreviewGap
	}

	row := HBoxNoSpacing(
		lead,
		HorizontalSpacer(theme.Sizes.SettingsPreviewGap),
		container.NewCenter(b.label),
	)

	var markerRow fyne.CanvasObject
	b.marker, markerRow = newSettingsMarker()

	b.content = NewMinHeightContainer(theme.Sizes.SettingsRailRowHeight,
		container.NewStack(b.background, markerRow, NewInset(row, 0, 0, indent, 0)))
	b.ExtendBaseWidget(b)

	b.setSelected(selected)

	return b
}

// setSelected repaints in place. The rail is not rebuilt to move the selection:
// following the pane's scroll would replace every button several times a second,
// including the one under the pointer — which then never hears MouseOut.
func (b *settingsRailButton) setSelected(selected bool) {
	b.selected = selected

	b.label.Color = theme.Colors.CategoryText
	b.background.FillColor = color.Transparent
	b.marker.FillColor = color.Transparent

	if selected {
		b.label.Color = theme.Colors.TextPrimary
		b.background.FillColor = theme.Colors.ChannelSelectedBg
		b.marker.FillColor = theme.Colors.TextPrimary
	}

	b.label.Refresh()
	b.background.Refresh()
	b.marker.Refresh()
}

func (b *settingsRailButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.content)
}

func (b *settingsRailButton) MouseIn(*desktop.MouseEvent) {
	if b.selected {
		return
	}

	b.background.FillColor = theme.Colors.TappableHoverBg
	b.background.Refresh()
}

func (b *settingsRailButton) MouseOut() {
	if b.selected {
		return
	}

	b.background.FillColor = color.Transparent
	b.background.Refresh()
}

/* Dismissal */

// dismissSink is the full-bleed surface behind a popover, taking the next click
// anywhere else. It draws nothing — a picker is not modal enough to earn a dimmed
// window — and keeps the ordinary cursor, so the page still reads as reachable.
type dismissSink struct {
	tapBase

	content fyne.CanvasObject
}

var (
	_ fyne.Tappable      = (*dismissSink)(nil)
	_ desktop.Cursorable = (*dismissSink)(nil)
)

func newDismissSink(onTap func()) *dismissSink {
	s := &dismissSink{content: canvas.NewRectangle(color.Transparent)}
	s.onTap = onTap
	s.ExtendBaseWidget(s)

	return s
}

func (s *dismissSink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

func (s *dismissSink) Cursor() desktop.Cursor { return desktop.DefaultCursor }
