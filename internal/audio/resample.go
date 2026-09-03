package audio

import "math"

// lowpass is the one filter both directions of a 48 kHz <-> 16 kHz change
// run: a windowed sinc under a Kaiser window, designed once. Linear phase,
// deliberately — the band the model never sees is recovered by subtracting
// this path from the input delayed by exactly the filter's group delay, and
// a minimum-phase filter would leave low band behind in that difference.
//
// The pass band reaches 6.5 kHz and the stop band starts at 8 kHz, which is
// all a 16 kHz model can carry.
type lowpass struct {
	// split is the taps by phase for decimating: split[r][q] = h[3q+r], so
	// that out[m] = sum_r sum_q split[r][q] * x[3(m+q)+r] runs over the
	// input's three phases as three contiguous rows.
	split []float32 // [3][taps/3]

	// phases is the same for interpolating, gain 3 folded in and each phase
	// reversed to run over a window oldest first: y[3m+p] = sum_q
	// phases[p][q] * x[m+q].
	phases []float32 // [3][taps/3]
}

const (
	rate3Taps   = 90     // a multiple of 3; group delay 44.5 samples, 0.9 ms, each way
	rate3Cutoff = 7000.0 // Hz, the middle of the transition band
	rate3Beta   = 5.0    // Kaiser, about 55 dB of stop-band rejection
	rate3Phase  = rate3Taps / 3
)

func newLowpass() *lowpass {

	const N, M = rate3Taps, float64(rate3Taps-1) / 2
	taps := make([]float64, N)
	var sum float64
	for n := range N {
		x := float64(n) - M
		h := 2 * rate3Cutoff / sampleRate
		if x != 0 {
			h = math.Sin(2*math.Pi*rate3Cutoff/sampleRate*x) / (math.Pi * x)
		}
		h *= kaiser(rate3Beta, 2*float64(n)/float64(N-1)-1)
		taps[n] = h
		sum += h
	}
	l := &lowpass{split: make([]float32, N), phases: make([]float32, N)}
	for n := range taps {
		taps[n] /= sum
	}
	const H = rate3Phase
	for r := range 3 {
		for q := range H {
			l.split[r*H+q] = float32(taps[3*q+r])
			l.phases[r*H+q] = float32(3 * taps[3*(H-1-q)+r])
		}
	}

	return l
}

// kaiser is the window at position t in [-1, 1].
func kaiser(beta, t float64) float64 {
	return bessel0(beta*math.Sqrt(max(0, 1-t*t))) / bessel0(beta)
}

// bessel0 is the modified Bessel function of the first kind, order zero, by
// its series: every term is the last times (x/2k)^2.
func bessel0(x float64) float64 {

	sum, term := 1.0, 1.0
	for k := 1; k < 64; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-12*sum {
			break
		}
	}

	return sum
}

// decimator takes 48 kHz frames down to 16 kHz.
type decimator struct {
	f    *lowpass
	hist []float32 // the last taps-1 input samples
	buf  []float32 // them plus a frame
	rows []float32 // buf split by phase, [3][rowStride]
	rowN int
}

func newDecimator(f *lowpass, frame int) *decimator {

	n := rate3Taps - 1 + frame
	rowN := (n + 2) / 3
	rowN = (rowN + 7) / 8 * 8

	return &decimator{
		f:    f,
		hist: make([]float32, rate3Taps-1),
		buf:  make([]float32, n),
		rows: make([]float32, 3*rowN),
		rowN: rowN,
	}
}

func (d *decimator) reset() { clear(d.hist) }

// process decimates in into out: len(in) is 3*len(out).
func (d *decimator) process(in, out []float32) {

	buf := d.buf[:rate3Taps-1+len(in)]
	copy(buf, d.hist)
	copy(buf[rate3Taps-1:], in)
	rows, N := d.rows, d.rowN
	for j := 0; 3*j+2 < len(buf); j++ {
		rows[j] = buf[3*j]
		rows[N+j] = buf[3*j+1]
		rows[2*N+j] = buf[3*j+2]
	}
	fir(out, rows, d.f.split, 3, rate3Phase, N)
	copy(d.hist, buf[len(in):])
}

// interpolator takes 16 kHz frames up to 48 kHz.
type interpolator struct {
	f    *lowpass
	hist []float32 // the last taps/3-1 input samples
	buf  []float32 // them plus a frame
	tmp  []float32 // the three phases' outputs, [3][frame/3]
}

func newInterpolator(f *lowpass, frame int) *interpolator {
	return &interpolator{
		f:    f,
		hist: make([]float32, rate3Phase-1),
		buf:  make([]float32, rate3Phase-1+frame/3),
		tmp:  make([]float32, frame),
	}
}

func (u *interpolator) reset() { clear(u.hist) }

// process interpolates in into out: len(out) is 3*len(in).
func (u *interpolator) process(in, out []float32) {

	buf := u.buf[:rate3Phase-1+len(in)]
	copy(buf, u.hist)
	copy(buf[rate3Phase-1:], in)
	n := len(in)
	for p := range 3 {
		fir(u.tmp[p*n:(p+1)*n], buf, u.f.phases[p*rate3Phase:(p+1)*rate3Phase], 1, rate3Phase, 0)
	}
	t0, t1, t2 := u.tmp[:n], u.tmp[n:2*n], u.tmp[2*n:3*n]
	for m := range n {
		out[3*m] = t0[m]
		out[3*m+1] = t1[m]
		out[3*m+2] = t2[m]
	}
	copy(u.hist, buf[len(in):])
}
