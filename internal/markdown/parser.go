package markdown

import (
	"strconv"
	"strings"
	"time"
)

// Parse turns a raw message string into a Document.
func Parse(input string) *Document {
	input = strings.ReplaceAll(input, "\r\n", "\n")

	return &Document{Blocks: parseBlocks(strings.Split(input, "\n"))}
}

// PlainText returns the unformatted text of inline nodes, for previews and for
// spans whose visuals show plain text.
func PlainText(nodes []Inline) string { return PlainTextNamed(nodes, nil) }

// DocumentText returns a whole document as one run of unformatted text, blocks
// joined by a space — for a preview with room for a sentence rather than a body.
func DocumentText(doc *Document) string { return DocumentTextNamed(doc, nil) }

// PlainTextNamed and DocumentTextNamed are those two for a caller that can name
// a custom emoji — which the parser cannot, a shortcode being held by the server
// and this package having no session. name answers a shortcode or "" for one the
// caller has no server for, and a nil namer answers "" for every one.
func PlainTextNamed(nodes []Inline, name func(emojiID string) string) string {
	f := flatten{name: name}
	f.inlines(nodes)

	return f.b.String()
}

func DocumentTextNamed(doc *Document, name func(emojiID string) string) string {
	f := flatten{name: name}
	f.blocks(doc.Blocks)

	return f.b.String()
}

// flatten writes a document out as one line. It carries the namer rather than
// passing it down, the walk being recursive through both blocks and inlines.
type flatten struct {
	b    strings.Builder
	name func(emojiID string) string
}

// Links reports every URL a document links to, in reading order and with
// duplicates kept — masked, bracketed and bare URLs all parse to a Link.
//
// Walking the tree rather than scanning the source is the point: a URL inside a
// code span or a fenced block is text somebody wrote about a link, not a link,
// and nothing there is a Link node to find.
func Links(doc *Document) []string {
	var found []string
	eachInline(doc.Blocks, func(nodes []Inline) { collectInlineLinks(&found, nodes) })

	return found
}

// eachInline visits every run of inline content in blocks, in reading order,
// descending into quotes and list items.
func eachInline(blocks []Block, visit func([]Inline)) {
	for _, block := range blocks {
		switch v := block.(type) {
		case *Paragraph:
			visit(v.Children)
		case *Heading:
			visit(v.Children)
		case *Subtext:
			visit(v.Children)
		case *Blockquote:
			eachInline(v.Blocks, visit)
		case *List:
			for _, item := range v.Items {
				visit(item.Children)
			}
		}
	}
}

func collectInlineLinks(found *[]string, nodes []Inline) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *Link:
			*found = append(*found, v.URL)
		case *Strong:
			collectInlineLinks(found, v.Children)
		case *Emphasis:
			collectInlineLinks(found, v.Children)
		case *Underline:
			collectInlineLinks(found, v.Children)
		case *Strike:
			collectInlineLinks(found, v.Children)
		case *Spoiler:
			// Not reported: a card unfurled from a spoiler would say what it hides.
		}
	}
}

// blocks appends each block's plain text, separated by a single space. The
// separator is written from what is already there rather than from the loop
// index, since a blockquote recurses and would otherwise open with a second one.
func (f *flatten) blocks(blocks []Block) {
	for _, block := range blocks {
		if s := f.b.String(); s != "" && s[len(s)-1] != ' ' {
			f.b.WriteByte(' ')
		}

		switch v := block.(type) {
		case *Paragraph:
			f.inlines(v.Children)
		case *Heading:
			f.inlines(v.Children)
		case *Subtext:
			f.inlines(v.Children)
		case *Blockquote:
			f.blocks(v.Blocks)
		case *CodeBlock:
			// The only text kept verbatim by the parser, so the only place a newline
			// can reach a preview — a canvas.Text draws one as a missing glyph.
			f.b.WriteString(strings.ReplaceAll(v.Text, "\n", " "))
		case *List:
			for i, item := range v.Items {
				if i > 0 {
					f.b.WriteByte(' ')
				}
				f.inlines(item.Children)
			}
		}
	}
}

