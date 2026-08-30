package ui

// The card that starts a screenshare: what to share, how often, and how big.
// It is the modal layer's own vocabulary — the dialog card, the field labels,
// the run of chips a channel search's filters are made of — because it asks
// the same kind of question they do.
//
// It knows nothing about capture. A source is a `ShareSource` the controller
// enumerated, the way a device list arrives as `ui.AudioDevice`, and the
// answer is a `ShareChoice` the controller turns into an encoder's arguments.

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
)

/* What can be shared */

// ShareSourceKind is what one entry in the list is. The picker draws the two
// under headings of their own, a monitor and a window being answers to
// different questions.
type ShareSourceKind int

const (
	ShareMonitor ShareSourceKind = iota
	ShareWindow
)

// ShareSource is one thing this machine can put on screen elsewhere, as the
// controller enumerated it. ID is the platform's own handle and is passed
// back untouched; the size is what sizes the encode box.
type ShareSource struct {
	ID    string
	Kind  ShareSourceKind
	Title string

	Width, Height int
}

// ShareChoice is the card's answer. Source is the ID of the picked entry,
// Height the shorter edge to encode at — 0 meaning the source's own — and FPS
// what the encoder is asked for.
type ShareChoice struct {
	Source string
	Height int
	FPS    int
}

// The two ladders the card offers. Height is the *short* edge, which is how
// every other client names a stream's quality, and 0 is the source's own size
// under whatever the server allows.
var (
	shareHeights = []int{0, 1080, 720, 480}
	shareRates   = []int{5, 15, 30, 60}
)

func shareHeightLabel(height int) string {
	if height == 0 {
		return "Source"
	}

	return fmt.Sprintf("%dp", height)
}

func shareRateLabel(fps int) string {
	return fmt.Sprintf("%d fps", fps)
}

/* The card */

// ShareDialog is the picker. It holds the answer rather than reporting each
// change: nothing is started until the button is pressed, so a chip toggled
// on the way is a decision not yet made.
type ShareDialog struct {
	// Content is the card to hand to the modal layer.
	Content fyne.CanvasObject

	rows   []*shareSourceRow
	source string

	heights []*pickChip
	rates   []*pickChip

	choice ShareChoice
	status dialogStatus
	action *Button
}

// ShareDialogConfig is what the card is opened with: what may be shared, what
// was picked last time, and the note under the list — the one line the
// platform gets to say about its own limits (X11's occluded windows, a
// machine with no capture at all).
type ShareDialogConfig struct {
	Sources []ShareSource
	Initial ShareChoice
	Note    string
}

// NewShareDialog builds the card. onStart is handed the answer; onClose
// dismisses the layer.
func NewShareDialog(cfg ShareDialogConfig, onStart func(ShareChoice), onClose func()) *ShareDialog {
	d := &ShareDialog{choice: cfg.Initial}

	if d.choice.FPS == 0 {
		d.choice.FPS = 15
	}

	rows := []fyne.CanvasObject{dialogHeader("Share your screen", onClose), NewRowDivider()}

	body := []fyne.CanvasObject{
		dialogField("What to share", d.buildSources(cfg)),
		dialogField("Quality", d.buildHeights()),
		dialogField("Frame rate", d.buildRates()),
	}

	if cfg.Note != "" {
		// Wrapped by hand: the platform's own caveat is a sentence, and a
		// canvas.Text draws straight past the card holding it.
		body = append(body, NewWrappedText(cfg.Note, shareDialogInnerWidth(),
			theme.Sizes.DialogDetailSize, theme.Colors.TimestampText))
	}

	d.status = newDialogStatus()
	body = append(body, d.status.row())

	d.action = NewWeightedButton("Share", ButtonPrimary, func() {
		if d.source == "" {
			d.Fail("Pick a screen or a window first.")
			return
		}

		d.action.Disable()
		d.status.set("Starting...", theme.Colors.TimestampText)
		onStart(d.answer())
	})
	body = append(body, d.action)

	if len(cfg.Sources) == 0 {
		d.action.Disable()
	}

	rows = append(rows, body...)

	padding := theme.Sizes.DialogPadding
	card := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), card))

	return d
}

// shareDialogInnerWidth is the card's width less its padding — what anything
// wrapped by hand has to be measured against.
func shareDialogInnerWidth() float32 {
	return theme.Sizes.ChannelDialogWidth - 2*theme.Sizes.DialogPadding
}

// answer is the card's state as the controller takes it. The source's own
// size is looked up here rather than carried: the row holds it already, and a
// caller would otherwise have to keep the list to make sense of the ID.
func (d *ShareDialog) answer() ShareChoice {
	return ShareChoice{Source: d.source, Height: d.choice.Height, FPS: d.choice.FPS}
}

// Fail reports a refusal on the card and gives the button back, so a stream
// the platform would not start can be tried at another source without the
// card closing under the reader.
func (d *ShareDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.action.Enable()
}

/* The list */

