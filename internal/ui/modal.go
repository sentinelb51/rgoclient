package ui

import (
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"
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

// AttachmentViewer is the card inside the attachment lightbox: the attachment
// over a metadata bar naming it, sized to fit bounds. An image is scaled to fit,
// a text file gets its whole contents in a selectable monospace pane, anything
// else a card offering the browser. The chrome is the card's own — there is no
// native window here to recolour.
type AttachmentViewer struct {
	// Content is the card to hand to the modal layer.
	Content fyne.CanvasObject

	// OnResize re-places the card after it changed size, which it does when a
	// picture Revolt gave no dimensions for is decoded. The layer is the
	// controller's, so re-placing it is too.
	OnResize func()

	deps       Deps
	attachment *domain.File

	// pixels is what the picture measures, from the file where it says and from
	// the decode where it does not. Zero for anything that is not one.
	pixels image.Point

	// meta is the bar's second label, which those dimensions are written into once
	// they are known.
	meta *canvas.Text

	// tip names the bar's buttons. The card's own rather than the app's: the app's
	// is a layer in the window's content and this card is a canvas overlay over all
	// of it, so a label mounted there would be covered by the button naming it.
	tip *Tooltip
}

// NewAttachmentViewer builds the card. onClose dismisses the modal layer.
func NewAttachmentViewer(deps Deps, attachment *domain.File, bounds fyne.Size, onClose func()) *AttachmentViewer {
	v := &AttachmentViewer{
		deps:       deps,
		attachment: attachment,
		pixels:     image.Pt(attachment.Width, attachment.Height),
		tip:        NewTooltip(),
	}

	// What is left of bounds once the chrome is paid for: NewPadded insets all four
	// sides, and the bar sits flush under the body.
	pad := fynetheme.Padding()
	body := fyne.NewSize(
		bounds.Width-2*pad,
		bounds.Height-theme.Sizes.ViewerBarHeight-2*pad,
	)

	var content fyne.CanvasObject
	switch {
	case attachment.Kind == domain.FileImage:
		content = v.image(body)
	case attachment.Kind == domain.FileText:
		content = viewerText(attachment, body)
	default:
		content = viewerUnsupported(body)
	}

	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.ViewerCornerRadius

	well := canvas.NewRectangle(theme.Colors.ViewerBodyBg)
	well.CornerRadius = theme.Sizes.ViewerCornerRadius

	inner := VBoxNoSpacing(container.NewStack(well, content), v.bar(onClose))
	sink := newTapSink(container.NewStack(card, container.NewPadded(inner), v.tip.Layer))
	sink.onSecondaryTap = func(event *fyne.PointEvent) {
		ShowContextMenu(sink, v.menuItems(), event.AbsolutePosition)
	}

	v.Content = sink

	return v
}

// bar is the strip under the attachment: what it is called and how large it is on
// the left, the two ways out of the card on the right. The same strip a message
// attachment wears, in the same fill — a picture reads the same in both places.
func (v *AttachmentViewer) bar(onClose func()) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.SwiftActionBg)

	name := newBoldText(v.attachment.Name, theme.Colors.TextPrimary, theme.Sizes.ViewerTitleSize)
	meta := newText(viewerMeta(v.attachment, v.detail()), theme.Colors.TimestampText, theme.Sizes.ViewerTitleSize)
	v.meta = meta

	gap := theme.Sizes.ViewerPadding
	left := HBoxNoSpacing(HorizontalSpacer(gap), vcenter(name), HorizontalSpacer(gap), vcenter(meta))

	// Centred rather than left to the row: a box layout stretches a child to the
	// strip's height, which would draw the two buttons as tall rectangles of
	// different widths rather than as the one square each.
	buttons := HBoxNoSpacing()
	if link := v.attachment.URL; link != "" {
		browse := NewGlyphButton(fynetheme.ComputerIcon(), func() { v.deps.Actions.OnLinkTapped(link, "") })
		buttons.Add(vcenter(browse.saying(v.tip, "Open in browser")))
	}
	buttons.Add(vcenter(NewCloseButton(onClose)))
	buttons.Add(HorizontalSpacer(gap))

	strip := container.NewBorder(nil, nil, left, buttons)

	return NewMinHeightContainer(theme.Sizes.ViewerBarHeight, container.NewStack(background, strip))
}

// viewerMeta is what the bar says about the file beside its name: for an image
// its pixel dimensions, then its size on disk. An embed's picture is not an
// upload and carries no byte count, so that half is left out rather than reported
// as nothing.
func viewerMeta(attachment *domain.File, detail string) string {
	if attachment.Size <= 0 {
		return detail
	}

	size := util.FormatFileSize(attachment.Size)
	if detail == "" {
		return size
	}

	return detail + "  ·  " + size
}

// menuItems is what right-clicking the card offers: the ways to take the
// attachment with you, and the browser.
func (v *AttachmentViewer) menuItems() []*fyne.MenuItem {
	var items []*fyne.MenuItem

	if v.attachment.Kind == domain.FileImage && v.attachment.URL != "" {
		items = append(items, fyne.NewMenuItemWithIcon("Copy image",
			actionMark(assets.ActionCopyIcon), v.copyImage))
	}

	if link := v.attachment.URL; link != "" {
		items = append(items,
			fyne.NewMenuItemWithIcon("Copy link", actionMark(assets.ActionCopyIcon),
				func() { CopyToClipboard(link) }),
			fyne.NewMenuItemWithIcon("Open in browser", fynetheme.ComputerIcon(),
				func() { v.deps.Actions.OnLinkTapped(link, "") }),
		)
	}

	return items
}

