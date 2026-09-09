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
	"github.com/sentinelb51/gopus"
)

/* The surface */

// ShareCodec names the byte stream a watched share is remuxed into — exactly
// the demuxer the decoder must be told to force, the codec having come from
// the session rather than from sniffing bytes.
type ShareCodec string

const (
	// ShareIVF is every codec in IVF: a 32-byte header, then twelve bytes per
	// frame — VP8, VP9 and AV1 as the container was made for, and H.264
	// under the H264 fourcc, which ffmpeg's demuxer maps like any other. One
	// framing on purpose: bare Annex-B carries no lengths, so its parser can
	// only close a frame at the start code opening the next, which on a live
	// stream is a frame of delay for nothing. The timebase is 1/90000, so the
	// RTP timestamp is the pts.
	ShareIVF ShareCodec = "ivf"

	// ShareHEVC is H.265 as raw Annex-B, the one codec the IVF demuxer
	// refuses a fourcc for. It avoids the frame of delay another way: every
	// access unit is followed by the *next* unit's access unit delimiter,
	// which is the NAL the parser closes a unit on, sent ahead of the frame
	// it belongs to. Measured at 5 fps: 5.6 ms held against 207 without it.
	ShareHEVC ShareCodec = "hevc"
)

// ShareOpen is the watcher's half of a watch: called once, from the call's
// own goroutine, when the share's track has actually arrived and its codec
// is known. codec is the demuxer to force and name the codec as a reader
// would say it ("AV1"), which is all a window title wants. width and height
// are what the sender declared, zero where they declared nothing. It answers
// with where the muxed bytes go — that writer closing is how the watcher's
// decoder learns the stream ended — or an error, which abandons the watch.
type ShareOpen func(codec ShareCodec, name string, width, height int) (io.WriteCloser, error)

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

	// The subscribe callback is what starts the reader, and only ever that.
	// The publication's TrackRemote is not consulted: lksdk keeps it after an
	// unsubscribe — nothing clears it short of an unpublish — so a second
	// watch of the same share would start its reader on the dead track,
	// read EOF at once and report the share ended while the fresh
	// subscription was still on its way. Every SetSubscribed(true) yields a
	// new track and a new callback, the one before it having been switched
	// off at registration, so waiting is always right.

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

	// shareMaxLate is how many packets the reassembler may hold behind a
	// missing one. It has to span the largest frame a share sends — a
	// keyframe at screenshare bitrates runs to hundreds of packets — plus the
	// reordering the network adds under it.
	shareMaxLate = 1024

	// shareReorderWait is how long a missing packet is waited for before the
	// frame it belongs to is given up on. A retransmission takes a round trip
	// and the relay's turnaround; giving up sooner drops a frame that was
	// about to be whole, and a dropped frame is a picture frozen until the
	// next keyframe, a CLI encoder being unable to answer the demand for one.
	shareReorderWait = 250 * time.Millisecond

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

	codec, name, mux, depacketizer, ok := shareCodec(track, width, height)
	if !ok {
		c.endShareWatch(userID, w,
			fmt.Errorf("the share is %s, which this client cannot take yet", track.Codec().MimeType))
		return
	}

	out, err := w.open(codec, name, width, height)
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

	assembler := newFrameAssembler(depacketizer)

	// The frame sink is built once rather than per packet: a closure capturing
	// its error by reference is two heap allocations, and a keyframe alone is
	// thirty-odd packets. The error is sticky, which the loop already assumed —
	// it returns on the first one. Neither muxer retains the sample.
	var (
		writeErr error
		sample   media.Sample
	)
	emit := func(frame []byte, timestamp uint32) {
		if writeErr != nil {
			return
		}

		sample.Data, sample.PacketTimestamp = frame, timestamp
		writeErr = mux.write(out, &sample)
	}

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

		// Read into a packet of the assembler's own rather than through ReadRTP,
		// which mints an MTU buffer and a packet struct per call — at a share's
		// packet rate, twenty times a talker's, that was the receive path's one
		// remaining per-packet allocation. The assembler hands it back once
		// nothing can still point into it.
		packet := assembler.acquire()
		n, _, err := track.Read(packet.buf[:])
		if err == nil {
			err = packet.Unmarshal(packet.buf[:n])
		}
		if err != nil {
			assembler.release(packet)

			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			if !errors.Is(err, io.EOF) {
				endErr = fmt.Errorf("the stream stopped: %w", err)
			}

			return
		}

		now := time.Now()
		assembler.push(packet, now, emit)

		// A frame lost to the network is one everything after it references,
		// so a keyframe is demanded — throttled: a burst of holes is one
		// demand.
		if assembler.takeDropped() > 0 && now.Sub(lastPLI) > sharePLIInterval {
			from.WritePLI(track.SSRC())
			lastPLI = now
		}

		if writeErr != nil {
			select {
			case <-w.done:
				// The watcher closed its decoder; not the stream failing.
			default:
				endErr = fmt.Errorf("the decoder stopped taking the stream: %w", writeErr)
			}

			return
		}
	}
}

