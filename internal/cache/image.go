package cache

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	flushInterval = 30 * time.Second // how often pending images hit disk
	trimInterval  = 5 * time.Minute  // how often the on-disk budget is enforced

	// diskHeadroomDivisor sets how far under budget a trim goes: 1/8th, so the
	// next few trims don't immediately re-run.
	diskHeadroomDivisor = 8
)

// ImageLimits is what the cache is allowed to occupy. The settings page edits
// them; the cache holds them atomically because the trimmer reads them on its own
// goroutine while the UI thread sets them.
type ImageLimits struct {
	DiskBytes   int64
	MemoryBytes int64

	// MaxEdge caps the longest side of a decoded image, in pixels. Revolt serves
	// attachments at their original resolution and the client never draws one
	// larger than the window, so a phone photo arrives as ~48 MiB of pixels to be
	// shown 400px wide. Capping the decode is what stops a handful of them
	// dwarfing everything else in the cache.
	//
	// It is one bound rather than the size each call site draws at, because entries
	// are keyed by file ID alone: the same avatar is asked for at four different
	// sizes through one key, so a per-call-site cap would let the smallest
	// requester decide what every larger one gets.
	MaxEdge int64

	// Loaders bounds how many pictures are fetched at once. Unlike the budgets it
	// is fixed at construction — the semaphore is a channel, and resizing one out
	// from under the goroutines holding it cannot be done safely — so SetLimits
	// ignores it and the settings row says a restart is needed.
	//
	// Without it a member list flung through thousands of rows fires a goroutine
	// and a connection per row it passes. A zero or negative value is one loader.
	Loaders int
}

// DefaultImageLimits are what the client ran with before the budgets became
// configurable.
func DefaultImageLimits() ImageLimits {
	return ImageLimits{
		DiskBytes:   512 * 1024 * 1024,
		MemoryBytes: 192 * 1024 * 1024,
		MaxEdge:     1600,
		Loaders:     8,
	}
}

// EmojiMaxEdge caps an emoji's decode. It is not the settings' MaxImageEdge and
// has no reason to be: an emoji is drawn at a line's height and never larger, so
// decoding one at the resolution a photograph needs would spend a whole memory
// budget on a dozen of them.
const EmojiMaxEdge = 128

// ImageStats is what the cache currently occupies, for the settings page to
// report.
type ImageStats struct {
	Files       int
	DiskBytes   int64
	MemoryBytes int64
}

// Add sums two caches' occupancy, for a settings page that meters more than one
// of them as the single number the budget is expressed in.
func (s ImageStats) Add(other ImageStats) ImageStats {
	return ImageStats{
		Files:       s.Files + other.Files,
		DiskBytes:   s.DiskBytes + other.DiskBytes,
		MemoryBytes: s.MemoryBytes + other.MemoryBytes,
	}
}

// ImageCache stores decoded images in memory and persists them to disk in the
// background. Safe for concurrent use.
//
// Memory is bounded in bytes rather than entries: a 32px avatar and a 12
// megapixel photo are one slot each, so a count is not a ceiling on anything.
type ImageCache struct {
	mu       sync.RWMutex // guards every map below plus recency, bytes and generation
	memory   map[string]image.Image
	circular map[string]image.Image // memory-only circular crops, keyed by id
	pending  map[string]image.Image // awaiting disk write
	inflight map[string]*imageLoad  // de-duplicates concurrent loads by id
	sizes    map[string]int64       // resident bytes per id: its image plus its crop
	touched  map[string]struct{}    // ids hit since the last mtime pass
	recency  *LRU                   // decoded-image ids by recency
	bytes    int64                  // sum of sizes; what maxMemory bounds

	// generation counts Clears. A load that started before one stores nothing, so
	// a cache the user emptied is not refilled by the downloads it interrupted.
	generation uint64

	// The budgets, read by the trimmer and by every decode without the lock.
	maxDisk   atomic.Int64
	maxMemory atomic.Int64
	maxEdge   atomic.Int64

	// loaders is the download semaphore — see ImageLimits.Loaders. Buffered to the
	// bound and never replaced, so it is read without the lock.
	loaders chan struct{}

	dir       string
	client    *http.Client
	flushNow  chan struct{} // nudged when unwritten images are blocking eviction
	stop      chan struct{}
	closeOnce sync.Once // stop is closed here, so a second Shutdown cannot panic
}