func (f *flatten) inlines(nodes []Inline) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *Text:
			f.b.WriteString(v.Text)
		case *Code:
			f.b.WriteString(v.Text)
		case *LineBreak:
			f.b.WriteByte(' ')
		case *Strong:
			f.inlines(v.Children)
		case *Emphasis:
			f.inlines(v.Children)
		case *Underline:
			f.inlines(v.Children)
		case *Strike:
			f.inlines(v.Children)
		case *Spoiler:
			f.inlines(v.Children)
		case *Link:
			f.inlines(v.Children)
		case *UserMention:
			// No session here to name the ID, and the raw token would read worse than
			// nothing, so the marker alone stands in.
			f.b.WriteByte('@')
		case *ChannelMention:
			f.b.WriteByte('#')
		case *Emoji:
			f.emoji(v.EmojiID)
		case *Timestamp:
			// The reader's clock format is the renderer's to decide, so a preview
			// takes the plainest reading of the instant.
			f.b.WriteString(v.Time.Local().Format("2 Jan 2006 15:04"))
		}
	}
}

// emoji writes an emoji as the shortcode it is typed as, and writes nothing
// where the caller cannot name it: the name is held on the server, and the ID is
// 26 characters of noise in a line asked for because space was short.
func (f *flatten) emoji(emojiID string) {
	if f.name == nil {
		return
	}

	name := f.name(emojiID)
	if name == "" {
		return
	}

	f.b.WriteString(":" + name + ":")
}

/* Blocks */

// lineKind is a line's block role. It is decided once per line: parseBlocks
// switches on it and paragraph collection stops at anything that is not lineText,
// so classifying costs one pass rather than a predicate per block type.
type lineKind uint8

const (
	lineText lineKind = iota
	lineBlank
	lineFence
	lineHeading
	lineSubtext
	lineQuote
	lineList
)

// classify reports a line's block role and, for a heading, its level.
func classify(line string) (lineKind, int) {
	if line == "" {
		return lineBlank, 0
	}

	switch line[0] {
	case ' ', '\t':
		if strings.TrimSpace(line) == "" {
			return lineBlank, 0
		}
		if isListItem(line) {
			return lineList, 0
		}
	case '`':
		if strings.HasPrefix(line, "```") {
			return lineFence, 0
		}
	case '#':
		if level := headingLevel(line); level > 0 {
			return lineHeading, level
		}
	case '-':
		if strings.HasPrefix(line, "-# ") {
			return lineSubtext, 0
		}
		if isListItem(line) {
			return lineList, 0
		}
	case '>':
		if isQuote(line) {
			return lineQuote, 0
		}
	case '*', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if isListItem(line) {
			return lineList, 0
		}
	}

	return lineText, 0
}

// parseBlocks groups lines into block-level nodes.
func parseBlocks(lines []string) []Block {
	var blocks []Block

	for i := 0; i < len(lines); {
		kind, level := classify(lines[i])

		var block Block
		next := i + 1

		switch kind {
		case lineBlank:
			i++ // paragraph separator
			continue
		case lineFence:
			block, next = parseFence(lines, i)
		case lineHeading:
			block = &Heading{Level: level, Children: parseInline(lines[i][level+1:])}
		case lineSubtext:
			block = &Subtext{Children: parseInline(lines[i][3:])}
		case lineQuote:
			block, next = parseQuote(lines, i)
		case lineList:
			block, next = parseList(lines, i)
		default:
			block, next = parseParagraph(lines, i)
		}

		blocks = append(blocks, block)
		i = next
	}

	return blocks
}

