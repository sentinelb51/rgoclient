package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// Icons are embedded rather than read from disk so the client runs correctly
// from any working directory and never pays file I/O while building widgets.
// Everything else the UI draws comes from Fyne's own theme icon set.

//go:embed mention.svg
var mentionSVG []byte

//go:embed rgo.png
var appIconPNG []byte

var (
	// MentionIcon marks the "also mention the author" toggle on a reply card.
	MentionIcon fyne.Resource = fyne.NewStaticResource("mention.svg", mentionSVG)

	// AppIcon is the window/taskbar icon.
	AppIcon fyne.Resource = fyne.NewStaticResource("rgo.png", appIconPNG)
)
