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
		if node, width := matchInline(s[i:]); node != nil {
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
// counterparts (** before *, __ before _).
func matchInline(s string) (Inline, int) {
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
		return wrap(s, "*", func(c []Inline) Inline { return &Emphasis{Children: c} })
	case s[0] == '_':
		return wrap(s, "_", func(c []Inline) Inline { return &Emphasis{Children: c} })
	case s[0] == '[':
		return matchLink(s)
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
