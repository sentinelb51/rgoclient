package ui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* The modal layer */

// Overlay is a modal layer over the whole window: a backdrop with content on it.
// Fyne sizes an overlay to the canvas and routes every pointer event to the
// top-most one, so the backdrop both dims and blocks what is underneath.
//
// A plain widget rather than a widget.PopUp, which draws its own themed card and
// paints its backdrop from the theme's shadow colour — far too faint to read as a
// lightbox. NewPopover is the same layer placed beside a widget with its backdrop
// left clear: a card belonging to what it points at should not dim its
// surroundings, but still has to take the click that dismisses it.
type Overlay struct {
	tapBase
	backdrop *canvas.Rectangle
	content  fyne.CanvasObject
	anchor   fyne.CanvasObject // nil for a centred modal

	// placement holds the content, so Reposition can re-run only that layout.
	placement *fyne.Container
}

var (
	_ fyne.Tappable      = (*Overlay)(nil)
	_ desktop.Cursorable = (*Overlay)(nil)
)

// NewOverlay creates a modal layer showing content centred on a dimmed backdrop,
// dismissed by tapping around it.
func NewOverlay(content fyne.CanvasObject, onDismiss func()) *Overlay {
	o := &Overlay{
		backdrop: canvas.NewRectangle(theme.Colors.OverlayBackdrop),
		content:  content,
	}
	o.onTap = onDismiss
	o.ExtendBaseWidget(o)

	return o
}

// NewPopover creates a modal layer showing content beside anchor, dismissed by
// tapping anywhere else. anchor must be mounted on the same canvas.
func NewPopover(content, anchor fyne.CanvasObject, onDismiss func()) *Overlay {
	o := NewOverlay(content, onDismiss)
	o.backdrop.FillColor = color.Transparent
	o.anchor = anchor

	return o
}

func (o *Overlay) CreateRenderer() fyne.WidgetRenderer {
	if o.anchor == nil {
		o.placement = container.NewCenter(o.content)
	} else {
		o.placement = container.New(&popoverLayout{anchor: o.anchor, host: o}, o.content)
	}

	return widget.NewSimpleRenderer(container.NewStack(o.backdrop, o.placement))
}

// Reposition re-places the content after it changed size — a profile card grows
// when its About section arrives. Neither placement re-runs on its own: Refresh
// repaints without laying out, and Resize is a no-op while the layer still fills
// the same canvas. Call on the UI thread.
func (o *Overlay) Reposition() { Relayout(o.placement) }

// Cursor keeps the normal pointer over the backdrop: tapBase advertises the hand
// for things that look clickable, and a dimmed background isn't one.
func (o *Overlay) Cursor() desktop.Cursor { return desktop.DefaultCursor }

// tapSink swallows taps on what it wraps: innermost wins, so without one a click
// on non-interactive content inside an Overlay falls through to the backdrop and
// dismisses it. Anything nested deeper still wins over the sink.
type tapSink struct {
	tapBase
	content fyne.CanvasObject
}

var (
	_ fyne.Tappable      = (*tapSink)(nil)
	_ desktop.Cursorable = (*tapSink)(nil)
)

func newTapSink(content fyne.CanvasObject) *tapSink {
	s := &tapSink{content: content}
	s.ExtendBaseWidget(s)

	return s
}