// imageLoad tracks an in-flight load so concurrent callers for the same id share
// one download instead of racing.
type imageLoad struct {
	done chan struct{}
	img  image.Image
}

// NewImageCache creates a cache in folder under root — an empty root for the
// user's cache directory — and starts the background flush, which also trims the
// directory back under budget.
func NewImageCache(root, folder string, limits ImageLimits) *ImageCache {
	dir := filepath.Join(CacheRoot(root), folder)

	c := &ImageCache{
		memory:   make(map[string]image.Image),
		circular: make(map[string]image.Image),
		pending:  make(map[string]image.Image),
		inflight: make(map[string]*imageLoad),
		sizes:    make(map[string]int64),
		touched:  make(map[string]struct{}),
		recency:  newLRU(),
		loaders:  make(chan struct{}, max(limits.Loaders, 1)),
		dir:      dir,
		client:   &http.Client{Timeout: 15 * time.Second},
		flushNow: make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}

	c.SetLimits(limits)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("image cache: create dir: %v", err)
	}

	go c.flushLoop()

	return c
}

// SetLimits changes what the cache may occupy. The memory budget is enforced on
// the next store or flush and the disk budget on the next trim, so nothing is
// discarded at the moment of the change. Loaders is fixed at construction and
// ignored here. Safe from any goroutine.
func (c *ImageCache) SetLimits(limits ImageLimits) {
	c.maxDisk.Store(limits.DiskBytes)
	c.maxMemory.Store(limits.MemoryBytes)
	c.maxEdge.Store(limits.MaxEdge)
}

// Dir is where the cache persists images.
func (c *ImageCache) Dir() string { return c.dir }

// Stats reports what the cache occupies. It reads the directory, so keep it off
// the UI thread.
func (c *ImageCache) Stats() ImageStats {
	c.mu.RLock()
	stats := ImageStats{MemoryBytes: c.bytes}
	c.mu.RUnlock()

	for _, file := range c.diskFiles() {
		if !file.isEntry() {
			continue
		}

		stats.Files++
		stats.DiskBytes += file.size
	}

	return stats
}

// diskFile is one persisted image, as the directory reports it.
type diskFile struct {
	name     string
	size     int64
	modified time.Time
}

// isEntry reports whether the file is a cached image rather than something else
// the directory holds — a temp left behind by a failed encode, say. Only entries
// count towards the stats and the disk budget. A ".gif" is an original kept for
// animation beside the ".png" still of its first frame.
func (f diskFile) isEntry() bool {
	return strings.HasSuffix(f.name, ".png") || strings.HasSuffix(f.name, ".gif")
}

// diskFiles lists the cache directory, skipping subdirectories and anything
// unreadable. It is the one walk Stats, Clear and trimDiskCache share, and it
// reports every file: Clear removes them all, and the trim sweeps stale temps,
// so the filtering is each caller's rather than done here.
func (c *ImageCache) diskFiles() []diskFile {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		log.Printf("image cache: read dir: %v", err)
		return nil
	}

	files := make([]diskFile, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.IsDir() {
			continue
		}

		files = append(files, diskFile{entry.Name(), info.Size(), info.ModTime()})
	}

	return files
}

// Clear drops everything the cache holds, in memory and on disk. Images queued
// for a write are dropped with the rest: they came off the network and will come
// off it again. Downloads already on their way are not waited for — the cache is
// shared with the UI thread and blocking on the network under the lock would
// freeze every avatar lookup — they are discarded on arrival by the generation
// they started under. It touches disk, so keep it off the UI thread.
func (c *ImageCache) Clear() {
	c.mu.Lock()
	c.memory = make(map[string]image.Image)
	c.circular = make(map[string]image.Image)
	c.pending = make(map[string]image.Image)
	c.sizes = make(map[string]int64)
	c.touched = make(map[string]struct{})
	c.recency = newLRU()
	c.bytes = 0
	c.generation++
	c.mu.Unlock()

	for _, file := range c.diskFiles() {
		if err := os.Remove(filepath.Join(c.dir, file.name)); err != nil {
			log.Printf("image cache: remove %s: %v", file.name, err)
		}
	}
}

