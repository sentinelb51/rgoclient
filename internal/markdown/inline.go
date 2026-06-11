package markdown

import "strings"

// PlainText returns the unformatted text of inline nodes. It is used for
// previews and for rendering atomic spans (spoilers, strikethrough) whose
// visuals show plain text.
func PlainText(nodes []Inline) string {
	var b strings.Builder
	writePlain(&b, nodes)
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
		}
	}
}

// escapable lists the punctuation that a preceding backslash turns into a
// literal character.
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
// the node and the number of bytes consumed, or (nil, 0) if none matches.
// Order matters: longer delimiters are tried before their single-character
// counterparts (** before *, __ before _). prevWord reports whether the byte
// before s is a word character: Discord treats _ as a word character, so
// underscore emphasis only opens at a word boundary (snake_case_names stay
// literal) while * needs no boundary (2*3*4 italicizes the 3, as on Discord).
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
	}
	return nil, 0
}

// matchEmphasis matches single-delimiter emphasis (*x* or _x_) with guards
// against the false positives the loose double-delimiter wrap would accept:
// the content must not start or end with whitespace ("5 * 3 * 4" stays
// literal), and underscore emphasis must also close at a word boundary
// ("_open_world" stays literal; the opening boundary is the caller's check).
// A rejected closing delimiter lazily extends the span to the next one, so
// "_foo_bar_" italicizes "foo_bar" like Discord.
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

// isWordByte reports whether b is a word character for boundary checks:
// letters, digits, underscore, or any non-ASCII byte (multibyte letters).
func isWordByte(b byte) bool {
	return b == '_' || b >= 0x80 ||
		'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t'
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

// findClose returns the index of the next unescaped occurrence of delim in s,
// or -1 if there is none.
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

// matchCode matches an inline code span. A run of N backticks opens it and the
// next run of exactly N backticks closes it; the contents are literal.
func matchCode(s string) (Inline, int) {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	fence := s[:n]
	rest := s[n:]
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