// Copy is what Ctrl+C over the card does: an image goes on the clipboard as a
// picture, anything else as its link — there being nothing else a zip or a
// half-read text file could usefully become. Call on the UI thread.
func (v *AttachmentViewer) Copy() {
	if v.attachment.Kind == domain.FileImage {
		v.copyImage()
		return
	}

	if link := v.attachment.URL; link != "" {
		CopyToClipboard(link)
	}
}

// copyImage copies the picture the card is showing — the cache's own decode of
// it, so the copy is capped by the decode the way the one on screen is.
func (v *AttachmentViewer) copyImage() {
	link := v.attachment.URL
	if link == "" {
		return
	}

	v.deps.Images.LoadAsync(fileCacheID(v.attachment), link, false, CopyImageToClipboard)
}

// image renders the attachment scaled to fit within bounds, in the box that
// decides how wide the card is. Where Revolt carried no dimensions — an avatar,
// a bare embed picture — the card opens at the whole of bounds and settles onto
// the picture's own shape once it is decoded; see refit.
func (v *AttachmentViewer) image(bounds fyne.Size) fyne.CanvasObject {
	size := fitWithin(v.pixels.X, v.pixels.Y, bounds.Width, bounds.Height)
	if size.IsZero() {
		size = bounds
	}

	// Built before the picture is asked for: a cached one is delivered on this
	// thread, and refit has a box to re-lay out either way.
	box := container.NewGridWrap(viewerBox(size))
	box.Add(imageFrame(v.deps.Images, v.attachment, bounds, size, color.Transparent,
		func(pixels image.Point, fitted fyne.Size) { v.refit(pixels, fitted, box) }))

	return box
}

// viewerBox is the card the viewer draws a picture in: the picture's own shape,
// but never so small that the bar's name and buttons have nowhere to sit. A
// picture narrower than that is letterboxed inside it.
func viewerBox(size fyne.Size) fyne.Size {
	return fyne.NewSize(
		max(size.Width, theme.Sizes.ViewerMinWidth),
		max(size.Height, theme.Sizes.ViewerMinHeight),
	)
}

// refit takes the card down to the shape of a picture only its own decode could
// measure, and tells the bar what it measured. Both the bar and the layer the
// card sits on may be missing: a cached picture arrives during the build, before
// there is either — and the card is then placed at its final size anyway.
func (v *AttachmentViewer) refit(pixels image.Point, fitted fyne.Size, box *fyne.Container) {
	v.pixels = pixels

	box.Layout = layout.NewGridWrapLayout(viewerBox(fitted))
	Relayout(box)

	if v.meta != nil {
		v.meta.Text = viewerMeta(v.attachment, v.detail())
		v.meta.Refresh()
	}
	if v.OnResize != nil {
		v.OnResize()
	}
}

// detail is what the bar says about a picture beside its name, once something
// knows: an attachment says so itself, anything else only after it is decoded.
func (v *AttachmentViewer) detail() string {
	if v.pixels.X <= 0 || v.pixels.Y <= 0 {
		return ""
	}

	return fmt.Sprintf("%d × %d", v.pixels.X, v.pixels.Y)
}

// viewerText shows a text attachment in full — the message preview only pulls
// the first few hundred characters — as selectable monospace text.
func viewerText(attachment *domain.File, bounds fyne.Size) fyne.CanvasObject {
	body := widget.NewLabel("Loading...")
	body.TextStyle = fyne.TextStyle{Monospace: true}
	body.Wrapping = fyne.TextWrapWord
	body.Selectable = true

	// An attachment with no URL is one nothing can be fetched for, so it is
	// answered here rather than sent and failed. That it starts no goroutine also
	// keeps the card off Fyne's *test* driver, which runs a DoOnUI callback
	// concurrently with the test rather than serialising it onto a UI thread — the
	// real driver has one and queues.
	if attachment.URL == "" {
		body.SetText("Could not load this file.")
	} else {
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
	}

	return container.NewGridWrap(bounds, container.NewPadded(container.NewVScroll(body)))
}

// viewerUnsupported is the placeholder for attachments this client cannot render
// inline; the header's browser button is the way out.
func viewerUnsupported(bounds fyne.Size) fyne.CanvasObject {
	label := newText("No preview available for this file type.",
		theme.Colors.TimestampText, theme.Sizes.DialogDetailSize)
	label.Alignment = fyne.TextAlignCenter

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

	// OnResize fires when the preview appears or is replaced, the card being
	// centred at the size it was mounted at.
	OnResize func()

	deps   Deps
	entry  *modalEntry
	status dialogStatus
	join   *Button

	// preview holds the card describing what the typed code opens, empty until one
	// resolves. previewed is the code it is describing, so a field re-edited back
	// to the same code does not ask again.
	preview   *fyne.Container
	previewed string

	// settle waits for the typing to stop before anything is asked: a code is
	// typed a character at a time and every prefix of one is a plausible code.
	settle *time.Timer
}

