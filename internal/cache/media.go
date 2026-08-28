package cache

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MediaCache is downloaded originals on disk — the video files a sandboxed
// decoder is pointed at. Nothing here is ever decoded or parsed: bytes come
// off the network under a ceiling, are named by what a caller-supplied sniff
// says they are, and are evicted by recency under a budget. It keeps no
// memory half — a video is read by a subprocess, not drawn — which is why it
// is not an ImageCache with a different folder.
type MediaCache struct {
	dir    string
	client *http.Client

	budget atomic.Int64

	mu       sync.Mutex
	inflight map[string]*mediaFetch // single-flight by id
}

// mediaFetch is one in-flight download; latecomers wait on done and share the
// answer.
type mediaFetch struct {
	done chan struct{}
	path string
	err  error
}

// VideosFolder holds fetched video originals under the cache root, apart from
// the picture folders for the reason those are apart: one afternoon of videos
// must not evict every avatar, and each folder answers to its own budget.
const VideosFolder = "videos"

// NewMediaCache creates a store in folder under root, with a disk budget in
// bytes.
func NewMediaCache(root, folder string, budget int64) *MediaCache {
	dir := filepath.Join(CacheRoot(root), folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("media cache: create dir: %v", err)
	}

	c := &MediaCache{
		dir:      dir,
		client:   &http.Client{Timeout: 10 * time.Minute}, // a whole file, not a request
		inflight: make(map[string]*mediaFetch),
	}
	c.budget.Store(budget)

	return c
}

// SetBudget changes what the store may occupy; enforced on the next fetch.
func (c *MediaCache) SetBudget(budget int64) { c.budget.Store(budget) }

// Path reports the file already on disk for id, stamping its recency. It
// reads the directory, so keep it off the UI thread.
func (c *MediaCache) Path(id string) (string, bool) {
	if !validMediaID(id) {
		return "", false
	}

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return "", false
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, id+".") || strings.HasSuffix(name, ".tmp") {
			continue
		}

		path := filepath.Join(c.dir, name)
		now := time.Now()
		_ = os.Chtimes(path, now, now)

		return path, true
	}

	return "", false
}

// Fetch downloads url to disk under id and answers the file's path. The file
// is capped at maxBytes and named by what sniff reads out of its bytes — a
// download nothing recognises is deleted, not kept under the sender's word
// for it. progress reports bytes so far against the total (total 0 when the
// server does not say); it is called from the downloading goroutine.
//
// Single-flight per id: a second caller waits for the first and shares its
// answer. Blocking — call off the UI thread.
func (c *MediaCache) Fetch(id, url string, maxBytes int64, sniff func(path string) (ext string, ok bool), progress func(done, total int64)) (string, error) {
	if !validMediaID(id) {
		return "", errors.New("media: bad id")
	}
	if url == "" {
		return "", errors.New("media: no URL")
	}

	c.mu.Lock()
	if call, ok := c.inflight[id]; ok {
		c.mu.Unlock()
		<-call.done
		return call.path, call.err
	}
	call := &mediaFetch{done: make(chan struct{})}
	c.inflight[id] = call
	c.mu.Unlock()

	call.path, call.err = c.fetch(id, url, maxBytes, sniff, progress)

	c.mu.Lock()
	delete(c.inflight, id)
	c.mu.Unlock()
	close(call.done)

	return call.path, call.err
}

func (c *MediaCache) fetch(id, url string, maxBytes int64, sniff func(string) (string, bool), progress func(done, total int64)) (string, error) {
	if path, ok := c.Path(id); ok {
		return path, nil
	}

	resp, err := c.client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media: fetch: %s", resp.Status)
	}

	// The declared length caps the fetch before a byte lands; the read below
	// still enforces it, a Content-Length being just a claim.
	total := resp.ContentLength
	if total > maxBytes {
		return "", fmt.Errorf("media: %d bytes is past the %d allowed", total, maxBytes)
	}
	if total < 0 {
		total = 0
	}

	tmp, err := os.CreateTemp(c.dir, id+"-*.tmp")
	if err != nil {
		return "", err
	}
	written, err := copyReporting(tmp, io.LimitReader(resp.Body, maxBytes+1), total, progress)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil && written > maxBytes {
		err = fmt.Errorf("media: body ran past the %d bytes allowed", maxBytes)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}

	ext, ok := sniff(tmp.Name())
	if !ok {
		_ = os.Remove(tmp.Name())
		return "", errors.New("media: not a recognised container")
	}

	path := filepath.Join(c.dir, id+ext)
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}

	c.trim(path)

	return path, nil
}

// copyReporting is io.Copy with a progress report per chunk landed.
func copyReporting(dst io.Writer, src io.Reader, total int64, progress func(done, total int64)) (int64, error) {
	buf := make([]byte, 256*1024)

	var written int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return written, err
		}
	}
}

// trim evicts oldest-first until the folder is back under budget. keep is the
// file just stored, never a candidate — a file larger than the whole budget
// still resolves rather than evicting itself. Serialised by the fetch lock's
// callers being the only writers; stale temps older than any live download
// are swept on the way.
func (c *MediaCache) trim(keep string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	type mediaFile struct {
		path     string
		size     int64
		modified time.Time
	}

	cutoff := time.Now().Add(-time.Hour)
	var files []mediaFile
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.IsDir() {
			continue
		}

		path := filepath.Join(c.dir, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(path)
			}
			continue
		}

		files = append(files, mediaFile{path, info.Size(), info.ModTime()})
		total += info.Size()
	}

	budget := c.budget.Load()
	if total <= budget {
		return
	}

	slices.SortFunc(files, func(x, y mediaFile) int { return x.modified.Compare(y.modified) })
	for _, file := range files {
		if total <= budget {
			return
		}
		if file.path == keep {
			continue
		}
		if err := os.Remove(file.path); err != nil {
			log.Printf("media cache: remove %s: %v", file.path, err)
			continue
		}
		total -= file.size
	}
}

// validMediaID keeps an id a bare filename stem: ids are ULIDs or hex hashes,
// and anything else has no business naming a path.
func validMediaID(id string) bool {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return false
	}

	return true
}