func (s *tapSink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

func (s *tapSink) Cursor() desktop.Cursor { return desktop.DefaultCursor }

/* Attachment viewer */

// NewAttachmentViewer is the card inside the attachment lightbox: a slim header
// over the attachment, sized to fit bounds. An image is scaled to fit, a text
// file gets its whole contents in a selectable monospace pane, anything else a
// card offering the browser. The chrome is the card's own — there is no native
// window here to recolour.
func NewAttachmentViewer(deps Deps, attachment *domain.File, bounds fyne.Size, onClose func()) fyne.CanvasObject {
	// What is left of bounds once the chrome is paid for: NewPadded insets all four
	// sides, and the Border puts one more gap between the header and the body.
	pad := fynetheme.Padding()
	body := fyne.NewSize(
		bounds.Width-2*pad,
		bounds.Height-theme.Sizes.ViewerHeaderHeight-3*pad,
	)

	var (
		content fyne.CanvasObject
		detail  string
	)
	switch {
	case attachment.Kind == domain.FileImage:
		content, detail = viewerImage(deps, attachment, body)
	case attachment.Kind == domain.FileText:
		content = viewerText(attachment, body)
	default:
		content = viewerUnsupported(body)
	}

	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.ViewerCornerRadius

	well := canvas.NewRectangle(theme.Colors.ViewerBodyBg)
	well.CornerRadius = theme.Sizes.ViewerCornerRadius

	header := viewerHeader(attachment, detail, onClose)
	inner := container.NewBorder(header, nil, nil, nil, container.NewStack(well, content))

	return newTapSink(container.NewStack(card, container.NewPadded(inner)))
}

// viewerHeader is the card's title strip: filename on the left, then the file
// size (and, for images, their pixel dimensions), a browser button, and close.
func viewerHeader(attachment *domain.File, detail string, onClose func()) fyne.CanvasObject {
	name := newBoldText(attachment.Name, theme.Colors.TextPrimary, theme.Sizes.ViewerTitleSize)

	// An embed's picture is not an upload and carries no byte count, so the size is
	// left out rather than reported as nothing.
	meta := detail
	if attachment.Size > 0 {
		meta = util.FormatFileSize(attachment.Size)
		if detail != "" {
			meta = detail + "  ·  " + meta
		}
	}
	info := newText(meta, theme.Colors.TimestampText, theme.Sizes.ViewerTitleSize)

	actions := container.NewHBox(info, HorizontalSpacer(theme.Sizes.ViewerPadding))
	if link := attachment.URL; link != "" {
		actions.Add(NewIconButton(fynetheme.ComputerIcon(), func() { openURL(link) }, nil))
	}
	actions.Add(NewCloseButton(onClose))

	strip := container.NewBorder(nil, nil,
		container.NewHBox(HorizontalSpacer(theme.Sizes.ViewerPadding), name), actions)

	return NewMinHeightContainer(theme.Sizes.ViewerHeaderHeight, strip)
}

// viewerImage renders the attachment scaled to fit within bounds and reports its
// real pixel dimensions for the header.
func viewerImage(deps Deps, attachment *domain.File, bounds fyne.Size) (fyne.CanvasObject, string) {
	pixelWidth, pixelHeight := attachment.Width, attachment.Height

	size := fitWithin(pixelWidth, pixelHeight, bounds.Width, bounds.Height)
	if size.IsZero() {
		size = bounds // no usable metadata: let the image scale itself once it arrives
	}
	size = fyne.NewSize(max(size.Width, theme.Sizes.ViewerMinWidth), max(size.Height, theme.Sizes.ViewerMinHeight))

	frame := container.NewStack()
	if link := attachment.URL; link != "" {
		deps.Images.LoadIntoContainer(imageCacheID(link), link, size, frame, false, nil)
	}

	detail := ""
	if pixelWidth > 0 && pixelHeight > 0 {
		detail = fmt.Sprintf("%d × %d", pixelWidth, pixelHeight)
	}

	return container.NewGridWrap(size, frame), detail
}

// viewerText shows a text attachment in full — the message preview only pulls
// the first few hundred characters — as selectable monospace text.
func viewerText(attachment *domain.File, bounds fyne.Size) fyne.CanvasObject {
	body := widget.NewLabel("Loading...")
	body.TextStyle = fyne.TextStyle{Monospace: true}
	body.Wrapping = fyne.TextWrapWord
	body.Selectable = true

	go func() {
		text, err := fetchText(attachment.URL, attachmentViewerRead)
		DoOnUI(func() {
			if err != nil {
				body.SetText("Could not load this file.")
				return
			}
			body.SetText(text)
		})
	}()

	return container.NewGridWrap(bounds, container.NewPadded(container.NewVScroll(body)))
}

// viewerUnsupported is the placeholder for attachments this client cannot render
// inline; the header's browser button is the way out.
func viewerUnsupported(bounds fyne.Size) fyne.CanvasObject {
	label := widget.NewLabelWithStyle("No preview available for this file type.",
		fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	height := max(bounds.Height/3, theme.Sizes.ViewerMinHeight)

	return container.NewGridWrap(fyne.NewSize(bounds.Width, height), container.NewCenter(label))
}

/* Join-server dialog */

// JoinServerDialog is the join-server card: a field taking an invite code or
// link, a Join button, and a way to create a server instead. It validates the
// input itself (util.InviteCode), so a typo never costs a round trip. The layout
// mirrors the login screen — centred heading, separators, full-width controls.
type JoinServerDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to
	// focus once it is up.
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	status dialogStatus
	join   *widget.Button
}

// NewJoinServerDialog builds the dialog. onJoin receives an already-validated
// invite code, onCreate opens server creation, and onClose dismisses the modal;
// all three are called on the UI thread.
func NewJoinServerDialog(onJoin func(code string), onCreate, onClose func()) *JoinServerDialog {
	d := &JoinServerDialog{}

	entry := newModalEntry(onClose)
	entry.SetPlaceHolder("stt.gg/dcRHWEF1")
	d.Entry = entry

	d.status = newDialogStatus()

	d.join = widget.NewButton("Join", func() {
		code := util.InviteCode(entry.Text)
		if code == "" {
			d.Fail("That doesn't look like an invite code or link.")
			return
		}
		d.status.set("Joining...", theme.Colors.TimestampText)
		d.join.Disable()
		onJoin(code)
	})

	// Guarded, unlike a click: OnTapped bypasses the button's own disabled check, so
	// Enter during an in-flight request would send it twice.
	entry.OnSubmitted = func(string) {
		if !d.join.Disabled() {
			d.join.OnTapped()
		}
	}

	inner := container.NewVBox(
		dialogHeader("Join a server", onClose),
		widget.NewSeparator(),
		widget.NewLabel("Invite code"),
		WithCaret(entry),
		d.status.row(),
		d.join,
		widget.NewSeparator(),
		widget.NewLabel("Start your own"),
		widget.NewButton("Create a server", onCreate),
	)
	body := NewMinWidthContainer(theme.Sizes.JoinDialogWidth, container.NewPadded(inner))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))

	return d
}

