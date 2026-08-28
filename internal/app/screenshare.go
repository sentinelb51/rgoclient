package app

// The receive half of a screenshare (docs/screenshare-todo.md): the watch a
// tap on a row's live mark opens, the window the stream is drawn in, and the
// pump between them. One watch at a time — one decoder child, the
// one-playback rule — and every decision is here because each is a policy
// about a remote participant's bitstream: what decodes it (internal/video's
// sandboxed child, fed on stdin instead of a file), at what size, and what
// tears it down — the window closing, the sender stopping, the call ending,
// a logout.
//
// Unlike the video player's, the frame pump paces nothing: a live stream is
// paced by the sender, so the pump paints what arrives when it arrives and
// always drains the pipe — a held frame is latency nothing takes back out.

import (
	"errors"
	"image"
	"image/color"
	"io"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/video"
	"RGOClient/internal/voice"
)

const (
	// shareDecodeCapWidth/Height bound the decoder's output. The window is
	// painted by texture scaling, so decoding past 1080p buys pixels a chat
	// client's window will not show, at four times the pipe and paint cost;
	// under it the sender's own size wins, text on a shared screen being
	// what downscaling blurs first.
	shareDecodeCapWidth  = 1920
	shareDecodeCapHeight = 1080

	// shareWindowWidth is the window's opening width; its height follows the
	// stream's aspect once that is known.
	shareWindowWidth = 960
)

// shareView is one watched screenshare: whose it is, the window it is drawn
// in, and — once the track has arrived — the decoder child and the buffers
// between it and the canvas.
type shareView struct {
	userID    string
	channelID string

	win      fyne.Window
	backdrop *canvas.Rectangle

	// frame is what the canvas draws and scratch what the pump reads into;
	// the copy between them happens under a waited UI hop, which is what
	// keeps the painter off bytes mid-frame. Both exist only once the feed
	// has mounted.
	view          *canvas.Image
	frame         *image.RGBA
	width, height int
	scratch       []byte

	// mu orders install against halt: the stream is created on the call's
	// goroutine and the halt can come from anywhere.
	mu      sync.Mutex
	stream  *video.Stream
	stopped bool
}

// install hands the view its decoder, unless the view was already stopped —
// in which case the caller keeps the stream's teardown.
func (v *shareView) install(s *video.Stream) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.stopped {
		return false
	}
	v.stream = s

	return true
}

// halt stops the decoder child, idempotently and from any goroutine. The
// child dying is also what unblocks the pump and, through the broken stdin,
// the voice reader writing to it.
func (v *shareView) halt() {
	v.mu.Lock()
	s := v.stream
	v.stopped = true
	v.mu.Unlock()

	if s != nil {
		s.Stop()
	}
}

/* Opening a watch */

// OnWatchShare opens the window watching somebody's stream — the tap on the
// live mark their row wears. The mark is drawn from the gateway's voice
// state, so it stands whether or not this client is in the call; only a call
// this client is in holds the media session a watch needs, which is the
// first thing answered here.
func (a *App) OnWatchShare(channelID, userID string) {
	call := a.call
	if call == nil || a.callChannelID != channelID {
		a.notify(ui.ToneWarning, "Join the call to watch the stream.")
		return
	}
	if userID == a.store.SelfID() {
		return // nothing here plays this machine's own screen back to it
	}
	if !a.videoInline {
		a.notifyTitled(ui.ToneWarning, "No video decoder",
			"Watching a stream needs ffmpeg, which was not found on this machine.")
		return
	}

	if v := a.share; v != nil {
		if v.userID == userID {
			v.win.RequestFocus()
			return
		}
		a.closeShare() // one stream at a time: one decoder child, one window
	}

	v := &shareView{userID: userID, channelID: channelID}
	v.buildWindow(a)
	a.share = v

	epoch := a.epoch
	a.background(func() error {
		// WatchShare answers at once; the track arriving is what runs
		// openShareDecoder, on the call's own goroutine. Still a worker:
		// subscribing writes to the signalling socket.
		return call.WatchShare(userID, func(codec voice.ShareCodec, width, height int) (io.WriteCloser, error) {
			return a.openShareDecoder(v, epoch, codec, width, height)
		})
	}, func(err error) {
		if a.share == v {
			a.closeShare()
		}
		if errors.Is(err, voice.ErrNoShare) {
			// The mark is the gateway's word and the publication the media
			// session's; between a share starting and its track being
			// announced, the two disagree.
			a.notifyTitled(ui.ToneWarning, "Not watched",
				"The stream is not available yet. It may still be starting — try again in a moment.")
			return
		}
		a.notifyTitled(ui.ToneWarning, "Not watched", "%v", err)
	})
}

// buildWindow opens the window the stream will land in, saying what it is
// doing until the first frame can. It exists before the subscription rather
// than after the first frame because closing it is the way out of the watch
// — a watch that never delivers must leave something to close.
func (v *shareView) buildWindow(a *App) {
	title := "Screenshare"
	if name := a.voiceParticipantOf(v.channelID, v.userID).Name; name != "" {
		title = name + " — screenshare"
	}

	// Black rather than a theme surface: it is the letterbox, and the decoder
	// pads its frames with exactly this.
	v.backdrop = canvas.NewRectangle(color.Black)

	note := canvas.NewText("Connecting to the stream...", theme.Colors.TextPrimary)
	note.TextSize = theme.Sizes.MemberStatusTextSize

	v.win = a.fyne.NewWindow(title)
	v.win.SetContent(container.NewStack(v.backdrop, container.NewCenter(note)))
	v.win.Resize(fyne.NewSize(shareWindowWidth, shareWindowWidth*9/16))
	v.win.SetOnClosed(func() { a.onShareWindowClosed(v) })
	v.win.Show()
}

