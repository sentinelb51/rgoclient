package ui

// The crop card: which part of a picture the client uploads. One card for every
// picture an account can send — an avatar, a profile banner, a server's icon or
// banner, a group's — because the question is the same every time and only the
// shape it is asked in differs.
//
// It knows nothing about files, formats or Autumn. The controller decodes, says
// what to draw and what pixel size the answer is in, and takes back a rectangle
// in the source's own pixels: the ui.ShareSource seam again.

import (
	"fmt"
	"image"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"RGOClient/assets"
	"RGOClient/internal/ui/theme"
)

/* What is being asked */

// CropAspect is one shape the frame can be locked to. A zero side is the free
// one, which keeps whatever the reader drags.
type CropAspect struct {
	Label string
	W, H  int
}

func (a CropAspect) free() bool { return a.W <= 0 || a.H <= 0 }

func (a CropAspect) ratio() float64 { return float64(a.W) / float64(a.H) }

// CropRequest is what the card is opened with.
type CropRequest struct {
	Title string

	// Picture is what is drawn and Size the picture's true size in pixels, which
	// the answer is in. The two are not the same image: a photograph is scaled
	// down for the screen long before it reaches a texture, and the crop is still
	// taken from every pixel the file has.
	Picture image.Image
	Size    image.Point

	// Aspects are the shapes on offer, the first being the one the frame opens at.
	Aspects []CropAspect

	// Round draws the circle inside the frame that an avatar is actually seen
	// through, so a face is not centred against a square nothing will use.
	Round bool

	// Note is the one line the picture gets to say about itself — that an
	// animated GIF is flattened by a crop, which is the whole reason to keep it.
	Note string
}

// CropCard is the card. It holds no answer of its own: the frame is the answer,
// and it is read off the stage when a button is pressed.
type CropCard struct {
	// Content is the card to hand to the modal layer.
	Content fyne.CanvasObject

	stage   *cropStage
	readout *canvas.Text
	chips   []*pickChip
}

// NewCropCard builds the card. onCrop is handed the rectangle in the source's
// pixels, onOriginal takes the file as it stands — a re-encode being a loss an
// animation does not survive — and onClose dismisses the layer.
func NewCropCard(req CropRequest, onCrop func(image.Rectangle), onOriginal, onClose func()) *CropCard {
	if len(req.Aspects) == 0 {
		req.Aspects = []CropAspect{{Label: "Free"}}
	}
	if req.Size.X <= 0 || req.Size.Y <= 0 {
		req.Size = req.Picture.Bounds().Size()
	}

	c := &CropCard{}
	c.stage = newCropStage(req)
	c.stage.onChange = c.report
	c.readout = newText("", theme.Colors.TimestampText, theme.Sizes.DialogDetailSize)

	rows := []fyne.CanvasObject{
		dialogHeader(req.Title, onClose),
		NewRowDivider(),
		c.stage,
		c.readout,
		dialogField("Shape", c.buildAspects(req.Aspects)),
	}

	if req.Note != "" {
		rows = append(rows, NewWrappedText(req.Note, cropInnerWidth(),
			theme.Sizes.DialogDetailSize, theme.Colors.TimestampText))
	}

	rows = append(rows, NewFillRow(1,
		NewButton("Use the whole file", onOriginal),
		HorizontalSpacer(theme.Sizes.DialogFieldGap),
		NewWeightedButton("Crop and upload", ButtonPrimary, func() { onCrop(c.stage.rect()) }),
	))

	c.report()

	padding := theme.Sizes.DialogPadding
	body := NewMinWidthContainer(theme.Sizes.CropDialogWidth,
		NewInset(spacedColumn(theme.Sizes.DialogFieldGap, rows...), padding, padding, padding, padding))

	c.Content = newTapSink(container.NewStack(newDialogCard(), body))

	return c
}

// cropInnerWidth is the card's width less its padding — what anything wrapped by
// hand has to be measured against.
func cropInnerWidth() float32 {
	return theme.Sizes.CropDialogWidth - 2*theme.Sizes.DialogPadding
}

