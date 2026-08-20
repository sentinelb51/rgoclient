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

	MessageMentionBackground      color.Color
	MessageMentionHoverBackground color.Color

	MessageJumpBackground color.Color

	MessageEditBackground color.Color

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
	TooltipBg              color.Color
	NoticeBg               color.Color
	OverlayBackdrop        color.Color
	ViewerCardBg           color.Color
	ViewerBodyBg           color.Color
	ComposerBg             color.Color
	ComposerBorderFocus    color.Color
	MentionRowSelectedBg   color.Color

	/* Edges */

	Outline    color.Color
	CardShadow color.Color

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
	SystemMessageText     color.Color
	SystemMessageIcon     color.Color
	SystemMessageJoin     color.Color
	SystemMessageLeave    color.Color
	SystemMessageDanger   color.Color
	SystemMessageChange   color.Color
	SystemMessageCall     color.Color
	SwiftActionIcon       color.Color
	SwiftActionConfirm    color.Color
	SwiftActionDanger     color.Color
	ReplyLine             color.Color
	ReplyMentionActive    color.Color
	MentionText           color.Color
	MentionHandleText     color.Color
	SlowmodeText          color.Color
	DockBadgeBg           color.Color
	SlowmodeWaiting       color.Color
	TypingText            color.Color
	TypingMark            color.Color
	JumpBarBg             color.Color
	JumpBarHoverBg        color.Color
	JumpBarAction         color.Color
	MessageStatusMark     color.Color
	ChannelNoteText       color.Color
	ChannelTopicText      color.Color
	ErrorText             color.Color

	/* Reactions */

	ReactionBg          color.Color
	ReactionHoverBg     color.Color
	ReactionMineBg      color.Color
	ReactionMineHoverBg color.Color
	ReactionCount       color.Color
	ReactionMine        color.Color

	/* Scrolling */

	ScrollIndicator color.Color

	/* Message embeds */

	EmbedBg     color.Color
	EmbedAccent color.Color
	EmbedTitle  color.Color
	EmbedSite   color.Color

	/* Code blocks */

	// The well a fenced block is drawn in, and the highlighter's palette. Each is
	// reachable as a Fyne colour *name* too (ColorNameCode…), RichText colouring a
	// segment by name rather than by value.
	CodeBlockBg color.Color
	CodeText    color.Color
	CodeKeyword color.Color
	CodeString  color.Color
	CodeComment color.Color
	CodeNumber  color.Color
	CodeCall    color.Color

	/* Member list */

	MemberNameOffline color.Color
	MemberSectionText color.Color
	MemberStatusText  color.Color

	/* Bot mark */

	// BotMark is the glyph following the name of an account Revolt marks as a
	// bot, on a member row and on a profile alike.
	BotMark color.Color

	/* Invite cards */

	InviteCaption color.Color
	InviteDetail  color.Color

	/* User profiles */

	ProfileBannerBg color.Color

	/* Buttons */

	// A plain button is the outlined surface: a small lift off whatever it is laid
	// on, carrying the client's one hairline. A weighted one drops the hairline and
	// fills with its tone, so which button a card is *for* is read off the fill
	// rather than off the words.
	ButtonBg           color.Color
	ButtonHoverBg      color.Color
	ButtonText         color.Color
	ButtonFilledText   color.Color
	ButtonDisabledBg   color.Color
	ButtonDisabledText color.Color

	/* Chips */

	ChipBg      color.Color
	ChipHoverBg color.Color

	/* Presence */

	PresenceOnline  color.Color
	PresenceIdle    color.Color
	PresenceFocus   color.Color
	PresenceBusy    color.Color
	PresenceOffline color.Color

	/* Notice tones */

	NoticeInfo    color.Color
	NoticeWarning color.Color
	NoticeDanger  color.Color

	/* Confirmations */

	// ConfirmHint is the line under the buttons naming the key that skips the
	// question. It sits below the body it follows, being an aside rather than part
	// of what is being asked.
	ConfirmHint color.Color
}{
	// Cool blue-slate ramp, darkest to lightest.
	ServerListBackground:   color.RGBA{R: 19, G: 21, B: 28, A: 255}, // #13151C
	ChannelListBackground:  color.RGBA{R: 31, G: 35, B: 48, A: 255}, // #1F2330
	MemberListBackground:   color.RGBA{R: 31, G: 35, B: 48, A: 255}, // #1F2330
	MessageAreaBackground:  color.RGBA{R: 24, G: 27, B: 36, A: 255}, // #181B24
	MessageHoverBackground: color.RGBA{R: 28, G: 31, B: 42, A: 255}, // #1C1F2A, a small lift off the area

	// A message that names the account is washed warm, the one place the client
	// leaves the cool ramp. It is a *rest* colour, not a state: the row still has
	// to lift under the pointer, so the pair moves together the way the area and
	// its hover do — and both stay dark enough that the body text over them, and
	// the hairline of any card inside them, read exactly as they do elsewhere.
	MessageMentionBackground:      color.RGBA{R: 53, G: 38, B: 15, A: 255}, // #35260F
	MessageMentionHoverBackground: color.RGBA{R: 68, G: 48, B: 20, A: 255}, // #443014

	// What a jump washes the row it landed on with, on the way back to whatever
	// that row's rest colour is. It leans on the accent rather than the warm ramp:
	// the wash says "here", where the mention wash says "you", and a message can
	// be both at once.
	MessageJumpBackground: color.RGBA{R: 45, G: 55, B: 92, A: 255}, // #2D375C

	// What an edit washes the row with as it lands, in and back out again. Cooler
	// and fainter than the jump wash: it announces a change the reader did not ask
	// for, where a jump answers one they did.
	MessageEditBackground: color.RGBA{R: 34, G: 52, B: 66, A: 255}, // #223442

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
	TooltipBg:              color.RGBA{R: 8, G: 9, B: 12, A: 240},     // darker than any column it floats over
	NoticeBg:               color.RGBA{R: 43, G: 49, B: 66, A: 250},   // #2B3142, lifted off whatever it floats over
	OverlayBackdrop:        color.RGBA{R: 8, G: 9, B: 12, A: 200},     // dim behind a modal
	ViewerCardBg:           color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330, the modal card
	ViewerBodyBg:           color.RGBA{R: 19, G: 21, B: 28, A: 255},   // #13151C, inset well

	// The composer card fills with the entry's own input background so the entry's
	// box disappears into it; the outline draws the boundary instead, and lights up
	// with the accent while the entry holds focus.
	ComposerBg:           color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330, == ColorNameInputBackground
	ComposerBorderFocus:  color.RGBA{R: 91, G: 124, B: 250, A: 255}, // #5B7CFA accent
	MentionRowSelectedBg: color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142, the picker's active row

	// One hairline draws every edge in the client: a card lifted off a surface
	// (an embed, an attachment, the composer dock) and the seam between two
	// columns of the main row. It is darker than anything it is ever drawn
	// against — including the message row's hover fill, which paints *under* any
	// card the row contains — so no state of what is behind it can wash it out.
	// That ordering is the invariant, not the literal value.
	Outline: color.RGBA{R: 15, G: 17, B: 23, A: 255}, // #0F1117

	// The cast shadow under the one card that floats. Black rather than a palette
	// entry: it multiplies whatever is behind it instead of naming a surface, so
	// it stays correct over the message area, a hovered row and a message alike.
	// Weak on purpose — it only has to say the card is nearer than the message
	// sliding under it. Enough to read as a bar's own fill and it becomes one.
	CardShadow: color.RGBA{A: 90},

	// An attachment is outlined at rest, so hovering one lightens its edge rather
	// than drawing it: a hover border a shade off the outline it replaces would
	// read as nothing happening at all.
	AttachmentHoverBorder: color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142
	AvatarPlaceholder:     color.RGBA{R: 60, G: 72, B: 110, A: 255},   // muted blue
	UnreadIndicator:       color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	HashtagIcon:           color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	CategoryText:          color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryIndicator:     color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	TextPrimary:           color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	TimestampText:         color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	DaySeparatorText:      color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	DaySeparatorLine:      color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142 hairline

	// A system line is the channel narrating itself, not someone speaking, so it
	// is set in the same grey the day separator names its date in — legible, and
	// plainly not a message. Its mark is the exception: two pixels of stroke
	// against a body of text, where colour is what lets a run of them be read as
	// arrivals and departures before a word of it is. The tone says what class of
	// event happened and the glyph says which, so the grey is what a kind carrying
	// no tone of its own falls back to — as an unknown kind does its generic mark.
	SystemMessageText:   color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	SystemMessageIcon:   color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	SystemMessageJoin:   color.RGBA{R: 58, G: 191, B: 126, A: 255},  // #3ABF7E, someone arrived
	SystemMessageLeave:  color.RGBA{R: 229, G: 166, B: 75, A: 255},  // #E5A64B, someone went
	SystemMessageDanger: color.RGBA{R: 217, G: 92, B: 92, A: 255},   // #D95C5C, someone was made to go
	SystemMessageChange: color.RGBA{R: 71, G: 153, B: 240, A: 255},  // #4799F0, the channel itself changed
	SystemMessageCall:   color.RGBA{R: 155, G: 138, B: 240, A: 255}, // #9B8AF0, a call

	// A message's own marks. They rest translucent and light under the pointer, so
	// these are the lit colours. The ones that only move you somewhere are neutral;
	// the two that commit something are coloured for what they commit, delete being
	// the one button in the row that cannot be taken back.
	SwiftActionIcon:    color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	SwiftActionConfirm: color.RGBA{R: 58, G: 191, B: 126, A: 255},  // #3ABF7E, save an edit
	SwiftActionDanger:  color.RGBA{R: 217, G: 92, B: 92, A: 255},   // #D95C5C, delete

	ReplyLine:          color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274, reads over the hover fill too
	ReplyMentionActive: color.RGBA{R: 91, G: 124, B: 250, A: 70},   // accent tint
	MentionText:        color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF, accent lifted for body text
	MentionHandleText:  color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280, the picker's @handle

	// The composer's slowmode badge. At rest it states a rule and reads as
	// furniture; once it is counting down it is the reason Enter did nothing, so
	// it warms to the amber the palette already uses for a holding state.
	SlowmodeText:    color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	SlowmodeWaiting: color.RGBA{R: 229, G: 166, B: 75, A: 255},  // #E5A64B

	// Who is typing is the quietest thing in the client: it is true for a few
	// seconds and then it is not, and nothing is lost by missing it. So the line
	// takes the grey a slowmode rule does, lifted one step now that it has a
	// ground of its own to be read against rather than whatever message is
	// passing under it.
	TypingText: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	TypingMark: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3

	// The pill both the typing line and the slowmode chip wear. It is the
	// composer's own fill rather than a lighter surface, so the two read as part
	// of the dock below rather than as two more cards over the conversation.
	DockBadgeBg: color.RGBA{R: 31, G: 35, B: 48, A: 255}, // #1F2330, == ComposerBg

	// The bar over the composer while the column is not showing the live tail. Its
	// ground is a lifted surface rather than a tint of the accent: the accent is
	// one knob the Interface section turns, and a bar whose fill did not follow it
	// would be the one indigo left on screen. What carries the colour is the
	// action at its trailing end, which is text and lifts with the rest of them.
	JumpBarBg:      color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142
	JumpBarHoverBg: color.RGBA{R: 53, G: 60, B: 80, A: 255},    // #353C50
	JumpBarAction:  color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF, accent lifted for text

	// The mark leading the line an empty column draws. Quieter than the sentence
	// beside it: it says what the line is about, and a column holding one short
	// sentence should not be led by the loudest thing on screen.
	MessageStatusMark: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3

	// The strip under the header saying what the client cannot do in the channel
	// below it. Quiet in the same way: it is a standing caption rather than a
	// notice, so it must not compete with the messages it sits over.
	ChannelNoteText: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3

	// The topic beside the channel's name. Quieter than the name and quieter than
	// the note above: it is the one line of somebody else's prose in the chrome,
	// and it shares its row with the buttons the header is there for.
	ChannelTopicText: color.RGBA{R: 122, G: 130, B: 148, A: 255}, // #7A8294

	ErrorText: color.RGBA{R: 248, G: 113, B: 113, A: 255}, // #F87171

	// A reaction chip. The one this account is in has to be legible down a whole
	// column at a glance, so it takes the accent twice over — as a tint behind it
	// and again on the count — which is what keeps it apart from a chip that merely
	// happens to be under the pointer. Both states light on hover, since a chip is
	// a button whichever it is in.
	// The two tints are NRGBA because they are the accent at an alpha: color.RGBA
	// is alpha-premultiplied, so #5B7CFA written there with A=55 is not a colour at
	// all — every channel is over the alpha — and composites as something else
	// entirely.
	ReactionBg:          color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142, the chip surface
	ReactionHoverBg:     color.RGBA{R: 55, G: 62, B: 82, A: 255},    // #373E52
	ReactionMineBg:      color.NRGBA{R: 91, G: 124, B: 250, A: 70},  // accent tint
	ReactionMineHoverBg: color.NRGBA{R: 91, G: 124, B: 250, A: 110}, // the same, lifted
	ReactionCount:       color.RGBA{R: 168, G: 176, B: 192, A: 255}, // #A8B0C0
	ReactionMine:        color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF, the accent as body text

	// The position indicator drawn while a view moves. The same slate as the reply
	// elbow, which is the palette's answer for a mark that has to read over the
	// message area and over a hovered row alike. Opaque: it is only up for a
	// moment, its own fade is what softens it, and an alpha here would be read
	// premultiplied and come back lighter than it was written.
	ScrollIndicator: color.RGBA{R: 90, G: 98, B: 116, A: 255}, // #5A6274

	// An embed is a card lifted off the message area, not a panel: it carries the
	// same fill the other cards do, and the stripe down its side is what the eye
	// reads it by. The stripe falls back to a slate rather than the accent, which
	// on a channel of link previews would be a column of blue bars. Its edge is
	// the shared Outline above.
	EmbedBg:     color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330
	EmbedAccent: color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	EmbedTitle:  color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF, the mention accent — a title is a link
	EmbedSite:   color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3

	// A code block is a well, not a card: darker than the message area rather than
	// lifted off it, so it still reads as inset where a hovered row lightens
	// everything around it. The token colours sit on that fill, warm ones against
	// the cool ramp so a string is told from an identifier at a glance.
	CodeBlockBg: color.RGBA{R: 18, G: 20, B: 27, A: 255},    // #12141B
	CodeText:    color.RGBA{R: 197, G: 203, B: 218, A: 255}, // #C5CBDA
	CodeKeyword: color.RGBA{R: 167, G: 139, B: 250, A: 255}, // #A78BFA
	CodeString:  color.RGBA{R: 224, G: 164, B: 114, A: 255}, // #E0A472
	CodeComment: color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CodeNumber:  color.RGBA{R: 229, G: 192, B: 123, A: 255}, // #E5C07B
	CodeCall:    color.RGBA{R: 122, G: 162, B: 247, A: 255}, // #7AA2F7

	// An offline name is dimmed rather than hidden, which is what lets one list
	// hold both halves without reading as two. It has a name of its own rather
	// than borrowing CategoryText: that is a *header's* colour, and two things
	// wanting different tunings must not share one entry — which is what they did
	// before the sections were rows in their own right.
	MemberNameOffline: color.RGBA{R: 122, G: 130, B: 147, A: 255}, // #7A8293
	MemberSectionText: color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280

	// What the sidebar says when it has no rows to say it with. Pitched at the
	// section headers rather than at a name: it is furniture, not a member.
	MemberStatusText: color.RGBA{R: 122, G: 130, B: 147, A: 255}, // #7A8293

	// The mark is a stroked glyph rather than a filled chip, so it takes the
	// accent as a line colour and needs no surface behind it.
	BotMark: color.RGBA{R: 122, G: 137, B: 205, A: 255}, // #7A89CD

	InviteCaption: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	InviteDetail:  color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3

	// The banner is the profile card's one block of colour, so it falls back to a
	// slate the palette already uses rather than the accent, which would make every
	// user without a coloured role look like the selected server.
	ProfileBannerBg: color.RGBA{R: 43, G: 49, B: 66, A: 255}, // #2B3142

	// A plain button is a shade off the surfaces it is laid on — the modal card,
	// a settings group, the member sidebar — so the hairline is what draws it and
	// the fill only has to keep it from reading as text. Hover lifts it one step
	// further. Disabled sinks *below* the card instead: a button that cannot be
	// pressed must not look like one that can, and its edge is all it keeps.
	ButtonBg:           color.RGBA{R: 35, G: 40, B: 56, A: 255},    // #232838
	ButtonHoverBg:      color.RGBA{R: 46, G: 53, B: 72, A: 255},    // #2E3548
	ButtonText:         color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	ButtonFilledText:   color.RGBA{R: 247, G: 248, B: 251, A: 255}, // #F7F8FB, on a tone fill
	ButtonDisabledBg:   color.RGBA{R: 29, G: 33, B: 44, A: 255},    // #1D212C
	ButtonDisabledText: color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280

	// A chip is a surface in its own right, so it takes the same slate the banner
	// does and wears the shared hairline: its text is whatever colour the thing it
	// names carries, and a coloured word alone on the card read as loose text.
	ChipBg:      color.RGBA{R: 43, G: 49, B: 66, A: 255}, // #2B3142, lifted off the card
	ChipHoverBg: color.RGBA{R: 55, G: 62, B: 82, A: 255}, // one step further up, for a chip that leads somewhere

	// Presence reads as a ring around an avatar, so these are saturated enough to
	// carry a few pixels of stroke against the card behind them. Offline is never
	// drawn — the ring is simply absent — so its entry only names the vocabulary.
	PresenceOnline:  color.RGBA{R: 58, G: 191, B: 126, A: 255},  // #3ABF7E
	PresenceIdle:    color.RGBA{R: 229, G: 166, B: 75, A: 255},  // #E5A64B
	PresenceFocus:   color.RGBA{R: 71, G: 153, B: 240, A: 255},  // #4799F0
	PresenceBusy:    color.RGBA{R: 217, G: 92, B: 92, A: 255},   // #D95C5C
	PresenceOffline: color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280

	// A tone colours a notice's icon and edge, and fills the matching button of a
	// confirmation, so each has to stay legible under white button text — hence
	// deeper reds and ambers than the light-on-dark ErrorText above.
	NoticeInfo:    color.RGBA{R: 91, G: 124, B: 250, A: 255}, // #5B7CFA accent
	NoticeWarning: color.RGBA{R: 201, G: 138, B: 42, A: 255}, // #C98A2A
	NoticeDanger:  color.RGBA{R: 199, G: 62, B: 66, A: 255},  // #C73E42

	ConfirmHint: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
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
	SelectionMarkerWidth  float32
	ChannelLabelSize      float32

	/* Member list */

	MemberAvatarSize float32
	MemberRowHeight  float32
	MemberNameSize   float32

	// A section is its own row in the list, so its height is load-bearing: the
	// list places rows by a prefix sum over exactly these two numbers, and the top
	// padding lives *inside* the section height rather than beside it so there are
	// still only two.
	MemberSectionHeight   float32
	MemberSectionTopPad   float32
	MemberSectionTextSize float32

	MemberPresenceDotSize float32
	MemberPresenceDotRing float32

	// The strip drawn over the list while a membership is on its way or after it
	// failed to arrive. The mark is the client's own sweeping line, so its width
	// is set here the way the composer's and a channel row's are.
	MemberStatusTextSize float32
	MemberStatusMarkSize float32
	MemberStatusPadding  float32
	MemberStatusGap      float32

	/* Bot mark */

	// One glyph at two sizes: it follows a name, and the two names it follows are
	// set differently.
	MemberBotMarkSize  float32
	ProfileBotMarkSize float32

	/* Server and channel rows */

	ServerIconSize          float32
	ServerItemHeight        float32
	ServerMarkerHeight      float32
	HashtagIconSize         float32
	CategoryHeight          float32
	ChannelItemHeight       float32
	ConversationItemHeight  float32
	ConversationAvatarSize  float32
	CategorySpacing         float32
	CategoryIndicatorSize   float32
	CategoryIndicatorStroke float32

	/* Message area */

	// The column's *reported* minimum, which is all the window has to go on: it
	// stands in for what the messages and the composer hold, so neither can grow
	// the window. Below these the content is clipped rather than the window pushed.
	MessageAreaMinWidth  float32
	MessageAreaMinHeight float32

	MessageAvatarSize             float32
	MessageAvatarColumnWidth      float32
	MessageContentPadding         float32
	MessageImageMaxWidth          float32
	MessageImageMaxHeight         float32
	MessageVerticalPadding        float32
	MessageGroupedVerticalPadding float32
	MessageHorizontalPadding      float32
	MessageAttachmentSpacing      float32
	MessageReplyBlockGap          float32
	MessageReplyLineInset         float32
	MessageReplyLineThickness     float32
	MessageReplyLineGap           float32
	MessageTimestampSize          float32
	MessagePinMarkSize            float32
	MessageEditMarkSize           float32
	ReactionEmojiSize             float32
	ReactionCountSize             float32
	ReactionRadius                float32
	ReactionPaddingH              float32
	ReactionPaddingV              float32
	ReactionSpacing               float32
	EmojiPickerWidth              float32
	EmojiPickerMaxHeight          float32
	EmojiPickerCellSize           float32
	EmojiPickerEmojiSize          float32
	EmojiPickerCaptionSize        float32
	EmojiPickerGap                float32
	SystemMessageTextSize         float32
	SystemMessageIconSize         float32
	SystemMessagePadding          float32
	DaySeparatorTextSize          float32
	DaySeparatorThickness         float32
	DaySeparatorTopPadding        float32
	DaySeparatorBottomPadding     float32
	DaySeparatorGap               float32
	SwiftActionSize               float32

	// The line the column draws in place of messages, and the mark leading it.
	MessageStatusMarkSize float32
	MessageStatusGap      float32

	// The strip under the message header, and the mark leading it.
	ChannelNoteTextSize float32
	ChannelNoteMarkSize float32
	ChannelNoteGap      float32
	ChannelNotePaddingV float32
	ChannelNotePaddingH float32

	// The topic beside the channel's name, and the rule dividing the two.
	ChannelTopicSize       float32
	ChannelTopicGap        float32
	ChannelTopicRuleHeight float32

	/* Edges */

	OutlineWidth   float32
	CardShadowBlur float32

	/* Scrolling */

	ScrollIndicatorWidth     float32
	ScrollIndicatorInset     float32
	ScrollIndicatorMinHeight float32
	ScrollIndicatorGrabWidth float32

	/* Message embeds */

	EmbedMaxWidth       float32
	EmbedAccentWidth    float32
	EmbedRadius         float32
	EmbedPaddingV       float32
	EmbedPaddingH       float32
	EmbedAccentGap      float32
	EmbedRowGap         float32
	EmbedIconSize       float32
	EmbedIconGap        float32
	EmbedSiteTextSize   float32
	EmbedTitleTextSize  float32
	EmbedImageMaxHeight float32
	EmbedSpacing        float32

	/* Code blocks */

	CodeBlockRadius   float32
	CodeBlockPaddingV float32
	CodeBlockPaddingH float32
	CodeBlockSpacing  float32

	// The chip in a well's corner that copies the block, and how far in from that
	// corner it sits. It floats over the source rather than reserving a strip, so
	// a wide first line runs under it.
	CodeCopySize  float32
	CodeCopyInset float32

	InviteCardWidth   float32
	InviteIconSize    float32
	InviteCaptionSize float32
	InviteNameSize    float32
	InviteDetailSize  float32
	InviteTextGap     float32

	/* Composer and its mention picker */

	ComposerDockMargin  float32
	ComposerRadius      float32
	ComposerPaddingV    float32
	ComposerPaddingH    float32
	ComposerGutterWidth float32
	ComposerButtonSize  float32
	ComposerIconSize    float32
	ComposerMaxLines    float32 // lines the entry grows to before it scrolls
	ComposerNoticeMark  float32
	ComposerNoticeGap   float32
	MentionRowHeight    float32
	MentionAvatarSize   float32
	MentionNameSize     float32
	MentionHandleSize   float32
	SlowmodeGlyphSize   float32
	SlowmodeTextSize    float32
	SlowmodeGap         float32
	SlowmodeInsetH      float32
	SlowmodeDockGap     float32
	TypingMarkSize      float32
	TypingTextSize      float32
	TypingAvatarSize    float32
	TypingGap           float32
	TypingInsetH        float32
	ChannelTypingSize   float32
	DockBadgeRadius     float32
	DockBadgePaddingV   float32
	DockBadgePaddingH   float32
	JumpBarRadius       float32
	JumpBarPaddingV     float32
	JumpBarPaddingH     float32
	JumpBarTextSize     float32
	JumpBarDockGap      float32

	/* User profiles */

	ProfileCardWidth          float32
	ProfileDialogWidth        float32
	ProfileBannerHeight       float32
	ProfileDialogBannerHeight float32
	ProfileAvatarSize         float32
	ProfileDialogAvatarSize   float32
	ProfileAvatarRing         float32
	ProfilePresenceRing       float32
	ProfileNameSize           float32
	ProfileDialogNameSize     float32
	ProfileHandleSize         float32
	ProfileHandleRadius       float32
	ProfileHandlePaddingV     float32
	ProfileHandlePaddingH     float32
	ProfileStatusSize         float32
	ProfileDetailSize         float32
	ProfileDetailIconSize     float32
	ProfileBioMaxHeight       float32
	ProfileBioRadius          float32
	ProfileBioPadding         float32
	ProfileSectionSize        float32
	ProfilePadding            float32
	ProfileGap                float32
	ProfileTightGap           float32
	ProfileCornerRadius       float32

	/* Buttons */

	ButtonRadius    float32
	ButtonTextSize  float32
	ButtonPaddingV  float32
	ButtonPaddingH  float32
	ButtonMinWidth  float32
	ButtonMinHeight float32

	/* Chips */

	ChipTextSize float32
	ChipRadius   float32
	ChipPaddingV float32
	ChipPaddingH float32
	ChipSpacing  float32
	ChipDotSize  float32
	ChipDotGap   float32

	/* Anchored popovers */

	PopoverGap    float32
	PopoverMargin float32

	/* Hover tooltips */

	TooltipTextSize float32
	TooltipRadius   float32
	TooltipPaddingV float32
	TooltipPaddingH float32
	TooltipGap      float32

	/* Notices and confirmations */

	NoticeWidth       float32
	NoticeRadius      float32
	NoticeEdgeWidth   float32
	NoticeIconSize    float32
	NoticePaddingV    float32
	NoticePaddingH    float32
	NoticeStackMargin float32
	NoticeTitleSize   float32
	NoticeBodySize    float32
	NoticeTitleGap    float32
	NoticeCountdown   float32
	NoticeCardSpacing float32
	ConfirmWidth      float32
	ConfirmRadius     float32
	ConfirmButtonGap  float32
	ConfirmHintSize   float32

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

	/* Channel editor, and the fields any card holds */

	ChannelDialogWidth float32
	DialogPadding      float32
	DialogLabelSize    float32
	DialogLabelGap     float32
	DialogFieldGap     float32

	/* Friends dialog */

	FriendsDialogWidth   float32
	FriendsListMaxHeight float32
	FriendsRowHeight     float32
	FriendsAvatarSize    float32
	FriendsNameSize      float32
	FriendsHandleSize    float32
	FriendsSectionSize   float32
	FriendsPadding       float32
	FriendsGap           float32

	/* Message panels: the pins list and channel search */

	PanelDialogWidth   float32
	PanelListMaxHeight float32
	PanelRowHeight     float32
	PanelAvatarSize    float32
	PanelNameSize      float32
	PanelPreviewSize   float32
	PanelPadding       float32
	PanelGap           float32

	/* Settings */

	SettingsRailWidth      float32
	SettingsRailRowHeight  float32
	SettingsRailTextSize   float32
	SettingsPageWidth      float32
	SettingsPagePadding    float32
	SettingsHeaderSize     float32
	SettingsCaptionSize    float32
	SettingsRowHeight      float32
	SettingsRowPaddingH    float32
	SettingsRowPaddingV    float32
	SettingsControlGap     float32
	SettingsGroupRadius    float32
	SettingsGroupGap       float32
	SettingsLabelSize      float32
	SettingsDetailSize     float32
	SettingsIconSize       float32
	SettingsControlWidth   float32
	SettingsValueWidth     float32
	SettingsInputHeight    float32
	SettingsInputRadius    float32
	SettingsToggleWidth    float32
	SettingsToggleHeight   float32
	SettingsToggleInset    float32
	SettingsSwatchSize     float32
	SettingsSliderHeight   float32
	SettingsSliderTrack    float32
	SettingsSliderKnob     float32
	SettingsUsageHeight    float32
	SettingsPaletteSize    float32
	SettingsPaletteGap     float32
	SettingsPreviewGap     float32
	SettingsNoteMarkSize   float32
	SettingsNoteMarkRadius float32
	SettingsNoteMarkGap    float32
}{
	ServerSidebarWidth:    60,
	ChannelSidebarWidth:   240,
	MemberSidebarWidth:    200,
	ChannelSidebarPadding: 6,
	ChannelLeftPadding:    8,
	UnreadIndicatorWidth:  1,
	SelectionMarkerWidth:  3,
	ChannelLabelSize:      14,

	MemberAvatarSize: 28,
	MemberRowHeight:  36,
	MemberNameSize:   13,

	MemberSectionHeight:   30,
	MemberSectionTopPad:   8,
	MemberSectionTextSize: 12,

	MemberPresenceDotSize: 10,
	MemberPresenceDotRing: 2,

	MemberStatusTextSize: 12,
	MemberStatusMarkSize: 24,
	MemberStatusPadding:  10,
	MemberStatusGap:      8,

	MemberBotMarkSize:  14,
	ProfileBotMarkSize: 18,

	ServerIconSize:          40,
	ServerItemHeight:        50,
	ServerMarkerHeight:      24,
	HashtagIconSize:         20,
	CategoryHeight:          32,
	ChannelItemHeight:       32,
	ConversationItemHeight:  44,
	ConversationAvatarSize:  32,
	CategorySpacing:         10,
	CategoryIndicatorSize:   14,
	CategoryIndicatorStroke: 2,

	MessageAreaMinWidth:  320,
	MessageAreaMinHeight: 160,

	MessageAvatarSize:             40,
	MessageAvatarColumnWidth:      46,
	MessageContentPadding:         12,
	MessageImageMaxWidth:          400,
	MessageImageMaxHeight:         300,
	MessageVerticalPadding:        10,
	MessageGroupedVerticalPadding: 2,
	MessageHorizontalPadding:      12,
	MessageAttachmentSpacing:      4,
	MessageReplyBlockGap:          14,
	MessageReplyLineInset:         28,
	MessageReplyLineThickness:     2,
	MessageReplyLineGap:           8,
	MessageTimestampSize:          12,
	MessagePinMarkSize:            11,
	MessageEditMarkSize:           10,

	// A reaction chip. The emoji is drawn a little above body size — it is the
	// whole of what the chip says, and a custom one is a picture of that side —
	// while the count beside it is the timestamp's size, being a note about it.
	ReactionEmojiSize: 15,
	ReactionCountSize: 12,
	ReactionRadius:    8,
	ReactionPaddingH:  7,
	ReactionPaddingV:  3,
	ReactionSpacing:   4,
	// The picker's grid wraps at whatever its own width allows, so the cell is the
	// only thing deciding how many fit on a row. The cell is a target rather than a
	// glyph, hence larger than the emoji it holds — which is itself larger than a
	// chip's, being the thing being aimed at rather than a label.
	EmojiPickerWidth:       324,
	EmojiPickerMaxHeight:   260,
	EmojiPickerCellSize:    34,
	EmojiPickerEmojiSize:   22,
	EmojiPickerCaptionSize: 11,
	EmojiPickerGap:         6,
	// A system line is one line whatever it says, so its margin is its own rather
	// than the gap that separates two people speaking: a run of joins is a block,
	// not a page. The mark is sized against that line, not against the avatar
	// whose gutter it is centred in.
	SystemMessageTextSize:     13,
	SystemMessageIconSize:     16,
	SystemMessagePadding:      5,
	DaySeparatorTextSize:      11,
	DaySeparatorThickness:     1,
	DaySeparatorTopPadding:    14,
	DaySeparatorBottomPadding: 2,
	DaySeparatorGap:           8,
	SwiftActionSize:           32,

	// The status line's mark is set against the sentence beside it rather than
	// against a message: it is the only thing in an otherwise empty column, so it
	// is large enough to be the shape somebody reads first.
	MessageStatusMarkSize: 30,
	MessageStatusGap:      4,

	// The note's own mark is set against the sentence rather than against the
	// header above it: the strip is a caption on the channel, and a mark the size
	// of the header's glyph would read as a second title. Its horizontal padding
	// matches the header's, so the sentence starts under the channel's name.
	ChannelNoteTextSize: 12,
	ChannelNoteMarkSize: 14,
	ChannelNoteGap:      6,
	ChannelNotePaddingV: 2,
	ChannelNotePaddingH: 8,

	ChannelTopicSize:       12,
	ChannelTopicGap:        10,
	ChannelTopicRuleHeight: 16,

	// A hairline, everywhere: a card's outline and a column seam are the same
	// stroke, so a card never reads as more framed than the column beside it.
	//
	// The blur reaches past the gap the elevated card sits in and onto the content
	// behind, because what it has to darken is the message passing underneath. A
	// halo that stopped inside the margin would only outline the gutter, which is
	// what a card sitting *in* a strip looks like.
	OutlineWidth:   1,
	CardShadowBlur: 14,

	// The indicator has to fit inside the message row's own horizontal padding —
	// width plus inset under MessageHorizontalPadding — or it is drawn over the
	// text rather than beside it. A width of zero is how it is turned off.
	//
	// The minimum height is what keeps it findable in a long channel, where the
	// viewport is a small enough fraction of the scrollback that an honest
	// proportion would be a couple of pixels.
	ScrollIndicatorWidth:     4,
	ScrollIndicatorInset:     4,
	ScrollIndicatorMinHeight: 28,

	// The bar is grabbed through a hit area this wide, drawn against its right edge:
	// four units is a hard target to hit, and widening the bar itself would put it
	// over the text. Grab width plus inset is under the same padding the bar is,
	// since a press this side of the text goes to the bar rather than the row.
	ScrollIndicatorGrabWidth: 8,

	// EmbedMaxWidth is a ceiling on the text column, not the width every card is
	// drawn at: an embed is measured against what it says and only capped here, so
	// a two-word preview stays small and a paragraph wraps instead of running the
	// width of the window.
	EmbedMaxWidth:       400,
	EmbedAccentWidth:    3,
	EmbedRadius:         6,
	EmbedPaddingV:       10,
	EmbedPaddingH:       12,
	EmbedAccentGap:      10,
	EmbedRowGap:         5,
	EmbedIconSize:       16,
	EmbedIconGap:        6,
	EmbedSiteTextSize:   11,
	EmbedTitleTextSize:  14,
	EmbedImageMaxHeight: 240,
	EmbedSpacing:        4,

	// CodeBlockSpacing is the gap to the text on either side of the well, on top of
	// the padding that text already carries — a couple of units, not a margin.
	CodeBlockRadius:   6,
	CodeBlockPaddingV: 10,
	CodeBlockPaddingH: 12,
	CodeBlockSpacing:  2,
	CodeCopySize:      20,
	CodeCopyInset:     5,

	// InviteCardWidth is the card's whole width, not a ceiling like
	// EmbedMaxWidth: an invite mounts empty and is filled in a moment later, so
	// it has to be the same size before and after.
	InviteCardWidth:   340,
	InviteIconSize:    44,
	InviteCaptionSize: 11,
	InviteNameSize:    15,
	InviteDetailSize:  12,
	InviteTextGap:     2,

	// The dock's margin is the gap it floats in, not what does the floating — the
	// messages running under it are. It also has to stay under the message row's
	// own horizontal padding: content wider than the gutter would show beside the
	// card instead of disappearing behind it.
	//
	// The padding *inside* the card is deliberately small by contrast: the entry
	// already carries InnerPadding above and below its text, so the card only
	// needs a couple of pixels more before it starts looking slack.
	ComposerMaxLines:    8,
	ComposerDockMargin:  8,
	ComposerRadius:      8,
	ComposerPaddingV:    3,
	ComposerPaddingH:    6,
	ComposerGutterWidth: 30,
	ComposerButtonSize:  24,
	ComposerIconSize:    18,
	ComposerNoticeMark:  14,
	ComposerNoticeGap:   6,
	MentionRowHeight:    30,
	MentionAvatarSize:   20,
	MentionNameSize:     13,
	MentionHandleSize:   11,

	// The inset keeps the chip off the card's rounded top-right corner, which it
	// would otherwise sit against.
	SlowmodeGlyphSize: 15,
	SlowmodeTextSize:  14,
	SlowmodeGap:       6,
	SlowmodeInsetH:    10,
	SlowmodeDockGap:   6,

	// The typing line hangs at the other end of the same row, so it takes the
	// chip's inset and gap unchanged and is set a size smaller: it is the one
	// thing over the message column that is nobody's message, and a sentence there
	// at conversation size reads as one. The dots are square on the line's height.
	TypingMarkSize:    22,
	TypingTextSize:    13,
	TypingAvatarSize:  16,
	TypingGap:         6,
	TypingInsetH:      10,
	ChannelTypingSize: 18,

	// The pill each of the two wears. Tight enough that it hugs what it holds
	// rather than reading as a second bar above the composer, which is what a
	// full-width surface there would.
	DockBadgeRadius:   6,
	DockBadgePaddingV: 3,
	DockBadgePaddingH: 8,

	JumpBarRadius:   8,
	JumpBarPaddingV: 6,
	JumpBarPaddingH: 12,
	JumpBarTextSize: 13,
	JumpBarDockGap:  6,

	// The avatar overhangs the banner by half its height, so the banner is sized
	// against it: too short and the avatar hangs off the card's top edge.
	ProfileCardWidth:          320,
	ProfileDialogWidth:        440,
	ProfileBannerHeight:       60,
	ProfileDialogBannerHeight: 96,
	ProfileAvatarSize:         64,
	ProfileDialogAvatarSize:   84,
	ProfileAvatarRing:         4,
	ProfilePresenceRing:       3,
	ProfileNameSize:           17,
	ProfileDialogNameSize:     21,
	ProfileHandleSize:         14,
	ProfileHandleRadius:       5,
	ProfileHandlePaddingV:     2,
	ProfileHandlePaddingH:     6,
	ProfileStatusSize:         12,
	ProfileDetailSize:         12,
	ProfileDetailIconSize:     13,
	ProfileBioMaxHeight:       160,
	ProfileBioRadius:          6,
	ProfileBioPadding:         10,
	ProfileSectionSize:        10,
	ProfilePadding:            14,
	ProfileGap:                10,
	ProfileTightGap:           4,
	ProfileCornerRadius:       8,

	// The corner follows the height rather than the width — a button is as round as
	// it is tall, which is what keeps a full-width one from reading as a bar — and
	// the horizontal padding is twice the vertical, so a short word still sits in a
	// button rather than in a box around it. The minimums are what stop "Join" and
	// "Retry" from being smaller targets than the words beside them.
	ButtonRadius:    9,
	ButtonTextSize:  13,
	ButtonPaddingV:  8,
	ButtonPaddingH:  16,
	ButtonMinWidth:  76,
	ButtonMinHeight: 32,

	// The dot is what carries a role's colour once the chip has an edge of its own,
	// so it is sized against the cap height of the text beside it rather than the
	// chip: bigger and it reads as a bullet the name hangs off.
	ChipTextSize: 11,
	ChipRadius:   9,
	ChipPaddingV: 3,
	ChipPaddingH: 8,
	ChipSpacing:  4,
	ChipDotSize:  8,
	ChipDotGap:   5,

	PopoverGap:    10,
	PopoverMargin: 12,

	TooltipTextSize: 13,
	TooltipRadius:   4,
	TooltipPaddingV: 5,
	TooltipPaddingH: 9,
	TooltipGap:      8,

	NoticeWidth:       320,
	NoticeRadius:      8,
	NoticeEdgeWidth:   3,
	NoticeIconSize:    18,
	NoticePaddingV:    10,
	NoticePaddingH:    12,
	NoticeStackMargin: 12,
	NoticeTitleSize:   13,
	NoticeBodySize:    12,
	NoticeTitleGap:    3,
	// The bar that drains along the bottom edge, so a notice says how long it has
	// left rather than simply vanishing.
	NoticeCountdown:   3,
	NoticeCardSpacing: 8,
	ConfirmWidth:      360,
	ConfirmRadius:     6,
	ConfirmButtonGap:  4,
	ConfirmHintSize:   11,

	SessionCardAvatarSize: 32,
	WindowDefaultWidth:    1200,
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

	ChannelDialogWidth: 380,
	DialogPadding:      14,
	DialogLabelSize:    11,
	DialogLabelGap:     5,
	DialogFieldGap:     14,

	FriendsDialogWidth:   460,
	FriendsListMaxHeight: 420,
	FriendsRowHeight:     52,
	FriendsAvatarSize:    36,
	FriendsNameSize:      15,
	FriendsHandleSize:    12,
	FriendsSectionSize:   12,
	FriendsPadding:       12,
	FriendsGap:           6,

	PanelDialogWidth:   480,
	PanelListMaxHeight: 420,
	PanelRowHeight:     54,
	PanelAvatarSize:    32,
	PanelNameSize:      14,
	PanelPreviewSize:   12,
	PanelPadding:       12,
	PanelGap:           6,

	SettingsRailWidth:     210,
	SettingsRailRowHeight: 34,
	SettingsRailTextSize:  14,
	// The pane is capped rather than filled: a row is a label at one end and one
	// control at the other, and across a maximised window the two lose each other.
	SettingsPageWidth:   720,
	SettingsPagePadding: 24,
	SettingsHeaderSize:  22,
	SettingsCaptionSize: 11,
	SettingsRowHeight:   52,
	SettingsRowPaddingH: 14,
	// Chosen against SettingsInputHeight: the two together are the row height, so
	// a row holding a control is exactly as tall as one holding only text.
	SettingsRowPaddingV: 8,
	// The gap above a control that sits under its description rather than beside
	// it. A slider is given the row's whole width, so nothing but this separates
	// it from the sentence explaining it.
	SettingsControlGap:   10,
	SettingsGroupRadius:  8,
	SettingsGroupGap:     20,
	SettingsLabelSize:    14,
	SettingsDetailSize:   11,
	SettingsIconSize:     18,
	SettingsControlWidth: 190,
	// The number beside a slider gets a slot of its own, so the slider does not
	// shorten as the value gains a digit — and it is wide enough for the field
	// that replaces it when the number is clicked.
	SettingsValueWidth:   58,
	SettingsInputHeight:  36,
	SettingsInputRadius:  6,
	SettingsToggleWidth:  40,
	SettingsToggleHeight: 22,
	SettingsToggleInset:  3,
	SettingsSwatchSize:   20,
	SettingsSliderHeight: 20,
	SettingsSliderTrack:  4,
	SettingsSliderKnob:   14,
	SettingsUsageHeight:  8,
	SettingsPaletteSize:  22,
	SettingsPaletteGap:   6,
	SettingsPreviewGap:   10,
	// The badge a note is filed under: a box wide enough for a letter, and a
	// corner just off square, so it reads as a mark rather than as a button.
	SettingsNoteMarkSize:   16,
	SettingsNoteMarkRadius: 4,
	SettingsNoteMarkGap:    6,
}

