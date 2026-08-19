package theme

// User overrides for the two tables. What changed is stored as field names and
// values rather than as a whole palette, so a size added later arrives with its
// default intact rather than pinned to whatever the file was written with.
//
// Fields are reached by reflection: the alternative is a switch with 174 cases
// extended by hand every time a name is added, the one place the tables would
// fall out of step with what the client can configure. Apply runs once per
// change, never on a read path.

import (
	"fmt"
	"image/color"
	"log"
	"reflect"
	"strconv"
	"strings"
)

// defaults are captured before anything can write to the tables. Both are
// anonymous structs, so a plain assignment is a complete copy.
var (
	defaultColors = Colors
	defaultSizes  = Sizes
)

// fontSize is what AppTheme.Size answers for SizeNameText. Zero leaves Fyne's
// own default in place.
var fontSize float32

// selectionAlpha is how much of the accent a text selection keeps. Any more and
// the glyphs under it stop being legible.
const selectionAlpha = 90

// accentTint is how far towards white the accent is lifted for text drawn in it.
// A mention or an embed title sits on a dark surface, where the accent itself is
// too close to the background to read as a link.
const accentTint = 0.35

// Apply resets both tables to their defaults and writes the named overrides over
// them. An unknown name or an unparseable value is logged and skipped: the
// settings file is hand-editable, so it must not be able to stop the client
// starting. Call on the UI thread and rebuild the widget tree afterwards — the
// tables are read at construction, so nothing already drawn changes on its own.
func Apply(colors map[string]string, sizes map[string]float32) {
	Colors = defaultColors
	Sizes = defaultSizes

	table := reflect.ValueOf(&Sizes).Elem()
	for name, value := range sizes {
		field := table.FieldByName(name)
		if !field.IsValid() {
			log.Printf("theme: unknown size %q", name)
			continue
		}

		field.SetFloat(float64(value))
	}

	palette := reflect.ValueOf(&Colors).Elem()
	for name, hex := range colors {
		field := palette.FieldByName(name)
		if !field.IsValid() {
			log.Printf("theme: unknown colour %q", name)
			continue
		}

		parsed, ok := ParseHex(hex)
		if !ok {
			log.Printf("theme: colour %q: cannot parse %q", name, hex)
			continue
		}

		field.Set(reflect.ValueOf(parsed))
	}

	selectionTint = withAlpha(Colors.ServerSelectedBg, selectionAlpha)
}

// SetFontSize sets what Fyne's built-in widgets draw text at. Zero restores
// Fyne's default. The client's own text is sized by named entries in the table.
func SetFontSize(size float32) {
	fontSize = size
}

/* Enumerating the tables */

// SizeFields lists every entry in the size table, in the order it is declared —
// which is the order the table groups them in, so a generated list reads the way
// the file does.
func SizeFields() []string {
	return fieldNames(reflect.TypeOf(Sizes))
}

// ColorFields lists every entry in the palette, in declaration order.
func ColorFields() []string {
	return fieldNames(reflect.TypeOf(Colors))
}

// DefaultSize is the compiled-in value of a named size.
func DefaultSize(name string) (float32, bool) {
	field := reflect.ValueOf(defaultSizes).FieldByName(name)
	if !field.IsValid() {
		return 0, false
	}

	return float32(field.Float()), true
}

// DefaultColor is the compiled-in value of a named colour.
func DefaultColor(name string) (color.Color, bool) {
	field := reflect.ValueOf(defaultColors).FieldByName(name)
	if !field.IsValid() {
		return nil, false
	}

	return field.Interface().(color.Color), true
}

// Size is the current value of a named size.
func Size(name string) (float32, bool) {
	field := reflect.ValueOf(Sizes).FieldByName(name)
	if !field.IsValid() {
		return 0, false
	}

	return float32(field.Float()), true
}

// Color is the current value of a named colour.
func Color(name string) (color.Color, bool) {
	field := reflect.ValueOf(Colors).FieldByName(name)
	if !field.IsValid() {
		return nil, false
	}

	return field.Interface().(color.Color), true
}