func isQuote(line string) bool {
	return line == ">" || strings.HasPrefix(line, "> ") ||
		line == ">>>" || strings.HasPrefix(line, ">>> ")
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

// parseFence reads a fenced code block starting at i, returning the block and
// the index of the line after the closing fence.
func parseFence(lines []string, i int) (Block, int) {
	open := 3
	for open < len(lines[i]) && lines[i][open] == '`' {
		open++
	}
	info := lines[i][open:]

	// A backtick in the info string means there is no info string — CommonMark's
	// rule, and what makes a one-line ```snippet``` the code block Discord draws
	// rather than a fence whose language is the snippet.
	if end := strings.Index(info, "```"); end >= 0 {
		return &CodeBlock{Text: info[:end]}, i + 1
	}

	var body []string

	// A language is one token. Anything else is a first line the author meant, and
	// naming it the language would silently swallow it.
	language := strings.TrimSpace(info)
	if strings.ContainsAny(language, " \t") {
		body = append(body, info)
		language = ""
	}

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

// parseQuote reads a blockquote starting at i. A ">>>" line quotes everything
// that follows; otherwise consecutive "> " lines are grouped. Either way the
// quoted lines go back through parseBlocks, so a heading, list or fence inside a
// quote is the block it looks like — and a quote marker among them is a quote,
// one strip having left it there.
func parseQuote(lines []string, i int) (Block, int) {
	if line := lines[i]; line == ">>>" || strings.HasPrefix(line, ">>> ") {
		body := append([]string{strings.TrimPrefix(line[3:], " ")}, lines[i+1:]...)
		return &Blockquote{Blocks: parseBlocks(body)}, len(lines)
	}

	var body []string
	j := i
	for ; j < len(lines); j++ {
		if kind, _ := classify(lines[j]); kind != lineQuote {
			break
		}
		body = append(body, strings.TrimPrefix(strings.TrimPrefix(lines[j], ">"), " "))
	}

	return &Blockquote{Blocks: parseBlocks(body)}, j
}

// List indentation. Discord counts two spaces per level; the cap is there because
// the indent is drawn, and a paste with a runaway margin would otherwise push the
// text off the message column.
const (
	listIndentWidth = 2
	listMaxIndent   = 4
	listTabWidth    = 4
)

// listMarker is a list item's marker as read off the line.
type listMarker struct {
	ordered bool
	number  int
	indent  int
	content string
}

// listItem reports whether a line is a list item and, if so, its marker details
// and content.
func listItem(line string) (listMarker, bool) {
	var m listMarker

	i := 0
	for ; i < len(line) && (line[i] == ' ' || line[i] == '\t'); i++ {
		if line[i] == '\t' {
			m.indent += listTabWidth
			continue
		}
		m.indent++
	}
	m.indent = min(m.indent/listIndentWidth, listMaxIndent)

	s := line[i:]
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
		m.content = s[2:]
		return m, true
	}

	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(s) && s[digits] == '.' && s[digits+1] == ' ' {
		m.ordered = true
		m.number, _ = strconv.Atoi(s[:digits])
		m.content = s[digits+2:]

		return m, true
	}

	return listMarker{}, false
}

func isListItem(line string) bool {
	_, ok := listItem(line)
	return ok
}

// parseList reads consecutive list items starting at i. A change of marker kind
// ends the run — a bulleted list under a numbered one is two lists — while a
// change of depth does not, an indented item being a sublist of the same run.
// Numbering restarts at every depth, and the outermost one continues from the
// number the first item gave.
func parseList(lines []string, i int) (Block, int) {
	first, _ := listItem(lines[i])
	list := &List{Ordered: first.ordered, Start: first.number}

	var counters [listMaxIndent + 1]int

	j := i
	for ; j < len(lines); j++ {
		item, ok := listItem(lines[j])
		if !ok || item.ordered != list.Ordered {
			break
		}

		counters[item.indent]++
		for depth := item.indent + 1; depth <= listMaxIndent; depth++ {
			counters[depth] = 0
		}

		number := counters[item.indent]
		if item.indent == 0 {
			number += list.Start - 1
		}

		list.Items = append(list.Items, ListItem{
			Indent:   item.indent,
			Number:   number,
			Children: parseInline(item.content),
		})
	}

	return list, j
}

// parseParagraph collects consecutive plain lines into one paragraph. They are
// parsed as a single run rather than one at a time, which is what lets a span
// cross a newline the way Discord's does; the scanner turns the newline into the
// LineBreak that draws it.
func parseParagraph(lines []string, i int) (Block, int) {
	j := i
	for ; j < len(lines); j++ {
		if kind, _ := classify(lines[j]); kind != lineText {
			break
		}
	}

	return &Paragraph{Children: parseInline(strings.Join(lines[i:j], "\n"))}, j
}

/* Inlines */

// inlineSpecial marks the bytes that can begin an inline construct. Everything
// else is literal, so the scanner walks ordinary text one byte at a time with no
// call per byte and emits the run as a slice of the source — nothing is copied
// unless an escape has to be dropped out of it.
var inlineSpecial = func() (table [256]bool) {
	for _, b := range []byte("\\`*_~|[<:\n") {
		table[b] = true
	}

	return table
}()

// inlineScanner accumulates inline nodes while walking a run of text.
type inlineScanner struct {
	src   string
	start int // start of the pending literal run

	buf strings.Builder // the run so far, once an escape has been dropped from it
	out []Inline
}

// flush emits the pending literal run up to end and moves the run's start there.
func (p *inlineScanner) flush(end int) {
	switch {
	case p.buf.Len() > 0:
		p.buf.WriteString(p.src[p.start:end])
		p.out = append(p.out, &Text{Text: p.buf.String()})
		p.buf.Reset()
	case end > p.start:
		p.out = append(p.out, &Text{Text: p.src[p.start:end]})
	}

	p.start = end
}

// emit closes the literal run at at and appends a node of width bytes.
func (p *inlineScanner) emit(node Inline, at, width int) {
	p.flush(at)
	p.out = append(p.out, node)
	p.start = at + width
}

// parseInline tokenizes a run of text — a whole paragraph, quote or list item,
// newlines included — into inline nodes. The run arrives whole rather than a line
// at a time because a Discord span crosses a line break, and only a scanner that
// can see past the newline can match one that does.
func parseInline(s string) []Inline {
	p := inlineScanner{src: s}

	for i := 0; i < len(s); {
		if !inlineSpecial[s[i]] {
			i++
			continue
		}

		switch {
		case s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			p.buf.WriteString(s[p.start:i])
			p.buf.WriteByte(s[i+1])
			i += 2
			p.start = i

			continue
		case s[i] == '\n':
			p.emit(&LineBreak{}, i, 1)
			i++

			continue
		case s[i] == ':' && strings.HasPrefix(s[i:], "://"):
			if at, node, width := autolink(s, i, p.start); node != nil {
				p.emit(node, at, width)
				i = at + width

				continue
			}
		}

		node, width := matchInline(s[i:], i > 0 && isWordByte(s[i-1]))
		if node == nil {
			i++
			continue
		}

		p.emit(node, i, width)
		i += width
	}
	p.flush(len(s))

	return p.out
}

// matchInline tries to match a formatting construct at the start of s, returning
// the node and bytes consumed, or (nil, 0). Order matters: longer delimiters are
// tried before their single-character counterparts. prevWord reports whether the
// byte before s is a word character — _ is itself a word character, so underscore
// emphasis only opens at a word boundary (snake_case stays literal) while * needs
// no boundary (2*3*4 italicises the 3).
func matchInline(s string, prevWord bool) (Inline, int) {
	switch s[0] {
	case '`':
		return matchCode(s)
	case '|':
		if strings.HasPrefix(s, "||") {
			return wrap(s, "||", func(c []Inline) Inline { return &Spoiler{Children: c} })
		}
	case '*':
		if strings.HasPrefix(s, "**") {
			return wrap(s, "**", func(c []Inline) Inline { return &Strong{Children: c} })
		}
		return matchEmphasis(s, "*", false)
	case '_':
		if strings.HasPrefix(s, "__") {
			return wrap(s, "__", func(c []Inline) Inline { return &Underline{Children: c} })
		}
		if !prevWord {
			return matchEmphasis(s, "_", true)
		}
	case '~':
		if strings.HasPrefix(s, "~~") {
			return wrap(s, "~~", func(c []Inline) Inline { return &Strike{Children: c} })
		}
	case '[':
		return matchLink(s)
	case '<':
		return matchAngle(s)
	case ':':
		return matchEmoji(s)
	}

	return nil, 0
}

/* Mentions and emoji */

// mentionIDMaxLen bounds how far matchReference looks for the closing '>'.
// Revolt IDs are 26-character ULIDs; the slack is there so a future ID format
// doesn't silently stop rendering, while "<@" in ordinary prose still falls
// through to literal text after a few bytes.
const mentionIDMaxLen = 64

// matchAngle matches what opens with '<': a mention, a timestamp, or a bracketed
// URL. A future kind of mention is one more case here and one more node in
// markdown.go — the ID rule and everything downstream is already shared.
func matchAngle(s string) (Inline, int) {
	if len(s) < 2 {
		return nil, 0
	}

	switch s[1] {
	case '@':
		return matchReference(s, func(id string) Inline { return &UserMention{UserID: id} })
	case '#':
		return matchReference(s, func(id string) Inline { return &ChannelMention{ChannelID: id} })
	case 't':
		// Falls through on a miss rather than returning: 't' is also a scheme byte,
		// so <tftp://host> reaches here and is a bracketed URL.
		if node, width := matchTimestamp(s); node != nil {
			return node, width
		}
	}

	return matchAngleURL(s)
}

// matchReference matches a <@id> or <#id> reference, handing the ID to build.
// The ID must be a non-empty run of alphanumerics, so "<@ someone>" and other
// prose opening with a delimiter stays literal.
func matchReference(s string, build func(id string) Inline) (Inline, int) {
	for i := 2; i < len(s) && i-2 <= mentionIDMaxLen; i++ {
		switch {
		case s[i] == '>':
			if i == 2 {
				return nil, 0 // "<@>" carries no ID
			}
			return build(s[2:i]), i + 1
		case !isAlphanumericByte(s[i]):
			return nil, 0
		}
	}

	return nil, 0
}

// timestampMaxDigits bounds the seconds a timestamp may carry. It is what stops
// a run of digits inside prose that opened with "<t:" from being scanned to the
// end of the body; anything this long is not an instant anybody meant.
const timestampMaxDigits = 20

// matchTimestamp matches a <t:1700000000> or <t:1700000000:F> instant. The style
// is validated rather than taken verbatim: an unknown one has no rendering to
// fall back to, and the default would silently show the wrong face of the
// instant where staying literal shows what the author typed.
func matchTimestamp(s string) (Inline, int) {
	if !strings.HasPrefix(s, "<t:") {
		return nil, 0
	}

	i := len("<t:")
	if i < len(s) && s[i] == '-' {
		i++ // an instant before the epoch
	}

	digits := i
	for i < len(s) && i-digits < timestampMaxDigits && '0' <= s[i] && s[i] <= '9' {
		i++
	}
	if i == digits {
		return nil, 0
	}

	seconds, err := strconv.ParseInt(s[len("<t:"):i], 10, 64)
	if err != nil {
		return nil, 0
	}

	style := ""
	if i < len(s) && s[i] == ':' {
		if i+2 >= len(s) || !isTimestampStyle(s[i+1]) {
			return nil, 0
		}
		style = s[i+1 : i+2]
		i += 2
	}
	if i >= len(s) || s[i] != '>' {
		return nil, 0
	}

	return &Timestamp{Time: time.Unix(seconds, 0), Style: style}, i + 1
}

// isTimestampStyle reports whether b names one of the faces an instant can be
// drawn as.
func isTimestampStyle(b byte) bool {
	switch b {
	case 't', 'T', 'd', 'D', 'f', 'F', 'R':
		return true
	}

	return false
}

// emojiIDLen is the exact length of the ULID between a custom emoji's colons.
// Nothing looser will do: unlike "<@" a colon is ordinary punctuation, so a
// length range would turn "10:30:00" into an emoji, and matching a shortcode
// nobody serves a picture for would leave a blank square in the sentence.
const emojiIDLen = 26

// matchEmoji matches a :01J9WN3PHX4ZQSNSZH10CK4RHS: custom emoji.
func matchEmoji(s string) (Inline, int) {
	width := emojiIDLen + 2
	if len(s) < width || s[width-1] != ':' {
		return nil, 0
	}

	for i := 1; i < width-1; i++ {
		if !isAlphanumericByte(s[i]) {
			return nil, 0
		}
	}

	return &Emoji{EmojiID: s[1 : width-1]}, width
}

/* Links */

// Bounds on the look-back for a bare URL's scheme. The minimum is what keeps a
// one-letter false positive — a Windows drive, a label — from becoming a link.
const (
	urlSchemeMinLen = 2
	urlSchemeMaxLen = 16

	// urlMaxLen bounds the search for a bracketed URL's closing '>'.
	urlMaxLen = 2048
)

// autolink matches a bare URL whose "://" begins at colon, returning where its
// scheme began. It is the one construct matched from behind — the scheme sits
// before the delimiter that announces it — so the scanner calls it rather than
// matchInline. min is the start of the pending literal run, which is what stops
// the look-back reaching into a node already emitted.
func autolink(s string, colon, min int) (start int, node Inline, width int) {
	start = colon
	for start > min && colon-start < urlSchemeMaxLen && isSchemeByte(s[start-1]) {
		start--
	}
	if colon-start < urlSchemeMinLen {
		return 0, nil, 0
	}

	host := colon + len("://")
	end := host
	for end < len(s) && !isURLEnd(s[end]) {
		end++
	}

	end = trimURLTail(s, host, end)
	if end == host {
		return 0, nil, 0 // a scheme with no host is not a link
	}

	raw := s[start:end]

	return start, &Link{Children: []Inline{&Text{Text: raw}}, URL: raw}, end - start
}

// matchAngleURL matches a <https://…> link. The brackets bound a URL that would
// otherwise stop at whatever punctuation the sentence puts after it, and on
// Revolt they are also what suppresses the embed.
func matchAngleURL(s string) (Inline, int) {
	// A scheme byte first, and a bounded window for the '>': every '<' in a body
	// reaches here, and an unbounded search per one would make prose full of them
	// quadratic.
	if len(s) < 2 || !isSchemeByte(s[1]) {
		return nil, 0
	}

	end := strings.IndexByte(s[:min(len(s), urlMaxLen)], '>')
	if end < 0 {
		return nil, 0
	}

	raw := s[1:end]
	scheme := strings.Index(raw, "://")
	if scheme <= 0 || scheme+len("://") == len(raw) || strings.ContainsAny(raw, " \t\n<") {
		return nil, 0
	}
	for i := 0; i < scheme; i++ {
		if !isSchemeByte(raw[i]) {
			return nil, 0
		}
	}

	return &Link{Children: []Inline{&Text{Text: raw}}, URL: raw}, end + 1
}

// matchLink matches a [label](destination "title") masked link.
func matchLink(s string) (Inline, int) {
	label, ok := matchLabel(s)
	if !ok || label == 1 {
		return nil, 0 // unterminated, or nothing to show
	}

	rest := s[label+1:]
	if len(rest) == 0 || rest[0] != '(' {
		return nil, 0
	}

	destination, width, ok := linkDestination(rest)
	if !ok {
		return nil, 0
	}

	return &Link{Children: parseInline(s[1:label]), URL: destination}, label + 1 + width
}

// matchLabel returns the index of a link label's closing ']'. Brackets nest, so
// the label of "[see [1]](u)" is the whole of "see [1]".
func matchLabel(s string) (int, bool) {
	depth := 0
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && isPunct(s[i+1]) {
				i++
			}
		case '[':
			depth++
		case ']':
			if depth == 0 {
				return i, true
			}
			depth--
		}
	}

	return 0, false
}

