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

	MessageDeletedBackground      color.Color
	MessageDeletedHoverBackground color.Color

	MessageSelectedBackground      color.Color
	MessageSelectedHoverBackground color.Color
	MessageSelectTickEdge          color.Color
	MessageSelectTickOn            color.Color
	MessageSelectTickOff           color.Color
	MessageSelectMark              color.Color

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
	SettingsIslandOutline  color.Color
	FriendsCardHoverBg     color.Color
	GroupPickChosenBg      color.Color
	SettingsJumpBackground color.Color
	SettingsBackText       color.Color
	TooltipBg              color.Color
	NoticeBg               color.Color
	NoticeCardBg           color.Color
	NoticeCardOutline      color.Color
	NoticeCardBody         color.Color
	SliderCardTrack        color.Color
	SliderDetent           color.Color
	OverlayBackdrop        color.Color
	ViewerCardBg           color.Color
	ViewerBodyBg           color.Color
	ComposerBg             color.Color
	ComposerBorderFocus    color.Color
	MentionRowSelectedBg   color.Color
	MenuHoverBg            color.Color
	MenuStripeBg           color.Color

	/* Edges */

	Outline     color.Color
	MenuOutline color.Color
	CardShadow  color.Color

	/* Elements */

	AttachmentHoverBorder color.Color

	// VideoScrim is the wash a video card's chrome sits on — the play badge,
	// the duration chip, the scrub track — over whatever frame is behind it.
	// Kept darker than translucent-safe limits require: its channels stay
	// under its alpha, the premultiplied trap theme.Fade names.
	VideoScrim color.Color

	AvatarPlaceholder   color.Color
	UnreadIndicator     color.Color
	MentionIndicator    color.Color
	MentionBadgeBg      color.Color
	MentionBadgeText    color.Color
	HashtagIcon         color.Color
	CategoryText        color.Color
	CategoryIndicator   color.Color
	TextPrimary         color.Color
	TimestampText       color.Color
	DaySeparatorText    color.Color
	DaySeparatorLine    color.Color
	SystemMessageText   color.Color
	SystemMessageIcon   color.Color
	SystemMessageJoin   color.Color
	SystemMessageLeave  color.Color
	SystemMessageDanger color.Color
	SystemMessageChange color.Color
	SystemMessageCall   color.Color
	SwiftActionIcon     color.Color
	SwiftActionConfirm  color.Color
	SwiftActionCaution  color.Color
	SwiftActionDanger   color.Color
	ReplyLine           color.Color
	ReplyMentionActive  color.Color
	ReplyStaleBg        color.Color
	ReplyStaleText      color.Color
	MentionText         color.Color
	MentionHandleText   color.Color
	LinkText            color.Color
	LinkTextHover       color.Color
	SlowmodeText        color.Color
	DockBadgeBg         color.Color
	SlowmodeWaiting     color.Color
	TypingText          color.Color
	TypingMark          color.Color
	JumpBarBg           color.Color
	JumpBarHoverBg      color.Color
	JumpBarAction       color.Color
	MessageStatusMark   color.Color
	ChannelTopicText    color.Color
	ErrorText           color.Color

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

	/* Voice participants */

	VoiceParticipantName color.Color
	VoiceParticipantMark color.Color

	// VoiceSpeaking is the ring around a participant who is talking. It is drawn
	// over the row's hover fill as well as over its rest state, so it has to read
	// against both.
	VoiceSpeaking color.Color

	// The two colours a mute or a deafen mark is drawn in, which is the whole of
	// what separates them: the glyph says *what* is held and the colour says who
	// held it. Server is a moderator's, and is the only one of the pair the person
	// cannot undo; Self is their own switch. A third case — their volume turned off
	// on this machine — wears VoiceParticipantMark, being about neither.
	VoiceHoldServer color.Color
	VoiceHoldSelf   color.Color

	/* The call island */

	// The card floating at the top of the window while this account is in a call or
	// looking at a voice channel. Muted is the server line under each channel name
	// and the rule between the halves. The two state colours are the connection's
	// health, carried by the bar along the card's bottom edge — a colour rather
	// than a word, the word being what its tooltip is for. TextHover is what the
	// call's two lines light to under the pointer: they are the target, and the
	// card is the only panel in play, so a fill behind them would read as a button
	// nobody drew an edge on. Join and the danger tint are what the two ends of the
	// card wear: every control on it is outlined and carries its own colour in its
	// hairline, so nothing else has to say which button is which.
	CallIslandBackground color.Color
	CallIslandOutline    color.Color
	CallIslandText       color.Color
	CallIslandTextHover  color.Color
	CallIslandMuted      color.Color
	CallIslandStateGood  color.Color
	CallIslandStatePoor  color.Color
	CallIslandJoin       color.Color
	CallIslandDanger     color.Color

	/* Bot mark */

	// BotMark is the glyph following the name of an account Revolt marks as a
	// bot, on a member row and on a profile alike. The webhook mark takes it too:
	// both say the same thing about who is posting, and only the glyph differs.
	BotMark color.Color

	/* Invite cards */

	InviteCaption       color.Color
	InviteDetail        color.Color
	InviteFailedText    color.Color
	InviteFailedOutline color.Color

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

	// ButtonFocusRing is the edge a button wears when it is what Tab has reached.
	// Its own entry rather than the accent: it has to be legible against a tone
	// fill as well as against the plain surface, which the accent is not.
	ButtonFocusRing color.Color

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

	/* Modals: the confirmation card and the centred notice */

	// ConfirmHint is the line under the buttons naming the key that skips the
	// question. It sits below the body it follows, being an aside rather than part
	// of what is being asked.
	ConfirmHint color.Color

	// ModalBodyText is the sentence under a modal's title — what is about to
	// happen, or what just did. Quieter than the title, which is what the card is
	// read by, and brighter than the hint, which is an aside.
	ModalBodyText color.Color

	/* The message islands: pins, mentions and channel search */

	IslandBg            color.Color
	IslandWellBg        color.Color
	IslandCardBg        color.Color
	IslandCardHoverBg   color.Color
	IslandCardMentioned color.Color
	IslandChipBg        color.Color
	IslandChipHoverBg   color.Color
	IslandChipOnBg      color.Color
	IslandChipText      color.Color
	IslandChipOnText    color.Color
	IslandCountText     color.Color
	IslandBadgeText     color.Color
	IslandHintText      color.Color
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

	// A message that has been deleted and whose row is still standing — held for a
	// few seconds so the column does not jump out from under the reader. The edit
	// wash's opposite number and the one wash here that is a *state* rather than an
	// animation: it stands until the row goes, so it is a rest colour that lifts
	// under the pointer like the two below it.
	MessageDeletedBackground:      color.RGBA{R: 58, G: 32, B: 38, A: 255}, // #3A2026
	MessageDeletedHoverBackground: color.RGBA{R: 74, G: 42, B: 49, A: 255}, // #4A2A31

	// A row picked for a bulk delete. It is a *rest* colour like the mention wash
	// and moves with the pointer the same way — but it is the accent rather than
	// the warm ramp, the set being something the reader built and is about to act
	// on, and it outranks the mention wash for exactly that reason.
	MessageSelectedBackground:      color.RGBA{R: 38, G: 46, B: 76, A: 255}, // #262E4C
	MessageSelectedHoverBackground: color.RGBA{R: 47, G: 57, B: 94, A: 255}, // #2F395E

	// The tick itself: an empty ring until it is picked, the accent filled behind a
	// dark mark once it is, and the disabled grey on a row Revolt's week-long
	// window or this channel's permissions put out of reach.
	MessageSelectTickEdge: color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	MessageSelectTickOn:   color.RGBA{R: 91, G: 124, B: 250, A: 255},  // #5B7CFA accent
	MessageSelectTickOff:  color.RGBA{R: 58, G: 63, B: 78, A: 255},    // #3A3F4E
	MessageSelectMark:     color.RGBA{R: 255, G: 255, B: 255, A: 255}, // #FFFFFF, over the accent

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
	// The rim on a card standing alone, and the one edge in the client that is
	// *lighter* than what it surrounds. Outline is a groove between two surfaces
	// that meet; an island meets nothing, and a groove around one against a page
	// barely darker than it is an edge nobody sees.
	SettingsIslandOutline: color.RGBA{R: 52, G: 60, B: 80, A: 255}, // #343C50
	// The one island in the client that is also a target — a friend's row, which
	// leads to their profile. A step off SessionCardBg rather than a wash of the
	// accent: the row is the way to a person, not an action, and the outline is
	// already what says the card is a thing of its own.
	FriendsCardHoverBg: color.RGBA{R: 40, G: 45, B: 62, A: 255}, // #282D3E
	// The same card once it has been *picked*, on the group cards. Hover is a
	// pointer passing over and chosen is an answer already given, so the two cannot
	// share a fill — this one is the accent the settings jump washes with, which is
	// what the client already means by "this one".
	GroupPickChosenBg: color.RGBA{R: 48, G: 60, B: 100, A: 255}, // #303C64
	// What a jump from the rail or the search washes the group card it landed on
	// with. The accent again, as a message jump takes it, and against a lighter
	// surface than a message row — a card the same colour as the wash says nothing.
	SettingsJumpBackground: color.RGBA{R: 48, G: 60, B: 100, A: 255}, // #303C64
	// The mark on the back button, which is dimmer than the word beside it: the
	// word is what is read, and a chevron at a label's own brightness is a second
	// thing competing with it at the corner of the page.
	SettingsBackText: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	TooltipBg:        color.RGBA{R: 8, G: 9, B: 12, A: 240},      // darker than any column it floats over
	NoticeBg:         color.RGBA{R: 43, G: 49, B: 66, A: 250},    // #2B3142, lifted off whatever it floats over
	// The transient card is the call island's surface rather than the centred
	// notice's: both float over whatever is being read, and two floating cards a
	// shade apart read as a mistake. Darker than anything below it, with an edge
	// lighter than the client's one hairline — that hairline is where two surfaces
	// *meet*, and drawn here it would be a groove cut into the column behind.
	// Opaque, unlike the centred notice: the tone's disc is mixed *against* it (see
	// theme.Mix), which a translucent surface would make a guess rather than a sum.
	NoticeCardBg:      color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C
	NoticeCardOutline: color.RGBA{R: 52, G: 60, B: 80, A: 255},    // #343C50
	NoticeCardBody:    color.RGBA{R: 168, G: 176, B: 191, A: 255}, // #A8B0BF, quieter than the heading
	OverlayBackdrop:   color.RGBA{R: 8, G: 9, B: 12, A: 200},      // dim behind a modal
	ViewerCardBg:      color.RGBA{R: 31, G: 35, B: 48, A: 255},    // #1F2330, the modal card
	ViewerBodyBg:      color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C, inset well

	// A slider's own track is the colour a lifted card is, so on one it has to be
	// darker than the surface rather than lighter: unfilled travel reads as a
	// recess, and at the bottom of the range there is nothing else to read.
	SliderCardTrack: color.RGBA{R: 30, G: 34, B: 47, A: 255}, // #1E222F

	// The mark standing where a slider's pivot is. Dimmer than the knob and
	// brighter than the track: it has to be legible under the fill that crosses
	// it as well as against the recess on either side.
	SliderDetent: color.RGBA{R: 92, G: 100, B: 124, A: 255}, // #5C647C

	// The composer card fills with the entry's own input background so the entry's
	// box disappears into it; the outline draws the boundary instead, and lights up
	// with the accent while the entry holds focus.
	ComposerBg:           color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330, == ColorNameInputBackground
	ComposerBorderFocus:  color.RGBA{R: 91, G: 124, B: 250, A: 255}, // #5B7CFA accent
	MentionRowSelectedBg: color.RGBA{R: 43, G: 49, B: 66, A: 255},   // #2B3142, the picker's active row

	// The row under the pointer in a pop-up menu, kept off the accent: a menu is
	// opened *on* a choice already made, and an accent block following the pointer
	// down a dropdown reads as the option that is set rather than the one about to
	// be. It has to clear MenuStripeBg as well as the surface, or the pointer says
	// nothing on every other row of a striped list.
	MenuHoverBg: color.RGBA{R: 53, G: 60, B: 80, A: 255}, // #353C50, the lift a server row takes

	// Every other row of a dropdown, off the menu's own surface. One step under
	// the hover fill, so the three read as a ramp: rest, striped, under the
	// pointer — a list of near-identical labels is a set of rows before it is
	// hovered, and the pointer still lifts whichever row it is on above both.
	MenuStripeBg: color.RGBA{R: 38, G: 43, B: 58, A: 255}, // #262B3A

	// One hairline draws every edge in the client: a card lifted off a surface
	// (an embed, an attachment, the composer dock) and the seam between two
	// columns of the main row. It is darker than anything it is ever drawn
	// against — including the message row's hover fill, which paints *under* any
	// card the row contains — so no state of what is behind it can wash it out.
	// That ordering is the invariant, not the literal value.
	Outline: color.RGBA{R: 15, G: 17, B: 23, A: 255}, // #0F1117

	// A menu's edge, lighter for the same reason the island's is: it hangs over
	// whatever the reader was looking at rather than meeting a surface, and a
	// groove around it disappears against everything darker than the menu itself.
	MenuOutline: color.RGBA{R: 52, G: 60, B: 80, A: 255}, // #343C50

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
	VideoScrim:            color.RGBA{R: 8, G: 9, B: 12, A: 176},      // near-black wash
	AvatarPlaceholder:     color.RGBA{R: 60, G: 72, B: 110, A: 255},   // muted blue
	UnreadIndicator:       color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF

	// A mention takes over the marker the unread bar draws in rather than standing
	// beside it — every mention is unread, so a row wearing both would be saying one
	// thing twice in one slot. It leans on the same warm ramp the mentioning message
	// itself is washed in, lifted to where a bar one pixel wide still carries.
	MentionIndicator:  color.RGBA{R: 240, G: 178, B: 60, A: 255},  // #F0B23C
	MentionBadgeBg:    color.RGBA{R: 214, G: 150, B: 38, A: 255},  // #D69626, filled where the bar is a line
	MentionBadgeText:  color.RGBA{R: 26, G: 20, B: 8, A: 255},     // #1A1408, dark on the fill
	HashtagIcon:       color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	CategoryText:      color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	CategoryIndicator: color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274
	TextPrimary:       color.RGBA{R: 231, G: 233, B: 239, A: 255}, // #E7E9EF
	TimestampText:     color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	DaySeparatorText:  color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	DaySeparatorLine:  color.RGBA{R: 43, G: 49, B: 66, A: 255},    // #2B3142 hairline

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
	SwiftActionCaution: color.RGBA{R: 217, G: 164, B: 65, A: 255},  // #D9A441, undone by asking again
	SwiftActionDanger:  color.RGBA{R: 217, G: 92, B: 92, A: 255},   // #D95C5C, delete

	ReplyLine:          color.RGBA{R: 90, G: 98, B: 116, A: 255},   // #5A6274, reads over the hover fill too
	ReplyMentionActive: color.RGBA{R: 91, G: 124, B: 250, A: 70},   // accent tint
	ReplyStaleBg:       color.RGBA{R: 29, G: 33, B: 44, A: 255},    // #1D212C, sinks below the composer card rather than resting on it
	ReplyStaleText:     color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
	MentionText:        color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF, accent lifted for body text
	MentionHandleText:  color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280, the picker's @handle

	// Text that leads somewhere and is not a button: the accent again, since a
	// mention and a link are the same promise made about a word.
	LinkText:      color.RGBA{R: 147, G: 169, B: 255, A: 255}, // #93A9FF
	LinkTextHover: color.RGBA{R: 189, G: 202, B: 253, A: 255}, // #BDCAFD, the same lifted under the pointer

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

	// Somebody in a call, on their row under the voice channel in the sidebar.
	// Pitched between the channel labels above them and the category headers over
	// those: a name is a person rather than a heading, but a row nobody tapped to
	// get to must not outweigh the channel it hangs off. A role colour overrides it
	// where the member has one, as it does in the member list.
	VoiceParticipantName: color.RGBA{R: 150, G: 158, B: 175, A: 255}, // #969EAF
	VoiceParticipantMark: color.RGBA{R: 122, G: 130, B: 147, A: 255}, // #7A8293

	// Bright enough to carry against the hover fill, which is the harder of the
	// two backgrounds it is drawn over.
	VoiceSpeaking: color.RGBA{R: 61, G: 214, B: 140, A: 255}, // #3DD68C

	VoiceHoldServer: color.RGBA{R: 217, G: 92, B: 92, A: 255},  // #D95C5C, a moderator's
	VoiceHoldSelf:   color.RGBA{R: 217, G: 164, B: 65, A: 255}, // #D9A441, their own

	// The server rail's own black, which is the darkest surface the client draws:
	// the card floats over whatever is being read, and a fill lighter than the page
	// would read as a hole cut in it rather than as something laid on top. What
	// lifts it is the outline and the shadow, not the fill — the outline being
	// lighter than the client's one hairline for the reason the settings island's
	// and the context menu's are.
	CallIslandBackground: color.RGBA{R: 19, G: 21, B: 28, A: 255},    // #13151C
	CallIslandOutline:    color.RGBA{R: 52, G: 60, B: 80, A: 255},    // #343C50
	CallIslandText:       color.RGBA{R: 214, G: 219, B: 229, A: 255}, // #D6DBE5
	CallIslandTextHover:  color.RGBA{R: 255, G: 255, B: 255, A: 255}, // #FFFFFF
	CallIslandMuted:      color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	CallIslandStateGood:  color.RGBA{R: 61, G: 214, B: 140, A: 255},  // #3DD68C
	CallIslandStatePoor:  color.RGBA{R: 232, G: 179, B: 84, A: 255},  // #E8B354
	CallIslandJoin:       color.RGBA{R: 91, G: 124, B: 250, A: 255},  // #5B7CFA, the accent
	CallIslandDanger:     color.RGBA{R: 226, G: 92, B: 92, A: 255},   // #E25C5C

	// What the sidebar says when it has no rows to say it with. Pitched at the
	// section headers rather than at a name: it is furniture, not a member.
	MemberStatusText: color.RGBA{R: 122, G: 130, B: 147, A: 255}, // #7A8293

	// The mark is a stroked glyph rather than a filled chip, so it takes the
	// accent as a line colour and needs no surface behind it.
	BotMark: color.RGBA{R: 122, G: 137, B: 205, A: 255}, // #7A89CD

	InviteCaption: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	InviteDetail:  color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3

	// A code that never resolved. Both are muted against the reds a confirmation
	// uses: nothing was destroyed and nobody has to act, so the card reads as a
	// dead end at a glance without pulling the eye off the messages around it. The
	// edge is opaque rather than a tint, the palette writing straight alpha into
	// color.RGBA (see Fade).
	InviteFailedText:    color.RGBA{R: 214, G: 122, B: 122, A: 255}, // #D67A7A
	InviteFailedOutline: color.RGBA{R: 92, G: 45, B: 51, A: 255},    // #5C2D33

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

	// Near-white rather than the accent, so the ring reads against a Danger fill
	// as well as against the plain surface — the two places a focused button is
	// most likely to be answered from the keyboard.
	ButtonFocusRing: color.RGBA{R: 247, G: 248, B: 251, A: 255}, // #F7F8FB

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

	ConfirmHint:   color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	ModalBodyText: color.RGBA{R: 168, G: 176, B: 191, A: 255}, // #A8B0BF

	// An island is three surfaces deep — island, well, card — so each has to be a
	// visible step off the one under it or the cards read as a list drawn straight
	// onto the modal. The well is darker than the island it is sunk into and the
	// cards lift back out of it, which is what makes a result a thing rather than
	// a row.
	IslandBg:            color.RGBA{R: 31, G: 35, B: 48, A: 255},   // #1F2330, the modal card
	IslandWellBg:        color.RGBA{R: 19, G: 21, B: 28, A: 255},   // #13151C, sunk into it
	IslandCardBg:        color.RGBA{R: 35, G: 40, B: 56, A: 255},   // #232838, lifted back out
	IslandCardHoverBg:   color.RGBA{R: 46, G: 53, B: 72, A: 255},   // #2E3548
	IslandCardMentioned: color.RGBA{R: 214, G: 150, B: 38, A: 255}, // #D69626, the mention amber as an edge

	// A filter chip is off far more often than it is on, so the lit state is a
	// wash of the accent rather than another slate: the row has to be readable at
	// a glance as "these two, of eight".
	IslandChipBg:      color.RGBA{R: 35, G: 40, B: 56, A: 255},    // #232838
	IslandChipHoverBg: color.RGBA{R: 46, G: 53, B: 72, A: 255},    // #2E3548
	IslandChipOnBg:    color.NRGBA{R: 91, G: 124, B: 250, A: 70},  // accent tint
	IslandChipText:    color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	IslandChipOnText:  color.RGBA{R: 195, G: 206, B: 255, A: 255}, // #C3CEFF, accent lifted for text

	IslandCountText: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	IslandBadgeText: color.RGBA{R: 138, G: 146, B: 163, A: 255}, // #8A92A3
	IslandHintText:  color.RGBA{R: 107, G: 114, B: 128, A: 255}, // #6B7280
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

	// The count a channel row carries at its trailing end. Height is fixed so the
	// pill is the same shape at one digit and at three, the width following the
	// number inside it.
	MentionBadgeHeight   float32
	MentionBadgeMinSize  float32
	MentionBadgeRadius   float32
	MentionBadgeTextSize float32
	MentionBadgePadH     float32

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

	// The band around a member row's avatar, coloured by presence — the member
	// list's only presence mark. It widens the avatar block by twice this on each
	// axis, so MemberRowHeight has to keep room for it.
	MemberPresenceRing float32

	/* Voice participants */

	// A row under a voice channel: shorter than the channel it hangs off, so a
	// call reads as part of that row rather than as more channels. The indent is
	// what puts the avatar under the channel's *name* rather than under its glyph.
	VoiceRowHeight  float32
	VoiceRowIndent  float32
	VoiceAvatarSize float32
	VoiceNameSize   float32
	VoiceMarkSize   float32
	VoiceMarkGap    float32

	// The speaking ring's band. The row has to be tall enough for the avatar plus
	// twice this or the ring clips at the top and bottom.
	VoiceSpeakingRing float32

	/* The call island */

	// The card floating at the top of the window. Height is a floor rather than a
	// ceiling — two lines of text and a state bar set the real one — and Margin is
	// how far in from the window's top edge the card hangs. None of these is
	// derived from a size elsewhere: one expressed as an offset from an unrelated
	// one cannot be edited without moving something nobody asked to move, and the
	// settings page reaches this table by reflection anyway.
	CallIslandHeight     float32
	CallIslandRadius     float32
	CallIslandMargin     float32
	CallIslandShadowBlur float32

	CallIslandPaddingV   float32
	CallIslandPaddingH   float32
	CallIslandGap        float32
	CallIslandLineGap    float32
	CallIslandNameSize   float32
	CallIslandDetailSize float32

	// The picture leading a half, and the space between it and the lines it names —
	// closer than Gap, which stands between whole blocks.
	CallIslandIconSize float32
	CallIslandIconGap  float32

	// A group with no picture of its own is drawn as the faces of the people in
	// it, overlapping. Step is how far each one stands from the last, so Size minus
	// Step is how much of the one behind is covered; Ring is the band of the card's
	// own colour each face wears, which is what cuts it out of its neighbour rather
	// than smudging into it. Size plus twice Ring must not exceed IconSize, or a
	// group makes the card taller than a server does.
	CallIslandFaceSize float32
	CallIslandFaceRing float32
	CallIslandFaceStep float32

	// The strip along the card's bottom edge saying how the call is doing. Gap is
	// what stands between it and the lines above; the radius rounds its ends, a
	// bar with square ends inside a rounded card reading as a rule someone drew
	// rather than as part of the card.
	CallIslandBarHeight float32
	CallIslandBarGap    float32
	CallIslandBarRadius float32

	// No button size: an OutlinedIconButton fixes its own square at
	// IconButtonSize, so a second number here would be one nothing reads.
	CallIslandButtonGap float32

	// The strip drawn over the list while a membership is on its way or after it
	// failed to arrive. The mark is the client's own sweeping line, so its width
	// is set here the way the composer's and a channel row's are.
	MemberStatusTextSize float32
	MemberStatusMarkSize float32
	MemberStatusPadding  float32
	MemberStatusGap      float32

	/* Bot mark */

	// One family of glyphs at four sizes: a mark follows a name, and the names it
	// follows are set differently. The last three size whichever of the author
	// marks a row draws — bot, webhook, masquerade — since they are read against
	// each other in one column.
	MemberBotMarkSize     float32
	ProfileBotMarkSize    float32
	MessageAuthorMarkSize float32
	ReplyAuthorMarkSize   float32
	IslandAuthorMarkSize  float32

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
	VideoBadgeSize                float32
	VideoScrubHeight              float32
	VideoScrubLine                float32
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
	MessageSelectTickSize         float32
	MessageSelectMarkSize         float32
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
	EmojiPickerRadius             float32
	EmojiPickerPreviewSize        float32
	EmojiPickerPreviewNameSize    float32
	EmojiPickerRailWidth          float32
	EmojiPickerRailIconSize       float32
	EmojiPickerRailRowHeight      float32
	GIFPickerWidth                float32
	GIFPickerMaxHeight            float32
	GIFPickerTitleSize            float32
	GIFPickerTilePad              float32
	GIFPickerChipHeight           float32
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

	// The mark on the chip in a well's corner that copies the block, and how far in
	// from that corner the chip sits — the chip itself is as tall as a code line's
	// row, so it follows the font size. It floats over the source rather than
	// reserving a strip, so a wide first line runs under it.
	CodeCopySize  float32
	CodeCopyInset float32

	InviteCardWidth    float32
	InviteBannerHeight float32
	InviteIconSize     float32
	InviteCaptionSize  float32
	InviteNameSize     float32
	InviteDetailSize   float32
	InviteTextGap      float32
	InviteFailedMark   float32

	/* Composer and its mention picker */

	ComposerDockMargin  float32
	ComposerRadius      float32
	ComposerPaddingV    float32
	ComposerPaddingH    float32
	ComposerRowGap      float32 // between the dock's rows, and between reply cards
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
	MentionEmojiSize    float32 // a unicode emoji leading a row, drawn as text
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

	// The bar standing where the composer's entry is while messages are being
	// picked for a bulk delete.
	SelectionBarTextSize float32
	SelectionBarNoteSize float32
	SelectionBarGap      float32

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

	// ButtonFocusWidth is the ring Tab draws. Wider than the hairline on purpose:
	// at one pixel a focused plain button and an unfocused one differ only in the
	// colour of an edge nobody is looking at.
	ButtonFocusWidth float32

	// An icon button with an edge: the square it occupies, the mark inside it, and
	// the gap between two of them in a row. Sized apart from a text button — what
	// makes one of those a button is the words in it, where this has to be a target
	// on its own.
	IconButtonSize  float32
	IconButtonGlyph float32
	IconButtonGap   float32

	/* Chips */

	ChipTextSize float32
	ChipRadius   float32
	ChipPaddingV float32
	ChipPaddingH float32
	ChipSpacing  float32
	ChipDotSize  float32
	ChipDotGap   float32

	// ChipAvatarSize is the picture a chip naming somebody leads with, where a role
	// chip leads with a dot.
	ChipAvatarSize float32

	/* Anchored popovers */

	PopoverGap    float32
	PopoverMargin float32

	// The card a menu opens where the value it names is a range rather than a
	// list. Width is pinned: a slider's own minimum is one knob wide, so nothing
	// else here would give it a track.
	SliderCardWidth    float32
	SliderCardRadius   float32
	SliderCardPadding  float32
	SliderCardGap      float32
	SliderCardTextSize float32

	// The vertical padding is its own entry rather than the horizontal one: a
	// card two lines tall is all margin if the two agree, and the slider already
	// carries clearance of its own inside its height.
	SliderCardPaddingV float32

	// The mark that says which direction the value is about, ahead of its title.
	// The gap is the head row's throughout: after the mark, and between a title
	// that ellipsises and the reading it must not run into.
	SliderCardIconSize float32
	SliderCardHeadGap  float32

	/* Hover tooltips */

	TooltipTextSize float32
	TooltipRadius   float32
	TooltipPaddingV float32
	TooltipPaddingH float32
	TooltipGap      float32

	/* Notices and confirmations */

	NoticeWidth      float32
	NoticeRadius     float32
	NoticeShadowBlur float32
	NoticePaddingV   float32
	NoticePaddingH   float32

	// The disc the tone's mark stands on at the card's leading edge, its ring, and
	// the gap between it and the heading.
	NoticeBadgeSize float32
	NoticeBadgeRing float32
	NoticeBadgeGap  float32
	NoticeIconSize  float32

	// NoticeAvatarSize is the face standing in that same slot on a notice about a
	// person rather than an outcome. Larger than the disc: a glyph is legible at any
	// size and a face is not.
	NoticeAvatarSize float32

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
	ConfirmTitleSize  float32
	ConfirmTitleGap   float32
	ConfirmPadding    float32

	/* The modal notice: one card in the middle of the window, briefly */

	ModalNoticeWidth    float32
	ModalNoticeRadius   float32
	ModalNoticePadding  float32
	ModalNoticeMarkSize float32
	ModalNoticeMarkGap  float32
	ModalNoticeTitle    float32
	ModalNoticeBodyGap  float32

	/* Login */

	SessionCardAvatarSize float32
	SessionCardRadius     float32
	SessionCardPadding    float32
	SessionCardGap        float32
	SessionCardNameSize   float32
	WindowDefaultWidth    float32
	WindowDefaultHeight   float32

	/* Attachment viewer */

	ViewerMaxWidth     float32
	ViewerMaxHeight    float32
	ViewerMinWidth     float32
	ViewerMinHeight    float32
	ViewerMargin       float32
	ViewerBarHeight    float32
	ViewerPadding      float32
	ViewerCornerRadius float32
	ViewerTitleSize    float32

	/* Every card on the modal layer, and the fields it holds */

	ChannelDialogWidth float32
	DialogPadding      float32
	DialogLabelSize    float32
	DialogLabelGap     float32
	DialogFieldGap     float32
	DialogDetailSize   float32

	/* Friends page */

	// FriendsPageWidth is a ceiling rather than a width: the page stands where the
	// messages do, which is as narrow as MessageAreaMinWidth and as wide as a
	// maximised window, and a row is a name at one end and its buttons at the other.
	FriendsPageWidth   float32
	FriendsPagePadding float32

	// FriendsFilterWidth is the box in the header's trailing edge. Fixed rather
	// than filling: the header is a title and this, and a field taking the whole
	// remainder of a maximised window reads as the page's subject.
	FriendsFilterWidth float32

	FriendsRowHeight    float32
	FriendsCardPaddingH float32
	FriendsCardPaddingV float32
	FriendsAvatarSize   float32
	FriendsNameSize     float32
	FriendsHandleSize   float32
	FriendsCaptionSize  float32

	// The ask bar at the head of the page: one field surface with the button seated
	// inside its trailing edge, so the two read as one control rather than a box in
	// a box. FriendsAskInset is the clearance around that button, and the height is
	// chosen against it — ButtonMinHeight plus twice the inset.
	FriendsAskHeight float32
	FriendsAskRadius float32
	FriendsAskInset  float32
	FriendsAskGlyph  float32

	// FriendsGap separates what is on a row, FriendsCardGap one card from the next
	// and FriendsGroupGap one section from the one before it.
	FriendsGap      float32
	FriendsCardGap  float32
	FriendsGroupGap float32

	/* Picking people: the group cards */

	// GroupDialogWidth is wider than a channel's card because the card holds a
	// list: a name and a handle side by side need the room a single field does not.
	// GroupPickerHeight is how much of that list is on screen before it scrolls,
	// which is what stops a card of forty friends from being taller than the window.
	GroupDialogWidth  float32
	GroupPickerHeight float32
	GroupPickMarkSize float32

	/* The message islands: pins, mentions and channel search */

	IslandWidth   float32
	IslandRadius  float32
	IslandPadding float32
	IslandGap     float32

	SearchFieldHeight float32
	SearchFieldRadius float32
	SearchFieldGlyph  float32

	SearchDrawerPadding float32
	SearchDateWidth     float32
	SearchLabelSize     float32

	IslandChipHeight   float32
	IslandChipRadius   float32
	IslandChipPaddingH float32
	IslandChipGap      float32
	IslandChipGlyph    float32
	IslandChipTextSize float32

	IslandWellRadius    float32
	IslandWellPadding   float32
	IslandListMaxHeight float32
	IslandCountTextSize float32

	IslandCardRadius    float32
	IslandCardPadding   float32
	IslandCardGap       float32
	IslandCardSpacing   float32
	IslandAvatarSize    float32
	IslandNameSize      float32
	IslandTimeSize      float32
	IslandPreviewSize   float32
	IslandBadgeGlyph    float32
	IslandBadgeTextSize float32
	IslandBadgeGap      float32
	IslandJumpGlyph     float32

	/* Settings */

	SettingsRailWidth     float32
	SettingsRailRowHeight float32
	SettingsRailTextSize  float32
	SettingsPageWidth     float32
	SettingsPagePadding   float32
	SettingsHeaderSize    float32
	SettingsCaptionSize   float32
	SettingsBackGap       float32
	SettingsBackMarkGap   float32
	SettingsRowHeight     float32
	SettingsRowPaddingH   float32
	SettingsRowPaddingV   float32
	SettingsControlGap    float32
	SettingsGroupRadius   float32
	SettingsGroupGap      float32
	SettingsLabelSize     float32
	SettingsDetailSize    float32
	SettingsIconSize      float32
	SettingsControlWidth  float32
	SettingsValueWidth    float32
	SettingsInputHeight   float32
	SettingsInputRadius   float32
	SettingsToggleWidth   float32
	SettingsToggleHeight  float32
	SettingsToggleInset   float32
	SettingsSwatchSize    float32
	SettingsSliderHeight  float32
	SettingsSliderTrack   float32
	SettingsSliderKnob    float32

	// The notch a pivoted slider draws at its middle, the level the knob snaps
	// back to.
	SettingsSliderDetentWidth  float32
	SettingsSliderDetentHeight float32

	SettingsUsageHeight    float32
	SettingsLevelHeight    float32
	SettingsLevelMarker    float32
	SettingsPaletteSize    float32
	SettingsPaletteGap     float32
	SettingsPreviewGap     float32
	SettingsNoteMarkSize   float32
	SettingsNoteMarkRadius float32
	SettingsNoteMarkGap    float32

	// SettingsEntryColumnWidth is the slot the first field of an entry row's second
	// line is given, so whatever follows it starts in the same place down the list
	// however long that field is. Content wider than the slot is shortened.
	SettingsEntryColumnWidth float32

	// SettingsPairGutter is the space between two cells sharing a row and
	// SettingsIslandGap the space between one row and the next, where a list is
	// drawn as a card per entry rather than as rows of one card. Both have to beat
	// the padding inside a card, or the eye pairs one card's buttons with the next
	// card's name.
	SettingsPairGutter float32
	SettingsIslandGap  float32

	// SettingsEntryLineGap separates the two lines of a row that has two — a name
	// and what is said about it. Its own entry rather than a chip's spacing, which
	// is what it used to borrow: the lines are not chips and the two numbers move
	// for different reasons.
	SettingsEntryLineGap float32

	// SettingsAreaMinLines and SettingsAreaMaxLines bound a prose field — a
	// profile's About, a server's description — which grows with what is typed
	// between the two. Lines rather than pixels, the box being sized off the text
	// size it draws at.
	SettingsAreaMinLines float32
	SettingsAreaMaxLines float32
}{
	ServerSidebarWidth:    60,
	ChannelSidebarWidth:   240,
	MemberSidebarWidth:    200,
	ChannelSidebarPadding: 6,
	ChannelLeftPadding:    8,
	UnreadIndicatorWidth:  1,
	SelectionMarkerWidth:  3,
	ChannelLabelSize:      14,

	MentionBadgeHeight:   16,
	MentionBadgeMinSize:  16,
	MentionBadgeRadius:   8,
	MentionBadgeTextSize: 11,
	MentionBadgePadH:     5,

	MemberAvatarSize: 28,
	MemberRowHeight:  36,
	MemberNameSize:   13,

	MemberSectionHeight:   30,
	MemberSectionTopPad:   8,
	MemberSectionTextSize: 12,

	MemberPresenceRing: 2,

	VoiceRowHeight:  28,
	VoiceRowIndent:  31,
	VoiceAvatarSize: 18,
	VoiceNameSize:   13,
	VoiceMarkSize:   13,
	VoiceMarkGap:    6,

	// 18 + 2x2 is 22 against a row of 28, so the ring has its headroom.
	VoiceSpeakingRing: 2,

	// A floor only: two lines over a bar come to more than this, and a half with no
	// server to name comes to less. The radius, the padding and the two text sizes
	// are the settings page's invite card — this is the same shape in another
	// place, and two cards a shade apart read as a mistake.
	CallIslandHeight: 52,
	CallIslandRadius: 8,
	CallIslandMargin: 10,

	// Half again the composer dock's. The dock meets the message area's own edge
	// and the island hangs over the header row with nothing under it, so the halo
	// is the only thing saying which of the two surfaces is on top.
	CallIslandShadowBlur: 22,

	CallIslandPaddingV:   8,
	CallIslandPaddingH:   14,
	CallIslandGap:        12,
	CallIslandLineGap:    4,
	CallIslandNameSize:   14,
	CallIslandDetailSize: 11,

	// 14 of name over 11 of detail with 4 between comes to 29, so the circle is
	// the height of the lines it stands beside.
	CallIslandIconSize: 30,
	CallIslandIconGap:  9,

	// 22 and 2 come to 26 against an icon of 30, so a cluster is no taller. A step
	// of 17 leaves 9 of each face covered — a third, which is what keeps three of
	// them reading as three people rather than as one shape, and leaves the letter
	// at a face's centre clear of the one in front.
	CallIslandFaceSize: 22,
	CallIslandFaceRing: 2,
	CallIslandFaceStep: 17,

	CallIslandBarHeight: 3,
	CallIslandBarGap:    7,
	CallIslandBarRadius: 1.5,

	CallIslandButtonGap: 6,

	MemberStatusTextSize: 12,
	MemberStatusMarkSize: 24,
	MemberStatusPadding:  10,
	MemberStatusGap:      8,

	MemberBotMarkSize:     14,
	ProfileBotMarkSize:    18,
	MessageAuthorMarkSize: 15,
	ReplyAuthorMarkSize:   12,
	IslandAuthorMarkSize:  13,

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
	VideoBadgeSize:                44,
	VideoScrubHeight:              14,
	VideoScrubLine:                4,
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
	MessageSelectTickSize:         20,
	MessageSelectMarkSize:         12,

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
	EmojiPickerWidth:       372,
	EmojiPickerMaxHeight:   260,
	EmojiPickerCellSize:    34,
	EmojiPickerEmojiSize:   22,
	EmojiPickerCaptionSize: 11,
	EmojiPickerGap:         6,
	// The island's own corner, wider than the pop-up Fyne draws behind it: the
	// picker is a card that floats rather than a menu that drops.
	EmojiPickerRadius:          12,
	EmojiPickerPreviewSize:     40,
	EmojiPickerPreviewNameSize: 13,
	// The rail is a column of server icons, so it is sized against the icon and not
	// against the cells beside it.
	EmojiPickerRailWidth:     46,
	EmojiPickerRailIconSize:  26,
	EmojiPickerRailRowHeight: 36,

	// The island is the emoji picker's own width; its columns are as tall as what
	// lands in them, so the ceiling is the whole of what says how much of a page is
	// on screen at once.
	GIFPickerWidth:     372,
	GIFPickerMaxHeight: 300,
	GIFPickerTitleSize: 11,

	// The frame a tile's picture sits inside. A canvas.Image has no corner radius
	// and Fyne clips nothing, so a picture drawn to the well's edge squares off the
	// corners it covers — the ring is what keeps the tile rounded.
	GIFPickerTilePad:    3,
	GIFPickerChipHeight: 26,
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
	InviteCardWidth: 340,

	// The banner is drawn only where the card is built already resolved, so its
	// height is never reserved on a card that may not get one — see NewInviteCardFor.
	InviteBannerHeight: 76,

	InviteIconSize:    44,
	InviteCaptionSize: 11,
	InviteNameSize:    15,
	InviteDetailSize:  12,
	InviteTextGap:     2,

	// The mark a failed card draws where a picture would be, kept well inside the
	// slot: it stands for nothing, so it must not carry an icon's weight.
	InviteFailedMark: 22,

	// The dock's margin is the gap it floats in, not what does the floating — the
	// messages running under it are. It also has to stay under the message row's
	// own horizontal padding: content wider than the gutter would show beside the
	// card instead of disappearing behind it.
	//
	// The padding *inside* the card is deliberately small by contrast: the entry
	// already carries InnerPadding above and below its text, so the card only
	// needs a couple of pixels more before it starts looking slack.
	ComposerMaxLines:    20,
	ComposerDockMargin:  8,
	ComposerRadius:      8,
	ComposerPaddingV:    3,
	ComposerPaddingH:    6,
	ComposerRowGap:      6,
	ComposerGutterWidth: 30,
	ComposerButtonSize:  24,
	ComposerIconSize:    18,
	ComposerNoticeMark:  14,
	ComposerNoticeGap:   6,
	MentionRowHeight:    30,
	MentionAvatarSize:   20,
	MentionNameSize:     13,
	MentionHandleSize:   11,
	MentionEmojiSize:    16,

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

	SelectionBarTextSize: 14,
	SelectionBarNoteSize: 12,
	SelectionBarGap:      10,

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

	ButtonFocusWidth: 2,

	// Square, and the same height as a text button so a row can hold either. The
	// mark is a little over half of it: any larger and the edge reads as a box drawn
	// round an icon rather than as the button's own shape.
	IconButtonSize:  32,
	IconButtonGlyph: 17,
	IconButtonGap:   6,

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

	ChipAvatarSize: 16,

	PopoverGap:    10,
	PopoverMargin: 12,

	SliderCardWidth:    186,
	SliderCardRadius:   10,
	SliderCardPadding:  12,
	SliderCardGap:      6,
	SliderCardTextSize: 12,
	SliderCardPaddingV: 8,
	SliderCardIconSize: 13,
	SliderCardHeadGap:  6,

	TooltipTextSize: 13,
	TooltipRadius:   4,
	TooltipPaddingV: 5,
	TooltipPaddingH: 9,
	TooltipGap:      8,

	NoticeWidth:  320,
	NoticeRadius: 10,
	// The same spread the call island floats on: how far a shadow reaches is the
	// whole of how high a card reads as floating, and this one hangs over a column
	// that is being read.
	NoticeShadowBlur: 22,
	NoticePaddingV:   11,
	NoticePaddingH:   13,

	NoticeBadgeSize:  30,
	NoticeBadgeRing:  1,
	NoticeBadgeGap:   11,
	NoticeIconSize:   16,
	NoticeAvatarSize: 36,

	NoticeStackMargin: 12,
	NoticeTitleSize:   13,
	NoticeBodySize:    12,
	NoticeTitleGap:    3,
	// The bar that drains along the bottom edge, so a notice says how long it has
	// left rather than simply vanishing.
	NoticeCountdown:   3,
	NoticeCardSpacing: 8,
	ConfirmWidth:      360,
	ConfirmRadius:     10,
	ConfirmButtonGap:  14,
	ConfirmHintSize:   11,
	ConfirmTitleSize:  16,
	ConfirmTitleGap:   8,
	ConfirmPadding:    18,

	ModalNoticeWidth:    260,
	ModalNoticeRadius:   10,
	ModalNoticePadding:  22,
	ModalNoticeMarkSize: 34,
	ModalNoticeMarkGap:  14,
	ModalNoticeTitle:    15,
	ModalNoticeBodyGap:  6,

	SessionCardAvatarSize: 32,
	SessionCardRadius:     6,
	SessionCardPadding:    8,
	SessionCardGap:        10,
	SessionCardNameSize:   13,
	WindowDefaultWidth:    1200,
	WindowDefaultHeight:   600,

	ViewerMaxWidth:     1200,
	ViewerMaxHeight:    800,
	ViewerMinWidth:     360,
	ViewerMinHeight:    240,
	ViewerMargin:       48,
	ViewerBarHeight:    38,
	ViewerPadding:      10,
	ViewerCornerRadius: 6,
	ViewerTitleSize:    13,

	ChannelDialogWidth: 380,
	DialogPadding:      14,
	DialogLabelSize:    11,
	DialogLabelGap:     5,
	DialogFieldGap:     14,
	DialogDetailSize:   12,

	FriendsPageWidth:   620,
	FriendsPagePadding: 20,
	FriendsFilterWidth: 220,

	FriendsRowHeight:    56,
	FriendsCardPaddingH: 14,
	FriendsCardPaddingV: 10,
	FriendsAvatarSize:   36,
	FriendsNameSize:     15,
	FriendsHandleSize:   12,
	FriendsCaptionSize:  11,

	FriendsAskHeight: 44,
	FriendsAskRadius: 12,
	FriendsAskInset:  6,
	FriendsAskGlyph:  16,

	FriendsGap:      10,
	FriendsCardGap:  8,
	FriendsGroupGap: 22,

	GroupDialogWidth: 400,
	// Four rows and part of the next. There is no scrollbar on this list — the rows
	// carry their own mark down the right-hand edge, where one would land — so the
	// cut card is the whole of what says the list goes on, and a ceiling that left
	// the fourth resting flush against the edge would say the opposite.
	GroupPickerHeight: 302,
	GroupPickMarkSize: 18,

	// Wide enough for what a card holds: a heading, a line and a row of badges. Any
	// narrower and the heading shortens a name to fit a date beside it.
	IslandWidth:   620,
	IslandRadius:  14,
	IslandPadding: 14,
	IslandGap:     10,

	SearchFieldHeight: 38,
	SearchFieldRadius: 10,
	SearchFieldGlyph:  16,

	SearchDrawerPadding: 10,
	SearchDateWidth:     148,
	SearchLabelSize:     11,

	IslandChipHeight:   26,
	IslandChipRadius:   13,
	IslandChipPaddingH: 9,
	IslandChipGap:      6,
	IslandChipGlyph:    13,
	IslandChipTextSize: 11,

	IslandWellRadius:    10,
	IslandWellPadding:   8,
	IslandListMaxHeight: 400,
	IslandCountTextSize: 11,

	IslandCardRadius:    9,
	IslandCardPadding:   9,
	IslandCardGap:       9,
	IslandCardSpacing:   6,
	IslandAvatarSize:    34,
	IslandNameSize:      13,
	IslandTimeSize:      11,
	IslandPreviewSize:   12,
	IslandBadgeGlyph:    12,
	IslandBadgeTextSize: 11,
	IslandBadgeGap:      4,
	IslandJumpGlyph:     15,

	SettingsRailWidth:     210,
	SettingsRailRowHeight: 34,
	SettingsRailTextSize:  14,
	// The pane is capped rather than filled: a row is a label at one end and one
	// control at the other, and across a maximised window the two lose each other.
	SettingsPageWidth:   720,
	SettingsPagePadding: 24,
	SettingsHeaderSize:  22,
	SettingsCaptionSize: 11,
	// Under the back button, and between its mark and its word. The button wears a
	// text button's own fill, outline, radius and label size — it is one, with a
	// mark in front — so only what Button has no room for is named here.
	SettingsBackGap:     12,
	SettingsBackMarkGap: 6,
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

	SettingsSliderDetentWidth:  2,
	SettingsSliderDetentHeight: 8,

	SettingsUsageHeight: 8,
	SettingsLevelHeight: 10,
	SettingsLevelMarker: 2,
	SettingsPaletteSize: 22,
	SettingsPaletteGap:  6,
	SettingsPreviewGap:  10,
	// Wide enough for a channel name of about eighteen characters at the detail
	// size, which is where a name stops being read and starts being recognised.
	SettingsEntryColumnWidth: 140,
	SettingsPairGutter:       16,
	SettingsIslandGap:        16,
	SettingsEntryLineGap:     6,
	// A floor of six lines is a paragraph on screen at once; the ceiling is where
	// a field stops being a row in a section and starts being the section.
	SettingsAreaMinLines: 6,
	SettingsAreaMaxLines: 16,
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
// floated free. Outline is the edge in the client; SettingsIslandOutline,
// MenuOutline and CallIslandOutline are the exceptions, all three surfaces
// standing on their own rather than against another — a hairline darker than
// what it is laid over reads as a groove, which is exactly wrong for something
// that has to look lifted.
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

	// The mention accent, for the covered word a spoiler draws where a mention
	// segment cannot wear the cover.
	ColorNameMention fyne.ThemeColorName = "rgoMention"
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
	case ColorNameMention:
		return Colors.MentionText
	case theme.ColorNameScrollBar:
		return color.Transparent
	case theme.ColorNamePrimary:
		return Colors.ServerSelectedBg
	// Focus is not the accent here: widget.Menu paints the item under the pointer
	// with it (widget/menu_item.go), and it is the only thing this client still
	// draws with a focus colour — a focus *ring* is drawn by nothing it mounts.
	case theme.ColorNameFocus:
		return Colors.MenuHoverBg
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
