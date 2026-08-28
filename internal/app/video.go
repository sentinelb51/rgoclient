package app

// The controller half of the video player (docs/video-player.md): fetching a
// sender's file to disk, probing and postering it through internal/video's
// sandboxed children, and pumping one playback at a time into a ui.VideoCard.
// The card is dumb on purpose — every policy is here: what a mount may fetch,
// what a tap decodes with, which file is handed to the OS, and the rule that
// starting one video stops the other.
//
// Playback is two children and two pumps. Frames are paced by the wall clock
// against the rate this side asked for, painted through one waited doOnUI hop
// each — waited because the pump's scratch buffer is reused the moment the
// hop returns. Sound is a mixer lane paced by the speakers (Sink.Wake/Want),
// the way a call participant's is. Pause is a stop that remembers its
// position: -ss on a restarted child is the only correct seek over a pipe,
// so pause, seek and resume are all the same restart.

import (
	"errors"
	"fmt"
	"image"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/video"
)

const (
	// videoFPS caps the frame pipe. 30 is what a chat card is worth: the pipe
	// carries width×height×4 bytes per frame, and the texture upload per
	// paint is the cost docs/performance.md prices.
	videoFPS = 30

	// videoMaxFetchBytes refuses a file before a byte of it lands; the
	// attachment carries Size, and the store enforces it again on the body.
	videoMaxFetchBytes = 512 << 20

	// videoPosterFetchBytes is how large a file a mere mount may fetch for
	// its poster. Past it the poster waits for the first tap, which fetches
	// anyway — scrolling past a hundred-megabyte video must not download it.
	videoPosterFetchBytes = 32 << 20

	// videoPosterSuffix keys a poster beside its file's other renditions in
	// the image cache. No Autumn ID can spell it, "-" being theirs but the
	// suffix not being a rendition Autumn serves.
	videoPosterSuffix = "-poster"

	// videoProgressStep is how often the card's clock and scrub move; every
	// frame would be a chip rewrite per paint for digits nobody can read.
	videoProgressStep = 500 * time.Millisecond

	// videoResumeSlack collapses a resume this close to the end into a
	// restart from the top — resuming into the last instants plays a blink
	// of video and ends.
	videoResumeSlack = time.Second
)

// videoPlayback is one running playback: the two children, the card being
// painted, and where in the file the run began. It is created on the UI
// thread, its streams installed by one hop (the installCall arrangement), and
// halted from anywhere.
type videoPlayback struct {
	id   string
	file *domain.File
	card *ui.VideoCard

	path   string
	format string
	info   video.Info

	width, height int
	base          time.Duration // where the streams began; the -ss offset
	loop          bool          // a GIF-mannered embed: the pipe never ends

	frames *video.Stream
	sound  *video.Stream // nil for a silent file, or when its child failed

	stop    chan struct{}
	stopped sync.Once
	played  atomic.Int64 // ns decoded past base, written by the frame pump
}

// halt kills both children and reaps them. Idempotent, safe from any
// goroutine, and tolerant of streams not yet installed.
func (p *videoPlayback) halt() {
	p.stopped.Do(func() { close(p.stop) })

	if p.frames != nil {
		p.frames.Stop()
	}
	if p.sound != nil {
		p.sound.Stop()
	}
}

// position is how far into the file playback has reached — wrapped for a
// looping stream, whose decoded time runs past the file's own.
func (p *videoPlayback) position() time.Duration {
	at := p.base + time.Duration(p.played.Load())
	if p.loop && p.info.Duration > 0 {
		at %= p.info.Duration
	}

	return at
}

/* The card's actions */

