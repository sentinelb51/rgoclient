package cache

import (
	"image"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	// Image format decoders.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

const (
	maxDiskBytes   int64 = 512 * 1024 * 1024 // on-disk budget
	maxMemoryBytes int64 = 192 * 1024 * 1024 // decoded images kept in memory
	flushInterval        = 30 * time.Second  // how often pending images hit disk
	trimInterval         = 5 * time.Minute   // how often the on-disk budget is enforced

	// maxImageEdge caps the longest side of a decoded image, in pixels. Revolt
	// serves attachments at their original resolution and the client never draws
	// one larger than the window, so a phone photo arrives as ~48 MiB of pixels to
	// be shown 400px wide. Capping the decode is what stops a handful of them
	// dwarfing everything else in the cache.
	//
	// It is one fixed bound rather than the size each call site draws at, because
	// entries are keyed by file ID alone: the same avatar is asked for at four
	// different sizes through one key, so a per-call-site cap would let the
	// smallest requester decide what every larger one gets.
	maxImageEdge = 1600

	// diskHeadroomDivisor sets how far under budget a trim goes: 1/8th, so the
	// next few trims don't immediately re-run.
	diskHeadroomDivisor = 8
)

// ImageCache stores decoded images in memory and persists them to disk in the
// background. Safe for concurrent use.
//
// Memory is bounded in bytes rather than entries: a 32px avatar and a 12
// megapixel photo are one slot each, so a count is not a ceiling on anything.
type ImageCache struct {
	mu       sync.RWMutex // guards every map below plus recency and bytes
	memory   map[string]image.Image
	circular map[string]image.Image // memory-only circular crops, keyed by id
	pending  map[string]image.Image // awaiting disk write
	inflight map[string]*imageLoad  // de-duplicates concurrent loads by id
	sizes    map[string]int64       // resident bytes per id: its image plus its crop
	recency  *LRU                   // decoded-image ids by recency
	bytes    int64                  // sum of sizes; what maxMemoryBytes bounds

	dir      string
	client   *http.Client
	ticker   *time.Ticker
	flushNow chan struct{} // nudged when unwritten images are blocking eviction
	stop     chan struct{}
}

// imageLoad tracks an in-flight load so concurrent callers for the same id share
// one download instead of racing.
type imageLoad struct {
	done chan struct{}
	img  image.Image
}

// NewImageCache creates a cache rooted at the user's cache directory and starts
// the background flush, which also trims the directory back under budget.
func NewImageCache() *ImageCache {
	dir := cacheDir()
	c := &ImageCache{
		memory:   make(map[string]image.Image),
		circular: make(map[string]image.Image),
		pending:  make(map[string]image.Image),
		inflight: make(map[string]*imageLoad),
		sizes:    make(map[string]int64),
		recency:  NewLRU(),
		dir:      dir,
		client:   &http.Client{Timeout: 15 * time.Second},
		flushNow: make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("image cache: create dir: %v", err)
	}

	c.ticker = time.NewTicker(flushInterval)
	go c.flushLoop()

	return c
}

// cacheDir returns the directory used to persist images.
func cacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "RGOClient", "assets", "images")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "RGOClient", "assets", "images")
	}

	return filepath.Join(".", "cache", "images")
}

// Shutdown stops the background flush and persists any pending images.
func (c *ImageCache) Shutdown() {
	close(c.stop)
	c.flush()
}

/* Reading and writing */

// Get returns an image by ID, checking memory then disk, or nil when neither has
// it. It touches disk, so keep it off the UI thread.
func (c *ImageCache) Get(id string) image.Image {
	if id == "" {
		return nil
	}

	c.mu.Lock()
	img, ok := c.memory[id]
	if !ok {
		// Evicted from memory but not yet written to disk — still right here, and
		// being asked for again is what makes it resident again.
		if img, ok = c.pending[id]; ok {
			c.storeLocked(id, img, false)
		}
	}
	if ok {
		c.touchLocked(id)
	}
	c.mu.Unlock()

	if ok {
		return img
	}

	path := filepath.Join(c.dir, id+".png")
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	img, _, err = image.Decode(file)
	if err != nil {
		_ = os.Remove(path) // a corrupt file would fail every future load of this id
		return nil
	}
	// Files written before the decode cap existed are still oversized on disk.
	img = downscale(img)

	// Disk eviction is oldest-first by mtime, which is only ever set at write —
	// insertion order, not recency. Stamping it on a hit is what makes the two
	// agree, so a picture the user keeps scrolling past outlives one seen once.
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		log.Printf("image cache: touch %s: %v", id, err)
	}

	c.mu.Lock()
	c.storeLocked(id, img, false)
	c.touchLocked(id)
	c.mu.Unlock()

	return img
}

// Set stores an image in memory and queues it for disk persistence.
func (c *ImageCache) Set(id string, img image.Image) {
	if id == "" || img == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.storeLocked(id, img, false)
	c.pending[id] = img
	c.touchLocked(id)
}

