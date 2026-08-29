package voice

// The receive half of a screenshare. A share is one more video track in the
// room the call already holds, and this file is what stands between that
// track and a decoder: the registry of who is publishing one, the
// subscription policy — nothing video is downloaded until somebody watches —
// and the watch itself, which reassembles RTP into whole frames and remuxes
// them into the byte stream an ffmpeg demuxer reads. The decoder is the
// caller's: WatchShare takes a factory answering with where the bytes go, so
// this package never learns what a decoder is. That is the PCMSource
// arrangement pointed the other way, and it is what keeps the surface
// liftable into rvoice with the rest.
//
// Who is *drawn* as sharing is not this file's question: the gateway carries
// the flag on the voice state and the sidebar draws from the store. This is
// the media side — whether the track is actually here to watch.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

/* The surface */

// ShareCodec names the byte stream a watched share is remuxed into — exactly
// the demuxer the decoder must be told to force, the codec having come from
// the session rather than from sniffing bytes.
type ShareCodec string

const (
	// ShareIVF is VP8 or VP9 in IVF: a 32-byte header, then twelve bytes per
	// frame. The timebase is 1/90000, so the RTP timestamp is the pts.
	ShareIVF ShareCodec = "ivf"

	// ShareH264 is H.264 as bare Annex-B, which carries no timestamps at all.
	ShareH264 ShareCodec = "h264"
)

// ShareOpen is the watcher's half of a watch: called once, from the call's
// own goroutine, when the share's track has actually arrived and its codec
// is known. width and height are what the sender declared, zero where they
// declared nothing. It answers with where the muxed bytes go — that writer
// closing is how the watcher's decoder learns the stream ended — or an
// error, which abandons the watch.
type ShareOpen func(codec ShareCodec, width, height int) (io.WriteCloser, error)

// ErrNoShare answers a watch of somebody who is not screensharing here: the
// mark that was tapped was drawn from the gateway's voice state, and the
// media session holds no publication behind it (yet).
var ErrNoShare = errors.New("no screenshare to watch")

// ShareLane is the sink lane a participant's share audio plays through,
// keyed apart from their voice so the two are separate dials. The NUL can be
// in no user ID, so the lane can never collide with a person.
func ShareLane(userID string) string { return userID + "\x00share" }

// ShareEnded reports a watch ending for any reason other than UnwatchShare:
// the sender stopped or left, the subscription failed, or the bytes stopped
// being writable. Err is nil where the sender simply stopped. Nothing is
// emitted about shares nobody here was watching — the sidebar's marks come
// off the gateway, not from here.
type ShareEnded struct {
	UserID string
	Err    error
}

func (ShareEnded) isVoiceEvent() {}

/* The registry */

// share is one participant's screenshare as the media session sees it: who
// publishes it (held for the keyframe demands only a participant can carry),
// the video publication, the audio published beside it where the sender
// shares sound, and the watch running against it — nil for a share nobody
// here is watching, which costs nothing at all.
type share struct {
	from  *lksdk.RemoteParticipant
	video *lksdk.RemoteTrackPublication
	audio *lksdk.RemoteTrackPublication

	watch *shareWatch
}

// shareWatch is one running watch: the factory the bytes will go to and the
// reader's controls. silent marks one ended by UnwatchShare, whose caller
// needs no event about what it just did; it is guarded by the call's mu, as
// started is.
type shareWatch struct {
	open ShareOpen

	done      chan struct{}
	closeOnce sync.Once

	started bool
	silent  bool
}

func (w *shareWatch) end() { w.closeOnce.Do(func() { close(w.done) }) }

// registerPublication runs for every remote publication, at the join sweep
// and as they appear. Video is unsubscribed whatever it is — a share must
// cost nothing until watched, and a camera has no surface here at all — and
// a share's two publications are filed so a watch can find them. Idempotent:
// the sweep and the publish callback filing the same publication write the
// same pointer twice.
func (c *Call) registerPublication(pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	video := pub.Kind() == lksdk.TrackKindVideo
	shareAudio := pub.Source() == livekit.TrackSource_SCREEN_SHARE_AUDIO
	if !video && !shareAudio {
		return
	}

	c.mu.Lock()
	if video && pub.Source() == livekit.TrackSource_SCREEN_SHARE {
		sh := c.shareLocked(rp.Identity())
		sh.from, sh.video = rp, pub
	}
	if shareAudio {
		sh := c.shareLocked(rp.Identity())
		sh.from, sh.audio = rp, pub
	}
	c.mu.Unlock()

	// The room is joined with autosubscribe — the working default for every
	// microphone — so everything above is switched off as it appears: a share
	// in the room must not bill every client its full bitrate to discard.
	// WatchShare is what turns exactly one back on.
	if err := pub.SetSubscribed(false); err != nil {
		log.Printf("voice: unsubscribe %s of %s: %v", pub.SID(), rp.Identity(), err)
	}
}

