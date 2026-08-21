package ui

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/cache"
	"RGOClient/internal/domain"
	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
)

// renderMessageBody renders a message's body. A body of one uniform style
// flattens to a selectable Label; a mixed one falls back to RichText, which Fyne
// cannot make selectable. A mention or a custom emoji is never flattenable — the
// first has its own colour, the second is not text at all.
//
// Two RichText constraints shape the rest of this file. It only wraps and flows
// native segments when every content segment is Inline, so each line is
// terminated by an empty non-inline break (mdBuilder.lineBreak). And strike,
// underline and spoilers have no native equivalent, so decoratedSegment draws
// them — split per word, RichText breaking rows only at text spaces.
//
// onMenu is the owning message's right-click handler — see bodyText.
func renderMessageBody(deps Deps, text string, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	doc := markdown.Parse(text)

	if hasCodeBlock(doc.Blocks) {
		return renderCodeColumn(deps, doc.Blocks, onMenu)
	}

	return renderBlocks(deps, doc.Blocks, onMenu)
}

// PreviewText flattens a body onto the one line a summary has room for — a
// panel row, a notice, anything listing a message it is not drawing. The store
// is what names a custom emoji, which would otherwise be 26 characters of ULID
// or nothing at all; whitespace is collapsed because the source's is a body's
// rather than a line's.
//
// Here rather than in the controller because markdown is inside this package:
// what a row shows and what a body renders should not be two readings of one
// message.
func PreviewText(store domain.Store, content string) string {
	flat := markdown.DocumentTextNamed(markdown.Parse(content), store.EmojiName)

	return strings.Join(strings.Fields(flat), " ")
}

// hasCodeBlock reports whether a fenced block stands on its own in the body. One
// inside a quote or a list item stays in the text, drawn monospace: the well is a
// block-level card and cannot be indented into a row of running text.
func hasCodeBlock(blocks []markdown.Block) bool {
	for _, block := range blocks {
		if _, ok := block.(*markdown.CodeBlock); ok {
			return true
		}
	}

	return false
}

// renderCodeColumn stacks the body around the wells its fences draw: each run of
// ordinary blocks rendered exactly as a whole body would be, each fence a card.
//
// Every caller wraps the body in newFlushContainer to cancel a RichText's own
// inner padding, which the text rows want and a card does not — so each card puts
// that padding back, and the column comes out flush on both counts.
func renderCodeColumn(deps Deps, blocks []markdown.Block, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	pad := fynetheme.InnerPadding()

	var rows []fyne.CanvasObject
	add := func(row fyne.CanvasObject) {
		if len(rows) > 0 {
			rows = append(rows, VerticalSpacer(theme.Sizes.CodeBlockSpacing))
		}
		rows = append(rows, row)
	}

	var run []markdown.Block
	flush := func() {
		if len(run) == 0 {
			return
		}
		add(renderBlocks(deps, run, onMenu))
		run = nil
	}

	for _, block := range blocks {
		code, ok := block.(*markdown.CodeBlock)
		if !ok {
			run = append(run, block)
			continue
		}

		flush()
		add(NewInset(newCodeBlock(code.Language, code.Text, onMenu), pad, pad, pad, pad))
	}
	flush()

	return NewWrapColumn(rows...)
}

// renderBlocks renders a run of blocks: flattened to a selectable Label where
// every leaf agrees on a style, RichText otherwise.
func renderBlocks(deps Deps, blocks []markdown.Block, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	// An empty body keeps the zero-height RichText: a Label would reserve a blank
	// line above whatever the message does carry.
	if flat, ok := flattenBlocks(blocks); ok && flat.text != "" {
		label := newBodyText(flat.text, onMenu)
		label.Wrapping = fyne.TextWrapWord
		label.Selectable = true
		label.TextStyle = flat.style
		label.SizeName = flat.size
		if flat.dim {
			label.Importance = widget.LowImportance // Disabled = the muted subtext colour
		}
		return label
	}

	b := &mdBuilder{deps: deps, onMenu: onMenu}
	for _, block := range blocks {
		b.block(block, widget.RichTextStyle{})
	}

	rt := widget.NewRichText(b.segs...)
	rt.Wrapping = fyne.TextWrapWord
	if b.reserve == 0 {
		return rt
	}

	// RichText never breaks a row *before* a segment it cannot measure as text, so a
	// mention or emoji landing at a line end draws past the right edge and is cut
	// off. Narrowing the text by the widest one the body carries gives that overhang
	// somewhere to land: the words wrap earlier and spill into the strip kept clear.
	return NewFillRow(0, rt, HorizontalSpacer(b.reserve))
}

/* Selectable body */

