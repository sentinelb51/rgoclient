package ui

// Fenced code blocks — the well one is drawn in, and the highlighter that
// colours what is inside it.
//
// The highlighter is deliberately a lexer and nothing more: comments, strings,
// numbers, a language's keywords, and an identifier that is immediately called.
// Anything past that needs a parser per language, and a message pane is not the
// place to keep one — a token it cannot classify is drawn as plain code text,
// which is what an unlabelled fence full of prose gets.

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
)

// newCodeBlock draws a fenced block: a well of its own, filled and outlined, with
// the highlighted source inside it. The card stretches to the body's width — a
// block sized to its longest line would step in and out as messages arrive.
//
// onMenu is the owning message's right-click handler, which the copy chip carries
// so it is not a hole in the row's own menu.
func newCodeBlock(language, text string, onMenu func(*fyne.PointEvent)) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Colors.CodeBlockBg)
	background.CornerRadius = theme.Sizes.CodeBlockRadius
	Outline(background)

	// Break rather than word wrapping: a line of code has no words to break at, and
	// what RichText cannot fit it draws past the card's edge.
	source := widget.NewRichText(codeSegments(text, language)...)
	source.Wrapping = fyne.TextWrapBreak

	padV, padH := theme.Sizes.CodeBlockPaddingV, theme.Sizes.CodeBlockPaddingH
	inset := theme.Sizes.CodeCopyInset
	corner := container.New(&overlayLayout{yOffset: inset, rightOffset: inset}, newCodeCopy(text, onMenu))

	return container.NewStack(background, NewInset(newFlushContainer(source), padV, padV, padH, padH), corner)
}

/* Copy chip */

// codeCopyRevert is how long the tick stands before the mark goes back to the
// clipboard glyph — long enough to be read, short of looking like a state.
const codeCopyRevert = 1500 * time.Millisecond

// codeCopy is the chip in a well's corner that puts the block on the clipboard.
// It exists because the block cannot be selected at all: highlighting is many
// RichText segments and Fyne 2.8 makes only a one-segment Label selectable, so
// there is no drag to copy from.
//
// Deliberately not hoverable — innermost wins, and a control inside a message
// body claiming hover would drop the row's own quick actions — so it rests
// dimmed and answers the tap instead, swapping the mark for a tick.
type codeCopy struct {
	tapBase
	icon *canvas.Image
	text string

	// generation guards the revert: a second tap arms a second timer, and the
	// first must not clear a tick the second put back.
	generation uint64
}

var _ fyne.Tappable = (*codeCopy)(nil)

func newCodeCopy(text string, onMenu func(*fyne.PointEvent)) *codeCopy {
	c := &codeCopy{icon: newScaledIcon(actionMark(assets.ActionCopyIcon), theme.Sizes.CodeCopySize*0.55), text: text}
	c.icon.Translucency = iconRestTranslucency
	c.onTap = c.copy
	c.onSecondaryTap = onMenu
	c.ExtendBaseWidget(c)

	return c
}

func (c *codeCopy) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(roundedPanel(), container.NewCenter(c.icon)))
}

// MinSize squares the chip to the row a code line occupies — one monospace line
// plus the well's padding, less the inset the chip floats in by — so it stands
// the height of the line beside it at any configured font size rather than being
// a small mark in a taller well. overlayLayout sizes it to this and nothing else
// asks.
func (c *codeCopy) MinSize() fyne.Size {
	line := fyne.MeasureText("M", c.Theme().Size(fynetheme.SizeNameText), fyne.TextStyle{Monospace: true}).Height
	side := line + 2*(theme.Sizes.CodeBlockPaddingV-theme.Sizes.CodeCopyInset)

	return fyne.NewSize(side, side)
}

func (c *codeCopy) copy() {
	CopyToClipboard(c.text)
	c.setCopied(true)

	c.generation++
	generation := c.generation
	time.AfterFunc(codeCopyRevert, func() {
		DoOnUI(func() {
			if c.generation == generation {
				c.setCopied(false)
			}
		})
	})
}