// buildSources is the scrolling list of what can be shared, measured rather
// than left to the scroller: a machine with thirty windows would otherwise
// open a card taller than the screen it is offering to share.
func (d *ShareDialog) buildSources(cfg ShareDialogConfig) fyne.CanvasObject {
	if len(cfg.Sources) == 0 {
		return VBoxNoSpacing(newText("Nothing here can be captured.",
			theme.Colors.TimestampText, 0))
	}

	rows := make([]fyne.CanvasObject, 0, len(cfg.Sources)+2)
	var monitors, windows bool

	for _, source := range cfg.Sources {
		// The two kinds are answers to different questions, so each run is
		// named once rather than every row saying which it is.
		switch {
		case source.Kind == ShareMonitor && !monitors:
			monitors = true
			rows = append(rows, shareGroupLabel("Screens"))
		case source.Kind == ShareWindow && !windows:
			windows = true
			rows = append(rows, shareGroupLabel("Windows"))
		}

		row := newShareSourceRow(source, d.pick)
		d.rows = append(d.rows, row)
		rows = append(rows, row)
	}

	// The remembered source where it is still there, else the first thing on
	// offer: a card that opens with nothing picked is one press longer for no
	// reason, and a monitor is what leads the list.
	pick := cfg.Sources[0].ID
	for _, source := range cfg.Sources {
		if source.ID == cfg.Initial.Source {
			pick = source.ID
			break
		}
	}
	d.pick(pick)

	list := NewGapColumn(theme.Sizes.ShareSourceGap, rows...)

	return container.New(
		&cappedHeightLayout{content: list, max: theme.Sizes.ShareSourceListHeight},
		NewPlainVScroll(list))
}

// pick marks one row and unmarks the rest — one source at a time, which is
// what the whole card is for.
func (d *ShareDialog) pick(id string) {
	d.source = id

	for _, row := range d.rows {
		row.setChosen(row.source.ID == id)
	}
}

func shareGroupLabel(text string) fyne.CanvasObject {
	return newBoldText(text, theme.Colors.CategoryText, theme.Sizes.DialogLabelSize)
}

/* The chips */

// buildHeights and buildRates are the two runs of chips. Each is one answer,
// so picking marks it and clears its neighbours — the shape a filter run has,
// with a radio's rule laid over it.
func (d *ShareDialog) buildHeights() fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(shareHeights))

	for _, height := range shareHeights {
		chip := newPickChip(assets.ScreenshareIcon, shareHeightLabel(height), height)
		chip.onTap = func() {
			d.choice.Height = chip.value
			markPickChips(d.heights, chip.value)
		}

		d.heights = append(d.heights, chip)
		chips = append(chips, chip)
	}

	markPickChips(d.heights, d.choice.Height)

	return NewFlow(shareDialogInnerWidth(), theme.Sizes.IslandChipGap, chips...)
}

func (d *ShareDialog) buildRates() fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(shareRates))

	for _, fps := range shareRates {
		chip := newPickChip(assets.PlayIcon, shareRateLabel(fps), fps)
		chip.onTap = func() {
			d.choice.FPS = chip.value
			markPickChips(d.rates, chip.value)
		}

		d.rates = append(d.rates, chip)
		chips = append(chips, chip)
	}

	markPickChips(d.rates, d.choice.FPS)

	return NewFlow(shareDialogInnerWidth(), theme.Sizes.IslandChipGap, chips...)
}

/* One source */

// shareSourceRow is one thing that can be shared, drawn as the group picker's
// row: the same island card, the same fills, and a mark at the end saying
// whether it is the answer. Chosen outranks hovered, an answer already given
// outranking a pointer passing over.
type shareSourceRow struct {
	tapBase

	background *canvas.Rectangle
	mark       *pickMark
	content    fyne.CanvasObject

	source ShareSource

	hovered bool
	chosen  bool
}

var (
	_ fyne.Tappable     = (*shareSourceRow)(nil)
	_ desktop.Hoverable = (*shareSourceRow)(nil)
)

func newShareSourceRow(source ShareSource, onPick func(string)) *shareSourceRow {
	w := &shareSourceRow{
		background: newIslandCard(),
		mark:       newPickMark(),
		source:     source,
	}
	w.onTap = func() { onPick(source.ID) }

	title := newText(source.Title, theme.Colors.TextPrimary, 0)
	size := newText(fmt.Sprintf("%d × %d", source.Width, source.Height),
		theme.Colors.TimestampText, theme.Sizes.FriendsHandleSize)
	lines := VBoxNoSpacing(NewEllipsisText(title), NewEllipsisText(size))

	gap := theme.Sizes.FriendsGap
	padV, padH := theme.Sizes.FriendsCardPaddingV, theme.Sizes.FriendsCardPaddingH

	row := NewFillRow(0,
		vcenter(lines),
		HorizontalSpacer(gap),
		vcenter(w.mark),
	)

	w.content = container.NewStack(w.background,
		NewMinHeightContainer(theme.Sizes.ShareSourceRowHeight, NewInset(row, padV, padV, padH, padH)))
	w.repaint()
	w.ExtendBaseWidget(w)

	return w
}

func (w *shareSourceRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

func (w *shareSourceRow) MouseIn(*desktop.MouseEvent) {
	w.hovered = true
	w.repaint()
}

func (w *shareSourceRow) MouseOut() {
	w.hovered = false
	w.repaint()
}

func (w *shareSourceRow) setChosen(chosen bool) {
	if w.chosen == chosen {
		return
	}
	w.chosen = chosen

	w.mark.set(chosen)
	w.repaint()
}

func (w *shareSourceRow) repaint() {
	fill := theme.Colors.SessionCardBg
	switch {
	case w.chosen:
		fill = theme.Colors.GroupPickChosenBg
	case w.hovered:
		fill = theme.Colors.FriendsCardHoverBg
	}
	if w.background.FillColor == fill {
		return
	}

	w.background.FillColor = fill
	w.background.Refresh()
}
