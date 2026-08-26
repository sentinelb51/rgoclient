package ui

// The reaction row under a message. Adding one opens the shared picker
// (ui/emoji.go) through the controller, which knows what is on offer.

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

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

// buildReactions is the chip row under a message, one chip per emoji plus the
// one that adds another. Nil when there is nothing to draw and nothing to offer.
// Without canReact the chips still report who chose what, but answer no click.
func buildReactions(deps Deps, message *domain.Message, canReact bool, onMenu func(*fyne.PointEvent), onHover func(bool)) fyne.CanvasObject {
	if len(message.Reactions) == 0 && !canReact {
		return nil
	}

	self := deps.Store.SelfID()
	allowed, restricted := message.ReactionsAllowed()

	chips := make([]fyne.CanvasObject, 0, len(message.Reactions)*2+1)
	add := func(chip fyne.CanvasObject) {
		if len(chips) > 0 {
			chips = append(chips, HorizontalSpacer(theme.Sizes.ReactionSpacing))
		}
		chips = append(chips, chip)
	}

	for i := range message.Reactions {
		reaction := &message.Reactions[i]

		// Read out now rather than captured: the click arrives through the closure,
		// and a pointer into a message since replaced is a stale read.
		emoji, mine, users := reaction.Emoji, reaction.By(self), reaction.Users

		// Joining one the message forbids would be refused by the server, so the chip
		// says who chose it and answers nothing. Leaving one already joined stays
		// open either way — a restriction is on what may be added.
		joinable := mine || !restricted || slices.Contains(allowed, emoji)

		var onTap func()
		if canReact && joinable {
			onTap = func() { deps.Actions.OnReact(message, emoji, !mine) }
		}

		// Names folded on hover rather than now: a page carries hundreds of chips
		// and nobody is over more than one.
		chip := newReactionChip(deps, emoji, reaction.Count(), mine, onTap, onMenu, onHover)
		add(chip.saying(deps.Tooltip, func() string { return reactorNames(deps.Store, users) }))
	}

	if canReact && (!restricted || len(allowed) > 0) {
		add(newAddReactionChip(deps, message, allowed, onMenu, onHover))
	}

	return HBoxNoSpacing(chips...)
}

/* The chip */

// reactionChip is one emoji on a message and how many people chose it. Declaring
// hover takes it from the message row beneath (innermost wins), so it reports
// back through onHover — otherwise the row's quick actions vanish the moment the
// pointer crosses a chip on the way to them.
type reactionChip struct {
	tapBase

	background *canvas.Rectangle
	content    fyne.CanvasObject

	// tip is asked at the moment of the hover — see saying.
	tooltip *Tooltip
	tip     func() string

	mine    bool
	hovered bool
	onHover func(bool)
}

var (
	_ fyne.Tappable     = (*reactionChip)(nil)
	_ desktop.Hoverable = (*reactionChip)(nil)
)

func newReactionChip(deps Deps, emoji string, count int, mine bool, onTap func(), onMenu func(*fyne.PointEvent), onHover func(bool)) *reactionChip {
	number := newText(strconv.Itoa(count), theme.Colors.ReactionCount, theme.Sizes.ReactionCountSize)
	if mine {
		number.Color = theme.Colors.ReactionMine
	}

	return newChipOf(HBoxNoSpacing(
		newReactionEmoji(deps, emoji),
		HorizontalSpacer(theme.Sizes.ReactionSpacing),
		vcenter(number),
	), mine, onTap, onMenu, onHover)
}

// newAddReactionChip is the chip at the end of the row that opens the picker; it
// carries a mark rather than an emoji since it stands for all of them.
func newAddReactionChip(deps Deps, message *domain.Message, allowed []string, onMenu func(*fyne.PointEvent), onHover func(bool)) *reactionChip {
	mark := newScaledIcon(tintedIcon(assets.ActionAddIcon, theme.Colors.ReactionCount), theme.Sizes.ReactionEmojiSize)

	var chip *reactionChip
	chip = newChipOf(container.NewCenter(mark), false, func() {
		deps.Actions.OnPickEmoji(chip, allowed, func(choice EmojiChoice) {
			deps.Actions.OnReact(message, choice.Value(), true)
		})
	}, onMenu, onHover)

	return chip.saying(deps.Tooltip, func() string { return "Add a reaction" })
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

// saying gives the chip what to say while hovered. A func rather than a string:
// the text is a walk of the people in the reaction, paid for only on the hover.
func (c *reactionChip) saying(tooltip *Tooltip, tip func() string) *reactionChip {
	c.tooltip, c.tip = tooltip, tip

	return c
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

	// Above rather than beside: a label past the right edge names the next chip.
	if on {
		c.tooltip.ShowAbove(c.tip(), c)
	} else {
		c.tooltip.Hide()
	}

	reportHover(c.onHover, on)
}

// fill follows the same rule as a mentioned row: the accent is a rest state, so
// hovering lifts it rather than replacing it with the ordinary hover fill.
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

/* Who is in one */

// reactionTipNames is how many people a chip's tooltip names before the rest
// become a count.
const reactionTipNames = 10

// reactorNames is who chose an emoji, in the typing indicator's line: names then
// a count of the rest. Someone the store cannot name is counted, not named — a
// reaction reaches accounts nothing on the page has resolved.
func reactorNames(store domain.Store, users []string) string {
	names := make([]string, 0, min(len(users), reactionTipNames))

	hidden := 0
	for _, userID := range users {
		name := store.UserName(userID)
		if name == "" || len(names) == reactionTipNames {
			hidden++
			continue
		}
		names = append(names, name)
	}

	if len(names) == 0 {
		if hidden == 1 {
			return "Someone"
		}

		return strconv.Itoa(hidden) + " people"
	}

	line := strings.Join(names, ", ")
	if hidden > 0 {
		line += " +" + strconv.Itoa(hidden)
	}

	return line
}

/* The emoji */

// newReactionEmoji draws one emoji at chip size: a picture for a custom one, the
// character itself otherwise. The square is reserved before the request starts,
// so one arriving repaints its own cell rather than moving the chips beside it.
// Unicode goes through canvas.Text — Fyne falls back to the platform's emoji font.
func newReactionEmoji(deps Deps, emoji string) fyne.CanvasObject {
	side := theme.Sizes.ReactionEmojiSize

	if !util.IsEmojiID(emoji) {
		text := newText(emoji, theme.Colors.TextPrimary, side)

		return vcenter(text)
	}

	size := fyne.NewSize(side, side)
	frame := container.NewGridWrap(size, canvas.NewRectangle(color.Transparent))
	deps.Emojis.LoadIntoContainer(emoji, deps.Store.EmojiURL(emoji), size, frame, false, nil)

	return container.NewCenter(frame)
}
