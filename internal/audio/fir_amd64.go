package audio

// The AVX2 FIR, taken when the processor has AVX2 and FMA and the OS saves
// the upper halves of the vector registers — which on amd64 this binary
// already assumes for the denoiser's C, but a check costs nothing and the
// plain loop is there either way.

func init() {
	if detectAVX2() {
		fir = firAVX2
	}
}

func detectAVX2() bool {

	const (
		fma     = 1 << 12 // cpuid 1, ecx
		osxsave = 1 << 27
		avx     = 1 << 28
		avx2    = 1 << 5 // cpuid 7.0, ebx
		ymm     = 6      // xgetbv 0, eax: xmm and ymm state saved
	)
	maxLeaf, _, _, _ := cpuid(0, 0)
	if maxLeaf < 7 {
		return false
	}
	_, _, ecx, _ := cpuid(1, 0)
	if ecx&(fma|osxsave|avx) != fma|osxsave|avx {
		return false
	}
	_, ebx, _, _ := cpuid(7, 0)
	if ebx&avx2 == 0 {
		return false
	}
	eax, _ := xgetbv()

	return eax&ymm == ymm
}

//go:noescape
func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

//go:noescape
func xgetbv() (eax, edx uint32)

//go:noescape
func firAVX2(out, in, taps []float32, rows, ntaps, inStride int)
