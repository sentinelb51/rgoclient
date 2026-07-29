package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// JoinServerDialog is the card shown inside the join-server modal: one field
// taking an invite code or link, a Join button, and (eventually) a way to create
// a server instead. It validates the input itself (util.InviteCode), so a typo
// never costs a round trip, and reports the outcome on its own status line
// rather than in a separate error dialog — a second dialog over a modal layer
// reads as a stack of windows.
//
// The layout deliberately mirrors the login screen: a centred bold heading,
// separators between sections, section labels, and full-width controls. Like the
// attachment viewer the card carries its own chrome — there is no native window
// here, so nothing has to be recoloured to match the palette.
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

	// Built empty rather than added on demand: the line holds its height from
	// the start, so the card doesn't jump when a message appears.
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
	// Guarded, unlike a click: calling OnTapped directly bypasses the button's
	// own disabled check, so Enter during an in-flight join would join twice.
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

// buildHeader is the title row: the heading is centred across the whole card
// (as on the login screen) with the close button laid over its right edge, so
// the button doesn't shift the title off centre.
func (d *JoinServerDialog) buildHeader(onClose func()) fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Join a server", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	// Centred so the button keeps its square min size instead of being stretched
	// to the row height by the border layout.
	closeButton := container.NewBorder(nil, nil, nil, container.NewCenter(NewCloseButton(onClose)))

	return container.NewStack(title, closeButton)
}

// statusLine indents the status text to line up with the labels around it,
// which carry the widget inner padding that raw canvas text does not.
func (d *JoinServerDialog) statusLine() fyne.CanvasObject {
	return container.NewBorder(nil, nil, HorizontalSpacer(fynetheme.InnerPadding()), nil, d.status)
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

// setStatus repaints the status line under the entry.
func (d *JoinServerDialog) setStatus(message string, textColor color.Color) {
	d.status.Text = message
	d.status.Color = textColor
	d.status.Refresh()
}

// inviteEntry is the dialog's single-line field. It handles Escape itself
// because a focused entry is the end of the line for key events: Fyne routes
// them to the focused widget and never reaches the canvas handler the modal
// layer uses to dismiss on Escape.
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
