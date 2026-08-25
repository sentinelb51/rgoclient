package voice

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/sentinelb51/gopus"
)

// The shape of what this client sends. 48 kHz mono at a 20 ms frame is what
// Opus is happiest at and what every other Revolt client publishes; matching it
// keeps a resampler out of the path at both ends.
const (
	frameMillis = 20
	frameSize   = sampleRate * frameMillis / 1000
	sampleRate  = 48000
	channels    = 1

	// sdpChannels is what the *declaration* says, which RFC 7587 fixes at 2 for
	// Opus whatever the audio actually is. `channels` above is the real one, and
	// is what the encoder and every buffer here are sized by.
	sdpChannels = 2

	// defaultBitrate is a voice bitrate rather than a music one. Opus at 32 kbps
	// mono is transparent for speech, and the headroom is worth more spent on FEC.
	defaultBitrate = 32000

	// maxPacket bounds one encoded frame. Opus never exceeds this at these
	// settings; it is a cap handed to the encoder, which allocates the packet it
	// returns — gopus offers no caller-supplied buffer.
	maxPacket = 1275
)

// opusTuning is what a libopus binding has to offer for the loss tolerance this
// client wants. Upstream layeh.com/gopus does not have these; sentinelb51/gopus
// does, and the assertion lights them up without this package knowing which is
// linked.
//
// Inband FEC is the single most valuable setting here: it is what lets the
// jitter buffer stay shallow, which is 60-80 % of mouth-to-ear latency.
type opusTuning interface {
	SetInBandFEC(bool) error
	SetPacketLossPerc(int) error
	SetDTX(bool) error
}

// publisher owns the microphone, the encoder and the track they feed. One
// goroutine drives all three, paced by the microphone rather than by a timer:
// the device is the clock, and a ticker beside it would drift against it.
type publisher struct {
	track *lksdk.LocalTrack
	pub   *lksdk.LocalTrackPublication

	source  PCMSource
	encoder *gopus.Encoder
	tuning  opusTuning

	// selfID is who to report this end's speaking as, and speaking is what was
	// last reported — the gate has hangover of its own, so this is already
	// debounced and only the transitions reach the call.
	selfID   string
	speaking bool

	muted atomic.Bool

	closeOnce sync.Once
	done      chan struct{}
	stopped   sync.WaitGroup
}

// microphone is the encoder and the track it feeds, with nothing driving them
// yet. It is built *before* the room exists because publishing in the join
// request means handing the track over at the dial — so what a publisher is made
// of and when it starts running are two separate moments.
type microphone struct {
	track   *lksdk.LocalTrack
	encoder *gopus.Encoder
	tuning  opusTuning
}

// micPublication is what the track is published as, shared by both dials so the
// far end sees the same thing whichever one connected.
//
// DisableDTX is left false so a gated frame costs comfort noise rather than a
// track the far end thinks has died.
func micPublication() *lksdk.TrackPublicationOptions {
	return &lksdk.TrackPublicationOptions{
		Name:   "microphone",
		Source: livekit.TrackSource_MICROPHONE,
	}
}

// newMicrophone builds the encoder and the track. A fresh one is needed per dial
// attempt: preparing a publication binds a transceiver to the track, so a track
// offered to a join that failed cannot be offered to the next one.
func newMicrophone(opts Options) (*microphone, error) {
	encoder, err := gopus.NewEncoder(sampleRate, channels, gopus.Voip)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}

	bitrate := opts.Bitrate
	if bitrate <= 0 {
		bitrate = defaultBitrate
	}
	encoder.SetBitrate(bitrate)
	encoder.SetVbr(true)

	mic := &microphone{encoder: encoder}

	// Loss tolerance, where the binding offers it. A build without the fork simply
	// runs without FEC rather than failing to compile or to start.
	if tuning, ok := any(encoder).(opusTuning); ok {
		mic.tuning = tuning
		_ = tuning.SetInBandFEC(true)
		_ = tuning.SetPacketLossPerc(initialLossPercent)
		_ = tuning.SetDTX(true)
	} else {
		log.Print("voice: opus binding has no FEC/DTX control; the jitter buffer will run deeper")
	}

	// Two channels, and the audio is mono. RFC 7587 declares Opus in SDP as
	// `opus/48000/2` *always* — the channel count in the rtpmap is fixed, and mono
	// against stereo is the `stereo=` fmtp parameter, which defaults to 0. This is
	// not cosmetic: pion matches a local track against the negotiated codec on
	// MimeType, clock rate **and channels** (`fmtp.ChannelsEqual`, strict), so
	// declaring 1 here finds no match, `Bind` answers ErrUnsupportedCodec, and the
	// microphone is never sent — a call that connects, says it published, and
	// carries no audio.
	//
	// The fmtp line is pion's own default registration verbatim, so the match is
	// the exact one rather than the fallback, and it is what says out loud that
	// this encoder has in-band FEC turned on.
	track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   sampleRate,
		Channels:    sdpChannels,
		SDPFmtpLine: "minptime=10;useinbandfec=1",
	})
	if err != nil {
		return nil, fmt.Errorf("opus track: %w", err)
	}
	mic.track = track

	return mic, nil
}

