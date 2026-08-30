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

	"RGOClient/assets"
	"RGOClient/internal/cache"
	"RGOClient/internal/config"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
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

// icon is the tone's glyph in the tone's own colour. The client's marks rather
// than Fyne's: a themed resource carries a colour *name*, and the name an info
// mark would have to borrow is mapped to the accent a server row is selected
// with, not to NoticeInfo — so the glyph disagreed with the card carrying it.
func (t Tone) icon() fyne.Resource {
	res := assets.NoticeInfoIcon
	switch t {
	case ToneWarning:
		res = assets.NoticeWarningIcon
	case ToneDanger:
		res = assets.NoticeDangerIcon
	}

	return tintedIcon(res, t.Color())
}

// title is the heading a notice takes when its caller gave none: the outcome in
// a word, which is the most a tone alone can honestly say. It is never a
// pleasantry — a heading that carries no information is a line of the card spent
// on nothing, so a caller whose outcome these three would misname (a partial
// success, something that *did* happen and is merely worth knowing) supplies its
// own rather than falling through to here.
func (t Tone) title() string {
	switch t {
	case ToneWarning:
		return "Not done"
	case ToneDanger:
		return "Failed"
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

	images *cache.ImageCache // the faces a notice about a person is led by
	list   *fyne.Container   // the cards, oldest first

	// history is every notice raised this session, oldest first and bounded at
	// noticeHistoryDepth. A card is on screen for seconds and gone; this is the
	// only record that it ever said anything.
	history []NoticeRecord
}

// noticeHistoryDepth is how many past notices are kept. A const rather than a
// setting: it bounds a few kilobytes of strings nobody chose to spend, and a row
// asking how much of a log to keep is a question the reader should not have to
// have an opinion about.
const noticeHistoryDepth = 100

// NoticeRecord is one notice as the history holds it: what was said, and when.
// The notice keeps its OnTap — a card leading to the message that named you is
// still the way there an hour later, which is most of why a history is worth
// having.
type NoticeRecord struct {
	Notice Notice
	At     time.Time
}

// Notice is one transient message: what kind of thing happened, a heading, and
// the sentence under it. The heading is the card's first line and the only part
// of it read at a glance, so it says what happened in as few words as that takes
// — never a pleasantry, which spends the line and reports nothing.
//
// The rest is what a notice about a *person* needs, and is what separates a ping
// from a receipt: a face instead of the tone's disc, and somewhere to go.
type Notice struct {
	Tone  Tone
	Title string // "" takes the tone's own
	Body  string

	// AvatarURL replaces the tone's disc with the face at the leading edge, and
	// Initial is the letter it stands on until the picture lands — a message is
	// recognised by who sent it before its heading is read. Either alone is enough
	// to make the card a person's.
	AvatarURL string
	Initial   string

	// OnTap is where the card leads: the message that named you, the conversation
	// somebody wrote in. It runs on the UI thread and the card goes with it. Nil
	// leaves the tap doing what every notice's does, which is dismiss.
	OnTap func()

	// Unfiltered puts the card up whatever the tone switches say. Those name which
	// *outcomes* are worth reporting; a message somebody else sent is not an
	// outcome of anything the reader did, and answers to its own setting instead.
	Unfiltered bool
}

// NewNoticeStack builds an empty notice layer. images is where a notice about a
// person gets its face; nil is a stack that draws initials.
func NewNoticeStack(images *cache.ImageCache) *NoticeStack {
	list := container.NewVBox()
	margin := theme.Sizes.NoticeStackMargin

	// Top right: the bottom of the message area belongs to the composer, and a
	// notice must never land on what is being typed. NewLayer keeps the stack out of
	// the window's minimum — without it a fourth notice made the window taller.
	layer := NewLayer(container.NewBorder(nil, nil, nil, NewInset(list, margin, margin, 0, margin)))

	return &NoticeStack{Layer: layer, images: images, list: list}
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
	if notice.Body == "" {
		return
	}
	if notice.Title == "" {
		notice.Title = notice.Tone.title()
	}

	// Recorded ahead of the tone filter, and so recorded even when nothing is
	// drawn: a switch turned off says "do not interrupt me", which is a different
	// question from "never tell me" — and the history is where the second one is
	// asked.
	n.record(notice)

	if !notice.Unfiltered && !notice.Tone.enabled(settings) {
		return
	}

	lifetime := settings.Lifetime()

	// The card must exist before the dismissal that removes it, and the dismissal
	// before the card carrying it — hence the declaration first.
	var card *noticeCard
	dismiss := func() {
		card.stop()
		n.remove(card)
	}
	card = newNoticeCard(n.images, notice, lifetime, dismiss)

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

// record files a notice in the history, dropping the oldest past the depth. Call
// on the UI thread, which every push already is.
func (n *NoticeStack) record(notice Notice) {
	n.history = append(n.history, NoticeRecord{Notice: notice, At: time.Now()})

	// Copied down rather than resliced: a slice held from the middle of the
	// backing array keeps every string before it alive for the run.
	if extra := len(n.history) - noticeHistoryDepth; extra > 0 {
		n.history = append(n.history[:0], n.history[extra:]...)
	}
}

// History is what has been said this session, newest first. The slice is the
// caller's — the stack goes on appending to its own. Call on the UI thread.
func (n *NoticeStack) History() []NoticeRecord {
	out := make([]NoticeRecord, len(n.history))
	for i, record := range n.history {
		out[len(out)-1-i] = record
	}

	return out
}

// ForgetHistory drops the record, for a reader who has read it or an account
// that has just been logged out of. Call on the UI thread.
func (n *NoticeStack) ForgetHistory() { n.history = nil }

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

// noticeCard is one message on the layer: the tone as a disc at the leading
// edge, a heading, the sentence, a close button, and a bar draining over its
// lifetime. The bar earns its keep — a card that simply vanishes gives no
// warning it is about to, so anything read slowly is read twice.
//
// Drawn as the call island is — the same surface, the same lighter-than-hairline
// edge, the same shadow — because the two are the only cards the client floats
// over what is being read, and two floating cards a shade apart read as a
// mistake.
type noticeCard struct {
	tapBase

	content   fyne.CanvasObject
	countdown *fyne.Animation

	// leads marks a card whose tap goes somewhere rather than only taking the card
	// down, which is the whole of what the cursor over it has to say.
	leads bool
}

var (
	_ fyne.Tappable      = (*noticeCard)(nil)
	_ desktop.Cursorable = (*noticeCard)(nil)
)

// newNoticeCard builds the card and starts its countdown. The whole card is
// tappable: it follows the notice's own action where there is one and dismisses
// either way. The close button inside wins the tap, being deeper, and only
// dismisses — so a card that leads somewhere can still be waved off without
// going there.
func newNoticeCard(images *cache.ImageCache, notice Notice, lifetime time.Duration, onDismiss func()) *noticeCard {
	tint := notice.Tone.Color()

	background := canvas.NewRectangle(theme.Colors.NoticeCardBg)
	background.CornerRadius = theme.Sizes.NoticeRadius
	Outline(background)
	background.StrokeColor = theme.Colors.NoticeCardOutline
	elevate(background, theme.Sizes.NoticeShadowBlur)

	padV, padH := theme.Sizes.NoticePaddingV, theme.Sizes.NoticePaddingH
	gap := theme.Sizes.NoticeCardSpacing
	inner := theme.Sizes.NoticeWidth - padH*2

	mark, markWidth := noticeMark(images, notice)

	// What the sentence has to wrap into: the card's inside, less the mark and the
	// close button standing either side of it and the gap in front of each.
	column := inner - markWidth - theme.Sizes.NoticeBadgeGap - gap - glyphButtonSize

	// Ellipsised rather than laid out plain: a heading naming a person and a channel
	// is as long as they are, and a canvas.Text draws straight past the card holding
	// it. The box reports no width of its own, so the fill row still sizes the column.
	title := NewEllipsisText(newBoldText(notice.Title, theme.Colors.TextPrimary, theme.Sizes.NoticeTitleSize))

	bar, countdown := newCountdownBar(tint, inner, lifetime)

	// The mark and the close button are centred against the whole row rather than
	// hung from the first line: the mark is what the card is read by at a glance,
	// and one level with the heading points at the heading rather than at the card.
	head := NewFillRow(2,
		container.NewCenter(mark),
		HorizontalSpacer(theme.Sizes.NoticeBadgeGap),
		VBoxNoSpacing(title, VerticalSpacer(theme.Sizes.NoticeTitleGap), newNoticeBody(notice.Body, column)),
		HorizontalSpacer(gap),
		container.NewCenter(NewCloseButton(onDismiss)),
	)

	c := &noticeCard{countdown: countdown, leads: notice.OnTap != nil}
	c.content = NewFixedWidthContainer(theme.Sizes.NoticeWidth, container.NewStack(
		background,
		NewInset(VBoxNoSpacing(head, VerticalSpacer(gap), bar), padV, padV, padH, padH),
	))
	c.onTap = func() {
		onDismiss() // first: what the action opens must not be drawn under the card
		if notice.OnTap != nil {
			notice.OnTap()
		}
	}
	c.ExtendBaseWidget(c)

	countdown.Start()

	return c
}

// noticeMark is what stands at the card's leading edge, and how wide it is: a
// face for a notice about a person, the tone's disc for one about an outcome.
// The width comes back beside it because the sentence is wrapped by hand and the
// two marks are not the same size.
func noticeMark(images *cache.ImageCache, notice Notice) (fyne.CanvasObject, float32) {
	if notice.AvatarURL == "" && notice.Initial == "" {
		return newNoticeBadge(notice.Tone), theme.Sizes.NoticeBadgeSize
	}

	side := theme.Sizes.NoticeAvatarSize

	return newInitialIcon(images, imageCacheID(notice.AvatarURL), notice.AvatarURL, notice.Initial,
		fyne.NewSize(side, side)), side
}

// How far the disc and its ring are carried from the card's own colour towards
// the tone. The plate is a place for the mark to stand rather than a second
// button, so it stays nearly the card; the ring is what separates it from the
// card at all, so it is most of the way to the tone.
const (
	noticeBadgeFill = 0.20
	noticeBadgeRing = 0.60
)

// newNoticeBadge is the tone's mark on a disc of the tone's own colour, centred
// at the card's leading edge. It replaces the coloured strip that used to run
// down that edge: a strip says a colour, where a disc carrying the mark says
// which colour it is and what it is about in the one object.
func newNoticeBadge(tone Tone) fyne.CanvasObject {
	side := theme.Sizes.NoticeBadgeSize
	tint := tone.Color()

	disc := canvas.NewCircle(theme.Mix(theme.Colors.NoticeCardBg, tint, noticeBadgeFill))
	disc.StrokeColor = theme.Mix(theme.Colors.NoticeCardBg, tint, noticeBadgeRing)
	disc.StrokeWidth = theme.Sizes.NoticeBadgeRing

	return NewFixedSizeContainer(fyne.NewSize(side, side), disc,
		container.NewCenter(newScaledIcon(tone.icon(), theme.Sizes.NoticeIconSize)))
}

// newNoticeBody is the sentence under a notice's heading, wrapped by hand to what
// the card leaves it. A widget.Label is what wraps text in Fyne and it brings
// InnerPadding on all four sides, which on a card this size is a line's worth of
// air the heading above it does not have.
func newNoticeBody(text string, width float32) fyne.CanvasObject {
	wrapped := wrapText(text, width, theme.Sizes.NoticeBodySize, fyne.TextStyle{})

	rows := make([]fyne.CanvasObject, len(wrapped))
	for i, line := range wrapped {
		rows[i] = newText(line, theme.Colors.NoticeCardBody, theme.Sizes.NoticeBodySize)
	}

	return VBoxNoSpacing(rows...)
}

func (c *noticeCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

// Cursor answers with a hand only where the tap leads somewhere. A card that
// merely dismisses is a message about to leave on its own, and a pointer over
// one says it is a control.
func (c *noticeCard) Cursor() desktop.Cursor {
	if c.leads {
		return desktop.PointerCursor
	}

	return desktop.DefaultCursor
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
	card := newDialogCard()

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

/* The history panel */

// NoticeHistoryDialog lists what the notice layer has said this session. Drawn on
// the same island the three message surfaces are — the shell is a header, a count
// and a well, none of which is about messages — with a card of its own: a notice
// has a tone and a heading where a message has an author and a face.
//
// It does not page. The history is bounded at noticeHistoryDepth and held in
// memory, so there is never a next page to ask anybody for.
type NoticeHistoryDialog struct {
	Content fyne.CanvasObject

	island *messageIsland
}

// NewNoticeHistoryDialog builds the panel. onClear drops the record; onClose
// dismisses the layer.
func NewNoticeHistoryDialog(deps Deps, onClear, onClose func()) *NoticeHistoryDialog {
	island, content := newMessageIsland(deps, islandParts{
		Mark:     assets.NotifyIcon,
		Title:    "Notices",
		Where:    "this session",
		Trailing: NewButton("Clear", onClear),
		OnClose:  onClose,
	})

	return &NoticeHistoryDialog{Content: content, island: island}
}

// SetRecords replaces the whole list, newest first. Call on the UI thread.
func (d *NoticeHistoryDialog) SetRecords(records []NoticeRecord) {
	cards := make([]fyne.CanvasObject, 0, len(records))
	for _, record := range records {
		cards = append(cards, newNoticeHistoryCard(record))
	}
	d.island.setCards(cards)

	if len(records) == 0 {
		d.island.setCount("")
		d.island.say("Nothing has been reported this session.")

		return
	}

	d.island.setCount(util.Quantity(len(records), "notice"))
	d.island.say("")
}

// newNoticeHistoryCard draws one past notice: its tone, its heading and when it
// was said, over the sentence it said. One line for the body rather than the
// wrapped paragraph the live card draws — a history is read by scanning, and a
// column of paragraphs is not scannable.
func newNoticeHistoryCard(record NoticeRecord) fyne.CanvasObject {
	gap := theme.Sizes.IslandCardGap
	pad := theme.Sizes.IslandCardPadding

	card := &noticeHistoryCard{background: canvas.NewRectangle(theme.Colors.IslandCardBg)}
	card.onTap = record.Notice.OnTap
	card.leads = record.Notice.OnTap != nil
	card.background.CornerRadius = theme.Sizes.IslandCardRadius

	title := newBoldText(record.Notice.Title, record.Notice.Tone.Color(), theme.Sizes.IslandNameSize)
	when := newText(util.ShortAgo(record.At), theme.Colors.TimestampText, theme.Sizes.IslandTimeSize)

	// The heading shortens and the time keeps its width, the way a message card's
	// does: a long heading is worth less than knowing when it was said.
	heading := NewFillRow(0,
		NewEllipsisText(title),
		HorizontalSpacer(gap),
		container.NewCenter(when),
	)

	body := newText(record.Notice.Body, theme.Colors.TimestampText, theme.Sizes.IslandPreviewSize)

	row := NewFillRow(2,
		container.NewCenter(newNoticeBadge(record.Notice.Tone)),
		HorizontalSpacer(gap),
		VBoxNoSpacing(
			heading,
			VerticalSpacer(theme.Sizes.IslandCardSpacing*halfStep),
			NewEllipsisText(body),
		),
	)

	card.content = container.NewStack(card.background, NewInset(row, pad, pad, pad, pad))
	card.ExtendBaseWidget(card)

	return card
}

// noticeHistoryCard is one row of the history. It fills under the pointer only
// where the notice led somewhere — most report an outcome and lead nowhere, and
// a card that lights up under the pointer and then does nothing reads as broken.
type noticeHistoryCard struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject

	leads bool
}

var (
	_ fyne.Tappable     = (*noticeHistoryCard)(nil)
	_ desktop.Hoverable = (*noticeHistoryCard)(nil)
)

func (c *noticeHistoryCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *noticeHistoryCard) Cursor() desktop.Cursor {
	if c.leads {
		return desktop.PointerCursor
	}

	return desktop.DefaultCursor
}

func (c *noticeHistoryCard) MouseIn(*desktop.MouseEvent) {
	if !c.leads {
		return
	}

	c.background.FillColor = theme.Colors.IslandCardHoverBg
	c.background.Refresh()
}

func (c *noticeHistoryCard) MouseOut() {
	c.background.FillColor = theme.Colors.IslandCardBg
	c.background.Refresh()
}
