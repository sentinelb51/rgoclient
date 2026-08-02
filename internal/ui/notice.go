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
	"fyne.io/fyne/v2/layout"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
)

const (
	// noticeLifetime is how long a notice stays up before taking itself down.
	// Long enough to read a sentence, short enough not to sit over the messages.
	noticeLifetime = 6 * time.Second

	// noticeCap bounds the stack. A burst of failures — every message in a
	// channel refusing to delete, say — must not paper over the client.
	noticeCap = 3
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

// NewNoticeStack builds an empty notice layer.
func NewNoticeStack() *NoticeStack {
	list := container.NewVBox()
	margin := theme.Sizes.NoticeStackMargin

	// Pinned to the top right: the bottom of the message area belongs to the
	// composer, and a notice must never land on what the user is typing.
	layer := container.NewBorder(nil, nil, nil, NewInset(list, margin, margin, 0, margin))

	return &NoticeStack{Layer: layer, list: list}
}

// Push puts a message on the layer for noticeLifetime, dropping the oldest when
// the stack is full. Call on the UI thread.
func (n *NoticeStack) Push(tone Tone, text string) {
	if text == "" {
		return
	}

	// The card has to exist before the dismissal that removes it, and the
	// dismissal before the card that carries it — hence the declaration first.
	var card fyne.CanvasObject
	dismiss := func() { n.remove(card) }
	card = newNoticeCard(tone, text, dismiss)

	n.list.Add(card)
	for len(n.list.Objects) > noticeCap {
		n.list.Remove(n.list.Objects[0])
	}
	n.Layer.Refresh() // Add alone lays the list out without repainting it

	time.AfterFunc(noticeLifetime, func() { DoOnUI(dismiss) })
}

// Clear takes every notice down at once, for when what they were about is no
// longer on screen. Call on the UI thread.
func (n *NoticeStack) Clear() {
	if len(n.list.Objects) == 0 {
		return
	}

	n.list.Objects = nil
	n.Layer.Refresh()
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

// newNoticeCard builds one message: a tone-coloured edge, the matching glyph,
// and the text. The whole card is tappable, dismissing it.
func newNoticeCard(tone Tone, text string, onTap func()) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = theme.Sizes.NoticeRadius

	// Rounded like the card behind it, so the edge follows its corners rather
	// than squaring them off.
	edge := canvas.NewRectangle(tone.Color())
	edge.CornerRadius = theme.Sizes.NoticeRadius
	edge.SetMinSize(fyne.NewSize(theme.Sizes.NoticeEdgeWidth, 0))

	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord

	padV, padH := theme.Sizes.NoticePaddingV, theme.Sizes.NoticePaddingH
	icon := container.NewCenter(newScaledIcon(tone.icon(), theme.Sizes.NoticeIconSize))
	row := container.NewBorder(nil, nil, container.NewHBox(HorizontalSpacer(padH), icon), nil, label)

	card := container.NewStack(background, container.NewHBox(edge), NewInset(row, padV, padV, 0, padH))

	// Fixed rather than minimum width: the label wraps to the width it is given,
	// and a long sentence must widen nothing.
	return NewFixedWidthContainer(theme.Sizes.NoticeWidth, NewTappableContainer(card, onTap))
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

	// Cancel comes first and stays plain: the weighted button is the one that
	// does something irreversible, so it should be the one you have to aim at.
	buttons := container.NewHBox(layout.NewSpacer(), widget.NewButton("Cancel", onClose), action)

	inner := container.NewVBox(
		confirmHeader(confirm, onClose),
		widget.NewSeparator(),
		body,
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
