package audio

import (
	"math"
	"sync/atomic"

	"RGOClient/internal/audio/rnnoise"
)

// Processor is one stage of the capture chain: it rewrites a frame in place and
// says whether it still holds speech. Stages run in order and the frame each one
// sees is what the one before it left.
//
// voiced is a vote rather than a verdict — Capture takes the last stage's answer
// — but a stage that has no opinion answers true rather than dropping the frame
// for the stage after it.
//
// This is the slot echo cancellation goes in when it arrives. AEC needs the
// playback signal as a reference, which is why the Engine owns both directions:
// a Processor built by the Engine can be handed the same mixer the speakers are
// reading.
type Processor interface {
	Process(frame []float32) (voiced bool)
}

/* High-pass */

// highPass removes what is below speech: mains hum, desk knocks, the rumble a
// laptop fan puts into a built-in microphone. It is a one-pole filter at ~90 Hz,
// which is under the lowest voice fundamental and costs two multiplies a sample.
//
// It runs before the gate rather than after: rumble is loud enough to hold a
// gate open on a frame with no speech in it at all.
type highPass struct {
	alpha           float32
	lastIn, lastOut float32

	// bypass passes every frame through untouched, so the setting can move
	// mid-call without the chain being rebuilt under a Read.
	bypass bool
}

func newHighPass(cutoff float64) *highPass {
	// Standard one-pole difference equation. rc/(rc+dt) is the pole; at 90 Hz and
	// 48 kHz it lands near 0.988.
	rc := 1 / (2 * math.Pi * cutoff)
	dt := 1 / float64(sampleRate)

	return &highPass{alpha: float32(rc / (rc + dt))}
}

// SetBypass turns the filter into a pass-through. Read applies it, like every
// other stage setting, so it cannot change in the middle of a frame. The memory
// is cleared on the way out so re-enabling starts settled rather than from the
// frame it last saw.
func (h *highPass) SetBypass(bypass bool) {
	if bypass && !h.bypass {
		h.lastIn, h.lastOut = 0, 0
	}
	h.bypass = bypass
}

func (h *highPass) Process(frame []float32) bool {
	if h.bypass {
		return true
	}

	for i, in := range frame {
		out := h.alpha * (h.lastOut + in - h.lastIn)
		h.lastIn, h.lastOut = in, out
		frame[i] = out
	}

	return true
}

/* Noise suppression */

// noiseSuppressor rewrites a frame with RNNoise, which is what removes noise
// *inside* speech — hiss, fans, hum, keyboard — where the gate can only silence
// the frames between words. It sits between the high-pass and the gate: the
// gate's RMS then measures the cleaned signal, so a fan under the threshold
// cannot hold it open.
//
// It answers true rather than the model's own voice estimate: the gate is the
// one deciding stage, and its threshold is a setting the reader has tuned
// against a meter. Two voice detectors with two opinions is a mode nobody can
// reason about.
type noiseSuppressor struct {
	enabled bool

	// dn is created on the first enabled frame rather than at open: its state is
	// ~100 KB of C memory, and most captures with the setting off never need it.
	dn *rnnoise.Denoiser
}

func (n *noiseSuppressor) SetEnabled(enabled bool) { n.enabled = enabled }

func (n *noiseSuppressor) Process(frame []float32) bool {
	if !n.enabled {
		return true
	}
	if n.dn == nil {
		n.dn = rnnoise.New()
	}

	// The model's frame is 10 ms against the chain's 20, so a frame is two calls.
	for at := 0; at+rnnoise.FrameSize <= len(frame); at += rnnoise.FrameSize {
		n.dn.Process(frame[at : at+rnnoise.FrameSize])
	}

	return true
}

/* Preamp */

// preamp is the microphone's own gain, and the meter's tap.
//
// It sits after the filters and in front of the gate rather than at the end of
// the chain, which is the whole of what makes a quiet microphone usable: the gate
// then measures the signal that is actually sent, so raising the gain is also
// what lets the gate hear a voice it was closing on. RNNoise still sees the level
// the microphone delivered, which is the level it was trained on.
//
// The meter reads from here for the same reason. A bar and a threshold that
// disagreed about the scale is the thing that makes a sensitivity slider
// untunable, so there is one measurement and both come off it.
//
// gain is written by whoever moves the setting and level read by the UI thread,
// while Process runs on the capture's own goroutine: both are atomic and neither
// is read inside the loop it bounds.
type preamp struct {
	gain  atomic.Uint32 // float32 bits
	level atomic.Uint32 // float32 bits
}

func newPreamp(gain float32) *preamp {
	p := &preamp{}
	p.SetGain(gain)

	return p
}

// SetGain scales what the gate and the encoder see, 0 to maxGain.
func (p *preamp) SetGain(gain float32) { p.gain.Store(floatBits(clampGain(gain))) }

// Level is the frame's RMS after the gain, 0-1, which is what the gate's
// threshold is compared against.
func (p *preamp) Level() float32 { return bitsFloat(p.level.Load()) }

// Process answers true: a gain has no opinion about whether a frame is speech.
func (p *preamp) Process(frame []float32) bool {
	if gain := bitsFloat(p.gain.Load()); gain != 1 {
		for i := range frame {
			frame[i] *= gain
		}
	}

	p.level.Store(floatBits(rms(frame)))

	return true
}

/* The gate */

// Gate thresholds, in dBFS. The sensitivity setting picks a point between them:
// 0 opens on almost anything, 100 takes a raised voice. They bound a slider
// rather than describing a room, so a reader whose microphone is quiet turns it
// down rather than the gate learning it — an adaptive floor that guesses wrong
// is a gate nobody can reason about.
const (
	gateQuietest = -70.0
	gateLoudest  = -20.0

	// hysteresis is how much further the frame has to fall to close the gate than
	// it took to open it. Without it a voice sitting on the threshold chatters the
	// gate open and shut on every frame, which is more audible than the noise.
	hysteresis = 6.0
)

