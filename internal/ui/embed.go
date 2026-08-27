package ui

// Embeds — the cards under a message: a link the server unfurled, or one an
// integration composed. One builder covers both, branching on what the embed
// carries rather than on its kind, the two shapes overlapping almost entirely.
//
// The card is sized to what it says. A wrapping body has no natural width, so it
// is measured as a single line and capped at EmbedMaxWidth — which is what stops
// a two-word preview drawing a card the width of the message area.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
)

// buildEmbeds stacks a message's embeds, separated by the gap that separates its
// attachments. Each row is boxed horizontally so the card keeps its own width
// rather than stretching to the message column's.
func buildEmbeds(deps Deps, embeds []*domain.Embed, onMenu func(*fyne.PointEvent)) *fyne.Container {
	rows := make([]fyne.CanvasObject, 0, len(embeds))
	for _, embed := range embeds {
		rows = append(rows, HBoxNoSpacing(buildEmbed(deps, embed, onMenu)))
	}

	return stackSpaced(theme.Sizes.EmbedSpacing, rows...)
}

// buildEmbed renders one embed. One with nothing to say — a bare image kind, or
// an unfurl that found only a picture — is the picture alone: a card around it is
// an empty frame with a stripe down the side.
//
// The card is inert, drawn from plain containers, so hover and right-click pass
// through to the message row. Only the parts that do something — the title, the
// picture — are widgets, and each takes onMenu so a right-click still raises the
// message's menu.
func buildEmbed(deps Deps, embed *domain.Embed, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	if embed.Title == "" && embed.Description == "" && embed.SiteName == "" && embed.Image != nil {
		return buildEmbedImage(deps, embed.Image, theme.Sizes.EmbedMaxWidth, onMenu)
	}

	width := embedContentWidth(embed)
	padV, padH := theme.Sizes.EmbedPaddingV, theme.Sizes.EmbedPaddingH

	// The hairline keeps the card's shape when the row underneath is hovered: the
	// fill alone is a couple of steps off the message area, which a highlight can
	// close. It is the background's own stroke rather than stacked over the card,
	// everything inside sitting within the padding.
	background := canvas.NewRectangle(theme.Colors.EmbedBg)
	background.CornerRadius = theme.Sizes.EmbedRadius
	Outline(background)

	// The stripe is inset with the content rather than run flush down the edge, so
	// it needs no corner of its own to meet the rounded ones — a pill beside the
	// text, which also keeps it off the message hover fill.
	accent := canvas.NewRectangle(embedAccent(embed))
	accent.SetMinSize(fyne.NewSize(theme.Sizes.EmbedAccentWidth, 0))
	accent.CornerRadius = theme.Sizes.EmbedAccentWidth / 2

	row := NewFillRow(2, accent, HorizontalSpacer(theme.Sizes.EmbedAccentGap), buildEmbedBody(deps, embed, width, onMenu))

	return container.NewStack(background, NewInset(row, padV, padV, padH, padH))
}

// embedAccent is the stripe's colour: the embed's own where the conversion could
// parse one, the palette's default otherwise.
func embedAccent(embed *domain.Embed) color.Color {
	if embed.Color != nil {
		return embed.Color
	}

	return theme.Colors.EmbedAccent
}

// buildEmbedBody stacks what the embed says in the order it reads: where it came
// from, what it is called, what it says, and the picture that goes with it. The
// column is pinned to width so a wrapping body can never widen the card.
func buildEmbedBody(deps Deps, embed *domain.Embed, width float32, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	var rows []fyne.CanvasObject
	add := func(row fyne.CanvasObject) {
		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.EmbedRowGap))
		}
		rows = append(rows, row)
	}

	if embed.SiteName != "" {
		add(buildEmbedSite(deps.Images, embed))
	}
	if embed.Title != "" {
		add(buildEmbedTitle(deps, embed, onMenu))
	}
	if embed.Description != "" {
		// The message body renderer, so markdown, mentions and selection behave here
		// exactly as they do in a message.
		add(newFlushContainer(renderMessageBody(deps, embed.Description, onMenu)))
	}
	if embed.Image != nil {
		add(buildEmbedImage(deps, embed.Image, width, onMenu))
	}

	return NewFixedWidthContainer(width, VBoxNoSpacing(rows...))
}

// buildEmbedSite is the provenance line: the site's own mark, then its name, in
// the small muted type a caption is set in.
func buildEmbedSite(images *cache.ImageCache, embed *domain.Embed) fyne.CanvasObject {
	name := newText(embed.SiteName, theme.Colors.EmbedSite, theme.Sizes.EmbedSiteTextSize)

	// The name absorbs the leftover width: an ellipsis text reports none of its own,
	// so it has to be the row's filling child or it is handed nothing.
	if embed.IconURL == "" {
		return NewFillRow(0, NewEllipsisText(name))
	}

	side := theme.Sizes.EmbedIconSize
	size := fyne.NewSize(side, side)
	icon := container.NewGridWrap(size, canvas.NewRectangle(theme.Colors.EmbedBg))
	// An embed's mark is fetched from whatever host the unfurl named, so it is
	// keyed by its URL rather than by a path that may be shaped like an ID.
	images.LoadIntoContainer(urlCacheID(embed.IconURL), embed.IconURL, size, icon, false, nil)

	return NewFillRow(2, container.NewCenter(icon), HorizontalSpacer(theme.Sizes.EmbedIconGap), NewEllipsisText(name))
}

