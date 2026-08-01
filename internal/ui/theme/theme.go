// Package theme holds the client's Cool Slate palette, its size table, and the
// Fyne theme that maps both onto the built-in widgets. Colours and sizes are
// never hardcoded at a call site — they are named here.
package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"RGOClient/assets"
)

// Colors is the application palette.
var Colors = struct {
	/* Backgrounds */

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
	SwiftActionBg          color.Color
	SwiftActionHoverBg     color.Color
	SessionCardBg          color.Color
	OverlayBackdrop        color.Color
	ViewerCardBg           color.Color
	ViewerBodyBg           color.Color
	ComposerBg             color.Color
	ComposerBorder         color.Color
	ComposerBorderFocus    color.Color
	MentionRowSelectedBg   color.Color

	/* Elements */

	AttachmentHoverBorder color.Color
	AvatarPlaceholder     color.Color
	UnreadIndicator       color.Color
	HashtagIcon           color.Color
	CategoryText          color.Color
	CategoryIndicator     color.Color
	TextPrimary           color.Color
	TimestampText         color.Color
	DaySeparatorText      color.Color
	DaySeparatorLine      color.Color
	ReplyMentionActive    color.Color
	MentionText           color.Color
	MentionHandleText     color.Color
	ErrorText             color.Color
}{
	// Cool blue-slate ramp, darkest to lightest.
	ServerListBackground:   color.RGBA{R: 19, G: 21, B: 28, A: 255},   // #13151C
	ChannelListBackground:  color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330
	MemberListBackground:   color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330
	MessageAreaBackground:  color.RGBA{R: 24, G: 27, B: 36, A: 255},   // #181B24
	MessageHoverBackground: color.RGBA{R: 30, G: 34, B: 45, A: 255},   // #1E222D
	ChannelHoverBackground: color.RGBA{R: 38, G: 43, B: 58, A: 255},   // #262B3A
	ChannelSelectedBg:      color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142
	ServerDefaultBg:        color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142
	ServerHoverBg:          color.RGBA{R: 53, G: 60, B: 80, A: 255},   // #353C50
	ServerSelectedBg:       color.RGBA{R: 91, G: 124, B: 250, A: 255}, // #5B7CFA accent
	ServerListSeparator:    color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142
	TappableHoverBg:        color.RGBA{R: 38, G: 43, B: 58, A: 255},   // #262B3A
	SwiftActionBg:          color.RGBA{R: 35, G: 40, B: 56, A: 255},   // #232838
	SwiftActionHoverBg:     color.RGBA{R: 46, G: 53, B: 72, A: 255},   // #2E3548
	SessionCardBg:          color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330
	OverlayBackdrop:        color.RGBA{R: 8, G: 9, B: 12, A: 200},     // dim behind a modal
	ViewerCardBg:           color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330, the modal card
	ViewerBodyBg:           color.RGBA{R: 19, G: 21, B: 28, A: 255},   // #13151C, inset well

	// The composer card fills with the entry's own input background so the entry's
	// box disappears into it; the outline draws the boundary instead, and lights up
	// with the accent while the entry holds focus.
	ComposerBg:           color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330, == ColorNameInputBackground
	ComposerBorder:       color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142, idle hairline
	ComposerBorderFocus:  color.RGBA{R: 91, G: 124, B: 250, A: 255}, // #5B7CFA accent
	MentionRowSelectedBg: color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142, the picker's active row

	AttachmentHoverBorder: color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C
	AvatarPlaceholder:     color.RGBA{R: 60, G: 72, B: 110, A: 255},   // muted blue
	UnreadIndicator:       color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	HashtagIcon:           color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	CategoryText:          color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryIndicator:     color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	TextPrimary:           color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	TimestampText:         color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	DaySeparatorText:      color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	DaySeparatorLine:      color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142 hairline
	ReplyMentionActive:    color.RGBA{R: 91, G: 124, B: 250, A: 70},   // accent tint
	MentionText:           color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF, accent lifted for body text
	MentionHandleText:     color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280, the picker's @handle
	ErrorText:             color.RGBA{R: 248, G: 113, B: 113, A: 255}, // #F87171
}