// hangoverFrames is how many frames the gate stays open after the last one that
// passed. Speech has gaps inside a word — a stop consonant is silence — and a
// gate that closes in them clips the word's tail. 25 frames of 10 ms is 250 ms,
// long enough to carry a sentence over its own pauses.
const hangoverFrames = 25

// noiseGate decides whether a frame is speech, and silences it when it is not.
//
// Silencing rather than muting matters upstream: the encoder is left running and
// answers a silent frame with discontinuous transmission, so the track stays
// alive at the cost of comfort noise. Muting is a different thing entirely — it
// is what the mute button does, and other clients are told about it.
type noiseGate struct {
	open      float32 // linear RMS to open at
	close     float32 // and to close at, always the lower of the two
	remaining int     // hangover frames left

	// bypass passes every frame through untouched and answers true. Push-to-talk
	// sets it: the key decides what is sent, and a gate deciding as well would
	// swallow the quiet start of a held-key sentence.
	bypass bool

	// fade smooths the edges. A gate that switches to and from zero in one sample
	// clicks; ramping over the frame costs one multiply a sample and does not.
	level float32
}

func newNoiseGate(sensitivity int) *noiseGate {
	g := &noiseGate{}
	g.SetSensitivity(sensitivity)

	return g
}

// SetSensitivity moves the threshold. 0-100, clamped, read from the settings
// whenever they change rather than per frame.
func (g *noiseGate) SetSensitivity(sensitivity int) {
	openDB := GateThresholdDB(sensitivity)

	g.open = float32(decibelsToLinear(openDB))
	g.close = float32(decibelsToLinear(openDB - hysteresis))
}

/* The meter's scale */

// meterCeiling is the top of the meter, in dBFS. Its floor is the gate's own
// quietest threshold, so the whole of what the sensitivity slider can ask for is
// somewhere on the bar, with the headroom above the loudest setting still shown.
const meterCeiling = 0.0

// GateThresholdDB is where the gate opens for a sensitivity of 0-100, in dBFS.
// Exported because the settings page draws this threshold on the level meter,
// and a second copy of the mapping up there would be free to drift from this one.
func GateThresholdDB(sensitivity int) float64 {
	fraction := float64(min(max(sensitivity, 0), 100)) / 100

	return gateQuietest + (gateLoudest-gateQuietest)*fraction
}

// MeterRatio places a linear RMS level on the meter, 0-1.
//
// Decibels rather than the level itself: speech sits around -26 dBFS, which is
// 0.05 linear, and the gate's default threshold is 0.0024. A linear bar draws
// both of those on the floor, which is the whole reason the sensitivity setting
// could not be tuned by looking at one.
func MeterRatio(level float32) float32 {
	if level <= 0 {
		return 0
	}

	return meterPosition(20 * math.Log10(float64(level)))
}

// GateRatio places the gate's threshold on that same scale, so the marker and
// the fill cannot disagree about what the setting means.
func GateRatio(sensitivity int) float32 {
	return meterPosition(GateThresholdDB(sensitivity))
}

func meterPosition(db float64) float32 {
	ratio := (db - gateQuietest) / (meterCeiling - gateQuietest)

	return float32(min(max(ratio, 0), 1))
}

// SetBypass turns the gate into a pass-through. Read applies it, like the
// sensitivity, so the mode cannot change in the middle of a frame.
func (g *noiseGate) SetBypass(bypass bool) { g.bypass = bypass }

func (g *noiseGate) Process(frame []float32) bool {
	if g.bypass {
		// Held open rather than merely passed through, so switching back to voice
		// activity does not start with the gate mid-ramp.
		g.level, g.remaining = 1, hangoverFrames

		return true
	}

	level := rms(frame)

	switch {
	case level >= g.open:
		g.remaining = hangoverFrames
	case level < g.close && g.remaining > 0:
		g.remaining--
	case level < g.close:
		g.remaining = 0
	}

	voiced := g.remaining > 0

	target := float32(0)
	if voiced {
		target = 1
	}

	// Ramp across the frame rather than jumping at its edge.
	step := (target - g.level) / float32(len(frame))
	for i := range frame {
		g.level += step
		frame[i] *= g.level
	}
	g.level = target

	return voiced
}

/* Maths */

// rms is the frame's level, which is what every threshold here is expressed in.
func rms(frame []float32) float32 {
	if len(frame) == 0 {
		return 0
	}

	var sum float64
	for _, sample := range frame {
		sum += float64(sample) * float64(sample)
	}

	return float32(math.Sqrt(sum / float64(len(frame))))
}

func decibelsToLinear(db float64) float64 { return math.Pow(10, db/20) }

/* Decibels, for the settings that are expressed in them */

// GainFromDB is the linear gain a decibel figure asks for, which is the unit
// every level in this package is expressed in.
//
// off is the bottom of the caller's range and means silence, no decibel figure
// being able to: the range's own end is passed in rather than named here, so the
// settings stay the one place that decides where it is.
func GainFromDB(db, off int) float32 {
	if db <= off {
		return 0
	}

	return float32(decibelsToLinear(float64(db)))
}

// DecibelsFromGain is the inverse, rounded to whole decibels — for a menu that
// has to mark which of its steps is the one in force.
func DecibelsFromGain(gain float64, off int) int {
	if gain <= 0 {
		return off
	}

	return int(math.Round(20 * math.Log10(gain)))
}