// shareLocked finds or files a participant's share entry. Callers hold mu.
func (c *Call) shareLocked(userID string) *share {
	sh := c.shares[userID]
	if sh == nil {
		sh = &share{}
		c.shares[userID] = sh
	}

	return sh
}

// unregisterPublication is a publication going away. A watch ends with its
// video track — the sender pressed stop — where the audio going alone only
// silences its lane.
func (c *Call) unregisterPublication(pub *lksdk.RemoteTrackPublication, userID string) {
	c.mu.Lock()
	sh := c.shares[userID]
	if sh == nil {
		c.mu.Unlock()
		return
	}

	var w *shareWatch
	switch pub {
	case sh.video:
		sh.video = nil
		w = sh.watch
	case sh.audio:
		sh.audio = nil
	}
	if sh.video == nil && sh.audio == nil && sh.watch == nil {
		delete(c.shares, userID)
	}
	c.mu.Unlock()

	if w != nil {
		c.endShareWatch(userID, w, nil)
	}
}

// forgetShare drops a departed participant's share, ending any watch on it.
func (c *Call) forgetShare(userID string) {
	c.mu.Lock()
	sh := c.shares[userID]
	var w *shareWatch
	if sh != nil {
		w = sh.watch
		sh.video, sh.audio = nil, nil
		if w == nil {
			delete(c.shares, userID)
		}
	}
	c.mu.Unlock()

	if w != nil {
		c.endShareWatch(userID, w, nil)
	}
}

// sweepPublications applies the subscription policy to what was already in
// the room at the join. lksdk fires OnTrackPublished for those too, but
// nothing in its contract promises it, and the cost of relying on that
// wrongly is exactly the bandwidth the policy exists to stop paying.
func (c *Call) sweepPublications() {
	for _, rp := range c.room.GetRemoteParticipants() {
		for _, pub := range rp.TrackPublications() {
			if remote, ok := pub.(*lksdk.RemoteTrackPublication); ok {
				c.registerPublication(remote, rp)
			}
		}
	}
}

/* Watching */

// WatchShare starts receiving one participant's screenshare. It subscribes
// the share's tracks and answers at once; open runs when the video track is
// actually delivered, and everything after flows into the writer it answers
// with. One watch per participant.
func (c *Call) WatchShare(userID string, open ShareOpen) error {
	if open == nil {
		return errors.New("no watcher")
	}

	c.mu.Lock()
	sh := c.shares[userID]
	if sh == nil || sh.video == nil {
		c.mu.Unlock()
		return ErrNoShare
	}
	if sh.watch != nil {
		c.mu.Unlock()
		return errors.New("already watching this share")
	}
	w := &shareWatch{open: open, done: make(chan struct{})}
	sh.watch = w
	video, audio := sh.video, sh.audio
	c.mu.Unlock()

	if err := video.SetSubscribed(true); err != nil {
		c.mu.Lock()
		if now := c.shares[userID]; now != nil && now.watch == w {
			now.watch = nil
		}
		c.mu.Unlock()
		w.end()

		return fmt.Errorf("subscribe the share: %w", err)
	}
	if audio != nil {
		if err := audio.SetSubscribed(true); err != nil {
			log.Printf("voice: share audio of %s: %v", userID, err)
		}
	}

	// The subscribe callback is what starts the reader — unless the track is
	// already here from a subscription that landed before anybody watched, in
	// which case no callback is coming.
	if track := video.TrackRemote(); track != nil {
		c.startShareReader(video, track, userID)
	}

	return nil
}