// OnVideoMounted fills a standing card in: the probed shape and duration if
// they are known or knowable, and the poster. A small file is fetched for the
// sake of its poster; a large one waits for the tap that would fetch it
// anyway.
func (a *App) OnVideoMounted(file *domain.File, card *ui.VideoCard) {
	id := ui.FileCacheID(file)
	if id == "" {
		return
	}

	if info, ok := a.videoInfo[id]; ok {
		card.SetInfo(info.Width, info.Height, info.Duration)
	}
	if a.videoFailed[id] {
		card.SetStatus("Not playable")
		return
	}
	if !a.videoInline || a.videoBusy[id] {
		return
	}
	a.videoBusy[id] = true

	info, haveInfo := a.videoInfo[id]
	epoch := a.epoch

	a.background(func() error {
		poster, got, gotInfo, err := a.resolveVideoCard(id, file, info, haveInfo)

		a.doOnUI(func() {
			delete(a.videoBusy, id)
			if a.stale(epoch) {
				return
			}
			if err != nil {
				if permanentVideoError(err) {
					a.videoFailed[id] = true
					card.SetStatus("Not playable")
				}
				return
			}
			if gotInfo {
				a.videoInfo[id] = got
				card.SetInfo(got.Width, got.Height, got.Duration)
			}
			if poster != nil {
				card.SetPoster(poster)
			}
		}, false)

		if err != nil && !permanentVideoError(err) {
			log.Printf("video %s: poster: %v", id, err)
		}

		return nil
	}, func(error) {})
}

// OnVideoTapped is the transport toggle: pause the video this card is
// playing, otherwise start it — stopping whatever else was playing, one
// playback being the rule the GIF animator already keeps.
func (a *App) OnVideoTapped(file *domain.File, card *ui.VideoCard) {
	id := ui.FileCacheID(file)
	if id == "" {
		return
	}

	if p := a.video; p != nil && p.id == id && p.card == card {
		a.pauseVideo()
		return
	}

	if !a.videoInline {
		a.notifyTitled(ui.ToneWarning, "No video decoder",
			"Inline playback needs ffmpeg, which was not found on this machine. "+
				"\"Open in your player\" still works once one is installed for it to open with.")
		return
	}
	if a.videoFailed[id] {
		a.notify(ui.ToneWarning, "This file is not a video this client can play.")
		return
	}

	a.startVideo(id, file, card)
}

// OnVideoSeek moves the playhead. Over a pipe a seek is a restarted child, so
// a seek while playing relaunches the streams at the target; at rest it only
// moves where the next play begins.
func (a *App) OnVideoSeek(file *domain.File, card *ui.VideoCard, frac float64) {
	id := ui.FileCacheID(file)
	info, ok := a.videoInfo[id]
	if id == "" || !ok || info.Duration <= 0 {
		return
	}

	target := time.Duration(frac * float64(info.Duration))

	p := a.video
	if p == nil || p.card != card {
		a.videoAt[id] = target
		card.SetProgress(target)
		return
	}

	a.stopVideo()
	a.videoAt[id] = target
	a.launchVideo(p.id, p.file, card, p.path, p.format, p.info)
}

// OnVideoMuted is the card's sound toggle landing on the lane.
func (a *App) OnVideoMuted(_ *domain.File, card *ui.VideoCard, muted bool) {
	if p := a.video; p != nil && p.card == card && p.sound != nil {
		a.sounds.Sink().SetVideoGain(videoGain(muted))
	}
}

// OnVideoOpen hands the fetched file to the system player, under the name its
// bytes earned — the sniffed extension, never the sender's. A file nothing
// recognises is refused rather than handed to a shell that would believe its
// name.
func (a *App) OnVideoOpen(file *domain.File, card *ui.VideoCard) {
	id := ui.FileCacheID(file)
	if id == "" {
		return
	}

	card.SetStatus("Fetching...")
	epoch := a.epoch

	var path string
	a.backgroundThen(func() error {
		var err error
		path, _, err = a.fetchVideo(id, file, a.videoProgress(card, epoch, int64(file.Size)))

		return err
	}, func(err error) {
		card.SetStatus("")
		a.notifyTitled(ui.ToneWarning, "Not opened", "%v", err)
	}, func() {
		card.SetStatus("")
		a.openLocalFile(path)
	})
}

/* Starting and stopping */