// bodyText is the flattened, selectable message body. It exists to get a
// right-click back: a selectable Label mounts an unexported selection overlay
// above its text, and innermost wins, so that overlay takes the click and answers
// with Fyne's own one-item "Copy" menu — having first pulled focus off the
// composer.
//
// The renderer therefore lays a catcher over it and drives the overlay through
// the exported interfaces it satisfies. A Fyne that stops exposing it leaves the
// catcher out (newSelectionCatcher returns nil) and the body is a plain Label.
type bodyText struct {
	widget.Label
	onMenu func(*fyne.PointEvent)
}

var _ fyne.Widget = (*bodyText)(nil)

// newBodyText creates a message body carrying the message's context menu.
func newBodyText(text string, onMenu func(*fyne.PointEvent)) *bodyText {
	t := &bodyText{onMenu: onMenu}
	t.Text = text
	t.ExtendBaseWidget(t)

	return t
}

func (t *bodyText) CreateRenderer() fyne.WidgetRenderer {
	// Label.CreateRenderer builds the selection overlay, so it runs before one can
	// be found. Its own ExtendBaseWidget call is a no-op: the base already points here.
	renderer := t.Label.CreateRenderer()

	catcher := newSelectionCatcher(renderer.Objects(), t.onMenu)
	if catcher == nil {
		return renderer
	}

	return &bodyRenderer{WidgetRenderer: renderer, catcher: catcher}
}

// bodyRenderer is the Label's renderer with the catcher laid over it, last
// because the hit test keeps the last match in tree order.
type bodyRenderer struct {
	fyne.WidgetRenderer
	catcher *selectionCatcher
}

func (r *bodyRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	r.catcher.Resize(size)
}

func (r *bodyRenderer) Objects() []fyne.CanvasObject {
	inner := r.WidgetRenderer.Objects()
	objects := make([]fyne.CanvasObject, 0, len(inner)+1)

	return append(append(objects, inner...), r.catcher)
}

// selectionCatcher covers a Label's selection overlay, answering right-clicks
// itself and passing everything selection needs through. Transparent and not
// hoverable, so the message row underneath keeps its hover.
type selectionCatcher struct {
	widget.BaseWidget
	onMenu func(*fyne.PointEvent)

	/* The selection overlay, by the interfaces it answers */

	mouse  desktop.Mouseable
	drag   fyne.Draggable
	tap    fyne.Tappable
	double fyne.DoubleTappable
}

var (
	_ fyne.Tappable          = (*selectionCatcher)(nil)
	_ fyne.SecondaryTappable = (*selectionCatcher)(nil)
	_ fyne.DoubleTappable    = (*selectionCatcher)(nil)
	_ fyne.Draggable         = (*selectionCatcher)(nil)
	_ desktop.Mouseable      = (*selectionCatcher)(nil)
	_ desktop.Cursorable     = (*selectionCatcher)(nil)
)

// newSelectionCatcher finds the selection overlay among a Label renderer's
// objects, or nil when there is nothing to catch for — no menu, or a Fyne whose
// overlay no longer answers the interfaces the forwarding depends on. Nothing
// else in a Label's renderer is interactive, so answering all four identifies it.
func newSelectionCatcher(objects []fyne.CanvasObject, onMenu func(*fyne.PointEvent)) *selectionCatcher {
	if onMenu == nil {
		return nil
	}

	for _, object := range objects {
		mouse, isMouse := object.(desktop.Mouseable)
		drag, isDrag := object.(fyne.Draggable)
		tap, isTap := object.(fyne.Tappable)
		double, isDouble := object.(fyne.DoubleTappable)
		if !isMouse || !isDrag || !isTap || !isDouble {
			continue
		}

		c := &selectionCatcher{onMenu: onMenu, mouse: mouse, drag: drag, tap: tap, double: double}
		c.ExtendBaseWidget(c)

		return c
	}

	return nil
}

func (c *selectionCatcher) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

// TappedSecondary is the whole point: the message's menu, not the overlay's.
func (c *selectionCatcher) TappedSecondary(event *fyne.PointEvent) { c.onMenu(event) }

func (c *selectionCatcher) Tapped(event *fyne.PointEvent)       { c.tap.Tapped(event) }
func (c *selectionCatcher) DoubleTapped(event *fyne.PointEvent) { c.double.DoubleTapped(event) }
func (c *selectionCatcher) Dragged(event *fyne.DragEvent)       { c.drag.Dragged(event) }
func (c *selectionCatcher) DragEnd()                            { c.drag.DragEnd() }
func (c *selectionCatcher) MouseUp(event *desktop.MouseEvent)   { c.mouse.MouseUp(event) }

// MouseDown withholds the secondary button: the overlay takes keyboard focus on
// any press, and a right-click that opens a menu has no business taking it from
// the composer.
func (c *selectionCatcher) MouseDown(event *desktop.MouseEvent) {
	if event.Button == desktop.MouseButtonSecondary {
		return
	}

	c.mouse.MouseDown(event)
}

