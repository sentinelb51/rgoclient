package audio

import (
	"math"
	"math/rand/v2"
)

// The built-in sounds are synthesised rather than shipped as files. That is not
// cleverness for its own sake: a sound is a licence and a binary each, a missing
// file is a state every caller would have to handle, and a client with no assets
// at all still has a full set to hear before anybody chooses their own. A custom
// file *replaces* one of these, so nothing is ever unset.
//
// Everything here is rendered at the device's rate and returned as the same
// interleaved stream a decoded file becomes.

// typingTakes is how many renders of one keystroke click are kept. Six, picked
// against a random rotation rather than a cycle: four repeats the previous take
// a quarter of the time, which is often enough to be heard as a loop. The whole
// typing set is still well under a megabyte.
const typingTakes = 6

// builtin synthesises the default for a key, the keystrokes from the named
// board. An unrecognised key answers with the plain message blip rather than
// silence — a sound that cannot be heard is indistinguishable from a setting
// that did not take — and an unrecognised board with the default one.
func builtin(key, profile string) *Sound {
	switch key {
	case Mention:
		return single(chord(0.45, 0.17, 0.50, 880, 1320))
	case Direct:
		return single(join(tone(0.11, 659.25, 0.07, 0.42), tone(0.16, 987.77, 0.09, 0.42)))
	case Message:
		return single(tone(0.16, 523.25, 0.06, 0.34))
	case Ambient:
		return single(tone(0.10, 440, 0.04, 0.20))
	case Send:
		return single(sweep(0.09, 380, 720, 0.05, 0.26))
	case Friend:
		return single(join(
			tone(0.09, 587.33, 0.06, 0.40),
			tone(0.09, 739.99, 0.06, 0.40),
			tone(0.18, 987.77, 0.10, 0.40),
		))
	case Reaction:
		return single(tone(0.07, 1174.66, 0.03, 0.30))
	case Error:
		// Two lows a semitone apart: the beating between them is what makes this read
		// as wrong without it having to be loud.
		return single(chord(0.34, 0.15, 0.38, 196, 207.65))
	case Offline:
		return single(sweep(0.24, 660, 330, 0.12, 0.34))
	case Online:
		return single(sweep(0.24, 330, 660, 0.12, 0.34))

	case KeyPress, KeySpace, KeyBackspace:
		spec, shape := profiles[ResolveTypingProfile(profile)], keyShapes[key]
		return takes(func(r *rand.Rand) []float32 { return keystroke(r, spec, shape) })
	case KeyEnter:
		// The one keystroke that finishes something, so it keeps a short pitched tail
		// the others have none of. It belongs to the *client* rather than to the board,
		// so every profile carries it.
		spec, shape := profiles[ResolveTypingProfile(profile)], keyShapes[key]
		return takes(func(r *rand.Rand) []float32 {
			return mix(keystroke(r, spec, shape), tone(0.09, 1108.73, 0.035, 0.16))
		})
	}

	return single(tone(0.16, 523.25, 0.06, 0.34))
}

/* Rendering */

// single wraps one mono render as a sound with one take.
func single(mono []float32) *Sound {
	return &Sound{takes: [][]byte{encode(mono, 1, sampleRate)}}
}

// takes renders the same recipe several times from different noise, for the
// sounds that repeat often enough for one render to be recognisable.
func takes(render func(*rand.Rand) []float32) *Sound {
	sound := &Sound{takes: make([][]byte, typingTakes)}

	for i := range sound.takes {
		// Seeded rather than global, so a click sounds the same from run to run and a
		// report of one being wrong is reproducible.
		r := rand.New(rand.NewPCG(uint64(i)+1, 0x9E3779B97F4A7C15))
		sound.takes[i] = encode(render(r), 1, sampleRate)
	}

	return sound
}

// frames is how many samples a duration is at the device's rate.
func frames(seconds float64) int { return int(seconds * sampleRate) }

