// Package assets embeds binary assets (currently fonts) so they ship inside the
// executable rather than being loaded from disk at runtime. It lives at the repo
// root because go:embed cannot reference paths above the embedding source file.
package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// Static weight instances. Fyne does not instance the variable font's weight
// axis (it renders the lightest master, so text looks too thin), so the fixed
// Regular/Bold/Italic cuts are embedded instead.
//
//go:embed fonts/Montserrat-Regular.ttf
var montserratRegular []byte

//go:embed fonts/Montserrat-Bold.ttf
var montserratBold []byte

//go:embed fonts/Montserrat-Italic.ttf
var montserratItalic []byte

//go:embed fonts/Montserrat-BoldItalic.ttf
var montserratBoldItalic []byte

// Montserrat font resources.
var (
	MontserratRegular    fyne.Resource = fyne.NewStaticResource("Montserrat-Regular.ttf", montserratRegular)
	MontserratBold       fyne.Resource = fyne.NewStaticResource("Montserrat-Bold.ttf", montserratBold)
	MontserratItalic     fyne.Resource = fyne.NewStaticResource("Montserrat-Italic.ttf", montserratItalic)
	MontserratBoldItalic fyne.Resource = fyne.NewStaticResource("Montserrat-BoldItalic.ttf", montserratBoldItalic)
)
