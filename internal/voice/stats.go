package voice

import (
	"time"

	"github.com/pion/webrtc/v4"
)

/* What a call measures about itself */

// Stats is how a connected call is doing past which of the three states it is
// in: what it costs to reach the voice node, and what it is not getting back.
//
// Both are this end's own measurements. The voice server sends a quality grade
// of its own down the signal socket — a word, when it feels like it — where
// these come off the media path itself and are numbers a reader can be shown.
type Stats struct {
	// RTT is the round trip to the voice node, zero before anything has measured
	// one. It is the ICE consent check's, which travels the same 5-tuple the audio
	// does, so it is what the audio actually pays rather than what a websocket
	// ping over some other route would claim.
	RTT time.Duration

	// Loss is the percentage the worst path *into* this client is missing, and is
	// negative where no window has completed — a room nobody has spoken in has
	// measured nothing, which is not the same as having measured nothing lost.
	//
	// One direction only: what the voice node makes of what this end sends comes
	// back in RTCP receiver reports, which lksdk reads for its own congestion
	// control and does not pass on.
	Loss int
}

// StatsChanged is one sample. Emitted on the timer rather than on a change: a
// round trip that was 42 ms and is now 43 ms has changed by nothing worth
// redrawing, so what of it is worth painting is the reader's to decide.
type StatsChanged struct {
	Stats Stats
}

func (StatsChanged) isVoiceEvent() {}

// statsInterval is how often a call measures itself. Matched to the ICE
// keepalive: the round trip under it is refreshed by a consent check every two
// seconds, so asking faster reports the same number twice.
const statsInterval = 2 * time.Second

// sampleStats measures the call for as long as it runs.
//
// A goroutine of its own rather than another job on playLanes': that one is the
// receive path's clock, woken by the speakers, and a lane topped up late is
// audible. Nothing here is on a deadline.
func (c *Call) sampleStats() {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return

		case <-ticker.C:
			c.emit(StatsChanged{Stats: Stats{RTT: c.roundTrip(), Loss: c.worstLoss()}})
		}
	}
}

// roundTrip is the round trip to the voice node. Zero where there is nothing to
// measure: before the first consent check answers, and on a connection being
// re-established under it.
func (c *Call) roundTrip() time.Duration {
	transport := c.iceTransport()
	if transport == nil {
		return 0
	}

	// The *selected* pair rather than a walk of GetStats looking for a nominated
	// one: a pair keeps its nomination after losing the selection, so a
	// renomination leaves two of them claiming to carry the media, and the answer
	// would be whichever came out of the map first. This asks the agent which one
	// it is actually sending on, and allocates nothing to do it.
	pair, ok := transport.GetSelectedCandidatePairStats()
	if !ok || pair.CurrentRoundTripTime <= 0 {
		return 0
	}

	return time.Duration(pair.CurrentRoundTripTime * float64(time.Second))
}

// iceTransport reaches the connection's ICE agent, which is the only thing here
// that has timed anything. Through the SCTP transport because that is the one
// route pion exposes from a peer connection down to it; the data channel it
// belongs to is beside the point, every transport on the connection being the
// same one.
//
// lksdk's default is a peer connection each way and `WithSinglePeerConnection`
// collapses them onto the publisher's, so the publisher is the one always
// present — the subscriber exists only where a node made this end fall back to
// the split.
func (c *Call) iceTransport() *webrtc.ICETransport {
	if c.room == nil || c.room.LocalParticipant == nil {
		return nil
	}

	pc := c.room.LocalParticipant.GetPublisherPeerConnection()
	if pc == nil {
		pc = c.room.LocalParticipant.GetSubscriberPeerConnection()
	}
	if pc == nil || pc.SCTP() == nil || pc.SCTP().Transport() == nil {
		return nil
	}

	return pc.SCTP().Transport().ICETransport()
}
