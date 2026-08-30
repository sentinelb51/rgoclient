package app

// The picture path: everything between a reader picking a file and Autumn
// holding it. Five things here upload one — this account's avatar and banner, a
// server's icon and banner, a group's picture — and every one of them asks the
// same question first, which is what part of the file to send.
//
// Filed apart from settings.go because none of it is a setting: two of the five
// are on another page entirely, and what they have in common is the file rather
// than the row that offers it. The card is ui.CropCard and knows nothing about
// Revolt; the shape it is opened in is policy and lives here, the way what a
// kind of CPU core is *for* lives beside the setting that names it.

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registered for the sniff: Revolt serves WebP

	"RGOClient/internal/ui"
)

/* What a picture is being chosen for */

// pictureShape is what one of the five is: the frames on offer, whether the
// first of them is previewed as a circle, and the longest edge the crop is
// reduced to before it is sent. The shapes are the client's rather than the
// card's — a server banner stands down the side of a channel list and a profile
// banner across the top of a card, and only this side knows that.
type pictureShape struct {
	aspects []ui.CropAspect
	round   bool
	maxEdge int
}

var (
	// squarePicture is everything drawn as a face or an icon. Round: Revolt draws
	// an avatar through a circle, so a square frame alone would centre a face
	// against corners nothing keeps.
	squarePicture = pictureShape{
		aspects: []ui.CropAspect{{Label: "Square", W: 1, H: 1}, {Label: "Free"}},
		round:   true,
		maxEdge: 1024,
	}

	// widePicture is the strip behind a profile. Clients draw it at their own
	// height — this one at 320 by 60 — so the run runs from a screen's shape to a
	// letterbox rather than naming one.
	widePicture = pictureShape{
		aspects: []ui.CropAspect{
			{Label: "16:9", W: 16, H: 9},
			{Label: "3:1", W: 3, H: 1},
			{Label: "5:1", W: 5, H: 1},
			{Label: "Free"},
		},
		maxEdge: 1920,
	}

	// tallPicture is a server's banner, which is the one picture Revolt draws
	// standing up: it goes behind the channel list rather than across anything.
	tallPicture = pictureShape{
		aspects: []ui.CropAspect{
			{Label: "9:16", W: 9, H: 16},
			{Label: "3:4", W: 3, H: 4},
			{Label: "16:9", W: 16, H: 9},
			{Label: "Free"},
		},
		maxEdge: 1440,
	}
)

// pictureFilter is what Revolt serves back as a picture. The filter is a
// courtesy — the server decides what it will take — so it stays generous.
var pictureFilter = ui.FileFilter{
	Label:      "Pictures",
	Extensions: []string{".png", ".jpg", ".jpeg", ".gif", ".webp"},
}

// picture is a file on its way to Autumn. Close removes a crop this client
// wrote and leaves a file the reader chose where it is — the upload is the last
// thing to read either, and a temporary one is nobody else's to find.
type picture struct {
	path, name string
	temp       bool
}

func (p picture) Close() {
	if !p.temp {
		return
	}

	if err := os.Remove(p.path); err != nil {
		log.Printf("picture: remove %s: %v", p.path, err)
	}
}

/* Choosing one */

// choosePicture asks for an image file, then asks which part of it to send, and
// reports the file to upload. Nothing is reported where the reader dismisses
// either card; the caller closes what it is handed once the upload is done.
// Call on the UI thread.
func (a *App) choosePicture(title string, shape pictureShape, onPicked func(picture)) {
	a.chooseFile(title, pictureFilter, func(path, name string) {
		a.cropPicture(title, shape, path, name, onPicked)
	})
}

// cropPicture opens the crop card over whatever page asked for the picture. The
// decode is a worker's: a photograph off a phone is twenty-four megapixels, and
// the UI thread is holding a settings page open while it happens.
//
// A file this client cannot read is one it cannot crop, not one it cannot send —
// Autumn decides what is a picture — so it goes up as it stands.
func (a *App) cropPicture(title string, shape pictureShape, path, name string, onPicked func(picture)) {
	epoch := a.epoch

	go func() {
		source, err := loadPicture(path)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("picture: decode %s: %v", path, err)
				onPicked(picture{path: path, name: name})

				return
			}

			a.showCropCard(title, shape, source, path, name, onPicked)
		}, false)
	}()
}

// showCropCard puts the card up and turns its two answers into one file. Call on
// the UI thread.
func (a *App) showCropCard(title string, shape pictureShape, source *sourcePicture, path, name string, onPicked func(picture)) {
	request := ui.CropRequest{
		Title:   title,
		Picture: source.preview,
		Size:    source.image.Bounds().Size(),
		Aspects: shape.aspects,
		Round:   shape.round,
		Note:    source.note,
	}

	card := ui.NewCropCard(request,
		func(rect image.Rectangle) {
			a.closeOverlay()
			a.writeCrop(source, rect, shape, name, onPicked)
		},
		func() {
			a.closeOverlay()
			onPicked(picture{path: path, name: name})
		},
		a.closeOverlay)

	a.showOverlay(card.Content)
}

