package markdown

import (
	"strconv"
	"strings"
)

// Parse turns a raw message string into a Document.
func Parse(input string) *Document {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	return &Document{Blocks: parseBlocks(strings.Split(input, "\n"))}
}

// PlainText returns the unformatted text of inline nodes, for previews and for
// spans whose visuals show plain text.
func PlainText(nodes []Inline) string {
	var b strings.Builder
	writePlain(&b, nodes)

	return b.String()
}

// DocumentText returns a whole document as one run of unformatted text, blocks
// joined by a space. It is for a preview with room for a sentence rather than a
// body — a profile card's bio — where the formatting would only get in the way
// of the words.
func DocumentText(doc *Document) string {
	var b strings.Builder

	for _, block := range doc.Blocks {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}

		switch v := block.(type) {
		case *Paragraph:
			writePlain(&b, v.Children)
		case *Heading:
			writePlain(&b, v.Children)
		case *Subtext:
			writePlain(&b, v.Children)
		case *Blockquote:
			writePlain(&b, v.Children)
		case *CodeBlock:
			b.WriteString(v.Text)
		case *List:
			for i, item := range v.Items {
				if i > 0 {
					b.WriteByte(' ')
				}
				writePlain(&b, item)
			}
		}
	}

	return b.String()
}

func writePlain(b *strings.Builder, nodes []Inline) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *Text:
			b.WriteString(v.Text)
		case *Code:
			b.WriteString(v.Text)
		case *LineBreak:
			b.WriteByte(' ')
		case *Strong:
			writePlain(b, v.Children)
		case *Emphasis:
			writePlain(b, v.Children)
		case *Underline:
			writePlain(b, v.Children)
		case *Strike:
			writePlain(b, v.Children)
		case *Spoiler:
			writePlain(b, v.Children)
		case *Link:
			writePlain(b, v.Children)
		case *Mention:
			// No session here to turn the ID into a name, and the raw token would be
			// worse than nothing in a preview, so the marker alone stands in.
			b.WriteByte('@')
		}
	}
}

/* Blocks */

// parseBlocks groups lines into block-level nodes.
func parseBlocks(lines []string) []Block {
	var blocks []Block

	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++ // blank line: paragraph separator
		case isFence(line):
			block, next := parseFence(lines, i)
			blocks = append(blocks, block)
			i = next
		case headingLevel(line) > 0:
			level := headingLevel(line)
			blocks = append(blocks, &Heading{Level: level, Children: parseInline(line[level+1:])})
			i++
		case isSubtext(line):
			blocks = append(blocks, &Subtext{Children: parseInline(line[3:])})
			i++
		case isQuote(line):
			block, next := parseQuote(lines, i)
			blocks = append(blocks, block)
			i = next
		case isListItem(line):
			block, next := parseList(lines, i)
			blocks = append(blocks, block)
			i = next
		default:
			block, next := parseParagraph(lines, i)
			blocks = append(blocks, block)
			i = next
		}
	}

	return blocks
}

// isBlockStart reports whether a line begins a block other than a paragraph,
// terminating the paragraph currently being collected.
func isBlockStart(line string) bool {
	return strings.TrimSpace(line) == "" || isFence(line) || headingLevel(line) > 0 ||
		isSubtext(line) || isQuote(line) || isListItem(line)
}

func isFence(line string) bool { return strings.HasPrefix(line, "```") }

func isSubtext(line string) bool { return strings.HasPrefix(line, "-# ") }

func isQuote(line string) bool {
	return line == ">" || strings.HasPrefix(line, "> ") || strings.HasPrefix(line, ">>> ")
}

// parseFence reads a fenced code block starting at i, returning the block and
// the index of the line after the closing fence.
func parseFence(lines []string, i int) (Block, int) {
	language := strings.TrimSpace(lines[i][3:])

	var body []string
	j := i + 1
	for ; j < len(lines); j++ {
		if strings.HasPrefix(lines[j], "```") {
			j++ // consume the closing fence
			break
		}
		body = append(body, lines[j])
	}

	return &CodeBlock{Language: language, Text: strings.Join(body, "\n")}, j
}

// headingLevel returns the header level (1-3) for a line, or 0 when it is not a
// header. A space is required after the # run.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n >= 1 && n <= 3 && n < len(line) && line[n] == ' ' {
		return n
	}

	return 0
}