// Cursor keeps the I-beam the selection overlay would have shown.
func (c *selectionCatcher) Cursor() desktop.Cursor { return desktop.TextCursor }

// flatBody is a whole message body flattened to one uniform style — everything
// a selectable Label can express.
type flatBody struct {
	text  string
	style fyne.TextStyle
	size  fyne.ThemeSizeName // "" → standard text size
	dim   bool               // muted colour (subtext)
}

// flattenBlocks flattens a run of blocks into one styled string, reporting false
// when anything mixes styles or needs a custom visual. Blocks join with single
// newlines, matching the breaks RichText emits.
func flattenBlocks(blocks []markdown.Block) (flatBody, bool) {
	var f flatBody
	var b strings.Builder

	// merge folds a leaf's style into the document's: the first sets it, the rest
	// must agree.
	styled := false
	merge := func(style fyne.TextStyle, size fyne.ThemeSizeName, dim bool) bool {
		if !styled {
			f.style, f.size, f.dim, styled = style, size, dim, true
			return true
		}
		return f.style == style && f.size == size && f.dim == dim
	}

	for i, block := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		ok := true
		switch n := block.(type) {
		case *markdown.Paragraph:
			ok = flattenInlines(&b, n.Children, emphasis{}, "", false, merge)
		case *markdown.Heading:
			ok = flattenInlines(&b, n.Children, emphasis{bold: true}, headingSize(n.Level), false, merge)
		case *markdown.Subtext:
			ok = flattenInlines(&b, n.Children, emphasis{}, fynetheme.SizeNameCaptionText, true, merge)
		case *markdown.List:
			for j, item := range n.Items {
				if j > 0 {
					b.WriteByte('\n')
				}
				if !merge(fyne.TextStyle{}, "", false) {
					return f, false
				}
				b.WriteString(listMarker(n.Ordered, item))
				if !flattenInlines(&b, item.Children, emphasis{}, "", false, merge) {
					return f, false
				}
			}
		default: // Blockquote draws an indent bar — not flattenable
			ok = false
		}
		if !ok {
			return f, false
		}
	}

	f.text = b.String()
	return f, true
}

// flattenInlines appends the nodes' text to b, folding each leaf's style into
// merge. False on a node needing a custom visual, or on a style conflict.
func flattenInlines(b *strings.Builder, nodes []markdown.Inline, em emphasis, size fyne.ThemeSizeName, dim bool, merge func(fyne.TextStyle, fyne.ThemeSizeName, bool) bool) bool {
	for _, node := range nodes {
		switch n := node.(type) {
		case *markdown.Text:
			if !merge(em.textStyle(), size, dim) {
				return false
			}
			b.WriteString(n.Text)
		case *markdown.LineBreak:
			b.WriteByte('\n') // style-neutral
		case *markdown.Code:
			style := em.textStyle()
			style.Monospace = true
			if !merge(style, size, dim) {
				return false
			}
			b.WriteString(n.Text)
		case *markdown.Strong:
			next := em
			next.bold = true
			if !flattenInlines(b, n.Children, next, size, dim, merge) {
				return false
			}
		case *markdown.Emphasis:
			next := em
			next.italic = true
			if !flattenInlines(b, n.Children, next, size, dim, merge) {
				return false
			}
		default: // Underline, Strike, Spoiler, Link, mentions, emoji, timestamp: custom visuals
			return false
		}
	}
	return true
}

// emphasis accumulates character formatting as the renderer descends the tree.
type emphasis struct {
	bold, italic, underline, strike bool
}

func (e emphasis) textStyle() fyne.TextStyle {
	return fyne.TextStyle{Bold: e.bold, Italic: e.italic}
}

// mdBuilder accumulates RichText segments. It carries Deps because a mention is
// only an ID in the AST, and onMenu because a segment that answers a tap must
// answer a right-click with the message's menu (see mentionText).
type mdBuilder struct {
	deps   Deps
	onMenu func(*fyne.PointEvent)
	segs   []widget.RichTextSegment

	// reserve is the width of the widest mention word or emoji emitted — the gutter
	// renderMessageBody has to keep clear on the right. See mentionSegment.
	reserve float32
}

// text appends styled, inline content: a native TextSegment for a plain run,
// per-word decoratedSegments for anything struck, underlined or spoilered.
func (b *mdBuilder) text(s string, em emphasis, base widget.RichTextStyle, sp *spoilerState) {
	if s == "" {
		return
	}
	if em.strike || em.underline || sp != nil {
		b.decorated(s, em, base, sp)
		return
	}

	style := base
	style.Inline = true
	style.TextStyle = em.over(base)

	b.segs = append(b.segs, &widget.TextSegment{Text: s, Style: style})
}