// linkDestination reads the "(…)" of a masked link, given s starting at the open
// paren. The destination may be wrapped in <> or carry balanced parentheses — an
// article ending in "(disambiguation)" is what a scan to the first ')' truncates
// — and a trailing "title" is accepted and dropped, nothing here showing one.
func linkDestination(s string) (destination string, width int, ok bool) {
	i := 1
	for i < len(s) && isSpaceByte(s[i]) {
		i++
	}

	if i < len(s) && s[i] == '<' {
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			return "", 0, false
		}
		destination = s[i+1 : i+end]
		i += end + 1
	} else {
		depth := 0
		start := i
		for ; i < len(s); i++ {
			b := s[i]
			if b == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
				i++
				continue
			}
			if isSpaceByte(b) {
				break
			}
			if b == '(' {
				depth++
				continue
			}
			if b == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
		}
		destination = s[start:i]
	}

	for i < len(s) && isSpaceByte(s[i]) {
		i++
	}
	if i < len(s) && (s[i] == '"' || s[i] == '\'') {
		end := strings.IndexByte(s[i+1:], s[i])
		if end < 0 {
			return "", 0, false
		}
		i += end + 2
	}

	if destination == "" || i >= len(s) || s[i] != ')' {
		return "", 0, false
	}

	return unescape(destination), i + 1, true
}