// setCopied swaps the mark for the state it is in. The tick is lit where the
// clipboard glyph rests dimmed: the confirmation is the whole point of it.
func (c *codeCopy) setCopied(copied bool) {
	c.icon.Resource, c.icon.Translucency = actionMark(assets.ActionCopyIcon), iconRestTranslucency
	if copied {
		c.icon.Resource, c.icon.Translucency = tintedIcon(assets.ActionSaveIcon, theme.Colors.SwiftActionConfirm), 0
	}
	c.icon.Refresh()
}

// codeSegments renders the highlighted source as RichText segments: every line's
// spans inline, then the empty non-inline segment that ends the row.
func codeSegments(text, language string) []widget.RichTextSegment {
	mono := fyne.TextStyle{Monospace: true}
	body := strings.Trim(strings.ReplaceAll(text, "\t", "    "), "\n")

	var segs []widget.RichTextSegment
	for _, span := range highlightCode(body, language) {
		for j, line := range strings.Split(span.text, "\n") {
			if j > 0 {
				segs = append(segs, &widget.TextSegment{Style: widget.RichTextStyle{TextStyle: mono}})
			}
			if line == "" {
				continue
			}
			segs = append(segs, &widget.TextSegment{
				Text:  line,
				Style: widget.RichTextStyle{Inline: true, TextStyle: mono, ColorName: span.kind.colorName()},
			})
		}
	}

	// The last row needs its terminator too, an inline-only body being one RichText
	// draws without wrapping at all.
	return append(segs, &widget.TextSegment{Style: widget.RichTextStyle{TextStyle: mono}})
}

/* Highlighting */

// codeKind is what one span of source was recognised as.
type codeKind int

const (
	codePlain codeKind = iota
	codeKeyword
	codeString
	codeComment
	codeNumber
	codeCall
)

func (k codeKind) colorName() fyne.ThemeColorName {
	switch k {
	case codeKeyword:
		return theme.ColorNameCodeKeyword
	case codeString:
		return theme.ColorNameCodeString
	case codeComment:
		return theme.ColorNameCodeComment
	case codeNumber:
		return theme.ColorNameCodeNumber
	case codeCall:
		return theme.ColorNameCodeCall
	}

	return theme.ColorNameCode
}

// codeSpan is a run of source of one kind. Spans tile the whole text in order,
// so rendering is a walk rather than a lookup.
type codeSpan struct {
	text string
	kind codeKind
}

// codeSyntax is what one family of languages looks like to the lexer.
type codeSyntax struct {
	keywords map[string]bool

	lineComment string // "" → the language has none
	blockOpen   string
	blockClose  string
	quotes      string // the characters that open a string
}

// highlightCode splits text into spans. language is the fence's info string,
// which is usually absent — guessLanguage answers for what is left, and the
// generic syntax for what it cannot name.
func highlightCode(text, language string) []codeSpan {
	syntax := codeSyntaxFor(language, text)

	var spans []codeSpan

	plain := 0 // start of the run not yet emitted
	emit := func(from, to int, kind codeKind) {
		if from > plain {
			spans = append(spans, codeSpan{text: text[plain:from]})
		}
		spans = append(spans, codeSpan{text: text[from:to], kind: kind})
		plain = to
	}

	for i := 0; i < len(text); {
		rest := text[i:]

		if syntax.blockOpen != "" && strings.HasPrefix(rest, syntax.blockOpen) {
			to := len(text)
			if end := strings.Index(text[i+len(syntax.blockOpen):], syntax.blockClose); end >= 0 {
				to = i + len(syntax.blockOpen) + end + len(syntax.blockClose)
			}
			emit(i, to, codeComment)
			i = to

			continue
		}

		if syntax.lineComment != "" && strings.HasPrefix(rest, syntax.lineComment) {
			to := len(text)
			if end := strings.IndexByte(rest, '\n'); end >= 0 {
				to = i + end
			}
			emit(i, to, codeComment)
			i = to

			continue
		}

		if strings.IndexByte(syntax.quotes, text[i]) >= 0 {
			to := codeStringEnd(text, i)
			emit(i, to, codeString)
			i = to

			continue
		}

		// A digit inside an identifier is part of the identifier, not a literal.
		if isCodeDigit(text[i]) && (i == 0 || !isCodeWord(text[i-1])) {
			to := i + 1
			for to < len(text) && (isCodeWord(text[to]) || (text[to] == '.' && to+1 < len(text) && isCodeDigit(text[to+1]))) {
				to++
			}
			emit(i, to, codeNumber)
			i = to

			continue
		}

		if isCodeWordStart(text[i]) {
			to := i + 1
			for to < len(text) && isCodeWord(text[to]) {
				to++
			}
			switch {
			case syntax.keywords[text[i:to]]:
				emit(i, to, codeKeyword)
			case to < len(text) && text[to] == '(':
				emit(i, to, codeCall)
			}
			i = to

			continue
		}

		i++
	}

	if plain < len(text) {
		spans = append(spans, codeSpan{text: text[plain:]})
	}

	return spans
}