/* Reassembly */

// frameAssembler turns a share's RTP packets back into whole frames and
// hands each over the moment its last packet lands. pion's samplebuilder
// stood here before it and holds every complete frame until the packet
// *after* it arrives — a frame of delay on every frame, two hundred
// milliseconds at 5 fps — and its flush gives up on whatever is still in
// flight, so it could not be hurried. This is the same reorder buffer without
// the wait: packets are filed by sequence number and consumed in order; a
// frame is the run of packets sharing a timestamp, closed by the marker; a
// packet missing for longer than shareReorderWait is given up on, and the
// frame it belonged to is dropped and counted so the reader can demand a
// keyframe. One goroutine's property.
type frameAssembler struct {
	newDepacketizer func() rtp.Depacketizer
	depacketizer    rtp.Depacketizer

	buffer [1 << 16]*sharePacket
	head   uint16    // the next sequence number to consume
	newest uint16    // the highest filed
	primed bool      // head is set
	gapAt  time.Time // when head was first found missing; zero while it is not

	// free is the packets not filed anywhere, and held the one consumed last:
	// pion's AV1 depacketizer keeps an unfinished fragment as a slice of the
	// payload it arrived in until the next packet completes it, so the packet
	// just consumed is the one something may still point into, and it goes
	// back a packet late.
	free []*sharePacket
	held *sharePacket

	frame     []byte
	timestamp uint32
	have      bool // a frame is open, at timestamp
	broken    bool // it lost a packet: dropped at its end rather than emitted
	dropped   int  // frames dropped since takeDropped
}

// sharePacket is one RTP packet and the bytes it was read into. Unmarshal
// points the payload into buf and reuses the header's own slices, so a packet
// costs nothing after its first use.
type sharePacket struct {
	rtp.Packet
	buf [readMTU]byte
}

func newFrameAssembler(newDepacketizer func() rtp.Depacketizer) *frameAssembler {
	return &frameAssembler{newDepacketizer: newDepacketizer, depacketizer: newDepacketizer()}
}

// acquire hands out a packet to read into: a freed one, or a new one while the
// reorder window is still growing.
func (a *frameAssembler) acquire() *sharePacket {
	if n := len(a.free); n > 0 {
		p := a.free[n-1]
		a.free = a.free[:n-1]

		return p
	}

	return &sharePacket{}
}

// release takes a packet back once nothing points into it.
func (a *frameAssembler) release(p *sharePacket) {
	a.free = append(a.free, p)
}

// retire is release for a consumed packet, one packet late — see held.
func (a *frameAssembler) retire(p *sharePacket) {
	if a.held != nil {
		a.release(a.held)
	}
	a.held = p
}

// push files one packet and consumes everything now in order, emit taking
// each frame that completes. The frame handed to emit is reused afterwards.
// The packet is the assembler's from here, whichever way it goes.
func (a *frameAssembler) push(p *sharePacket, now time.Time, emit func(frame []byte, timestamp uint32)) {
	if !a.primed {
		a.head, a.newest, a.primed = p.SequenceNumber, p.SequenceNumber, true
	} else {
		if int16(p.SequenceNumber-a.head) < 0 {
			a.release(p)
			return // consumed or given up on: a retransmission that came too late
		}
		if int16(p.SequenceNumber-a.newest) > 0 {
			a.newest = p.SequenceNumber
		}
	}
	if dup := a.buffer[p.SequenceNumber]; dup != nil {
		a.release(dup)
	}
	a.buffer[p.SequenceNumber] = p

	for {
		next := a.buffer[a.head]
		if next == nil {
			if a.head == a.newest+1 {
				a.gapAt = time.Time{}
				return // caught up
			}
			// A hole with packets filed beyond it: a retransmission takes a
			// round trip, so it is waited for — up to a point, and up to a
			// number of packets, the buffer being a ring.
			if a.gapAt.IsZero() {
				a.gapAt = now
			}
			if now.Sub(a.gapAt) < shareReorderWait && int(a.newest-a.head) < shareMaxLate {
				return
			}
			a.head++
			a.gapAt = time.Time{}
			a.lose()

			continue
		}

		a.buffer[a.head] = nil
		a.head++
		a.gapAt = time.Time{}
		a.consume(next, emit)
		a.retire(next)
	}
}

