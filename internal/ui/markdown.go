package ui

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
)

// renderMessageBody renders a message's body. A body whose whole content shares
// one uniform style flattens to a selectable Label, so its text can be selected
// with the mouse; only genuinely mixed-style bodies fall back to a RichText,
// which Fyne cannot make selectable (its selection machinery is unexported and
// assumes a single uniform style).
//
// Two RichText constraints shape the rest of this file. It only wraps and flows
// native segments when every content segment is marked Inline, so content is
// emitted inline and each line is terminated by an empty non-inline segment
// acting as a break (see mdBuilder.lineBreak). And strike, underline and spoilers
// have no native equivalent, so decoratedSegment draws them — split per word,
// since RichText only breaks rows at text spaces, never between two custom
// segments.
//
// A body carrying a mention — of a person or of a channel — is never flattened:
// the mention has its own colour, exactly the mixed-style case a Label cannot
// express.
//
// onMenu is the owning message's right-click handler, which a selectable body
// has to be given explicitly — see bodyText.
func renderMessageBody(deps Deps, text string, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	doc := markdown.Parse(text)

	// An empty body (attachment-only message) keeps the zero-height RichText; a
	// Label would reserve a blank text line above the attachment.
	if flat, ok := flattenDocument(doc); ok && flat.text != "" {
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
	for _, block := range doc.Blocks {
		b.block(block)
	}

	rt := widget.NewRichText(b.segs...)
	rt.Wrapping = fyne.TextWrapWord
	if b.reserve == 0 {
		return rt
	}

	// RichText never breaks a row *before* a segment it cannot measure as text: a
	// mention is appended to the row in hand however little of it is left, so one
	// landing at a line end draws past the right edge and is cut off by the message
	// column. Narrowing the text by the widest mention the body carries is what
	// gives that overhang somewhere to land — the words wrap earlier, and the
	// mention that follows them spills into the strip kept clear for it.
	return NewFillRow(0, rt, HorizontalSpacer(b.reserve))
}

/* Selectable body */

// bodyText is the flattened, selectable message body.
//
// It exists to get a right-click back. A selectable Label mounts an invisible
// selection overlay above its text, and the driver delivers pointer events to
// the *innermost* object under the cursor — so that overlay takes the click and
// answers it with Fyne's own one-item "Copy" menu instead of the message's
// context menu, having first pulled keyboard focus off the composer. Neither is
// reachable from outside the widget: the overlay is unexported, and its
// behaviour is not configurable.
//
// So the renderer mounts a catcher above it, and the overlay — which the Label's
// own renderer hands over as a plain fyne.CanvasObject — is driven through the
// exported interfaces it satisfies. A Fyne that stops exposing it leaves the
// catcher out entirely (newSelectionCatcher returns nil) and the body degrades to
// an ordinary selectable Label.
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
	// Label.CreateRenderer is what builds the selection overlay, so it has to run
	// before one can be found. Its own ExtendBaseWidget call is a no-op here: the
	// base widget already points at us.
	renderer := t.Label.CreateRenderer()

	catcher := newSelectionCatcher(renderer.Objects(), t.onMenu)
	if catcher == nil {
		return renderer
	}

	return &bodyRenderer{WidgetRenderer: renderer, catcher: catcher}
}

// bodyRenderer is the Label's own renderer with the catcher laid over it. The
// catcher goes last because the driver's hit test keeps the last match in tree
// order, which is what puts it in front of the selection overlay.
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
// itself and passing everything selection needs straight through. It is
// transparent and not hoverable, so the message row underneath still lights up
// and still owns the pointer for everything else.
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
// objects and builds a catcher for it, or returns nil when there is nothing to
// catch for — an unselectable label, no menu to show, or a Fyne whose overlay no
// longer answers the interfaces the forwarding depends on. Nothing else in a
// Label's renderer is interactive, so answering all four identifies it.
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

