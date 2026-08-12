package ui

// The notification system. One vocabulary — Tone — behind two presentations:
// NoticeStack, a transient message the user need not answer, and ConfirmDialog,
// a modal question they must. Anything that reports an outcome or asks before
// doing something irreversible goes through one of the two, so the client has a
// single look for "this went wrong" and "are you sure".

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/config"
	"RGOClient/internal/ui/theme"
)

/* Tones */

// Tone is what a notice or a confirmation is about. It is the only thing that
// decides colour, icon, and button weight, so a caller says what it means and
// never how it should look.
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

// title is the heading a notice carries when its caller gave none. It says what
// kind of thing happened, which a card of body text alone never does at a
// glance.
func (t Tone) title() string {
	switch t {
	case ToneWarning:
		return "Heads up"
	case ToneDanger:
		return "That didn't work"
	}

	return "Done"
}

// importance is how a confirming button paints itself. Fyne reads the fill off
// the theme's error/warning/primary colours, which AppTheme maps to the same
// tones as Color.
func (t Tone) importance() widget.Importance {
	switch t {
	case ToneWarning:
		return widget.WarningImportance
	case ToneDanger:
		return widget.DangerImportance
	}

	return widget.HighImportance
}

/* Transient notices */

// NoticeStack is the layer transient messages appear on: a corner of cards, each
// on its own timer, dismissed early by clicking it.
//
// Layer is stacked over the main layout like a Tooltip's, and for the same
// reason — a canvas overlay would take the whole hit test, and a message nobody
// has to answer must not block the client. Nothing in the layer matches a
// pointer event except a card itself, so clicks elsewhere reach the UI beneath.
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

	// Pinned to the top right: the bottom of the message area belongs to the
	// composer, and a notice must never land on what the user is typing. The stack
	// of cards is the layer's own business, so NewLayer keeps it out of the window's
	// minimum size — without it a fourth notice made the window taller.
	layer := NewLayer(container.NewBorder(nil, nil, nil, NewInset(list, margin, margin, 0, margin)))

	return &NoticeStack{Layer: layer, list: list}
}

// Push puts a message on the layer under its tone's own heading. Call on the UI
// thread.
func (n *NoticeStack) Push(tone Tone, text string) {
	n.PushNotice(Notice{Tone: tone, Body: text})
}

// PushNotice puts a message on the layer for its configured lifetime, dropping
// the oldest when the stack is full. A tone the user has switched off is dropped
// here rather than at the call sites, so nothing has to ask before it reports.
// Call on the UI thread.
func (n *NoticeStack) PushNotice(notice Notice) {
	settings := config.Current().Notifications
	if notice.Body == "" || !notice.Tone.enabled(settings) {
		return
	}
	if notice.Title == "" {
		notice.Title = notice.Tone.title()
	}

	lifetime := settings.Lifetime()

	// The card has to exist before the dismissal that removes it, and the
	// dismissal before the card that carries it — hence the declaration first.
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
// heading, the sentence under it, a close button, and a bar along the bottom
// that drains over the notice's lifetime.
//
// The bar is the part that earns its keep. A card that simply vanishes gives no
// warning that it is about to, so anything read slowly is read twice; a bar
// running out says how long is left without saying anything.
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

	title := canvas.NewText(notice.Title, tint)
	title.TextSize = theme.Sizes.NoticeTitleSize
	title.TextStyle = fyne.TextStyle{Bold: true}

	body := widget.NewLabel(notice.Body)
	body.Wrapping = fyne.TextWrapWord

	padV, padH := theme.Sizes.NoticePaddingV, theme.Sizes.NoticePaddingH
	gap := theme.Sizes.NoticeCardSpacing
	inner := theme.Sizes.NoticeWidth - padH*2 - theme.Sizes.NoticeEdgeWidth

	bar, countdown := newCountdownBar(tint, inner, lifetime)

	// Both the glyph and the close button hang from the top of the card rather
	// than centring on it: a two-line body would otherwise push the mark that
	// says what kind of message this is into the middle of the sentence.
	head := NewFillRow(2,
		topAligned(theme.Sizes.NoticeIconSize, theme.Sizes.NoticeTitleSize,
			newScaledIcon(notice.Tone.icon(), theme.Sizes.NoticeIconSize)),
		HorizontalSpacer(gap),
		VBoxNoSpacing(title, VerticalSpacer(theme.Sizes.NoticeTitleGap), newFlushContainer(body)),
		HorizontalSpacer(gap),
		topAligned(closeButtonSize, theme.Sizes.NoticeTitleSize, NewCloseButton(onDismiss)),
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

// newCountdownBar is the draining bar and the animation that drains it. The
// width is passed in rather than measured: the card is a fixed width, and an
// animation cannot wait for a layout pass to learn how far it has to travel.
func newCountdownBar(tint color.Color, width float32, lifetime time.Duration) (fyne.CanvasObject, *fyne.Animation) {
	height := theme.Sizes.NoticeCountdown

	bar := canvas.NewRectangle(tint)
	bar.CornerRadius = height / 2
	bar.Resize(fyne.NewSize(width, height))

	// Positioned rather than laid out: a layout would put the width back every
	// time the card was refreshed, which is once per frame of the animation.
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
// action, cancelling, and the close button — so the caller never has to.
func NewConfirmDialog(confirm Confirm, onClose func()) fyne.CanvasObject {
	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.ConfirmRadius

	body := widget.NewLabel(confirm.Body)
	body.Wrapping = fyne.TextWrapWord

	action := widget.NewButton(confirm.Action, func() {
		onClose()
		if confirm.OnConfirm != nil {
			confirm.OnConfirm()
		}
	})
	action.Importance = confirm.Tone.importance()

	// Two halves of the card's width, cancel first and plain.
	//
	// Full width rather than a pair in the corner: the two answers to one question
	// should be the same size and in the same place every time, so the dialog is
	// answered by position rather than by reading a small label — and the weighted
	// one is still the only thing carrying colour, so which is destructive is read
	// off that rather than off which is easier to hit.
	buttons := container.NewGridWithColumns(2, widget.NewButton("Cancel", onClose), action)

	inner := container.NewVBox(
		confirmHeader(confirm, onClose),
		widget.NewSeparator(),
		body,
		VerticalSpacer(theme.Sizes.ConfirmButtonGap),
		buttons,
	)

	return newTapSink(container.NewStack(card,
		NewFixedWidthContainer(theme.Sizes.ConfirmWidth, container.NewPadded(inner))))
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
