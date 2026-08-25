package ui

// The notification system: one vocabulary (Tone) behind three presentations —
// NoticeStack, a transient message in the corner needing no answer; ModalNotice,
// the same message taking the middle of the window when it is worth stopping for;
// and ConfirmDialog, a modal question that has to be answered. Everything
// reporting an outcome or asking before something irreversible goes through one,
// so the client has one look for each.

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/config"
	"RGOClient/internal/ui/theme"
)

/* Tones */

// Tone is what a notice or confirmation is about — the only thing deciding
// colour, icon and button weight, so a caller says what it means and never how it
// should look.
type Tone int

const (
	ToneInfo    Tone = iota // something happened, nothing is wrong
	ToneWarning             // proceed carefully; the effect is disruptive
	ToneDanger              // destructive, irreversible, or outright failed
)

// Color is the tone's accent: a notice's edge and icon, and the fill of the
// button that carries the action out.
func (t Tone) Color() color.Color {
	switch t {
	case ToneWarning:
		return theme.Colors.NoticeWarning
	case ToneDanger:
		return theme.Colors.NoticeDanger
	}

	return theme.Colors.NoticeInfo
}

// icon is the tone's glyph, tinted through the theme colour Fyne already maps to
// the same palette entry Color returns.
func (t Tone) icon() fyne.Resource {
	switch t {
	case ToneWarning:
		return fynetheme.NewColoredResource(fynetheme.WarningIcon(), fynetheme.ColorNameWarning)
	case ToneDanger:
		return fynetheme.NewColoredResource(fynetheme.ErrorIcon(), fynetheme.ColorNameError)
	}

	return fynetheme.NewColoredResource(fynetheme.InfoIcon(), fynetheme.ColorNamePrimary)
}

// title is the heading a notice takes when its caller gave none: what kind of
// thing happened, which body text alone never says at a glance.
func (t Tone) title() string {
	switch t {
	case ToneWarning:
		return "Heads up"
	case ToneDanger:
		return "That didn't work"
	}

	return "Done"
}

// weight is how a button carrying the tone fills itself — the colour Color
// already returns, a confirming button and the notice reporting what it did
// being one statement made twice.
func (t Tone) weight() ButtonWeight {
	switch t {
	case ToneWarning:
		return ButtonWarning
	case ToneDanger:
		return ButtonDanger
	}

	return ButtonPrimary
}

/* Transient notices */

// NoticeStack is the layer transient messages appear on: a corner of cards, each
// on its own timer, dismissed early by a click. Layer is stacked over the main
// layout like a Tooltip's and for the same reason — a canvas overlay takes the
// whole hit test, and a message nobody has to answer must not block the client.
type NoticeStack struct {
	Layer *fyne.Container // stack this over the main layout

	list *fyne.Container // the cards, oldest first
}

// Notice is one transient message: what kind of thing happened, a heading, and
// the sentence under it.
type Notice struct {
	Tone  Tone
	Title string // "" takes the tone's own
	Body  string
}

// NewNoticeStack builds an empty notice layer.
func NewNoticeStack() *NoticeStack {
	list := container.NewVBox()
	margin := theme.Sizes.NoticeStackMargin

	// Top right: the bottom of the message area belongs to the composer, and a
	// notice must never land on what is being typed. NewLayer keeps the stack out of
	// the window's minimum — without it a fourth notice made the window taller.
	layer := NewLayer(container.NewBorder(nil, nil, nil, NewInset(list, margin, margin, 0, margin)))

	return &NoticeStack{Layer: layer, list: list}
}

// Push puts a message on the layer under its tone's own heading. Call on the UI
// thread.
func (n *NoticeStack) Push(tone Tone, text string) {
	n.PushNotice(Notice{Tone: tone, Body: text})
}

// PushNotice puts a message on the layer for its configured lifetime, dropping
// the oldest when the stack is full. A tone switched off is dropped here rather
// than at the call sites, so nothing has to ask before it reports. Call on the UI
// thread.
func (n *NoticeStack) PushNotice(notice Notice) {
	settings := config.Current().Notifications
	if notice.Body == "" || !notice.Tone.enabled(settings) {
		return
	}
	if notice.Title == "" {
		notice.Title = notice.Tone.title()
	}

	lifetime := settings.Lifetime()

	// The card must exist before the dismissal that removes it, and the dismissal
	// before the card carrying it — hence the declaration first.
	var card *noticeCard
	dismiss := func() {
		card.stop()
		n.remove(card)
	}
	card = newNoticeCard(notice, lifetime, dismiss)

	n.list.Add(card)
	for len(n.list.Objects) > settings.MaxStacked {
		oldest := n.list.Objects[0]
		stopCountdown(oldest)
		n.list.Remove(oldest)
	}
	n.Layer.Refresh() // Add alone lays the list out without repainting it

	time.AfterFunc(lifetime, func() { DoOnUI(dismiss) })
}