// over is the emphasis a run carries laid over what its enclosing block already
// decided — a heading's bold and a code block's monospace are the block's.
func (e emphasis) over(base widget.RichTextStyle) fyne.TextStyle {
	style := e.textStyle()
	style.Bold = style.Bold || base.TextStyle.Bold
	style.Monospace = base.TextStyle.Monospace

	return style
}

// decorated splits a run into per-word segments separated by ordinary
// break-point spaces, each bridging its trailing space so the decoration joins
// the next word's.
func (b *mdBuilder) decorated(s string, em emphasis, base widget.RichTextStyle, sp *spoilerState) {
	ts := em.over(base)

	toks := splitTokens(s)
	words := 0
	for _, tok := range toks {
		if !tok.space {
			words++
		}
	}

	for i, tok := range toks {
		if tok.space {
			style := base
			style.Inline = true
			style.TextStyle = ts
			b.segs = append(b.segs, &widget.TextSegment{Text: tok.text, Style: style})
			continue
		}
		b.segs = append(b.segs, &decoratedSegment{
			text:      tok.text,
			style:     ts,
			colorName: base.ColorName,
			sizeName:  base.SizeName,
			strike:    em.strike,
			underline: em.underline,
			state:     sp,
			onMenu:    b.onMenu,
			bridge:    i+1 < len(toks) && toks[i+1].space,
			solo:      words == 1,
		})
	}
}

// lineBreak terminates the row with an empty, non-inline segment. base carries
// the size, so the break's row height matches the text around it.
func (b *mdBuilder) lineBreak(base widget.RichTextStyle) {
	style := base
	style.Inline = false
	b.segs = append(b.segs, &widget.TextSegment{Style: style})
}

// block renders one block over base, which carries what an enclosing block
// decided — a quote's muted colour being the only one today, and why base is a
// parameter rather than each case starting from zero.
func (b *mdBuilder) block(block markdown.Block, base widget.RichTextStyle) {
	switch n := block.(type) {
	case *markdown.Paragraph:
		b.inlines(n.Children, emphasis{}, base, nil)
		b.lineBreak(base)
	case *markdown.Heading:
		style := base
		style.SizeName = headingSize(n.Level)
		b.inlines(n.Children, emphasis{bold: true}, style, nil)
		b.lineBreak(style)
	case *markdown.Subtext:
		style := base
		style.SizeName = fynetheme.SizeNameCaptionText
		style.ColorName = fynetheme.ColorNamePlaceHolder
		b.inlines(n.Children, emphasis{}, style, nil)
		b.lineBreak(style)
	case *markdown.Blockquote:
		b.blockquote(n, base)
	case *markdown.CodeBlock:
		// A non-inline segment renders its multi-line text literally and separates
		// itself from what surrounds it.
		b.segs = append(b.segs, &widget.TextSegment{
			Text:  n.Text,
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}},
		})
	case *markdown.List:
		b.list(n, base)
	}
}

// quoteBar is the indent mark drawn at the start of every quoted row.
const quoteBar = "▏ "

// blockquote renders a quote's blocks with the bar at the start of every source
// line — a wrapped continuation gets none, RichText owning that wrapping.
//
// Blocks are built first and the bars spliced in after, because what ends a row
// is a block's own non-inline break and nothing before it knows where those land.
// A quote holding a heading or a list is thus the ordinary path with a prefix,
// and nested quotes stack their bars for free.
func (b *mdBuilder) blockquote(n *markdown.Blockquote, base widget.RichTextStyle) {
	base.ColorName = fynetheme.ColorNamePlaceHolder

	bar := func() widget.RichTextSegment {
		style := base
		style.Inline = true

		return &widget.TextSegment{Text: quoteBar, Style: style}
	}

	mark := len(b.segs)
	for _, block := range n.Blocks {
		b.block(block, base)
	}

	rows := b.segs[mark:]
	if len(rows) == 0 {
		b.segs = append(b.segs, bar())
		b.lineBreak(base)

		return
	}

	quoted := make([]widget.RichTextSegment, 0, len(rows)+len(rows)/2+1)
	quoted = append(quoted, bar())
	for i, seg := range rows {
		quoted = append(quoted, seg)
		if !seg.Inline() && i < len(rows)-1 {
			quoted = append(quoted, bar())
		}
	}

	b.segs = append(b.segs[:mark], quoted...)
}

