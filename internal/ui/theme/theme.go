package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"RGOClient/assets"
)

// Colors defines the color palette for the application.
// Centralizing colors makes it easy to maintain consistency and support theming.
var Colors = struct {
	// Backgrounds
	ServerListBackground   color.Color
	ChannelListBackground  color.Color
	MemberListBackground   color.Color
	MessageAreaBackground  color.Color
	MessageHoverBackground color.Color
	ChannelHoverBackground color.Color
	ChannelSelectedBg      color.Color
	ServerDefaultBg        color.Color
	ServerHoverBg          color.Color
	ServerSelectedBg       color.Color
	ServerListSeparator    color.Color
	TappableHoverBg        color.Color
	OverlayBackdrop        color.Color
	ViewerCardBg           color.Color
	ViewerBodyBg           color.Color

	// Elements
	AttachmentHoverBorder color.Color
	AvatarPlaceholder     color.Color
	HashtagIcon           color.Color
	CategoryText          color.Color
	CategoryArrow         color.Color
	CategoryIndicator     color.Color
	TextPrimary           color.Color
	TimestampText         color.Color
	DaySeparatorText      color.Color
	DaySeparatorLine      color.Color
	XButtonNormal         color.Color
	XButtonHover          color.Color
	SessionCardBg         color.Color
	UnreadIndicator       color.Color
	SwiftActionBg         color.Color
	SwiftActionHoverBg    color.Color
	SwiftActionText       color.Color
	ReplyMentionActive    color.Color
	ErrorText             color.Color
}{
	// Backgrounds — cool blue-slate ramp (darkest → lightest)
	ServerListBackground:   color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C
	ChannelListBackground:  color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330
	MemberListBackground:   color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330
	MessageAreaBackground:  color.RGBA{R: 24, G: 27, B: 36, A: 255},    // #181B24
	MessageHoverBackground: color.RGBA{R: 30, G: 34, B: 45, A: 255},    // #1E222D
	ChannelHoverBackground: color.RGBA{R: 38, G: 43, B: 58, A: 255},    // #262B3A
	ChannelSelectedBg:      color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142
	ServerDefaultBg:        color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142
	ServerHoverBg:          color.RGBA{R: 53, G: 60, B: 80, A: 255},    // #353C50
	ServerSelectedBg:       color.RGBA{R: 91, G: 124, B: 250, A: 255},  // #5B7CFA accent
	ServerListSeparator:    color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142, subtle bar
	TappableHoverBg:        color.RGBA{R: 38, G: 43, B: 58, A: 255},    // #262B3A
	SwiftActionBg:          color.RGBA{R: 35, G: 40, B: 56, A: 255},    // #232838
	SwiftActionHoverBg:     color.RGBA{R: 46, G: 53, B: 72, A: 255},    // #2E3548
	SwiftActionText:        color.RGBA{R: 196, G: 201, B: 212, A: 255}, // #C4C9D4
	OverlayBackdrop:        color.RGBA{R: 8, G: 9, B: 12, A: 200},      // near-black dim behind a modal
	ViewerCardBg:           color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330, the modal card itself
	ViewerBodyBg:           color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C, inset well the content sits in

	// Elements
	AttachmentHoverBorder: color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C, darkest ramp
	AvatarPlaceholder:     color.RGBA{R: 60, G: 72, B: 110, A: 255},   // muted blue
	UnreadIndicator:       color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	HashtagIcon:           color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	CategoryText:          color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryArrow:         color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryIndicator:     color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	TextPrimary:           color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	TimestampText:         color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	DaySeparatorText:      color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3, brighter than a timestamp
	DaySeparatorLine:      color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142, hairline on the message bg
	ReplyMentionActive:    color.RGBA{R: 91, G: 124, B: 250, A: 70},   // accent tint, on the card bg
	XButtonNormal:         color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	XButtonHover:          color.RGBA{R: 248, G: 113, B: 113, A: 255}, // #F87171
	SessionCardBg:         color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330
	ErrorText:             color.RGBA{R: 248, G: 113, B: 113, A: 255}, // #F87171, failed actions
}