// consume is one packet in order. A new timestamp is a new frame, which
// closes the one open whether or not its marker was seen — a sender that
// sets none still delimits by time.
func (a *frameAssembler) consume(p *sharePacket, emit func([]byte, uint32)) {
	if a.have && p.Timestamp != a.timestamp {
		a.finish(emit)
	}
	if !a.have {
		a.have, a.timestamp = true, p.Timestamp
		if !a.depacketizer.IsPartitionHead(p.Payload) {
			a.lose() // its first packet is what went missing
		}
	}
	if a.broken {
		return
	}

	payload, err := a.depacketizer.Unmarshal(p.Payload)
	if err != nil {
		a.lose()
		return
	}
	a.frame = append(a.frame, payload...)

	if a.depacketizer.IsPartitionTail(p.Marker, p.Payload) {
		a.finish(emit)
	}
}

// lose marks the frame in hand lost, and replaces the depacketizer with it:
// pion's carry a fragment being reassembled across packets, and one whose end
// never came would be prepended to the next frame's first unit.
func (a *frameAssembler) lose() {
	a.broken = true
	a.depacketizer = a.newDepacketizer()
}

// finish closes the frame in hand: handed over whole, or counted as dropped.
func (a *frameAssembler) finish(emit func([]byte, uint32)) {
	switch {
	case a.broken:
		a.dropped++
	case len(a.frame) > 0:
		emit(a.frame, a.timestamp)
	}
	a.frame = a.frame[:0]
	a.have, a.broken = false, false
}