// buildAspects is the run of shapes, one of which is the answer: a filter run
// with a radio's rule laid over it, which the tap enforces rather than the chip.
func (c *CropCard) buildAspects(aspects []CropAspect) fyne.CanvasObject {
	chips := make([]fyne.CanvasObject, 0, len(aspects))

	for index, aspect := range aspects {
		chip := newPickChip(assets.SearchImageIcon, aspect.Label, index)
		chip.onTap = func() {
			c.stage.setAspect(aspect)
			markPickChips(c.chips, chip.value)
		}

		c.chips = append(c.chips, chip)
		chips = append(chips, chip)
	}

	markPickChips(c.chips, 0)

	return NewFlow(cropInnerWidth(), theme.Sizes.IslandChipGap, chips...)
}

// report writes what the frame is worth under the picture. Pixels rather than a
// percentage: what matters about a crop is whether what is left is still big
// enough to be served back as the picture it is being uploaded as.
func (c *CropCard) report() {
	rect := c.stage.rect()

	c.readout.Text = fmt.Sprintf("%d × %d pixels", rect.Dx(), rect.Dy())
	c.readout.Refresh()
}

/* The stage */

// cropGrab is what a gesture is moving. The four corners lead, so that a grab is
// the index of the handle it landed on.
type cropGrab int

const (
	cropTopLeft cropGrab = iota
	cropTopRight
	cropBottomLeft
	cropBottomRight
	cropMove
)

func (g cropGrab) left() bool { return g == cropTopLeft || g == cropBottomLeft }
func (g cropGrab) top() bool  { return g == cropTopLeft || g == cropTopRight }

// cropMinPixels is the smallest frame a drag may leave, in source pixels. Small
// enough to take a face out of a screenshot, large enough that a frame cannot be
// lost under its own handles.
const cropMinPixels = 16

// cropZoomStep is what one notch of the wheel is worth.
const cropZoomStep = 1.08

// cropStage is the picture with the frame over it: a wash over what is dropped,
// four corner handles, and a drag that moves or resizes. The frame is held in
// the source's own pixels rather than in screen units — the answer is in pixels,
// and a card resized under a frame kept in screen units would crop elsewhere.
type cropStage struct {
	widget.BaseWidget

	backdrop *canvas.Rectangle
	picture  *canvas.Image
	masks    []*canvas.Rectangle
	frame    *canvas.Rectangle
	circle   *canvas.Circle
	handles  []*canvas.Rectangle
	content  *fyne.Container

	bounds image.Point // the picture's size in pixels
	box    cropBox     // what is kept, in those pixels
	aspect CropAspect

	// scale and origin are where the picture landed inside the stage, which the
	// layout works out and every drag reads back.
	scale  float64
	origin fyne.Position

	grab     cropGrab
	dragging bool

	onChange func()
}

var (
	_ fyne.Draggable     = (*cropStage)(nil)
	_ fyne.Scrollable    = (*cropStage)(nil)
	_ desktop.Cursorable = (*cropStage)(nil)
)

func newCropStage(req CropRequest) *cropStage {
	s := &cropStage{
		backdrop: canvas.NewRectangle(theme.Colors.VideoScrim),
		picture:  canvas.NewImageFromImage(req.Picture),
		frame:    canvas.NewRectangle(nil),
		circle:   canvas.NewCircle(nil),
		bounds:   req.Size,
		aspect:   req.Aspects[0],
	}
	s.box = openingBox(s.aspect, s.bounds)

	// Stretched into the box the layout worked out: the letterboxing is this
	// widget's own, every drag being measured against where the picture landed.
	s.picture.FillMode = canvas.ImageFillStretch
	s.picture.ScaleMode = canvas.ImageScaleSmooth

	s.frame.StrokeColor = theme.Colors.TextPrimary
	s.frame.StrokeWidth = theme.Sizes.CropFrameLine
	s.circle.StrokeColor = theme.Colors.TextPrimary
	s.circle.StrokeWidth = theme.Sizes.OutlineWidth
	if !req.Round {
		s.circle.Hide()
	}

	objects := []fyne.CanvasObject{s.backdrop, s.picture}
	for range 4 {
		mask := canvas.NewRectangle(theme.Colors.VideoScrim)
		s.masks = append(s.masks, mask)
		objects = append(objects, mask)
	}

	objects = append(objects, s.frame, s.circle)
	for range 4 {
		handle := canvas.NewRectangle(theme.Colors.TextPrimary)
		s.handles = append(s.handles, handle)
		objects = append(objects, handle)
	}

	s.content = container.New(&cropStageLayout{stage: s}, objects...)
	s.ExtendBaseWidget(s)

	return s
}