// invitePreviewDelay is how long the field must go quiet before the code in it is
// resolved. Long enough that typing one out sends a single request, short enough
// that a paste answers while the pointer is still moving to the button.
const invitePreviewDelay = 400 * time.Millisecond

// NewJoinServerDialog builds the dialog. onJoin receives an already-validated
// invite code, onCreate opens server creation, and onClose dismisses the modal;
// all three are called on the UI thread.
func NewJoinServerDialog(deps Deps, onJoin func(code string), onCreate, onClose func()) *JoinServerDialog {
	d := &JoinServerDialog{deps: deps, preview: container.NewStack()}
	d.preview.Hide() // nothing to preview until a code is typed

	entry := newModalEntry(onClose)
	entry.SetPlaceHolder("stt.gg/dcRHWEF1")
	entry.OnChanged = d.codeTyped
	d.Entry, d.entry = entry, entry

	d.status = newDialogStatus()

	d.join = NewWeightedButton("Join", ButtonPrimary, func() {
		code := util.InviteCode(entry.Text)
		if code == "" {
			d.Fail("That doesn't look like an invite code or link.")
			return
		}
		d.status.set("Joining...", theme.Colors.TimestampText)
		d.join.Disable()
		onJoin(code)
	})

	// Tap rather than the action itself: calling that bypasses the button's own
	// disabled check, so Enter during an in-flight request would send it twice.
	entry.OnSubmitted = func(string) { d.join.Tap() }

	rows := []fyne.CanvasObject{
		dialogHeader("Join a server", onClose),
		NewRowDivider(),
		dialogField("Invite code", fieldSurface(entry)),
		d.preview,
		d.status.row(),
		d.join,
		NewRowDivider(),
		dialogField("Start your own", NewButton("Create a server", onCreate)),
	}

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))

	return d
}

// Fail reports a failed join and re-enables the button so the code can be
// corrected and tried again. Call on the UI thread.
func (d *JoinServerDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.join.Enable()
}

// Close stops the card asking anything more on its way out — the field can be
// edited and the dialog dismissed inside one delay. Call on the UI thread.
func (d *JoinServerDialog) Close() {
	if d.settle != nil {
		d.settle.Stop()
	}
}

// codeTyped previews what the field's code opens, once the typing has stopped.
// Every prefix of a code parses as one, so the delay is what keeps a code typed
// out by hand to a single request.
func (d *JoinServerDialog) codeTyped(text string) {
	if d.settle != nil {
		d.settle.Stop()
	}

	code := util.InviteCode(text)
	if code == d.previewed {
		return // what is up is what the field says
	}

	// Cleared before the ask rather than replaced after it: a card describing a
	// code the field no longer holds is beside a button that would redeem the one
	// it does.
	d.setPreview("", nil)

	if code == "" {
		return
	}

	d.settle = time.AfterFunc(invitePreviewDelay, func() {
		DoOnUI(func() { d.resolvePreview(code) })
	})
}

// resolvePreview asks what a code opens and draws the answer beside the button
// that would redeem it. A failure leaves the empty slot rather than reporting
// anything: a code half typed comes back refused exactly as an expired one does,
// and the field is still being worked in. The status line answers for a join
// actually attempted.
func (d *JoinServerDialog) resolvePreview(code string) {
	d.deps.Actions.ResolveInvite(code, func(invite domain.Invite, err error) {
		// The field has moved on: what came back is not what it now says.
		if err != nil || code != util.InviteCode(d.entry.Text) {
			return
		}

		d.setPreview(code, NewInviteCardFor(d.deps, invite))
	})
}

// setPreview mounts a card, or clears the slot when card is nil. The dialog is
// centred at the size it was mounted at, so the layer is told either way.
func (d *JoinServerDialog) setPreview(code string, card *InviteCard) {
	if code == d.previewed && card == nil {
		return // already empty; nothing to re-place
	}
	d.previewed = code

	d.preview.Objects = nil
	if card != nil {
		d.preview.Objects = []fyne.CanvasObject{container.NewCenter(card.Content)}
	}
	d.preview.Refresh()

	// Hidden while there is nothing to preview: an empty but visible slot still
	// costs the gap either side of it — see spacedColumn.
	if card == nil {
		d.preview.Hide()
	} else {
		d.preview.Show()
	}

	if d.OnResize != nil {
		d.OnResize()
	}
}

// dialogHeader is the title row every card on the modal layer wears: the heading
// centred across the whole card, with the close button laid *over* its right edge
// so it does not shift the title off centre. The button is centred to keep its
// square minimum rather than be stretched to the row height.
func dialogHeader(title string, onClose func()) fyne.CanvasObject {
	heading := newBoldText(title, theme.Colors.TextPrimary, theme.Sizes.ConfirmTitleSize)
	heading.Alignment = fyne.TextAlignCenter

	closeButton := container.NewBorder(nil, nil, nil, container.NewCenter(NewCloseButton(onClose)))

	return container.NewStack(vcenter(heading), closeButton)
}

