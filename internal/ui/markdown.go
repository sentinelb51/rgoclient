package ui

import (
	"fmt"
	"image/color"
	"net/url"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/internal/markdown"
	"RGOClient/internal/ui/theme"
)

// renderMessageContent renders a message as a RichText: a bold author line
// followed by the markdown-formatted body.
//
// Fyne's RichText wraps and flows native segments (text, bold/italic, links)
// only when every content segment is marked Inline; a non-inline segment is
// isolated on its own row and forces the next onto a fresh one. So every piece
// of content is emitted inline and each block/line is terminated by an empty
// non-inline segment that acts as a single-line break (see mdBuilder.lineBreak).
//
// Strikethrough, underline (Fyne's TextStyle.Underline is not rendered) and
// spoilers have no usable native equivalent, so they are drawn by a custom
// decoratedSegment. To wrap like ordinary words, each such span is split into
// per-word segments interleaved with normal spaces (RichText only breaks rows at
// text spaces, never between two custom segments); each word's decoration bridges
// the following space so the line/cover still reads as continuous.
func renderMessageContent(name, text string) *widget.RichText {
	b := &mdBuilder{}
	b.text(name, emphasis{bold: true}, widget.RichTextStyle{}, nil)
	b.lineBreak(widget.RichTextStyle{})
	for _, block := range markdown.Parse(text).Blocks {
		b.block(block)
	}

	rt := widget.NewRichText(b.segs...)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// emphasis accumulates inline character formatting as the renderer descends the
// inline tree.
type emphasis struct {
	bold, italic, underline, strike bool
}

func (e emphasis) textStyle() fyne.TextStyle {
	return fyne.TextStyle{Bold: e.bold, Italic: e.italic}
}

// mdBuilder accumulates RichText segments.
type mdBuilder struct {
	segs []widget.RichTextSegment
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
			bridge:    i+1 < len(toks) && toks[i+1].space,
			solo:      words == 1,
		})
	}
}

// lineBreak terminates the current row with an empty, non-inline segment so the
// next content starts on a fresh line. base carries the size so the break's row
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
		}
	}
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
	bridge    bool // extend the decoration over the following space
	solo      bool // the only word of its span (round the spoiler cover)
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

// decoratedText draws a single word with any combination of a strike line, an
// underline and a tappable spoiler cover. It draws at its intrinsic (text) width
// regardless of the size RichText gives it, so decorations never stretch to fill
// a row; the optional bridge extends them by one space to meet the next word.
type decoratedText struct {
	widget.BaseWidget
	colorName  fyne.ThemeColorName
	sizeName   fyne.ThemeSizeName
	bridge     bool
	state      *spoilerState
	textObj    *canvas.Text
	strikeLine *canvas.Line
	underLine  *canvas.Line
	cover      *canvas.Rectangle
}

var (
	_ fyne.Widget        = (*decoratedText)(nil)
	_ fyne.Tappable      = (*decoratedText)(nil)
	_ desktop.Cursorable = (*decoratedText)(nil)
)

func newDecoratedText(seg *decoratedSegment) *decoratedText {
	w := &decoratedText{
		textObj: canvas.NewText(seg.text, color.Transparent),
		state:   seg.state,
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

// apply copies the styling fields from a segment onto the widget. The set of
// decorations (which lines/cover exist) is fixed for a given segment, so only
// text and styling are updated here.
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
		extent += fyne.MeasureText(" ", size, w.textObj.TextStyle).Width + 1
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