func (s *cropStage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

func (s *cropStage) Cursor() desktop.Cursor { return desktop.PointerCursor }

// rect is the frame as the card's answer takes it.
func (s *cropStage) rect() image.Rectangle { return s.box.rect(s.bounds) }

// setAspect locks the frame to a shape, keeping where the reader put it. The
// free one changes nothing: the frame it is switched to is already the answer.
func (s *cropStage) setAspect(aspect CropAspect) {
	s.aspect = aspect
	s.box = s.box.fitted(aspect, s.bounds)
	s.changed()
}

// Dragged moves the frame or one of its corners. Which of the two is decided
// once per gesture: a corner dragged past the opposite one goes on resizing
// rather than turning into a move under the pointer.
func (s *cropStage) Dragged(event *fyne.DragEvent) {
	if s.scale <= 0 {
		return
	}
	if !s.dragging {
		s.dragging = true
		s.grab = s.grabAt(event.Position)
	}

	if s.grab == cropMove {
		s.box = s.box.moved(float64(event.Dragged.DX)/s.scale, float64(event.Dragged.DY)/s.scale, s.bounds)
	} else {
		x, y := s.source(event.Position)
		s.box = s.box.resized(s.grab, x, y, s.aspect, s.bounds)
	}

	s.changed()
}

// DragEnd completes fyne.Draggable. Without it the driver never recognises the
// stage as draggable and Dragged is never called at all.
func (s *cropStage) DragEnd() { s.dragging = false }

// Scrolled grows and shrinks the frame about its own centre, which is the one
// adjustment a corner drag is clumsy at: a locked shape kept while both edges
// move.
func (s *cropStage) Scrolled(event *fyne.ScrollEvent) {
	if event.Scrolled.DY == 0 {
		return
	}

	factor := 1 / cropZoomStep
	if event.Scrolled.DY < 0 {
		factor = cropZoomStep
	}

	s.box = s.box.zoomed(factor, s.bounds)
	s.changed()
}

// grabAt names what is under the pointer where a gesture started. A corner wins
// within a handle's reach of it; everything else moves the frame, the picture
// outside it included — a frame dragged from where it is not is still a frame
// being moved.
func (s *cropStage) grabAt(pos fyne.Position) cropGrab {
	reach := theme.Sizes.CropHandleSize

	for index, corner := range s.corners() {
		if abs32(pos.X-corner.X) <= reach && abs32(pos.Y-corner.Y) <= reach {
			return cropGrab(index)
		}
	}

	return cropMove
}

// source is a point on the stage in the picture's own pixels.
func (s *cropStage) source(pos fyne.Position) (x, y float64) {
	return float64(pos.X-s.origin.X) / s.scale, float64(pos.Y-s.origin.Y) / s.scale
}

// corners is where the frame's four corners are on the stage, in the order
// cropGrab names them.
func (s *cropStage) corners() []fyne.Position {
	pos, size := s.screenFrame()

	return []fyne.Position{
		pos,
		fyne.NewPos(pos.X+size.Width, pos.Y),
		fyne.NewPos(pos.X, pos.Y+size.Height),
		fyne.NewPos(pos.X+size.Width, pos.Y+size.Height),
	}
}

// screenFrame is the frame in the stage's own units.
func (s *cropStage) screenFrame() (fyne.Position, fyne.Size) {
	pos := fyne.NewPos(
		s.origin.X+float32(s.box.x*s.scale),
		s.origin.Y+float32(s.box.y*s.scale),
	)
	size := fyne.NewSize(float32(s.box.w*s.scale), float32(s.box.h*s.scale))

	return pos, size
}

func (s *cropStage) changed() {
	Relayout(s.content)

	if s.onChange != nil {
		s.onChange()
	}
}

/* Placing it */

// cropStageLayout places the picture and everything drawn over it. The whole
// arrangement is one function: where the picture landed is what the frame is
// measured against, and nothing else can work it out.
type cropStageLayout struct {
	stage *cropStage
}

func (l *cropStageLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, theme.Sizes.CropStageHeight)
}

