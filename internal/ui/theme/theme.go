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
	AvatarPlaceholder  color.Color
	HashtagIcon        color.Color
	CategoryText       color.Color
	CategoryArrow      color.Color
	CategoryIndicator  color.Color
	TextPrimary        color.Color
	TimestampText      color.Color
	XButtonNormal      color.Color
	XButtonHover       color.Color
	SessionCardBg      color.Color
	UnreadIndicator    color.Color
	SwiftActionBg      color.Color
	SwiftActionHoverBg color.Color
	SwiftActionText    color.Color
}{
	// Backgrounds — cool blue-slate ramp (darkest → lightest)
	ServerListBackground:   color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C
	ChannelListBackground:  color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330
	MessageAreaBackground:  color.RGBA{R: 24, G: 27, B: 36, A: 255},    // #181B24
	MessageHoverBackground: color.RGBA{R: 30, G: 34, B: 45, A: 255},    // #1E222D
	ChannelHoverBackground: color.RGBA{R: 38, G: 43, B: 58, A: 255},    // #262B3A
	ChannelSelectedBg:      color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142
	ServerDefaultBg:        color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142
	ServerHoverBg:          color.RGBA{R: 53, G: 60, B: 80, A: 255},    // #353C50
	ServerSelectedBg:       color.RGBA{R: 91, G: 124, B: 250, A: 255},  // #5B7CFA accent
	TappableHoverBg:        color.RGBA{R: 38, G: 43, B: 58, A: 255},    // #262B3A
	SwiftActionBg:          color.RGBA{R: 35, G: 40, B: 56, A: 255},    // #232838
	SwiftActionHoverBg:     color.RGBA{R: 46, G: 53, B: 72, A: 255},    // #2E3548
	SwiftActionText:        color.RGBA{R: 196, G: 201, B: 212, A: 255}, // #C4C9D4

	// Elements
	AvatarPlaceholder: color.RGBA{R: 60, G: 72, B: 110, A: 255},   // muted blue
	UnreadIndicator:   color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	HashtagIcon:       color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	CategoryText:      color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryArrow:     color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryIndicator: color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	TextPrimary:       color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	TimestampText:     color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	XButtonNormal:     color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	XButtonHover:      color.RGBA{R: 248, G: 113, B: 113, A: 255}, // #F87171
	SessionCardBg:     color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330
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

	// Swift Actions
	SwiftActionSize float32

	// Session/Login
	SessionCardAvatarSize float32

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

	// Swift Actions
	SwiftActionSize: 32,

	// Session/Login
	SessionCardAvatarSize: 32,

	// Window
	WindowDefaultWidth:  1000,
	WindowDefaultHeight: 600,

	// Image viewer
	ImageViewerMaxWidth:  1200,
	ImageViewerMaxHeight: 800,
	ImageViewerMinWidth:  400,
	ImageViewerMinHeight: 300,
}

// selectionTint is the accent used for text selection, with alpha so the
// glyphs underneath stay legible.
var selectionTint = color.RGBA{R: 91, G: 124, B: 250, A: 90}

// flatShadow is a softened shadow so scroll edges read as a subtle seam rather
// than a heavy drop shadow — keeps the flat/metro feel.
var flatShadow = color.RGBA{R: 0, G: 0, B: 0, A: 40}

// AppTheme applies the Cool Slate palette to Fyne's built-in widgets (entries,
// buttons, dialogs, the login form) so they match our custom widgets, and hides
// scrollbars for a cleaner look.
type AppTheme struct {
	fyne.Theme
}

// NewAppTheme wraps a base theme with the application palette and overrides.
func NewAppTheme(base fyne.Theme) *AppTheme {
	return &AppTheme{Theme: base}
}

// Color maps Fyne's semantic color names onto the Cool Slate palette so
// built-in widgets inherit the app's look.
func (t *AppTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameScrollBar:
		return color.Transparent
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return Colors.ServerSelectedBg // #5B7CFA accent
	case theme.ColorNameBackground:
		return Colors.MessageAreaBackground
	case theme.ColorNameInputBackground:
		return Colors.ChannelListBackground
	case theme.ColorNameInputBorder:
		return Colors.ChannelSelectedBg
	case theme.ColorNameForeground:
		return Colors.TextPrimary
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return Colors.TimestampText
	case theme.ColorNameButton:
		return Colors.SwiftActionBg
	case theme.ColorNameHover:
		return Colors.TappableHoverBg
	case theme.ColorNamePressed:
		return Colors.SwiftActionHoverBg
	case theme.ColorNameSelection:
		return selectionTint
	case theme.ColorNameSeparator:
		return Colors.ChannelSelectedBg
	case theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return Colors.ChannelListBackground
	case theme.ColorNameShadow:
		return flatShadow
	}
	return t.Theme.Color(name, variant)
}

// Size hides scrollbars and flattens input corners for a metro look.
func (t *AppTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar:
		return 0
	case theme.SizeNameInputRadius:
		return 0
	}
	return t.Theme.Size(name)
}
