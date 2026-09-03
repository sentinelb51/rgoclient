package audio

// fir is the one loop the resampler is made of: out[m] = sum over rows r and
// taps k of taps[r][k] * in[r][m+k], with taps [rows][ntaps] and in a row
// every inStride samples, each row at least len(out)+ntaps-1 long. One row
// is a plain FIR over a window; three rows are a polyphase decimator, the
// input split by phase so every load is contiguous. A function variable so
// amd64 can install the AVX2 version at init.
var fir = firGeneric

func firGeneric(out, in, taps []float32, rows, ntaps, inStride int) {

	for m := range out {
		var s float32
		for r := range rows {
			t := taps[r*ntaps : (r+1)*ntaps]
			x := in[r*inStride+m:][:ntaps]
			for k, v := range t {
				s += v * x[k]
			}
		}
		out[m] = s
	}
}