// writeCrop cuts the frame out and hands back what was written. On a worker for
// the same reason the decode is: the scale is the expensive half, and the card
// is already gone by the time it starts.
func (a *App) writeCrop(source *sourcePicture, rect image.Rectangle, shape pictureShape, name string, onPicked func(picture)) {
	epoch := a.epoch

	go func() {
		file, err := cropToFile(source, rect, shape.maxEdge, name)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("picture: crop %s: %v", name, err)
				a.notify(ui.ToneWarning, "Could not prepare that picture.")

				return
			}

			onPicked(file)
		}, false)
	}()
}

/* Reading one */

// previewLimit is the longest edge the card is handed. A texture is uploaded to
// the GPU whole, and a twenty-four-megapixel photograph is ninety-odd megabytes
// of one for a stage three hundred units tall.
const previewLimit = 1600

// sourcePicture is a decoded file: every pixel, what the card draws, and the one
// line the file gets to say about itself.
type sourcePicture struct {
	image   image.Image
	preview image.Image

	format string
	note   string
}

// loadPicture decodes a file and works out what the card should say about it.
// The bytes are read once and decoded twice for a GIF, because whether one is
// animated is a question only the frame list answers.
func loadPicture(path string) (*sourcePicture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	source := &sourcePicture{image: decoded, preview: decoded, format: format}
	if bounds := decoded.Bounds(); max(bounds.Dx(), bounds.Dy()) > previewLimit {
		source.preview = fitPicture(decoded, bounds, previewLimit)
	}

	// The one thing a crop cannot keep. Said rather than refused: a still taken
	// out of an animation is a perfectly good avatar, and the other button is
	// right there.
	if format == "gif" && animated(data) {
		source.note = "Animated. A crop keeps the first frame — the whole file keeps the animation."
	}

	return source, nil
}

// animated reports whether a GIF has more than one frame.
func animated(data []byte) bool {
	frames, err := gif.DecodeAll(bytes.NewReader(data))

	return err == nil && len(frames.Image) > 1
}

/* Writing one back */

// cropToFile cuts the frame out of the picture, reduces what is left to what the
// shape is worth sending, and writes it where a temporary file goes. Reduced
// rather than sent whole: an avatar is served back at 256 pixels, and Autumn
// refuses a file over its limit rather than resizing it.
//
// A photograph is written back as a photograph and everything else as PNG. The
// file the reader picked is what decides, rather than what the pixels look like:
// a logo re-encoded as a JPEG rings along every edge it has, and a photograph
// written as a PNG is several megabytes of a limit measured in them.
func cropToFile(source *sourcePicture, rect image.Rectangle, maxEdge int, name string) (picture, error) {
	// The card answers in a picture that starts at the origin; an image decoded
	// out of a file need not.
	rect = rect.Add(source.image.Bounds().Min).Intersect(source.image.Bounds())
	if rect.Empty() {
		return picture{}, fmt.Errorf("nothing left of %s to send", name)
	}

	cropped := fitPicture(source.image, rect, maxEdge)

	file, err := os.CreateTemp("", "rgoclient-crop-*")
	if err != nil {
		return picture{}, err
	}

	written := picture{path: file.Name(), name: retitle(name, ".png"), temp: true}
	if source.format == "jpeg" {
		written.name = retitle(name, ".jpg")
		err = jpeg.Encode(file, cropped, &jpeg.Options{Quality: jpegQuality})
	} else {
		err = png.Encode(file, cropped)
	}

	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		written.Close()

		return picture{}, err
	}

	return written, nil
}

// jpegQuality is what a crop out of a photograph is written at. High enough that
// the re-encode is invisible at the sizes anything draws these at, low enough to
// be the point of writing one.
const jpegQuality = 90

// fitPicture is one rectangle of a picture as a picture of its own, reduced to
// maxEdge along its longer side. The cut and the scale are one operation: the
// scaler takes the rectangle it reads from, so nothing is copied twice.
func fitPicture(src image.Image, rect image.Rectangle, maxEdge int) image.Image {
	width, height := rect.Dx(), rect.Dy()
	if longest := max(width, height); maxEdge > 0 && longest > maxEdge {
		width, height = width*maxEdge/longest, height*maxEdge/longest
	}

	out := image.NewNRGBA(image.Rect(0, 0, max(width, 1), max(height, 1)))
	if out.Rect.Dx() == rect.Dx() && out.Rect.Dy() == rect.Dy() {
		draw.Draw(out, out.Rect, src, rect.Min, draw.Src)

		return out
	}

	xdraw.CatmullRom.Scale(out, out.Rect, src, rect, xdraw.Src, nil)

	return out
}

// retitle is the reader's own file name carrying the extension of what was
// actually written. Autumn sniffs the name it is given, so a JPEG announced as a
// .png is a file Revolt serves back with the wrong type.
func retitle(name, extension string) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" {
		stem = "picture"
	}

	return stem + extension
}
