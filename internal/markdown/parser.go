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
// which terminates the paragraph currently being collected.
func isBlockStart(line string) bool {
	return strings.TrimSpace(line) == "" || isFence(line) || headingLevel(line) > 0 ||
		isSubtext(line) || isQuote(line) || isListItem(line)
}

func isFence(line string) bool { return strings.HasPrefix(line, "```") }

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

// headingLevel returns the header level (1-3) for a line, or 0 if it is not a
// header. Discord requires a space after the # run.
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

func isSubtext(line string) bool { return strings.HasPrefix(line, "-# ") }

func isQuote(line string) bool {
	return line == ">" || strings.HasPrefix(line, "> ") || strings.HasPrefix(line, ">>> ")
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

// listItem returns whether a line is a list item and, if so, its marker
// details and content.
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

// parseList reads consecutive list items starting at i. The list inherits its
// ordered-ness and start number from the first item.
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

// parseParagraph collects consecutive plain lines into one paragraph, joining
// them with hard line breaks.
func parseParagraph(lines []string, i int) (Block, int) {
	var body []string
	j := i
	for ; j < len(lines) && !isBlockStart(lines[j]); j++ {
		body = append(body, lines[j])
	}
	return &Paragraph{Children: parseInlineLines(body)}, j
}

// parseInlineLines parses each line's inline content, separating lines with
// LineBreak nodes.
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
