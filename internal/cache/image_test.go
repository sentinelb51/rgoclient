package cache

import (
	"image"
	"image/color"
	"testing"
)

// TestImageBytes pins what each decoded format costs the budget. A charge below
// what the image really occupies is invisible — the cache keeps working, just
// over its ceiling — so the deep formats are the point: a 16-bit PNG is eight
// bytes a pixel, not four.
func TestImageBytes(t *testing.T) {
	rect := image.Rect(0, 0, 10, 20)
	pixels := int64(10 * 20)

	cases := []struct {
		name string
		img  image.Image
		want int64
	}{
		{"RGBA", image.NewRGBA(rect), pixels * 4},
		{"NRGBA", image.NewNRGBA(rect), pixels * 4},
		{"RGBA64", image.NewRGBA64(rect), pixels * 8},
		{"NRGBA64", image.NewNRGBA64(rect), pixels * 8},
		{"CMYK", image.NewCMYK(rect), pixels * 4},
		{"Gray", image.NewGray(rect), pixels},
		{"Gray16", image.NewGray16(rect), pixels * 2},
		{"Alpha", image.NewAlpha(rect), pixels},
		{"Alpha16", image.NewAlpha16(rect), pixels * 2},
		{"Paletted", image.NewPaletted(rect, color.Palette{color.Black, color.White}), pixels + 2*4},
		{"YCbCr 4:4:4", image.NewYCbCr(rect, image.YCbCrSubsampleRatio444), pixels * 3},
		{"YCbCr 4:2:0", image.NewYCbCr(rect, image.YCbCrSubsampleRatio420), pixels + 2*(5*10)},
		{"NYCbCrA 4:4:4", image.NewNYCbCrA(rect, image.YCbCrSubsampleRatio444), pixels * 4},
		{"unknown", image.NewUniform(color.Black), 0},
	}

	for _, c := range cases {
		if got := imageBytes(c.img); got != c.want {
			t.Errorf("%s: imageBytes = %d, want %d", c.name, got, c.want)
		}
	}
}