// UnwatchShare stops watching. The writer is closed behind it, which the
// watcher's decoder sees as the stream ending, and no event follows — the
// caller asked. Safe with no watch running.
func (c *Call) UnwatchShare(userID string) {
	c.mu.Lock()
	var w *shareWatch
	if sh := c.shares[userID]; sh != nil && sh.watch != nil {
		w = sh.watch
		w.silent = true
	}
	c.mu.Unlock()

	if w != nil {
		c.endShareWatch(userID, w, nil)
	}
}

// startShareReader spins up the one goroutine a watched share needs, if a
// watch is waiting for this track and none is running. The subscribe
// callback and WatchShare both call it — whichever finds the track first —
// so the claim is made under the lock.
func (c *Call) startShareReader(pub *lksdk.RemoteTrackPublication, track *webrtc.TrackRemote, userID string) {
	c.mu.Lock()
	sh := c.shares[userID]
	if sh == nil || sh.video != pub || sh.watch == nil || sh.watch.started {
		c.mu.Unlock()
		return
	}
	w, from := sh.watch, sh.from
	w.started = true
	c.mu.Unlock()

	go c.readShare(track, pub, from, w, userID)
}

// endShareWatch retires a watch: the reader is told to stop, the registry
// forgets it, the subscriptions are switched back off, and — unless the
// watcher itself asked — ShareEnded says why. Idempotent per watch, safe
// from any goroutine; only the caller that finds the watch still current
// reports and unsubscribes, so the reader's exit and an unpublish racing
// each other say it once.
func (c *Call) endShareWatch(userID string, w *shareWatch, err error) {
	c.mu.Lock()
	sh := c.shares[userID]
	current := sh != nil && sh.watch == w
	var video, audio *lksdk.RemoteTrackPublication
	if current {
		sh.watch = nil
		video, audio = sh.video, sh.audio
		if video == nil && audio == nil {
			delete(c.shares, userID)
		}
	}
	silent := w.silent
	c.mu.Unlock()

	w.end()

	if !current {
		return
	}

	// Watched was the only reason these were subscribed.
	if video != nil {
		_ = video.SetSubscribed(false)
	}
	if audio != nil {
		_ = audio.SetSubscribed(false)
	}

	if !silent {
		c.emit(ShareEnded{UserID: userID, Err: err})
	}
}

const (
	// videoClockRate is RTP's one video clock; it is also the IVF timebase the
	// muxer declares, which is what makes the RTP timestamp the pts verbatim.
	videoClockRate = 90000

	// shareMaxLate is how many packets the reassembler may hold. It has to
	// span the largest frame a share sends — a keyframe at screenshare
	// bitrates runs to hundreds of packets — plus the reordering the network
	// adds under it.
	shareMaxLate = 1024

	// sharePLIInterval rate-limits the keyframe demands loss triggers: a
	// burst of drops is one demand, not one per broken frame.
	sharePLIInterval = 500 * time.Millisecond
)

// readShare is a watch's whole life: RTP off the track, reassembled into
// frames, remuxed and written until something ends it. One goroutine per
// watch, owning the reassembler, the muxer and the writer.
func (c *Call) readShare(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication,
	from *lksdk.RemoteParticipant, w *shareWatch, userID string) {

	width, height := shareDimensions(pub)

	codec, mux, depacketizer, ok := shareCodec(track, width, height)
	if !ok {
		c.endShareWatch(userID, w,
			fmt.Errorf("the share is %s, which this client cannot take yet", track.Codec().MimeType))
		return
	}

	out, err := w.open(codec, width, height)
	if err != nil {
		c.endShareWatch(userID, w, err)
		return
	}

	// Whatever ends the loop, the writer is closed — stdin's EOF is how the
	// decoder learns the stream is over.
	var endErr error
	defer func() {
		_ = out.Close()
		c.endShareWatch(userID, w, endErr)
	}()

	// A watch starts at a keyframe: the PLI demands one now, and nothing
	// before it arrives is decodable anyway — the muxer holds the stream
	// until one starts it.
	from.WritePLI(track.SSRC())
	lastPLI := time.Now()

	builder := samplebuilder.New(shareMaxLate, depacketizer, videoClockRate)

	for {
		select {
		case <-w.done:
			return
		case <-c.done:
			return
		default:
		}

		// Re-armed every pass, the way readTrack's is: a lapsed deadline is
		// how the loop looks at done, not a failure.
		_ = track.SetReadDeadline(time.Now().Add(readTimeout))

		packet, _, err := track.ReadRTP()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			if !errors.Is(err, io.EOF) {
				endErr = fmt.Errorf("the stream stopped: %w", err)
			}

			return
		}

		builder.Push(packet)

		for sample := builder.Pop(); sample != nil; sample = builder.Pop() {
			// A frame truncated by loss was dropped by the reassembler, and
			// what follows references it — so a keyframe is demanded,
			// throttled: a burst of holes is one demand.
			if sample.PrevDroppedPackets > 0 && time.Since(lastPLI) > sharePLIInterval {
				from.WritePLI(track.SSRC())
				lastPLI = time.Now()
			}

			if len(sample.Data) == 0 {
				continue
			}

			if err := mux.write(out, sample); err != nil {
				select {
				case <-w.done:
					// The watcher closed its decoder; not the stream failing.
				default:
					endErr = fmt.Errorf("the decoder stopped taking the stream: %w", err)
				}

				return
			}
		}
	}
}