// takeDropped reports the frames dropped since it was last asked.
func (a *frameAssembler) takeDropped() int {
	n := a.dropped
	a.dropped = 0

	return n
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
// told, the codec's reader-facing name and the depacketizer that reassembles
// it. AV1 rides the IVF muxer like the VP codecs: the depacketizer emits
// temporal units in the low-overhead bitstream, which is exactly what an IVF
// frame holds. H.265's depacketizer emits Annex-B the way H.264's does, and
// goes out raw with a delimiter behind every unit — see ShareHEVC. Not ok is
// a codec this client cannot remux.
func shareCodec(track *webrtc.TrackRemote, width, height int) (ShareCodec, string, shareMux, func() rtp.Depacketizer, bool) {
	mime := track.Codec().MimeType
	switch {
	case strings.EqualFold(mime, webrtc.MimeTypeVP8):
		return ShareIVF, "VP8", newIVFMux("VP80", width, height), func() rtp.Depacketizer { return &codecs.VP8Packet{} }, true
	case strings.EqualFold(mime, webrtc.MimeTypeVP9):
		return ShareIVF, "VP9", newIVFMux("VP90", width, height), func() rtp.Depacketizer { return &codecs.VP9Packet{} }, true
	case strings.EqualFold(mime, webrtc.MimeTypeAV1):
		return ShareIVF, "AV1", newIVFMux("AV01", width, height), func() rtp.Depacketizer { return &codecs.AV1Depacketizer{} }, true
	case strings.EqualFold(mime, webrtc.MimeTypeH264):
		return ShareIVF, "H.264", newIVFMux("H264", width, height), func() rtp.Depacketizer { return &codecs.H264Packet{} }, true
	case strings.EqualFold(mime, webrtc.MimeTypeH265):
		return ShareHEVC, "H.265", &annexBMux{}, func() rtp.Depacketizer { return &codecs.H265Depacketizer{} }, true
	}

	return "", "", nil, nil, false
}

/* Muxing */

// shareMux writes reassembled frames as the byte stream the decoder's
// demuxer was told to expect. One goroutine's property; nothing here locks.
type shareMux interface {
	write(out io.Writer, sample *media.Sample) error
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
		// VP8 says whether a frame is a keyframe in its first bit, and an
		// AV1 keyframe is entered at its sequence header — which the RTP
		// spec makes a new coded video sequence carry, so the unit the PLI
		// demands is recognisable. VP9's header is not worth parsing here,
		// and a decoder skips what it cannot enter at the cost of a logged
		// complaint.
		if m.fourCC == "VP80" && !vp8KeyframeStarts(sample.Data) {
			return nil
		}
		if m.fourCC == "AV01" && !av1SequenceHeaderIn(sample.Data) {
			return nil
		}
		if m.fourCC == "H264" && !h264KeyframeStarts(sample.Data) {
			return nil
		}
		if err := m.writeHeader(out); err != nil {
			return err
		}
		m.started = true
		m.last = sample.PacketTimestamp
	}

	// The assembler emits in order, so the uint32 subtraction carries a
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

// annexBMux writes H.265 as it came off the depacketizer — Annex-B, which is
// what the raw demuxer reads — with one access unit delimiter after every
// unit. The delimiter opens the *next* unit, so the parser closes this one
// the moment it lands instead of at the next frame's first slice; a
// delimiter is optional in Annex-B and a unit may open with one, so the
// stream stays legal. Gated like the IVF muxer: nothing goes out before a
// keyframe carrying its own parameter sets, which is what the PLI demands.
type annexBMux struct {
	started bool
}

// hevcAUD is the delimiter: NAL type 35, pic_type "any", the trailing bit.
// A copy of video.hevcAUD by construction — voice imports only domain.
var hevcAUD = []byte{0, 0, 0, 1, 0x46, 0x01, 0x50}

func (m *annexBMux) write(out io.Writer, sample *media.Sample) error {
	if !m.started {
		if !h265KeyframeStarts(sample.Data) {
			return nil
		}
		m.started = true
	}

	if _, err := out.Write(sample.Data); err != nil {
		return err
	}
	_, err := out.Write(hevcAUD)

	return err
}

// vp8KeyframeStarts reads the one bit VP8 puts first: the frame tag's P bit,
// zero for a keyframe.
func vp8KeyframeStarts(frame []byte) bool {
	return len(frame) > 0 && frame[0]&0x01 == 0
}

// av1SequenceHeaderIn hops a temporal unit's OBUs asking for a sequence
// header — the OBU a decoder cannot enter the stream without. The
// depacketizer writes a size field onto every OBU it emits, so the walk can
// always hop.
//
// A copy of video.av1HasSequenceHeader by construction: voice imports only
// domain (the rvoice seam), so the walk cannot live in one place. A fix to
// either must be carried to the other.
func av1SequenceHeaderIn(unit []byte) bool {
	for i := 0; i < len(unit); {
		header := unit[i]
		if header&0x80 != 0 {
			return false // the forbidden bit; this is not an OBU
		}
		if (header>>3)&0xF == 1 {
			return true
		}
		i++
		if header&0x04 != 0 {
			i++ // the extension byte
		}
		if header&0x02 == 0 {
			return false // no size field, so nothing to hop by
		}

		size, shift := 0, 0
		for {
			if i >= len(unit) || shift > 28 {
				return false
			}
			c := unit[i]
			i++
			size |= int(c&0x7F) << shift
			shift += 7
			if c&0x80 == 0 {
				break
			}
		}
		i += size
	}

	return false
}

// h264KeyframeStarts scans the Annex-B units for an IDR slice or an SPS. An
// SPS counts because encoders that send parameter sets in a sample of their
// own put them just ahead of the IDR — a stream entered at the IDR alone
// would be one the decoder has no parameters for.
func h264KeyframeStarts(frame []byte) bool {
	return annexBHas(frame, func(header byte) bool {
		kind := header & 0x1F

		return kind == 5 || kind == 7
	})
}

// h265KeyframeStarts is the same scan in H.265's numbering: the type is six
// bits from the top of the first header byte, a keyframe is any of the IRAP
// range (16-23: BLA, IDR and CRA), and the VPS (32) or SPS (33) count for
// the reason the H.264 SPS does.
func h265KeyframeStarts(frame []byte) bool {
	return annexBHas(frame, func(header byte) bool {
		kind := (header >> 1) & 0x3F

		return (kind >= 16 && kind <= 23) || kind == 32 || kind == 33
	})
}

// annexBHas walks the start codes and asks want about the first header byte
// of each NAL unit behind one.
func annexBHas(frame []byte, want func(header byte) bool) bool {
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
		if want(frame[j+1]) {
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

// ShareSendCodec names what an outbound share's bytes are, which is what the
// track is published as: AV1 temporal units, or H.265 and H.264 access units
// in Annex-B — the shapes lksdk's packetisers eat. H.264 comes in two,
// because the profile is in the SDP as well as in the bitstream: Main is
// the efficient one (CABAC, measured at about two thirds of the bits for the
// same picture on screen content — docs/performance.md) and every current
// decoder takes it; constrained Baseline is what the oldest do.
type ShareSendCodec string

const (
	SendShareAV1          ShareSendCodec = "av1"
	SendShareHEVC         ShareSendCodec = "hevc"
	SendShareH264         ShareSendCodec = "h264"
	SendShareH264Baseline ShareSendCodec = "h264-baseline"
)

// mime is the codec as the room negotiates it.
func (c ShareSendCodec) mime() string {
	switch c {
	case SendShareAV1:
		return webrtc.MimeTypeAV1
	case SendShareHEVC:
		return webrtc.MimeTypeH265
	}

	return webrtc.MimeTypeH264
}

// fmtp is the SDP line the codec is declared with. H.264 names its profile —
// pion registers Main and constrained Baseline among its defaults, and the
// packetisation mode every WebRTC stack uses — where AV1 and H.265 are
// declared bare, which is how pion registers them. Level 3.1 is what every
// browser offers and, with asymmetry allowed, advisory.
func (c ShareSendCodec) fmtp() string {
	switch c {
	case SendShareH264:
		return "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f"
	case SendShareH264Baseline:
		return "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
	}

	return ""
}

// keyframeStarts reports whether a frame is one a decoder can enter the
// stream at, in this codec's terms.
func (c ShareSendCodec) keyframeStarts(frame []byte) bool {
	switch c {
	case SendShareAV1:
		return av1SequenceHeaderIn(frame)
	case SendShareHEVC:
		return h265KeyframeStarts(frame)
	}

	return h264KeyframeStarts(frame)
}

// ErrShareRefused is the room turning a share away at the publish — the one
// failure a caller can do something about, a codec the instance's LiveKit will
// not negotiate being retryable at another. Every other refusal here is about
// this end (no stream, no call, no permission) and retrying it is one more
// encoder started for the same answer.
var ErrShareRefused = errors.New("the room refused the share")

// ShareSource is the stream an outbound share publishes, already framed:
// ReadFrame answers one whole frame — an H.264 access unit in Annex-B, or an
// AV1 temporal unit with its container framing stripped, the two sample
// shapes lksdk packetises — blocking until one is ready, and its error is the
// stream ending. Declared structurally so voice never imports video, the
// PCMSource arrangement again; video.ShareTee is what app hands in.
type ShareSource interface {
	ReadFrame() ([]byte, error)
	Close() error
}

// ShareStopped reports this end's own share ending on its own terms rather
// than the caller's: the byte stream feeding the track hit EOF or broke — the
// captured window closed, the encoder died. Nothing follows StopShare or the
// call ending; the caller caused those.
type ShareStopped struct{}

func (ShareStopped) isVoiceEvent() {}

// outboundShare is this end's own stream: the source the write loop drains
// and the publication the room knows it as. stopped marks one being taken
// down on purpose; settled marks the write loop having already ended, for the
// start that races it. All of it is guarded by the call's mu.
type outboundShare struct {
	src   ShareSource
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

// StartShare publishes a screenshare: codec's frames read from src — an
// encoder's stdout, framed — declared to the room at width×height, which
// every viewer sizes a window by and the server enforces its limits against.
// It blocks for the publish negotiation, so it belongs on a worker. One
// share at a time; the source is closed by whatever ends it.
//
// fps steps the RTP timestamps and nothing else — the write loop paces
// nothing, see pumpShareSend. It is asked for rather than read off the
// stream because Annex-B carries no timing at all, and the capture child's
// own fps filter is what makes the flat step honest. The one-slice-per-frame
// contract on the encoder (video's args say so too) is what lets a frame be
// recognised at all: an access unit ends at its slice, so a sliced encode
// would fuse frames. For AV1 the frame is the IVF frame — one temporal unit
// — and the contract holds by construction.
func (c *Call) StartShare(src ShareSource, codec ShareSendCodec, width, height, fps int) error {
	if src == nil {
		return errors.New("no stream to publish")
	}
	if fps < 1 {
		return errors.New("no frame rate to stamp the share with")
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

	track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{
		MimeType: codec.mime(), ClockRate: videoClockRate, SDPFmtpLine: codec.fmtp(),
	})
	if err != nil {
		release()
		return fmt.Errorf("share track: %w", err)
	}

	// The write loop starts now, ahead of the publish, because draining the
	// encoder through the negotiation is the point: frames it produces while
	// the room is still answering are dropped here rather than queued in the
	// pipe — a queue the paced sender it replaced replayed at 1× forever, a
	// standing delay every viewer kept and the sender's own preview never
	// showed. Bound once is enough: a rebind after a reconnect finds the loop
	// already running.
	bound := make(chan struct{})
	var once sync.Once
	track.OnBind(func() { once.Do(func() { close(bound) }) })
	go c.pumpShareSend(out, track, codec, time.Second/time.Duration(fps), bound)

	pub, err := c.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:        "screenshare",
		Source:      livekit.TrackSource_SCREEN_SHARE,
		VideoWidth:  width,
		VideoHeight: height,
	})
	if err != nil {
		release()
		return fmt.Errorf("publish the share: %w: %w", ErrShareRefused, err)
	}

	c.mu.Lock()
	out.track, out.pub = track, pub
	settled, stopped := out.settled, out.stopped
	c.mu.Unlock()

	// The stream can end inside the negotiation window — a capture child that
	// died at its first frame, or a StopShare that beat the publish. Either
	// found no publication to retire, so the track published a moment later is
	// retired here, and the error is the report where the event would have
	// been.
	if settled || stopped {
		_ = c.room.LocalParticipant.UnpublishTrack(pub.SID())
		return errors.New("the stream ended as it was being published")
	}

	// A codec the room will not take is not an error from PublishTrack: the
	// server answers the publish with whatever codec it *did* file — or the
	// negotiation that follows leaves the track unbound, WriteSample a silent
	// no-op for the life of the share. Both are read here, so the caller can
	// try the next codec down: the recorded codec first, which costs nothing,
	// then the bind, which lands within a round trip of the publish on a room
	// that took the codec and never on one that did not.
	if err := c.awaitShareBind(pub, codec, bound); err != nil {
		c.mu.Lock()
		out.stopped = true
		if c.outShare == out {
			c.outShare = nil
		}
		c.mu.Unlock()
		_ = c.room.LocalParticipant.UnpublishTrack(pub.SID())

		return fmt.Errorf("%w: %w", ErrShareRefused, err)
	}

	return nil
}

// shareBindWait is how long a published share may take to bind before the
// room is taken to have refused its codec. A bind follows the publish by one
// offer/answer round trip on the signalling socket, so this is generous; a
// codec the room lacks never binds at all.
const shareBindWait = 5 * time.Second

// awaitShareBind is the two refusal checks StartShare makes after a publish:
// the codec the server recorded against the publication, and the bind.
func (c *Call) awaitShareBind(pub *lksdk.LocalTrackPublication, codec ShareSendCodec, bound <-chan struct{}) error {
	if mime := pub.MimeType(); mime != "" && !strings.EqualFold(mime, codec.mime()) {
		return fmt.Errorf("the room filed the share as %s rather than %s", mime, codec.mime())
	}

	select {
	case <-bound:
		return nil
	case <-c.done:
		return errors.New("the call ended")
	case <-time.After(shareBindWait):
		return fmt.Errorf("the room did not negotiate %s", codec.mime())
	}
}

// pumpShareSend is an outbound share's write loop, and it paces nothing: the
// capture child's fps filter is the clock, so a frame is published the moment
// it arrives and frame only steps the RTP timestamps. lksdk's own reader
// track is deliberately not used — its writeWorker sends exactly one frame
// per duration off a schedule of its own, so a backlog standing when it
// starts, and any a stall ever adds, is replayed at 1× for the life of the
// share: a fixed seconds-long delay every viewer keeps, measured here at
// roughly five. Writing on arrival is what makes the share as live as the
// encoder.
//
// Until the track is bound nothing can be sent, and entering mid-GOP decodes
// as nothing anyway, so pre-bind and pre-keyframe frames are dropped rather
// than queued: a viewer starts at most one keyframe interval behind the
// screen, never seconds.
func (c *Call) pumpShareSend(out *outboundShare, track *lksdk.LocalTrack,
	codec ShareSendCodec, frame time.Duration, bound <-chan struct{}) {

	defer c.settleShareSend(out)

	started := false
	for {
		select {
		case <-c.done:
			return
		default:
		}

		data, err := out.src.ReadFrame()
		if err != nil {
			return
		}

		select {
		case <-bound:
		default:
			continue
		}

		if !started {
			if !codec.keyframeStarts(data) {
				continue
			}
			started = true
		}

		if err := track.WriteSample(media.Sample{Data: data, Duration: frame}, nil); err != nil {
			return
		}
	}
}

// StopShare takes this end's stream down on purpose: unpublish, and the
// source closed so the encoder behind it learns now rather than at its next
// frame. No event follows — the caller asked. Safe with nothing running.
func (c *Call) StopShare() {
	// pub is read under the lock: StartShare writes it from another goroutine
	// once the publish answers, and a stop inside that window must not race the
	// write — StartShare sees stopped and retires the track itself.
	c.mu.Lock()
	out := c.outShare
	var sid string
	if out != nil {
		out.stopped = true
		c.outShare = nil
		if out.pub != nil {
			sid = out.pub.SID()
		}
	}
	c.mu.Unlock()

	if out == nil {
		return
	}

	if sid != "" {
		_ = c.room.LocalParticipant.UnpublishTrack(sid)
	}
	_ = out.src.Close()

	// The sound is a track of its own but not a share of its own: the picture
	// stopping is the share ending, and sound with no picture is not a thing
	// this client offers.
	c.StopShareAudio()
}

// settleShareSend is the write loop ending — EOF from the encoder, the call
// closing, or the track refusing a write. Only an end nobody here asked for
// is reported; a share that never finished publishing is StartShare's error
// to carry instead.
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

/* Share audio */

// ShareAudioSource is the sound a share sends beside its picture: interleaved
// signed 16-bit at the rate and channel count StartShareAudio is told, one
// 20 ms frame per Read, blocking until there is one. Declared structurally so
// voice never imports audio — the PCMSource arrangement again, and
// audio.Loopback is what app hands in.
//
// Read must answer on the cadence of a clock rather than of the material: a
// program rendering nothing produces no packets at all, and a source that
// simply blocked there would stall the encoder where it should send silence.
type ShareAudioSource interface {
	Read(pcm []int16) (int, error)
	Close()
}

// shareAudio is this end's own share-audio publication: the source, the track
// it feeds and the encoder between them. stopped marks one being taken down on
// purpose, for the write loop that may notice the end first. Guarded by the
// call's mu, like outboundShare.
type shareAudio struct {
	src   ShareAudioSource
	track *lksdk.LocalTrack
	pub   *lksdk.LocalTrackPublication

	encoder *gopus.Encoder
	into    opusEncodeIn
	packet  []byte

	// frame is one Read's worth in samples, channels included, and framePer the
	// per-channel count Opus is asked for — the one gopus counts a frame in.
	frame    int
	framePer int

	stopped bool

	// done ends the write loop, and closeOnce is what lets every path that can
	// end one — a refused publish, a deliberate stop, the call going down —
	// close it without the second of them panicking.
	done      chan struct{}
	closeOnce sync.Once
}

// end stops the write loop. Idempotent, like shareWatch.end.
func (sa *shareAudio) end() { sa.closeOnce.Do(func() { close(sa.done) }) }

// shareAudioFmtp declares the stereo the encoder actually sends.
//
// The microphone's line is pion's default registration verbatim because mono
// against stereo is the `stereo=` parameter and its default of 0 is the truth
// there. Here it is not: this track carries two real channels, and a receiver
// taking the default would allocate one and fold the image down. The extra
// parameters cannot cost the match — pion compares the keys two sides *share* —
// but a mismatch would be silent, Bind never firing and the track never being
// sent, which is why the write loop says so out loud rather than waiting
// forever.
const shareAudioFmtp = "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1"

// shareAudioBitrate is what a screenshare's sound is worth if nothing says
// otherwise. Music rather than speech: 128 kbps stereo is about where Opus
// stops being the thing anybody notices, and a share is already spending
// megabits on the picture.
const shareAudioBitrate = 128000

// bindWarning is how long the write loop waits for the track to bind before
// saying that it has not. Past any real negotiation, and short enough that a
// codec the room would not take is reported while the share is still on screen
// rather than never.
const bindWarning = 10 * time.Second

// StartShareAudio publishes the machine's own sound as a second track beside
// the share's picture, at rate and channels — which must be what src answers
// in — and bitrate bits per second.
//
// It blocks for the publish negotiation, so it belongs on a worker. One at a
// time, and the source is closed by whatever ends it. Independent of the
// picture on purpose: they are separate tracks to the room, so the sound can be
// refused or fail without taking the share down with it.
func (c *Call) StartShareAudio(src ShareAudioSource, rate, channels, bitrate int) error {
	if src == nil {
		return errors.New("no sound to publish")
	}
	if !c.canShare {
		return errors.New("the server does not allow publishing a screenshare here")
	}
	if c.room == nil {
		return errors.New("no call to share into")
	}

	// Opus is told the rate it is actually fed. Every rate audio offers is one it
	// encodes natively, so nothing resamples and the frame is 20 ms of whatever
	// was asked for.
	encoder, err := gopus.NewEncoder(rate, channels, gopus.Audio)
	if err != nil {
		return fmt.Errorf("share audio encoder: %w", err)
	}

	if bitrate <= 0 {
		bitrate = shareAudioBitrate
	}
	encoder.SetBitrate(bitrate)
	encoder.SetVbr(true)

	// DTX on and FEC off, the opposite of the microphone and for the same
	// reason: this is music. True digital silence — a game paused, a video
	// stopped — is worth not sending, and in-band FEC is SILK's, which Opus has
	// left for CELT at any bitrate a share runs at.
	if tuning, ok := any(encoder).(opusTuning); ok {
		_ = tuning.SetDTX(true)
		_ = tuning.SetInBandFEC(false)
	}

	sa := &shareAudio{
		src:      src,
		encoder:  encoder,
		frame:    rate / 50 * channels,
		framePer: rate / 50,
		done:     make(chan struct{}),
	}
	if into, ok := any(encoder).(opusEncodeIn); ok {
		sa.into, sa.packet = into, make([]byte, maxPacket)
	}

	c.mu.Lock()
	if c.outShareAudio != nil {
		c.mu.Unlock()

		return errors.New("already sharing sound")
	}
	c.outShareAudio = sa
	c.mu.Unlock()

	// The write loop is started before the publish, so releasing has to end it:
	// a track that never binds would otherwise leave it parked on the bind wait
	// for the rest of the process.
	release := func() {
		c.mu.Lock()
		if c.outShareAudio == sa {
			c.outShareAudio = nil
		}
		c.mu.Unlock()

		sa.end()
	}

	// The clock rate is Opus's declared 48 kHz whatever the encoder is fed:
	// RFC 7587 fixes the RTP clock, and a lower capture rate is a narrower band
	// inside the same timebase rather than a slower one.
	track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   sampleRate,
		Channels:    sdpChannels,
		SDPFmtpLine: shareAudioFmtp,
	})
	if err != nil {
		release()

		return fmt.Errorf("share audio track: %w", err)
	}

	bound := make(chan struct{})
	var once sync.Once
	track.OnBind(func() { once.Do(func() { close(bound) }) })
	go c.pumpShareAudio(sa, track, bound)

	pub, err := c.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:   "screenshare audio",
		Source: livekit.TrackSource_SCREEN_SHARE_AUDIO,
	})
	if err != nil {
		release()

		return fmt.Errorf("publish the share's sound: %w: %w", ErrShareRefused, err)
	}

	c.mu.Lock()
	sa.track, sa.pub = track, pub
	stopped := sa.stopped
	c.mu.Unlock()

	// A stop that beat the publish found no publication to retire, so the track
	// published a moment later is retired here.
	if stopped {
		_ = c.room.LocalParticipant.UnpublishTrack(pub.SID())

		return errors.New("the sound was stopped as it was being published")
	}

	return nil
}

