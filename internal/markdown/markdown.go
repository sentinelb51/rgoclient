// Package markdown parses the Discord/Revolt flavour of markdown into a small
// AST. It is deliberately not CommonMark: a single newline is a hard line break,
// __text__ is underline rather than bold, and it adds -# subtext and ||spoiler||
// syntax. Where the two do not disagree it follows CommonMark — backslash escapes
// any ASCII punctuation, a code span closes on a backtick run of its own length,
// a link destination may bracket or balance its parentheses.
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

	// Blockquote is a > quoted run. It holds blocks rather than inlines because a
	// quote may contain any of them — "> # Note" is a heading inside a quote, and
	// a second ">" survives the strip, so quotes nest.
	Blockquote struct{ Blocks []Block }

	// CodeBlock is a fenced ``` block whose content is rendered literally.
	CodeBlock struct {
		Language string
		Text     string
	}

	// List is a run of consecutive list items, ordered or unordered. Start is the
	// first item's number, kept for a renderer that only wants the run's origin.
	List struct {
		Ordered bool
		Start   int
		Items   []ListItem
	}

	// ListItem is one entry of a List. Indent is its nesting depth — a sublist is
	// not a List of its own, since the run is one block and only the marker column
	// moves — and Number is what an ordered item counts as at that depth.
	ListItem struct {
		Indent   int
		Number   int
		Children []Inline
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

	// Link is a [label](url) masked link, a <url> bracketed one, or a bare URL
	// found in running text — the last two carry the URL as their own label.
	Link struct {
		Children []Inline
		URL      string
	}

	// UserMention is a <@id> user reference, ChannelMention a <#id> channel one.
	// Both carry only the ID: turning one into a name needs the session, which
	// this package deliberately has no access to, so the renderer resolves it.
	UserMention struct{ UserID string }

	ChannelMention struct{ ChannelID string }

	// Emoji is a :01J9WN3PHX4ZQSNSZH10CK4RHS: custom emoji, carrying only the ID
	// for the same reason the mentions do — the picture is served from a CDN this
	// package has no business naming.
	Emoji struct{ EmojiID string }
)

func (*Text) isInline()           {}
func (*LineBreak) isInline()      {}
func (*Strong) isInline()         {}
func (*Emphasis) isInline()       {}
func (*Underline) isInline()      {}
func (*Strike) isInline()         {}
func (*Spoiler) isInline()        {}
func (*Code) isInline()           {}
func (*Link) isInline()           {}
func (*UserMention) isInline()    {}
func (*ChannelMention) isInline() {}
func (*Emoji) isInline()          {}