// newDialogCard is the surface every card on the modal layer is drawn on — the
// confirmation's included, which is why the radius, the hairline and the shadow
// are here rather than in one card's constructor. Two cards on the same layer a
// shade or a corner apart read as a mistake, and a flat one over a dimmed window
// has nothing but the dimming to say it is in front.
func newDialogCard() *canvas.Rectangle {
	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.ConfirmRadius
	Outline(card)
	Elevate(card)

	return card
}

// dialogStatus is the line a card reports its own outcome on, a second dialog
// stacked over the modal layer being one that covers the field it is about. It is
// mounted empty rather than added on demand, so the card does not jump when a
// message appears.
type dialogStatus struct {
	text *canvas.Text
	line *fyne.Container
}

func newDialogStatus() dialogStatus {
	text := newText("", theme.Colors.TimestampText, theme.Sizes.DialogDetailSize)

	return dialogStatus{
		text: text,
		// Indented to meet the labels around it, which carry the widget inner padding
		// a raw canvas.Text does not.
		line: container.NewBorder(nil, nil, HorizontalSpacer(fynetheme.InnerPadding()), nil, text),
	}
}

// row is the line to put in the card's column. It starts hidden: the card is
// built before anything has gone wrong, and a visible empty line costs the gap
// around it — see spacedColumn.
func (s dialogStatus) row() fyne.CanvasObject {
	s.line.Hide()

	return s.line
}

func (s dialogStatus) set(message string, fill color.Color) {
	s.text.Text = message
	s.text.Color = fill
	s.text.Refresh()

	if message == "" {
		s.line.Hide()

		return
	}

	s.line.Show()
}

/* Prompt dialog */

// PromptField is one line of a prompt: what to call it, what stands in it while
// it is empty, and whether what is typed should be hidden.
type PromptField struct {
	Label       string
	Placeholder string

	// Value is what the field opens with, for a card that changes something rather
	// than asking for it new: a rename that made the reader retype the name would
	// be asking them to remember it.
	Value string

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
	action *Button
}

// NewPromptDialog builds the card. onClose dismisses the modal layer.
func NewPromptDialog(prompt Prompt, onClose func()) *PromptDialog {
	d := &PromptDialog{}

	rows := []fyne.CanvasObject{dialogHeader(prompt.Title, onClose), NewRowDivider()}
	entries := make([]*modalEntry, 0, len(prompt.Fields))

	for _, field := range prompt.Fields {
		entry := newModalEntry(onClose)
		entry.SetPlaceHolder(field.Placeholder)
		entry.SetText(field.Value)
		entry.Password = field.Password

		// Tap rather than the action itself: calling that bypasses the button's own
		// disabled check, so Enter during an in-flight request would send it twice.
		entry.OnSubmitted = func(string) { d.action.Tap() }

		entries = append(entries, entry)
		rows = append(rows, dialogField(field.Label, fieldSurface(entry)))
	}
	if len(entries) > 0 {
		d.Entry = entries[0]
	}

	d.status = newDialogStatus()

	d.action = NewWeightedButton(prompt.Action, ButtonPrimary, func() {
		values := promptValues(entries)
		if values == nil {
			return
		}

		d.status.set(prompt.Busy, theme.Colors.TimestampText)
		d.action.Disable()
		prompt.OnSubmit(values)
	})

	rows = append(rows, d.status.row(), d.action)

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

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

/* Second-factor challenge */

// ChallengeMethod is one way an account can prove it is the account: what to call
// it where it is picked, and what to call the field once it is. Both are the
// controller's — which factors Revolt will accept is a question about the account
// rather than about this card.
type ChallengeMethod struct {
	Label  string // "Authenticator app"
	Prompt string // "The six-digit code from your authenticator app"
}

// ChallengeDialog asks for that proof. It is its own card rather than a Prompt
// with a picker bolted on, because the two answers are not independent: which
// method is chosen is what the field is *called*, so the label has to follow the
// pick — and a Prompt's fields are fixed once it is built.
//
// The field is masked whichever method is chosen. A password must be; a recovery
// code is a secret written down once; and a TOTP code masked is at worst
// unremarkable, where a field that changed its mind about hiding what is in it as
// the picker moved would be worse than either.
type ChallengeDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to focus
	// once it is up.
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	status dialogStatus
	action *Button
}