// LoadAsync loads an image and delivers it to onLoaded on the UI thread. When
// circular is set the image is clipped to a circle, and the clipped variant is
// memoised so repeated avatars are not re-clipped.
func (c *ImageCache) LoadAsync(id, url string, circular bool, onLoaded func(image.Image)) {
	if url == "" {
		return
	}

	// Fast path: the right variant is already in memory, so deliver it on the
	// calling thread without touching disk, the network, or circleClip.
	if img := c.cachedVariant(id, circular); img != nil {
		onLoaded(img)
		return
	}

	go func() {
		img := c.loadShared(id, url)
		if img == nil {
			return
		}
		if circular {
			img = c.circularVariant(id, img)
		}

		// Dispatched straight to the driver rather than through ui.DoOnUI, which
		// internal/ui cannot export to us (it imports this package). Waiting also
		// throttles the loader to the UI thread's pace instead of queueing a
		// callback per in-flight image.
		fyne.CurrentApp().Driver().DoFromGoroutine(func() { onLoaded(img) }, true)
	}()
}

// LoadIntoContainer loads an image and renders it into target, optionally behind
// a background object and optionally clipped to a circle.
func (c *ImageCache) LoadIntoContainer(id, url string, size fyne.Size, target *fyne.Container, circular bool, background fyne.CanvasObject) {
	c.LoadAsync(id, url, circular, func(img image.Image) {
		canvasImg := canvas.NewImageFromImage(img)
		canvasImg.FillMode = canvas.ImageFillContain
		canvasImg.SetMinSize(size)

		if background != nil {
			target.Objects = []fyne.CanvasObject{background, canvasImg}
		} else {
			target.Objects = []fyne.CanvasObject{canvasImg}
		}
		target.Refresh()
	})
}

/* Internals */

// touchLocked marks id most recently used and evicts the least recently used
// decoded images until the cache is back inside its byte budget. The entry just
// touched is never a candidate, so a single image larger than the whole budget
// still resolves rather than evicting itself. Callers must hold the write lock.
func (c *ImageCache) touchLocked(id string) {
	c.recency.Touch(id)

	for c.bytes > maxMemoryBytes && c.recency.Len() > 1 {
		c.releaseLocked(c.recency.EvictOldest())
	}
}

// releaseLocked drops an id's decoded images, plain and circular, and uncharges
// them. An image still queued for its disk write keeps that reference — dropping
// it would lose the image outright — so the flusher is nudged instead: once it
// has been written the reference goes with the queue, and until then the budget
// is short by that much. Callers must hold the write lock.
func (c *ImageCache) releaseLocked(id string) {
	delete(c.memory, id)
	delete(c.circular, id)

	c.bytes -= c.sizes[id]
	delete(c.sizes, id)

	if _, queued := c.pending[id]; queued {
		select {
		case c.flushNow <- struct{}{}:
		default: // a flush is already asked for; one is enough
		}
	}
}

// storeLocked puts a decoded image — the plain one, or the circular crop of it —
// in memory under id and charges what it costs, replacing whatever was there.
// Callers must hold the write lock.
func (c *ImageCache) storeLocked(id string, img image.Image, circular bool) {
	into := c.memory
	if circular {
		into = c.circular
	}

	if old, ok := into[id]; ok {
		c.chargeLocked(id, -imageBytes(old))
	}
	into[id] = img
	c.chargeLocked(id, imageBytes(img))
}

// chargeLocked moves an id's resident total and the cache's by n bytes. Callers
// must hold the write lock.
func (c *ImageCache) chargeLocked(id string, n int64) {
	c.sizes[id] += n
	c.bytes += n

	if c.sizes[id] == 0 {
		delete(c.sizes, id)
	}
}

// imageBytes is what a decoded image costs resident, at four bytes a pixel. That
// is exact for the RGBA images downscaling and circleClip produce, and an
// over-estimate for the subsampled or paletted ones that arrive small enough to
// be kept as decoded — erring high is what keeps the budget a ceiling.
func imageBytes(img image.Image) int64 {
	bounds := img.Bounds()

	return int64(bounds.Dx()) * int64(bounds.Dy()) * 4
}

// downscale shrinks img so its longest side is at most maxImageEdge, returning it
// untouched when it already fits. It runs once per image, off the UI thread, and
// its result is what every later draw is scaled from — so it is worth a good
// filter rather than a cheap one.
func downscale(img image.Image) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxImageEdge && height <= maxImageEdge {
		return img
	}

	scale := float64(maxImageEdge) / float64(max(width, height))
	dst := image.NewRGBA(image.Rect(0, 0,
		max(int(float64(width)*scale), 1),
		max(int(float64(height)*scale), 1),
	))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)

	return dst
}

// cachedVariant returns the in-memory image for id, the circular crop when
// requested, or nil. It never reads disk, so it is safe on the UI thread.
func (c *ImageCache) cachedVariant(id string, circular bool) image.Image {
	if id == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	img := c.memory[id]
	if circular {
		img = c.circular[id]
	}
	if img != nil {
		c.touchLocked(id)
	}

	return img
}

