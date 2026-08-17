package markdown

import (
	"fmt"
	"strings"
	"testing"
)

// inlineString renders inline nodes into a compact tagged form for assertions,
// e.g. "b(hi)" for bold, "sp(x)" for spoiler.
func inlineString(nodes []Inline) string {
	var b strings.Builder
	for _, n := range nodes {
		switch v := n.(type) {
		case *Text:
			b.WriteString(v.Text)
		case *LineBreak:
			b.WriteString("\\n")
		case *Strong:
			fmt.Fprintf(&b, "b(%s)", inlineString(v.Children))
		case *Emphasis:
			fmt.Fprintf(&b, "i(%s)", inlineString(v.Children))
		case *Underline:
			fmt.Fprintf(&b, "u(%s)", inlineString(v.Children))
		case *Strike:
			fmt.Fprintf(&b, "s(%s)", inlineString(v.Children))
		case *Spoiler:
			fmt.Fprintf(&b, "sp(%s)", inlineString(v.Children))
		case *Code:
			fmt.Fprintf(&b, "c(%s)", v.Text)
		case *Link:
			fmt.Fprintf(&b, "a(%s|%s)", inlineString(v.Children), v.URL)
		case *UserMention:
			fmt.Fprintf(&b, "@(%s)", v.UserID)
		case *ChannelMention:
			fmt.Fprintf(&b, "#(%s)", v.ChannelID)
		case *Emoji:
			fmt.Fprintf(&b, ":(%s)", v.EmojiID)
		case *Timestamp:
			fmt.Fprintf(&b, "t(%d|%s)", v.Time.Unix(), v.Style)
		}
	}
	return b.String()
}

