package audio

import (
	"math"

	gtcrn "github.com/sentinelb51/gtcrn-go"
)

// gtcrnStage runs GTCRN — which works at 16 kHz on 256-sample hops — inside a
// chain that works at 48 kHz on 20 ms frames, and gives back the band the
// model never sees.
//
// A frame is 320 samples down there, which is a hop and a quarter, so the
// two meet only every four frames: input queues until a whole hop is ready,
// output queues until a whole frame is, and the output starts with enough
// silence that it never runs dry. The delay that costs is fixed by those
// two numbers — hop minus their greatest common divisor, 192 samples — and
// sits on top of the model's own hop and the two filters: about 30 ms.
//
// The model's path carries nothing above 8 kHz. The same hops go through the
// queue untouched and back up to 48 kHz beside it, and that, subtracted from
// the input delayed by exactly the same amount, is what the low path cannot
// carry: the band above, and what the filters' transition took. It is added
// back under a gain the model itself supplies — the share of energy it kept
// above 6 kHz, its verdict on the top of what it did see — so a fricative
// keeps its air and hiss does not. At a gain floor of 1 the sum is the input
// to the sample, delayed.
type gtcrnStage struct {
	dn *gtcrn.Denoiser

	filter *lowpass
	down   *decimator
	up     *interpolator // the model's output
	upRaw  *interpolator // the same hops untouched

	low    []float32 // one frame at 16 kHz
	in     []float32 // the input queue, at 16 kHz
	out    []float32 // the output queue: the model's hops
	raw    []float32 // the same hops as they went in
	gains  []float32 // the high band's gain, one per output-queue sample
	inN    int
	outN   int
	prev   []float32 // the hop the model was handed last: what it answers with next
	delay  []float32 // the input at 48 kHz, gtcrnDelay samples of history then the frame
	band   []float32 // the raw path back at 48 kHz, one frame
	smooth float32   // the high-band gain as applied, eased between hops
}

// gtcrnFrame is the chain's frame at the model's rate.
const gtcrnFrame = FrameSamples / 3

// gtcrnPrime is how much silence the output queue starts with: the most a
// frame can be short by, whole hops having been produced against frames
// consumed, anywhere in the cycle where the two meet again.
var gtcrnPrime = primeSamples(gtcrnFrame, gtcrn.FrameSize)

// gtcrnDelay is the whole path in 48 kHz samples: both filters' group
// delays, which sum to a whole sample, then the model's hop and the queue's
// priming at three samples each. 1433 here, 29.9 ms.
var gtcrnDelay = rate3Taps - 1 + 3*(gtcrn.Latency+gtcrnPrime)

// gtcrnBandFrom is where the model's verdict on its top bands is read from,
// in Hz: high enough to be about the same sounds as the band above 8 kHz,
// low enough to hold a few of the model's bands.
const gtcrnBandFrom = 6000.0

// gtcrnGainTime is how quickly the high band's gain moves between hops, in
// seconds: a step every 16 ms on a noise-like band would click.
const gtcrnGainTime = 0.002

func primeSamples(frame, hop int) int {

	held, avail, worst := 0, 0, 0
	for range hop {
		held += frame
		produced := held / hop * hop
		held -= produced
		avail += produced - frame
		worst = max(worst, -avail)
	}

	return worst
}

func newGTCRNStage() *gtcrnStage {

	f := newLowpass()
	g := &gtcrnStage{
		dn:     gtcrn.New(),
		filter: f,
		down:   newDecimator(f, FrameSamples),
		up:     newInterpolator(f, FrameSamples),
		upRaw:  newInterpolator(f, FrameSamples),
		low:    make([]float32, gtcrnFrame),
		in:     make([]float32, gtcrnFrame+gtcrn.FrameSize),
		out:    make([]float32, gtcrnPrime+2*gtcrn.FrameSize+gtcrnFrame),
		raw:    make([]float32, gtcrnPrime+2*gtcrn.FrameSize+gtcrnFrame),
		gains:  make([]float32, gtcrnPrime+2*gtcrn.FrameSize+gtcrnFrame),
		prev:   make([]float32, gtcrn.FrameSize),
		delay:  make([]float32, gtcrnDelay+FrameSamples),
		band:   make([]float32, FrameSamples),
	}
	g.resetQueues()

	return g
}

