// Package update asks GitHub whether a newer release of this client exists.
//
// It imports nothing internal and knows nothing about the UI: what is done with
// an answer — announcing it, drawing it, opening it — belongs to the controller.
// Latest is a network round trip and must be called off the UI thread; everything
// else here is pure.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/* Versions */

// Unstamped is what a plain `go build` leaves in main.version. It compares older
// than every real release, so a working tree would be offered an update on every
// launch — Comparable is what refuses it.
const Unstamped = "0.0.0"

// devSuffix marks a CI build of main: build.yml stamps the number the *next*
// release will carry, so 26.9.1-dev precedes 26.9.1 rather than equalling it.
const devSuffix = "-dev"

// version is a calendar version as the tags spell it: YY.M.N, optionally
// v-prefixed on the ones predating that scheme, optionally a -dev build of a
// number not yet released.
type version struct {
	parts [3]int
	dev   bool
}

// parseVersion reads one, reporting whether it is a version at all. Anything else
// — a hand-written tag, a fork's own scheme — is left uncompared rather than
// guessed at.
func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")

	var v version
	if rest, found := strings.CutSuffix(s, devSuffix); found {
		s, v.dev = rest, true
	}

	fields := strings.Split(s, ".")
	if len(fields) != len(v.parts) {
		return version{}, false
	}

	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.parts[i] = n
	}

	return v, true
}

// newer reports whether other is a later release than v.
func (v version) newer(other version) bool {
	for i := range v.parts {
		if v.parts[i] != other.parts[i] {
			return other.parts[i] > v.parts[i]
		}
	}

	// The same number either way: a -dev build of it is the run that preceded the
	// release, so the release supersedes it.
	return v.dev && !other.dev
}

// Comparable reports whether a version can be measured against a release at all.
// The unstamped local build cannot: it is older than everything, and saying so on
// every launch of a working tree is noise rather than news.
func Comparable(v string) bool {
	if v == Unstamped {
		return false
	}
	_, ok := parseVersion(v)

	return ok
}

// Newer reports whether candidate supersedes current. False whenever either
// cannot be read — an update is claimed only when it is certain.
func Newer(current, candidate string) bool {
	running, runningOK := parseVersion(current)
	latest, latestOK := parseVersion(candidate)

	return runningOK && latestOK && running.newer(latest)
}

/* Platforms */

// target is what release.yml's matrix builds for one platform: the token its
// asset is named for, and the name a person would call the platform.
type target struct {
	token string
	label string
}

// targets is every platform that workflow builds. One absent from it has no
// asset at all — nothing builds darwin/amd64 or linux/arm64 — and offering the
// wrong binary is worse than saying there is none.
var targets = map[string]target{
	"windows/amd64": {"windows-amd64", "Windows"},
	"linux/amd64":   {"linux-amd64", "Linux"},
	"darwin/arm64":  {"macos-arm64", "macOS on Apple silicon"},
}

// Platform names a platform for a sentence about it, falling back to Go's own
// spelling for one no release is built for.
func Platform(goos, goarch string) string {
	if t, ok := targets[goos+"/"+goarch]; ok {
		return t.label
	}

	return goos + "/" + goarch
}

/* Releases */

// Release is one published release, as much of it as the client has a use for.
type Release struct {
	Version   string // the tag, less the v the older ones carry
	Notes     string // the body, Markdown as it was written
	URL       string // the release page
	Published time.Time

	Assets []Asset
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// assetPrefix is what release.yml names every asset with, ahead of the target and
// whatever extension that platform ships in.
const assetPrefix = "RGOClient-"

// AssetFor is the asset built for a platform, if this release carries one.
func (r Release) AssetFor(goos, goarch string) (Asset, bool) {
	t, ok := targets[goos+"/"+goarch]
	if !ok {
		return Asset{}, false
	}

	// The extension is the platform's rather than the target's — a zip on Windows,
	// a tarball elsewhere — so the match ends at the separator before it.
	name := assetPrefix + t.token + "."
	for _, asset := range r.Assets {
		if strings.HasPrefix(asset.Name, name) {
			return asset, true
		}
	}

	return Asset{}, false
}

/* Asking GitHub */

// latestURL is the newest published release of the client's own repository.
// GitHub excludes drafts and prereleases from this route, so nothing here has to.
const latestURL = "https://api.github.com/repos/sentinelb51/rgoclient/releases/latest"

// timeout bounds the whole exchange. A check nobody asked for must not hold a
// worker open against a network that will not answer.
const timeout = 15 * time.Second

// bodyLimit caps what is read back. Release notes are prose and the asset list is
// half a dozen entries; anything past this is not an answer to this question.
const bodyLimit = 1 << 20

// ErrNoRelease is a repository with nothing published yet, which is a state
// rather than a failure: the client is up to date with everything there is.
var ErrNoRelease = errors.New("no release published")

// Latest fetches the newest release. agent is the User-Agent GitHub requires of
// an unauthenticated caller — without one the request is refused outright.
//
// Unauthenticated is sixty calls an hour per address and this client spends one
// per run, so a rate limit here means something else on the machine is asking:
// it is reported rather than retried.
func Latest(ctx context.Context, agent string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", agent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Release{}, ErrNoRelease
	case http.StatusForbidden, http.StatusTooManyRequests:
		return Release{}, fmt.Errorf("github refused the request (%s)", resp.Status)
	default:
		return Release{}, fmt.Errorf("github answered %s", resp.Status)
	}

	var body struct {
		TagName     string    `json:"tag_name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`

		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&body); err != nil {
		return Release{}, err
	}
	if body.TagName == "" {
		return Release{}, ErrNoRelease
	}

	release := Release{
		Version:   strings.TrimPrefix(body.TagName, "v"),
		Notes:     strings.TrimSpace(body.Body),
		URL:       body.HTMLURL,
		Published: body.PublishedAt,
		Assets:    make([]Asset, len(body.Assets)),
	}
	for i, asset := range body.Assets {
		release.Assets[i] = Asset{Name: asset.Name, URL: asset.URL, Size: asset.Size}
	}

	return release, nil
}