func (b *mdBuilder) inlines(nodes []markdown.Inline, em emphasis, base widget.RichTextStyle, sp *spoilerState) {
	for _, node := range nodes {
		switch n := node.(type) {
		case *markdown.Text:
			b.text(n.Text, em, base, sp)
		case *markdown.LineBreak:
			b.lineBreak(base)
		case *markdown.Strong:
			next := em
			next.bold = true
			b.inlines(n.Children, next, base, sp)
		case *markdown.Emphasis:
			next := em
			next.italic = true
			b.inlines(n.Children, next, base, sp)
		case *markdown.Underline:
			next := em
			next.underline = true
			b.inlines(n.Children, next, base, sp)
		case *markdown.Strike:
			next := em
			next.strike = true
			b.inlines(n.Children, next, base, sp)
		case *markdown.Spoiler:
			b.inlines(n.Children, em, base, &spoilerState{})
		case *markdown.Code:
			style := base
			style.Inline = true
			style.TextStyle = fyne.TextStyle{Monospace: true, Bold: em.bold, Italic: em.italic}
			b.segs = append(b.segs, &widget.TextSegment{Text: n.Text, Style: style})
		case *markdown.Link:
			u, _ := url.Parse(n.URL)
			b.segs = append(b.segs, &widget.HyperlinkSegment{Text: markdown.PlainText(n.Children), URL: u})
		case *markdown.UserMention:
			userID := n.UserID
			b.mention("@"+mentionName(b.deps.Store.UserName(userID)), em, base, func(anchor fyne.CanvasObject) {
				b.deps.Actions.OnUserTapped(userID, anchor)
			})
		case *markdown.ChannelMention:
			channelID := n.ChannelID
			b.mention("#"+mentionName(b.deps.Store.ChannelName(channelID)), em, base, func(fyne.CanvasObject) {
				b.deps.Actions.OnChannelTapped(channelID)
			})
		case *markdown.Emoji:
			b.emoji(n.EmojiID, base)
		case *markdown.Timestamp:
			// Drawn as a mention and for the same reason: a fact the client resolved
			// rather than something the author typed, so it stands apart from the
			// sentence. It opens nothing — an instant leads nowhere — hence the nil tap.
			b.mention(util.MessageTimestamp(n.Time, n.Style), em, base, nil)
		}
	}
}

// mention renders an already-marked "@Name" or "#channel" as bold accent text
// opening what it names. A nil onTap draws the highlight with nothing behind it,
// which is what a rendered timestamp is.
//
// Inline text rather than the tinted pill other clients use: a pill needs a
// background bleeding behind the row's line spacing, which RichText gives a
// segment no way to do without colliding on wrapped lines. The words are emitted
// separately, as a decorated span's are — a segment is atomic to RichText, so a
// two-word name kept whole could not wrap.
func (b *mdBuilder) mention(text string, em emphasis, base widget.RichTextStyle, onTap func(anchor fyne.CanvasObject)) {
	style := fyne.TextStyle{Bold: true, Italic: em.italic, Monospace: base.TextStyle.Monospace}

	size := mentionSize(base.SizeName)

	for _, tok := range splitTokens(text) {
		if tok.space {
			spacer := base
			spacer.Inline = true
			spacer.TextStyle = style
			b.segs = append(b.segs, &widget.TextSegment{Text: tok.text, Style: spacer})
			continue
		}

		b.reserve = max(b.reserve, fyne.MeasureText(tok.text, size, style).Width)
		b.segs = append(b.segs, &mentionSegment{
			text:     tok.text,
			style:    style,
			sizeName: base.SizeName,
			onTap:    onTap,
			onMenu:   b.onMenu,
		})
	}
}

// mentionName is what an unresolved target says: "unknown" rather than the raw
// ID, which is noise wherever it lands. Lazy resolution fills a user in shortly;
// a channel the account cannot see never resolves, and reads as one it cannot see.
func mentionName(resolved string) string {
	if resolved == "" {
		return "unknown"
	}

	return resolved
}

// mentionSegment is one word of a rendered mention. A native TextSegment carries
// the colour but not the tap, so the word is a widget — the same trade
// decoratedSegment makes, at the same cost: RichText can neither break such a
// segment nor break before it, which is what mdBuilder.reserve answers.
type mentionSegment struct {
	text     string
	style    fyne.TextStyle
	sizeName fyne.ThemeSizeName // "" → standard text size
	onTap    func(anchor fyne.CanvasObject)
	onMenu   func(*fyne.PointEvent)
}

var _ widget.RichTextSegment = (*mentionSegment)(nil)

func (s *mentionSegment) Inline() bool              { return true }
func (s *mentionSegment) Textual() string           { return s.text }
func (s *mentionSegment) Select(_, _ fyne.Position) {}
func (s *mentionSegment) SelectedText() string      { return "" }
func (s *mentionSegment) Unselect()                 {}

func (s *mentionSegment) Visual() fyne.CanvasObject {
	return newMentionText(s.text, mentionSize(s.sizeName), s.style, s.onTap, s.onMenu)
}

// mentionSize is whatever the text around it is drawn at — a mention inside a
// heading is a heading.
func mentionSize(name fyne.ThemeSizeName) float32 {
	if name == "" {
		name = fynetheme.SizeNameText
	}

	return fynetheme.Size(name)
}

