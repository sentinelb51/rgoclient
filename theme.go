package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// AppColors defines the color palette for the application.
// Centralizing colors makes it easy to maintain consistency and support theming.
var AppColors = struct {
	ServerListBackground   color.Color
	ChannelListBackground  color.Color
	MessageAreaBackground  color.Color
	MessageHoverBackground color.Color
	ChannelHoverBackground color.Color
	ChannelSelectedBg      color.Color
	ServerDefaultBg        color.Color
	ServerHoverBg          color.Color
	ServerSelectedBg       color.Color
	ServerNameBg           color.Color
	AvatarPlaceholder      color.Color
	HashtagIcon            color.Color
	CategoryText           color.Color
	CategoryArrow          color.Color
	CategoryIndicator      color.Color
	TextPrimary            color.Color
}{
	ServerListBackground:   color.RGBA{R: 20, G: 20, B: 20, A: 255},
	ChannelListBackground:  color.RGBA{R: 44, G: 44, B: 44, A: 255},
	MessageAreaBackground:  color.RGBA{R: 28, G: 28, B: 28, A: 255},
	MessageHoverBackground: color.RGBA{R: 45, G: 45, B: 45, A: 255},
	ChannelHoverBackground: color.RGBA{R: 60, G: 60, B: 60, A: 255},
	ChannelSelectedBg:      color.RGBA{R: 80, G: 80, B: 80, A: 255},
	ServerDefaultBg:        color.RGBA{R: 60, G: 60, B: 60, A: 255},
	ServerHoverBg:          color.RGBA{R: 80, G: 80, B: 80, A: 255},
	ServerSelectedBg:       color.RGBA{R: 114, G: 137, B: 218, A: 255}, // "Blurple"
	ServerNameBg:           color.RGBA{R: 30, G: 30, B: 30, A: 240},    // Dark semi-transparent
	AvatarPlaceholder:      color.RGBA{R: 100, G: 100, B: 200, A: 255},
	HashtagIcon:            color.RGBA{R: 180, G: 180, B: 180, A: 255},
	CategoryText:           color.RGBA{R: 150, G: 150, B: 150, A: 255},
	CategoryArrow:          color.RGBA{R: 150, G: 150, B: 150, A: 255},
	CategoryIndicator:      color.RGBA{R: 140, G: 140, B: 140, A: 255}, // Grey for +/- icons
	TextPrimary:            color.White,
}

// AppSizes defines standard sizes used throughout the application.
var AppSizes = struct {
	ServerSidebarWidth      float32
	ChannelSidebarWidth     float32
	ChannelSidebarPadding   float32
	ChannelLeftPadding      float32
	ServerIconSize          float32
	ServerItemHeight        float32
	AvatarSize              float32
	SessionCardAvatarSize   float32
	MessageImageMaxWidth    float32
	MessageImageMaxHeight   float32
	HashtagIconSize         float32
	CategoryHeight          float32
	CategorySpacing         float32
	CategoryIndicatorSize   float32
	CategoryIndicatorStroke float32
	WindowDefaultWidth      float32
	WindowDefaultHeight     float32
}{
	ServerSidebarWidth:      60,
	ChannelSidebarWidth:     240,
	ChannelSidebarPadding:   6,
	ChannelLeftPadding:      8,
	ServerIconSize:          40,
	ServerItemHeight:        50,
	AvatarSize:              40,
	SessionCardAvatarSize:   32,
	MessageImageMaxWidth:    400,
	MessageImageMaxHeight:   300,
	HashtagIconSize:         20,
	CategoryHeight:          32,
	CategorySpacing:         10,
	CategoryIndicatorSize:   14,
	CategoryIndicatorStroke: 2,
	WindowDefaultWidth:      1000,
	WindowDefaultHeight:     600,
}

// noScrollTheme hides scrollbars for a cleaner look.
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