// Asset folders under the cache root, one per class of picture. They are kept
// apart so a budget, a trim and a clear address one class without touching the
// others: an afternoon of scrolling through attachments must not evict the
// handful of emoji every message is drawn with.
const (
	ImagesFolder = "images" // avatars, icons, attachments, embeds
	EmojisFolder = "emojis" // custom emoji drawn inside a message body
)

// CacheRoot returns the directory the client keeps its cached assets under:
// the configured one, or a place inside the user's cache directory.
func CacheRoot(configured string) string {
	if configured != "" {
		return configured
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "RGOClient", "assets")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "RGOClient", "assets")
	}

	return filepath.Join(".", "cache")
}

// Shutdown stops the background flush and persists any pending images. Calling
// it more than once is allowed; only the first stops anything, and each one
// still persists whatever has been queued since.
func (c *ImageCache) Shutdown() {
	c.closeOnce.Do(func() { close(c.stop) })

	c.flush()
	c.stampTouched()
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
		c.enforceMemoryLocked()
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
	// Files written under a larger cap than the current one are still oversized on
	// disk.
	img = downscale(img, int(c.maxEdge.Load()))

	c.mu.Lock()
	c.storeLocked(id, img, false)
	c.touchLocked(id)
	c.enforceMemoryLocked()
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

	c.storePendingLocked(id, img)
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
		// Claimed here rather than before the goroutine: acquiring on the calling
		// thread would block the UI thread on whatever is already downloading. A
		// worker still queued for a turn when the cache shuts down leaves instead.
		select {
		case c.loaders <- struct{}{}:
		case <-c.stop:
			return
		}
		img := c.loadShared(id, url)
		<-c.loaders

		if img == nil {
			return
		}
		if circular {
			img = c.circularVariant(id, img)
		}

		// Once the driver has drained, DoFromGoroutine runs the callback inline on
		// this goroutine rather than on the UI thread — and onLoaded touches
		// widgets. The window between this check and the enqueue is the driver's to
		// close; this closes the rest.
		select {
		case <-c.stop:
			return
		default:
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

/* Animated GIF originals */

// gifMaxBytes bounds the encoded GIF kept for animation. Tighter than
// decodeMaxBytes: a still decodes one frame where the player decodes every
// frame, and a file of minimal frames buys an allocation per frame, so the byte
// ceiling is what bounds that.
const gifMaxBytes = 8 << 20

// GIF returns the encoded bytes of an animated GIF by id, from disk or the
// network, or nil for anything that is not a GIF worth handing a player. The
// bytes are kept on disk as id+".gif" — beside the ".png" still Get answers
// with — so the next hover reads rather than refetches, and the trim evicts
// them by mtime like any entry. Fetched only on demand, never by the still's
// own load: a GIF nobody hovers costs nothing.
//
// It touches disk and the network; call off the UI thread.
func (c *ImageCache) GIF(id, url string) []byte {
	if id == "" || url == "" {
		return nil
	}

	path := filepath.Join(c.dir, id+".gif")
	if raw, err := os.ReadFile(path); err == nil {
		if validGIF(raw) {
			// Best-effort recency stamp, so an animation somebody keeps returning to
			// is not the first thing a trim takes.
			now := time.Now()
			_ = os.Chtimes(path, now, now)

			return raw
		}
		_ = os.Remove(path)
	}

	c.mu.RLock()
	generation := c.generation
	c.mu.RUnlock()

	resp, err := c.client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	// One past the ceiling, so a file sitting exactly on it is told apart from
	// one that was cut short.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, gifMaxBytes+1))
	if err != nil || len(raw) > gifMaxBytes || !validGIF(raw) {
		return nil
	}

	c.mu.RLock()
	current := c.generation == generation
	c.mu.RUnlock()
	if current {
		c.writeGIF(id, raw)
	}

	return raw
}

// validGIF is the magic and the canvas check: the bytes announce themselves as
// GIF and name a canvas small enough to decode. The frame-count and playback
// caps are the player's — this only keeps a mislabelled or hostile file out of
// the directory.
func validGIF(raw []byte) bool {
	if !bytes.HasPrefix(raw, []byte("GIF87a")) && !bytes.HasPrefix(raw, []byte("GIF89a")) {
		return false
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "gif" {
		return false
	}

	return config.Width > 0 && config.Height > 0 &&
		int64(config.Width)*int64(config.Height) <= decodeMaxPixels
}

// writeGIF persists the encoded bytes via a temp file renamed into place, the
// arrangement writeToDisk has and for the same reason. Written from the
// fetching goroutine rather than the flush loop: the rename is atomic and
// removeStaleTemps spares temps younger than a flush cycle, so the trim cannot
// catch a half-written one.
func (c *ImageCache) writeGIF(id string, raw []byte) {
	path := filepath.Join(c.dir, id+".gif")

	tmp, err := os.CreateTemp(c.dir, id+"-*.tmp")
	if err != nil {
		return
	}

	_, err = tmp.Write(raw)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		log.Printf("image cache: write %s: %v", id, err)
		_ = os.Remove(tmp.Name())
	}
}

/* Internals */

// touchLocked marks id most recently used, in memory and on disk. It stays two
// map operations because the UI thread runs it per avatar per frame through
// cachedVariant: the memory budget is applied by the paths that can grow the
// resident set, and the file's mtime is restamped by the flush goroutine, which
// is the only one allowed in the directory. Callers must hold the write lock.
func (c *ImageCache) touchLocked(id string) {
	c.recency.Touch(id)
	c.touched[id] = struct{}{}
}

// storePendingLocked makes id resident, queues it for its disk write and applies
// the memory budget. Callers must hold the write lock.
func (c *ImageCache) storePendingLocked(id string, img image.Image) {
	c.storeLocked(id, img, false)
	c.pending[id] = img
	c.touchLocked(id)
	c.enforceMemoryLocked()
}

// enforceMemoryLocked evicts the least recently used decoded images until the
// cache is back inside its byte budget. The most recently used entry is never a
// candidate, so a single image larger than the whole budget still resolves rather
// than evicting itself. Callers must hold the write lock.
func (c *ImageCache) enforceMemoryLocked() {
	for c.bytes > c.maxMemory.Load() && c.recency.Len() > 1 {
		c.releaseLocked(c.recency.EvictOldest())
	}
}

// enforceMemory applies the memory budget from the flush goroutine, so a budget
// lowered in the settings takes effect without waiting for the next store.
func (c *ImageCache) enforceMemory() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.enforceMemoryLocked()
}

// stampTouched restamps the mtime of every id hit since the last pass, which is
// what makes trimDiskCache's oldest-first eviction mean least recently *used*
// rather than least recently written — a picture the reader keeps scrolling past
// outliving one seen once. It runs on the flush goroutine, which owns the
// directory, and in a batch because os.Chtimes is a syscall: stamping at the hit
// would charge one to every avatar the UI thread draws.
func (c *ImageCache) stampTouched() {
	c.mu.Lock()
	touched := c.touched
	c.touched = make(map[string]struct{})
	c.mu.Unlock()

	now := time.Now()
	for id := range touched {
		// An id resident in memory need not be on disk yet: the flush that writes it
		// stamps it, and until then there is nothing to stamp.
		if err := os.Chtimes(filepath.Join(c.dir, id+".png"), now, now); err != nil && !os.IsNotExist(err) {
			log.Printf("image cache: touch %s: %v", id, err)
		}
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

// imageBytes is what a decoded image costs resident. It measures the pixel buffer
// rather than deriving it from the bounds: stride padding is part of the
// allocation, and a 16-bit PNG decodes to eight bytes a pixel, so the flat four
// this used to charge was half of what those cost and the budget stopped being a
// ceiling. An unrecognised type is charged that worst case.
func imageBytes(img image.Image) int64 {
	switch img := img.(type) {
	case *image.RGBA:
		return int64(len(img.Pix))
	case *image.NRGBA:
		return int64(len(img.Pix))
	case *image.RGBA64:
		return int64(len(img.Pix))
	case *image.NRGBA64:
		return int64(len(img.Pix))
	case *image.CMYK:
		return int64(len(img.Pix))
	case *image.Gray:
		return int64(len(img.Pix))
	case *image.Gray16:
		return int64(len(img.Pix))
	case *image.Alpha:
		return int64(len(img.Pix))
	case *image.Alpha16:
		return int64(len(img.Pix))
	case *image.Paletted:
		return int64(len(img.Pix) + len(img.Palette)*4)
	case *image.YCbCr:
		return int64(len(img.Y) + len(img.Cb) + len(img.Cr))
	case *image.NYCbCrA:
		return int64(len(img.Y) + len(img.Cb) + len(img.Cr) + len(img.A))
	}

	// Bounds are not trusted to be finite — image.Uniform's cover the plane, and
	// the product of those wraps past int64 into a negative charge that would
	// credit the budget rather than spend it.
	const maxEdge = 1 << 16

	bounds := img.Bounds()
	width, height := int64(bounds.Dx()), int64(bounds.Dy())
	if width <= 0 || height <= 0 || width > maxEdge || height > maxEdge {
		return 0
	}

	return width * height * 8
}

// downscale shrinks img so its longest side is at most edge, returning it
// untouched when it already fits. It runs once per image, off the UI thread, and
// its result is what every later draw is scaled from — so it is worth a good
// filter rather than a cheap one.
func downscale(img image.Image, edge int) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= edge && height <= edge {
		return img
	}

	scale := float64(edge) / float64(max(width, height))
	dst := image.NewRGBA(image.Rect(0, 0,
		max(int(float64(width)*scale), 1),
		max(int(float64(height)*scale), 1),
	))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)

	return dst
}

// cachedVariant returns the in-memory image for id, the circular crop when
// requested, or nil. It never reads disk, so it is safe on the UI thread — and
// it holds the write lock for a recency touch and nothing else: the LRU is a
// list, so a read lock cannot serve it, and dropping the touch would leave the
// hottest images looking coldest.
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

	c.mu.RLock()
	generation := c.generation
	c.mu.RUnlock()

	resp, err := c.client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	img := decodeBounded(resp.Body, url)
	if img == nil {
		return nil
	}
	img = downscale(img, int(c.maxEdge.Load()))
	c.setIfCurrent(id, img, generation)

	return img
}

