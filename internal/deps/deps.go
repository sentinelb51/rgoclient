// Package deps manages the one program this client needs and does not ship:
// ffmpeg. Discovery stays the first answer — a machine carrying its own build
// keeps it, encoders and all — and this is the second, for the machines that
// have none, which on Windows is nearly all of them.
//
// No internal dependencies: the directory arrives as an argument the way the
// caches take theirs. Install is a network round trip and a disk write and must
// be called off the UI thread; everything else here is a stat or a table
// lookup.
package deps

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

/* Where it lives */

const (
	// appFolder is this client's directory inside whatever the platform calls a
	// user's data directory; toolFolder is ffmpeg's inside that.
	appFolder  = "RGOClient"
	toolFolder = "ffmpeg"
)

// Root returns the directory downloaded dependencies are kept under: the
// configured one, or the platform's own place for data an application
// installed.
//
// Deliberately not cache.CacheRoot. That root is budgeted and trimmed by
// recency, and a toolchain evicted between two runs is a screenshare that stops
// working for no reason the reader can see. A cache holds what can be fetched
// again on demand; this holds what was installed once.
func Root(configured string) string {
	if configured != "" {
		return configured
	}

	switch runtime.GOOS {
	case "windows":
		// LocalAppData rather than Roaming: a domain profile copies Roaming at
		// every sign-in, and this is a hundred megabytes of binary specific to
		// the machine it was unpacked on.
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, appFolder, "deps")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", appFolder, "deps")
		}
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, appFolder, "deps")
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", appFolder, "deps")
		}
	}

	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, appFolder, "deps")
	}

	return filepath.Join(".", "deps")
}

/* What is pinned */

// build is the archive one platform's ffmpeg is extracted from, named by digest
// rather than by tag. A release of this client installs the bytes it was tested
// against: a publisher who rebuilds under the same name fails the check instead
// of being run.
type build struct {
	Version string // ffmpeg's own, and the folder the pair is installed under
	URL     string
	Digest  string // SHA-256 of the archive, lowercase hex
	Size    int64  // what an offer of a download has to say out loud

	// The suffix each binary's path inside the archive ends with. A suffix
	// rather than a whole path: the archive wraps everything in one directory
	// named after the build.
	FFmpegMember  string
	FFprobeMember string
}

// builds is every platform with a managed copy.
//
// Windows takes gyan.dev's essentials build, published as GyanD/codexffmpeg on
// GitHub. It carries libx264 and every hardware encoder this client probes for
// — NVENC, AMF and QSV — plus the d3d11 support ddagrab needs, and it is
// released per ffmpeg version rather than under a rolling tag, so the pin still
// resolves in a year. The competing source, BtbN/FFmpeg-Builds, keeps fewer
// than a hundred releases at a time: a dated tag pinned here would 404 within
// months.
//
// A platform absent from this table is told what to run instead. Every Linux
// distribution ships ffmpeg in its own repository and macOS has Homebrew, so
// neither earns a hundred-megabyte download this client has to verify and carry
// a digest for — and the Linux builds are .tar.xz, which the standard library
// cannot read at all.
var builds = map[string]build{
	"windows/amd64": {
		Version:       "9.0.1",
		URL:           "https://github.com/GyanD/codexffmpeg/releases/download/9.0.1/ffmpeg-9.0.1-essentials_build.zip",
		Digest:        "fec81ae03971d9dd4be3ebe02e263bd2ec1d789483f931bdba5f5715e65da2e9",
		Size:          111253802,
		FFmpegMember:  "/bin/ffmpeg.exe",
		FFprobeMember: "/bin/ffprobe.exe",
	},
}

// manual is the one thing a reader on a platform with no managed build should
// do, spelled as the command it is.
var manual = map[string]string{
	"linux":  "Install it with your package manager: apt install ffmpeg, dnf install ffmpeg or pacman -S ffmpeg.",
	"darwin": "Install it with Homebrew: brew install ffmpeg.",
}

// fallbackAdvice is for a platform in neither table — a BSD, or an architecture
// no build targets.
const fallbackAdvice = "Install ffmpeg and make sure both ffmpeg and ffprobe are on your PATH."

// ErrNoBuild is a platform with no managed copy on offer. A state rather than a
// failure: Advice is the sentence that goes with it.
var ErrNoBuild = errors.New("no managed ffmpeg is published for this platform")

// current is this machine's build, if one is published for it.
func current() (build, bool) {
	b, ok := builds[runtime.GOOS+"/"+runtime.GOARCH]

	return b, ok
}

// Offered reports whether this platform can download a copy, and how many bytes
// that costs — the number the offer has to carry, nobody having agreed to an
// unnamed download.
func Offered() (size int64, ok bool) {
	b, ok := current()

	return b.Size, ok
}

// Advice is what a reader whose platform has no managed copy should do instead.
// Empty where one is offered, so a caller can use it as the whole answer.
func Advice() string {
	if _, ok := current(); ok {
		return ""
	}
	if line, ok := manual[runtime.GOOS]; ok {
		return line
	}

	return fallbackAdvice
}

/* The installed copy */

