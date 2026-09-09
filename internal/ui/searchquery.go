package ui

// The channel-search query language: what the field means, and nothing about how
// it is drawn. `key:value[,value…]`, with anything else the reader typed left as
// the free text the route is actually asked for.
//
// One rule: **or within a key, and across keys**. `has:image,video` is either,
// `has:image from:me` is both, and a key named twice merges into its own set —
// so a comma always widens and a space always narrows, whichever key it is.
// A span is the one exception and tightens on both, two windows only ever
// meaning their overlap.
//
// A token whose name is not a key is *text*, silently: a URL carries a colon,
// so does a time, so does an emoji shortcode, and warning about those would be
// noise on every third query. A known key handed a value it does not take is
// the case worth saying out loud, and is what Unknown carries.

import (
	"slices"
	"strings"
	"time"

	"RGOClient/internal/domain"
)

/* Keys */

// SearchKey is one filter a query can name. KeyNone is a token that named none,
// which is a word to search for.
type SearchKey uint8

const (
	KeyNone SearchKey = iota
	KeyFrom
	KeyMentions
	KeyHas
	KeyIs
	KeyBefore
	KeyAfter
	KeyDuring
)

// searchKeys is every key, in the order the drawer offers them: who, then what,
// then when. Hint is the line under the name there — what the key asks, in the
// fewest words that say it.
var searchKeys = []struct {
	key  SearchKey
	name string
	hint string
}{
	{KeyFrom, "from", "who wrote it"},
	{KeyMentions, "mentions", "who it pings"},
	{KeyHas, "has", "what it carries"},
	{KeyIs, "is", "what kind of message"},
	{KeyAfter, "after", "on or after a day"},
	{KeyBefore, "before", "on or before a day"},
	{KeyDuring, "during", "a day, a month or a year"},
}

// searchKeyOf reads a name back, lowercased by the caller.
func searchKeyOf(name string) (SearchKey, bool) {
	for _, entry := range searchKeys {
		if entry.name == name {
			return entry.key, true
		}
	}

	return KeyNone, false
}

// searchKeyName is that inverse, "" for KeyNone.
func searchKeyName(key SearchKey) string {
	for _, entry := range searchKeys {
		if entry.key == key {
			return entry.name
		}
	}

	return ""
}

/* What a message carries, and what kind it is */

// SearchFilter is one condition a single bit can hold — a message either carries
// a picture or it does not. Who wrote it and when are values rather than bits and
// are fields on SearchTerms instead.
type SearchFilter uint8

const (
	/* has: */

	FilterFile SearchFilter = iota
	FilterImage
	FilterVideo
	FilterAudio
	FilterLink
	FilterEmbed
	FilterReaction
	FilterReply

	/* is: */

	FilterPinned
	FilterEdited
	FilterBot
	FilterSystem
)

// SearchFilters is a set of them, one bit each — a value, so two queries can be
// told apart with == rather than by walking a map.
type SearchFilters uint32

// Has reports whether filter is on.
func (f SearchFilters) Has(filter SearchFilter) bool { return f&(1<<filter) != 0 }

// Any reports whether anything is set.
func (f SearchFilters) Any() bool { return f != 0 }

func (f SearchFilters) with(filter SearchFilter, on bool) SearchFilters {
	if on {
		return f | 1<<filter
	}

	return f &^ (1 << filter)
}

// searchFlagValues is every value has: and is: take, in the order the drawer
// lists them. alias is the second spelling one answers to and is not offered —
// two chips meaning one bit would read as two conditions.
var searchFlagValues = []struct {
	key    SearchKey
	value  string
	alias  string
	filter SearchFilter
}{
	{KeyHas, "file", "attachment", FilterFile},
	{KeyHas, "image", "", FilterImage},
	{KeyHas, "video", "", FilterVideo},
	{KeyHas, "audio", "sound", FilterAudio},
	{KeyHas, "link", "", FilterLink},
	{KeyHas, "embed", "", FilterEmbed},
	{KeyHas, "reaction", "", FilterReaction},
	{KeyHas, "reply", "", FilterReply},

	{KeyIs, "pinned", "", FilterPinned},
	{KeyIs, "edited", "", FilterEdited},
	{KeyIs, "bot", "", FilterBot},
	{KeyIs, "system", "", FilterSystem},
}

// searchFlag resolves one value of has: or is:, lowercased by the caller.
func searchFlag(key SearchKey, value string) (SearchFilter, bool) {
	for _, entry := range searchFlagValues {
		if entry.key == key && (entry.value == value || entry.alias == value) {
			return entry.filter, true
		}
	}

	return 0, false
}