// NewChallengeDialog builds the card. purpose is the line under the title saying
// what the proof is *for* — the card is raised by half a dozen different actions
// and is otherwise identical for all of them. onSubmit is called on the UI thread
// with the index of the chosen method and what was typed; the caller owns the
// request, so the card says only that one is out and is closed or failed by them.
func NewChallengeDialog(purpose string, methods []ChallengeMethod,
	onSubmit func(method int, code string), onClose func()) *ChallengeDialog {

	d := &ChallengeDialog{}

	if len(methods) == 0 {
		methods = []ChallengeMethod{{Label: "Password", Prompt: "Password"}}
	}
	picked := 0

	code := newModalEntry(onClose)
	code.Password = true
	code.OnSubmitted = func(string) { d.action.Tap() }

	// The box rather than the text: what a pick re-labels is the fitted box, the
	// text object holding whatever last fitted the card's width. Upper-cased as
	// every other field label on a card is — the *sentence* explaining what is
	// being asked for is the purpose line at the top, said once.
	label := NewEllipsisText(newBoldText(strings.ToUpper(methods[0].Prompt),
		theme.Colors.CategoryText, theme.Sizes.DialogLabelSize))

	rows := []fyne.CanvasObject{
		dialogHeader("Confirm it's you", onClose),
		NewRowDivider(),
		NewWrappedText(purpose, theme.Sizes.ChannelDialogWidth, theme.Sizes.DialogDetailSize,
			theme.Colors.TimestampText),
	}

	// The picker is drawn only where there is something to pick between, which for
	// most accounts there is not: a password is the only factor they have.
	if len(methods) > 1 {
		options := make([]settingsOption, len(methods))
		for i, method := range methods {
			options[i] = settingsOption{Label: method.Label, Value: strconv.Itoa(i)}
		}

		var control *optionControl
		control = newOptionControl("0", options, func(value string) {
			picked, _ = strconv.Atoi(value)
			control.set(value)

			SetEllipsisText(label, strings.ToUpper(methods[picked].Prompt))
		})

		rows = append(rows, dialogField("Method", control))
	}

	rows = append(rows, VBoxNoSpacing(
		label,
		VerticalSpacer(theme.Sizes.DialogLabelGap),
		fieldSurface(code),
	))

	d.status = newDialogStatus()
	d.action = NewWeightedButton("Confirm", ButtonPrimary, func() {
		if code.Text == "" {
			return
		}

		d.status.set("Checking...", theme.Colors.TimestampText)
		d.action.Disable()

		onSubmit(picked, code.Text)
	})

	rows = append(rows, d.status.row(), d.action)

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))
	d.Entry = code

	return d
}

// Fail reports a refused answer and re-enables the button, so a mistyped code can
// be corrected in the field it came from. Call on the UI thread.
func (d *ChallengeDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.action.Enable()
}

/* Setting up an authenticator */

// SecretDialog is the second half of turning TOTP on: the shared secret, and the
// code proving it was stored. Both have to be on one card — the secret is shown
// once and asking for the code afterwards would mean showing it again — and the
// code is the *only* proof worth taking here, a password saying nothing about
// whether the authenticator was actually set up.
//
// The secret is selectable rather than drawn as text: it is a string somebody has
// to get into another program, and a copy button is the whole of what this card
// is for besides the field.
type SecretDialog struct {
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	status dialogStatus
	action *Button
}

// NewSecretDialog builds it. onSubmit takes the typed code on the UI thread; the
// caller owns the request and closes the card or fails it.
func NewSecretDialog(secret string, onSubmit func(code string), onClose func()) *SecretDialog {
	d := &SecretDialog{}

	code := newModalEntry(onClose)
	code.SetPlaceHolder("000000")
	code.OnSubmitted = func(string) { d.action.Tap() }

	d.status = newDialogStatus()
	d.action = NewWeightedButton("Turn on", ButtonPrimary, func() {
		if code.Text == "" {
			return
		}

		d.status.set("Checking...", theme.Colors.TimestampText)
		d.action.Disable()
		onSubmit(code.Text)
	})

	rows := []fyne.CanvasObject{
		dialogHeader("Set up your authenticator", onClose),
		NewRowDivider(),
		NewWrappedText(
			"Add this key to your authenticator app, then enter the code it gives you. "+
				"It is shown once. Starting again generates a different one.",
			theme.Sizes.ChannelDialogWidth, theme.Sizes.DialogDetailSize, theme.Colors.TimestampText),
		dialogField("Setup key", newSecretWell(secret)),
		dialogField("Code from the app", fieldSurface(code)),
		d.status.row(),
		d.action,
	}

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))
	d.Entry = code

	return d
}

// Fail reports a refused code and re-enables the button. Call on the UI thread.
func (d *SecretDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.action.Enable()
}

/* Recovery codes */

// NewCodesDialog shows a set of recovery codes and nothing else. There is no
// action on it: the codes *are* the outcome, and the only thing to do with them
// is take a copy — which is why the button copies rather than confirms.
//
// It is raised both by asking to see the codes and by generating new ones, so
// what it says about them is the caller's: one is a reminder, the other is the
// only time the new set will ever be legible.
func NewCodesDialog(purpose string, codes []string, onClose func()) fyne.CanvasObject {
	joined := strings.Join(codes, "\n")

	body := newSecretWell(joined)
	if len(codes) == 0 {
		body = newSecretWell("There are no codes on this account.")
	}

	rows := []fyne.CanvasObject{
		dialogHeader("Recovery codes", onClose),
		NewRowDivider(),
		NewWrappedText(purpose, theme.Sizes.ChannelDialogWidth, theme.Sizes.DialogDetailSize,
			theme.Colors.TimestampText),
		body,
		NewWeightedButton("Copy", ButtonPrimary, func() { CopyToClipboard(joined) }),
	}

	padding := theme.Sizes.DialogPadding
	card := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	return newTapSink(container.NewStack(newDialogCard(), card))
}

// newSecretWell is a string somebody has to read off the screen and get into
// another program: monospaced so a zero and an O are different shapes, selectable
// so it can be copied by hand, and on the same field surface every other value on
// a card sits on.
func newSecretWell(text string) fyne.CanvasObject {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading,
		fyne.TextStyle{Monospace: true})
	label.Selectable = true
	label.Wrapping = fyne.TextWrapBreak

	padding := theme.Sizes.SettingsRowPaddingH

	return container.NewStack(newFieldBackground(), NewInset(label, 0, 0, padding, padding))
}

