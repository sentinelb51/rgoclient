package main

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
	mu        sync.RWMutex
	memory    map[string]image.Image
	pending   map[string]image.Image // Images waiting to be saved to disk
	cacheDir  string
	client    *http.Client
	saveTimer *time.Ticker
	stopChan  chan struct{}
}

var (
	globalImageCache *ImageCache
	imageCacheOnce   sync.Once
)

// GetImageCache returns the global image cache instance.
func GetImageCache() *ImageCache {
	imageCacheOnce.Do(func() {
		cacheDir := getAppCacheDir()
		globalImageCache = &ImageCache{
			memory:   make(map[string]image.Image),
			pending:  make(map[string]image.Image),
			cacheDir: cacheDir,
			client:   &http.Client{Timeout: 15 * time.Second},
			stopChan: make(chan struct{}),
		}

		// Create cache directory
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			println("Warning: Failed to create image cache directory:", err.Error())
		}

		// Start periodic save goroutine (every 2 minutes)
		globalImageCache.startPeriodicSave(2 * time.Minute)
	})
	return globalImageCache
}

// getAppCacheDir returns the application cache directory path.
func getAppCacheDir() string {
	if cacheDir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cacheDir, "RGOClient", "assets", "images")
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(homeDir, ".cache", "RGOClient", "assets", "images")
	}
	return filepath.Join(".", "cache", "images")
}

// startPeriodicSave starts a background goroutine that saves pending images periodically.
func (ic *ImageCache) startPeriodicSave(interval time.Duration) {
	ic.saveTimer = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ic.saveTimer.C:
				ic.FlushToDisk()
			case <-ic.stopChan:
				ic.saveTimer.Stop()
				return
			}
		}
	}()
}

// Shutdown stops the periodic save and flushes all pending images to disk.
// Call this when the application is closing.
func (ic *ImageCache) Shutdown() {
	close(ic.stopChan)
	ic.FlushToDisk()
}

// FlushToDisk saves all pending images to disk.
func (ic *ImageCache) FlushToDisk() {
	ic.mu.Lock()
	if len(ic.pending) == 0 {
		ic.mu.Unlock()
		return
	}

	// Copy pending map and clear it
	toSave := ic.pending
	ic.pending = make(map[string]image.Image)
	ic.mu.Unlock()

	// Save images outside the lock
	for id, img := range toSave {
		ic.saveImageToDisk(id, img)
	}
}

// saveImageToDisk writes a single image to the cache directory.
func (ic *ImageCache) saveImageToDisk(imageID string, img image.Image) {
	cachePath := filepath.Join(ic.cacheDir, imageID+".png")

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
func (ic *ImageCache) Get(imageID string) image.Image {
	if imageID == "" {
		return nil
	}

	// Check memory cache
	ic.mu.RLock()
	if img, ok := ic.memory[imageID]; ok {
		ic.mu.RUnlock()
		return img
	}
	ic.mu.RUnlock()

	// Check disk cache
	cachePath := filepath.Join(ic.cacheDir, imageID+".png")
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
	ic.mu.Lock()
	ic.memory[imageID] = img
	ic.mu.Unlock()

	return img
}

// Set stores an image in memory and marks it for later disk persistence.
func (ic *ImageCache) Set(imageID string, img image.Image) {
	if imageID == "" || img == nil {
		return
	}

	ic.mu.Lock()
	ic.memory[imageID] = img
	ic.pending[imageID] = img
	ic.mu.Unlock()
}

// LoadFromURL loads an image from URL, using cache if available.
func (ic *ImageCache) LoadFromURL(imageID, url string) image.Image {
	if url == "" {
		return nil
	}

	if img := ic.Get(imageID); img != nil {
		return img
	}

	resp, err := ic.client.Get(url)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil
	}

	ic.Set(imageID, img)
	return img
}

// LoadFromURLAsync loads an image asynchronously and calls onLoaded on the UI thread.
func (ic *ImageCache) LoadFromURLAsync(imageID, url string, circular bool, onLoaded func(image.Image)) {
	if url == "" {
		return
	}

	// Check cache first (fast path)
	if img := ic.Get(imageID); img != nil {
		if circular {
			img = circleClip(img)
		}
		onLoaded(img)
		return
	}

	go func() {
		img := ic.LoadFromURL(imageID, url)
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
func (ic *ImageCache) LoadImageToContainer(imageID, url string, size fyne.Size, target *fyne.Container, circular bool, bg fyne.CanvasObject) {
	ic.LoadFromURLAsync(imageID, url, circular, func(loadedImg image.Image) {
		img := canvas.NewImageFromImage(loadedImg)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(size)

		if bg != nil {
			target.Objects = []fyne.CanvasObject{bg, img}
		} else {
			target.Objects = []fyne.CanvasObject{img}
		}
		target.Refresh()
	})
}

// ClearMemoryCache clears only the in-memory cache.
func (ic *ImageCache) ClearMemoryCache() {
	ic.mu.Lock()
	ic.memory = make(map[string]image.Image)
	ic.mu.Unlock()
}

// circleClip clips an image to a circle shape.
func circleClip(src image.Image) image.Image {
	bounds := src.Bounds()
	size := bounds.Dx()
	if bounds.Dy() < size {
		size = bounds.Dy()
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radiusSq := center * center

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center + 0.5
			dy := float64(y) - center + 0.5
			if dx*dx+dy*dy <= radiusSq {
				dst.Set(x, y, src.At(bounds.Min.X+x, bounds.Min.Y+y))
			}
		}
	}

	return dst
}