// pumpShareAudio is the share audio write loop. The capture paces it: Read
// blocks for a frame's worth and answers with silence rather than nothing when
// the machine is quiet, so this wakes fifty times a second either way and the
// track never stalls.
//
// Unlike the picture's loop it drops nothing it cannot send yet. Audio is a
// continuous stream and a hole in it is heard, where a dropped frame of video
// is not seen; it waits for the bind instead, and says so when the bind is what
// never comes.
func (c *Call) pumpShareAudio(sa *shareAudio, track *lksdk.LocalTrack, bound <-chan struct{}) {
	defer c.settleShareAudio(sa)

	warn := time.NewTimer(bindWarning)
	defer warn.Stop()

	select {
	case <-bound:
	case <-sa.done:
		return
	case <-warn.C:
		// An unbound track is a codec the room would not take, which for Opus
		// means the fmtp or the channel count — the one failure here that is
		// otherwise perfectly silent.
		log.Print("voice: share audio track has not bound; the room may have refused the codec")

		select {
		case <-bound:
		case <-sa.done:
			return
		}
	}

	pcm := make([]int16, sa.frame)

	for {
		select {
		case <-sa.done:
			return
		default:
		}

		n, err := sa.src.Read(pcm)
		if err != nil {
			return // the capture ended; the picture is not this loop's to stop
		}
		if n < sa.frame {
			continue
		}

		encoded, err := sa.encodeFrame(pcm)
		if err != nil {
			log.Printf("voice: encode share audio: %v", err)

			continue
		}

		if err := track.WriteSample(media.Sample{
			Data:     encoded,
			Duration: frameMillis * time.Millisecond,
		}, nil); err != nil {
			select {
			case <-sa.done:
				return
			default:
			}

			log.Printf("voice: publish share audio: %v", err)
		}
	}
}