func (s *mentionSegment) Update(o fyne.CanvasObject) {
	if v, ok := o.(*mentionText); ok {
		v.SetText(s.text)
	}
}

// mentionText is a rendered mention: accent text opening the profile or channel
// it names. The size is given rather than read because a system line mounts one
// at its own.
//
// It answers a right-click with the menu of the message it sits in. The driver
// gives a click to the innermost object accepting one and does not walk back up
// when that object has no answer for the button, so a tappable word without the
// menu would be a hole in the row.
type mentionText struct {
	tapBase
	textObj *canvas.Text
}

var (
	_ fyne.Widget            = (*mentionText)(nil)
	_ fyne.Tappable          = (*mentionText)(nil)
	_ fyne.SecondaryTappable = (*mentionText)(nil)
	_ desktop.Cursorable     = (*mentionText)(nil)
)

func newMentionText(text string, size float32, style fyne.TextStyle, onTap func(anchor fyne.CanvasObject), onMenu func(*fyne.PointEvent)) *mentionText {
	w := &mentionText{textObj: newText(text, theme.Colors.MentionText, size)}
	w.textObj.TextStyle = style

	// The profile card anchors on the word, so the tap hands back the widget itself;
	// tapBase's handler takes no argument, hence the closure.
	if onTap != nil {
		w.onTap = func() { onTap(w) }
	}
	w.onSecondaryTap = onMenu
	w.ExtendBaseWidget(w)

	return w
}

// SetText rewrites the name, for a target that resolves after the line is
// mounted. The word beside it moves, so the caller relayouts what holds them.
func (w *mentionText) SetText(text string) {
	if w.textObj.Text == text {
		return
	}

	w.textObj.Text = text
	w.Refresh()
}

func (w *mentionText) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.textObj)
}

// Cursor keeps the hand for a mention that opens something and the ordinary
// pointer for one that does not: a timestamp is drawn as a highlight but leads
// nowhere, and a hand would promise a click that does nothing.
func (w *mentionText) Cursor() desktop.Cursor {
	if w.onTap == nil {
		return desktop.DefaultCursor
	}

	return desktop.PointerCursor
}

/* Custom emoji */

// emoji renders a :ULID: as the picture it names. The URL is derived from the ID
// rather than looked up — see domain.Store.EmojiURL — so an emoji from a server
// the account is not in draws like any other.
func (b *mdBuilder) emoji(emojiID string, base widget.RichTextStyle) {
	url := b.deps.Store.EmojiURL(emojiID)
	if url == "" {
		return
	}

	side := emojiSide(base.SizeName)
	b.reserve = max(b.reserve, side)
	b.segs = append(b.segs, &emojiSegment{id: emojiID, url: url, side: side, images: b.deps.Emojis})
}

// emojiSide is the square an emoji is drawn in: one line of the text around it,
// so one inside a heading is heading-sized as a mention is.
//
// Measured rather than named, and not only for proportion. RichText
// baseline-aligns a row as soon as its objects differ in height, reading the
// baseline of a segment it cannot measure as text as zero — so an emoji a pixel
// taller is moved *down* a whole baseline and draws through the line below.
// Measured here rather than through lineHeight's memo, which is keyed by size
// alone and so may answer from a font no longer installed.
func emojiSide(sizeName fyne.ThemeSizeName) float32 {
	return fyne.MeasureText("M", mentionSize(sizeName), fyne.TextStyle{}).Height
}

// emojiSegment is one custom emoji in a body — like a mention, a segment RichText
// cannot read as text, hence the reserve above. Unlike a mention it draws nothing
// interactive, so the hover and menu stay with the row underneath, the same trade
// an embed card makes.
type emojiSegment struct {
	id     string
	url    string
	side   float32
	images *cache.ImageCache
}

var _ widget.RichTextSegment = (*emojiSegment)(nil)

func (s *emojiSegment) Inline() bool              { return true }
func (s *emojiSegment) Select(_, _ fyne.Position) {}
func (s *emojiSegment) SelectedText() string      { return "" }
func (s *emojiSegment) Unselect()                 {}
func (s *emojiSegment) Update(fyne.CanvasObject)  {}

// Textual is empty because the picture is the whole of it: RichText measures this
// segment by its visual rather than by its text.
func (s *emojiSegment) Textual() string { return "" }

// Visual is the square the picture lands in, reserved before the load starts so
// an emoji arriving repaints its own cell rather than re-flowing the line.
func (s *emojiSegment) Visual() fyne.CanvasObject {
	size := fyne.NewSize(s.side, s.side)
	frame := container.NewGridWrap(size, canvas.NewRectangle(color.Transparent))
	s.images.LoadIntoContainer(s.id, s.url, size, frame, false, nil)

	return frame
}