// shareDimensions is what the sender declared for the track, zero where they
// declared nothing. Sender-controlled: a caller sizes a *box* from it, never
// a buffer.
func shareDimensions(pub *lksdk.RemoteTrackPublication) (int, int) {
	info := pub.TrackInfo()
	if info == nil {
		return 0, 0
	}

	return int(info.Width), int(info.Height)
}

// shareCodec maps the negotiated mime onto the container the decoder will be
// told and the depacketizer that reassembles it. Not ok is a codec this
// client cannot remux — AV1, until somebody builds its OBU handling.
func shareCodec(track *webrtc.TrackRemote, width, height int) (ShareCodec, shareMux, rtp.Depacketizer, bool) {
	mime := track.Codec().MimeType
	switch {
	case strings.EqualFold(mime, webrtc.MimeTypeVP8):
		return ShareIVF, newIVFMux("VP80", width, height), &codecs.VP8Packet{}, true
	case strings.EqualFold(mime, webrtc.MimeTypeVP9):
		return ShareIVF, newIVFMux("VP90", width, height), &codecs.VP9Packet{}, true
	case strings.EqualFold(mime, webrtc.MimeTypeH264):
		return ShareH264, &annexBMux{}, &codecs.H264Packet{}, true
	}

	return "", nil, nil, false
}

/* Muxing */

// shareMux writes reassembled frames as the byte stream the decoder's
// demuxer was told to expect. One goroutine's property; nothing here locks.
type shareMux interface {
	write(out io.Writer, sample *media.Sample) error
}

// annexBMux is H.264's: the depacketizer already emits Annex-B, so a frame
// is its bytes and nothing else. The stream is held until an IDR starts it —
// a decoder cannot enter a stream mid-GOP.
type annexBMux struct {
	started bool
}

func (m *annexBMux) write(out io.Writer, sample *media.Sample) error {
	if !m.started {
		if !h264KeyframeStarts(sample.Data) {
			return nil
		}
		m.started = true
	}

	_, err := out.Write(sample.Data)

	return err
}

// ivfMux frames VP8/VP9 in IVF. The header's dimensions are advisory — the
// decoder reads the real ones off the keyframes — and the pts is the RTP
// timestamp unwrapped, the declared timebase making them the same unit.
type ivfMux struct {
	fourCC        string
	width, height uint16

	started bool
	last    uint32 // the previous frame's RTP timestamp, for the delta
	pts     uint64
	header  [12]byte
}

func newIVFMux(fourCC string, width, height int) *ivfMux {
	return &ivfMux{fourCC: fourCC, width: clampUint16(width), height: clampUint16(height)}
}

