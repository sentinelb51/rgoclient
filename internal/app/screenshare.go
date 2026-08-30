package app

// Both halves of a screenshare (docs/screenshare-todo.md).
//
// Receiving: the watch a
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
//
// Sending is the same pipeline pointed the other way and is shorter, because
// nothing here reads it: the capture child's stdout *is* the published
// track's source, so lksdk drains the pipe and the frame duration handed to
// StartShare is the clock — Annex-B carries no timing of its own, and IVF's
// is overridden. What this half owns is the picker, the box the
// encoder is started at — which the instance's publish limits bound, a
// declared size over them being a disconnection rather than a refusal — and
// every way a share stops.

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"math"
	"runtime"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"RGOClient/internal/config"
	"RGOClient/internal/domain"
	"RGOClient/internal/ui"
	"RGOClient/internal/ui/theme"
	"RGOClient/internal/util"
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

	// self marks the one watch nothing was subscribed for: this machine's
	// own stream, teed off the encoder rather than delivered by the room.
	self bool

	// codecTag names what the feed's bytes are, worn by the window title once
	// the feed mounts — the codec for a watch, plus the encoder's own name
	// for the self preview.
	codecTag string

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
	if !a.videoInline {
		a.notifyTitled(ui.ToneWarning, "ffmpeg not found",
			"Watching a stream needs ffmpeg, which is not installed. %s", ffmpegAdvice())
		return
	}

	// This account's own mark is drawn like anybody else's, and the room
	// will never deliver back what this client publishes — so the tap lands
	// on the tee instead. The mark can stand a moment before the encoder
	// does, the gateway carrying the flag from the server.
	self := userID == a.store.SelfID()
	if self && a.sending == nil {
		a.notify(ui.ToneWarning, "You are not sharing your screen.")
		return
	}

	if v := a.share; v != nil {
		if v.userID == userID {
			v.win.RequestFocus()
			return
		}
		a.closeShare() // one stream at a time: one decoder child, one window
	}

	v := &shareView{userID: userID, channelID: channelID, self: self}
	v.buildWindow(a)
	a.share = v

	if self {
		a.startSelfPreview(v)
		return
	}

	epoch := a.epoch
	a.background(func() error {
		// WatchShare answers at once; the track arriving is what runs
		// openShareDecoder, on the call's own goroutine. Still a worker:
		// subscribing writes to the signalling socket.
		return call.WatchShare(userID, func(codec voice.ShareCodec, name string, width, height int) (io.WriteCloser, error) {
			return a.openShareDecoder(v, epoch, codec, name, width, height)
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
	switch name := a.voiceParticipantOf(v.channelID, v.userID).Name; {
	case v.self:
		title = "Your screen"
	case name != "":
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

	a.releaseShareSource(v)
	go v.halt()
}

// releaseShareSource lets go of whatever was feeding a view: a subscription
// for somebody else's stream, the encoder's tee for this machine's own.
func (a *App) releaseShareSource(v *shareView) {
	if v.self {
		if a.sending != nil {
			a.sending.tee.Detach()
		}

		return
	}

	if a.call != nil {
		a.call.UnwatchShare(v.userID)
	}
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

	a.releaseShareSource(v)
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
	name string, srcWidth, srcHeight int) (io.WriteCloser, error) {

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
	v.codecTag = name
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

// startSelfPreview is openShareDecoder's other caller: the same window, the
// same child and the same pump, fed by the encoder's tee rather than by a
// track. Nothing is subscribed and nothing is remuxed — the bytes are
// already the stream a decoder eats, H.264 Annex-B or AV1 in IVF — so the
// whole of it is the launch, which blocks and therefore belongs on a worker.
func (a *App) startSelfPreview(v *shareView) {
	sending := a.sending
	width, height := shareDecodeSize(sending.width, sending.height)
	epoch := a.epoch

	format, tag := voice.ShareH264, "H.264"
	if sending.av1 {
		format, tag = voice.ShareIVF, "AV1"
	}
	v.codecTag = tag + " · " + sending.encoder

	a.background(func() error {
		stream, in, err := a.videoTools.LiveFrames(video.LiveConfig{
			Format: string(format), Width: width, Height: height,
		})
		if err != nil {
			return err
		}

		if !v.install(stream) {
			_ = in.Close()
			stream.Stop()

			return errors.New("the preview was closed")
		}

		v.width, v.height = width, height
		v.scratch = make([]byte, width*height*4)

		a.doOnUI(func() {
			if a.stale(epoch) || a.share != v {
				return
			}
			v.mountFeed()
		}, false)

		// Last, so a preview that is about to be torn down anyway is never
		// the reason a frame was copied. Attaching to a tee whose share has
		// since stopped costs one failed write.
		sending.tee.Attach(in)
		go a.pumpShareFrames(v, epoch)

		return nil
	}, func(err error) {
		if a.share == v {
			a.closeShare()
		}
		a.notifyTitled(ui.ToneWarning, "No preview", "%v", err)
	})
}

// mountFeed swaps the window's waiting note for the live picture, sized to
// the stream's aspect. UI thread.
func (v *shareView) mountFeed() {
	if v.codecTag != "" {
		v.win.SetTitle(v.win.Title() + "  [" + v.codecTag + "]")
	}

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

/* Sending: what this account puts on screen elsewhere */

// sendingShare is this end's own running share: the capture child and what it
// was started with. The child's stdout *is* the published track's source, so
// there is no pump here — lksdk reads the pipe on its own goroutine and paces
// the track by the frame duration StartShare was handed.
type sendingShare struct {
	stream *video.Stream
	choice ui.ShareChoice

	// tee is what the track actually reads: the child's stdout, with a copy
	// of each frame available to a local preview. av1 says what those bytes
	// are — which demuxer that preview forces — and encoder which encoder
	// writes them, for that preview's title. width and height are the box
	// the child was started at, which is what that preview decodes into.
	tee           *video.ShareTee
	av1           bool
	encoder       string
	width, height int

	// stopped orders the two ends of a teardown: the controller killing the
	// child, and the write loop noticing the pipe die. Whoever is second does
	// nothing.
	mu      sync.Mutex
	stopped bool
}

// halt kills the capture child, idempotently and from any goroutine. The
// child dying is what ends the reader track, which unpublishes. Through the
// tee rather than the stream, so a preview watching this end is let go with
// it and one still being launched never attaches at all.
func (s *sendingShare) halt() bool {
	s.mu.Lock()
	first := !s.stopped
	s.stopped = true
	s.mu.Unlock()

	if first {
		_ = s.tee.Close()
	}

	return first
}

// OnShare is the island's share button: it stops a running stream, and opens
// the picker otherwise. UI thread.
func (a *App) OnShare() {
	if a.sending != nil {
		a.stopSharing()
		return
	}

	a.startSharing()
}

// startSharing opens the picker. Enumerating sources talks to the display
// server — a round trip per window on X11 — so it happens on a worker and the
// card is raised with the answer.
func (a *App) startSharing() {
	if a.shareStarting {
		return
	}

	call := a.call
	if call == nil {
		a.notify(ui.ToneWarning, "Join a call before sharing your screen.")
		return
	}
	if !a.videoInline {
		a.notifyTitled(ui.ToneWarning, "ffmpeg not found",
			"Sharing your screen needs ffmpeg, which is not installed. %s", ffmpegAdvice())
		return
	}
	if !call.CanShare() {
		a.notifyTitled(ui.ToneWarning, "Not allowed",
			"You do not have permission to share video in this channel.")
		return
	}

	a.shareStarting = true

	var (
		sources  []video.CaptureSource
		fallback bool
	)
	tools := a.videoTools

	a.backgroundThen(func() error {
		// The claim is released from here rather than from the two callbacks
		// below: a success landing in a stale epoch runs neither (App.run), and
		// the flag outlives the session it was set in — stuck true, OnShare is
		// dead for the rest of the run.
		defer a.doOnUI(func() { a.shareStarting = false }, false)

		found, err := video.ShareSources()
		if err != nil {
			return err
		}
		sources = found

		// Asked here rather than at the start of a share: it is what the
		// picker warns with, and the probes behind it are the ones the share
		// would otherwise have paid for anyway. The encoder probe rides the
		// same worker for the same reason — answered before anything is
		// picked, so starting a share never waits on it.
		fallback = tools.CaptureFallback(found)
		tools.ShareEncoder(captureCodec(config.Current().Screenshare.Codec))

		return nil
	}, func(err error) {
		a.notifyTitled(ui.ToneWarning, "Cannot share", "%v", err)
	}, func() {
		if a.call == nil || a.sending != nil {
			return
		}

		a.showSharePicker(sources, fallback)
	})
}

// showSharePicker raises the card, seeded with what was picked last time. UI
// thread.
func (a *App) showSharePicker(sources []video.CaptureSource, fallback bool) {
	state := config.Current().State

	dialog := ui.NewShareDialog(ui.ShareDialogConfig{
		Sources: toShareSources(sources),
		Initial: ui.ShareChoice{
			Source: state.ShareSource,
			Height: state.ShareHeight,
			FPS:    state.ShareFPS,
		},
		Note: shareCaptureNote(fallback),
	}, func(choice ui.ShareChoice) {
		a.beginShare(sources, choice)
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.shareDialog = dialog
}

// beginShare starts the capture child and publishes what it writes. Both
// block — the encoder's first frame, then a renegotiation — so both are on a
// worker, with one hop back to install; a share that landed into a call the
// reader has since left is killed rather than left publishing.
func (a *App) beginShare(sources []video.CaptureSource, choice ui.ShareChoice) {
	call := a.call
	if call == nil {
		a.failShare("The call ended.")
		return
	}

	source, ok := findShareSource(sources, choice.Source)
	if !ok {
		a.failShare("That screen is no longer there.")
		return
	}

	width, height := a.shareEncodeSize(source, choice.Height)
	tools := a.videoTools
	epoch, gen := a.epoch, a.callGen

	// One attempt at one codec preference: the capture child, the tee around
	// its stdout and the publish. refused marks a share the room turned away
	// at the publish — the one failure worth a second attempt at the fallback
	// codec, every other being an answer that does not change with it.
	attempt := func(codec video.CaptureCodec) (sending *sendingShare, refused bool, err error) {
		enc, ok := tools.ShareEncoder(codec)
		if !ok {
			// The other half of what used to be one "No encoder": ffmpeg is
			// here and none of the encoders this client probes for answered,
			// which is a different thing to fix from not having ffmpeg at all.
			return nil, false, errors.New("no encoder this client can use: the ffmpeg on this machine carries neither a hardware encoder nor libx264")
		}

		settings := config.Current().Screenshare
		stream, err := tools.CaptureShare(video.CaptureConfig{
			Source:          source,
			Width:           width,
			Height:          height,
			FPS:             choice.FPS,
			Bitrate:         shareBitrate(width, height, choice.FPS, enc.AV1, settings),
			KeyframeSeconds: shareKeyframeSeconds(settings.Keyframes),
			Codec:           codec,
			Speed:           captureSpeed(settings.EncoderSpeed),
			Latency:         captureLatency(settings.Latency),
		})
		if err != nil {
			return nil, false, err
		}

		sending = &sendingShare{
			stream: stream, choice: choice,
			tee: video.NewShareTee(stream, enc.AV1), av1: enc.AV1, encoder: enc.Name,
			width: width, height: height,
		}

		// The declared size is what every viewer draws a window from and what
		// the server measures its limits against, so it is the box the child
		// was actually started at rather than the source's own. The rate is
		// passed for the same reason it is asked for at all — see StartShare.
		sendCodec := voice.SendShareH264
		if enc.AV1 {
			sendCodec = voice.SendShareAV1
		}
		if err := call.StartShare(sending.tee, sendCodec, width, height, choice.FPS); err != nil {
			sending.halt()

			// Only the room's own refusal is worth another encoder: the rest
			// (no call, no permission, a stream that died being published)
			// answer the same way at either codec.
			return nil, errors.Is(err, voice.ErrShareRefused), err
		}

		return sending, false, nil
	}

	a.background(func() error {
		codec := captureCodec(config.Current().Screenshare.Codec)

		sending, refused, err := attempt(codec)
		if refused && codec == video.CaptureCodecAuto {
			// The GPU offering AV1 does not make the room take it — an
			// instance whose LiveKit has the codec off answers the publish
			// with a refusal — so one refusal falls back to H.264 before
			// anything is reported.
			if enc, ok := tools.ShareEncoder(codec); ok && enc.AV1 {
				log.Printf("app: AV1 share refused (%v); retrying as H.264", err)
				sending, _, err = attempt(video.CaptureCodecH264)
			}
		}
		if err != nil {
			return err
		}

		a.doOnUI(func() { a.installShare(sending, epoch, gen, call) }, false)

		return nil
	}, func(err error) {
		a.failShare(fmt.Sprintf("%v", err))
	})
}

// installShare is the start's last step, back on the UI thread — the
// installCall arrangement: a share that connected into a session or a call
// that has since gone is stopped rather than installed.
func (a *App) installShare(sending *sendingShare, epoch, gen uint64, call *voice.Call) {
	if a.stale(epoch) || a.call != call || a.callGen != gen {
		// The publication first, so the media session records this as a
		// deliberate stop; killing the child first would have its write loop
		// report an end nobody needs to hear about.
		call.StopShare()
		sending.halt()

		return
	}

	a.sending = sending
	a.shareDialog = nil
	a.closeOverlay()

	config.RememberShare(sending.choice.Source, sending.choice.Height, sending.choice.FPS)
	a.syncCallIsland()
}

// failShare reports a refusal into the picker where it is still up, and as a
// notice where the reader has dismissed it. UI thread.
func (a *App) failShare(message string) {
	if a.shareDialog != nil {
		a.shareDialog.Fail(message)
		return
	}

	a.notifyTitled(ui.ToneWarning, "Not shared", "%s", message)
}

// stopSharing takes this end's stream down on purpose: the publication first,
// so the room stops drawing it now rather than at the encoder's next frame,
// then the child. Safe with nothing running, which is what lets dropCall call
// it unconditionally. UI thread.
func (a *App) stopSharing() {
	sending := a.sending
	if sending == nil {
		return
	}
	a.closeSelfPreview()
	a.sending = nil

	if a.call != nil {
		a.call.StopShare()
	}
	go sending.halt()

	a.syncCallIsland()
}

// closeSelfPreview shuts the window watching this machine's own stream,
// there being nothing left to draw in it. Called before a.sending is
// cleared, which is what lets the detach find its tee. UI thread.
func (a *App) closeSelfPreview() {
	if v := a.share; v != nil && v.self {
		a.closeShare()
	}
}

// onShareStopped is the media session reporting this end's own stream ending
// on its own terms — the captured window closed, the encoder died. The
// publication is already retired; what is left is the child and the button.
func (a *App) onShareStopped() {
	sending := a.sending
	if sending == nil {
		return
	}
	a.closeSelfPreview()
	a.sending = nil

	go sending.halt()
	a.syncCallIsland()
	a.notifyTitled(ui.ToneWarning, "Sharing ended", "Your screen is no longer being shared.")
}

/* What a share is encoded at */

const (
	// shareBitsPerPixel is what "auto" spends: roughly a tenth of a bit per
	// pixel per frame, which is 720p30 ≈ 2.7 Mbps and 1080p60 ≈ 12 — screen
	// content being mostly still, and the encoder spending nothing on what
	// does not move.
	shareBitsPerPixel = 0.1

	// shareAV1BitrateScale is AV1's discount on that budget: the same picture
	// costs roughly two thirds of the bits, and the gain is taken as
	// bandwidth rather than as extra quality — which is what the codec is
	// for here, a share's ceiling being somebody's uplink.
	shareAV1BitrateScale = 0.7
)

// shareCaptureNote is the one line the picker gets to say about what capture
// cannot do here. Only two things are worth saying now. On X11 an occluded
// window hands back whatever the server still holds for it, which is a limit
// of the grabber rather than of this client — see docs/known-gaps.md.
//
// fallback is Windows having had to reach past Graphics Capture *and*
// Desktop Duplication to GDI's BitBlt, which copies on the CPU and flickers
// the pointer for everybody at the machine. It is the one worth calling a
// warning: the whole capture path running slower than the machine can, and
// unlike the rest it can be fixed from outside the client.
func shareCaptureNote(fallback bool) string {
	if fallback {
		return "Screen capture fell back to GDI, which uses more CPU and flickers the " +
			"mouse pointer. The faster paths need Windows 10 version 1903 or newer and " +
			"a recent ffmpeg."
	}

	if runtime.GOOS == "linux" {
		return "A window hidden behind another may capture as garbage without a compositor."
	}

	return ""
}

// shareEncodeSize is the box a share is encoded into: the source fitted to
// the picked short edge, then under whatever the instance enforces.
//
// The limits are not advisory. voice-ingress measures the *declared* size on
// publish and, over either bound, removes the publisher from the voice
// channel — so a box that would be refused is shrunk here rather than costing
// the reader their place in the call.
func (a *App) shareEncodeSize(source video.CaptureSource, wanted int) (int, int) {
	width, height := source.Width, source.Height
	if width < 1 || height < 1 {
		width, height = 1280, 720
	}

	// The picked height is a *ceiling*: nothing is upscaled, a 720p monitor
	// shared "at 1080p" being 720p with more pixels to send.
	if wanted > 0 && height > wanted {
		width = width * wanted / height
		height = wanted
	}

	return fitShareBox(width, height, a.shareLimits())
}

// shareAspectMargin keeps the box a hair inside the aspect band rather than
// on it. The ingress compares in f32 where this computes in float64, and a
// ratio landing exactly on the bound is one rounding away from a
// disconnection.
const shareAspectMargin = 0.99

// fitShareBox is the order the two limits have to be applied in, and it is
// not the obvious one. Area first, then even, then aspect — because both of
// the first two *truncate*, and truncating the two edges independently moves
// the ratio: an ultrawide fitted exactly to 2.5 comes out of the area scale
// at 2.502 and is refused. Aspect last, and by shrinking the offending edge
// alone, so the area already fitted still holds — a smaller box is never a
// larger one.
func fitShareBox(width, height int, limits domain.VideoLimits) (int, int) {
	if limits.MaxArea > 0 && width*height > limits.MaxArea {
		// Both edges by the same factor, which is what keeps the picture the
		// shape it was.
		scale := math.Sqrt(float64(limits.MaxArea) / float64(width*height))
		width = int(float64(width) * scale)
		height = int(float64(height) * scale)
	}

	// H.264 here is 4:2:0, which halves both dimensions: an odd edge is a
	// filter error rather than a rounding.
	width, height = evenDown(width), evenDown(height)

	if limits.AspectMin <= 0 || limits.AspectMax <= limits.AspectMin {
		return width, height
	}

	high, low := limits.AspectMax*shareAspectMargin, limits.AspectMin/shareAspectMargin

	if float64(width)/float64(height) > high {
		width = evenDown(int(float64(height) * high))
	}
	// Asked of the width this may have just narrowed: shrinking one edge is
	// what moves the ratio towards the other bound.
	if float64(width)/float64(height) < low {
		height = evenDown(int(float64(width) / low))
	}

	return width, height
}

// shareFallbackLimits is what a share is fitted under before the instance has
// been asked — stoat.chat's own new-user tier, which is the *smaller* of the
// two it advertises. Guessing low is the whole point: the ingress does not
// refuse an oversized track, it disconnects the publisher from the call, so a
// share started in the second before the fetch lands must not be the thing
// that ends somebody's call. A stream a little smaller than it could be is
// the cost, and the picker's own answer is unaffected.
var shareFallbackLimits = domain.VideoLimits{
	Enabled:   true,
	MaxArea:   1080 * 720,
	AspectMin: 0.3, AspectMax: 2.5,
}

// shareLimits is the tier this account publishes under: the instance's own
// boundary is in hours since the account was made, which its ULID carries.
// Nothing fetched yet falls back rather than clamping to nothing — see
// shareFallbackLimits.
func (a *App) shareLimits() domain.VideoLimits {
	tiers := a.videoLimits
	if tiers.Default.MaxArea == 0 && tiers.NewUser.MaxArea == 0 {
		return shareFallbackLimits
	}

	if tiers.NewUserHours > 0 {
		if made, err := util.Timestamp(a.store.SelfID()); err == nil {
			if time.Since(made) < time.Duration(tiers.NewUserHours)*time.Hour {
				return tiers.NewUser
			}
		}
	}

	return tiers.Default
}

// evenDown rounds down to an even number, with a floor of two: yuv420p halves
// both dimensions, and an odd one is a filter error rather than a rounding.
func evenDown(v int) int {
	if v < 2 {
		return 2
	}

	return v &^ 1
}

// shareBitrate is what a share asks the encoder for: the automatic budget
// its size and frame rate earn, AV1's discount, then the Bandwidth setting's
// cut — bounded in video so a 60 fps full-screen share does not try to fill
// somebody's whole uplink.
//
// A custom bandwidth is none of that: a number somebody typed is the answer, and
// AV1's discount is not applied to it either — the codec is chosen after this and
// a ceiling that moved with it would not be the ceiling that was asked for.
func shareBitrate(width, height, fps int, av1 bool, settings config.Screenshare) int {
	if settings.Bandwidth == config.ShareBandwidthCustom {
		return settings.Bitrate * 1000
	}

	rate := float64(width) * float64(height) * float64(fps) * shareBitsPerPixel
	if av1 {
		rate *= shareAV1BitrateScale
	}

	switch settings.Bandwidth {
	case config.ShareBandwidthHalf:
		rate *= 0.5
	case config.ShareBandwidthQuarter:
		rate *= 0.25
	}

	return int(rate)
}

// shareKeyframeSeconds is the Keyframes setting as the interval the encoder
// is forced to, the mapping here for the reason captureSpeed's is: what
// "frequent" costs is policy, not a codec fact.
//
// Sparse is four seconds rather than eight because eight bought nothing:
// measured at 1080p30 under capped VBR, moderate motion costs 1.17 Mbps at
// either, the keyframe being a rounding error against what the P-frames
// already spend. What it did buy was the wait — a viewer sees nothing until
// the next keyframe, there being no way to answer their PLI — so the interval
// nobody pays for is the one that halves it.
func shareKeyframeSeconds(setting string) int {
	switch setting {
	case config.ShareKeyframesFrequent:
		return 1
	case config.ShareKeyframesSparse:
		return 4
	}

	return 2
}

// captureSpeed is the setting as the encoder's own level. The mapping is here
// rather than in video for the reason resolveCores is here: video knows what a
// level costs, and which of them somebody asked for is this side's.
func captureSpeed(setting string) video.CaptureSpeed {
	switch setting {
	case config.ShareSpeedBalanced:
		return video.CaptureBalanced
	case config.ShareSpeedFast:
		return video.CaptureFast
	}

	return video.CaptureQuality
}

// captureLatency is captureSpeed's twin for the other dial.
func captureLatency(setting string) video.CaptureLatency {
	if setting == config.ShareLatencyBuffered {
		return video.CaptureBuffered
	}

	return video.CaptureLowestLatency
}

// captureCodec is the third dial: the codec preference as video's own value.
func captureCodec(setting string) video.CaptureCodec {
	if setting == config.ShareCodecH264 {
		return video.CaptureCodecH264
	}

	return video.CaptureCodecAuto
}

// toShareSources converts what the video package enumerated into what a
// widget may see — the ui.AudioDevice seam, again.
func toShareSources(sources []video.CaptureSource) []ui.ShareSource {
	out := make([]ui.ShareSource, 0, len(sources))
	for _, source := range sources {
		kind := ui.ShareMonitor
		if source.Kind == video.CaptureWindow {
			kind = ui.ShareWindow
		}

		out = append(out, ui.ShareSource{
			ID: shareSourceKey(source), Kind: kind, Title: source.Title,
			Width: source.Width, Height: source.Height,
		})
	}

	return out
}

// shareSourceKey names one source across the two sides, and across two runs:
// it is what the last pick is remembered as. So it is deliberately *not*
// `CaptureSource.ID` — that is a live handle on Windows and an X11 window id
// on Linux, and neither means anything to the next enumeration, let alone the
// next launch. A window is keyed by its title and a monitor by where it is,
// both being what a reader would recognise it by. Two windows sharing a title
// seed the picker with the first, which is a seed rather than a commitment.
func shareSourceKey(source video.CaptureSource) string {
	if source.Kind == video.CaptureWindow {
		return "w:" + source.Title
	}

	return fmt.Sprintf("m:%d,%d,%dx%d", source.X, source.Y, source.Width, source.Height)
}

// findShareSource is the picked key back to the source it named. A miss is a
// window closed between the enumeration and the answer, which is a refusal
// rather than a guess at the nearest one.
func findShareSource(sources []video.CaptureSource, key string) (video.CaptureSource, bool) {
	for _, source := range sources {
		if shareSourceKey(source) == key {
			return source, true
		}
	}

	return video.CaptureSource{}, false
}