func (b *mdBuilder) list(n *markdown.List, base widget.RichTextStyle) {
	for _, item := range n.Items {
		b.text(listMarker(n.Ordered, item), emphasis{}, base, nil)
		b.inlines(item.Children, emphasis{}, base, nil)
		b.lineBreak(base)
	}
}

// listMarker is an item's indent and bullet or number, the one run of text that
// opens its row. Spaces rather than a layout: the only thing that can push a
// RichText row in is its first segment.
func listMarker(ordered bool, item markdown.ListItem) string {
	indent := strings.Repeat("   ", item.Indent)
	if ordered {
		return fmt.Sprintf("%s%d.  ", indent, item.Number)
	}

	return indent + "•  "
}

// token is a run of either whitespace or non-whitespace from a split span.
type token struct {
	text  string
	space bool
}

// splitTokens splits s into alternating whitespace / non-whitespace runs,
// preserving every character. The runs are slices of s rather than copies, and
// the walk is in place: this runs over every text run of every rendered body.
func splitTokens(s string) []token {
	if s == "" {
		return nil
	}

	var tokens []token
	start, space := 0, false
	for i, r := range s {
		switch isSpace := unicode.IsSpace(r); {
		case i == 0:
			space = isSpace
		case isSpace != space:
			tokens = append(tokens, token{text: s[start:i], space: space})
			start, space = i, isSpace
		}
	}

	return append(tokens, token{text: s[start:], space: space})
}

// headingSize maps a header level to the nearest themed size: three levels onto
// Fyne's two heading sizes plus the body size.
func headingSize(level int) fyne.ThemeSizeName {
	switch level {
	case 1:
		return fynetheme.SizeNameHeadingText
	case 2:
		return fynetheme.SizeNameSubHeadingText
	default:
		return fynetheme.SizeNameText
	}
}

// spoilerState is shared by every word of a single spoiler span so tapping any
// word reveals (or re-hides) the whole span at once.
type spoilerState struct {
	revealed bool
	covers   []*canvas.Rectangle
}

func (s *spoilerState) add(c *canvas.Rectangle) {
	s.covers = append(s.covers, c)
	if s.revealed {
		c.Hide()
	}
}

func (s *spoilerState) toggle() {
	s.revealed = !s.revealed
	for _, c := range s.covers {
		if s.revealed {
			c.Hide()
		} else {
			c.Show()
		}
		c.Refresh()
	}
}

// decoratedSegment is one word of a strike / underline / spoiler span. Per-word
// is what lets the span wrap; the shared spoilerState keeps reveal atomic.
type decoratedSegment struct {
	text      string
	style     fyne.TextStyle
	colorName fyne.ThemeColorName // "" → foreground
	sizeName  fyne.ThemeSizeName  // "" → standard text size
	strike    bool
	underline bool
	state     *spoilerState
	onMenu    func(*fyne.PointEvent) // the owning message's menu — see mentionText
	bridge    bool                   // extend the decoration over the following space
	solo      bool                   // the only word of its span (round the spoiler cover)
}

var _ widget.RichTextSegment = (*decoratedSegment)(nil)

func (s *decoratedSegment) Inline() bool              { return true }
func (s *decoratedSegment) Textual() string           { return s.text }
func (s *decoratedSegment) Visual() fyne.CanvasObject { return newDecoratedText(s) }
func (s *decoratedSegment) Select(_, _ fyne.Position) {}
func (s *decoratedSegment) SelectedText() string      { return "" }
func (s *decoratedSegment) Unselect()                 {}
func (s *decoratedSegment) Update(o fyne.CanvasObject) {
	if v, ok := o.(*decoratedText); ok {
		v.apply(s)
		v.Refresh()
	}
}

// decoratedText draws one word with any combination of strike line, underline and
// tappable spoiler cover, at its intrinsic width whatever size RichText gives it —
// so a decoration never stretches to fill a row. bridge extends it one space to
// meet the next word.
type decoratedText struct {
	widget.BaseWidget
	colorName  fyne.ThemeColorName
	sizeName   fyne.ThemeSizeName
	bridge     bool
	state      *spoilerState
	onMenu     func(*fyne.PointEvent)
	textObj    *canvas.Text
	strikeLine *canvas.Line
	underLine  *canvas.Line
	cover      *canvas.Rectangle
}

var (
	_ fyne.Widget            = (*decoratedText)(nil)
	_ fyne.Tappable          = (*decoratedText)(nil)
	_ fyne.SecondaryTappable = (*decoratedText)(nil)
	_ desktop.Cursorable     = (*decoratedText)(nil)
)