// envelope is the shape every sound here is drawn through: a short attack so it
// does not start on a click of its own, then an exponential decay. tau is the
// time the level falls to about a third, which is what makes a sound read as
// short or as ringing.
func envelope(i, count int, tau float64) float32 {
	t := float64(i) / sampleRate

	const attack = 0.004
	level := math.Exp(-t/tau) * min(t/attack, 1)

	// The last few samples are taken to zero whatever the decay had left, a buffer
	// ending mid-cycle being a click at the end of every play.
	if tail := count - i; tail < frames(0.002) {
		level *= float64(tail) / float64(frames(0.002))
	}

	return float32(level)
}

// tone is one sine under that envelope.
func tone(seconds, freq, tau, gain float64) []float32 {
	return chord(seconds, tau, gain, freq)
}

// chord is several sines summed, scaled by how many there are so two partials
// are not twice as loud as one.
func chord(seconds, tau, gain float64, freqs ...float64) []float32 {
	count := frames(seconds)
	out := make([]float32, count)
	level := gain / float64(len(freqs))

	for i := range out {
		t := float64(i) / sampleRate

		var sum float64
		for _, freq := range freqs {
			sum += math.Sin(2 * math.Pi * freq * t)
		}

		out[i] = float32(sum*level) * envelope(i, count, tau)
	}

	return out
}

// sweep is a sine whose frequency moves from one pitch to another. The phase is
// accumulated rather than computed from the instantaneous frequency: multiplying
// a moving frequency by t sweeps at twice the rate and lands on the wrong note.
func sweep(seconds, from, to, tau, gain float64) []float32 {
	count := frames(seconds)
	out := make([]float32, count)

	var phase float64
	for i := range out {
		freq := from + (to-from)*float64(i)/float64(count)
		phase += 2 * math.Pi * freq / sampleRate

		out[i] = float32(math.Sin(phase)*gain) * envelope(i, count, tau)
	}

	return out
}

/* Keystrokes */

// A keystroke is not one impact but two a few milliseconds apart: the switch's
// own click, which is where the crispness is — a narrow band around 2-5 kHz, the
// range the ear is sharpest in — and the keycap bottoming out against the plate,
// which is lower, later, quieter, and what stops the click reading as a hiss with
// nothing behind it.
//
// Both halves are noise through a *resonator* rather than a low-pass. A one-pole
// low-pass can only ever be duller: it keeps every frequency under its cutoff, so
// the broadband rumble that reads as static survives whatever the cutoff is set
// to. Resonance is what puts a pitch on an impact, and a pitched impact is what a
// typist hears as a board being clicky rather than mushy.
// impact is one of those collisions. The first in a board is always the switch
// itself and is what the whole keystroke is normalised against; the rest are
// placed behind it and scaled to it.
type impact struct {
	freq  float64 // where its resonance sits
	q     float64 // how narrow: past about 4 it stops being noise and starts being a ping
	tau   float64 // how fast it goes
	delay float64 // how far behind the first impact it lands
	gain  float64 // its peak against the first impact's, which is always 1

	// A body ringing under it, for the impact that is a case rather than a strike.
	// Without these the bottom-out is only more noise, and a board with no pitch in
	// it is the one that reads as a hiss.
	modes [2]float64
	ring  float64 // how much of it is that ring rather than noise
}

// clickSpec is a whole keystroke: its impacts, its peak and how long it runs.
type clickSpec struct {
	impacts []impact
	gain    float64
	length  float64
}