// codeStringEnd finds the byte after the literal opening at i. A quote with no
// partner ends at the line's end rather than swallowing the rest of the block —
// an apostrophe in prose is more common than an unterminated string.
func codeStringEnd(text string, i int) int {
	quote := text[i]

	// Python's triple quote, the one multi-line literal common enough to matter.
	if fence := strings.Repeat(string(quote), 3); strings.HasPrefix(text[i:], fence) {
		if end := strings.Index(text[i+3:], fence); end >= 0 {
			return i + 3 + end + 3
		}

		return len(text)
	}

	raw := quote == '`'
	for j := i + 1; j < len(text); j++ {
		switch {
		case text[j] == quote:
			return j + 1
		case !raw && text[j] == '\\':
			j++
		case !raw && text[j] == '\n':
			return j
		}
	}

	return len(text)
}

func isCodeDigit(c byte) bool { return c >= '0' && c <= '9' }

func isCodeWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isCodeWord(c byte) bool { return isCodeWordStart(c) || isCodeDigit(c) }

/* Languages */

// codeSyntaxFor resolves a fence's info string, falling back to what the source
// itself looks like and then to the generic C-shaped syntax.
func codeSyntaxFor(language, text string) codeSyntax {
	name := strings.ToLower(strings.TrimSpace(language))
	if name == "" {
		name = guessLanguage(text)
	}

	if syntax, ok := codeSyntaxes[name]; ok {
		return syntax
	}

	return codeSyntaxes[""]
}

// guessLanguage names a language from a marker only that language has. It is a
// courtesy for the fences nobody labels: a miss costs the wrong keyword set, not
// a wrong rendering, so the checks stay cheap and few.
func guessLanguage(text string) string {
	switch {
	case strings.HasPrefix(strings.TrimSpace(text), "#!"):
		return "shell"
	case strings.Contains(text, "package ") && strings.Contains(text, "func "):
		return "go"
	case strings.Contains(text, ":=") || strings.Contains(text, "func ("):
		return "go"
	case strings.Contains(text, "fn ") && strings.Contains(text, "let "):
		return "rust"
	case strings.Contains(text, "def ") || strings.Contains(text, "elif "):
		return "python"
	case strings.Contains(text, "#include"):
		return "c"
	case strings.Contains(text, "=>") || strings.Contains(text, "function "):
		return "javascript"
	}

	return ""
}

// words is the keyword table's constructor — a set is what the lexer asks.
func words(list string) map[string]bool {
	set := make(map[string]bool)
	for _, word := range strings.Fields(list) {
		set[word] = true
	}

	return set
}