// onShareWindowClosed is the reader closing the window, which is the whole
// of how a watch is given up on purpose — and the echo of closeShare's own
// Close, told apart by the field already being cleared.
func (a *App) onShareWindowClosed(v *shareView) {
	if a.share != v {
		return
	}
	a.share = nil

	if a.call != nil {
		a.call.UnwatchShare(v.userID)
	}
	go v.halt()
}

// closeShare tears the running watch down: the subscription, the decoder and
// the window. Safe with nothing watched, which is what lets dropCall call it
// unconditionally. UI thread.
func (a *App) closeShare() {
	v := a.share
	if v == nil {
		return
	}
	a.share = nil

	if a.call != nil {
		a.call.UnwatchShare(v.userID)
	}
	go v.halt()
	v.win.Close()
}

// onShareEnded is the voice session reporting a watch ending on the far
// side's terms: the sender stopped, left, or the stream broke. A stop is
// silent — the window going is the whole of the news — where a failure says
// why.
func (a *App) onShareEnded(e voice.ShareEnded) {
	v := a.share
	if v == nil || v.userID != e.UserID {
		return
	}

	a.closeShare()
	if e.Err != nil {
		a.notifyTitled(ui.ToneWarning, "Stream ended", "%v", e.Err)
	}
}

/* The decoder and the pump */

// openShareDecoder is the watch's ShareOpen: the track has arrived, so the
// sandboxed child is launched at the size the stream earns and the pump
// started. It runs on the call's goroutine — the installCall arrangement,
// with install answering whether anybody still wants what was started.
func (a *App) openShareDecoder(v *shareView, epoch uint64, codec voice.ShareCodec,
	srcWidth, srcHeight int) (io.WriteCloser, error) {

	width, height := shareDecodeSize(srcWidth, srcHeight)

	stream, in, err := a.videoTools.LiveFrames(video.LiveConfig{
		Format: string(codec), Width: width, Height: height,
	})
	if err != nil {
		return nil, err
	}

	if !v.install(stream) {
		_ = in.Close()
		stream.Stop()

		return nil, errors.New("the watch was closed")
	}

	v.width, v.height = width, height
	v.scratch = make([]byte, width*height*4)

	a.doOnUI(func() {
		if a.stale(epoch) || a.share != v {
			return
		}
		v.mountFeed()
	}, false)

	go a.pumpShareFrames(v, epoch)

	return in, nil
}

// mountFeed swaps the window's waiting note for the live picture, sized to
// the stream's aspect. UI thread.
func (v *shareView) mountFeed() {
	v.frame = image.NewRGBA(image.Rect(0, 0, v.width, v.height))
	v.view = canvas.NewImageFromImage(v.frame)
	v.view.FillMode = canvas.ImageFillContain
	v.view.ScaleMode = canvas.ImageScaleSmooth

	v.win.SetContent(container.NewStack(v.backdrop, v.view))
	if v.width > 0 {
		v.win.Resize(fyne.NewSize(shareWindowWidth,
			shareWindowWidth*float32(v.height)/float32(v.width)))
	}
}

// pumpShareFrames reads frames and paints them as they come — no wall
// clock: the sender paces the stream, and always draining is what keeps the
// pipe from turning into latency. The hop waits because scratch is reused
// the moment it returns. The stream's own window has its own canvas, so a
// paint here dirties nothing of the main window.
func (a *App) pumpShareFrames(v *shareView, epoch uint64) {
	for {
		if err := v.stream.ReadFrame(v.scratch); err != nil {
			a.settleShareEnd(v, epoch)
			return
		}

		a.doOnUI(func() {
			if a.stale(epoch) || a.share != v || v.frame == nil {
				return
			}

			copy(v.frame.Pix, v.scratch)
			v.view.Refresh()
		}, true)
	}
}

// settleShareEnd is the frame pipe ending. An owner tearing the watch down
// has already put things right — the stop flag says so — and the voice
// session's own teardown paths report through ShareEnded; what is left is
// the child dying on its own, which closes the window like any other end.
func (a *App) settleShareEnd(v *shareView, epoch uint64) {
	v.mu.Lock()
	stopped := v.stopped
	v.mu.Unlock()
	if stopped {
		return
	}

	a.doOnUI(func() {
		if a.stale(epoch) || a.share != v {
			return
		}
		a.closeShare()
	}, false)
}

// shareDecodeSize is the box the stream is decoded into: what the sender
// declared, fitted under the cap — the numbers are theirs, so nothing past
// it is believed — and a sensible box where they declared nothing. The
// child letterboxes into whatever this answers, so a lie costs pixels,
// never a misread pipe.
func shareDecodeSize(width, height int) (int, int) {
	if width < 1 || height < 1 {
		return 1280, 720
	}

	scale := 1.0
	if s := float64(shareDecodeCapWidth) / float64(width); s < scale {
		scale = s
	}
	if s := float64(shareDecodeCapHeight) / float64(height); s < scale {
		scale = s
	}

	return max(int(float64(width)*scale), 1), max(int(float64(height)*scale), 1)
}
