package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type noScrollTheme struct {
	fyne.Theme
}

func (t *noScrollTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameScrollBar {
		return color.Transparent
	}
	return t.Theme.Color(name, variant)
}

func (t *noScrollTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameScrollBar {
		return 0
	}
	return t.Theme.Size(name)
}