// encodeFrame turns one frame into a packet, into the loop's own buffer where
// the binding offers that. The answer aliases sa.packet and is good only until
// the next frame, WriteSample having consumed it by then.
func (sa *shareAudio) encodeFrame(pcm []int16) ([]byte, error) {
	if sa.into == nil {
		return sa.encoder.Encode(pcm, sa.framePer, maxPacket)
	}

	n, err := sa.into.EncodeIn(pcm, sa.framePer, sa.packet)
	if err != nil {
		return nil, err
	}

	return sa.packet[:n], nil
}

// StopShareAudio takes this end's share audio down on purpose. Safe with none
// running and safe to call twice.
func (c *Call) StopShareAudio() {
	c.mu.Lock()
	sa := c.outShareAudio
	var sid string
	if sa != nil {
		sa.stopped = true
		c.outShareAudio = nil
		if sa.pub != nil {
			sid = sa.pub.SID()
		}
	}
	c.mu.Unlock()

	if sa == nil {
		return
	}

	sa.end()

	if sid != "" {
		_ = c.room.LocalParticipant.UnpublishTrack(sid)
	}
	sa.src.Close()
}

// settleShareAudio is the write loop ending on its own — the capture died, or
// the track stopped taking writes. An end nobody asked for retires the
// publication; one that was asked for has already done it.
func (c *Call) settleShareAudio(sa *shareAudio) {
	c.mu.Lock()
	current := c.outShareAudio == sa
	stopped := sa.stopped
	if current {
		c.outShareAudio = nil
	}
	pub := sa.pub
	c.mu.Unlock()

	if !current || stopped {
		return
	}

	sa.src.Close()
	if pub != nil {
		_ = c.room.LocalParticipant.UnpublishTrack(pub.SID())
	}
}
