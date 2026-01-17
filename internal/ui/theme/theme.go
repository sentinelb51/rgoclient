package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Colors defines the color palette for the application.
// Centralizing colors makes it easy to maintain consistency and support theming.
var Colors = struct {
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
	MessageUsernameColor   color.Color
	MessageContentColor    color.Color
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

// Sizes defines standard sizes used throughout the application.
var Sizes = struct {
	// Sidebar sizes
	ServerSidebarWidth    float32
	ChannelSidebarWidth   float32
	ChannelSidebarPadding float32
	ChannelLeftPadding    float32

	// Server/Channel widget sizes
	ServerIconSize          float32
	ServerItemHeight        float32
	HashtagIconSize         float32
	CategoryHeight          float32
	CategorySpacing         float32
	CategoryIndicatorSize   float32
	CategoryIndicatorStroke float32

	// Message area sizes
	MessageAvatarSize             float32
	MessageAvatarColumnWidth      float32
	MessageAvatarTopPadding       float32
	MessageContentPadding         float32
	MessageImageMaxWidth          float32
	MessageImageMaxHeight         float32
	MessageVerticalPadding        float32
	MessageHorizontalPadding      float32
	MessageAttachmentSpacing      float32
	MessageTextLeftPadding        float32 // Left padding for text/attachments to align with label internal padding
	MessageUsernameContentSpacing float32
	MessageUsernameTopPadding     float32

	// Session/Login sizes
	SessionCardAvatarSize float32
	AvatarSize            float32 // General avatar size (used elsewhere)

	// Window sizes
	WindowDefaultWidth  float32
	WindowDefaultHeight float32
}{
	// Sidebar sizes
	ServerSidebarWidth:    60,
	ChannelSidebarWidth:   240,
	ChannelSidebarPadding: 6,
	ChannelLeftPadding:    8,

	// Server/Channel widget sizes
	ServerIconSize:          40,
	ServerItemHeight:        50,
	HashtagIconSize:         20,
	CategoryHeight:          32,
	CategorySpacing:         10,
	CategoryIndicatorSize:   14,
	CategoryIndicatorStroke: 2,

	// Message area sizes
	MessageAvatarSize:             40,
	MessageAvatarColumnWidth:      48, // Avatar size + minimal padding for the fixed left column
	MessageAvatarTopPadding:       4,  // Top padding to align avatar with first line of text
	MessageContentPadding:         0,  // Padding between avatar column and message content
	MessageImageMaxWidth:          400,
	MessageImageMaxHeight:         300,
	MessageVerticalPadding:        4,  // Vertical padding inside message widget
	MessageHorizontalPadding:      8,  // Horizontal padding inside message widget
	MessageAttachmentSpacing:      4,  // Spacing between attachments
	MessageTextLeftPadding:        4,  // Left padding for username/attachments to align with label
	MessageUsernameContentSpacing: -8, // Negative to overlap label's internal top padding
	MessageUsernameTopPadding:     2,  // Small top padding for username

	// Session/Login sizes
	SessionCardAvatarSize: 32,
	AvatarSize:            40,

	// Window sizes
	WindowDefaultWidth:  1000,
	WindowDefaultHeight: 600,
}

// Fonts defines font styles used throughout the application.
var Fonts = struct {
	// Message fonts
	MessageUsernameBold bool
	MessageUsernameSize float32
	MessageContentSize  float32
}{
	// Message fonts
	MessageUsernameBold: true,
	MessageUsernameSize: 14,
	MessageContentSize:  14,
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