func TestParseInline(t *testing.T) {
	cases := map[string]string{
		"plain text":        "plain text",
		"**bold**":          "b(bold)",
		"*italic*":          "i(italic)",
		"_italic_":          "i(italic)",
		"__underline__":     "u(underline)",
		"~~strike~~":        "s(strike)",
		"||spoiler||":       "sp(spoiler)",
		"`code`":            "c(code)",
		"**bold _nested_**": "b(bold i(nested))",
		"a **b** c":         "a b(b) c",
		"[label](http://x)": "a(label|http://x)",
		"[**bold**](u)":     "a(b(bold)|u)",
		`\*not bold\*`:      "*not bold*",     // escaped asterisks are literal
		"`**literal**`":     "c(**literal**)", // no formatting inside code
		"||a **b**||":       "sp(a b(b))",
		"unclosed **bold":   "unclosed **bold", // dangling delimiter stays literal
		"5 * 3 = 15":        "5 * 3 = 15",      // lone asterisk with no close
		"~~a~~ and ~~b~~":   "s(a) and s(b)",   // lazy close

		// Single-delimiter emphasis guards (Discord-compatible): _ is a word
		// character, so it only opens/closes at word boundaries, and neither *
		// nor _ accepts whitespace-edged content.
		"snake_case_name":    "snake_case_name",   // intraword _ never opens
		"_open_world":        "_open_world",       // close mid-word rejected
		"_foo_bar_":          "i(foo_bar)",        // rejected close extends the span
		"use _force_ now":    "use i(force) now",  // boundary-delimited still works
		"5 * 3 * 4":          "5 * 3 * 4",         // space-edged content stays literal
		"2*3*4":              "2i(3)4",            // * needs no word boundary
		"__init__ is dunder": "u(init) is dunder", // __ keeps matching intraword

		// Mentions. The two forms differ only in the marker, and both need a
		// non-empty run of alphanumerics — so prose that happens to open with one
		// of the delimiters stays literal.
		"hi <@01ABC>":       "hi @(01ABC)",
		"see <#01XYZ> for":  "see #(01XYZ) for",
		"<@01A> and <#01B>": "@(01A) and #(01B)",
		"<@>":               "<@>",         // no ID
		"<# general>":       "<# general>", // not an ID
		"a < b # c":         "a < b # c",

		// Timestamps. The style is validated rather than taken verbatim: an unknown
		// letter has no rendering, and drawing it as the default would show the
		// wrong face of the right instant instead of what was typed. 't' is also a
		// scheme byte, so a miss has to fall through to the bracketed-URL match.
		"due <t:1700000000:R>":  "due t(1700000000|R)",
		"<t:1700000000>":        "t(1700000000|)",
		"<t:0:F>":               "t(0|F)",
		"<t:-86400:d>":          "t(-86400|d)", // before the epoch
		"<t:1700000000:Q>":      "<t:1700000000:Q>",
		"<t:1700000000:>":       "<t:1700000000:>",
		"<t:>":                  "<t:>",
		"<t:soon>":              "<t:soon>",
		"<tftp://host/x>":       "a(tftp://host/x|tftp://host/x)",
		`\<t:1700000000:R>`:     "<t:1700000000:R>",
		"see <t:1700000000:R> ": "see t(1700000000|R) ",

		// Custom emoji. A colon is ordinary punctuation, so only an exact ULID
		// between two of them is one — everything else has to survive untouched, or
		// prose and clock times would sprout blank squares.
		"hey :01J9WN3PHX4ZQSNSZH10CK4RHS: there": "hey :(01J9WN3PHX4ZQSNSZH10CK4RHS) there",
		":01J9WN3PHX4ZQSNSZH10CK4RHS:":           ":(01J9WN3PHX4ZQSNSZH10CK4RHS)",
		":smile:":                                ":smile:",
		"meeting at 10:30:00 sharp":              "meeting at 10:30:00 sharp",
		":01J9WN3PHX4ZQSNSZH10CK4RH:":            ":01J9WN3PHX4ZQSNSZH10CK4RH:",   // a character short
		":01J9WN3PHX4ZQSNSZH10CK4RHSX:":          ":01J9WN3PHX4ZQSNSZH10CK4RHSX:", // one too many
		":01J9WN3PHX4ZQSNSZH10CK4RH-:":           ":01J9WN3PHX4ZQSNSZH10CK4RH-:",  // not all alphanumeric
		"ratio 1:2":                              "ratio 1:2",

		// Escapes cover all ASCII punctuation, which is the only rule wide enough
		// to reach the syntax this flavour adds on top of CommonMark's.
		`\:01J9WN3PHX4ZQSNSZH10CK4RHS:`: ":01J9WN3PHX4ZQSNSZH10CK4RHS:",
		`\<@01ABC>`:                     "<@01ABC>",

		// A code span closes on a backtick run of its own length, so a longer run
		// inside one is content rather than a second span.
		"`a``b`":    "c(a``b)",
		"``a`b``":   "c(a`b)",
		"`` `x` ``": "c( `x` )",

		// Bare URLs link the way Discord's do, including the punctuation rules
		// that decide where one ends.
		"see https://x.dev/a for":      "see a(https://x.dev/a|https://x.dev/a) for",
		"(see https://x.dev)":          "(see a(https://x.dev|https://x.dev))",
		"https://en.wikipedia.org/(a)": "a(https://en.wikipedia.org/(a)|https://en.wikipedia.org/(a))",
		"<https://x.dev>":              "a(https://x.dev|https://x.dev)",
		"say https:// only":            "say https:// only", // a scheme with no host
		"not xy://":                    "not xy://",
		"a://b":                        "a://b", // one letter is not a scheme

		// Link destinations may bracket or balance their parentheses, and a title
		// is accepted and dropped.
		"[a](<u v>)":         "a(a|u v)",
		`[a](u "t")`:         "a(a|u)",
		"[a](https://x/(y))": "a(a|https://x/(y))",
		"[see [1]](u)":       "a(see [1]|u)",
		"[a]()":              "[a]()",
	}

	for input, want := range cases {
		got := inlineString(parseInline(input))
		if got != want {
			t.Errorf("parseInline(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseBlocks(t *testing.T) {
	doc := Parse("# Title\nsome **text**\nsecond line\n\n-# subtext here")
	if len(doc.Blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(doc.Blocks))
	}

	h, ok := doc.Blocks[0].(*Heading)
	if !ok || h.Level != 1 || inlineString(h.Children) != "Title" {
		t.Errorf("block 0 = %#v, want H1 'Title'", doc.Blocks[0])
	}

	p, ok := doc.Blocks[1].(*Paragraph)
	if !ok || inlineString(p.Children) != "some b(text)\\nsecond line" {
		t.Errorf("block 1 = %q, want paragraph with line break", inlineString(p.Children))
	}

	if sub, ok := doc.Blocks[2].(*Subtext); !ok || inlineString(sub.Children) != "subtext here" {
		t.Errorf("block 2 = %#v, want subtext", doc.Blocks[2])
	}
}

func TestParseHeadingLevels(t *testing.T) {
	for _, tc := range []struct {
		in    string
		level int
	}{
		{"# h1", 1},
		{"## h2", 2},
		{"### h3", 3},
		{"#### h4", 0}, // Discord supports only 3 levels; #### is a paragraph
		{"#nospace", 0},
	} {
		doc := Parse(tc.in)
		h, ok := doc.Blocks[0].(*Heading)
		switch {
		case tc.level == 0 && ok:
			t.Errorf("%q parsed as heading, want paragraph", tc.in)
		case tc.level != 0 && (!ok || h.Level != tc.level):
			t.Errorf("%q got heading %#v, want level %d", tc.in, doc.Blocks[0], tc.level)
		}
	}
}

func TestParseCodeBlock(t *testing.T) {
	doc := Parse("```go\nfmt.Println()\nx := 1\n```")
	cb, ok := doc.Blocks[0].(*CodeBlock)
	if !ok {
		t.Fatalf("block 0 = %#v, want code block", doc.Blocks[0])
	}
	if cb.Language != "go" {
		t.Errorf("language = %q, want go", cb.Language)
	}
	if cb.Text != "fmt.Println()\nx := 1" {
		t.Errorf("text = %q", cb.Text)
	}
}

// TestParseFenceInfoString covers the two cases the info string decides: a
// backtick in it means the fence closes on its own line, and a space in it means
// it was never a language and must not be swallowed as one.
func TestParseFenceInfoString(t *testing.T) {
	cb, ok := Parse("```one line```").Blocks[0].(*CodeBlock)
	if !ok || cb.Language != "" || cb.Text != "one line" {
		t.Errorf("one-line fence = %#v", Parse("```one line```").Blocks[0])
	}

	cb, ok = Parse("```not a language\nbody\n```").Blocks[0].(*CodeBlock)
	if !ok || cb.Language != "" || cb.Text != "not a language\nbody" {
		t.Errorf("prose info string = %#v", Parse("```not a language\nbody\n```").Blocks[0])
	}
}

func TestParseList(t *testing.T) {
	doc := Parse("- one\n- two\n- three")
	list, ok := doc.Blocks[0].(*List)
	if !ok || list.Ordered || len(list.Items) != 3 {
		t.Fatalf("block 0 = %#v, want unordered list of 3", doc.Blocks[0])
	}

	doc = Parse("2. a\n3. b")
	list, ok = doc.Blocks[0].(*List)
	if !ok || !list.Ordered || list.Start != 2 || len(list.Items) != 2 {
		t.Errorf("ordered list = %#v", doc.Blocks[0])
	}
}

// quoted returns a blockquote's single paragraph in tagged form.
func quoted(t *testing.T, block Block) string {
	t.Helper()

	bq, ok := block.(*Blockquote)
	if !ok || len(bq.Blocks) != 1 {
		t.Fatalf("block = %#v, want a blockquote of one block", block)
	}
	p, ok := bq.Blocks[0].(*Paragraph)
	if !ok {
		t.Fatalf("quoted block = %#v, want a paragraph", bq.Blocks[0])
	}

	return inlineString(p.Children)
}

func TestParseBlockquote(t *testing.T) {
	if got := quoted(t, Parse("> quoted line\n> second").Blocks[0]); got != "quoted line\\nsecond" {
		t.Errorf("got %q", got)
	}
	if got := quoted(t, Parse(">>> everything\nafter is quoted").Blocks[0]); got != "everything\\nafter is quoted" {
		t.Errorf("triple-quote got %q", got)
	}
}

// TestParseBlockquoteBlocks covers what a quote holding blocks rather than a run
// of inlines is for: block markers inside one still mean what they say, and a
// quote marker among them nests.
func TestParseBlockquoteBlocks(t *testing.T) {
	bq, ok := Parse("> # Note\n> - one\n> - two").Blocks[0].(*Blockquote)
	if !ok || len(bq.Blocks) != 2 {
		t.Fatalf("block 0 = %#v, want a quote of two blocks", Parse("> # Note\n> - one").Blocks[0])
	}
	if h, ok := bq.Blocks[0].(*Heading); !ok || h.Level != 1 {
		t.Errorf("quoted block 0 = %#v, want H1", bq.Blocks[0])
	}
	if list, ok := bq.Blocks[1].(*List); !ok || len(list.Items) != 2 {
		t.Errorf("quoted block 1 = %#v, want a list of 2", bq.Blocks[1])
	}

	outer, ok := Parse("> > deep").Blocks[0].(*Blockquote)
	if !ok || len(outer.Blocks) != 1 {
		t.Fatalf("nested quote = %#v", Parse("> > deep").Blocks[0])
	}
	if got := quoted(t, outer.Blocks[0]); got != "deep" {
		t.Errorf("nested quote body = %q", got)
	}
}

// TestParseNestedList covers indentation: a deeper item is a sublist of the same
// run, and each depth numbers itself.
func TestParseNestedList(t *testing.T) {
	list, ok := Parse("1. a\n  1. b\n  2. c\n2. d").Blocks[0].(*List)
	if !ok || len(list.Items) != 4 {
		t.Fatalf("block 0 = %#v, want an ordered list of 4", Parse("1. a\n  1. b").Blocks[0])
	}

	for i, want := range []ListItem{{Indent: 0, Number: 1}, {Indent: 1, Number: 1}, {Indent: 1, Number: 2}, {Indent: 0, Number: 2}} {
		if got := list.Items[i]; got.Indent != want.Indent || got.Number != want.Number {
			t.Errorf("item %d = indent %d number %d, want %d/%d", i, got.Indent, got.Number, want.Indent, want.Number)
		}
	}

	// A change of marker kind ends the run; a change of depth does not.
	if blocks := Parse("- a\n1. b").Blocks; len(blocks) != 2 {
		t.Errorf("got %d blocks for a bullet followed by a number, want 2", len(blocks))
	}
}

// TestParseMultilineSpans covers the reason a paragraph is parsed as one run
// rather than a line at a time: a Discord span crosses a hard line break.
func TestParseMultilineSpans(t *testing.T) {
	cases := map[string]string{
		"**bold\nacross**": "b(bold\\nacross)",
		"||spoiler\nhid||": "sp(spoiler\\nhid)",
		"*a\n*b":           "*a\\n*b", // whitespace-edged content still stays literal
		"one **two\nthree": "one **two\\nthree",
	}

	for input, want := range cases {
		p, ok := Parse(input).Blocks[0].(*Paragraph)
		if !ok {
			t.Fatalf("%q did not parse as a paragraph", input)
		}
		if got := inlineString(p.Children); got != want {
			t.Errorf("Parse(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDocumentText(t *testing.T) {
	input := "# Heading\n**bold** and `code`\n\n- one\n- two\n\n> quoted <@01USER>"
	want := "Heading bold and code one two quoted @"

	if got := DocumentText(Parse(input)); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := DocumentText(Parse("")); got != "" {
		t.Errorf("an empty document yielded %q", got)
	}

	// A fenced block's text is the one thing kept verbatim, so it is the one thing
	// that can put a newline in a run promised to have none — and the reply preview
	// it feeds is a canvas.Text, which draws a newline as a missing glyph.
	if got := DocumentText(Parse("before\n```\none\ntwo\n```")); got != "before one two" {
		t.Errorf("a fenced block leaked its newlines: %q", got)
	}
}

// TestLinks covers what the walk is for: every way of writing a link is found
// wherever it is nested, and the two places a URL is written but not linked —
// code, and a spoiler somebody chose to hide — report nothing.
func TestLinks(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"bare https://x.dev/a here", []string{"https://x.dev/a"}},
		{"[masked](https://x.dev/b)", []string{"https://x.dev/b"}},
		{"<https://x.dev/c>", []string{"https://x.dev/c"}},
		{"> quoted https://x.dev/d", []string{"https://x.dev/d"}},
		{"- item https://x.dev/e", []string{"https://x.dev/e"}},
		{"# heading https://x.dev/f", []string{"https://x.dev/f"}},
		{"**bold https://x.dev/g**", []string{"https://x.dev/g"}},
		{"two https://x.dev/h and https://x.dev/i", []string{"https://x.dev/h", "https://x.dev/i"}},

		// Written about, not written as a link.
		{"`https://x.dev/j`", nil},
		{"```\nhttps://x.dev/k\n```", nil},
		{"||https://x.dev/l||", nil},
		{"nothing here", nil},
	}

	for _, c := range cases {
		got := Links(Parse(c.input))
		if len(got) != len(c.want) {
			t.Errorf("Links(%q) = %q, want %q", c.input, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Links(%q) = %q, want %q", c.input, got, c.want)
				break
			}
		}
	}
}