// searchFlagName is the spelling a filter is written back as — the canonical one,
// so a chip tap and a typed alias converge on one string.
func searchFlagName(filter SearchFilter) string {
	for _, entry := range searchFlagValues {
		if entry.filter == filter {
			return entry.value
		}
	}

	return ""
}

/* What a query means */

// SearchTerms is a field parsed: the words left over, and everything narrowing
// them. From and Mentions stay as the reader typed them — resolving a name to
// an account is a question about the store, which this package cannot ask.
//
// After and Before are half-open, After being the first instant kept and Before
// the first one dropped, and are the only narrowing here the route is actually
// sent.
type SearchTerms struct {
	Text string

	Has, Is SearchFilters

	From     []string
	Mentions []string

	After, Before time.Time

	// Unknown is every `key:value` naming a key that does not take that value —
	// `has:sticker`, `during:soon`. Said out loud by the island, a filter that
	// quietly matched everything being worse than one that refused.
	Unknown []string
}

// Narrowed reports whether anything at all is cutting the answer down.
func (t SearchTerms) Narrowed() bool {
	return t.Has.Any() || t.Is.Any() || len(t.From) > 0 || len(t.Mentions) > 0 ||
		!t.After.IsZero() || !t.Before.IsZero()
}

// Asks reports whether there is a question here at all. A field holding only
// filters is one — the client walks the channel's history for it — where an
// empty one is the prompt.
func (t SearchTerms) Asks() bool { return t.Text != "" || t.Narrowed() }

// holds reports whether one term is on, which is what lights a chip. Asked of
// the parse rather than of the text so an alias counts: `has:attachment` is the
// Files chip, there being one bit behind both spellings.
func (t SearchTerms) holds(key SearchKey, value string) bool {
	switch key {
	case KeyHas, KeyIs:
		filter, ok := searchFlag(key, strings.ToLower(value))
		if !ok {
			return false
		}
		if key == KeyHas {
			return t.Has.Has(filter)
		}

		return t.Is.Has(filter)
	case KeyFrom:
		return indexFold(t.From, value) >= 0
	case KeyMentions:
		return indexFold(t.Mentions, value) >= 0
	}

	return false
}

// ParseSearchTerms reads a field.
func ParseSearchTerms(raw string) SearchTerms {
	var (
		terms SearchTerms
		words []string
	)

	for _, token := range scanSearch(raw) {
		if token.key == KeyNone {
			if token.text != "" {
				words = append(words, token.text)
			}

			continue
		}

		for _, value := range splitValues(token.value) {
			terms.take(token.key, value)
		}
	}
	terms.Text = strings.Join(words, " ")

	return terms
}

// take files one value under its key.
func (t *SearchTerms) take(key SearchKey, value string) {
	switch key {
	case KeyFrom:
		t.From = append(t.From, value)
	case KeyMentions:
		t.Mentions = append(t.Mentions, value)
	case KeyHas, KeyIs:
		filter, ok := searchFlag(key, strings.ToLower(value))
		if !ok {
			t.reject(key, value)
			return
		}
		if key == KeyHas {
			t.Has = t.Has.with(filter, true)
		} else {
			t.Is = t.Is.with(filter, true)
		}
	case KeyBefore, KeyAfter, KeyDuring:
		t.takeDay(key, value)
	}
}

// takeDay narrows the span. Repeated bounds *tighten* rather than widening, a
// second window over the first only ever meaning where the two agree.
func (t *SearchTerms) takeDay(key SearchKey, value string) {
	from, until, ok := searchDay(strings.ToLower(value))
	if !ok {
		t.reject(key, value)
		return
	}

	if key == KeyAfter || key == KeyDuring {
		if t.After.IsZero() || from.After(t.After) {
			t.After = from
		}
	}
	if key == KeyBefore || key == KeyDuring {
		if t.Before.IsZero() || until.Before(t.Before) {
			t.Before = until
		}
	}
}

func (t *SearchTerms) reject(key SearchKey, value string) {
	t.Unknown = append(t.Unknown, searchKeyName(key)+":"+value)
}

// searchDay reads a value naming a day, a month or a year, and answers with the
// span it covers: the first instant in it and the first one after it. Local
// days, since that is the calendar the reader typed one from.
//
// Both bounds are inclusive of the day named — `before:` is "on or before", the
// same reading the drawer's own two fields are labelled with — which is why a
// day is answered as a span rather than as an instant.
func searchDay(value string) (from, until time.Time, ok bool) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	switch value {
	case "today":
		return today, today.AddDate(0, 0, 1), true
	case "yesterday":
		return today.AddDate(0, 0, -1), today, true
	}

	if day, err := time.ParseInLocation(dayEntryLayout, value, time.Local); err == nil {
		return day, day.AddDate(0, 0, 1), true
	}
	if month, err := time.ParseInLocation(monthEntryLayout, value, time.Local); err == nil {
		return month, month.AddDate(0, 1, 0), true
	}
	if year, err := time.ParseInLocation(yearEntryLayout, value, time.Local); err == nil {
		return year, year.AddDate(1, 0, 0), true
	}

	return time.Time{}, time.Time{}, false
}