// codeSyntaxes is keyed by every name a fence is labelled with, aliases and all.
// "" is the fallback: C-shaped comments, and the keywords the curly-brace
// languages agree on.
var codeSyntaxes = func() map[string]codeSyntax {
	cLike := func(keywords map[string]bool) codeSyntax {
		return codeSyntax{keywords: keywords, lineComment: "//", blockOpen: "/*", blockClose: "*/", quotes: "\"'`"}
	}

	table := map[string]codeSyntax{
		"": cLike(words(`if else for while do return break continue switch case default
			function class new true false null nil void const let var import export
			try catch finally throw this typeof instanceof static public private`)),
	}

	add := func(syntax codeSyntax, names ...string) {
		for _, name := range names {
			table[name] = syntax
		}
	}

	add(cLike(words(`break case chan const continue default defer else fallthrough for func go
		goto if import interface map package range return select struct switch type var
		bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64
		rune string uint uint8 uint16 uint32 uint64 uintptr any true false nil iota
		make new len cap append copy delete panic recover`)), "go", "golang")

	add(codeSyntax{
		keywords: words(`and as assert async await break class continue def del elif else except
			finally for from global if import in is lambda none nonlocal not or pass raise
			return try while with yield True False None self print len range str int float
			dict list set tuple`),
		lineComment: "#",
		quotes:      "\"'",
	}, "python", "py")

	add(cLike(words(`as async await break const continue crate dyn else enum extern false fn for
		if impl in let loop match mod move mut pub ref return self Self static struct super
		trait true type unsafe use where while bool char str String Vec Option Result Some
		None Ok Err usize isize u8 u16 u32 u64 i8 i16 i32 i64 f32 f64 println`)), "rust", "rs")

	add(cLike(words(`abstract async await break case catch class const continue debugger default
		delete do else enum export extends false finally for from function get if implements
		import in instanceof interface let new null of private public return set static super
		switch this throw true try typeof undefined var void while with yield any boolean
		number string type readonly namespace declare keyof as satisfies`)),
		"javascript", "js", "jsx", "mjs", "typescript", "ts", "tsx", "node")

	add(cLike(words(`auto break case char class const constexpr continue default delete do double
		else enum explicit extern false final float for friend goto if inline int long
		namespace new nullptr operator override private protected public register return
		short signed sizeof static struct switch template this throw true try typedef
		typename union unsigned using virtual void volatile while abstract assert boolean
		byte catch extends final finally implements import instanceof interface native
		package super synchronized throws transient var record sealed string decimal event
		internal object params readonly ref out async await`)),
		"c", "h", "cpp", "c++", "cc", "hpp", "java", "cs", "csharp", "kotlin", "kt", "swift", "php", "dart", "scala", "groovy")

	add(codeSyntax{
		keywords: words(`if then elif else fi for while until do done case esac function in select
			return exit local export source alias unset echo cd set trap read shift eval sudo
			apt git curl wget grep sed awk cat ls rm cp mv mkdir chmod chown`),
		lineComment: "#",
		quotes:      "\"'`",
	}, "shell", "sh", "bash", "zsh", "console", "terminal", "powershell", "ps1")

	add(codeSyntax{
		keywords: words(`SELECT FROM WHERE INSERT INTO VALUES UPDATE SET DELETE CREATE TABLE DROP
			ALTER ADD INDEX VIEW JOIN LEFT RIGHT INNER OUTER FULL ON GROUP BY ORDER HAVING
			LIMIT OFFSET UNION ALL DISTINCT AS AND OR NOT NULL IS IN LIKE BETWEEN EXISTS
			CASE WHEN THEN ELSE END PRIMARY KEY FOREIGN REFERENCES DEFAULT
			select from where insert into values update set delete create table drop alter
			add index view join left right inner outer full on group by order having limit
			offset union all distinct as and or not null is in like between exists case when
			then else end primary key foreign references default`),
		lineComment: "--",
		blockOpen:   "/*",
		blockClose:  "*/",
		quotes:      "'\"",
	}, "sql", "postgres", "postgresql", "mysql", "sqlite")

	add(codeSyntax{
		keywords: words(`true false null`),
		quotes:   "\"",
	}, "json")

	add(codeSyntax{
		keywords:    words(`true false null yes no on off`),
		lineComment: "#",
		quotes:      "\"'",
	}, "yaml", "yml", "toml", "ini", "conf", "dockerfile", "docker", "makefile", "make", "ruby", "rb", "perl", "r", "elixir", "ex")

	return table
}()
