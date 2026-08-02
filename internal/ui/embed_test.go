package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui/theme"
)

// embedCardWidth is what a card built from embed comes out to, laid out.
func embedCardWidth(t *testing.T, embed *domain.Embed) float32 {
	t.Helper()

	card := buildEmbed(testDeps(), embed, func(*fyne.PointEvent) {})
	card.Resize(card.MinSize())

	return card.Size().Width
}

// TestEmbedCardIsSizedToWhatItSays covers the one piece of geometry a card has.
// Wrapping text cannot be asked how wide it wants to be — it answers with
// whatever it was last given — so the width is measured from the text as a
// single line and capped. A short embed therefore draws a small card, and a long
// one stops at the cap and wraps inside it rather than running the width of the
// message area.
func TestEmbedCardIsSizedToWhatItSays(t *testing.T) {
	styledApp(t)

	chrome := 2*theme.Sizes.EmbedPaddingH + theme.Sizes.EmbedAccentWidth + theme.Sizes.EmbedAccentGap
	capped := theme.Sizes.EmbedMaxWidth + chrome

	short := embedCardWidth(t, &domain.Embed{Kind: domain.EmbedText, Title: "Hi"})
	if short >= capped {
		t.Errorf("a two-word card is %vpx wide, want less than the capped %v", short, capped)
	}
	if short <= chrome {
		t.Errorf("a two-word card is %vpx wide, which is no room for the words", short)
	}

	long := embedCardWidth(t, &domain.Embed{
		Kind:        domain.EmbedText,
		Title:       "About Embeds",
		Description: strings.Repeat("a description that goes on well past any sensible card width. ", 8),
	})
	if long != capped {
		t.Errorf("a long card is %vpx wide, want the cap %v", long, capped)
	}
}

// unfurledEmbed is a link preview with every text field filled in — the card
// with the most parts, and so the one with the most ways to swallow a pointer
// event that belongs to the message underneath.
func unfurledEmbed() *domain.Embed {
	return &domain.Embed{
		Kind:        domain.EmbedWebsite,
		URL:         "https://example.test/article",
		SiteName:    "Example",
		Title:       "An article",
		Description: "what the page says",
	}
}

// TestEmbedCardLeavesTheRowItsHover is the innermost-hoverable rule applied to a
// card: Fyne delivers hover to the deepest hoverable object, so anything inside
// an embed that accepted it would take the hover off the message row — and with
// it the row's highlight and its quick-action buttons — every time the pointer
// crossed the card.
func TestEmbedCardLeavesTheRowItsHover(t *testing.T) {
	styledApp(t)

	card := buildEmbed(testDeps(), unfurledEmbed(), func(*fyne.PointEvent) {})
	card.Resize(card.MinSize())

	walkTree(card, func(obj fyne.CanvasObject, _ fyne.Position) {
		if _, ok := obj.(desktop.Hoverable); ok {
			t.Errorf("%T inside an embed accepts hover, which takes it off the message row", obj)
		}
	})
}

// TestEmbedTitleHandsBackTheRightClick covers the other half: the title is a
// widget because it has somewhere to go when tapped, and a widget under the
// pointer is what answers a right-click — so it has to raise the message's menu
// rather than nothing at all.
func TestEmbedTitleHandsBackTheRightClick(t *testing.T) {
	styledApp(t)

	raised := false
	card := buildEmbed(testDeps(), unfurledEmbed(), func(*fyne.PointEvent) { raised = true })
	card.Resize(card.MinSize())

	var title *embedLink
	walkTree(card, func(obj fyne.CanvasObject, _ fyne.Position) {
		if link, ok := obj.(*embedLink); ok {
			title = link
		}
	})

	if title == nil {
		t.Fatal("the card drew no tappable title")
	}
	title.TappedSecondary(&fyne.PointEvent{})

	if !raised {
		t.Error("right-clicking the title raised no menu")
	}
}

// TestBareImageEmbedDrawsNoCard covers the one embed that is not a card: a
// picture with nothing said about it gets no frame, no stripe and no padding —
// only the picture, which would otherwise sit inside an empty box.
func TestBareImageEmbedDrawsNoCard(t *testing.T) {
	styledApp(t)

	embed := &domain.Embed{
		Kind:  domain.EmbedImage,
		Image: &domain.File{URL: "https://example.test/photo.jpg", Kind: domain.FileImage, Width: 200, Height: 100},
	}

	card := buildEmbed(testDeps(), embed, func(*fyne.PointEvent) {})
	card.Resize(card.MinSize())

	var stripes int
	walkTree(card, func(obj fyne.CanvasObject, _ fyne.Position) {
		if rect, ok := obj.(*canvas.Rectangle); ok && rect.FillColor == theme.Colors.EmbedAccent {
			stripes++
		}
	})
	if stripes != 0 {
		t.Errorf("drew %d accent stripes around a bare picture, want none", stripes)
	}

	// The placeholder is sized to the picture, so the row it mounts in is exactly
	// as tall as what is coming.
	if got := card.MinSize(); got.Width != 200 || got.Height != 100 {
		t.Errorf("the picture reserves %v, want its own 200x100", got)
	}
}

// TestEmbedDescriptionRendersMarkdown checks an embed's text goes through the
// same renderer a message body does, rather than being drawn as the source it
// arrived as: the words survive and the syntax around them does not.
func TestEmbedDescriptionRendersMarkdown(t *testing.T) {
	styledApp(t)

	embed := &domain.Embed{Kind: domain.EmbedText, Title: "Card", Description: "**bold** and _italic_"}
	card := buildEmbed(testDeps(), embed, func(*fyne.PointEvent) {})
	card.Resize(card.MinSize())

	var drawn strings.Builder
	walkTree(card, func(obj fyne.CanvasObject, _ fyne.Position) {
		switch text := obj.(type) {
		case *canvas.Text:
			drawn.WriteString(text.Text)
		case *bodyText:
			drawn.WriteString(text.Text)
		}
	})

	if !strings.Contains(drawn.String(), "bold") {
		t.Errorf("the description was not drawn at all: %q", drawn.String())
	}
	if strings.ContainsAny(drawn.String(), "*_") {
		t.Errorf("the markdown source was drawn verbatim: %q", drawn.String())
	}
}
