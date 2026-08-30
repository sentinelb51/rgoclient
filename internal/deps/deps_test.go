package deps

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The client's one download of something it will then execute. A digest that is
// not checked fails silently and looks exactly like one that is, so what these
// cover is the refusal itself: the wrong bytes must not reach the zip reader,
// must leave nothing behind, and must not be the same code path as a broken
// archive — hence the archive being valid in every case, the digest the only
// thing wrong.

func TestInstallRefusesAnArchiveThatFailsItsDigest(t *testing.T) {
	archive := validArchive(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer server.Close()

	root := t.TempDir()
	pinned := syntheticBuild(server.URL, digestOf([]byte("the bytes this client was built against")))

	err := install(context.Background(), root, pinned, nil)
	if err == nil {
		t.Fatal("an archive whose digest does not match the pin was accepted")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("the refusal does not say it was a checksum: %v", err)
	}

	// Never extracted: the version directory is what FFmpeg reports a complete
	// install from, and it must not exist at all.
	if _, err := os.Stat(filepath.Join(root, toolFolder, pinned.Version)); !os.IsNotExist(err) {
		t.Error("a refused archive was extracted anyway")
	}

	// And nothing left on disk to be picked up or run later.
	assertNoLeftovers(t, filepath.Join(root, toolFolder))
}

// The control. Without it the refusal above passes for any reason at all —
// a server that never answered, a zip the reader could not open — and would go
// on passing if the digest check were deleted outright.
func TestInstallAcceptsTheArchiveItWasPinnedTo(t *testing.T) {
	archive := validArchive(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer server.Close()

	root := t.TempDir()
	pinned := syntheticBuild(server.URL, digestOf(archive))

	var last int64
	err := install(context.Background(), root, pinned, func(done, total int64) { last = done })
	if err != nil {
		t.Fatalf("the pinned archive was refused: %v", err)
	}

	dir := filepath.Join(root, toolFolder, pinned.Version)
	for name, want := range map[string]string{
		exeName("ffmpeg"):  "ffmpeg binary",
		exeName("ffprobe"): "ffprobe binary",
	} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s was not installed: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}

	if last != int64(len(archive)) {
		t.Errorf("progress ended at %d of %d bytes", last, len(archive))
	}

	// The archive itself is working space and does not survive the install.
	assertNoLeftovers(t, filepath.Join(root, toolFolder))
}

// A member is picked by suffix and written to a name this package chose, so an
// entry claiming a path of its own has nowhere to go. Nothing reads the entry
// name as a destination today; this is what says so if someone later "restores"
// the archive's own layout.
func TestInstallIgnoresThePathAnArchiveClaims(t *testing.T) {
	archive := archiveOf(t, map[string]string{
		"../../../escaped/bin/" + exeName("ffmpeg"):  "ffmpeg binary",
		"../../../escaped/bin/" + exeName("ffprobe"): "ffprobe binary",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer server.Close()

	root := t.TempDir()
	pinned := syntheticBuild(server.URL, digestOf(archive))

	if err := install(context.Background(), root, pinned, nil); err != nil {
		t.Fatalf("install: %v", err)
	}

	dir := filepath.Join(root, toolFolder, pinned.Version)
	if _, err := os.Stat(filepath.Join(dir, exeName("ffmpeg"))); err != nil {
		t.Errorf("the binary did not land where this package puts it: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
		t.Error("an entry name reached outside the install directory")
	}
}

// An archive carrying only half the pair is a failed install, not one to report
// as complete: the client needs ffprobe as much as ffmpeg.
func TestInstallRefusesAnArchiveMissingHalfThePair(t *testing.T) {
	archive := archiveOf(t, map[string]string{
		"pkg/bin/" + exeName("ffmpeg"): "ffmpeg binary",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer server.Close()

	root := t.TempDir()
	pinned := syntheticBuild(server.URL, digestOf(archive))

	if err := install(context.Background(), root, pinned, nil); err == nil {
		t.Fatal("an archive holding one of the two binaries was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, toolFolder, pinned.Version)); !os.IsNotExist(err) {
		t.Error("half an install was left where FFmpeg would look for a whole one")
	}
}

/* Helpers */

func syntheticBuild(url, digest string) build {
	return build{
		Version:       "0.0.0-test",
		URL:           url,
		Digest:        digest,
		Size:          1,
		FFmpegMember:  "/bin/" + exeName("ffmpeg"),
		FFprobeMember: "/bin/" + exeName("ffprobe"),
	}
}

// validArchive is what a good download looks like: both members, under the one
// wrapping directory the real archives use.
func validArchive(t *testing.T) []byte {
	t.Helper()

	return archiveOf(t, map[string]string{
		"pkg/bin/" + exeName("ffmpeg"):  "ffmpeg binary",
		"pkg/bin/" + exeName("ffprobe"): "ffprobe binary",
	})
}

func archiveOf(t *testing.T, members map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range members {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// assertNoLeftovers checks the working files are gone: a part-file kept after a
// refusal is a downloaded archive sitting beside the binaries.
func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") || strings.HasSuffix(entry.Name(), ".partial") {
			t.Errorf("%s was left behind", entry.Name())
		}
	}
}