// enabled reports whether notices of this tone are wanted.
func (t Tone) enabled(settings config.Notifications) bool {
	switch t {
	case ToneWarning:
		return settings.ShowWarning
	case ToneDanger:
		return settings.ShowDanger
	}

	return settings.ShowInfo
}

// Clear takes every notice down at once, for when what they were about is no
// longer on screen. Call on the UI thread.
func (n *NoticeStack) Clear() {
	if len(n.list.Objects) == 0 {
		return
	}

	for _, card := range n.list.Objects {
		stopCountdown(card)
	}

	n.list.Objects = nil
	n.Layer.Refresh()
}

// stopCountdown halts a card's timer bar. A card taken down early would
// otherwise keep an animation running against a rectangle nothing draws.
func stopCountdown(object fyne.CanvasObject) {
	if card, ok := object.(*noticeCard); ok {
		card.stop()
	}
}

// remove takes one card down. Harmless once it is already gone, which is the
// normal case for a card the user dismissed before its timer ran out.
func (n *NoticeStack) remove(card fyne.CanvasObject) {
	before := len(n.list.Objects)
	n.list.Remove(card)

	if len(n.list.Objects) != before {
		n.Layer.Refresh()
	}
}

// noticeCard is one message on the layer: a tone-coloured edge and glyph, a
// heading, the sentence, a close button, and a bar draining over its lifetime.
// The bar earns its keep — a card that simply vanishes gives no warning it is
// about to, so anything read slowly is read twice.
type noticeCard struct {
	tapBase

	content   fyne.CanvasObject
	countdown *fyne.Animation
}

var _ fyne.Tappable = (*noticeCard)(nil)

// newNoticeCard builds the card and starts its countdown. The whole card is
// tappable, dismissing it; the close button inside wins the tap, being deeper,
// and does the same thing.
func newNoticeCard(notice Notice, lifetime time.Duration, onDismiss func()) *noticeCard {
	tint := notice.Tone.Color()

	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = theme.Sizes.NoticeRadius
	Outline(background)

	// Rounded like the card behind it, so the edge follows its corners rather
	// than squaring them off.
	edge := canvas.NewRectangle(tint)
	edge.CornerRadius = theme.Sizes.NoticeRadius
	edge.SetMinSize(fyne.NewSize(theme.Sizes.NoticeEdgeWidth, 0))

	title := newBoldText(notice.Title, tint, theme.Sizes.NoticeTitleSize)

	body := widget.NewLabel(notice.Body)
	body.Wrapping = fyne.TextWrapWord

	padV, padH := theme.Sizes.NoticePaddingV, theme.Sizes.NoticePaddingH
	gap := theme.Sizes.NoticeCardSpacing
	inner := theme.Sizes.NoticeWidth - padH*2 - theme.Sizes.NoticeEdgeWidth

	bar, countdown := newCountdownBar(tint, inner, lifetime)

	// Glyph and close button hang from the top rather than centring: a two-line body
	// would otherwise push the mark into the middle of the sentence.
	head := NewFillRow(2,
		topAligned(theme.Sizes.NoticeIconSize, theme.Sizes.NoticeTitleSize,
			newScaledIcon(notice.Tone.icon(), theme.Sizes.NoticeIconSize)),
		HorizontalSpacer(gap),
		VBoxNoSpacing(title, VerticalSpacer(theme.Sizes.NoticeTitleGap), newFlushContainer(body)),
		HorizontalSpacer(gap),
		topAligned(glyphButtonSize, theme.Sizes.NoticeTitleSize, NewCloseButton(onDismiss)),
	)

	c := &noticeCard{countdown: countdown}
	c.content = NewFixedWidthContainer(theme.Sizes.NoticeWidth, container.NewStack(
		background,
		container.NewHBox(edge),
		NewInset(VBoxNoSpacing(head, VerticalSpacer(gap), bar),
			padV, padV, padH+theme.Sizes.NoticeEdgeWidth, padH),
	))
	c.onTap = onDismiss
	c.ExtendBaseWidget(c)

	countdown.Start()

	return c
}

