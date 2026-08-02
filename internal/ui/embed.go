package ui

// Embeds — the cards drawn beneath a message: a link the server unfurled into a
// preview, or one an integration composed itself. One builder covers both,
// branching on what the embed actually carries rather than on its kind, because
// the two shapes overlap almost entirely.
//
// The card is sized to what it says. A wrapping body has no natural width — it
// takes whatever it is given — so the width is measured from the text as a
// single line and capped at EmbedMaxWidth, which is what stops a two-word
// preview drawing a card the width of the message area and a paragraph refusing
// to wrap at all.

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

// buildEmbeds stacks a message's embeds, separated by the same small gap that
// separates its attachments. Each row is boxed horizontally so the card keeps
// its own width instead of being stretched to the message column's.
func buildEmbeds(deps Deps, embeds []*domain.Embed, onMenu func(*fyne.PointEvent)) *fyne.Container {
	box := container.NewVBox()

	for i, embed := range embeds {
		if i > 0 {
			box.Add(VerticalSpacer(theme.Sizes.EmbedSpacing))
		}
		box.Add(HBoxNoSpacing(buildEmbed(deps, embed, onMenu)))
	}

	return box
}

// buildEmbed renders one embed. An embed with nothing to say — Revolt's bare
// image kind, or an unfurl that found only a picture — is drawn as the picture
// alone: a card around it would be an empty frame with a stripe down the side.
//
// The card itself is inert: it is drawn from plain containers, so hover and
// right-click both pass straight through to the message row it belongs to. Only
// the parts that actually do something — the title, the picture — are widgets,
// and each takes onMenu so a right-click on one still raises the message's menu.
func buildEmbed(deps Deps, embed *domain.Embed, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	if embed.Title == "" && embed.Description == "" && embed.SiteName == "" && embed.Image != nil {
		return buildEmbedImage(deps, embed.Image, theme.Sizes.EmbedMaxWidth, onMenu)
	}

	width := embedContentWidth(embed)
	padV, padH := theme.Sizes.EmbedPaddingV, theme.Sizes.EmbedPaddingH

	// The hairline is what keeps the card's shape when the row underneath is
	// hovered: the fill alone is a couple of steps off the message area, which a
	// highlight can close, but the shared outline is darker than either state and
	// cannot be washed out by one. It is drawn as the background's own stroke
	// rather than stacked over the card, since everything inside sits within the
	// card's padding and so never paints over its edge.
	background := canvas.NewRectangle(theme.Colors.EmbedBg)
	background.CornerRadius = theme.Sizes.EmbedRadius
	background.StrokeColor = theme.Colors.Outline
	background.StrokeWidth = theme.Sizes.OutlineWidth

	// The stripe is inset with the content rather than run flush down the card's
	// edge, so it needs no corner of its own to meet the rounded ones: a pill
	// beside the text, which is also what keeps it off the message hover fill.
	accent := canvas.NewRectangle(embedAccent(embed))
	accent.SetMinSize(fyne.NewSize(theme.Sizes.EmbedAccentWidth, 0))
	accent.CornerRadius = theme.Sizes.EmbedAccentWidth / 2

	row := NewFillRow(2, accent, HorizontalSpacer(theme.Sizes.EmbedAccentGap), buildEmbedBody(deps, embed, width, onMenu))

	return container.NewStack(background, NewInset(row, padV, padV, padH, padH))
}

// embedAccent is the colour of the stripe: the embed's own where it named one
// the conversion could parse, the palette's default otherwise.
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
		add(buildEmbedTitle(embed, onMenu))
	}
	if embed.Description != "" {
		// The same renderer a message body goes through, so an embed's markdown,
		// mentions and text selection all behave exactly as a message's do.
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
	name := canvas.NewText(embed.SiteName, theme.Colors.EmbedSite)
	name.TextSize = theme.Sizes.EmbedSiteTextSize

	// The name absorbs the leftover width and shortens into it; an ellipsis text
	// reports no width of its own, so it has to be the row's filling child or it
	// would be handed none at all.
	if embed.IconURL == "" {
		return NewFillRow(0, NewEllipsisText(name))
	}

	side := theme.Sizes.EmbedIconSize
	size := fyne.NewSize(side, side)
	icon := container.NewGridWrap(size, canvas.NewRectangle(theme.Colors.EmbedBg))
	images.LoadIntoContainer(imageCacheID(embed.IconURL), embed.IconURL, size, icon, false, nil)

	return NewFillRow(2, container.NewCenter(icon), HorizontalSpacer(theme.Sizes.EmbedIconGap), NewEllipsisText(name))
}