// isSchemeByte reports whether b can appear in a URL scheme.
func isSchemeByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// isURLEnd reports whether b ends a bare URL. Only what a URL may not carry
// unencoded stops it — * and _ are legal in a path, so a link is never cut short
// by a byte that would elsewhere open emphasis.
func isURLEnd(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '<', '>', '"', '`', '|', '\\':
		return true
	}

	return false
}

// trimURLTail drops the punctuation a sentence leaves on the end of a bare URL.
// A closing parenthesis stays when the URL opened one, which is the difference
// between "(see https://x)" and an article named "…_(disambiguation)".
func trimURLTail(s string, from, end int) int {
	for end > from {
		switch s[end-1] {
		case '.', ',', ';', ':', '!', '?', '\'':
			end--
		case ')':
			if strings.Count(s[from:end], "(") >= strings.Count(s[from:end], ")") {
				return end
			}
			end--
		default:
			return end
		}
	}

	return end
}

/* Spans */

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

// matchCode matches an inline code span: a run of N backticks opens it and the
// next run of *exactly* N closes it, so "`a“b`" is one span rather than two.
// The contents are literal — no escape is honoured inside one.
func matchCode(s string) (Inline, int) {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}

	for i := n; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}

		run := 0
		for i+run < len(s) && s[i+run] == '`' {
			run++
		}
		if run == n {
			return &Code{Text: s[n:i]}, i + n
		}
		i += run
	}

	return nil, 0
}