// Fail reports a failed join and re-enables the button so the code can be
// corrected and tried again. Call on the UI thread.
func (d *JoinServerDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.join.Enable()
}

// dialogHeader is the title row every card on the modal layer wears: the heading
// centred across the whole card, with the close button laid *over* its right edge
// so it does not shift the title off centre. The button is centred to keep its
// square minimum rather than be stretched to the row height.
func dialogHeader(title string, onClose func()) fyne.CanvasObject {
	heading := widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	closeButton := container.NewBorder(nil, nil, nil, container.NewCenter(NewCloseButton(onClose)))

	return container.NewStack(heading, closeButton)
}

// newDialogCard is the surface those cards are drawn on.
func newDialogCard() *canvas.Rectangle {
	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.JoinDialogCornerRadius

	return card
}

// dialogStatus is the line a card reports its own outcome on, a second dialog
// stacked over the modal layer being one that covers the field it is about. It is
// mounted empty rather than added on demand, so the card does not jump when a
// message appears.
type dialogStatus struct{ text *canvas.Text }

func newDialogStatus() dialogStatus {
	return dialogStatus{text: newText("", theme.Colors.TimestampText, theme.Sizes.JoinDialogTextSize)}
}

// row indents the line to meet the labels around it, which carry the widget inner
// padding a raw canvas.Text does not.
func (s dialogStatus) row() fyne.CanvasObject {
	return container.NewBorder(nil, nil, HorizontalSpacer(fynetheme.InnerPadding()), nil, s.text)
}