/* Channel dialog */

// topicRows is how much of a topic is on screen at once: enough for the sentence
// most channels have, without the card being mostly one field.
const topicRows = 3

// ChannelSettings is a channel as the card found it, and as it would leave it.
//
// Slowmode and UserLimit are pointers because not every channel has them — a
// group has no send cooldown, and only a voice channel has a user limit. A nil
// one is a row the card leaves out and a field the request omits, which is the
// same rule read twice: what comes back is what may be sent.
type ChannelSettings struct {
	Name        string
	Description string

	Slowmode  *time.Duration
	UserLimit *int

	// Voice follows the same rule for the one thing only a *new* channel decides:
	// Revolt takes the kind at creation and never again, an edit that named one
	// being what would turn a text channel into a voice one. So a nil is an edit
	// and the row is left out; a non-nil is a creation and the row is drawn.
	Voice *bool

	NSFW bool
}

// ChannelDialog edits a channel. Not a Prompt: a prompt asks from empty and
// refuses a blank field, where this one opens on what the channel already is,
// takes more than typing, and lets a topic be nothing.
type ChannelDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to focus
	// once it is up.
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	status dialogStatus
	action *Button
}

// NewChannelDialog builds the card, opened on current. onSubmit is called on the
// UI thread with what the fields hold; onClose dismisses the modal layer.
func NewChannelDialog(current ChannelSettings, onSubmit func(ChannelSettings), onClose func()) *ChannelDialog {
	return newChannelDialog("Edit channel", "Save", "Saving...", current, onSubmit, onClose)
}

// NewChannelCreateDialog is the same card asking from empty. Revolt takes only a
// name, a topic, a kind and the age gate at creation — no cooldown and no user
// limit — so those two rows are absent by the rule that already leaves them out
// of a group's edit, and the channel is edited for them once it exists.
func NewChannelCreateDialog(onSubmit func(ChannelSettings), onClose func()) *ChannelDialog {
	text := false

	return newChannelDialog("New channel", "Create", "Creating...",
		ChannelSettings{Voice: &text}, onSubmit, onClose)
}

func newChannelDialog(title, action, pending string, current ChannelSettings,
	onSubmit func(ChannelSettings), onClose func()) *ChannelDialog {
	d := &ChannelDialog{}

	name := newModalEntry(onClose)
	name.SetPlaceHolder("general")
	name.SetText(current.Name)

	// Enter in the name field saves, as it does in a prompt; the topic keeps Enter
	// for a line break, being the one field with more than one line to give.
	name.OnSubmitted = func(string) { d.action.Tap() }

	topic := newModalEntry(onClose)
	topic.MultiLine = true
	topic.Wrapping = fyne.TextWrapWord
	topic.SetMinRowsVisible(topicRows)
	topic.SetPlaceHolder("What this channel is for")
	topic.SetText(current.Description)

	nsfw := NewToggle(current.NSFW, nil)

	fields := []fyne.CanvasObject{
		dialogField("Name", fieldSurface(name)),
		dialogField("Topic", fieldSurface(topic)),
	}

	// The kind leads the rest: it is the one answer that cannot be changed
	// afterwards, so it is decided before the fields that can.
	var kind *choiceField
	if current.Voice != nil {
		kind = newChoiceField(boolChoice(*current.Voice), []int{0, 1}, channelKindLabel)
		fields = append(fields, dialogField("Kind", kind.control))
	}

	var slowmode, limit *choiceField
	if current.Slowmode != nil {
		slowmode = newChoiceField(int(*current.Slowmode/time.Second), slowmodeChoices, slowmodeLabel)
		fields = append(fields, dialogField("Slowmode", slowmode.control))
	}
	if current.UserLimit != nil {
		limit = newChoiceField(*current.UserLimit, userLimitChoices, userLimitLabel)
		fields = append(fields, dialogField("User limit", limit.control))
	}

	fields = append(fields, dialogSwitch("Age-restricted", "Hidden until the reader agrees to see it.", nsfw))

	d.status = newDialogStatus()
	d.action = NewWeightedButton(action, ButtonPrimary, func() {
		d.status.set(pending, theme.Colors.TimestampText)
		d.action.Disable()

		edited := ChannelSettings{Name: name.Text, Description: topic.Text, NSFW: nsfw.On()}
		if kind != nil {
			voice := kind.value == 1
			edited.Voice = &voice
		}
		if slowmode != nil {
			cooldown := time.Duration(slowmode.value) * time.Second
			edited.Slowmode = &cooldown
		}
		if limit != nil {
			edited.UserLimit = &limit.value
		}

		onSubmit(edited)
	})

	rows := []fyne.CanvasObject{dialogHeader(title, onClose), NewRowDivider()}
	rows = append(rows, fields...)
	rows = append(rows, d.status.row(), d.action)

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), body))
	d.Entry = name

	return d
}

// Fail reports a refused edit and re-enables the button, so the fields it came
// from can be corrected and sent again. Call on the UI thread.
func (d *ChannelDialog) Fail(message string) {
	d.status.set(message, theme.Colors.ErrorText)
	d.action.Enable()
}