// parseQuote reads a blockquote starting at i. A ">>> " line quotes everything
// that follows; otherwise consecutive "> " lines are grouped.
func parseQuote(lines []string, i int) (Block, int) {
	if strings.HasPrefix(lines[i], ">>> ") {
		body := append([]string{lines[i][4:]}, lines[i+1:]...)
		return &Blockquote{Children: parseInlineLines(body)}, len(lines)
	}

	var body []string
	j := i
	for ; j < len(lines) && isQuote(lines[j]); j++ {
		body = append(body, strings.TrimPrefix(strings.TrimPrefix(lines[j], ">"), " "))
	}

	return &Blockquote{Children: parseInlineLines(body)}, j
}

// listItem reports whether a line is a list item and, if so, its marker details
// and content.
func listItem(line string) (ordered bool, number int, content string, ok bool) {
	s := strings.TrimLeft(line, " ")
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
		return false, 0, s[2:], true
	}

	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(s) && s[digits] == '.' && s[digits+1] == ' ' {
		number, _ = strconv.Atoi(s[:digits])
		return true, number, s[digits+2:], true
	}

	return false, 0, "", false
}

func isListItem(line string) bool {
	_, _, _, ok := listItem(line)
	return ok
}

// parseList reads consecutive list items starting at i, inheriting orderedness
// and start number from the first.
func parseList(lines []string, i int) (Block, int) {
	ordered, start, _, _ := listItem(lines[i])
	list := &List{Ordered: ordered, Start: start}

	j := i
	for ; j < len(lines); j++ {
		_, _, content, ok := listItem(lines[j])
		if !ok {
			break
		}
		list.Items = append(list.Items, parseInline(content))
	}

	return list, j
}

// parseParagraph collects consecutive plain lines into one paragraph, joined
// with hard line breaks.
func parseParagraph(lines []string, i int) (Block, int) {
	var body []string
	j := i
	for ; j < len(lines) && !isBlockStart(lines[j]); j++ {
		body = append(body, lines[j])
	}

	return &Paragraph{Children: parseInlineLines(body)}, j
}

// parseInlineLines parses each line's inline content, separated by LineBreaks.
func parseInlineLines(lines []string) []Inline {
	var out []Inline
	for i, line := range lines {
		if i > 0 {
			out = append(out, &LineBreak{})
		}
		out = append(out, parseInline(line)...)
	}

	return out
}

/* Inlines */

// escapable lists the punctuation a preceding backslash turns literal.
const escapable = "\\`*_~|[]()>#-+.!"

// parseInline tokenizes a single run of text into inline nodes.
func parseInline(s string) []Inline {
	var out []Inline
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			out = append(out, &Text{Text: buf.String()})
			buf.Reset()
		}
	}

	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(escapable, s[i+1]) >= 0 {
			buf.WriteByte(s[i+1])
			i += 2
			continue
		}
		if node, width := matchInline(s[i:], i > 0 && isWordByte(s[i-1])); node != nil {
			flush()
			out = append(out, node)
			i += width
			continue
		}
		buf.WriteByte(s[i])
		i++
	}
	flush()

	return out
}

// matchInline tries to match a formatting construct at the start of s, returning
// the node and bytes consumed, or (nil, 0). Order matters: longer delimiters are
// tried before their single-character counterparts. prevWord reports whether the
// byte before s is a word character — _ is itself a word character, so
// underscore emphasis only opens at a word boundary (snake_case stays literal)
// while * needs no boundary (2*3*4 italicises the 3).
func matchInline(s string, prevWord bool) (Inline, int) {
	switch {
	case s[0] == '`':
		return matchCode(s)
	case strings.HasPrefix(s, "||"):
		return wrap(s, "||", func(c []Inline) Inline { return &Spoiler{Children: c} })
	case strings.HasPrefix(s, "**"):
		return wrap(s, "**", func(c []Inline) Inline { return &Strong{Children: c} })
	case strings.HasPrefix(s, "__"):
		return wrap(s, "__", func(c []Inline) Inline { return &Underline{Children: c} })
	case strings.HasPrefix(s, "~~"):
		return wrap(s, "~~", func(c []Inline) Inline { return &Strike{Children: c} })
	case s[0] == '*':
		return matchEmphasis(s, "*", false)
	case s[0] == '_' && !prevWord:
		return matchEmphasis(s, "_", true)
	case s[0] == '[':
		return matchLink(s)
	case strings.HasPrefix(s, "<@"):
		return matchMention(s)
	}

	return nil, 0
}

