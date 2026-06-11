package cache

import (
	"image"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

// DefaultMaxCacheSize is the default on-disk image cache budget (5 GB).
const DefaultMaxCacheSize int64 = 5 * 1024 * 1024 * 1024

// flushInterval is how often pending images are written to disk.
const flushInterval = 2 * time.Minute

// maxMemoryImages caps how many decoded images stay in memory; the least
// recently used are evicted (their disk copies remain, so Get reloads them).
const maxMemoryImages = 200

// ImageCache stores decoded images in memory and persists them to disk in the
// background. It is safe for concurrent use.
type ImageCache struct {
	mu       sync.RWMutex
	memory   map[string]image.Image
	circular map[string]image.Image // memory-only circular crops, keyed by id
	pending  map[string]image.Image // awaiting disk write
	inflight map[string]*imageLoad  // de-duplicates concurrent loads by id
	recency  *lruKeys               // decoded-image ids by recency
	dir      string
	client   *http.Client
	ticker   *time.Ticker
	stop     chan struct{}
	maxBytes int64
}

// imageLoad tracks an in-flight load so concurrent callers requesting the same
// id share one download instead of racing.
type imageLoad struct {
	done chan struct{}
	img  image.Image
}

// NewImageCache creates a cache rooted at the user's cache directory, purges it
// if it exceeds the size budget, and starts the periodic disk flush.
func NewImageCache() *ImageCache {
	dir := cacheDir()
	c := &ImageCache{
		memory:   make(map[string]image.Image),
		circular: make(map[string]image.Image),
		pending:  make(map[string]image.Image),
		inflight: make(map[string]*imageLoad),
		recency:  newLRUKeys(),
		dir:      dir,
		client:   &http.Client{Timeout: 15 * time.Second},
		stop:     make(chan struct{}),
		maxBytes: DefaultMaxCacheSize,
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("image cache: create dir: %v", err)
	}
	c.purgeIfOverBudget()

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

// Shutdown stops the background flush and persists any pending images.
func (c *ImageCache) Shutdown() {
	close(c.stop)
	c.flush()
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

// writeToDisk encodes a single image to the cache directory as PNG, writing to
// a temp file and renaming into place so a failed encode can't leave a partial
// file that would poison every later load of this id.
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

// touchLocked marks id most recently used and evicts the least recently used
// decoded images (both plain and circular variants) past maxMemoryImages.
// Eviction never touches pending, so queued disk writes still happen, and Get
// reloads evicted images from disk. Callers must hold the write lock.
func (c *ImageCache) touchLocked(id string) {
	c.recency.Touch(id)
	for c.recency.Len() > maxMemoryImages {
		evicted := c.recency.EvictOldest()
		delete(c.memory, evicted)
		delete(c.circular, evicted)
	}
}

// Get returns an image by ID, checking memory first then disk. Returns nil if
// the image is not cached.
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
		// A corrupt file would otherwise fail every future load of this id.
		_ = os.Remove(path)
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
	c.memory[id] = img
	c.pending[id] = img
	c.touchLocked(id)
	c.mu.Unlock()
}

// load fetches an image by ID, returning the cached copy if present, otherwise
// downloading and caching it. Returns nil on any failure.
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

// LoadAsync loads an image and delivers it to onLoaded on the UI thread. If
// circular is set, the image is clipped to a circle (the clipped variant is
// cached in memory so repeated avatars are not re-clipped).
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
		fyne.CurrentApp().Driver().DoFromGoroutine(func() { onLoaded(img) }, true)
	}()
}

// cachedVariant returns the in-memory image for id (the circular crop when
// requested), or nil. It never reads disk, so it is safe on the UI thread.
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

// loadShared resolves an image by id, ensuring that concurrent callers for the
// same id share a single underlying load instead of each downloading a copy.
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

// circularVariant returns the circular crop of base for id, computing and
// caching it on first use so the same avatar is clipped only once.
func (c *ImageCache) circularVariant(id string, base image.Image) image.Image {
	c.mu.RLock()
	clipped, ok := c.circular[id]
	c.mu.RUnlock()
	if ok {
		return clipped
	}

	clipped = circleClip(base)

	c.mu.Lock()
	c.circular[id] = clipped
	c.touchLocked(id)
	c.mu.Unlock()
	return clipped
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

// purgeIfOverBudget clears the disk cache when it exceeds the size budget.
func (c *ImageCache) purgeIfOverBudget() {
	size, err := c.diskSize()
	if err != nil {
		log.Printf("image cache: measure size: %v", err)
		return
	}
	if size > c.maxBytes {
		log.Print("image cache: over budget, purging")
		c.purge()
	}
}

// diskSize returns the total size of the on-disk cache in bytes.
func (c *ImageCache) diskSize() (int64, error) {
	var total int64
	err := filepath.Walk(c.dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// purge clears the in-memory cache, pending writes, and on-disk files.
func (c *ImageCache) purge() {
	c.mu.Lock()
	c.memory = make(map[string]image.Image)
	c.circular = make(map[string]image.Image)
	c.pending = make(map[string]image.Image)
	c.recency = newLRUKeys()
	c.mu.Unlock()

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		log.Printf("image cache: read dir: %v", err)
		return
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(c.dir, entry.Name())); err != nil {
			log.Printf("image cache: remove %s: %v", entry.Name(), err)
		}
	}
}

// circleClip returns a square, circular crop of src, centred on the image.
func circleClip(src image.Image) image.Image {
	bounds := src.Bounds()
	size := min(bounds.Dx(), bounds.Dy())

	// Flatten the crop region once so the pixel loop below copies raw bytes
	// instead of paying an image.Image interface call plus a colour conversion
	// for every pixel.
	flat := image.NewRGBA(image.Rect(0, 0, size, size))
	origin := image.Pt(bounds.Min.X+(bounds.Dx()-size)/2, bounds.Min.Y+(bounds.Dy()-size)/2)
	draw.Draw(flat, flat.Bounds(), src, origin, draw.Src)

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radiusSq := center * center

	for y := 0; y < size; y++ {
		dy := float64(y) - center + 0.5
		srcRow := flat.Pix[y*flat.Stride:]
		dstRow := dst.Pix[y*dst.Stride:]
		for x := 0; x < size; x++ {
			dx := float64(x) - center + 0.5
			if dx*dx+dy*dy <= radiusSq {
				copy(dstRow[x*4:x*4+4], srcRow[x*4:x*4+4])
			}
		}
	}
	return dst
}