// Sizes defines standard sizes used throughout the application.
var Sizes = struct {
	// Sidebar
	ServerSidebarWidth    float32
	ChannelSidebarWidth   float32
	MemberSidebarWidth    float32
	ChannelSidebarPadding float32
	ChannelLeftPadding    float32
	UnreadIndicatorWidth  float32
	ChannelLabelSize      float32

	// Member list
	MemberAvatarSize float32
	MemberRowHeight  float32
	MemberNameSize   float32

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
	MessageAvatarSize             float32
	MessageAvatarColumnWidth      float32
	MessageContentPadding         float32
	MessageImageMaxWidth          float32
	MessageImageMaxHeight         float32
	MessageVerticalPadding        float32
	MessageGroupedVerticalPadding float32
	MessageHorizontalPadding      float32
	MessageAttachmentSpacing      float32
	MessageTimestampSize          float32
	MessageTimestampTopOffset     float32
	DaySeparatorTextSize          float32
	DaySeparatorThickness         float32
	DaySeparatorTopPadding        float32
	DaySeparatorBottomPadding     float32
	DaySeparatorGap               float32

	// Swift Actions
	SwiftActionSize float32

	// Session/Login
	SessionCardAvatarSize float32

	// Window
	WindowDefaultWidth  float32
	WindowDefaultHeight float32

	// Attachment viewer (the modal lightbox)
	ViewerMaxWidth     float32
	ViewerMaxHeight    float32
	ViewerMinWidth     float32
	ViewerMinHeight    float32
	ViewerMargin       float32
	ViewerHeaderHeight float32
	ViewerPadding      float32
	ViewerCornerRadius float32
	ViewerTitleSize    float32

	// Join-server dialog (the invite modal)
	JoinDialogWidth        float32
	JoinDialogCornerRadius float32
	JoinDialogTextSize     float32
}{
	// Sidebar
	ServerSidebarWidth:    60,
	ChannelSidebarWidth:   240,
	MemberSidebarWidth:    200,
	ChannelSidebarPadding: 6,
	ChannelLeftPadding:    8,
	UnreadIndicatorWidth:  1,
	ChannelLabelSize:      14,

	// Member list
	MemberAvatarSize: 28,
	MemberRowHeight:  36,
	MemberNameSize:   13,

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
	MessageAvatarSize:             40,
	MessageAvatarColumnWidth:      46,
	MessageContentPadding:         4,
	MessageImageMaxWidth:          400,
	MessageImageMaxHeight:         300,
	MessageVerticalPadding:        10,
	MessageGroupedVerticalPadding: 2,
	MessageHorizontalPadding:      12,
	MessageAttachmentSpacing:      4,
	MessageTimestampSize:          12,
	MessageTimestampTopOffset:     4,
	DaySeparatorTextSize:          11,
	DaySeparatorThickness:         1,
	DaySeparatorTopPadding:        14,
	DaySeparatorBottomPadding:     2,
	DaySeparatorGap:               8,

	// Swift Actions
	SwiftActionSize: 32,

	// Session/Login
	SessionCardAvatarSize: 32,

	// Window
	WindowDefaultWidth:  1000,
	WindowDefaultHeight: 600,

	// Attachment viewer (the modal lightbox)
	ViewerMaxWidth:     1200,
	ViewerMaxHeight:    800,
	ViewerMinWidth:     360,
	ViewerMinHeight:    240,
	ViewerMargin:       48,
	ViewerHeaderHeight: 38,
	ViewerPadding:      10,
	ViewerCornerRadius: 6,
	ViewerTitleSize:    13,

	// Join-server dialog (the invite modal)
	JoinDialogWidth:        320,
	JoinDialogCornerRadius: 6,
	JoinDialogTextSize:     12,
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

// Font returns the Montserrat family for all text, leaving monospace (code
// blocks, inline code) on Fyne's default so it stays fixed-width.
func (t *AppTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Monospace || style.Symbol {
		return t.Theme.Font(style)
	}
	switch {
	case style.Bold && style.Italic:
		return assets.MontserratBoldItalic
	case style.Bold:
		return assets.MontserratBold
	case style.Italic:
		return assets.MontserratItalic
	default:
		return assets.MontserratRegular
	}
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

// Size hides scrollbars and flattens inputs for a metro look: no corner radius
// and no border stroke, so the entry reads as a flat filled bar rather than an
// outlined box (the outline — accent-blue when focused — is what makes the
// default entry look bordered/textured). The zero input border also collapses
// the entry caret, which Fyne draws InputBorder wide — ui.WithCaret restores
// it per entry without bringing the outline back.
func (t *AppTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar:
		return 0
	case theme.SizeNameInputRadius:
		return 0
	case theme.SizeNameInputBorder:
		return 0
	}
	return t.Theme.Size(name)
}
