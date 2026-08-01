// Package markdown parses the Discord/Revolt flavour of markdown into a small
// AST. It is deliberately not CommonMark: a single newline is a hard line break,
// __text__ is underline rather than bold, and it adds -# subtext and ||spoiler||
// syntax.
//
// The package is pure — no UI dependency — so it can be tested in isolation.
// Rendering the AST to Fyne widgets lives in internal/ui.
package markdown

/* Blocks */

// Document is the root of a parsed message.
type Document struct {
	Blocks []Block
}

// Block is a top-level element that occupies its own vertical space.
type Block interface{ isBlock() }

type (
	// Paragraph is a run of inline content; embedded LineBreak nodes mark the
	// single newlines that render as hard breaks.
	Paragraph struct{ Children []Inline }

	// Heading is a # / ## / ### header (levels 1-3).
	Heading struct {
		Level    int
		Children []Inline
	}

	// Subtext is the small, muted -# line.
	Subtext struct{ Children []Inline }

	// Blockquote is a > quoted run; multiple quoted lines join with LineBreaks.
	Blockquote struct{ Children []Inline }

	// CodeBlock is a fenced ``` block whose content is rendered literally.
	CodeBlock struct {
		Language string
		Text     string
	}

	// List is a run of consecutive list items, ordered or unordered.
	List struct {
		Ordered bool
		Start   int
		Items   [][]Inline
	}
)

func (*Paragraph) isBlock()  {}
func (*Heading) isBlock()    {}
func (*Subtext) isBlock()    {}
func (*Blockquote) isBlock() {}
func (*CodeBlock) isBlock()  {}
func (*List) isBlock()       {}

/* Inlines */

// Inline is a span of formatted content within a block.
type Inline interface{ isInline() }

type (
	// Text is a literal run with no formatting.
	Text struct{ Text string }

	// LineBreak is a hard newline within a paragraph or quote.
	LineBreak struct{}

	// Strong is **bold** text.
	Strong struct{ Children []Inline }

	// Emphasis is *italic* / _italic_ text.
	Emphasis struct{ Children []Inline }

	// Underline is __underlined__ text.
	Underline struct{ Children []Inline }

	// Strike is ~~struck~~ text.
	Strike struct{ Children []Inline }

	// Spoiler is ||hidden|| text revealed on tap.
	Spoiler struct{ Children []Inline }

	// Code is `inline code`, rendered literally in a monospace font.
	Code struct{ Text string }

	// Link is a [label](url) masked link.
	Link struct {
		Children []Inline
		URL      string
	}
)

func (*Text) isInline()      {}
func (*LineBreak) isInline() {}
func (*Strong) isInline()    {}
func (*Emphasis) isInline()  {}
func (*Underline) isInline() {}
func (*Strike) isInline()    {}
func (*Spoiler) isInline()   {}
func (*Code) isInline()      {}
func (*Link) isInline()      {}
