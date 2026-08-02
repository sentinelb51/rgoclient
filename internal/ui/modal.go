package ui

import (
	"fmt"
	"image/color"
	"net/url"

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

// Overlay is a modal layer drawn over the whole window: a backdrop with content
// placed on it. Fyne sizes an overlay to the canvas when it is pushed onto
// Canvas().Overlays() and routes every pointer event to the top-most overlay, so
// the backdrop both dims and blocks the UI underneath. Tapping it dismisses the
// overlay.
//
// It is a plain widget rather than a widget.PopUp because a pop-up draws its own
// themed card around the content and paints its backdrop from the theme's shadow
// colour — a light seam tint, far too faint to read as a lightbox.
//
// An anchored overlay (NewPopover) is the same layer with the content placed
// beside a widget instead of centred, and its backdrop left clear: a card that
// belongs to the thing it points at should not dim what surrounds it, but it
// still has to take the click that dismisses it.
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
// when its About section arrives, and both placements size the card from its own
// minimum. Neither re-runs on its own: Refresh repaints without laying out, and
// Resize is a no-op while the layer still fills the same canvas. Call on the UI
// thread.
func (o *Overlay) Reposition() { Relayout(o.placement) }

// Cursor keeps the normal pointer over the backdrop: tapBase advertises the hand
// for things that look clickable, and a dimmed background isn't one.
func (o *Overlay) Cursor() desktop.Cursor { return desktop.DefaultCursor }

// tapSink swallows taps on whatever it wraps. Fyne delivers a tap to the deepest
// object that accepts one, so without a sink a click anywhere on non-interactive
// content inside an Overlay would fall through to the backdrop and dismiss it.
// Buttons nested deeper still win over the sink, and so keep working.
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

// NewAttachmentViewer builds the card shown inside the attachment lightbox: a
// slim header (name, metadata, open-in-browser, close) over the attachment
// itself, sized to fit within bounds. Images render scaled to fit; text files get
// their full contents in a selectable, scrollable monospace pane. Anything else
// falls back to a card offering the browser.
//
// The card carries its own chrome — there is no native window here, so nothing
// has to be recoloured to match the palette.
func NewAttachmentViewer(deps Deps, attachment *domain.File, bounds fyne.Size, onClose func()) fyne.CanvasObject {
	// The body gets what is left of bounds once the card's own chrome is paid for:
	// NewPadded insets all four sides, and the Border layout puts one more gap
	// between the header and the body.
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
	name := canvas.NewText(attachment.Name, theme.Colors.TextPrimary)
	name.TextSize = theme.Sizes.ViewerTitleSize
	name.TextStyle = fyne.TextStyle{Bold: true}

	meta := util.FormatFileSize(attachment.Size)
	if detail != "" {
		meta = detail + "  ·  " + meta
	}
	info := canvas.NewText(meta, theme.Colors.TimestampText)
	info.TextSize = theme.Sizes.ViewerTitleSize

	actions := container.NewHBox(info, HorizontalSpacer(theme.Sizes.ViewerPadding))
	if link := attachment.URL; link != "" {
		actions.Add(NewIconButton(fynetheme.ComputerIcon(), func() {
			if u, err := url.Parse(link); err == nil {
				_ = fyne.CurrentApp().OpenURL(u)
			}
		}, nil))
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
	if link := attachment.URL; link != "" && attachment.ID != "" {
		deps.Images.LoadIntoContainer(attachment.ID, link, size, frame, false, nil)
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

// JoinServerDialog is the card shown inside the join-server modal: one field
// taking an invite code or link, a Join button, and a way to create a server
// instead. It validates the input itself (util.InviteCode), so a typo never
// costs a round trip, and reports the outcome on its own status line rather than
// in a second dialog stacked over the modal layer.
//
// The layout mirrors the login screen: a centred bold heading, separators between
// sections, section labels, and full-width controls.
type JoinServerDialog struct {
	// Content is the card to hand to the modal layer, and Entry the field to
	// focus once it is up.
	Content fyne.CanvasObject
	Entry   fyne.Focusable

	status *canvas.Text
	join   *widget.Button
}

// NewJoinServerDialog builds the dialog. onJoin receives an already-validated
// invite code, onCreate opens server creation, and onClose dismisses the modal;
// all three are called on the UI thread.
func NewJoinServerDialog(onJoin func(code string), onCreate, onClose func()) *JoinServerDialog {
	d := &JoinServerDialog{}

	entry := newInviteEntry(onClose)
	entry.SetPlaceHolder("stt.gg/dcRHWEF1")
	d.Entry = entry

	// Built empty rather than added on demand: the line holds its height from the
	// start, so the card doesn't jump when a message appears.
	d.status = canvas.NewText("", theme.Colors.TimestampText)
	d.status.TextSize = theme.Sizes.JoinDialogTextSize

	d.join = widget.NewButton("Join", func() {
		code := util.InviteCode(entry.Text)
		if code == "" {
			d.Fail("That doesn't look like an invite code or link.")
			return
		}
		d.setStatus("Joining...", theme.Colors.TimestampText)
		d.join.Disable()
		onJoin(code)
	})

	// Guarded, unlike a click: calling OnTapped directly bypasses the button's own
	// disabled check, so Enter during an in-flight join would join twice.
	entry.OnSubmitted = func(string) {
		if !d.join.Disabled() {
			d.join.OnTapped()
		}
	}

	card := canvas.NewRectangle(theme.Colors.ViewerCardBg)
	card.CornerRadius = theme.Sizes.JoinDialogCornerRadius

	inner := container.NewVBox(
		d.buildHeader(onClose),
		widget.NewSeparator(),
		widget.NewLabel("Invite code"),
		WithCaret(entry),
		d.statusLine(),
		d.join,
		widget.NewSeparator(),
		widget.NewLabel("Start your own"),
		widget.NewButton("Create a server", onCreate),
	)
	body := NewMinWidthContainer(theme.Sizes.JoinDialogWidth, container.NewPadded(inner))

	d.Content = newTapSink(container.NewStack(card, body))
	return d
}

// Fail reports a failed join and re-enables the button so the user can correct
// the code and try again. Call on the UI thread.
func (d *JoinServerDialog) Fail(message string) {
	d.setStatus(message, theme.Colors.ErrorText)
	d.join.Enable()
}

// Notice reports a neutral message on the status line — nothing failed, there is
// just something to say. Call on the UI thread.
func (d *JoinServerDialog) Notice(message string) {
	d.setStatus(message, theme.Colors.TimestampText)
}

func (d *JoinServerDialog) setStatus(message string, textColor color.Color) {
	d.status.Text = message
	d.status.Color = textColor
	d.status.Refresh()
}

// buildHeader is the title row: the heading is centred across the whole card, as
// on the login screen, with the close button laid over its right edge so the
// button doesn't shift the title off centre.
func (d *JoinServerDialog) buildHeader(onClose func()) fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Join a server", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Centred so the button keeps its square min size instead of being stretched
	// to the row height by the border layout.
	closeButton := container.NewBorder(nil, nil, nil, container.NewCenter(NewCloseButton(onClose)))

	return container.NewStack(title, closeButton)
}

// statusLine indents the status text to line up with the labels around it, which
// carry the widget inner padding that raw canvas text does not.
func (d *JoinServerDialog) statusLine() fyne.CanvasObject {
	return container.NewBorder(nil, nil, HorizontalSpacer(fynetheme.InnerPadding()), nil, d.status)
}

// inviteEntry is the dialog's single-line field. It handles Escape itself
// because a focused entry is the end of the line for key events: Fyne routes them
// to the focused widget and never reaches the canvas handler the modal layer uses
// to dismiss on Escape.
type inviteEntry struct {
	widget.Entry
	onCancel func()
}

func newInviteEntry(onCancel func()) *inviteEntry {
	e := &inviteEntry{onCancel: onCancel}
	e.ExtendBaseWidget(e)

	return e
}

func (e *inviteEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		if e.onCancel != nil {
			e.onCancel()
		}
		return
	}

	e.Entry.TypedKey(key)
}
