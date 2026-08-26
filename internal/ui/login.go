package ui

import (
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"RGOClient/internal/ui/theme"
)

/* The screens before Ready */

// The sign-in, second-factor and signing-in screens are the first surface a
// reader sees, and until Ready there is no main UI to draw them over — so each
// one *is* the window. They are drawn as a card on the modal layer all the same:
// the same rectangle, hairline, shadow, heading and field vocabulary the join
// and prompt cards use, so the client does not open looking like a different
// program from the one it becomes.
//
// The pieces are exported rather than the screens themselves. What is asked and
// in what order is the controller's — it holds the account, the ticket and the
// saved sessions — and the shape is this package's.

// NewAuthCard is the surface those screens are drawn on: the dialog card, a
// centred heading, and the rows under it at a card's own spacing. No close
// button, unlike every other card here — there is nothing behind one to go back
// to before a session exists.
func NewAuthCard(title string, rows ...fyne.CanvasObject) fyne.CanvasObject {
	heading := newBoldText(title, theme.Colors.TextPrimary, theme.Sizes.ConfirmTitleSize)
	heading.Alignment = fyne.TextAlignCenter

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.ChannelDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, append([]fyne.CanvasObject{heading}, rows...)...),
			padding, padding, padding, padding))

	return container.NewStack(newDialogCard(), body)
}

// NewAuthField is a labelled entry on one of those screens, the shape every
// field on a card has: the name above it, upper-cased, and the entry on the
// field surface rather than bare.
func NewAuthField(label string, entry fyne.CanvasObject) fyne.CanvasObject {
	return dialogField(label, fieldSurface(entry))
}

// NewAuthCaption names a block that is not a field — the saved logins — in the
// same type a field's label is set in, so the card reads as one list of parts.
func NewAuthCaption(label string) fyne.CanvasObject {
	return newBoldText(strings.ToUpper(label), theme.Colors.CategoryText, theme.Sizes.DialogLabelSize)
}

// NewAuthNote is a sentence on one of those screens: where a code comes from,
// that there are no saved logins yet. Muted and wrapped to the card, as a
// dialog's purpose line is.
func NewAuthNote(text string) fyne.CanvasObject {
	return NewWrappedText(text, theme.Sizes.ChannelDialogWidth,
		theme.Sizes.DialogDetailSize, theme.Colors.TimestampText)
}

// NewAuthChoice is the client's own dropdown over a list of labels, for the
// second-factor screen picking between the methods an account will accept. It
// reports the index picked, the labels being what the reader chooses between and
// the caller owning what they stand for.
//
// Fyne's Select is not used for the reason the settings page does not use one:
// AppTheme zeroes the input border it draws its field with, leaving the value
// looking like a line of prose rather than a control.
func NewAuthChoice(labels []string, selected int, onPick func(index int)) fyne.CanvasObject {
	options := make([]settingsOption, len(labels))
	for i, label := range labels {
		options[i] = settingsOption{Label: label, Value: strconv.Itoa(i)}
	}

	var control *optionControl
	control = newOptionControl(strconv.Itoa(selected), options, func(value string) {
		control.set(value)

		if index, err := strconv.Atoi(value); err == nil {
			onPick(index)
		}
	})

	return control
}