/* Reading the field */

// searchToken is one whitespace-separated run of the field, and where it sits so
// the field can be rewritten around it. A token that named a key carries value;
// one that did not carries text, which is a word to search for.
type searchToken struct {
	start, end int

	key   SearchKey
	value string
	text  string
}

// scanSearch splits a field into tokens, honouring double quotes so a name with
// a space in it is one value.
func scanSearch(raw string) []searchToken {
	var tokens []searchToken

	for i := 0; i < len(raw); {
		for i < len(raw) && raw[i] == ' ' {
			i++
		}
		if i >= len(raw) {
			break
		}

		start, quoted := i, false
		for i < len(raw) {
			if raw[i] == '"' {
				quoted = !quoted
			} else if raw[i] == ' ' && !quoted {
				break
			}
			i++
		}

		tokens = append(tokens, readSearchToken(raw, start, i))
	}

	return tokens
}

// readSearchToken decides what one token is: a term where its name is a key, and
// a word otherwise. A leading colon is never a key — that is an emoji shortcode.
func readSearchToken(raw string, start, end int) searchToken {
	token := searchToken{start: start, end: end}
	body := raw[start:end]

	colon, quoted := -1, false
	for i := range len(body) {
		switch body[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted && colon < 0 {
				colon = i
			}
		}
	}

	key, ok := KeyNone, false
	if colon > 0 {
		key, ok = searchKeyOf(strings.ToLower(body[:colon]))
	}
	if !ok {
		token.text = unquote(body)
		return token
	}

	token.key, token.value = key, unquote(body[colon+1:])

	return token
}

// splitValues is one term's comma-separated values, empties dropped — a trailing
// comma is somebody still typing.
func splitValues(value string) []string {
	var values []string

	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}

	return values
}

// termValues is what the field currently says a key holds, read off the tokens
// rather than off the parse: a chip toggling one value must leave the other
// spellings the reader typed exactly as they are.
func termValues(raw string, key SearchKey) []string {
	var values []string

	for _, token := range scanSearch(raw) {
		if token.key == key {
			values = append(values, splitValues(token.value)...)
		}
	}

	return values
}

/* Completing what is being typed */

// searchCompletion is the part of the field the drawer is offering to finish:
// which key it belongs to, what has been typed of it, and where it begins so it
// can be replaced.
//
// The **end** of the field rather than the caret. A search box is typed at its
// end almost always, and reading the caret means converting Fyne's rune column
// against a byte offset on every keystroke to serve the case where somebody went
// back to correct a term — which they can still do, they simply do it without
// the drawer's help.
type searchCompletion struct {
	key    SearchKey
	prefix string // lowercased, so a chip run can be filtered by it directly
	start  int    // byte offset into the field where prefix begins
}

// completionIn reads that off a field. A field ending in a space is a new token
// beginning, which is a key to name rather than nothing.
func completionIn(raw string) searchCompletion {
	// A trailing space ends a token — unless it is inside a name somebody is still
	// quoting, where it is part of the value and not the end of anything.
	if raw == "" || (raw[len(raw)-1] == ' ' && !quoteOpen(raw)) {
		return searchCompletion{start: len(raw)}
	}

	tokens := scanSearch(raw)
	if len(tokens) == 0 {
		return searchCompletion{start: len(raw)}
	}
	token := tokens[len(tokens)-1]

	if token.key == KeyNone {
		return searchCompletion{prefix: strings.ToLower(token.text), start: token.start}
	}

	// Past the key's colon, and past the last comma after it: a list of values is
	// completed one value at a time. The first colon in the token is the key's —
	// the name carries no quotes, so nothing before it can hide one.
	body := raw[token.start:token.end]
	at := strings.IndexByte(body, ':') + 1
	if comma := strings.LastIndexByte(body, ','); comma >= at {
		at = comma + 1
	}

	return searchCompletion{
		key:    token.key,
		prefix: strings.ToLower(unquote(body[at:])),
		start:  token.start + at,
	}
}

// completeWith replaces the part being typed with text — the whole of what a
// drawer pick does to the field.
func completeWith(raw string, at searchCompletion, text string) string {
	return raw[:at.start] + text
}

