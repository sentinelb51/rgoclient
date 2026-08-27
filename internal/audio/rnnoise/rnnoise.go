// Package rnnoise is Xiph's RNNoise speech denoiser, vendored as C and bound
// with cgo the way gopus vendors libopus. It removes background noise from
// voice — hiss, fans, hum, keyboard — where a gate can only silence the frames
// between words.
//
// The sources are xiph/rnnoise v0.1.1 (commit 6cbfd53) plus one marked patch —
// a gain floor under the band gains (rnnoise_set_gain_floor), which is what a
// suppression-strength dial is; every change carries an "rgoclient" comment so
// a future re-vendor knows what to carry. That release is the last
// release whose model ships inside the tree: rnn_data.c carries the ~85 KB of
// trained weights, so a fresh clone builds with nothing downloaded. Later
// releases fetch a 30-78 MB model at build time, which is why this is not a
// newer one. That release also prefixed every non-API symbol with rnn_, so the
// CELT-derived FFT and pitch code in here cannot collide with the libopus that
// gopus links into the same binary. License: BSD-3 (COPYING).
package rnnoise

/*
#cgo CFLAGS: -O2
#cgo !windows LDFLAGS: -lm
#include "rnnoise.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// FrameSize is how many samples one Process call rewrites: 10 ms at the 48 kHz
// the model is trained for, which is exactly half the 20 ms capture frame.
const FrameSize = 480

// Denoiser is one denoising stream. State is per stream and per goroutine:
// nothing here locks, so a Denoiser must only ever be driven by one goroutine —
// the capture chain's, in practice.
type Denoiser struct {
	st *C.DenoiseState

	// buf is the scale bridge: the chain works in ±1 floats, the model in ±32768
	// ones (it casts 16-bit samples without normalising), so every frame crosses
	// through here scaled up and comes back scaled down.
	buf [FrameSize]C.float
}

// New returns a Denoiser on the built-in model. The C state is freed when the
// Denoiser is collected — a cleanup rather than a Close, so no caller has to
// prove no Process is in flight first.
func New() *Denoiser {
	d := &Denoiser{st: C.rnnoise_create(nil)}
	runtime.AddCleanup(d, func(st *C.DenoiseState) { C.rnnoise_destroy(st) }, d.st)

	return d
}

// SetGainFloor caps how far the denoiser may push any band down: 0 is full
// suppression (the default), 1 is passthrough, and a value between is a floor
// under the model's per-band gains — so 0.1 caps the noise reduction at 20 dB.
// Takes effect on the next Process, so it can move mid-stream; the flooring is
// spectral, which is why it costs no dry/wet delay line and no comb filtering.
func (d *Denoiser) SetGainFloor(floor float32) {
	C.rnnoise_set_gain_floor(d.st, C.float(floor))
	runtime.KeepAlive(d)
}

// Process denoises one frame in place and reports the model's own estimate that
// it held speech, 0-1. frame must hold exactly FrameSize samples.
func (d *Denoiser) Process(frame []float32) float32 {
	if len(frame) != FrameSize {
		return 0
	}

	for i, sample := range frame {
		d.buf[i] = C.float(sample * 32768)
	}

	p := (*C.float)(unsafe.Pointer(&d.buf[0]))
	vad := C.rnnoise_process_frame(d.st, p, p)

	for i := range frame {
		frame[i] = float32(d.buf[i]) / 32768
	}

	// The receiver must outlive the C call: without this the cleanup may free
	// d.st while rnnoise_process_frame is still inside it, d being dead to the
	// compiler once its fields are loaded.
	runtime.KeepAlive(d)

	return float32(vad)
}
