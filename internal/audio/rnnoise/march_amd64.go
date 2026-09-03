//go:build cgo && !rnnoise_baseline

// The x86-64-v3 floor — AVX2 and FMA, which is every amd64 part from Haswell
// and Excavator (2013) onward.
//
// vec.h reaches vec_avx.h off the compiler's own __SSE2__ either way, but the
// width it works in comes from __AVX2__ and __FMA__, so without this the model
// runs 128 bits wide: one 10 ms frame costs ~194 µs instead of ~95, which is
// 1.9 % of a core rather than 0.95 %. That is the whole of the upgrade's
// headroom — the model it replaces cost 264 µs — so the flag is not a tuning
// pass, it is what makes a 33× larger network cheaper than the one before it.
//
// gopus already puts this exact floor under the binary for Deep PLC, so nothing
// new is ruled out here; `-tags rnnoise_baseline` is the matching escape, and
// below the floor the failure is SIGILL rather than a slow frame.
//
// This is the top of the ladder, not a rung short of it. vec_avx.h has a VNNI
// path above the AVX2 one, and taking it is a **15 % regression**: 107 µs a
// frame against 93 on a 13700HX, with -mno-avxvnni recovering every bit of it.
// gopus measured the same header the same way round on Zen 5, so that is both
// vendors' current silicon losing to the sequence the newer instruction was
// meant to replace. -march=native is therefore the wrong flag here as well as
// the unportable one.
//
// Not x86-64-v4 either: AVX-512 is absent from every Intel consumer part since
// Rocket Lake.

package rnnoise

// #cgo CFLAGS: -march=x86-64-v3
import "C"