// MouseDown withholds the secondary button. The overlay takes keyboard focus on
// any press, and a right-click that only opens a menu has no business pulling
// focus out of the composer.
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

// flattenDocument tries to flatten a document into a single styled string,
// reporting false when blocks or inlines mix styles or need custom visuals.
// Blocks join with single newlines, matching the breaks RichText emits.
func flattenDocument(doc *markdown.Document) (flatBody, bool) {
	var f flatBody
	var b strings.Builder

	// merge folds one leaf's effective style into the document style: the first
	// leaf sets it, every later leaf must agree.
	styled := false
	merge := func(style fyne.TextStyle, size fyne.ThemeSizeName, dim bool) bool {
		if !styled {
			f.style, f.size, f.dim, styled = style, size, dim, true
			return true
		}
		return f.style == style && f.size == size && f.dim == dim
	}

	for i, block := range doc.Blocks {
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
		case *markdown.CodeBlock:
			ok = merge(fyne.TextStyle{Monospace: true}, "", false)
			b.WriteString(n.Text)
		case *markdown.List:
			for j, item := range n.Items {
				if j > 0 {
					b.WriteByte('\n')
				}
				marker := "•  "
				if n.Ordered {
					marker = fmt.Sprintf("%d.  ", n.Start+j)
				}
				if !merge(fyne.TextStyle{}, "", false) {
					return f, false
				}
				b.WriteString(marker)
				if !flattenInlines(&b, item, emphasis{}, "", false, merge) {
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

// flattenInlines appends the nodes' text to b, folding each leaf's effective
// style into merge. It reports false on nodes needing custom visuals, or when a
// leaf's style conflicts with those seen so far.
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
		default: // Underline, Strike, Spoiler, Link and both mentions need custom visuals
			return false
		}
	}
	return true
}

// emphasis accumulates inline character formatting as the renderer descends the
// inline tree.
type emphasis struct {
	bold, italic, underline, strike bool
}

func (e emphasis) textStyle() fyne.TextStyle {
	return fyne.TextStyle{Bold: e.bold, Italic: e.italic}
}

// mdBuilder accumulates RichText segments. It carries Deps because one inline
// node — the mention — is only an ID in the AST and needs the session to resolve
// a name, and onMenu because the segments that answer a tap have to answer a
// right-click with the message's own menu (see mentionText).
type mdBuilder struct {
	deps   Deps
	onMenu func(*fyne.PointEvent)
	segs   []widget.RichTextSegment

	// reserve is the width of the widest mention word emitted — the gutter
	// renderMessageBody has to keep clear on the right. See mentionSegment.
	reserve float32
}

// text appends styled, inline content. Plain runs become native TextSegments;
// runs that are struck, underlined or inside a spoiler become per-word
// decoratedSegments so they wrap like ordinary words.
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
	ts := em.textStyle()
	ts.Bold = ts.Bold || base.TextStyle.Bold
	ts.Monospace = base.TextStyle.Monospace
	style.TextStyle = ts

	b.segs = append(b.segs, &widget.TextSegment{Text: s, Style: style})
}