func (l *cropStageLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	s := l.stage
	if size.Width <= 0 || size.Height <= 0 || s.bounds.X <= 0 || s.bounds.Y <= 0 {
		return
	}

	s.backdrop.Move(fyne.NewPos(0, 0))
	s.backdrop.Resize(size)

	// Contained, and enlarged where the picture is smaller than the stage: an
	// icon 64 pixels square is one nobody could aim a frame at otherwise.
	s.scale = math.Min(float64(size.Width)/float64(s.bounds.X), float64(size.Height)/float64(s.bounds.Y))
	drawn := fyne.NewSize(float32(float64(s.bounds.X)*s.scale), float32(float64(s.bounds.Y)*s.scale))
	s.origin = fyne.NewPos((size.Width-drawn.Width)/2, (size.Height-drawn.Height)/2)

	s.picture.Move(s.origin)
	s.picture.Resize(drawn)

	pos, frame := s.screenFrame()
	right, bottom := pos.X+frame.Width, pos.Y+frame.Height

	// The four bands of what is dropped, laid around the frame rather than over
	// it: a wash with a hole in it is not a shape Fyne draws.
	placeRect(s.masks[0], 0, 0, size.Width, pos.Y)
	placeRect(s.masks[1], 0, bottom, size.Width, size.Height-bottom)
	placeRect(s.masks[2], 0, pos.Y, pos.X, frame.Height)
	placeRect(s.masks[3], right, pos.Y, size.Width-right, frame.Height)

	s.frame.Move(pos)
	s.frame.Resize(frame)

	side := min(frame.Width, frame.Height)
	s.circle.Move(fyne.NewPos(pos.X+(frame.Width-side)/2, pos.Y+(frame.Height-side)/2))
	s.circle.Resize(fyne.NewSize(side, side))

	handle := theme.Sizes.CropHandleSize
	for index, corner := range s.corners() {
		placeRect(s.handles[index], corner.X-handle/2, corner.Y-handle/2, handle, handle)
	}
}

// placeRect is one rectangle put where it belongs, clamped so a frame against an
// edge does not ask for a band of negative width.
func placeRect(rect *canvas.Rectangle, x, y, width, height float32) {
	rect.Move(fyne.NewPos(x, y))
	rect.Resize(fyne.NewSize(max(width, 0), max(height, 0)))
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}

	return v
}

/* The frame's arithmetic */

// cropBox is what is kept, in the source's own pixels and held as floats: a drag
// is a screen distance divided by the scale, and rounding one to a whole pixel
// per event rather than per gesture is a frame that lags the pointer.
type cropBox struct {
	x, y, w, h float64
}

// openingBox is the largest centred frame of the given shape, which is the crop
// somebody who presses the button straight away meant.
func openingBox(aspect CropAspect, bounds image.Point) cropBox {
	whole := cropBox{w: float64(bounds.X), h: float64(bounds.Y)}

	return whole.fitted(aspect, bounds)
}

// rect is the box as the answer takes it, held inside the picture so a rounded
// edge cannot name a pixel the file does not have.
func (b cropBox) rect(bounds image.Point) image.Rectangle {
	x, y := int(math.Round(b.x)), int(math.Round(b.y))

	return image.Rect(x, y, x+int(math.Round(b.w)), y+int(math.Round(b.h))).
		Intersect(image.Rectangle{Max: bounds})
}