// findClose returns the index of the next unescaped occurrence of delim, or -1.
func findClose(s, delim string) int {
	for i := 0; i+len(delim) <= len(s); {
		switch {
		case s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			i += 2
		case s[i] == delim[0] && strings.HasPrefix(s[i:], delim):
			return i
		default:
			i++
		}
	}

	return -1
}

// unescape drops the backslashes a span carried through a scan that only had to
// step over them.
func unescape(s string) string {
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
			i++
		}
		b.WriteByte(s[i])
	}

	return b.String()
}

/* Byte classes */

// isPunct reports whether b is ASCII punctuation, which a preceding backslash
// turns literal. CommonMark's rule is the only one wide enough to cover the
// syntax this flavour adds on top — \: for an emoji, \< for a mention.
func isPunct(b byte) bool {
	return '!' <= b && b <= '/' || ':' <= b && b <= '@' ||
		'[' <= b && b <= '`' || '{' <= b && b <= '~'
}

// isWordByte reports whether b is a word character for boundary checks: letters,
// digits, underscore, or any non-ASCII byte (multibyte letters).
func isWordByte(b byte) bool {
	return b == '_' || b >= 0x80 ||
		'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

// isSpaceByte reports whether b is inline whitespace. A newline counts: a span
// now runs past one, and content edged by a line break is as much a false
// positive as content edged by a space.
func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }

func isAlphanumericByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}