// decorated splits a decorated run into per-word custom segments separated by
// ordinary (break-point) spaces. Each word bridges the trailing space so its
// line/cover joins the next word's.
func (b *mdBuilder) decorated(s string, em emphasis, base widget.RichTextStyle, sp *spoilerState) {
	ts := em.textStyle()
	ts.Bold = ts.Bold || base.TextStyle.Bold
	ts.Monospace = base.TextStyle.Monospace

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

// lineBreak terminates the current row with an empty, non-inline segment so the
// next content starts on a fresh line. base carries the size, so the break's row
// height matches the surrounding text.
func (b *mdBuilder) lineBreak(base widget.RichTextStyle) {
	style := base
	style.Inline = false
	b.segs = append(b.segs, &widget.TextSegment{Style: style})
}

func (b *mdBuilder) block(block markdown.Block) {
	switch n := block.(type) {
	case *markdown.Paragraph:
		base := widget.RichTextStyle{}
		b.inlines(n.Children, emphasis{}, base, nil)
		b.lineBreak(base)
	case *markdown.Heading:
		base := widget.RichTextStyle{SizeName: headingSize(n.Level)}
		b.inlines(n.Children, emphasis{bold: true}, base, nil)
		b.lineBreak(base)
	case *markdown.Subtext:
		base := widget.RichTextStyle{
			SizeName:  fynetheme.SizeNameCaptionText,
			ColorName: fynetheme.ColorNamePlaceHolder,
		}
		b.inlines(n.Children, emphasis{}, base, nil)
		b.lineBreak(base)
	case *markdown.Blockquote:
		b.blockquote(n)
	case *markdown.CodeBlock:
		// A non-inline block segment renders its multi-line text literally and
		// separates itself from surrounding content.
		b.segs = append(b.segs, &widget.TextSegment{
			Text:  n.Text,
			Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}},
		})
	case *markdown.List:
		b.list(n)
	}
}

// blockquote renders a quote with the indent bar repeated at the start of every
// source line (continuation of wrapped lines is not bar-prefixed — RichText owns
// that wrapping).
func (b *mdBuilder) blockquote(n *markdown.Blockquote) {
	base := widget.RichTextStyle{ColorName: fynetheme.ColorNamePlaceHolder}
	b.text("▏ ", emphasis{}, base, nil)
	for _, child := range n.Children {
		if _, ok := child.(*markdown.LineBreak); ok {
			b.lineBreak(base)
			b.text("▏ ", emphasis{}, base, nil)
			continue
		}
		b.inlines([]markdown.Inline{child}, emphasis{}, base, nil)
	}
	b.lineBreak(base)
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
		}
	}
}

// mention renders an already-marked "@Name" or "#channel" as bold, accent-
// coloured text that opens what it names when tapped.
//
// It is inline text rather than the tinted pill other clients use: a pill needs
// a background bleeding behind the row's line spacing, and RichText gives a
// segment no way to do that without colliding on wrapped lines.
//
// The words are emitted separately, as a decorated span's are: a segment is
// atomic to RichText, which breaks a row only at a space *between* segments, so
// a two-word name kept whole could not wrap. Each word carries the same tap.
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

// mentionName is what a mention says for a target the store cannot resolve:
// "unknown" rather than the raw ID, which is noise wherever it lands. Lazy
// author resolution fills a user in shortly and re-renders the message; a
// channel the account cannot see never resolves, and reads as one it cannot see.
func mentionName(resolved string) string {
	if resolved == "" {
		return "unknown"
	}

	return resolved
}

// mentionSegment is one word of a rendered mention. A native TextSegment can
// carry the colour but not the tap, so the word is a widget — the same trade
// decoratedSegment makes for a decoration RichText cannot draw, and it costs the
// same thing: RichText measures a segment it cannot read as text only to subtract
// it, so a word here can neither break nor be broken before. Splitting per word
// is what lets a two-word name wrap at all, and mdBuilder.reserve is what keeps
// the one that lands at a line end from being cut off.
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

// mentionSize resolves the size a mention is drawn at, which is whatever the text
// around it is drawn at — a mention inside a heading is a heading.
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

// mentionText is a rendered mention drawn as a widget: accent-coloured text that
// opens the profile or the channel it names. A system line uses one for the name
// it announces, which is why the size is given rather than read — that line is
// drawn at its own.
//
// It answers a right-click with the menu of the message it sits in. The driver
// hands a click to the innermost object accepting one and does not walk back up
// when that object has no answer for the button, so a tappable word in a message
// row that did not carry the menu would be a hole in it.
type mentionText struct {
	widget.BaseWidget
	textObj *canvas.Text
	onTap   func(anchor fyne.CanvasObject)
	onMenu  func(*fyne.PointEvent)
}