// moved slides the frame, which stops at the picture's edges rather than
// carrying part of itself off them.
func (b cropBox) moved(dx, dy float64, bounds image.Point) cropBox {
	b.x = clamp(b.x+dx, 0, math.Max(0, float64(bounds.X)-b.w))
	b.y = clamp(b.y+dy, 0, math.Max(0, float64(bounds.Y)-b.h))

	return b
}

// resized drags one corner to a point, the opposite one staying where it is.
// Under a locked shape the two edges the pointer asks for disagree and the
// longer wins, so the frame follows the axis the pointer moved furthest along.
func (b cropBox) resized(grab cropGrab, x, y float64, aspect CropAspect, bounds image.Point) cropBox {
	anchorX, anchorY := b.x, b.y
	if grab.left() {
		anchorX = b.x + b.w
	}
	if grab.top() {
		anchorY = b.y + b.h
	}

	x = clamp(x, 0, float64(bounds.X))
	y = clamp(y, 0, float64(bounds.Y))
	w, h := math.Abs(x-anchorX), math.Abs(y-anchorY)

	minW, minH := float64(cropMinPixels), float64(cropMinPixels)
	if !aspect.free() {
		ratio := aspect.ratio()
		if w/ratio > h {
			h = w / ratio
		} else {
			w = h * ratio
		}

		if ratio >= 1 {
			minW = minH * ratio
		} else {
			minH = minW / ratio
		}
	}

	// The room between the anchor and the edge the frame is growing towards. Both
	// edges are scaled by the tighter of the two, so a locked shape keeps its
	// ratio instead of being squared off against one side.
	roomX, roomY := float64(bounds.X)-anchorX, float64(bounds.Y)-anchorY
	if grab.left() {
		roomX = anchorX
	}
	if grab.top() {
		roomY = anchorY
	}

	fit := 1.0
	if w > roomX {
		fit = math.Min(fit, roomX/w)
	}
	if h > roomY {
		fit = math.Min(fit, roomY/h)
	}
	w, h = math.Max(w*fit, minW), math.Max(h*fit, minH)

	b.w, b.h = w, h
	b.x, b.y = anchorX, anchorY
	if grab.left() {
		b.x = anchorX - w
	}
	if grab.top() {
		b.y = anchorY - h
	}

	return b.moved(0, 0, bounds)
}

// fitted reshapes the frame to a locked shape about its own centre, keeping as
// much of what was framed as that shape allows.
func (b cropBox) fitted(aspect CropAspect, bounds image.Point) cropBox {
	if aspect.free() {
		return b
	}

	ratio := aspect.ratio()
	centreX, centreY := b.x+b.w/2, b.y+b.h/2

	w, h := b.w, b.w/ratio
	if h > b.h {
		w, h = b.h*ratio, b.h
	}

	// A picture is not always larger than the shape asked of it: a wide banner out
	// of a portrait photograph is the whole width and none of the height.
	if w > float64(bounds.X) {
		w, h = float64(bounds.X), float64(bounds.X)/ratio
	}
	if h > float64(bounds.Y) {
		w, h = float64(bounds.Y)*ratio, float64(bounds.Y)
	}

	b.w, b.h = w, h
	b.x, b.y = centreX-w/2, centreY-h/2

	return b.moved(0, 0, bounds)
}

// zoomed scales the frame about its centre, never past the picture and never
// under the floor a drag stops at.
func (b cropBox) zoomed(factor float64, bounds image.Point) cropBox {
	centreX, centreY := b.x+b.w/2, b.y+b.h/2
	w, h := b.w*factor, b.h*factor

	if fit := math.Min(float64(bounds.X)/w, float64(bounds.Y)/h); fit < 1 {
		w, h = w*fit, h*fit
	}
	if w < cropMinPixels || h < cropMinPixels {
		return b
	}

	b.w, b.h = w, h
	b.x, b.y = centreX-w/2, centreY-h/2

	return b.moved(0, 0, bounds)
}