func (m *ivfMux) write(out io.Writer, sample *media.Sample) error {
	if !m.started {
		// VP8 says whether a frame is a keyframe in its first bit; VP9's
		// header is not worth parsing here, and a decoder skips what it
		// cannot enter at the cost of a logged complaint.
		if m.fourCC == "VP80" && !vp8KeyframeStarts(sample.Data) {
			return nil
		}
		if err := m.writeHeader(out); err != nil {
			return err
		}
		m.started = true
		m.last = sample.PacketTimestamp
	}

	// The samplebuilder emits in order, so the uint32 subtraction carries a
	// wrap correctly; a jump past any real frame gap is a lie held out of the
	// clock rather than believed.
	delta := sample.PacketTimestamp - m.last
	if delta > videoClockRate*60 {
		delta = 0
	}
	m.pts += uint64(delta)
	m.last = sample.PacketTimestamp

	binary.LittleEndian.PutUint32(m.header[0:], uint32(len(sample.Data)))
	binary.LittleEndian.PutUint64(m.header[4:], m.pts)
	if _, err := out.Write(m.header[:]); err != nil {
		return err
	}
	_, err := out.Write(sample.Data)

	return err
}

func (m *ivfMux) writeHeader(out io.Writer) error {
	var h [32]byte
	copy(h[0:], "DKIF")
	binary.LittleEndian.PutUint16(h[6:], 32) // header length; version stays 0
	copy(h[8:], m.fourCC)
	binary.LittleEndian.PutUint16(h[12:], m.width)
	binary.LittleEndian.PutUint16(h[14:], m.height)
	binary.LittleEndian.PutUint32(h[16:], videoClockRate) // timebase denominator
	binary.LittleEndian.PutUint32(h[20:], 1)              // timebase numerator
	// Bytes 24-27 are the frame count, unknowable live; zero is what muxers
	// write for a stream.
	_, err := out.Write(h[:])

	return err
}

// vp8KeyframeStarts reads the one bit VP8 puts first: the frame tag's P bit,
// zero for a keyframe.
func vp8KeyframeStarts(frame []byte) bool {
	return len(frame) > 0 && frame[0]&0x01 == 0
}

// h264KeyframeStarts scans the Annex-B units for an IDR slice or an SPS. An
// SPS counts because encoders that send parameter sets in a sample of their
// own put them just ahead of the IDR — a stream entered at the IDR alone
// would be one the decoder has no parameters for.
func h264KeyframeStarts(frame []byte) bool {
	for i := 0; i+3 < len(frame); i++ {
		if frame[i] != 0 || frame[i+1] != 0 {
			continue
		}
		j := i + 2
		if frame[j] == 0 {
			j++
		}
		if j+1 >= len(frame) || frame[j] != 1 {
			continue
		}
		if kind := frame[j+1] & 0x1F; kind == 5 || kind == 7 {
			return true
		}
		i = j
	}

	return false
}

func clampUint16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 65535 {
		return 65535
	}

	return uint16(v)
}

/* Sending */

// ShareStopped reports this end's own share ending on its own terms rather
// than the caller's: the byte stream feeding the track hit EOF or broke — the
// captured window closed, the encoder died. Nothing follows StopShare or the
// call ending; the caller caused those.
type ShareStopped struct{}

func (ShareStopped) isVoiceEvent() {}

// outboundShare is this end's own stream: the source the reader track eats
// and the publication the room knows it as. stopped marks one being taken
// down on purpose; settled marks the write loop having already ended, for the
// start that races it. All of it is guarded by the call's mu.
type outboundShare struct {
	src   io.ReadCloser
	track *lksdk.LocalTrack
	pub   *lksdk.LocalTrackPublication

	stopped bool
	settled bool
}

// CanShare reports whether the join token grants publishing a screenshare.
// Stoat mints the grant off the channel's Video permission and the instance's
// video flag, so this is the server's own answer read locally — what greys
// the button before a refused AddTrack has to say it.
func (c *Call) CanShare() bool { return c.canShare }

