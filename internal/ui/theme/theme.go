package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Colors defines the color palette for the application.
// Centralizing colors makes it easy to maintain consistency and support theming.
var Colors = struct {
	// Backgrounds
	ServerListBackground   color.Color
	ChannelListBackground  color.Color
	MessageAreaBackground  color.Color
	MessageHoverBackground color.Color
	ChannelHoverBackground color.Color
	ChannelSelectedBg      color.Color
	ServerDefaultBg        color.Color
	ServerHoverBg          color.Color
	ServerSelectedBg       color.Color
	TappableHoverBg        color.Color

	// Elements
	AvatarPlaceholder color.Color
	HashtagIcon       color.Color
	CategoryText      color.Color
	CategoryArrow     color.Color
	CategoryIndicator color.Color
	TextPrimary       color.Color
	TimestampText     color.Color
	XButtonNormal     color.Color
	XButtonHover      color.Color
	SessionCardBg     color.Color
	UnreadIndicator   color.Color
}{
	// Backgrounds
	ServerListBackground:   color.RGBA{R: 20, G: 20, B: 20, A: 255},
	ChannelListBackground:  color.RGBA{R: 44, G: 44, B: 44, A: 255},
	MessageAreaBackground:  color.RGBA{R: 28, G: 28, B: 28, A: 255},
	MessageHoverBackground: color.RGBA{R: 45, G: 45, B: 45, A: 255},
	ChannelHoverBackground: color.RGBA{R: 60, G: 60, B: 60, A: 255},
	ChannelSelectedBg:      color.RGBA{R: 80, G: 80, B: 80, A: 255},
	ServerDefaultBg:        color.RGBA{R: 60, G: 60, B: 60, A: 255},
	ServerHoverBg:          color.RGBA{R: 80, G: 80, B: 80, A: 255},
	ServerSelectedBg:       color.RGBA{R: 114, G: 137, B: 218, A: 255}, // "Blurple"
	TappableHoverBg:        color.RGBA{R: 70, G: 70, B: 70, A: 255},

	// Elements
	AvatarPlaceholder: color.RGBA{R: 100, G: 100, B: 200, A: 255},
	UnreadIndicator:   color.White,
	HashtagIcon:       color.RGBA{R: 150, G: 150, B: 150, A: 255},
	CategoryText:      color.RGBA{R: 150, G: 150, B: 150, A: 255},
	CategoryArrow:     color.RGBA{R: 150, G: 150, B: 150, A: 255},
	CategoryIndicator: color.RGBA{R: 140, G: 140, B: 140, A: 255},
	TextPrimary:       color.White,
	TimestampText:     color.RGBA{R: 120, G: 120, B: 120, A: 255},
	XButtonNormal:     color.RGBA{R: 150, G: 150, B: 150, A: 255},
	XButtonHover:      color.RGBA{R: 255, G: 100, B: 100, A: 255},
	SessionCardBg:     color.RGBA{R: 50, G: 50, B: 50, A: 255},
}

// Sizes defines standard sizes used throughout the application.
var Sizes = struct {
	// Sidebar
	ServerSidebarWidth    float32
	ChannelSidebarWidth   float32
	ChannelSidebarPadding float32
	ChannelLeftPadding    float32
	UnreadIndicatorWidth  float32

	// Server/Channel widgets
	ServerIconSize          float32
	ServerItemHeight        float32
	HashtagIconSize         float32
	CategoryHeight          float32
	ChannelItemHeight       float32
	CategorySpacing         float32
	CategoryIndicatorSize   float32
	CategoryIndicatorStroke float32

	// Message area
	MessageAvatarSize         float32
	MessageAvatarColumnWidth  float32
	MessageContentPadding     float32
	MessageImageMaxWidth      float32
	MessageImageMaxHeight     float32
	MessageVerticalPadding    float32
	MessageHorizontalPadding  float32
	MessageAttachmentSpacing  float32
	MessageTextLeftPadding    float32
	MessageTimestampSize      float32
	MessageTimestampTopOffset float32

	// Session/Login
	SessionCardAvatarSize float32
	XButtonSize           float32 // todo: remove?

	// Window
	WindowDefaultWidth  float32
	WindowDefaultHeight float32

	// Image viewer
	ImageViewerMaxWidth  float32
	ImageViewerMaxHeight float32
	ImageViewerMinWidth  float32
	ImageViewerMinHeight float32
}{
	// Sidebar
	ServerSidebarWidth:    60,
	ChannelSidebarWidth:   240,
	ChannelSidebarPadding: 6,
	ChannelLeftPadding:    8,
	UnreadIndicatorWidth:  1,

	// Server/Channel widgets
	ServerIconSize:          40,
	ServerItemHeight:        50,
	HashtagIconSize:         20,
	CategoryHeight:          32,
	ChannelItemHeight:       32,
	CategorySpacing:         10,
	CategoryIndicatorSize:   14,
	CategoryIndicatorStroke: 2,

	// Message area
	MessageAvatarSize:         40,
	MessageAvatarColumnWidth:  46,
	MessageContentPadding:     0,
	MessageImageMaxWidth:      400,
	MessageImageMaxHeight:     300,
	MessageVerticalPadding:    4,
	MessageHorizontalPadding:  8,
	MessageAttachmentSpacing:  4,
	MessageTextLeftPadding:    4,
	MessageTimestampSize:      12,
	MessageTimestampTopOffset: 4,

	// Session/Login
	SessionCardAvatarSize: 32,
	XButtonSize:           24,

	// Window
	WindowDefaultWidth:  1000,
	WindowDefaultHeight: 600,

	// Image viewer
	ImageViewerMaxWidth:  1200,
	ImageViewerMaxHeight: 800,
	ImageViewerMinWidth:  400,
	ImageViewerMinHeight: 300,
}

// NoScrollTheme hides scrollbars for a cleaner look.
type NoScrollTheme struct {
	fyne.Theme
}

// NewNoScrollTheme creates a theme that hides scrollbars.
func NewNoScrollTheme(base fyne.Theme) *NoScrollTheme {
	return &NoScrollTheme{Theme: base}
}

// Color returns the color for the given name and variant.
func (t *NoScrollTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameScrollBar {
		return color.Transparent
	}
	return t.Theme.Color(name, variant)
}

// Size returns the size for the given name.
func (t *NoScrollTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameScrollBar {
		return 0
	}
	return t.Theme.Size(name)
}