// mentionIDMaxLen bounds how far matchMention looks for the closing '>'. Revolt
// IDs are 26-character ULIDs; the slack is there so a future ID format doesn't
// silently stop rendering, while "<@" in ordinary prose still falls through to
// literal text after a few bytes.
const mentionIDMaxLen = 64

// matchMention matches a <@id> user reference. The ID must be a non-empty run of
// alphanumerics, so "<@ someone>" and other prose that happens to open with the
// delimiter stays literal.
func matchMention(s string) (Inline, int) {
	for i := 2; i < len(s) && i-2 <= mentionIDMaxLen; i++ {
		switch {
		case s[i] == '>':
			if i == 2 {
				return nil, 0 // "<@>" carries no ID
			}
			return &Mention{UserID: s[2:i]}, i + 1
		case !isAlphanumericByte(s[i]):
			return nil, 0
		}
	}

	return nil, 0
}

// matchEmphasis matches single-delimiter emphasis (*x* or _x_) with guards
// against the false positives a loose wrap would accept: the content must not
// start or end with whitespace ("5 * 3 * 4" stays literal), and underscore
// emphasis must also close at a word boundary ("_open_world" stays literal). A
// rejected closing delimiter lazily extends the span to the next one, so
// "_foo_bar_" italicises "foo_bar".
func matchEmphasis(s, delim string, boundary bool) (Inline, int) {
	rest := s[1:]

	for end := findClose(rest, delim); end > 0; {
		content := rest[:end]
		edgesOK := !isSpaceByte(content[0]) && !isSpaceByte(content[end-1])
		closeOK := !boundary || end+2 >= len(s) || !isWordByte(s[end+2])
		if edgesOK && closeOK {
			return &Emphasis{Children: parseInline(content)}, end + 2
		}

		next := findClose(rest[end+1:], delim)
		if next < 0 {
			break
		}
		end += 1 + next
	}

	return nil, 0
}

// wrap matches a delimiter-bounded span (e.g. **bold**), recursively parsing its
// contents. An unterminated or empty span does not match.
func wrap(s, delim string, build func([]Inline) Inline) (Inline, int) {
	rest := s[len(delim):]
	end := findClose(rest, delim)
	if end <= 0 {
		return nil, 0
	}

	return build(parseInline(rest[:end])), len(delim)*2 + end
}

// matchCode matches an inline code span: a run of N backticks opens it, the next
// run of exactly N closes it, and the contents are literal.
func matchCode(s string) (Inline, int) {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}

	fence, rest := s[:n], s[n:]
	idx := strings.Index(rest, fence)
	if idx < 0 {
		return nil, 0
	}

	return &Code{Text: rest[:idx]}, n*2 + idx
}

// matchLink matches a [label](url) masked link.
func matchLink(s string) (Inline, int) {
	label := findClose(s[1:], "]")
	if label < 0 {
		return nil, 0
	}

	rest := s[1+label+1:] // after the closing ']'
	if len(rest) == 0 || rest[0] != '(' {
		return nil, 0
	}

	link := findClose(rest[1:], ")")
	if link < 0 {
		return nil, 0
	}

	node := &Link{Children: parseInline(s[1 : 1+label]), URL: strings.TrimSpace(rest[1 : 1+link])}
	return node, label + link + 4 // [ label ] ( url )
}

// findClose returns the index of the next unescaped occurrence of delim, or -1.
func findClose(s, delim string) int {
	for i := 0; i+len(delim) <= len(s); {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if strings.HasPrefix(s[i:], delim) {
			return i
		}
		i++
	}

	return -1
}

// isWordByte reports whether b is a word character for boundary checks: letters,
// digits, underscore, or any non-ASCII byte (multibyte letters).
func isWordByte(b byte) bool {
	return b == '_' || b >= 0x80 ||
		'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' }

func isAlphanumericByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}