// StartShare publishes a screenshare: VP8 in IVF read from src — an encoder's
// stdout — declared to the room at width×height, which every viewer sizes a
// window by and the server enforces its limits against. It blocks for the
// publish negotiation, so it belongs on a worker. One share at a time; the
// source is closed by whatever ends it.
//
// fps is what the track is *paced* by, and it is asked for rather than read
// off the stream on purpose. lksdk derives a sample's duration from the IVF
// timebase and the timestamp delta, and ffmpeg does not write those two in
// agreement for every grabber: x11grab declares 1/fps and then steps the
// timestamps by fps, which comes out as a second per frame and publishes the
// share at a fifteenth of its speed. The capture child is held to a constant
// rate by its own fps filter, so the duration is a number this side already
// knows — ReaderTrackWithFrameDuration overrides that arithmetic with it.
func (c *Call) StartShare(src io.ReadCloser, width, height, fps int) error {
	if src == nil {
		return errors.New("no stream to publish")
	}
	if fps < 1 {
		return errors.New("no frame rate to pace the share by")
	}
	if !c.canShare {
		return errors.New("the server does not allow publishing a screenshare here")
	}
	if c.room == nil {
		return errors.New("no call to share into")
	}

	out := &outboundShare{src: src}

	c.mu.Lock()
	if c.outShare != nil {
		c.mu.Unlock()
		return errors.New("already sharing")
	}
	c.outShare = out
	c.mu.Unlock()

	release := func() {
		c.mu.Lock()
		if c.outShare == out {
			c.outShare = nil
		}
		c.mu.Unlock()
	}

	track, err := lksdk.NewLocalReaderTrack(src, webrtc.MimeTypeVP8,
		lksdk.ReaderTrackWithFrameDuration(time.Second/time.Duration(fps)),
		lksdk.ReaderTrackWithOnWriteComplete(func() { c.settleShareSend(out) }))
	if err != nil {
		release()
		return fmt.Errorf("share track: %w", err)
	}

	pub, err := c.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:        "screenshare",
		Source:      livekit.TrackSource_SCREEN_SHARE,
		VideoWidth:  width,
		VideoHeight: height,
	})
	if err != nil {
		release()
		return fmt.Errorf("publish the share: %w", err)
	}

	c.mu.Lock()
	out.track, out.pub = track, pub
	settled := out.settled
	c.mu.Unlock()

	// The stream can end inside the negotiation window — a capture child that
	// died at its first frame. The completion callback found no publication to
	// retire, so the track published a moment later is retired here, and the
	// error is the report where the event would have been.
	if settled {
		_ = c.room.LocalParticipant.UnpublishTrack(pub.SID())
		return errors.New("the stream ended as it was being published")
	}

	return nil
}

// StopShare takes this end's stream down on purpose: unpublish, and the
// source closed so the encoder behind it learns now rather than at its next
// frame. No event follows — the caller asked. Safe with nothing running.
func (c *Call) StopShare() {
	c.mu.Lock()
	out := c.outShare
	if out != nil {
		out.stopped = true
		c.outShare = nil
	}
	c.mu.Unlock()

	if out == nil {
		return
	}

	if out.pub != nil {
		_ = c.room.LocalParticipant.UnpublishTrack(out.pub.SID())
	}
	_ = out.src.Close()
}

// settleShareSend is the reader track's write loop ending — EOF from the
// encoder, or the track closed under it. Only an end nobody here asked for is
// reported; a share that never finished publishing is StartShare's error to
// carry instead.
func (c *Call) settleShareSend(out *outboundShare) {
	c.mu.Lock()
	out.settled = true
	current := c.outShare == out
	stopped := out.stopped
	if current {
		c.outShare = nil
	}
	pub := out.pub
	c.mu.Unlock()

	if !current || stopped {
		return
	}

	_ = out.src.Close()
	if pub == nil {
		return // StartShare is still in flight and will see settled
	}

	_ = c.room.LocalParticipant.UnpublishTrack(pub.SID())
	c.emit(ShareStopped{})
}

// tokenAllowsScreen reads the join token's video grant without verifying it —
// the server holds the key and the enforcement; this is only what greys a
// button. LiveKit's semantics: canPublish false forbids everything, and a
// canPublishSources list restricts to what it names, an empty or absent list
// restricting nothing. Stoat lists "screen_share" exactly when the channel
// grants the Video permission and the instance has video on. An unreadable
// token answers true and leaves the server to say no.
func tokenAllowsScreen(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.StdEncoding.DecodeString(parts[1]); err != nil {
			return true
		}
	}

	var claims struct {
		Video struct {
			CanPublish        *bool    `json:"canPublish"`
			CanPublishSources []string `json:"canPublishSources"`
		} `json:"video"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return true
	}

	if claims.Video.CanPublish != nil && !*claims.Video.CanPublish {
		return false
	}
	if len(claims.Video.CanPublishSources) == 0 {
		return true
	}

	return slices.Contains(claims.Video.CanPublishSources, "screen_share")
}