func newDecoratedText(seg *decoratedSegment) *decoratedText {
	w := &decoratedText{
		textObj: canvas.NewText(seg.text, color.Transparent),
		state:   seg.state,
		onMenu:  seg.onMenu,
	}
	if seg.strike {
		w.strikeLine = canvas.NewLine(color.Transparent)
		w.strikeLine.StrokeWidth = 1
	}
	if seg.underline {
		w.underLine = canvas.NewLine(color.Transparent)
		w.underLine.StrokeWidth = 1
	}
	if seg.state != nil {
		w.cover = canvas.NewRectangle(theme.Colors.SwiftActionBg)
		// A lone word rounds into a pill; multi-word covers stay square so bridged
		// words form one bar rather than pinching at every boundary.
		if seg.solo {
			w.cover.CornerRadius = 3
		}
		seg.state.add(w.cover)
	}
	w.apply(seg)
	w.ExtendBaseWidget(w)
	return w
}

// apply copies a segment's styling onto the widget. Which decorations exist is
// fixed per segment, so only text and styling move.
func (w *decoratedText) apply(seg *decoratedSegment) {
	w.colorName = seg.colorName
	w.sizeName = seg.sizeName
	w.bridge = seg.bridge
	w.textObj.Text = seg.text
	w.textObj.TextStyle = seg.style
}

func (w *decoratedText) color() color.Color {
	name := w.colorName
	if name == "" {
		name = fynetheme.ColorNameForeground
	}
	return fynetheme.ColorForWidget(name, w)
}

func (w *decoratedText) textSize() float32 {
	name := w.sizeName
	if name == "" {
		name = fynetheme.SizeNameText
	}
	return fynetheme.SizeForWidget(name, w)
}

func (w *decoratedText) MinSize() fyne.Size {
	w.textObj.TextSize = w.textSize()
	return w.textObj.MinSize()
}

func (w *decoratedText) Tapped(*fyne.PointEvent) {
	if w.state != nil {
		w.state.toggle()
	}
}

// TappedSecondary hands the right-click to the message, as mentionText does: a
// word that accepts a click and has no answer for this one swallows it.
func (w *decoratedText) TappedSecondary(event *fyne.PointEvent) {
	if w.onMenu != nil {
		w.onMenu(event)
	}
}

func (w *decoratedText) Cursor() desktop.Cursor {
	if w.state != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (w *decoratedText) CreateRenderer() fyne.WidgetRenderer {
	// Text first, then decorations, then the cover last so it hides everything
	// beneath until revealed. Which decorations exist is fixed per segment, so the
	// list is composed once rather than on every paint.
	objects := []fyne.CanvasObject{w.textObj}
	if w.strikeLine != nil {
		objects = append(objects, w.strikeLine)
	}
	if w.underLine != nil {
		objects = append(objects, w.underLine)
	}
	if w.cover != nil {
		objects = append(objects, w.cover)
	}

	return &decoratedRenderer{w: w, objects: objects}
}

type decoratedRenderer struct {
	w       *decoratedText
	objects []fyne.CanvasObject
}

func (r *decoratedRenderer) Layout(fyne.Size) {
	w := r.w
	size := w.textSize()
	col := w.color()

	w.textObj.TextSize = size
	w.textObj.Color = col
	min := w.textObj.MinSize()
	w.textObj.Resize(min)
	w.textObj.Move(fyne.NewPos(0, 0))

	extent := min.Width
	if w.bridge {
		// The break-point space plus 1px, so no sub-pixel seam shows between two
		// adjacent decorations.
		extent += spaceWidth(size, w.textObj.TextStyle) + 1
	}

	if w.strikeLine != nil {
		w.strikeLine.StrokeColor = col
		y := min.Height * 0.5
		w.strikeLine.Position1 = fyne.NewPos(0, y)
		w.strikeLine.Position2 = fyne.NewPos(extent, y)
	}
	if w.underLine != nil {
		w.underLine.StrokeColor = col
		y := min.Height * 0.84
		w.underLine.Position1 = fyne.NewPos(0, y)
		w.underLine.Position2 = fyne.NewPos(extent, y)
	}
	if w.cover != nil {
		w.cover.Resize(fyne.NewSize(extent, min.Height))
		w.cover.Move(fyne.NewPos(0, 0))
	}
}

// spaceWidths memoises one space's width per size and style, so Layout does not
// re-measure for every bridged word on every pass. UI thread only.
var spaceWidths = map[spaceKey]float32{}

type spaceKey struct {
	size  float32
	style fyne.TextStyle
}

func spaceWidth(size float32, style fyne.TextStyle) float32 {
	key := spaceKey{size: size, style: style}
	w, ok := spaceWidths[key]
	if !ok {
		w = fyne.MeasureText(" ", size, style).Width
		spaceWidths[key] = w
	}
	return w
}

func (r *decoratedRenderer) MinSize() fyne.Size { return r.w.MinSize() }

func (r *decoratedRenderer) Refresh() {
	r.Layout(r.w.Size())
	for _, object := range r.objects {
		canvas.Refresh(object)
	}
}

func (r *decoratedRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *decoratedRenderer) Destroy() {}