// buildEmbedTitle is the headline, leading to the unfurled page where there is
// one and drawn in the accent either way. One line that shortens rather than a
// wrapping paragraph: a card is a summary, and a title long enough to wrap is one
// the description already says more usefully.
func buildEmbedTitle(deps Deps, embed *domain.Embed, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	title := newBoldText(embed.Title, theme.Colors.EmbedTitle, theme.Sizes.EmbedTitleTextSize)

	line := NewFillRow(0, NewEllipsisText(title))
	if embed.URL == "" {
		return line
	}

	return newEmbedLink(deps, line, embed.URL, embed.Title, onMenu)
}

// embedLink is the tappable title — a widget of its own rather than a
// TappableContainer, which is hoverable: innermost wins, so a title accepting
// hover would drop the message row's quick actions whenever the pointer crossed
// the card. ui.Avatar is unhoverable for the same reason.
type embedLink struct {
	tapBase
	content fyne.CanvasObject
}

var (
	_ fyne.Tappable          = (*embedLink)(nil)
	_ fyne.SecondaryTappable = (*embedLink)(nil)
)

func newEmbedLink(deps Deps, content fyne.CanvasObject, link, label string, onMenu func(*fyne.PointEvent)) *embedLink {
	l := &embedLink{content: content}
	l.onTap = func() { deps.Actions.OnLinkTapped(link, label) }
	l.onSecondaryTap = onMenu
	l.ExtendBaseWidget(l)

	return l
}

func (l *embedLink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.content)
}

// buildEmbedImage renders the embed's picture, opening in the lightbox as an
// attachment does. width is the column it fits inside; a smaller picture is never
// enlarged to fill it.
func buildEmbedImage(deps Deps, file *domain.File, width float32, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	bounds := fyne.NewSize(width, theme.Sizes.EmbedImageMaxHeight)

	// Revolt carries no dimensions for a bare image embed: reserve a wide
	// half-height box and let the row settle once the picture arrives.
	reserve := fyne.NewSize(width, theme.Sizes.EmbedImageMaxHeight/2)

	picture := imageFrame(deps.Images, file, bounds, reserve, theme.Colors.ServerDefaultBg, nil)

	// The stack's hover doubles as the GIF's play control, the arrangement
	// buildAttachment has and for the same reason.
	var onHover func(bool)
	if gifCandidate(file) {
		if anim := newGIFAnimator(deps.Images, fileCacheID(file), file.URL, picture); anim != nil {
			onHover = anim.SetPlaying
		}
	}

	stack := NewHoverableStack(picture, func() { deps.Actions.OnAttachmentTapped(file) }, onHover)
	stack.onSecondaryTap = onMenu

	return stack
}

/* Width */

// embedContentWidth is the room the text column gets: the widest thing the embed
// draws, capped at EmbedMaxWidth. Text is measured as one unbroken line, so a
// short embed asks for exactly its own width and a long one is cut back to the
// cap and wraps inside it. Wrapping text cannot be asked how wide it wants to be
// — it answers with whatever it was last given.
func embedContentWidth(embed *domain.Embed) float32 {
	var width float32

	if embed.SiteName != "" {
		line := fyne.MeasureText(embed.SiteName, theme.Sizes.EmbedSiteTextSize, fyne.TextStyle{}).Width
		if embed.IconURL != "" {
			line += theme.Sizes.EmbedIconSize + theme.Sizes.EmbedIconGap
		}
		width = max(width, line)
	}
	if embed.Title != "" {
		width = max(width, fyne.MeasureText(embed.Title, theme.Sizes.EmbedTitleTextSize, fyne.TextStyle{Bold: true}).Width)
	}
	if embed.Description != "" {
		// Flattened first: the source's width counts characters the reader never sees,
		// and a card sized for its asterisks is a wide one.
		text := markdown.DocumentText(markdown.Parse(embed.Description))
		width = max(width, fyne.MeasureText(text, fynetheme.TextSize(), fyne.TextStyle{}).Width)
	}
	if embed.Image != nil {
		width = max(width, fitWithin(embed.Image.Width, embed.Image.Height,
			theme.Sizes.EmbedMaxWidth, theme.Sizes.EmbedImageMaxHeight).Width)
	}

	return min(width, theme.Sizes.EmbedMaxWidth)
}
