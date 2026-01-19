package cache

import (
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	// Register image format decoders
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// ImageCache manages image caching with in-memory storage and periodic disk persistence.
type ImageCache struct {
	mutex             sync.RWMutex
	memory            map[string]image.Image
	pending           map[string]image.Image // Images waiting to be saved to disk
	cacheDir          string
	client            *http.Client
	saveTimer         *time.Ticker
	stopChan          chan struct{}
	MaxCacheSizeBytes int64 // Maximum cache size in bytes (default 5GB)
}

// DefaultMaxCacheSizeBytes is the default maximum cache size (5 GB).
const DefaultMaxCacheSizeBytes int64 = 5 * 1024 * 1024 * 1024

var (
	globalImageCache *ImageCache
	imageCacheOnce   sync.Once
)

// GetImageCache returns the global image cache instance.
func GetImageCache() *ImageCache {
	imageCacheOnce.Do(func() {
		cacheDirectory := getAppCacheDir()
		globalImageCache = &ImageCache{
			memory:            make(map[string]image.Image),
			pending:           make(map[string]image.Image),
			cacheDir:          cacheDirectory,
			client:            &http.Client{Timeout: 15 * time.Second},
			stopChan:          make(chan struct{}),
			MaxCacheSizeBytes: DefaultMaxCacheSizeBytes,
		}

		// Create cache directory
		if err := os.MkdirAll(cacheDirectory, 0755); err != nil {
			println("Warning: Failed to create image cache directory:", err.Error())
		}

		// Check and purge cache if it exceeds the size limit
		globalImageCache.CheckAndPurgeCache()

		// Start periodic save goroutine (every 2 minutes)
		globalImageCache.startPeriodicSave(2 * time.Minute)
	})
	return globalImageCache
}

// getAppCacheDir returns the application cache directory path.
func getAppCacheDir() string {
	if cacheDirectory, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cacheDirectory, "RGOClient", "assets", "images")
	}
	if homeDirectory, err := os.UserHomeDir(); err == nil {
		return filepath.Join(homeDirectory, ".cache", "RGOClient", "assets", "images")
	}
	return filepath.Join(".", "cache", "images")
}

// startPeriodicSave starts a background goroutine that saves pending images periodically.
func (cache *ImageCache) startPeriodicSave(interval time.Duration) {
	cache.saveTimer = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-cache.saveTimer.C:
				cache.FlushToDisk()
			case <-cache.stopChan:
				cache.saveTimer.Stop()
				return
			}
		}
	}()
}

// Shutdown stops the periodic save and flushes all pending images to disk.
// Call this when the application is closing.
func (cache *ImageCache) Shutdown() {
	close(cache.stopChan)
	cache.FlushToDisk()
}

// FlushToDisk saves all pending images to disk.
func (cache *ImageCache) FlushToDisk() {
	cache.mutex.Lock()
	if len(cache.pending) == 0 {
		cache.mutex.Unlock()
		return
	}

	// Copy pending map and clear it
	imagesToSave := cache.pending
	cache.pending = make(map[string]image.Image)
	cache.mutex.Unlock()

	// Save images outside the lock
	for imageID, img := range imagesToSave {
		cache.saveImageToDisk(imageID, img)
	}
}

// saveImageToDisk writes a single image to the cache directory.
func (cache *ImageCache) saveImageToDisk(imageID string, img image.Image) {
	cachePath := filepath.Join(cache.cacheDir, imageID+".png")

	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return
	}

	file, err := os.Create(cachePath)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()

	_ = png.Encode(file, img)
}

// Get retrieves an image from cache (memory first, then disk).
func (cache *ImageCache) Get(imageID string) image.Image {
	if imageID == "" {
		return nil
	}

	// Check memory cache
	cache.mutex.RLock()
	if img, exists := cache.memory[imageID]; exists {
		cache.mutex.RUnlock()
		return img
	}
	cache.mutex.RUnlock()

	// Check disk cache
	cachePath := filepath.Join(cache.cacheDir, imageID+".png")
	file, err := os.Open(cachePath)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil
	}

	// Store in memory for faster access
	cache.mutex.Lock()
	cache.memory[imageID] = img
	cache.mutex.Unlock()

	return img
}

