package client

// The GIF service. It is not the Revolt API — it is a service of Stoat's beside
// it, holding the key to the provider it proxies and taking this session's token
// in place of one — so nothing here is cached, no event announces any of it, and
// every call is a request. Its rate limit is ten in ten seconds per route, which
// is what makes the caller's job to ask rarely.

import (
	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// gifPageLimit is how many results a page asks for. The service allows more; a
// picker draws what it is given, and a page nobody scrolls to the end of is a
// page of pictures fetched for nothing.
const gifPageLimit = 50

// SearchGIFs searches for GIFs. A position is `GIFPage.Next` from a previous
// page, and category says the query came from a heading rather than being typed.
func (c *Client) SearchGIFs(query, position string, category bool) (domain.GIFPage, error) {
	session := c.session.Load()
	if session == nil {
		return domain.GIFPage{}, ErrNoSession
	}

	page, err := session.GIFSearch(revoltgo.GIFSearchParams{
		Query:      query,
		Limit:      gifPageLimit,
		Position:   position,
		IsCategory: category,
	})
	if err != nil {
		return domain.GIFPage{}, err
	}

	return toGIFPage(page), nil
}

// TrendingGIFs is what the service is featuring, which is what a picker opens on.
func (c *Client) TrendingGIFs(position string) (domain.GIFPage, error) {
	session := c.session.Load()
	if session == nil {
		return domain.GIFPage{}, ErrNoSession
	}

	page, err := session.GIFTrending(revoltgo.GIFTrendingParams{
		Limit:    gifPageLimit,
		Position: position,
	})
	if err != nil {
		return domain.GIFPage{}, err
	}

	return toGIFPage(page), nil
}

// GIFCategories fetches the headings GIFs are browsable by.
func (c *Client) GIFCategories() ([]domain.GIFCategory, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}

	categories, err := session.GIFCategories("")
	if err != nil {
		return nil, err
	}

	out := make([]domain.GIFCategory, 0, len(categories))
	for _, category := range categories {
		if category == nil || category.Title == "" {
			continue
		}

		out = append(out, domain.GIFCategory{Title: category.Title, ImageURL: category.Image})
	}

	return out, nil
}

// toGIFPage converts a page, dropping a result with no page to send: the picture
// is what is chosen, but the page is what a message carries.
func toGIFPage(page revoltgo.GIFPage) domain.GIFPage {
	out := domain.GIFPage{Next: page.Next, Results: make([]domain.GIF, 0, len(page.Results))}

	for _, result := range page.Results {
		if result == nil || result.URL == "" {
			continue
		}

		out.Results = append(out.Results, domain.GIF{
			ID:      result.ID,
			PageURL: result.URL,
			Formats: toGIFFormats(result.MediaFormats),
		})
	}

	return out
}

// toGIFFormats copies the renditions across. Dimensions arrive as a slice rather
// than a pair and the service is free to send neither, so a short one leaves the
// picture to be measured by whatever decodes it.
func toGIFFormats(formats map[string]revoltgo.GIFMedia) map[string]domain.GIFFormat {
	if len(formats) == 0 {
		return nil
	}

	out := make(map[string]domain.GIFFormat, len(formats))
	for name, media := range formats {
		if media.URL == "" {
			continue
		}

		format := domain.GIFFormat{URL: media.URL}
		if len(media.Dimensions) == 2 {
			format.Width, format.Height = media.Dimensions[0], media.Dimensions[1]
		}

		out[name] = format
	}

	return out
}