// What a downloaded picture may cost before it is refused. Both are ceilings on
// somebody else's file: an embed's picture is fetched from whatever host the
// unfurl named, and a few hundred compressed bytes can name a canvas of billions
// of pixels — decoding one is a crash rather than a slow load. The pixel bound
// is the one that matters; the byte bound is what stops a body that never ends.
//
// 32 Mpx is past any photograph a person posts (a 50 MP phone picture is 50) and
// costs 128 MB while it decodes, of which downscale keeps the visible fraction.
const (
	decodeMaxBytes  = 32 << 20
	decodeMaxPixels = 32 << 20
)

// decodeBounded reads a picture from the network under those two ceilings,
// reporting nil for anything refused. The body is read into memory first because
// the dimensions have to be read *before* the pixels are allocated, and a
// decoder handed a stream has already consumed the header by the time it has
// answered.
func decodeBounded(body io.Reader, url string) image.Image {
	// One past the ceiling, so a file sitting exactly on it is told apart from one
	// that was cut short.
	raw, err := io.ReadAll(io.LimitReader(body, decodeMaxBytes+1))
	if err != nil {
		return nil
	}
	if len(raw) > decodeMaxBytes {
		log.Printf("image %s: larger than %d bytes", url, decodeMaxBytes)

		return nil
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	if config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > decodeMaxPixels {
		log.Printf("image %s: %dx%d is past what will be decoded", url, config.Width, config.Height)

		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}

	return img
}

// setIfCurrent is Set for a download that was already on its way: it stores
// nothing when the cache was emptied since generation was read, so a Clear the
// user asked for is not undone by the loads it interrupted. The caller is still
// given the image and paints it once. c.inflight is deliberately left alone by
// Clear — its entries release themselves, so emptying it would only cost a
// duplicate download to whoever would have joined this load.
func (c *ImageCache) setIfCurrent(id string, img image.Image, generation uint64) {
	if id == "" || img == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.generation != generation {
		return
	}

	c.storePendingLocked(id, img)
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
	c.enforceMemoryLocked()

	return clipped
}

/* Disk */

// flushLoop writes pending images to disk and keeps the directory inside its
// budget, until Shutdown is called. Both jobs live on this one goroutine so
// nothing races the directory: a trim removing a file a flush is writing would
// only lose an image the client can fetch again, but there is no reason to allow
// it.
func (c *ImageCache) flushLoop() {
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()

	trim := time.NewTicker(trimInterval)
	defer trim.Stop()

	// The first trim runs here rather than in the constructor, where it read the
	// whole cache directory and sorted it before the window could appear.
	c.trimDiskCache()

	for {
		select {
		case <-flush.C:
			c.flush()
			c.stampTouched()
		case <-c.flushNow:
			c.flush()
			c.stampTouched()
		case <-trim.C:
			c.stampTouched() // before the trim, so it evicts on what it has just learned
			c.trimDiskCache()
		case <-c.stop:
			return
		}

		c.enforceMemory()
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
// again. Recency is the file's mtime, which stampTouched restamps for every id
// hit since the last pass — so what survives is what the user keeps seeing, not
// merely what arrived last. Only cache entries are counted or evicted; the
// orphaned temps are swept first, since they are neither.
//
// Call from flushLoop only: it reads and sorts the whole directory.
func (c *ImageCache) trimDiskCache() {
	files := c.diskFiles()
	c.removeStaleTemps(files)

	var total int64
	for _, file := range files {
		if file.isEntry() {
			total += file.size
		}
	}

	budget := c.maxDisk.Load()
	if total <= budget {
		return
	}
	slices.SortFunc(files, func(x, y diskFile) int { return x.modified.Compare(y.modified) })

	target := budget - budget/diskHeadroomDivisor
	log.Printf("image cache: %d MiB over budget, trimming to %d MiB",
		(total-budget)/(1024*1024), target/(1024*1024))

	for _, file := range files {
		if total <= target {
			return
		}
		if !file.isEntry() {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, file.name)); err != nil {
			log.Printf("image cache: remove %s: %v", file.name, err)
			continue
		}
		total -= file.size
	}
}

// removeStaleTemps deletes the temp files a crashed or failed encode leaves
// behind. The age check is what keeps a trim from removing the file the flush is
// about to rename into place: nothing else writes here, so anything older than a
// flush cycle is dead. Call from flushLoop only.
func (c *ImageCache) removeStaleTemps(files []diskFile) {
	cutoff := time.Now().Add(-flushInterval)

	for _, file := range files {
		if !strings.HasSuffix(file.name, ".tmp") || file.modified.After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, file.name)); err != nil {
			log.Printf("image cache: remove %s: %v", file.name, err)
		}
	}
}