var (
	_ fyne.Widget            = (*mentionText)(nil)
	_ fyne.Tappable          = (*mentionText)(nil)
	_ fyne.SecondaryTappable = (*mentionText)(nil)
	_ desktop.Cursorable     = (*mentionText)(nil)
)

func newMentionText(text string, size float32, style fyne.TextStyle, onTap func(anchor fyne.CanvasObject), onMenu func(*fyne.PointEvent)) *mentionText {
	w := &mentionText{
		textObj: canvas.NewText(text, theme.Colors.MentionText),
		onTap:   onTap,
		onMenu:  onMenu,
	}
	w.textObj.TextSize = size
	w.textObj.TextStyle = style
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

func (w *mentionText) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap(w)
	}
}

func (w *mentionText) TappedSecondary(event *fyne.PointEvent) {
	if w.onMenu != nil {
		w.onMenu(event)
	}
}

func (w *mentionText) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (w *mentionText) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.textObj)
}

func (b *mdBuilder) list(n *markdown.List) {
	base := widget.RichTextStyle{}
	for i, item := range n.Items {
		marker := "•  "
		if n.Ordered {
			marker = fmt.Sprintf("%d.  ", n.Start+i)
		}
		b.text(marker, emphasis{}, base, nil)
		b.inlines(item, emphasis{}, base, nil)
		b.lineBreak(base)
	}
}

// token is a run of either whitespace or non-whitespace from a split span.
type token struct {
	text  string
	space bool
}

// splitTokens splits s into alternating whitespace / non-whitespace runs,
// preserving all characters.
func splitTokens(s string) []token {
	var toks []token
	r := []rune(s)
	for i := 0; i < len(r); {
		space := unicode.IsSpace(r[i])
		j := i
		for j < len(r) && unicode.IsSpace(r[j]) == space {
			j++
		}
		toks = append(toks, token{text: string(r[i:j]), space: space})
		i = j
	}
	return toks
}

// headingSize maps a header level to the nearest themed text size. Discord has
// three header levels; Fyne offers two heading sizes plus the body size.
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

// decoratedSegment is one word of a strikethrough / underline / spoiler span.
// Word-level granularity lets the span wrap; the shared spoilerState (when set)
// keeps reveal atomic across the whole span.
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

// decoratedText draws one word with any combination of a strike line, an
// underline and a tappable spoiler cover. It draws at its intrinsic text width
// regardless of the size RichText gives it, so decorations never stretch to fill
// a row; the optional bridge extends them by one space to meet the next word.
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
		// Round a lone spoiler word into a pill; multi-word spoilers use square
		// covers so adjacent (bridged) words form one continuous bar instead of
		// pinching into notches at every word boundary.
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
// fixed per segment, so only text and styling are updated here.
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

// TappedSecondary hands the right-click to the message, for the same reason
// mentionText does: a word that accepts a click and has no answer for this one
// swallows it where it stands.
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
	return &decoratedRenderer{w: w}
}

type decoratedRenderer struct{ w *decoratedText }

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
		// Bridge the break-point space to the next word, plus 1px of overlap so
		// no sub-pixel seam shows between adjacent decorations.
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

// spaceWidths memoises the measured width of a single space per size and style,
// so Layout doesn't re-measure for every bridged word on every pass. UI thread
// only, hence unsynchronised.
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
	for _, o := range r.Objects() {
		canvas.Refresh(o)
	}
}

// Objects lists the text first, then decorations, then the cover last so the
// cover sits on top and hides everything beneath it until revealed.
func (r *decoratedRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.w.textObj}
	if r.w.strikeLine != nil {
		objs = append(objs, r.w.strikeLine)
	}
	if r.w.underLine != nil {
		objs = append(objs, r.w.underLine)
	}
	if r.w.cover != nil {
		objs = append(objs, r.w.cover)
	}
	return objs
}

func (r *decoratedRenderer) Destroy() {}