// Sizes is the application's size table. Never express one size as an offset
// from an unrelated one; add a named entry instead.
var Sizes = struct {
	/* Sidebars */

	ServerSidebarWidth    float32
	ChannelSidebarWidth   float32
	MemberSidebarWidth    float32
	ChannelSidebarPadding float32
	ChannelLeftPadding    float32
	UnreadIndicatorWidth  float32
	ChannelLabelSize      float32

	/* Member list */

	MemberAvatarSize float32
	MemberRowHeight  float32
	MemberNameSize   float32

	/* Server and channel rows */

	ServerIconSize          float32
	ServerItemHeight        float32
	HashtagIconSize         float32
	CategoryHeight          float32
	ChannelItemHeight       float32
	CategorySpacing         float32
	CategoryIndicatorSize   float32
	CategoryIndicatorStroke float32

	/* Message area */

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
	SwiftActionSize               float32

	/* Composer and its mention picker */

	ComposerRadius      float32
	ComposerPaddingV    float32
	ComposerPaddingH    float32
	ComposerGutterWidth float32
	ComposerButtonSize  float32
	ComposerIconSize    float32
	MentionRowHeight    float32
	MentionAvatarSize   float32
	MentionNameSize     float32
	MentionHandleSize   float32

	/* Login */

	SessionCardAvatarSize float32
	WindowDefaultWidth    float32
	WindowDefaultHeight   float32

	/* Attachment viewer */

	ViewerMaxWidth     float32
	ViewerMaxHeight    float32
	ViewerMinWidth     float32
	ViewerMinHeight    float32
	ViewerMargin       float32
	ViewerHeaderHeight float32
	ViewerPadding      float32
	ViewerCornerRadius float32
	ViewerTitleSize    float32

	/* Join-server dialog */

	JoinDialogWidth        float32
	JoinDialogCornerRadius float32
	JoinDialogTextSize     float32
}{
	ServerSidebarWidth:    60,
	ChannelSidebarWidth:   240,
	MemberSidebarWidth:    200,
	ChannelSidebarPadding: 6,
	ChannelLeftPadding:    8,
	UnreadIndicatorWidth:  1,
	ChannelLabelSize:      14,

	MemberAvatarSize: 28,
	MemberRowHeight:  36,
	MemberNameSize:   13,

	ServerIconSize:          40,
	ServerItemHeight:        50,
	HashtagIconSize:         20,
	CategoryHeight:          32,
	ChannelItemHeight:       32,
	CategorySpacing:         10,
	CategoryIndicatorSize:   14,
	CategoryIndicatorStroke: 2,

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
	SwiftActionSize:               32,

	// The composer's vertical padding is deliberately small: the entry already
	// carries InnerPadding above and below its text, so the card only needs a
	// couple of pixels more before it starts looking slack.
	ComposerRadius:      8,
	ComposerPaddingV:    3,
	ComposerPaddingH:    6,
	ComposerGutterWidth: 30,
	ComposerButtonSize:  24,
	ComposerIconSize:    18,
	MentionRowHeight:    30,
	MentionAvatarSize:   20,
	MentionNameSize:     13,
	MentionHandleSize:   11,

	SessionCardAvatarSize: 32,
	WindowDefaultWidth:    1000,
	WindowDefaultHeight:   600,

	ViewerMaxWidth:     1200,
	ViewerMaxHeight:    800,
	ViewerMinWidth:     360,
	ViewerMinHeight:    240,
	ViewerMargin:       48,
	ViewerHeaderHeight: 38,
	ViewerPadding:      10,
	ViewerCornerRadius: 6,
	ViewerTitleSize:    13,

	JoinDialogWidth:        320,
	JoinDialogCornerRadius: 6,
	JoinDialogTextSize:     12,
}

// ColorNameMention is an app-specific theme colour name. A RichText segment can
// only carry a *named* colour, so drawing an @mention in the accent needs a name
// of our own for AppTheme.Color to answer.
const ColorNameMention fyne.ThemeColorName = "rgoMention"

// selectionTint is the accent used for text selection, alpha'd so the glyphs
// underneath stay legible.
var selectionTint = color.RGBA{R: 91, G: 124, B: 250, A: 90}

// flatShadow softens scroll-edge shadows into a seam, keeping the flat look.
var flatShadow = color.RGBA{A: 40}

// AppTheme applies the palette to Fyne's built-in widgets so they match the
// client's own, and hides scrollbars.
type AppTheme struct {
	fyne.Theme
}

// NewAppTheme wraps a base theme with the application palette and overrides.
func NewAppTheme(base fyne.Theme) *AppTheme {
	return &AppTheme{Theme: base}
}

// Font returns Montserrat for all text, leaving monospace on Fyne's default so
// code stays fixed-width.
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

// Color maps Fyne's semantic colour names onto the palette.
func (t *AppTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case ColorNameMention:
		return Colors.MentionText
	case theme.ColorNameScrollBar:
		return color.Transparent
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return Colors.ServerSelectedBg
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

// Size hides scrollbars and flattens inputs: no corner radius and no border
// stroke, so an entry reads as a flat filled bar rather than an outlined box.
// The zero border also collapses the caret, which Fyne draws InputBorder wide —
// ui.WithCaret restores it per entry without bringing the outline back.
func (t *AppTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar, theme.SizeNameInputRadius, theme.SizeNameInputBorder:
		return 0
	}

	return t.Theme.Size(name)
}
