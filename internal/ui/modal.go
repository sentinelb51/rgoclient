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
		browse := NewGlyphButton(fynetheme.ComputerIcon(), func() { openURL(link) })
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
				func() { openURL(link) }),
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

	v.deps.Images.LoadAsync(imageCacheID(link), link, false, CopyImageToClipboard)
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
	join   *Button
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

	inner := container.NewVBox(
		dialogHeader("Join a server", onClose),
		widget.NewSeparator(),
		widget.NewLabel("Invite code"),
		WithCaret(entry),
		d.status.row(),
		d.join,
		widget.NewSeparator(),
		widget.NewLabel("Start your own"),
		NewButton("Create a server", onCreate),
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
	action *Button
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

		// Tap rather than the action itself: calling that bypasses the button's own
		// disabled check, so Enter during an in-flight request would send it twice.
		entry.OnSubmitted = func(string) { d.action.Tap() }

		entries = append(entries, entry)
		rows = append(rows, widget.NewLabel(field.Label), WithCaret(entry))
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
	d.action = NewWeightedButton("Save", ButtonPrimary, func() {
		d.status.set("Saving...", theme.Colors.TimestampText)
		d.action.Disable()

		edited := ChannelSettings{Name: name.Text, Description: topic.Text, NSFW: nsfw.On()}
		if slowmode != nil {
			cooldown := time.Duration(slowmode.value) * time.Second
			edited.Slowmode = &cooldown
		}
		if limit != nil {
			edited.UserLimit = &limit.value
		}

		onSubmit(edited)
	})

	rows := []fyne.CanvasObject{dialogHeader("Edit channel", onClose), widget.NewSeparator()}
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
		newText(detail, theme.Colors.TimestampText, theme.Sizes.JoinDialogTextSize),
	)

	return NewFillRow(0, vcenter(text), HorizontalSpacer(theme.Sizes.DialogFieldGap), vcenter(toggle))
}

// spacedColumn stacks rows with one gap between each pair. A VBox's own spacing
// is the theme's padding, which is what a form's fields were reading as: evenly
// spread, with the label no closer to its field than to the one above.
func spacedColumn(gap float32, rows ...fyne.CanvasObject) *fyne.Container {
	stacked := make([]fyne.CanvasObject, 0, 2*len(rows)-1)

	for i, row := range rows {
		if i > 0 {
			stacked = append(stacked, VerticalSpacer(gap))
		}
		stacked = append(stacked, row)
	}

	return VBoxNoSpacing(stacked...)
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