// startVideo prepares the file on a worker — fetch, sniff, probe, all of it
// cached after the first time — and launches the streams. Two prepares racing
// resolve to whichever lands last; each stops what it finds running.
func (a *App) startVideo(id string, file *domain.File, card *ui.VideoCard) {
	a.stopVideo()
	card.SetStatus("Preparing...")

	info, haveInfo := a.videoInfo[id]
	epoch := a.epoch

	var path, format string
	var got video.Info
	a.backgroundThen(func() error {
		var err error
		path, format, err = a.fetchVideo(id, file, a.videoProgress(card, epoch, int64(file.Size)))
		if err != nil {
			return err
		}

		got = info
		if !haveInfo {
			got, err = a.videoTools.Probe(path, format)
		}

		return err
	}, func(err error) {
		card.SetStatus("")
		if permanentVideoError(err) {
			a.videoFailed[id] = true
		}
		a.notifyTitled(ui.ToneDanger, "Not played", "%v", err)
	}, func() {
		a.videoInfo[id] = got
		card.SetInfo(got.Width, got.Height, got.Duration)
		a.launchVideo(id, file, card, path, format, got)
	})
}

// launchVideo starts the decode children for a prepared file and installs
// them. The card has its final shape by now, so its box is the pipe's frame
// size. UI thread.
func (a *App) launchVideo(id string, file *domain.File, card *ui.VideoCard, path, format string, info video.Info) {
	a.stopVideo()

	at := a.videoAt[id]
	if info.Duration > 0 && at > info.Duration-videoResumeSlack {
		at = 0
	}

	width, height := card.DecodeSize()
	p := &videoPlayback{
		id: id, file: file, card: card,
		path: path, format: format, info: info,
		width: width, height: height, base: at,
		loop: card.Loop,
		stop: make(chan struct{}),
	}
	a.video = p
	card.SetStatus("Starting...")

	epoch := a.epoch
	go func() {
		frames, err := a.videoTools.Frames(video.FrameConfig{
			Path: path, Format: format,
			Width: width, Height: height, FPS: videoFPS,
			Start: at, Loop: p.loop,
		})
		if err != nil {
			a.doOnUI(func() {
				if a.video == p {
					a.video = nil
				}
				if a.stale(epoch) {
					return
				}
				card.SetStatus("")
				a.notifyTitled(ui.ToneDanger, "Not played", "The decoder could not start: %v", err)
			}, false)
			return
		}

		var sound *video.Stream
		if info.HasAudio {
			sound, err = a.videoTools.PCM(video.PCMConfig{Path: path, Format: format, Start: at, Loop: p.loop})
			if err != nil {
				// A silent video is still the video; the failure is logged
				// and the card simply offers no sound toggle.
				log.Printf("video %s: sound: %v", id, err)
			}
		}

		// The install is one hop, after the blocking starts — the installCall
		// arrangement. A playback landing into a replaced session, or after
		// another play took over, is closed rather than installed.
		a.doOnUI(func() {
			if a.stale(epoch) || a.video != p {
				go func() {
					frames.Stop()
					if sound != nil {
						sound.Stop()
					}
				}()
				return
			}

			p.frames = frames
			p.sound = sound
			if sound != nil {
				a.sounds.StartOutput()
				sink := a.sounds.Sink()
				sink.StartVideo()
				sink.SetVideoGain(videoGain(card.Muted()))
			}

			card.SetStatus("")
			card.ShowPlaying(sound != nil)
			card.SetProgress(p.base)

			go a.pumpVideoFrames(p, epoch)
			if sound != nil {
				go a.pumpVideoSound(p)
			}
		}, false)
	}()
}

// pauseVideo stops the running playback where it stands: the children die,
// the position is kept, and the card holds its frame under a play badge.
// UI thread.
func (a *App) pauseVideo() {
	p := a.video
	if p == nil {
		return
	}
	a.video = nil
	a.videoAt[p.id] = p.position()
	a.releaseVideoLane(p)

	go p.halt()
	p.card.ShowPaused()
}

// stopVideo ends the running playback and puts its card back at rest, keeping
// the position so a later tap resumes — leaving a channel abandons the card,
// not the fact that half of it was watched. UI thread; safe with nothing
// playing.
func (a *App) stopVideo() {
	p := a.video
	if p == nil {
		return
	}
	a.video = nil
	if at := p.position(); at > time.Second {
		a.videoAt[p.id] = at
	}
	a.releaseVideoLane(p)

	go p.halt()
	p.card.EndPlayback()
}