func (s dialogStatus) set(message string, fill color.Color) {
	s.text.Text = message
	s.text.Color = fill
	s.text.Refresh()
}

/* Prompt dialog */

// PromptField is one line of a prompt: what to call it, what stands in it while
// it is empty, and whether what is typed should be hidden.
type PromptField struct {
	Label       string
	Placeholder string

	// Password masks the field. Nothing keeps what is typed into one: it is sent
	// with the request that needed it and dropped with the card.
	Password bool
}

// Prompt asks for what a request needs and nothing else: a field per answer, one
// button, and what to do with what is typed. Creating a server is a name alone —
// Revolt takes no icon at creation — and changing a username is why it takes more
// than one, Revolt asking for the account password with it.
type Prompt struct {
	Title  string
	Action string // the button, and what the status line says it is doing
	Busy   string

	Fields []PromptField

	// OnSubmit takes what was typed, in the fields' own order, on the UI thread.
	// Whoever supplies it owns the request, so the dialog says only that one is out
	// — the caller closes it or reports through Fail. Not called until every field
	// has something in it, so the values are as long as Fields and none is empty.
	OnSubmit func(values []string)
}

// PromptDialog is that card: the join dialog's shape with one section instead of
// three.
type PromptDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to focus
	// once it is up — the first one, the rest reached with Tab.
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	status dialogStatus
	action *widget.Button
}

// NewPromptDialog builds the card. onClose dismisses the modal layer.
func NewPromptDialog(prompt Prompt, onClose func()) *PromptDialog {
	d := &PromptDialog{}

	rows := []fyne.CanvasObject{dialogHeader(prompt.Title, onClose), widget.NewSeparator()}
	entries := make([]*modalEntry, 0, len(prompt.Fields))

	for _, field := range prompt.Fields {
		entry := newModalEntry(onClose)
		entry.SetPlaceHolder(field.Placeholder)
		entry.Password = field.Password

		// Guarded, unlike a click: OnTapped bypasses the button's own disabled check,
		// so Enter during an in-flight request would send it twice.
		entry.OnSubmitted = func(string) {
			if !d.action.Disabled() {
				d.action.OnTapped()
			}
		}

		entries = append(entries, entry)
		rows = append(rows, widget.NewLabel(field.Label), WithCaret(entry))
	}
	if len(entries) > 0 {
		d.Entry = entries[0]
	}

	d.status = newDialogStatus()

	d.action = widget.NewButton(prompt.Action, func() {
		values := promptValues(entries)
		if values == nil {
			return
		}

		d.status.set(prompt.Busy, theme.Colors.TimestampText)
		d.action.Disable()
		prompt.OnSubmit(values)
	})

	rows = append(rows, d.status.row(), d.action)
	body := NewMinWidthContainer(theme.Sizes.JoinDialogWidth, container.NewPadded(container.NewVBox(rows...)))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))

	return d
}

// promptValues is what the fields hold, or nothing while any is empty: a card is
// answered whole, a half-answered request being refused for a reason the reader
// can see for themselves.
func promptValues(entries []*modalEntry) []string {
	values := make([]string, len(entries))

	for i, entry := range entries {
		if entry.Text == "" {
			return nil
		}
		values[i] = entry.Text
	}

	return values
}

// Fail reports a failed request and re-enables the button, so the text can be
// corrected and sent again. Call on the UI thread.
func (d *PromptDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.action.Enable()
}

// modalEntry is a single-line field on the modal layer. It handles Escape itself
// because a focused entry is the end of the line for key events: Fyne routes them
// to the focused widget and never reaches the canvas handler the layer dismisses on.
type modalEntry struct {
	widget.Entry
	onCancel func()
}

func newModalEntry(onCancel func()) *modalEntry {
	e := &modalEntry{onCancel: onCancel}
	e.ExtendBaseWidget(e)

	return e
}

func (e *modalEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		if e.onCancel != nil {
			e.onCancel()
		}
		return
	}

	e.Entry.TypedKey(key)
}