// resetQueues drops what the queues hold and primes the output again. The
// model's own memory is left alone: it is the room, still valid, where the
// queues are audio that was never played and would be if left.
func (g *gtcrnStage) resetQueues() {

	g.inN = 0
	clear(g.out[:gtcrnPrime])
	clear(g.raw[:gtcrnPrime])
	clear(g.gains[:gtcrnPrime])
	g.outN = gtcrnPrime
	clear(g.delay)
	g.down.reset()
	g.up.reset()
	g.upRaw.reset()
	g.smooth = 0
}

func (g *gtcrnStage) setFloor(floor float32) { g.dn.SetGainFloor(floor) }

// process rewrites one 48 kHz frame in place with the model's output for the
// audio gtcrnDelay samples earlier, the band above it restored under the
// model's gain, and reports the share of the frame's energy the model kept,
// the highest over the hops it ran — an estimate of speech the gate's veto
// can read the way it reads RNNoise's.
func (g *gtcrnStage) process(frame []float32) float32 {

	frame = frame[:FrameSamples]
	copy(g.delay[gtcrnDelay:], frame)
	g.down.process(frame, g.low)
	copy(g.in[g.inN:], g.low)
	g.inN += len(g.low)

	var kept float32
	at := 0
	for g.inN-at >= gtcrn.FrameSize {
		hop := g.in[at : at+gtcrn.FrameSize]
		// The model answers with the hop before this one, so the raw queue
		// takes the hop before too, and the two line up sample for sample.
		copy(g.raw[g.outN:], g.prev)
		copy(g.prev, hop)
		kept = max(kept, g.dn.Process(hop))
		copy(g.out[g.outN:], hop)
		gain := float32(math.Sqrt(float64(g.dn.KeptAbove(gtcrnBandFrom))))
		fill(g.gains[g.outN:g.outN+gtcrn.FrameSize], gain)
		g.outN += gtcrn.FrameSize
		at += gtcrn.FrameSize
	}
	copy(g.in, g.in[at:g.inN])
	g.inN -= at

	if g.outN < gtcrnFrame {
		// The priming makes this unreachable; silence rather than a panic if
		// the arithmetic is ever wrong.
		clear(g.out[g.outN:gtcrnFrame])
		clear(g.raw[g.outN:gtcrnFrame])
		clear(g.gains[g.outN:gtcrnFrame])
		g.outN = gtcrnFrame
	}
	g.up.process(g.out[:gtcrnFrame], frame)
	g.upRaw.process(g.raw[:gtcrnFrame], g.band)

	// frame = model + gain * (input delayed - raw path), the gain eased.
	const alpha = float32(1 / (gtcrnGainTime * sampleRate))
	smooth := g.smooth
	delayed := g.delay[:FrameSamples]
	gains := g.gains[:gtcrnFrame]
	for i := range frame {
		target := gains[i/3]
		smooth += (target - smooth) * alpha
		if d := target - smooth; d < 1e-6 && d > -1e-6 {
			// Landed: a one-pole left to itself tails off into denormals,
			// which cost a hundred times a normal multiply.
			smooth = target
		}
		frame[i] += smooth * (delayed[i] - g.band[i])
	}
	g.smooth = smooth

	copy(g.out, g.out[gtcrnFrame:g.outN])
	copy(g.raw, g.raw[gtcrnFrame:g.outN])
	copy(g.gains, g.gains[gtcrnFrame:g.outN])
	g.outN -= gtcrnFrame
	copy(g.delay, g.delay[FrameSamples:])

	return kept
}

func fill(x []float32, v float32) {
	for i := range x {
		x[i] = v
	}
}
