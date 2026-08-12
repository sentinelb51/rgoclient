package ui

// The reaction row under a message: a chip that is a button, and an emoji that is
// a picture or a character depending on what the server sent. Adding one opens
// the shared picker (ui/emoji.go) through the controller, which is what knows
// which server's emoji are on offer.

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

/* The row */

// buildReactions is the chip row that hangs under a message, one chip per emoji
// plus the one that adds another. Nil when there is nothing to draw and nothing
// to offer, which is the overwhelming majority of messages.
//
// canReact gates both halves: without it the chips still say who chose what —
// that is what they are for — but nothing here answers a click.
func buildReactions(deps Deps, message *domain.Message, canReact bool, onMenu func(*fyne.PointEvent), onHover func(bool)) fyne.CanvasObject {
	if len(message.Reactions) == 0 && !canReact {
		return nil
	}

	self := deps.Store.SelfID()

	chips := make([]fyne.CanvasObject, 0, len(message.Reactions)*2+1)
	for i := range message.Reactions {
		reaction := &message.Reactions[i]

		// The emoji is read now rather than captured as the reaction: the widget
		// outlives nothing here, but the closure is what the click arrives through
		// and a pointer into a message that has since been replaced is a stale read.
		emoji, mine := reaction.Emoji, reaction.By(self)

		var onTap func()
		if canReact {
			onTap = func() { deps.Actions.OnReact(message, emoji, !mine) }
		}

		if len(chips) > 0 {
			chips = append(chips, HorizontalSpacer(theme.Sizes.ReactionSpacing))
		}
		chips = append(chips, newReactionChip(deps, emoji, reaction.Count(), mine, onTap, onMenu, onHover))
	}

	if canReact {
		if len(chips) > 0 {
			chips = append(chips, HorizontalSpacer(theme.Sizes.ReactionSpacing))
		}
		chips = append(chips, newAddReactionChip(deps, message, onMenu, onHover))
	}

	return HBoxNoSpacing(chips...)
}

/* The chip */

// reactionChip is one emoji on a message and how many people chose it.
//
// It declares hover for itself, which takes hover from the message row beneath —
// innermost wins — so it reports back through onHover the same way the row's own
// quick-action group does. Without that the actions would vanish the moment the
// pointer crossed a chip on the way to them.
type reactionChip struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject

	mine    bool
	hovered bool
	onHover func(bool)
}

var (
	_ fyne.Tappable     = (*reactionChip)(nil)
	_ desktop.Hoverable = (*reactionChip)(nil)
)

func newReactionChip(deps Deps, emoji string, count int, mine bool, onTap func(), onMenu func(*fyne.PointEvent), onHover func(bool)) *reactionChip {
	number := canvas.NewText(strconv.Itoa(count), theme.Colors.ReactionCount)
	number.TextSize = theme.Sizes.ReactionCountSize
	if mine {
		number.Color = theme.Colors.ReactionMine
	}

	return newChipOf(HBoxNoSpacing(
		newReactionEmoji(deps, emoji),
		HorizontalSpacer(theme.Sizes.ReactionSpacing),
		vcenter(number),
	), mine, onTap, onMenu, onHover)
}

// newAddReactionChip is the chip at the end of the row: the one that opens the
// picker. It carries a mark rather than an emoji, since what it stands for is
// every emoji rather than any one of them.
func newAddReactionChip(deps Deps, message *domain.Message, onMenu func(*fyne.PointEvent), onHover func(bool)) *reactionChip {
	mark := newScaledIcon(tintedIcon(assets.ActionAddIcon, theme.Colors.ReactionCount), theme.Sizes.ReactionEmojiSize)

	var chip *reactionChip
	chip = newChipOf(container.NewCenter(mark), false, func() {
		deps.Actions.OnPickEmoji(chip, func(choice EmojiChoice) {
			deps.Actions.OnReact(message, choice.Value(), true)
		})
	}, onMenu, onHover)

	return chip
}

// newChipOf is the surface both chips share.
func newChipOf(content fyne.CanvasObject, mine bool, onTap func(), onMenu func(*fyne.PointEvent), onHover func(bool)) *reactionChip {
	c := &reactionChip{
		background: canvas.NewRectangle(color.Transparent),
		mine:       mine,
		onHover:    onHover,
	}
	c.background.CornerRadius = theme.Sizes.ReactionRadius
	c.background.FillColor = c.fill()

	padH, padV := theme.Sizes.ReactionPaddingH, theme.Sizes.ReactionPaddingV
	c.content = container.NewStack(c.background, NewInset(content, padV, padV, padH, padH))

	c.onTap = onTap
	c.onSecondaryTap = onMenu
	c.ExtendBaseWidget(c)

	return c
}

func (c *reactionChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *reactionChip) MouseIn(*desktop.MouseEvent) { c.setHovered(true) }
func (c *reactionChip) MouseOut()                   { c.setHovered(false) }

func (c *reactionChip) setHovered(on bool) {
	if c.hovered == on {
		return
	}

	c.hovered = on
	c.background.FillColor = c.fill()
	c.background.Refresh()

	if c.onHover != nil {
		c.onHover(on)
	}
}

// fill is the chip's surface, on the same four-way rule a mentioned row's
// background follows: the accent is a rest state, so hovering lifts it rather
// than replacing it with the ordinary hover fill.
func (c *reactionChip) fill() color.Color {
	switch {
	case c.mine && c.hovered:
		return theme.Colors.ReactionMineHoverBg
	case c.mine:
		return theme.Colors.ReactionMineBg
	case c.hovered:
		return theme.Colors.ReactionHoverBg
	}

	return theme.Colors.ReactionBg
}

/* The emoji */

// newReactionEmoji draws one emoji at chip size: the picture for a custom one,
// the character itself for everything else.
//
// A custom emoji is loaded into a square reserved before the request starts, so
// one arriving repaints its own cell rather than moving the chips beside it. The
// unicode half is a plain canvas.Text — Fyne falls back to the platform's emoji
// font for glyphs the client's own font has none of, which is every emoji.
func newReactionEmoji(deps Deps, emoji string) fyne.CanvasObject {
	side := theme.Sizes.ReactionEmojiSize

	if !util.IsEmojiID(emoji) {
		text := canvas.NewText(emoji, theme.Colors.TextPrimary)
		text.TextSize = side

		return vcenter(text)
	}

	size := fyne.NewSize(side, side)
	frame := container.NewGridWrap(size, canvas.NewRectangle(color.Transparent))
	deps.Emojis.LoadIntoContainer(emoji, deps.Store.EmojiURL(emoji), size, frame, false, nil)

	return container.NewCenter(frame)
}
