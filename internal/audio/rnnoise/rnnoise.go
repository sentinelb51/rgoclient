// Package rnnoise is Xiph's RNNoise speech denoiser, vendored as C and bound
// with cgo the way gopus vendors libopus. It removes background noise from
// voice — hiss, fans, hum, keyboard — where a gate can only silence the frames
// between words.
//
// The sources are xiph/rnnoise main at 70f1d256 (2025-02-22, the newest commit
// on either the GitLab original or the GitHub mirror) plus one marked patch —
// a gain floor under the band gains (rnnoise_set_gain_floor), which is what a
// suppression-strength dial is; every change carries an "rgoclient" comment so
// a future re-vendor knows what to carry. Re-vendor with
// scripts/update-rnnoise.sh. License: BSD-3 (COPYING).
//
// The weights are rnnoise_data.bin rather than C literals, because upstream
// emits them as a 74 MB source file that USE_WEIGHTS_FILE excludes from the
// build anyway. Embedded, so a fresh clone still builds with nothing
// downloaded.
//
// Two things a caller has to know. It costs **10 ms of delay**: from v0.2 on,
// the model gets a frame of lookahead and rnnoise_process_frame answers with
// the previous frame's audio, so a live path pays that on top of its own
// buffering — only while suppression is on, the stage being bypassed
// otherwise. And it needs AVX2 on amd64 to be cheap: see march_amd64.go, which
// asks for the same x86-64-v3 floor gopus already puts under this binary.
package rnnoise

/*
#cgo CFLAGS: -O3 -DUSE_WEIGHTS_FILE
#cgo !windows LDFLAGS: -lm
#include <stdlib.h>
#include "rnnoise.h"
*/
import "C"

import (
	_ "embed"
	"log"
	"runtime"
	"sync"
	"unsafe"
)

// FrameSize is how many samples one Process call rewrites: 10 ms at the 48 kHz
// the model is trained for, which is exactly half the 20 ms capture frame.
const FrameSize = 480

//go:embed rnnoise_data.bin
var weights []byte

// model is the trained weights, shared by every Denoiser. The bytes are copied
// into C memory rather than passed in place because rnnoise keeps pointers into
// the blob for as long as a state built from it lives, which cgo does not allow
// of Go memory; neither the copy nor the model is ever freed, both outliving
// every caller by construction.
var model = sync.OnceValue(func() *C.RNNModel {
	return C.rnnoise_model_from_buffer(C.CBytes(weights), C.int(len(weights)))
})

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
// prove no Process is in flight first. A Denoiser whose model would not load
// passes audio through untouched rather than reporting: suppression is a stage
// the chain can do without, and the alternative is a capture that fails to open.
func New() *Denoiser {
	d := &Denoiser{st: C.rnnoise_create(model())}
	if d.st == nil {
		log.Printf("rnnoise: built-in model did not load, suppression is off")

		return d
	}
	runtime.AddCleanup(d, func(st *C.DenoiseState) { C.rnnoise_destroy(st) }, d.st)

	return d
}

// SetGainFloor caps how far the denoiser may push any band down: 0 is full
// suppression (the default), 1 is passthrough, and a value between is a floor
// under the model's per-band gains — so 0.1 caps the noise reduction at 20 dB.
// Takes effect on the next Process, so it can move mid-stream; the flooring is
// spectral, which is why it costs no dry/wet delay line and no comb filtering.
func (d *Denoiser) SetGainFloor(floor float32) {
	if d.st == nil {
		return
	}
	C.rnnoise_set_gain_floor(d.st, C.float(floor))
	runtime.KeepAlive(d)
}

// Process denoises one frame in place and reports the model's own estimate that
// it held speech, 0-1. frame must hold exactly FrameSize samples. What comes
// back is the *previous* frame's audio: see the package comment.
func (d *Denoiser) Process(frame []float32) float32 {
	if d.st == nil || len(frame) != FrameSize {
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