// Set stores an image in memory and marks it for later disk persistence.
func (cache *ImageCache) Set(imageID string, img image.Image) {
	if imageID == "" || img == nil {
		return
	}

	cache.mutex.Lock()
	cache.memory[imageID] = img
	cache.pending[imageID] = img
	cache.mutex.Unlock()
}

// LoadFromURL loads an image from URL, using cache if available.
func (cache *ImageCache) LoadFromURL(imageID, url string) image.Image {
	if url == "" {
		return nil
	}

	if img := cache.Get(imageID); img != nil {
		return img
	}

	response, err := cache.client.Get(url)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil
	}

	img, _, err := image.Decode(response.Body)
	if err != nil {
		return nil
	}

	cache.Set(imageID, img)
	return img
}

// LoadFromURLAsync loads an image asynchronously and calls onLoaded on the UI thread.
func (cache *ImageCache) LoadFromURLAsync(imageID, url string, circular bool, onLoaded func(image.Image)) {
	if url == "" {
		return
	}

	// Check cache first (fast path)
	if img := cache.Get(imageID); img != nil {
		if circular {
			img = circleClip(img)
		}
		onLoaded(img)
		return
	}

	go func() {
		img := cache.LoadFromURL(imageID, url)
		if img == nil {
			return
		}

		if circular {
			img = circleClip(img)
		}

		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			onLoaded(img)
		}, true)
	}()
}

// LoadImageToContainer loads an image and updates a container with it.
func (cache *ImageCache) LoadImageToContainer(imageID, url string, size fyne.Size, target *fyne.Container, circular bool, background fyne.CanvasObject) {
	cache.LoadFromURLAsync(imageID, url, circular, func(loadedImage image.Image) {
		img := canvas.NewImageFromImage(loadedImage)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(size)

		if background != nil {
			target.Objects = []fyne.CanvasObject{background, img}
		} else {
			target.Objects = []fyne.CanvasObject{img}
		}

		target.Refresh()
	})
}

// ClearMemoryCache clears only the in-memory cache.
func (cache *ImageCache) ClearMemoryCache() {
	cache.mutex.Lock()
	cache.memory = make(map[string]image.Image)
	cache.mutex.Unlock()
}

// SetMaxCacheSize sets the maximum cache size in bytes.
func (cache *ImageCache) SetMaxCacheSize(sizeBytes int64) {
	cache.mutex.Lock()
	cache.MaxCacheSizeBytes = sizeBytes
	cache.mutex.Unlock()
}

// GetCacheSize returns the total size of the disk cache in bytes.
func (cache *ImageCache) GetCacheSize() (int64, error) {
	var totalSize int64

	err := filepath.Walk(cache.cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	return totalSize, err
}

// CheckAndPurgeCache checks if the cache exceeds the size limit and purges it if necessary.
func (cache *ImageCache) CheckAndPurgeCache() {
	cacheSize, err := cache.GetCacheSize()
	if err != nil {
		println("Warning: Failed to check cache size:", err.Error())
		return
	}

	cache.mutex.RLock()
	maxSize := cache.MaxCacheSizeBytes
	cache.mutex.RUnlock()

	if cacheSize > maxSize {
		println("Image cache exceeds size limit, purging...")
		cache.PurgeCache()
	}
}

// PurgeCache removes all files from the disk cache.
func (cache *ImageCache) PurgeCache() {
	// Clear memory cache first
	cache.ClearMemoryCache()

	// Clear pending writes
	cache.mutex.Lock()
	cache.pending = make(map[string]image.Image)
	cache.mutex.Unlock()

	// Remove all files from cache directory
	entries, err := os.ReadDir(cache.cacheDir)
	if err != nil {
		println("Warning: Failed to read cache directory:", err.Error())
		return
	}

	for _, entry := range entries {
		entryPath := filepath.Join(cache.cacheDir, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			println("Warning: Failed to remove cache entry:", err.Error())
		}
	}
}

// circleClip clips an image to a circle shape.
func circleClip(source image.Image) image.Image {
	bounds := source.Bounds()
	size := bounds.Dx()
	if bounds.Dy() < size {
		size = bounds.Dy()
	}

	destination := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radiusSquared := center * center

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center + 0.5
			dy := float64(y) - center + 0.5
			if dx*dx+dy*dy <= radiusSquared {
				destination.Set(x, y, source.At(bounds.Min.X+x, bounds.Min.Y+y))
			}
		}
	}

	return destination
}