// The boards. Each is written as its *ordinary* key; the other three are that
// one struck elsewhere, which is what keyShapes says.
//
// The gain of a bottom-out is the number to distrust when one of these stops
// sounding right. At the switch's own height it is mush — the first render of
// this peaked on the bottom-out and read as a thud with a tick in front of it.
// Much under a quarter and there is no board behind the click at all. Every one
// of these was rendered and measured before it was settled on.
var profiles = map[string]clickSpec{
	// Tactile: a brown, near enough. One clean strike with the case under it, and
	// the middle ground the other three are departures from.
	ProfileTactile: {
		impacts: []impact{
			{freq: 4200, q: 2.6, tau: 0.0015, gain: 1},
			{freq: 1600, q: 1.4, tau: 0.0060, delay: 0.0028, gain: 0.42,
				modes: [2]float64{620, 1040}, ring: 0.30},
		},
		gain: 0.42, length: 0.036,
	},

	// Clicky: a blue. The click jacket is a second strike of its own, brighter and
	// shorter than the switch under it and very nearly a pitch — that separate,
	// high, sub-millisecond snap is the whole of what "clicky" means, and no amount
	// of brightening a single impact produces it. The bottom-out is kept light on
	// purpose: a board you can hear the click bar on is not one that thuds.
	ProfileClicky: {
		impacts: []impact{
			{freq: 3200, q: 3.4, tau: 0.0011, gain: 1},
			{freq: 7400, q: 7.0, tau: 0.0006, delay: 0.0003, gain: 1},
			{freq: 2000, q: 1.6, tau: 0.0045, delay: 0.0030, gain: 0.26,
				modes: [2]float64{760, 1240}, ring: 0.22},
		},
		gain: 0.44, length: 0.032,
	},

	// Thocky: a gasket board full of foam. The strike is dull and the case is most
	// of what is heard, which is the one place a low, loud, slow bottom-out is the
	// point rather than the failure above.
	ProfileThocky: {
		impacts: []impact{
			{freq: 2400, q: 1.8, tau: 0.0018, gain: 1},
			{freq: 700, q: 1.2, tau: 0.0140, delay: 0.0040, gain: 0.74,
				modes: [2]float64{300, 470}, ring: 0.55},
		},
		gain: 0.46, length: 0.050,
	},

	// Typewriter: three impacts, because that is what it is — the key, the type bar
	// reaching the platen a moment later, and the frame taking it. The long low
	// ring is the only one here that outlasts the keystroke that made it.
	ProfileTypewriter: {
		impacts: []impact{
			{freq: 5200, q: 4.5, tau: 0.0010, gain: 1},
			{freq: 2600, q: 3.0, tau: 0.0035, delay: 0.0013, gain: 0.62},
			{freq: 900, q: 1.1, tau: 0.0180, delay: 0.0055, gain: 0.44,
				modes: [2]float64{180, 320}, ring: 0.50},
		},
		gain: 0.46, length: 0.062,
	},
}

// keyShape is how one of the four keys departs from whatever board is chosen. A
// real board is one case struck in four places rather than four sounds: space is
// the stabilised bar, lower and longer; backspace is a small key struck lightly,
// so it is thinner, quicker and quieter; enter is the one with somewhere to go,
// so it keeps the most bottom-out.
//
// Written as multipliers rather than as four more tables, so a board is tuned in
// one place and the other three keys follow it.
type keyShape struct {
	pitch  float64 // scales every resonance, the modes included
	length float64 // scales every decay, every delay and the whole render
	body   float64 // scales everything behind the first impact
	gain   float64
}

var keyShapes = map[string]keyShape{
	KeyPress:     {pitch: 1.00, length: 1.00, body: 1.00, gain: 1.00},
	KeySpace:     {pitch: 0.68, length: 1.45, body: 1.20, gain: 1.09},
	KeyBackspace: {pitch: 1.22, length: 0.78, body: 0.72, gain: 0.86},
	KeyEnter:     {pitch: 0.86, length: 1.28, body: 1.08, gain: 1.05},
}

// keystroke renders one press of one key on one board. There is no attack ramp
// anywhere in it — an impact starts on its first sample, and the step at the
// front is the broadband transient the whole thing is heard by.
func keystroke(r *rand.Rand, spec clickSpec, shape keyShape) []float32 {
	count := frames(spec.length * shape.length)
	out := make([]float64, count)

	for n, hit := range spec.impacts {
		// Each impact is rendered apart and brought to its own height before it is
		// summed in, rather than the sum being normalised once at the end. A
		// resonator's output level moves with its Q, so without this, retuning the
		// bottom-out would quietly change how loud the switch is.
		skip := min(frames(hit.delay*shape.length), count)

		gain := hit.gain
		if n > 0 {
			gain *= shape.body
		}

		part := impactRender(r, hit, shape, count-skip)
		scale(part, gain)

		for i, sample := range part {
			out[skip+i] += sample
		}
	}

	return finish(out, spec.gain*shape.gain)
}

