package app

// Which ffmpeg the client runs, and how one gets onto a machine that has none.
//
// Filed apart from video.go and screenshare.go because it serves both: one
// pair of binaries decides whether a video plays inline, whether a poster is
// drawn, whether a share can be watched and whether one can be sent. Nothing
// here knows what any of those do with it.

import (
	"context"
	"log"

	"RGOClient/internal/deps"
	"RGOClient/internal/ui"
	"RGOClient/internal/video"
)

// resolveVideoTools decides which ffmpeg this run uses. PATH first, so a reader
// who installed their own build keeps it — theirs may carry encoders the pinned
// one does not, and second-guessing that from here would be wrong more often
// than right. The downloaded copy is the answer only for a machine with none.
//
// Once per run: swapping the pair mid-session would strand the running children
// and invalidate the encoder probe's memo, which is keyed by family rather than
// by binary.
func (a *App) resolveVideoTools() {
	if tools, ok := video.Discover(); ok {
		a.videoTools, a.videoInline, a.videoManaged = tools, true, false

		return
	}

	if ffmpeg, ffprobe, ok := deps.FFmpeg(a.toolsDir); ok {
		a.videoTools = video.Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}
		a.videoInline, a.videoManaged = true, true
		log.Printf("video: using the downloaded ffmpeg at %s", ffmpeg)

		return
	}

	a.videoTools, a.videoInline, a.videoManaged = video.Tools{}, false, false
}

// ffmpegAdvice is what to tell a reader who has just been stopped by a missing
// ffmpeg: where to get one on a platform this client downloads for, and the
// command to run on a platform it does not.
func ffmpegAdvice() string {
	if advice := deps.Advice(); advice != "" {
		return advice
	}

	return "Settings → Screenshare can download it."
}

/* Installing */

// FFmpegState is what the settings row draws: which copy is in use, whether one
// can be downloaded, and how far a download has got.
func (a *App) ffmpegState() ui.FFmpegState {
	size, offered := deps.Offered()

	state := ui.FFmpegState{
		Path:        a.videoTools.FFmpeg,
		Found:       a.videoInline,
		Managed:     a.videoManaged,
		Directory:   a.toolsDir,
		Version:     deps.Version(),
		Size:        size,
		Offered:     offered,
		Advice:      deps.Advice(),
		Installing:  a.installingFFmpeg,
		Downloaded:  a.ffmpegDone,
		DownloadAll: a.ffmpegTotal,
	}

	return state
}

// installFFmpeg downloads the pinned build and takes it into use. Single-flight
// on installingFFmpeg: the archive is a hundred megabytes, and a second press
// while the first is in flight must cost nothing.
//
// redraw is called on the UI thread whenever there is something new to draw —
// the claim, each step of the download, and the answer — so the row reports
// progress without the page polling for it.
func (a *App) installFFmpeg(redraw func()) {
	if a.installingFFmpeg {
		return
	}
	if _, offered := deps.Offered(); !offered {
		a.notifyTitled(ui.ToneWarning, "Nothing to download", "%s", deps.Advice())

		return
	}

	a.installingFFmpeg = true
	a.ffmpegDone, a.ffmpegTotal = 0, 0
	redraw()

	root := a.toolsDir
	epoch := a.epoch

	// Reported at a rate a row can be drawn at rather than at the network's:
	// the counter fires on every read, which is thousands of times across a
	// hundred megabytes, and each hop is a repaint of the page.
	var last int64
	progress := func(done, total int64) {
		if done-last < ffmpegProgressStep && done != total {
			return
		}
		last = done

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			a.ffmpegDone, a.ffmpegTotal = done, total
			redraw()
		}, false)
	}

	a.backgroundThen(func() error {
		err := deps.Install(context.Background(), root, progress)

		// Completed here rather than in the callbacks below: the download is the
		// machine's, not the session's. A success landing in a stale epoch runs
		// neither callback (App.run), which would leave the claim stuck true and
		// the finished install unused until the next launch. Only the notices
		// below stay epoch-guarded.
		a.doOnUI(func() {
			a.installingFFmpeg = false
			if err == nil {
				a.resolveVideoTools()
			}
			redraw()
		}, false)

		return err
	}, func(err error) {
		a.notifyTitled(ui.ToneDanger, "ffmpeg not installed", "%v", err)
	}, func() {
		// After the completion post above — doOnUI is FIFO, so videoInline is set.
		if !a.videoInline {
			a.notifyTitled(ui.ToneDanger, "ffmpeg not installed",
				"The download finished but the binaries are not where they were expected.")

			return
		}

		a.notifyTitled(ui.ToneInfo, "ffmpeg installed",
			"Screen sharing and inline video work now, on ffmpeg %s.", deps.Version())
	})
}

// ffmpegProgressStep is how many bytes have to land before the row is redrawn.
// A megabyte is a visible move on any progress line and around a hundred hops
// across the whole archive.
const ffmpegProgressStep = 1 << 20