// newPublisher starts driving an already-published track. The publication is the
// dial's to produce — in the join request where the node takes one there, in a
// negotiation of its own where it does not — so by here it exists either way.
func newPublisher(mic *microphone, pub *lksdk.LocalTrackPublication, src PCMSource, call *Call, opts Options) (*publisher, error) {
	if src == nil {
		return nil, errors.New("no microphone")
	}

	p := &publisher{
		track:   mic.track,
		pub:     pub,
		source:  src,
		encoder: mic.encoder,
		tuning:  mic.tuning,
		done:    make(chan struct{}),
	}

	p.selfID = opts.SelfID

	// The joining state is applied before the loop starts. Options promises no
	// frame is sent the reader did not mean to send, and the capture ring already
	// holds audio from before the dial — a loop born unmuted and muted a moment
	// later has read it, encoded it and sent it.
	p.setMuted(opts.Muted || opts.Deafened)

	p.stopped.Add(1)
	go p.run(call)

	return p, nil
}

// initialLossPercent is what the encoder assumes before it has been told
// otherwise. Non-zero from the first frame: FEC that switches on after loss has
// already been measured is FEC that was not there for the loss that measured it.
const initialLossPercent = 5

// run is the publish loop. The microphone paces it: Read blocks for a frame's
// worth, so this goroutine wakes 50 times a second and does nothing else.
func (p *publisher) run(call *Call) {
	defer p.stopped.Done()

	pcm := make([]int16, frameSize)

	for {
		select {
		case <-p.done:
			return
		default:
		}

		n, err := p.source.Read(pcm)
		if err != nil {
			// The microphone went away, which ends the call rather than publishing
			// silence for the rest of it. fail tears the publisher down and close
			// waits for *this* goroutine, so it cannot be called from inside it.
			go call.fail(fmt.Errorf("microphone: %w", err))

			return
		}
		if n < frameSize {
			continue
		}

		// A muted track is still published — the far end sees the mute rather than a
		// track that stopped — but nothing is sent through it.
		if p.muted.Load() {
			p.reportSpeaking(call, false)
			continue
		}

		// This end's own ring, off the gate rather than off the server's
		// active-speaker report: that report is about remote participants and lands
		// half a second late, where the gate has already decided.
		p.reportSpeaking(call, p.source.Voiced())

		encoded, err := p.encoder.Encode(pcm, frameSize, maxPacket)
		if err != nil {
			log.Printf("voice: encode: %v", err)
			continue
		}

		if err := p.track.WriteSample(media.Sample{
			Data:     encoded,
			Duration: frameMillis * time.Millisecond,
		}, nil); err != nil {
			select {
			case <-p.done:
				return
			default:
			}

			log.Printf("voice: publish frame: %v", err)
		}
	}
}

// reportSpeaking passes this end's own gate decision to the call, on a change
// and never per frame.
func (p *publisher) reportSpeaking(call *Call, speaking bool) {
	if p.selfID == "" || p.speaking == speaking {
		return
	}
	p.speaking = speaking

	call.setSpeaking(p.selfID, speaking)
}

// setMuted holds the microphone and tells the room about it, which is what
// separates a mute from the gate closing.
func (p *publisher) setMuted(muted bool) {
	p.muted.Store(muted)

	if p.pub != nil {
		p.pub.SetMuted(muted)
	}
}

// setLoss retunes FEC to what the connection is actually losing. Reported by the
// subscriber, which is the only half that can see it.
func (p *publisher) setLoss(percent int) {
	if p.tuning == nil {
		return
	}

	_ = p.tuning.SetPacketLossPerc(min(max(percent, 0), 100))
}

func (p *publisher) close() {
	p.closeOnce.Do(func() {
		close(p.done)

		// The source is the app's, not ours, so it is not closed here — but the loop
		// is inside a blocking Read on it and will not notice done until that Read
		// returns. Waiting is bounded by one frame.
		p.stopped.Wait()
	})
}