// FFmpeg reports the managed pair, if a complete one at the pinned version is
// installed. Both or nothing, the way video.Discover answers for PATH: half an
// install is a failed one, and the client needs ffprobe as much as ffmpeg.
func FFmpeg(root string) (ffmpeg, ffprobe string, ok bool) {
	b, ok := current()
	if !ok {
		return "", "", false
	}

	dir := filepath.Join(root, toolFolder, b.Version)
	ffmpeg = filepath.Join(dir, exeName("ffmpeg"))
	ffprobe = filepath.Join(dir, exeName("ffprobe"))
	if !isFile(ffmpeg) || !isFile(ffprobe) {
		return "", "", false
	}

	return ffmpeg, ffprobe, true
}

// Version is the ffmpeg release this client would install, for a row that has
// to name it before anything is downloaded.
func Version() string {
	b, _ := current()

	return b.Version
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}

	return base
}

func isFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

/* Installing */

const (
	// downloadTimeout bounds the whole exchange. Generous because the archive is
	// a hundred megabytes and the reader asked for it; not unbounded, because a
	// stalled connection must not hold a worker open forever.
	downloadTimeout = 30 * time.Minute

	// maxMember caps what one archive member may inflate to. The binaries are
	// tens of megabytes; anything past this is a compression bomb, which the
	// digest check does not catch because the bytes are the pinned bytes.
	maxMember = 512 << 20
)

// Install downloads this platform's build, checks the whole archive against the
// pinned digest and extracts the two binaries. progress may be nil; its total
// is zero where the server declines to say how long the body is.
//
// Nothing from the archive names anything on disk: the two members are picked
// by suffix and written to paths chosen here, so a crafted entry name has
// nowhere to escape to. The digest is checked before the archive is opened at
// all, so a mismatch never reaches the zip reader.
func Install(ctx context.Context, root string, progress func(done, total int64)) error {
	b, ok := current()
	if !ok {
		return ErrNoBuild
	}

	return install(ctx, root, b, progress)
}

// install is Install with the build named rather than looked up, which is what
// lets the refusal be tested: the pinned table points at the real internet, and
// what has to be exercised is a digest that does not match.
func install(ctx context.Context, root string, b build, progress func(done, total int64)) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	dir := filepath.Join(root, toolFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// A process that exited mid-copy left its .part behind, and nothing else
	// ever reads this folder — so the next attempt is what sweeps. The running
	// download's own file is younger than any orphan by construction.
	if stale, _ := filepath.Glob(filepath.Join(dir, "download-*.part")); stale != nil {
		for _, path := range stale {
			os.Remove(path)
		}
	}

	archive, err := download(ctx, dir, b, progress)
	if err != nil {
		return err
	}
	defer os.Remove(archive)

	return extract(archive, filepath.Join(dir, b.Version), b)
}

// download fetches the archive to a temporary file beside its destination,
// hashing as the bytes land, and answers the path only once the digest matches.
func download(ctx context.Context, dir string, b build, progress func(done, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading ffmpeg: the server answered %s", resp.Status)
	}

	file, err := os.CreateTemp(dir, "download-*.part")
	if err != nil {
		return "", err
	}
	path := file.Name()

	digest := sha256.New()
	counted := &counter{r: resp.Body, total: resp.ContentLength, report: progress}
	_, err = io.Copy(io.MultiWriter(file, digest), counted)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)

		return "", err
	}

	if got := hex.EncodeToString(digest.Sum(nil)); got != b.Digest {
		os.Remove(path)

		// Never retried and never run anyway: the bytes this client was built
		// against are not the bytes that arrived, and which of the two is wrong
		// is not something this end can find out.
		return "", fmt.Errorf("the downloaded ffmpeg does not match the checksum this client was built with (wanted %s, got %s)", b.Digest, got)
	}

	return path, nil
}

// extract writes the two binaries into a staging directory and moves it into
// place whole, so a failure part-way through never leaves an install FFmpeg
// would go on to report as complete.
func extract(archive, dest string, b build) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()

	staging := dest + ".partial"
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	wanted := map[string]string{
		b.FFmpegMember:  exeName("ffmpeg"),
		b.FFprobeMember: exeName("ffprobe"),
	}

	found := 0
	for _, entry := range reader.File {
		for suffix, name := range wanted {
			if !strings.HasSuffix(entry.Name, suffix) {
				continue
			}
			if err := extractOne(entry, filepath.Join(staging, name)); err != nil {
				return err
			}
			found++
		}
	}
	if found != len(wanted) {
		return fmt.Errorf("the ffmpeg archive holds %d of the %d binaries expected", found, len(wanted))
	}

	// Removed rather than renamed aside: on Windows this fails outright while a
	// child is running out of the directory, which is the honest answer — a
	// reinstall during a share is not something to do quietly behind the share.
	if err := os.RemoveAll(dest); err != nil {
		return err
	}

	return os.Rename(staging, dest)
}

func extractOne(entry *zip.File, path string) error {
	src, err := entry.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	// 0o755 rather than the archive's own mode: a zip built on Windows carries
	// none worth honouring, and these have to be executable everywhere.
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}

	written, err := io.Copy(dst, io.LimitReader(src, maxMember+1))
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written > maxMember {
		return fmt.Errorf("%s is larger than this client will unpack", entry.Name)
	}

	return nil
}

// counter reports how far a download has got. It reports on every read, which
// is every few tens of kilobytes: how often that is worth drawing is the
// caller's decision, the way it is for the input meter.
type counter struct {
	r     io.Reader
	total int64
	done  int64

	report func(done, total int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.report != nil {
		c.report(c.done, c.total)
	}

	return n, err
}