// releaseVideoLane closes the mixer lane a playback held, if any.
func (a *App) releaseVideoLane(p *videoPlayback) {
	if p.sound != nil {
		a.sounds.Sink().StopVideo()
	}
}

/* The pumps */

// pumpVideoFrames reads raw frames and paints them at the pipe's rate. The
// hop waits because scratch is reused the moment it returns; a pump more than
// two frames late drops the frame and re-anchors rather than queueing a
// slideshow. The pump is also what notices a dropped card — a discarded
// widget hears nothing, so the paint is the only thing placed to stop the
// decode.
func (a *App) pumpVideoFrames(p *videoPlayback, epoch uint64) {
	interval := time.Second / videoFPS
	scratch := make([]byte, p.width*p.height*4)

	start := time.Now()
	decoded := 0
	lastProgress := time.Duration(-videoProgressStep)

	for {
		if err := p.frames.ReadFrame(scratch); err != nil {
			a.settleVideoEnd(p, epoch)
			return
		}

		decoded++
		p.played.Store(int64(time.Duration(decoded) * interval))

		due := start.Add(time.Duration(decoded-1) * interval)
		now := time.Now()
		if wait := due.Sub(now); wait > 0 {
			select {
			case <-p.stop:
				return
			case <-time.After(wait):
			}
		} else if late := now.Sub(due); late > 2*interval {
			start = start.Add(late)
			continue
		}

		at := p.base + time.Duration(decoded)*interval
		if p.loop && p.info.Duration > 0 {
			at %= p.info.Duration
		}
		progress := at-lastProgress >= videoProgressStep || at < lastProgress
		if progress {
			lastProgress = at
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.video != p {
				return
			}
			if !p.card.Mounted() {
				a.stopVideo()
				return
			}

			p.card.ShowFrame(scratch)
			if progress {
				p.card.SetProgress(at)
			}
		}, true)

		select {
		case <-p.stop:
			return
		default:
		}
	}
}

// settleVideoEnd is the pipe ending: the file finished, or the child was
// killed. A kill means an owner is already putting the card right; a finish
// is this pump's to report.
func (a *App) settleVideoEnd(p *videoPlayback, epoch uint64) {
	select {
	case <-p.stop:
		return
	default:
	}

	a.doOnUI(func() {
		if a.stale(epoch) || a.video != p {
			return
		}
		a.video = nil
		delete(a.videoAt, p.id)
		a.releaseVideoLane(p)
		p.card.EndPlayback()
	}, false)

	go p.halt()
}

// pumpVideoSound tops the mixer lane up to what the speakers report short,
// woken by them — the pacing every lane writer keeps. The ticker is
// insurance: a call's own playout waits on the same wake channel, and a wake
// consumed there is one this pump never sees.
func (a *App) pumpVideoSound(p *videoPlayback) {
	sink := a.sounds.Sink()
	wake := sink.Wake()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	buf := make([]int16, 4096)
	for {
		select {
		case <-p.stop:
			return
		case <-wake:
		case <-ticker.C:
		}

		for {
			want := sink.VideoWant()
			if want <= 0 {
				break
			}

			got, err := p.sound.ReadPCM(buf[:min(want, len(buf))])
			if got > 0 {
				sink.WriteVideo(buf[:got])
			}
			if err != nil {
				return
			}
		}
	}
}

/* Preparing the file */

// fetchVideo brings the file to the media store and answers its path and
// demuxer. Single-flight and cached by the store; blocking, so call on a
// worker.
func (a *App) fetchVideo(id string, file *domain.File, progress func(done, total int64)) (string, string, error) {
	if file.Size > videoMaxFetchBytes {
		return "", "", fmt.Errorf("the file is %d MiB; up to %d MiB is fetched",
			file.Size>>20, videoMaxFetchBytes>>20)
	}

	path, err := a.videoMedia.Fetch(id, file.URL, videoMaxFetchBytes, func(candidate string) (string, bool) {
		_, ext, ok := video.Sniff(candidate)
		return ext, ok
	}, progress)
	if err != nil {
		return "", "", err
	}

	format, _, ok := video.Sniff(path)
	if !ok {
		return "", "", errVideoUnrecognised
	}

	return path, format, nil
}

