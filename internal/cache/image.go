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

	_ "golang.org/x/image/webp"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

const (
	maxDiskBytes    int64 = 5 * 1024 * 1024 * 1024 // on-disk budget
	maxMemoryImages       = 200                    // decoded images kept in memory
	flushInterval         = 2 * time.Minute        // how often pending images hit disk

	// diskHeadroomDivisor sets how far under budget a trim goes: 1/8th, so the
	// next few sessions don't immediately re-trim.
	diskHeadroomDivisor = 8
)

// ImageCache stores decoded images in memory and persists them to disk in the
// background. Safe for concurrent use.
type ImageCache struct {
	mu       sync.RWMutex // guards every map below plus recency
	memory   map[string]image.Image
	circular map[string]image.Image // memory-only circular crops, keyed by id
	pending  map[string]image.Image // awaiting disk write
	inflight map[string]*imageLoad  // de-duplicates concurrent loads by id
	recency  *LRU                   // decoded-image ids by recency

	dir    string
	client *http.Client
	ticker *time.Ticker
	stop   chan struct{}
}

// imageLoad tracks an in-flight load so concurrent callers for the same id share
// one download instead of racing.
type imageLoad struct {
	done chan struct{}
	img  image.Image
}

// NewImageCache creates a cache rooted at the user's cache directory, trims it
// back under budget, and starts the periodic disk flush.
func NewImageCache() *ImageCache {
	dir := cacheDir()
	c := &ImageCache{
		memory:   make(map[string]image.Image),
		circular: make(map[string]image.Image),
		pending:  make(map[string]image.Image),
		inflight: make(map[string]*imageLoad),
		recency:  NewLRU(),
		dir:      dir,
		client:   &http.Client{Timeout: 15 * time.Second},
		stop:     make(chan struct{}),
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("image cache: create dir: %v", err)
	}
	c.trimDiskCache()

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

	c.mu.Lock()
	c.memory[id] = img
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

	c.memory[id] = img
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
// decoded images, both plain and circular. Eviction never touches pending, so
// queued disk writes still happen and Get reloads evicted images from disk.
// Callers must hold the write lock.
func (c *ImageCache) touchLocked(id string) {
	c.recency.Touch(id)

	for c.recency.Len() > maxMemoryImages {
		evicted := c.recency.EvictOldest()
		delete(c.memory, evicted)
		delete(c.circular, evicted)
	}
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

	c.circular[id] = clipped
	c.touchLocked(id)

	return clipped
}

/* Disk */

// flushLoop writes pending images to disk until Shutdown is called.
func (c *ImageCache) flushLoop() {
	for {
		select {
		case <-c.ticker.C:
			c.flush()
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

// trimDiskCache evicts the least recently modified files until the on-disk cache
// fits inside the budget, leaving headroom so the next start isn't immediately
// over again. Evicting oldest-first keeps the images the user actually sees.
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