// impactRender is one collision: noise through its resonator, decaying, with
// whatever body rings under it. The modes are cosines rather than sines — a
// struck body is displaced at the instant it is hit, so it starts at full swing.
// Started from zero it would fade *in*, which is the one thing an impact never
// does.
func impactRender(r *rand.Rand, hit impact, shape keyShape, count int) []float64 {
	out := make([]float64, max(count, 0))
	filter := newBandpass(hit.freq*shape.pitch*jitter(r, 0.05), hit.q)

	var modes [len(hit.modes)]float64
	for k, freq := range hit.modes {
		modes[k] = freq * shape.pitch * jitter(r, 0.03)
	}

	tau := hit.tau * shape.length

	for i := range out {
		t := float64(i) / sampleRate

		sample := filter.step(r.Float64()*2 - 1)
		for _, freq := range modes {
			if freq > 0 {
				sample += hit.ring * math.Cos(2*math.Pi*freq*t) / float64(len(modes))
			}
		}

		out[i] = sample * math.Exp(-t/tau)
	}

	return out
}

// scale brings a render's loudest sample to peak, in place.
func scale(samples []float64, peak float64) {
	var loudest float64
	for _, sample := range samples {
		loudest = max(loudest, math.Abs(sample))
	}

	if loudest == 0 {
		return
	}

	level := peak / loudest
	for i := range samples {
		samples[i] *= level
	}
}

// bandpass is one biquad section, the resonance every impact here is coloured by.
// Constant peak gain, so Q changes how narrow the band is without also changing
// how loud what comes out of it is. b1 is zero for this shape, which is why only
// x2 is fed forward.
type bandpass struct {
	b0, b2, a1, a2 float64
	x1, x2, y1, y2 float64
}

func newBandpass(freq, q float64) *bandpass {
	w := 2 * math.Pi * freq / sampleRate
	alpha := math.Sin(w) / (2 * q)
	a0 := 1 + alpha

	return &bandpass{
		b0: alpha / a0,
		b2: -alpha / a0,
		a1: -2 * math.Cos(w) / a0,
		a2: (1 - alpha) / a0,
	}
}

func (f *bandpass) step(x float64) float64 {
	y := f.b0*x + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2

	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y

	return y
}

// jitter is a small multiplier around one, for the parts of a click that must
// not be identical between takes. A resonator moved a few per cent is heard as
// another key on the same board; moved much further, as another board.
func jitter(r *rand.Rand, spread float64) float64 {
	return 1 + (r.Float64()*2-1)*spread
}

// finish brings a whole keystroke to its gain and takes its end to zero, a
// buffer ending mid-cycle being a click at the end of every play.
func finish(samples []float64, gain float64) []float32 {
	scale(samples, gain)

	out := make([]float32, len(samples))
	taper := frames(0.002)

	for i, sample := range samples {
		if tail := len(samples) - i; tail < taper {
			sample *= float64(tail) / float64(taper)
		}

		out[i] = float32(sample)
	}

	return out
}

/* Assembly */

// join concatenates renders, for a sound that is more than one note.
func join(parts ...[]float32) []float32 {
	var total int
	for _, part := range parts {
		total += len(part)
	}

	out := make([]float32, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}

	return out
}

// mix sums renders over one another, the longer one deciding the length.
func mix(parts ...[]float32) []float32 {
	var longest int
	for _, part := range parts {
		longest = max(longest, len(part))
	}

	out := make([]float32, longest)
	for _, part := range parts {
		for i, sample := range part {
			out[i] += sample
		}
	}

	return out
}