// resolveVideoCard is the mount's worker: whatever of the probe and the
// poster can be had without fetching more than the mount policy allows. A
// file not on disk and past the poster ceiling resolves to nothing, with no
// error — the tap that plays it is what will fetch it.
func (a *App) resolveVideoCard(id string, file *domain.File, info video.Info, haveInfo bool) (image.Image, video.Info, bool, error) {
	path, ok := a.videoMedia.Path(id)
	if !ok {
		if file.Size <= 0 || file.Size > videoPosterFetchBytes {
			return nil, video.Info{}, false, nil
		}

		var err error
		path, _, err = a.fetchVideo(id, file, nil)
		if err != nil {
			return nil, video.Info{}, false, err
		}
	}

	format, _, ok := video.Sniff(path)
	if !ok {
		return nil, video.Info{}, false, errVideoUnrecognised
	}

	if !haveInfo {
		var err error
		info, err = a.videoTools.Probe(path, format)
		if err != nil {
			return nil, video.Info{}, false, err
		}
	}

	posterID := id + videoPosterSuffix
	poster := a.images.Get(posterID)
	if poster == nil {
		width, height := videoPosterSize(info.Width, info.Height)
		frame, err := a.videoTools.Poster(path, format, width, height)
		if err != nil {
			return nil, video.Info{}, false, err
		}

		poster = frame
		a.images.Set(posterID, frame)
	}

	return poster, info, !haveInfo, nil
}

// errVideoUnrecognised is a file whose bytes name no container this player
// speaks — permanent, unlike a fetch that merely failed.
var errVideoUnrecognised = errors.New("video: not a container this player recognises")

// permanentVideoError tells a failure of the file from a failure of the
// network: the driver's own refusals and an unrecognised container stay true
// on every retry, so they are memoised where a lost connection is not.
func permanentVideoError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()

	return strings.HasPrefix(text, "video:") || strings.Contains(text, "not a recognised container")
}

// videoProgress is the fetch's status line: a percentage into the card's
// chip, throttled to whole points, delivered on the UI thread and dropped
// with the session.
func (a *App) videoProgress(card *ui.VideoCard, epoch uint64, total int64) func(done, reported int64) {
	last := -1

	return func(done, reported int64) {
		if reported <= 0 {
			reported = total
		}
		if reported <= 0 {
			return
		}

		pct := int(done * 100 / reported)
		if pct == last {
			return
		}
		last = pct

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			card.SetStatus(fmt.Sprintf("Fetching %d%%", pct))
		}, false)
	}
}

// openLocalFile hands a file this client fetched and named to the OS default
// handler, through the toolkit's opener. Not ui.openURL — that gate is for
// destinations somebody else chose, and it refuses file URLs on purpose.
func (a *App) openLocalFile(path string) {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}

	if err := a.fyne.OpenURL(&url.URL{Scheme: "file", Path: slashed}); err != nil {
		a.notifyTitled(ui.ToneWarning, "Not opened", "No player would take the file: %v", err)
	}
}

func videoGain(muted bool) float64 {
	if muted {
		return 0
	}

	return 1
}

// videoPosterSize is the box a poster is rendered into: the card's own fit,
// in whole pixels, so the cached picture and the widget's box agree.
func videoPosterSize(width, height int) (int, int) {
	maxW := int(theme.Sizes.MessageImageMaxWidth)
	maxH := int(theme.Sizes.MessageImageMaxHeight)

	scale := 1.0
	if s := float64(maxW) / float64(width); s < scale {
		scale = s
	}
	if s := float64(maxH) / float64(height); s < scale {
		scale = s
	}

	return max(int(float64(width)*scale), 1), max(int(float64(height)*scale), 1)
}