/* Ban dialog */

// BanRequest is what a ban card is answered with. Both are optional: an empty
// reason and a zero window are the plain ban that holding Shift sends.
type BanRequest struct {
	Reason         string
	DeleteMessages time.Duration
}

// BanDialog asks for a ban's terms. Not a Confirm, which is answered yes or no:
// the route takes a reason and a window of the member's recent messages, and
// neither is worth a second card.
type BanDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to focus
	// once it is up.
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	action *Button
}

// NewBanDialog builds the card for banning name. onSubmit is called on the UI
// thread with what the fields hold; onClose dismisses the modal layer, and
// closing after a submit is the caller's — the outcome is a notice, as a kick's
// is, rather than a line on a card left standing.
func NewBanDialog(name string, onSubmit func(BanRequest), onClose func()) *BanDialog {
	d := &BanDialog{}

	reason := newModalEntry(onClose)
	reason.SetPlaceHolder("Optional, kept with the ban")
	reason.OnSubmitted = func(string) { d.action.Tap() }

	deletion := newChoiceField(0, banDeleteChoices, banDeleteLabel)

	note := NewWrappedText(
		fmt.Sprintf("%s will be banned and cannot come back until the ban is lifted.", name),
		theme.Sizes.ChannelDialogWidth, theme.Sizes.DialogDetailSize, theme.Colors.TimestampText)

	d.action = NewWeightedButton("Ban", ButtonDanger, func() {
		onSubmit(BanRequest{
			Reason:         reason.Text,
			DeleteMessages: time.Duration(deletion.value) * time.Second,
		})
	})

	// Cancel first and full width, as a confirmation's buttons are: a card this one
	// is answered by position rather than by reading a small label.
	buttons := container.NewGridWithColumns(2, NewButton("Cancel", onClose), d.action)

	rows := []fyne.CanvasObject{
		dialogHeader("Ban member", onClose),
		NewRowDivider(),
		note,
		dialogField("Reason", fieldSurface(reason)),
		dialogField("Delete recent messages", deletion.control),
		buttons,
	}
	if hint := shiftSkipHint(); hint != nil {
		rows = append(rows, hint)
	}

	padding := theme.Sizes.DialogPadding
	card := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	d.Content = newTapSink(container.NewStack(newDialogCard(), card))
	d.Entry = reason

	return d
}

/* The pieces a card's fields are built from */

// dialogField is a labelled control: the label above rather than beside it, a
// field being as wide as the card and a caption beside one leaving no room for
// what it names.
func dialogField(label string, control fyne.CanvasObject) fyne.CanvasObject {
	return VBoxNoSpacing(
		newBoldText(strings.ToUpper(label), theme.Colors.CategoryText, theme.Sizes.DialogLabelSize),
		VerticalSpacer(theme.Sizes.DialogLabelGap),
		control,
	)
}

// dialogSwitch is a boolean: what it is on the left, the switch held to the right
// at its own size — a row would otherwise stretch the pill to its whole height.
func dialogSwitch(label, detail string, toggle *Toggle) fyne.CanvasObject {
	text := VBoxNoSpacing(
		newText(label, theme.Colors.TextPrimary, 0),
		VerticalSpacer(theme.Sizes.DialogLabelGap),
		newText(detail, theme.Colors.TimestampText, theme.Sizes.DialogDetailSize),
	)

	return NewFillRow(0, vcenter(text), HorizontalSpacer(theme.Sizes.DialogFieldGap), vcenter(toggle))
}

// spacedColumn stacks rows with one gap between each pair. A VBox's own spacing
// is the theme's padding, which is what a form's fields were reading as: evenly
// spread, with the label no closer to its field than to the one above.
func spacedColumn(gap float32, rows ...fyne.CanvasObject) *fyne.Container {
	// NewGapColumn rather than a column of spacers: a card's optional rows — the
	// invite preview, the status line — are hidden while they have nothing to say,
	// and a spacer either side of a hidden row is a hole the reader can see.
	return NewGapColumn(gap, rows...)
}

// choiceField is a dropdown holding a number until the card is answered. The
// settings page's control reports every pick to whoever owns the setting; here
// nothing is owned until Save, so the value is kept beside the control.
type choiceField struct {
	control *optionControl
	value   int
}

// newChoiceField builds one over choices, which current joins if it is not
// already among them — a channel set from another client must not have its
// setting quietly rounded to the nearest thing this card offers.
func newChoiceField(current int, choices []int, label func(int) string) *choiceField {
	f := &choiceField{value: current}

	option := func(value int) settingsOption {
		return settingsOption{Label: label(value), Value: strconv.Itoa(value)}
	}

	options := make([]settingsOption, 0, len(choices)+1)
	listed := slices.Contains(choices, current)

	for _, choice := range choices {
		if !listed && current < choice {
			options = append(options, option(current))
			listed = true
		}
		options = append(options, option(choice))
	}
	if !listed {
		options = append(options, option(current))
	}

	f.control = newOptionControl(strconv.Itoa(current), options, func(picked string) {
		f.value, _ = strconv.Atoi(picked)
		f.control.set(picked)
	})

	return f
}

