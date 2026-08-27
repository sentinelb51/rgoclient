package app

// The GIF picker's contents. Nothing is held here: every list is a request to a
// service beside Revolt's own, which announces nothing and so can only be asked
// again — the picker holds its answers for as long as it is open and this is the
// worker behind it.
//
// Which *rendition* a tile is drawn from is decided here rather than in ui, the
// way a keystroke's sound is: the service names its own formats, and what this
// client can put on screen is a question about Fyne.

import (
	"maps"
	"math"
	"slices"
	"strings"

	"fyne.io/fyne/v2"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
)

// gifPreviewOrder is the renditions a tile is preferred to be drawn from,
// smallest first: a grid of forty is forty pictures fetched, and the largest of
// them is several megabytes of a format Fyne draws one frame of. The names are
// Tenor's, which the service answers in.
var gifPreviewOrder = []string{
	"tinygif", "nanogif", "gifpreview", "tinygifpreview", "nanogifpreview", "mediumgif", "gif",
}

// gifVideoMarkers name a rendition image.Decode cannot be handed. The service
// prefers video — its own advice is WebM, then MP4, then GIF — and there is no
// player here, so those are the renditions to step over. Matched on the *name*
// and the URL both: a name is the provider's to choose and a URL need not carry
// an extension at all, so neither alone is a test.
var gifVideoMarkers = []string{"mp4", "webm", "mkv", "mov", "video"}

// OnPickGIF opens the GIF picker beside anchor and reports the page URL of
// whatever is chosen. Call on the UI thread.
func (a *App) OnPickGIF(anchor fyne.CanvasObject, onPick func(pageURL string)) {
	source := ui.GIFSource{
		Trending: func(done func([]ui.GIFChoice, error)) {
			a.fetchGIFs(func() (domain.GIFPage, error) { return a.client.TrendingGIFs("") }, done)
		},
		Search: func(query string, category bool, done func([]ui.GIFChoice, error)) {
			a.fetchGIFs(func() (domain.GIFPage, error) { return a.client.SearchGIFs(query, "", category) }, done)
		},
		Categories: a.fetchGIFCategories,
	}

	ui.ShowGIFPicker(a.deps(), anchor, source, func(choice ui.GIFChoice) { onPick(choice.PageURL) })
}

// fetchGIFs runs one list off the UI thread and answers on it. The failure is
// handed to the picker rather than raised as a notice: nothing was asked for
// out loud, and the line above the grid is where the reader is already looking.
func (a *App) fetchGIFs(ask func() (domain.GIFPage, error), done func([]ui.GIFChoice, error)) {
	epoch := a.epoch

	a.background(func() error {
		page, err := ask()
		choices := gifChoices(page)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			done(choices, err)
		}, false)

		return nil
	}, func(error) {})
}

func (a *App) fetchGIFCategories(done func([]ui.GIFCategory, error)) {
	epoch := a.epoch

	a.background(func() error {
		categories, err := a.client.GIFCategories()

		out := make([]ui.GIFCategory, 0, len(categories))
		for _, category := range categories {
			out = append(out, ui.GIFCategory{Title: category.Title, ImageURL: category.ImageURL})
		}

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}

			done(out, err)
		}, false)

		return nil
	}, func(error) {})
}

// gifChoices converts a page, dropping a result with nothing drawable in it: a
// tile is recognised by its picture, and an empty square is not a choice.
func gifChoices(page domain.GIFPage) []ui.GIFChoice {
	out := make([]ui.GIFChoice, 0, len(page.Results))

	for _, gif := range page.Results {
		preview, ok := gifPreview(gif)
		if !ok {
			continue
		}

		out = append(out, ui.GIFChoice{
			ID:            gif.ID,
			PageURL:       gif.PageURL,
			PreviewURL:    preview.URL,
			AnimatedURL:   gifAnimated(gif),
			PreviewWidth:  preview.Width,
			PreviewHeight: preview.Height,
		})
	}

	return out
}

// gifPreview picks the still a tile is drawn from: the named order first, then
// the smallest rendition that is not a video, whatever it is called. The names
// are the provider's rather than anything the wire format guarantees, so a set
// this client has never heard of still draws — which is the case worth covering,
// the alternative being an empty grid. Walked in sorted order so an unknown set
// answers the same way twice, a map's own order being random.
// The dimensions travel with it: they are what shapes the tile, so a rendition
// carrying them is worth more than the same picture without.
func gifPreview(gif domain.GIF) (domain.GIFFormat, bool) {
	for _, name := range gifPreviewOrder {
		if format, ok := gif.Formats[name]; ok && format.URL != "" {
			return format, true
		}
	}

	var best domain.GIFFormat
	smallest := 0

	for _, name := range slices.Sorted(maps.Keys(gif.Formats)) {
		format := gif.Formats[name]
		if format.URL == "" || gifPlays(name) || gifPlays(format.URL) {
			continue
		}

		// A rendition with no dimensions sorts last: it may be the full-size one.
		area := format.Width * format.Height
		if area == 0 {
			area = math.MaxInt
		}
		if best.URL == "" || area < smallest {
			best, smallest = format, area
		}
	}

	return best, best.URL != ""
}

// gifAnimatedOrder is the renditions a tile is animated from, smallest first.
// Only names known to be GIFs that move: the "...preview" names beside them in
// gifPreviewOrder are stills, and an unknown rendition cannot be told from one,
// so there is no fallback — a set naming none of these keeps a still tile.
var gifAnimatedOrder = []string{"tinygif", "nanogif", "mediumgif", "gif"}

// gifAnimated picks the rendition a tile plays under the pointer, or "".
func gifAnimated(gif domain.GIF) string {
	for _, name := range gifAnimatedOrder {
		if format, ok := gif.Formats[name]; ok && format.URL != "" {
			return format.URL
		}
	}

	return ""
}

// gifPlays reports whether a name or a URL says video.
func gifPlays(s string) bool {
	lowered := strings.ToLower(s)

	return slices.ContainsFunc(gifVideoMarkers, func(marker string) bool {
		return strings.Contains(lowered, marker)
	})
}