func (c *noticeCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

// stop halts the countdown. Safe to call more than once, which it is: a card
// dismissed by hand is also still on a timer.
func (c *noticeCard) stop() {
	if c != nil && c.countdown != nil {
		c.countdown.Stop()
	}
}

// newCountdownBar is the draining bar and the animation draining it. The width is
// passed in rather than measured: the card is fixed, and an animation cannot wait
// for a layout pass to learn how far it travels.
func newCountdownBar(tint color.Color, width float32, lifetime time.Duration) (fyne.CanvasObject, *fyne.Animation) {
	height := theme.Sizes.NoticeCountdown

	bar := canvas.NewRectangle(tint)
	bar.CornerRadius = height / 2
	bar.Resize(fyne.NewSize(width, height))

	// Positioned rather than laid out: a layout would put the width back on every
	// refresh, which is once per frame of the animation.
	strip := NewMinHeightContainer(height, container.NewWithoutLayout(bar))

	animation := canvas.NewSizeAnimation(
		fyne.NewSize(width, height), fyne.NewSize(0, height), lifetime,
		func(size fyne.Size) {
			bar.Resize(size)
			canvas.Refresh(bar) // Resize alone marks nothing dirty
		})

	return strip, animation
}

// topAligned pins obj to the top of the row it is in, centred on a line of text
// of the given size rather than on the row's whole height.
func topAligned(width, lineSize float32, obj fyne.CanvasObject) fyne.CanvasObject {
	offset := max((lineHeight(lineSize)-obj.MinSize().Height)/2, 0)

	return container.New(&columnLayout{width: width, topOffset: offset, collapse: true}, obj)
}

/* The centred notice */

// ModalNotice is the middle of the window, borrowed for a moment: one card, a
// tone mark over a line, gone when its time is up. What it is for is a message
// that has to be *read* rather than glanced at — a login refused, an action
// nothing else on screen would report — and the surfaces with no notice layer at
// all, the login and second-factor screens, which are drawn before the main UI
// exists.
//
// Its Layer is stacked over the content rather than mounted as a canvas overlay,
// which is what makes it float rather than take over: an overlay takes the whole
// hit test, so the client would stop answering the pointer for as long as a
// message nobody has to answer was up. Only the card itself takes a click, and
// that click dismisses it.
//
// Nothing here consults the notice-layer tone switches. Those name that layer,
// and a caller reaching for this one is saying the message must be seen.
type ModalNotice struct {
	Layer *fyne.Container

	slot *fyne.Container // holds the card; empty while nothing is up
	seq  int             // drops a timer whose card has already been replaced
}

// NewModalNotice builds the layer with nothing on it.
func NewModalNotice() *ModalNotice {
	slot := container.New(layout.NewStackLayout())

	return &ModalNotice{Layer: NewLayer(container.NewCenter(slot)), slot: slot}
}

// Show puts a notice in the middle of the window for the configured duration,
// replacing whatever was there. Call on the UI thread.
func (m *ModalNotice) Show(notice Notice) {
	if notice.Title == "" && notice.Body == "" {
		return
	}
	if notice.Title == "" {
		notice.Title = notice.Tone.title()
	}

	// The card is replaced rather than queued: two of these stacked would cover
	// each other, and the newer message is the one that is still true.
	m.Clear()

	m.seq++
	shown := m.seq

	card := newModalNoticeCard(notice, m.Dismiss)
	m.slot.Objects = []fyne.CanvasObject{card}
	m.Layer.Refresh()
	card.fade(fadeIn, nil)

	// The hold is what the reader gets, so the card is *gone* at the end of it
	// rather than only starting to leave.
	hold := max(config.Current().Notifications.ModalLifetime()-modalFadeDuration, 0)
	time.AfterFunc(hold, func() {
		DoOnUI(func() {
			if m.seq == shown {
				m.Dismiss()
			}
		})
	})
}

// Dismiss fades the card out and takes it down behind itself — what a tap on it
// does, and what its own timer does. Safe when nothing is up. Call on the UI
// thread.
func (m *ModalNotice) Dismiss() {
	card := m.card()
	if card == nil {
		return
	}

	m.seq++
	going := m.seq

	card.fade(fadeOut, func() {
		if m.seq == going {
			m.Clear()
		}
	})
}

// Clear takes the card down now, animation and all — for a caller with something
// else to say, and for one that has decided the message no longer applies. Safe
// when nothing is up. Call on the UI thread.
func (m *ModalNotice) Clear() {
	card := m.card()
	if card == nil {
		return
	}

	card.stop()
	m.slot.Objects = nil
	m.Layer.Refresh()
}

// card is what is up, or nil.
func (m *ModalNotice) card() *modalNoticeCard {
	if len(m.slot.Objects) == 0 {
		return nil
	}

	card, _ := m.slot.Objects[0].(*modalNoticeCard)

	return card
}

// The fade the card arrives and leaves on. Quantised deliberately: Fyne caches a
// rendered line of text under a key its *colour* is part of, so every distinct
// alpha is a texture of its own. Eight steps means eight, shared by every notice
// the client ever draws, where a smooth ramp would mint a dozen per word per fade.
const (
	modalFadeDuration = 180 * time.Millisecond
	modalFadeSteps    = 8
)

// fadeDirection is which way a fade runs; the animation reports its progress the
// same way either way.
type fadeDirection bool

const (
	fadeIn  fadeDirection = true
	fadeOut fadeDirection = false
)

// modalNoticeCard is that card: the tone's mark, the heading under it, and the
// sentence under that where there is one. Centred throughout — it is read at a
// glance and there is no second column to line anything up with.
//
// Everything on it is a canvas object with a colour this can scale, which is why
// the body is wrapped by hand: a widget.RichText carries a theme *name* rather
// than a colour, and a name cannot be faded.
type modalNoticeCard struct {
	tapBase
	content fyne.CanvasObject

	background *canvas.Rectangle
	mark       *canvas.Image
	texts      []*canvas.Text

	// full is each text's colour at rest, in the order of texts, since a fade has
	// to scale from the colour rather than towards a remembered one.
	full []color.Color

	animation *fyne.Animation
	step      int // the alpha step currently drawn, so a repeat costs nothing
}

// noStep is an alpha step no fraction maps to, so a card's first setAlpha always
// writes — a zero would read as "already drawn at nothing" and leave the card at
// full strength for the fade to jump from.
const noStep = -1

var (
	_ fyne.Tappable      = (*modalNoticeCard)(nil)
	_ desktop.Cursorable = (*modalNoticeCard)(nil)
)

func newModalNoticeCard(notice Notice, onDismiss func()) *modalNoticeCard {
	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = theme.Sizes.ModalNoticeRadius
	Outline(background)
	Elevate(background)

	title := newBoldText(notice.Title, theme.Colors.TextPrimary, theme.Sizes.ModalNoticeTitle)
	title.Alignment = fyne.TextAlignCenter

	mark := newScaledIcon(notice.Tone.icon(), theme.Sizes.ModalNoticeMarkSize)

	c := &modalNoticeCard{background: background, mark: mark, texts: []*canvas.Text{title}, step: noStep}

	rows := []fyne.CanvasObject{
		container.NewCenter(mark),
		VerticalSpacer(theme.Sizes.ModalNoticeMarkGap),
		title,
	}
	if notice.Body != "" {
		body, lines := newModalBody(notice.Body, theme.Sizes.ModalNoticeWidth-theme.Sizes.ModalNoticePadding*2)
		rows = append(rows, VerticalSpacer(theme.Sizes.ModalNoticeBodyGap), body)
		c.texts = append(c.texts, lines...)
	}

	c.full = make([]color.Color, len(c.texts))
	for i, text := range c.texts {
		c.full[i] = text.Color
	}

	pad := theme.Sizes.ModalNoticePadding
	c.content = NewFixedWidthContainer(theme.Sizes.ModalNoticeWidth, container.NewStack(
		background,
		NewInset(VBoxNoSpacing(rows...), pad, pad, pad, pad),
	))
	c.onTap = onDismiss
	c.ExtendBaseWidget(c)
	c.setAlpha(0) // mounted invisible: the first frame of the fade is the first frame drawn

	return c
}

func (c *modalNoticeCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

// Cursor keeps the arrow: the card is dismissible, not a control, and a hand over
// a message that is about to leave on its own says the wrong thing.
func (c *modalNoticeCard) Cursor() desktop.Cursor { return desktop.DefaultCursor }

// fade runs the card in or out, calling done once it has arrived or gone. Any
// fade already running is dropped, so a card dismissed while it is still arriving
// leaves from wherever it got to.
//
// The end is the tick reporting 1: the runner calls it with exactly that once the
// duration is up and never again, which is the only completion hook a
// fyne.Animation has. The ticks run in the driver's own loop, on the thread that
// paints — the same one DoOnUI hands work to.
func (c *modalNoticeCard) fade(direction fadeDirection, done func()) {
	c.stop()

	c.animation = fyne.NewAnimation(modalFadeDuration, func(progress float32) {
		strength := progress
		if direction == fadeOut {
			strength = 1 - progress
		}

		c.setAlpha(strength)

		if progress < 1 {
			return
		}

		c.animation = nil
		if done != nil {
			done()
		}
	})
	c.animation.Curve = fyne.AnimationEaseInOut
	c.animation.Start()
}

// stop halts a running fade where it stands, without finishing it. Safe more than
// once, which it is: a card taken down by hand is also on a timer.
func (c *modalNoticeCard) stop() {
	if c.animation == nil {
		return
	}

	animation := c.animation
	c.animation = nil
	animation.Stop()
}

// setAlpha draws the whole card at a fraction of its strength, quantised to
// modalFadeSteps. Everything is scaled together — the fill, the outline, the
// shadow, the mark and every line of text — so the card dissolves as one object
// rather than in pieces.
func (c *modalNoticeCard) setAlpha(fraction float32) {
	step := int(min(max(fraction, 0), 1)*modalFadeSteps + 0.5)
	if step == c.step {
		return
	}
	c.step = step

	scale := float32(step) / modalFadeSteps

	c.background.FillColor = dissolve(theme.Colors.NoticeBg, scale)
	c.background.StrokeColor = dissolve(theme.Colors.Outline, scale)
	c.background.Shadow.Color = dissolve(theme.Colors.CardShadow, scale)
	c.background.Refresh()

	c.mark.Translucency = float64(1 - scale)
	c.mark.Refresh()

	for i, text := range c.texts {
		text.Color = dissolve(c.full[i], scale)
		text.Refresh()
	}
}

// dissolve scales a colour towards nothing. Every channel, not the alpha alone:
// what Color.RGBA hands back is premultiplied, so a colour whose alpha alone is
// scaled composites brighter than it should — the trap theme.Fade names. RGBA64
// is what holds that result without dividing the alpha back out, and it is
// comparable, which the texture cache keying on it requires.
func dissolve(fill color.Color, scale float32) color.Color {
	r, g, b, a := fill.RGBA()
	channel := func(v uint32) uint16 { return uint16(float32(v) * scale) }

	return color.RGBA64{R: channel(r), G: channel(g), B: channel(b), A: channel(a)}
}

// newModalBody is a modal's sentence: centred, quieter than the title above it,
// and wrapped here rather than by a widget — a widget.RichText is what wraps text
// in Fyne and it carries a theme colour *name*, which a fade cannot move. The
// lines come back alongside the column so a caller that fades can reach them.
func newModalBody(text string, width float32) (fyne.CanvasObject, []*canvas.Text) {
	wrapped := wrapText(text, width, fynetheme.TextSize(), fyne.TextStyle{})

	lines := make([]*canvas.Text, len(wrapped))
	rows := make([]fyne.CanvasObject, len(wrapped))
	for i, line := range wrapped {
		lines[i] = newText(line, theme.Colors.ModalBodyText, 0)
		lines[i].Alignment = fyne.TextAlignCenter
		rows[i] = lines[i]
	}

	return VBoxNoSpacing(rows...), lines
}

// wrapText breaks text into lines no wider than width, at spaces where it can and
// mid-word where it cannot — a transport error carries URLs, which no space will
// ever break and which would otherwise draw past the card holding them.
func wrapText(text string, width, size float32, style fyne.TextStyle) []string {
	var lines []string

	for _, word := range strings.Fields(text) {
		if len(lines) > 0 {
			joined := lines[len(lines)-1] + " " + word
			if fyne.MeasureText(joined, size, style).Width <= width {
				lines[len(lines)-1] = joined
				continue
			}
		}

		lines = append(lines, breakWord(word, width, size, style)...)
	}

	return lines
}

// breakWord cuts one word into pieces that fit, for a word that never will.
func breakWord(word string, width, size float32, style fyne.TextStyle) []string {
	if fyne.MeasureText(word, size, style).Width <= width {
		return []string{word}
	}

	var lines []string
	line := ""
	for _, r := range word {
		next := line + string(r)
		if line != "" && fyne.MeasureText(next, size, style).Width > width {
			lines = append(lines, line)
			line = string(r)

			continue
		}

		line = next
	}

	return append(lines, line)
}

/* Confirmations */

// Confirm describes a question the user has to answer before something that
// cannot be undone happens. Everything about the dialog's look comes from Tone,
// so callers only supply words.
type Confirm struct {
	Title  string // what is about to happen: "Leave server"
	Body   string // its consequence, in a sentence
	Action string // the confirming button: "Leave"
	Tone   Tone

	// OnConfirm runs on the UI thread, after the dialog has closed. Nil makes the
	// dialog a plain acknowledgement.
	OnConfirm func()
}

// NewConfirmDialog builds the card for a Confirm, to be shown on the modal
// layer. onClose dismisses that layer and is called on every way out — the
// action and cancelling — so the caller never has to.
//
// Drawn as the centred notice is, one column down the middle: a question is
// three things (what, what it costs, the two answers) and a heading row with a
// glyph at one end and a close button at the other made furniture of two of them.
// There is no close button because there is nothing here a Cancel does not
// already say, and no tone glyph because the confirming button already carries
// the tone — as a fill, which is the loudest thing on the card.
func NewConfirmDialog(confirm Confirm, onClose func()) fyne.CanvasObject {
	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.ConfirmRadius
	Outline(card)
	Elevate(card)

	title := newBoldText(confirm.Title, theme.Colors.TextPrimary, theme.Sizes.ConfirmTitleSize)
	title.Alignment = fyne.TextAlignCenter

	action := NewWeightedButton(confirm.Action, confirm.Tone.weight(), func() {
		onClose()
		if confirm.OnConfirm != nil {
			confirm.OnConfirm()
		}
	})

	rows := []fyne.CanvasObject{
		title,
		VerticalSpacer(theme.Sizes.ConfirmTitleGap),
		confirmBody(confirm.Body),
		VerticalSpacer(theme.Sizes.ConfirmButtonGap),
		confirmButtons(confirm, action, onClose),
	}
	if hint := confirmHint(confirm); hint != nil {
		rows = append(rows, VerticalSpacer(theme.Sizes.ConfirmTitleGap), hint)
	}

	pad := theme.Sizes.ConfirmPadding

	return newTapSink(container.NewStack(card,
		NewFixedWidthContainer(theme.Sizes.ConfirmWidth, NewInset(VBoxNoSpacing(rows...), pad, pad, pad, pad))))
}

// confirmBody is the sentence under the title, wrapped to what the card leaves
// for it. It drops the lines newModalBody hands back: nothing fades here.
func confirmBody(text string) fyne.CanvasObject {
	body, _ := newModalBody(text, theme.Sizes.ConfirmWidth-theme.Sizes.ConfirmPadding*2)

	return body
}

// confirmButtons is how the card is answered. A question takes two halves of it,
// cancel first and plain — full width rather than a pair in the corner, so it is
// answered by position rather than by reading a small label, and the weighted one
// is the only thing carrying colour. A statement takes one button across the
// whole card: there is nothing to cancel, and a Cancel beside an OK that does the
// same thing is a choice that isn't one.
func confirmButtons(confirm Confirm, action *Button, onClose func()) fyne.CanvasObject {
	if confirm.OnConfirm == nil {
		return action
	}

	return container.NewGridWithColumns(2, NewButton("Cancel", onClose), action)
}

// confirmHint says how to skip the question next time — a shortcut nobody would
// find by trying. Drawn only where the key can be read (see ShiftHeld) and only
// on a question that does something, an acknowledgement having nothing to skip.
func confirmHint(confirm Confirm) fyne.CanvasObject {
	if confirm.OnConfirm == nil {
		return nil
	}

	return shiftSkipHint()
}

// shiftSkipHint is that line, for every card the key skips — the ban card asks
// for more than a yes and is skipped by the same hold. Nil where the key cannot
// be read (see ShiftHeld), so nothing offers a way out that would never work.
func shiftSkipHint() fyne.CanvasObject {
	if !shiftSkippable {
		return nil
	}

	hint := newText("Hold Shift to skip this confirmation.", theme.Colors.ConfirmHint, theme.Sizes.ConfirmHintSize)
	hint.Alignment = fyne.TextAlignCenter

	return hint
}

// confirmHeader is the dialog's title row: the tone's glyph, the title, and the
// close button.
func confirmHeader(confirm Confirm, onClose func()) fyne.CanvasObject {
	title := widget.NewLabelWithStyle(confirm.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Truncation = fyne.TextTruncateEllipsis

	icon := container.NewCenter(newScaledIcon(confirm.Tone.icon(), theme.Sizes.NoticeIconSize))

	// Both ends are centred so neither is stretched to the row height by the
	// border layout.
	return container.NewBorder(nil, nil, icon, container.NewCenter(NewCloseButton(onClose)), title)
}