// buildEmbedTitle is the headline. It leads to the page the embed was unfurled
// from when there is one, and is drawn in the accent either way — a title with
// nowhere to go still heads its card.
//
// It is one line that shortens rather than a wrapping paragraph: a card is a
// summary, and a title long enough to wrap is one the description already says
// more usefully.
func buildEmbedTitle(embed *domain.Embed, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	title := canvas.NewText(embed.Title, theme.Colors.EmbedTitle)
	title.TextSize = theme.Sizes.EmbedTitleTextSize
	title.TextStyle = fyne.TextStyle{Bold: true}

	line := NewFillRow(0, NewEllipsisText(title))
	if embed.URL == "" {
		return line
	}

	return newEmbedLink(line, embed.URL, onMenu)
}

// embedLink is the tappable title.
//
// It is a widget of its own rather than a TappableContainer because that one is
// hoverable, and Fyne delivers hover to the innermost hoverable object: a title
// accepting hover would take it off the message row and drop the row's quick
// actions every time the pointer crossed the card. ui.Avatar is left
// unhoverable for exactly the same reason.
type embedLink struct {
	tapBase
	content fyne.CanvasObject
}

var (
	_ fyne.Tappable          = (*embedLink)(nil)
	_ fyne.SecondaryTappable = (*embedLink)(nil)
)

func newEmbedLink(content fyne.CanvasObject, link string, onMenu func(*fyne.PointEvent)) *embedLink {
	l := &embedLink{content: content}
	l.onTap = func() { openURL(link) }
	l.onSecondaryTap = onMenu
	l.ExtendBaseWidget(l)

	return l
}

func (l *embedLink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.content)
}

// buildEmbedImage renders the embed's picture, which opens in the lightbox when
// tapped like an attachment does. width is the column it has to fit inside; a
// picture smaller than that is never enlarged to fill it.
func buildEmbedImage(deps Deps, file *domain.File, width float32, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	size := fitWithin(file.Width, file.Height, width, theme.Sizes.EmbedImageMaxHeight)
	if size.Width == 0 || size.Height == 0 {
		// Revolt carries no dimensions for a bare image embed, so reserve a wide,
		// half-height box and let the row settle once the picture arrives.
		size = fyne.NewSize(width, theme.Sizes.EmbedImageMaxHeight/2)
	}

	placeholder := canvas.NewRectangle(theme.Colors.ServerDefaultBg)
	placeholder.SetMinSize(size)
	image := container.NewStack(placeholder)
	deps.Images.LoadIntoContainer(imageCacheID(file.URL), file.URL, size, image, false, nil)

	stack := NewHoverableStack(image, func() { deps.Actions.OnAttachmentTapped(file) }, nil)
	stack.onSecondaryTap = onMenu

	return stack
}

/* Width */

// embedContentWidth is the room the text column gets: the widest thing the embed
// has to draw, capped at EmbedMaxWidth.
//
// The text is measured as one unbroken line, which is what it would occupy if
// nothing made it wrap — so a short embed asks for exactly its own width and a
// long one asks for more than the cap and is cut back to it, wrapping inside
// what is left. Wrapping text cannot be asked how wide it wants to be: it
// answers with whatever it was last given.
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
		// Flattened first: the width of the markdown source counts characters the
		// reader never sees, and a card sized for its asterisks is a wide one.
		text := markdown.DocumentText(markdown.Parse(embed.Description))
		width = max(width, fyne.MeasureText(text, fynetheme.TextSize(), fyne.TextStyle{}).Width)
	}
	if embed.Image != nil {
		width = max(width, fitWithin(embed.Image.Width, embed.Image.Height,
			theme.Sizes.EmbedMaxWidth, theme.Sizes.EmbedImageMaxHeight).Width)
	}

	return min(width, theme.Sizes.EmbedMaxWidth)
}