// appendKey starts a term at the end of the field, which is what a chip standing
// for a *kind* of answer does rather than opening a panel directly: the drawer
// reads the field, so there is one thing deciding what it shows.
func appendKey(raw string, key SearchKey) string {
	if raw != "" && !strings.HasSuffix(raw, " ") {
		raw += " "
	}

	return raw + searchKeyName(key) + ":"
}

// clearSpan takes every bound off, all three keys naming one window.
func clearSpan(raw string) string {
	for _, key := range []SearchKey{KeyAfter, KeyBefore, KeyDuring} {
		raw = SetTerm(raw, key, "")
	}

	return raw
}

/* Writing the field */

// ToggleTerm adds value to key, or takes it off where it is already there — what
// a chip tap does. The field is the query, so a chip writes into it rather than
// holding a state of its own that the text could then disagree with.
func ToggleTerm(raw string, key SearchKey, value string) string {
	values := termValues(raw, key)
	if index := indexFold(values, value); index >= 0 {
		values = slices.Delete(values, index, index+1)
	} else {
		values = append(values, value)
	}

	return writeTerm(raw, key, values)
}

// SetTerm makes key hold value alone, or drops it entirely for an empty one —
// what a drawer's pick does, a person and a span being answers rather than
// conditions.
func SetTerm(raw string, key SearchKey, value string) string {
	var values []string
	if value != "" {
		values = []string{value}
	}

	return writeTerm(raw, key, values)
}

// writeTerm rewrites raw so key holds exactly values, in the place the key first
// appeared. Rebuilt rather than patched: a chip and a typed term have to produce
// the same string, or the run and the field come to disagree about what is on.
func writeTerm(raw string, key SearchKey, values []string) string {
	parts := make([]string, 0, 8)
	placed := false

	for _, token := range scanSearch(raw) {
		if token.key == key {
			if !placed && len(values) > 0 {
				parts = append(parts, formatTerm(key, values))
				placed = true
			}

			continue
		}

		parts = append(parts, raw[token.start:token.end])
	}
	if !placed && len(values) > 0 {
		parts = append(parts, formatTerm(key, values))
	}

	return strings.Join(parts, " ")
}

// formatTerm is one term written out, values quoted only where they need it.
func formatTerm(key SearchKey, values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = quoteValue(value)
	}

	return searchKeyName(key) + ":" + strings.Join(quoted, ",")
}

// quoteValue wraps a value the scanner would otherwise break apart.
func quoteValue(value string) string {
	if !strings.ContainsAny(value, ` ,"`) {
		return value
	}

	return `"` + strings.ReplaceAll(value, `"`, "") + `"`
}

// quoteOpen reports whether the field ends inside a quoted value.
func quoteOpen(raw string) bool { return strings.Count(raw, `"`)%2 == 1 }

func unquote(s string) string {
	if !strings.Contains(s, `"`) {
		return s
	}

	return strings.ReplaceAll(s, `"`, "")
}

// indexFold finds value among values, ignoring case — `has:Image` and the Images
// chip are the same condition.
func indexFold(values []string, value string) int {
	return slices.IndexFunc(values, func(held string) bool {
		return strings.EqualFold(held, value)
	})
}

/* The whole state of the island */

// SearchQuery is what the island last reported: the field verbatim, the order
// picked beside it, and the field parsed. Raw is the whole of the state — the
// chips write into it and read back out of it — and Terms is it parsed once,
// rather than every chip parsing it again to decide whether it is lit.
type SearchQuery struct {
	Raw   string
	Sort  domain.MessageSort
	Terms SearchTerms
}

// NewSearchQuery parses a field.
func NewSearchQuery(raw string, sort domain.MessageSort) SearchQuery {
	return SearchQuery{Raw: raw, Sort: sort, Terms: ParseSearchTerms(raw)}
}

// SameRequest reports whether q and other would be answered by the same request.
// The words, the order and the span are what is sent; everything else narrows
// what came back, so changing one of those costs nothing.
func (q SearchQuery) SameRequest(other SearchQuery) bool {
	return q.Terms.Text == other.Terms.Text && q.Sort == other.Sort &&
		q.Terms.After.Equal(other.Terms.After) && q.Terms.Before.Equal(other.Terms.Before)
}

// Narrowed reports whether anything is cutting the answer down. What Clear puts
// back.
func (q SearchQuery) Narrowed() bool { return q.Terms.Narrowed() }

// Asks reports whether the field holds a question.
func (q SearchQuery) Asks() bool { return q.Terms.Asks() }