// loadShared resolves an image by id, ensuring concurrent callers for the same
// id share one underlying load instead of each downloading a copy.
func (c *ImageCache) loadShared(id, url string) image.Image {
	c.mu.Lock()
	if call, ok := c.inflight[id]; ok {
		c.mu.Unlock()
		<-call.done
		return call.img
	}
	call := &imageLoad{done: make(chan struct{})}
	c.inflight[id] = call
	c.mu.Unlock()

	img := c.load(id, url)

	c.mu.Lock()
	delete(c.inflight, id)
	c.mu.Unlock()

	call.img = img
	close(call.done)

	return img
}

// load returns the cached copy of an image, otherwise downloading and caching
// it. Returns nil on any failure.
func (c *ImageCache) load(id, url string) image.Image {
	if url == "" {
		return nil
	}
	if img := c.Get(id); img != nil {
		return img
	}

	resp, err := c.client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil
	}
	img = downscale(img)
	c.Set(id, img)

	return img
}

// circularVariant returns the circular crop of base for id, computing it on
// first use so the same avatar is clipped only once.
func (c *ImageCache) circularVariant(id string, base image.Image) image.Image {
	c.mu.RLock()
	clipped, ok := c.circular[id]
	c.mu.RUnlock()

	if ok {
		return clipped
	}
	clipped = circleClip(base)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.storeLocked(id, clipped, true)
	c.touchLocked(id)

	return clipped
}

/* Disk */

// flushLoop writes pending images to disk and keeps the directory inside its
// budget, until Shutdown is called. Both jobs live on this one goroutine so
// nothing races the directory: a trim removing a file a flush is writing would
// only lose an image the client can fetch again, but there is no reason to allow
// it.
func (c *ImageCache) flushLoop() {
	trim := time.NewTicker(trimInterval)
	defer trim.Stop()

	// The first trim runs here rather than in the constructor, where it read the
	// whole cache directory and sorted it before the window could appear.
	c.trimDiskCache()

	for {
		select {
		case <-c.ticker.C:
			c.flush()
		case <-c.flushNow:
			c.flush()
		case <-trim.C:
			c.trimDiskCache()
		case <-c.stop:
			c.ticker.Stop()
			return
		}
	}
}

// flush persists all pending images to disk.
func (c *ImageCache) flush() {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}
	pending := c.pending
	c.pending = make(map[string]image.Image)
	c.mu.Unlock()

	for id, img := range pending {
		c.writeToDisk(id, img)
	}
}

// writeToDisk encodes one image to the cache directory as PNG, via a temp file
// renamed into place so a failed encode cannot leave a partial file that would
// poison every later load of this id.
func (c *ImageCache) writeToDisk(id string, img image.Image) {
	path := filepath.Join(c.dir, id+".png")
	tmp := path + ".tmp"

	file, err := os.Create(tmp)
	if err != nil {
		return
	}

	err = png.Encode(file, img)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}

	if err != nil {
		log.Printf("image cache: write %s: %v", id, err)
		_ = os.Remove(tmp)
	}
}

// trimDiskCache evicts the least recently used files until the on-disk cache
// fits inside the budget, leaving headroom so the next trim isn't immediately due
// again. Recency is the file's mtime, which Get stamps on every hit — so what
// survives is what the user keeps seeing, not merely what arrived last.
//
// Call from flushLoop only: it reads and sorts the whole directory.
func (c *ImageCache) trimDiskCache() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		log.Printf("image cache: read dir: %v", err)
		return
	}

	type diskFile struct {
		name     string
		size     int64
		modified time.Time
	}

	files := make([]diskFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, diskFile{entry.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}

	if total <= maxDiskBytes {
		return
	}
	slices.SortFunc(files, func(x, y diskFile) int { return x.modified.Compare(y.modified) })

	target := maxDiskBytes - maxDiskBytes/diskHeadroomDivisor
	log.Printf("image cache: %d MiB over budget, trimming to %d MiB",
		(total-maxDiskBytes)/(1024*1024), target/(1024*1024))

	for _, file := range files {
		if total <= target {
			return
		}
		if err := os.Remove(filepath.Join(c.dir, file.name)); err != nil {
			log.Printf("image cache: remove %s: %v", file.name, err)
			continue
		}
		total -= file.size
	}
}

// circleClip returns a square, circular crop of src, centred on the image.
func circleClip(src image.Image) image.Image {
	bounds := src.Bounds()
	size := min(bounds.Dx(), bounds.Dy())

	// Flatten the crop region once so the pixel loop copies raw bytes instead of
	// paying an image.Image interface call plus a colour conversion per pixel.
	flat := image.NewRGBA(image.Rect(0, 0, size, size))
	origin := image.Pt(bounds.Min.X+(bounds.Dx()-size)/2, bounds.Min.Y+(bounds.Dy()-size)/2)
	draw.Draw(flat, flat.Bounds(), src, origin, draw.Src)

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radiusSq := center * center

	for y := range size {
		dy := float64(y) - center + 0.5
		srcRow := flat.Pix[y*flat.Stride:]
		dstRow := dst.Pix[y*dst.Stride:]

		for x := range size {
			dx := float64(x) - center + 0.5
			if dx*dx+dy*dy <= radiusSq {
				copy(dstRow[x*4:x*4+4], srcRow[x*4:x*4+4])
			}
		}
	}

	return dst
}