// What the two number fields offer. Slowmode is Revolt's own ladder, ending at
// the route's six-hour ceiling; the user limit is a voice channel's, where the
// numbers are only ever round.
var (
	slowmodeChoices  = []int{0, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 21600}
	userLimitChoices = []int{0, 2, 5, 10, 15, 25, 50, 100}
)

// channelKindLabel names the two kinds a channel can be created as. Revolt takes
// "Text" or "Voice" and nothing else, and a voice channel is a text channel
// carrying a voice object — so the client's own glyph is the only thing that
// tells them apart afterwards.
func channelKindLabel(voice int) string {
	if voice == 1 {
		return "Voice"
	}

	return "Text"
}

// boolChoice is a bool as a choiceField holds it, that field being numeric for
// the two duration ladders it was built for.
func boolChoice(on bool) int {
	if on {
		return 1
	}

	return 0
}

func slowmodeLabel(seconds int) string {
	if seconds <= 0 {
		return "Off"
	}

	return util.ShortDuration(time.Duration(seconds) * time.Second)
}

func userLimitLabel(users int) string {
	switch {
	case users <= 0:
		return "No limit"
	case users == 1:
		return "1 user"
	}

	return strconv.Itoa(users) + " users"
}

// banDeleteChoices is how much of a banned member's recent history may go with
// them, up to the route's seven-day ceiling.
var banDeleteChoices = []int{0, 3600, 21600, 86400, 259200, 604800}

// banDeleteLabel names one of those windows. Not util.ShortDuration, which has
// no unit above the hour: seven days reads as "168h".
func banDeleteLabel(seconds int) string {
	const day = int(24 * time.Hour / time.Second)

	switch {
	case seconds <= 0:
		return "Keep them"
	case seconds < day:
		return "Last " + util.ShortDuration(time.Duration(seconds)*time.Second)
	case seconds < 2*day:
		return "Last day"
	}

	return "Last " + strconv.Itoa(seconds/day) + " days"
}

// modalEntry is a text field on the modal layer. It handles Escape itself
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

/* A value on a card */

// SliderCard is what a menu offers where the value it names is a range rather
// than a list: a fyne.MenuItem carries text and an icon and nothing else, so a
// menu on its own can only offer the steps somebody thought of. Reading names a
// value in the caller's own units, ui having none.
type SliderCard struct {
	Title string

	// Icon leads the title and says what *kind* of value this is — which
	// direction a volume is, where a title only names whose it is. Nil for a card
	// whose title is the whole answer.
	Icon fyne.Resource

	Low, High, Step float64
	Value           float64

	// Pivot is the value the middle of the travel is pinned to, nil for a plain
	// linear scale. See Slider.SetPivot.
	Pivot *float64

	Reading   func(float64) string
	OnChanged func(float64)
}

// NewSliderCard draws one, for App.showPopover to hang beside whatever the value
// belongs to. Not a widget.PopUp: a popover is dismissed by the tap that lands
// outside it, and a card holding a control has to keep the ones that land inside.
func NewSliderCard(spec SliderCard) fyne.CanvasObject {
	pad, padV := theme.Sizes.SliderCardPadding, theme.Sizes.SliderCardPaddingV

	background := canvas.NewRectangle(theme.Colors.NoticeBg)
	background.CornerRadius = theme.Sizes.SliderCardRadius
	Outline(background)
	Elevate(background)

	// The title takes the fill slot and is ellipsised there: it is somebody's name
	// as often as it is a word, and the card's width is pinned. The reading keeps
	// its minimum against the right edge, so it stays put as the knob moves and
	// the title's end is what gives way.
	title := NewEllipsisText(newText(spec.Title, theme.Colors.TimestampText,
		theme.Sizes.SliderCardTextSize))

	reading := newBoldText(spec.reading(spec.Value), theme.Colors.TextPrimary,
		theme.Sizes.SliderCardTextSize)
	reading.Alignment = fyne.TextAlignTrailing

	slider := NewSlider(spec.Low, spec.High, spec.Step, spec.Value, func(value float64) {
		reading.Text = spec.reading(value)
		reading.Refresh()

		if spec.OnChanged != nil {
			spec.OnChanged(value)
		}
	})
	slider.SetTrack(theme.Colors.SliderCardTrack)

	if spec.Pivot != nil {
		slider.SetPivot(*spec.Pivot)
	}

	gap := theme.Sizes.SliderCardHeadGap

	head, fill := []fyne.CanvasObject{title, HorizontalSpacer(gap), reading}, 0
	if spec.Icon != nil {
		mark := newScaledIcon(tintedIcon(spec.Icon, theme.Colors.TimestampText),
			theme.Sizes.SliderCardIconSize)

		head = append([]fyne.CanvasObject{mark, HorizontalSpacer(gap)}, head...)
		fill = 2
	}

	body := VBoxNoSpacing(NewFillRow(fill, head...),
		VerticalSpacer(theme.Sizes.SliderCardGap), slider)

	return newTapSink(NewFixedWidthContainer(theme.Sizes.SliderCardWidth,
		container.NewStack(background, NewInset(body, padV, padV, pad, pad))))
}

// reading names one value, falling back to the number itself where the caller
// gave no units.
func (s SliderCard) reading(value float64) string {
	if s.Reading == nil {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}

	return s.Reading(value)
}
