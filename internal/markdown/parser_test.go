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
		"``a`b``":           "c(a`b)", // double-backtick span keeps inner backtick
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

func TestParseBlockquote(t *testing.T) {
	doc := Parse("> quoted line\n> second")
	bq, ok := doc.Blocks[0].(*Blockquote)
	if !ok || inlineString(bq.Children) != "quoted line\\nsecond" {
		t.Errorf("block 0 = %#v", doc.Blocks[0])
	}

	doc = Parse(">>> everything\nafter is quoted")
	if bq, ok := doc.Blocks[0].(*Blockquote); !ok || inlineString(bq.Children) != "everything\\nafter is quoted" {
		t.Errorf("triple-quote = %#v", doc.Blocks[0])
	}
}