// circleClip returns a square, circular crop of src, centred on the image.
func circleClip(src image.Image) image.Image {
	bounds := src.Bounds()
	size := min(bounds.Dx(), bounds.Dy())

	// Flatten the crop region once so the row copies move raw bytes instead of
	// paying an image.Image interface call plus a colour conversion per pixel.
	flat := image.NewRGBA(image.Rect(0, 0, size, size))
	origin := image.Pt(bounds.Min.X+(bounds.Dx()-size)/2, bounds.Min.Y+(bounds.Dy()-size)/2)
	draw.Draw(flat, flat.Bounds(), src, origin, draw.Src)

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radiusSq := center * center

	// Solving the circle for x gives the span inside it, so each row is one copy
	// rather than a distance test and a four-byte copy per pixel.
	for y := range size {
		dy := float64(y) - center + 0.5
		if dy*dy > radiusSq {
			continue
		}

		half := math.Sqrt(radiusSq - dy*dy)
		first := max(int(math.Ceil(center-0.5-half)), 0)
		last := min(int(math.Floor(center-0.5+half)), size-1)
		if first > last {
			continue
		}

		lo, hi := first*4, (last+1)*4
		copy(dst.Pix[y*dst.Stride+lo:y*dst.Stride+hi], flat.Pix[y*flat.Stride+lo:y*flat.Stride+hi])
	}

	return dst
}