func fieldNames(t reflect.Type) []string {
	names := make([]string, t.NumField())
	for i := range names {
		names[i] = t.Field(i).Name
	}

	return names
}

/* Accent */

// AccentColors are the palette entries one accent colour drives. Choosing an
// accent in the Interface section writes all of them, so they remain ordinary
// overrides that the Advanced section can still edit one at a time.
var AccentColors = []string{
	"ServerSelectedBg",
	"ComposerBorderFocus",
	"NoticeInfo",
	"ReplyMentionActive",
	"MentionText",
	"JumpBarAction",
	"EmbedTitle",
}

// AccentOverrides expands an accent into the overrides that carry it. The three
// text entries are lifted towards white and the reply highlight is dropped to a
// tint, because those four are drawn against the dark surfaces rather than
// filling one.
func AccentOverrides(accent string) map[string]string {
	base, ok := ParseHex(accent)
	if !ok {
		return nil
	}

	hex := Hex(base)
	text := Hex(Lighten(base, accentTint))

	return map[string]string{
		"ServerSelectedBg":    hex,
		"ComposerBorderFocus": hex,
		"NoticeInfo":          hex,
		"ReplyMentionActive":  Hex(withAlpha(base, 70)),
		"MentionText":         text,
		"JumpBarAction":       text,
		"EmbedTitle":          text,
	}
}

/* Hex */

// ParseHex reads "#RRGGBB" or "#RRGGBBAA", with or without the hash.
func ParseHex(s string) (color.Color, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 && len(s) != 8 {
		return nil, false
	}

	value, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil, false
	}

	if len(s) == 6 {
		value = value<<8 | 0xFF
	}

	return color.RGBA{
		R: uint8(value >> 24),
		G: uint8(value >> 16),
		B: uint8(value >> 8),
		A: uint8(value),
	}, true
}

// Hex formats a colour as "#RRGGBB", or "#RRGGBBAA" when it is not opaque. It
// round-trips ParseHex because toRGBA preserves straight alpha — see there.
func Hex(c color.Color) string {
	rgba := toRGBA(c)

	if rgba.A == 0xFF {
		return fmt.Sprintf("#%02X%02X%02X", rgba.R, rgba.G, rgba.B)
	}

	return fmt.Sprintf("#%02X%02X%02X%02X", rgba.R, rgba.G, rgba.B, rgba.A)
}

// Fade scales a colour's alpha, leaving its channels where they are. The palette
// writes straight alpha into color.RGBA — see Hex — so the channel is scaled in
// place rather than read back through the color.Color interface, which
// premultiplies and would darken what it returned.
func Fade(c color.Color, factor float32) color.Color {
	rgba := toRGBA(c)
	rgba.A = uint8(float32(rgba.A) * factor)

	return rgba
}

func withAlpha(c color.Color, alpha uint8) color.Color {
	rgba := toRGBA(c)
	rgba.A = alpha

	return rgba
}

// Lighten walks a colour amount of the way to white, leaving its alpha alone. It
// is how a surface already carrying a colour of its own — a button filled with a
// tone — answers the pointer, there being no palette entry for "that colour, one
// step up".
func Lighten(c color.Color, amount float64) color.Color {
	rgba := toRGBA(c)
	lift := func(channel uint8) uint8 {
		return channel + uint8(float64(255-channel)*amount)
	}

	return color.RGBA{R: lift(rgba.R), G: lift(rgba.G), B: lift(rgba.B), A: rgba.A}
}

// toRGBA reads a colour's channels with its alpha left *straight*, which is what
// every caller here needs and what the color.Color interface will not give.
//
// The palette writes straight alpha into color.RGBA, so a palette entry is
// returned as it was written. RGBA() is the fallback for anything else, and it
// reports channels already multiplied by the alpha — so a translucent entry taken
// that way comes back darker than it was written and never round-trips. Hence the
// assertion first.
func toRGBA(c color.Color) color.RGBA {
	if rgba, ok := c.(color.RGBA); ok {
		return rgba
	}

	r, g, b, a := c.RGBA()

	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}