// selectionTint is the accent used for text selection, alpha'd so the glyphs
// underneath stay legible. Apply recomputes it from the palette, so it follows a
// changed accent rather than staying on the one compiled in here.
var selectionTint color.Color = color.RGBA{R: 91, G: 124, B: 250, A: 90}

// noShadow removes Fyne's only other edge treatment. A scroll paints it as a
// gradient along whichever edge has more content past it — a smear rather than a
// line, which read as a bar welded under the message area once the composer
// floated free. Outline is the single edge in the client.
var noShadow = color.Transparent

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

// The code highlighter's colours, as names. A RichText segment carries a theme
// *name* rather than a colour, so a palette it draws from has to be answerable
// here; the prefix keeps them clear of Fyne's own.
const (
	ColorNameCode        fyne.ThemeColorName = "rgoCode"
	ColorNameCodeKeyword fyne.ThemeColorName = "rgoCodeKeyword"
	ColorNameCodeString  fyne.ThemeColorName = "rgoCodeString"
	ColorNameCodeComment fyne.ThemeColorName = "rgoCodeComment"
	ColorNameCodeNumber  fyne.ThemeColorName = "rgoCodeNumber"
	ColorNameCodeCall    fyne.ThemeColorName = "rgoCodeCall"
)

// Color maps Fyne's semantic colour names onto the palette.
func (t *AppTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case ColorNameCode:
		return Colors.CodeText
	case ColorNameCodeKeyword:
		return Colors.CodeKeyword
	case ColorNameCodeString:
		return Colors.CodeString
	case ColorNameCodeComment:
		return Colors.CodeComment
	case ColorNameCodeNumber:
		return Colors.CodeNumber
	case ColorNameCodeCall:
		return Colors.CodeCall
	case theme.ColorNameScrollBar:
		return color.Transparent
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return Colors.ServerSelectedBg
	// The tones a notice or a confirmation button paints itself with; Fyne's
	// Danger/Warning button importances read them straight off the theme.
	case theme.ColorNameError:
		return Colors.NoticeDanger
	case theme.ColorNameWarning:
		return Colors.NoticeWarning
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
		return noShadow
	}

	return t.Theme.Color(name, variant)
}

// Size hides scrollbars and flattens inputs: no corner radius, no border stroke,
// so an entry reads as a flat filled bar. The zero border also collapses the
// caret, which Fyne draws InputBorder wide — ui.WithCaret restores it per entry.
//
// *Both* scrollbar sizes are zeroed. Fyne's bar lives in a scrollBarArea over the
// content's right edge, a hover-accepting widget sized ScrollBarSmall*2 at rest,
// so zeroing only the large one left a strip that — being innermost — took the
// hover a message row needed. ui.ObservableScroll draws an inert indicator instead.
func (t *AppTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar, theme.SizeNameScrollBarSmall,
		theme.SizeNameInputRadius, theme.SizeNameInputBorder:
		return 0
	case theme.SizeNameButtonRadius:
		return Sizes.ButtonRadius // for anything Fyne still draws a button inside
	case theme.SizeNameText:
		if fontSize > 0 {
			return fontSize
		}
	}

	return t.Theme.Size(name)
}
