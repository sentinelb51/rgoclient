package ui

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// iconResources caches asset icons as shared, in-memory resources keyed by file
// path. A nil entry records a failed read so a missing file isn't retried on
// every widget build.
var (
	iconMu        sync.Mutex
	iconResources = map[string]fyne.Resource{}
)

// iconResource loads an asset file once and returns it as a shared resource.
// Repeated calls for the same path return the cached resource instead of
// re-reading and re-decoding the file, so building many widgets that share an
// icon (notably the per-message hover actions) doesn't touch the disk once per
// widget.
func iconResource(path string) fyne.Resource {
	iconMu.Lock()
	defer iconMu.Unlock()

	if res, ok := iconResources[path]; ok {
		return res
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("load icon %s: %v", path, err)
		iconResources[path] = nil
		return nil
	}

	res := fyne.NewStaticResource(filepath.Base(path), data)
	iconResources[path] = res
	return res
}

// newIconImage builds a canvas image for the given resource (a Fyne theme icon
// or an asset loaded via iconResource).
func newIconImage(res fyne.Resource) *canvas.Image {
	return canvas.NewImageFromResource(res)
}
