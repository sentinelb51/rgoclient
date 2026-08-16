package audio

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// wav builds a RIFF file around a data chunk, with a junk chunk in front of the
// format one so the chunk walk is exercised rather than an assumed layout.
func wav(format, channels, rate, bits int, payload []byte) []byte {
	fmtChunk := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtChunk[0:], uint16(format))
	binary.LittleEndian.PutUint16(fmtChunk[2:], uint16(channels))
	binary.LittleEndian.PutUint32(fmtChunk[4:], uint32(rate))
	binary.LittleEndian.PutUint32(fmtChunk[8:], uint32(rate*channels*bits/8))
	binary.LittleEndian.PutUint16(fmtChunk[12:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(fmtChunk[14:], uint16(bits))

	chunk := func(id string, body []byte) []byte {
		out := make([]byte, 8, 8+len(body)+len(body)&1)
		copy(out, id)
		binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
		out = append(out, body...)
		if len(body)&1 == 1 {
			out = append(out, 0) // chunks are padded to an even length
		}

		return out
	}

	body := []byte("WAVE")
	body = append(body, chunk("LIST", []byte("INFOhi"))...)
	body = append(body, chunk("fmt ", fmtChunk)...)
	body = append(body, chunk("data", payload)...)

	out := make([]byte, 8)
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))

	return append(out, body...)
}

// write puts a file in the test's own directory and hands back its path.
func write(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// samples reads a decoded take back as the left channel's floats, which is what
// every assertion below is about.
func samples(take []byte) []float32 {
	out := make([]float32, len(take)/bytesPerFrame)
	for i := range out {
		out[i] = float32(int16(binary.LittleEndian.Uint16(take[i*bytesPerFrame:]))) / 32768
	}

	return out
}

func TestDecodeWAVWidths(t *testing.T) {
	// The same three samples written at every width the decoder claims to read.
	// Full scale, silence, and half scale negative — the last being the one that
	// catches a sign extension that was not done.
	cases := []struct {
		name    string
		format  int
		bits    int
		payload []byte
	}{
		{"8-bit", 1, 8, []byte{0xFF, 0x80, 0x40}},
		{"16-bit", 1, 16, []byte{0xFF, 0x7F, 0x00, 0x00, 0x00, 0xC0}},
		{"24-bit", 1, 24, []byte{0xFF, 0xFF, 0x7F, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0}},
		{"32-bit", 1, 32, []byte{0xFF, 0xFF, 0xFF, 0x7F, 0, 0, 0, 0, 0x00, 0x00, 0x00, 0xC0}},
		{"float32", 3, 32, floats(1, 0, -0.5)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, "sound.wav", wav(tc.format, 1, sampleRate, tc.bits, tc.payload))

			sound, err := Decode(path)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			got := samples(sound.takes[0])
			if len(got) != 3 {
				t.Fatalf("got %d frames, want 3", len(got))
			}

			// 8-bit has a coarser scale than the rest, so the tolerance is a step of it.
			const tolerance = 0.01
			for i, want := range []float32{1, 0, -0.5} {
				if math.Abs(float64(got[i]-want)) > tolerance {
					t.Errorf("sample %d is %.4f, want %.4f", i, got[i], want)
				}
			}
		})
	}
}

// floats writes samples as IEEE 32-bit, the one WAVE encoding that is not an
// integer.
func floats(values ...float32) []byte {
	out := make([]byte, len(values)*4)
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}

	return out
}

func TestDecodeWAVMonoBecomesStereo(t *testing.T) {
	path := write(t, "mono.wav", wav(1, 1, sampleRate, 16, []byte{0x00, 0x40, 0x00, 0xC0}))

	sound, err := Decode(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	take := sound.takes[0]
	if len(take) != 2*bytesPerFrame {
		t.Fatalf("got %d bytes, want %d — a mono file must come back as two channels",
			len(take), 2*bytesPerFrame)
	}

	for frame := range 2 {
		left := int16(binary.LittleEndian.Uint16(take[frame*bytesPerFrame:]))
		right := int16(binary.LittleEndian.Uint16(take[frame*bytesPerFrame+2:]))
		if left != right {
			t.Errorf("frame %d is %d/%d — mono must be duplicated, not left in one channel", frame, left, right)
		}
	}
}

func TestDecodeWAVResamples(t *testing.T) {
	// A quarter of the device's rate, so the decoded length must be four times the
	// source's — the whole point being that a file recorded at any rate plays at the
	// right speed rather than at the ratio between the two.
	const rate = sampleRate / 4

	payload := make([]byte, 100*2)
	path := write(t, "slow.wav", wav(1, 1, rate, 16, payload))

	sound, err := Decode(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	frames := len(sound.takes[0]) / bytesPerFrame
	if frames != 400 {
		t.Errorf("got %d frames, want 400", frames)
	}
}

func TestDecodeWAVFoldsSurroundToStereo(t *testing.T) {
	// Six channels, one frame: every channel at a different level. Folding averages
	// them rather than taking the first two, so nothing mixed to the rear is lost.
	path := write(t, "surround.wav", wav(3, 6, sampleRate, 32, floats(0.6, 0.6, 0.6, 0.6, 0.6, 0.6)))

	sound, err := Decode(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := samples(sound.takes[0])
	if len(got) != 1 {
		t.Fatalf("got %d frames, want 1", len(got))
	}
	if math.Abs(float64(got[0]-0.6)) > 0.001 {
		t.Errorf("folded to %.4f, want 0.6", got[0])
	}
}

func TestDecodeWAVClampsRatherThanWraps(t *testing.T) {
	// A file mastered past full scale. Wrapping would turn the loudest sample into
	// the quietest, which is a crack where a clamp is a flat top nobody hears.
	path := write(t, "hot.wav", wav(3, 1, sampleRate, 32, floats(1.8, -1.8)))

	sound, err := Decode(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := samples(sound.takes[0])
	if got[0] < 0.99 || got[1] > -0.99 {
		t.Errorf("clamped to %.4f/%.4f, want about 1 and -1", got[0], got[1])
	}
}

func TestDecodeWAVExtensibleReadsTheRealFormat(t *testing.T) {
	// WAVE_FORMAT_EXTENSIBLE says nothing itself: the format is the first two bytes
	// of the GUID after the channel mask. Read as 0xFFFE the file is refused, and
	// anything a modern encoder writes is extensible.
	fmtChunk := make([]byte, 40)
	binary.LittleEndian.PutUint16(fmtChunk[0:], 0xFFFE)
	binary.LittleEndian.PutUint16(fmtChunk[2:], 1)
	binary.LittleEndian.PutUint32(fmtChunk[4:], sampleRate)
	binary.LittleEndian.PutUint16(fmtChunk[14:], 16)
	binary.LittleEndian.PutUint16(fmtChunk[24:], 1) // the GUID's format word: plain PCM

	payload := []byte{0x00, 0x40}

	body := []byte("WAVE")
	for _, part := range []struct {
		id   string
		body []byte
	}{{"fmt ", fmtChunk}, {"data", payload}} {
		head := make([]byte, 8)
		copy(head, part.id)
		binary.LittleEndian.PutUint32(head[4:], uint32(len(part.body)))
		body = append(body, head...)
		body = append(body, part.body...)
	}

	file := make([]byte, 8)
	copy(file, "RIFF")
	binary.LittleEndian.PutUint32(file[4:], uint32(len(body)))
	path := write(t, "extensible.wav", append(file, body...))

	sound, err := Decode(path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := samples(sound.takes[0])
	if len(got) != 1 || math.Abs(float64(got[0]-0.5)) > 0.001 {
		t.Errorf("got %v, want one sample of about 0.5", got)
	}
}

func TestDecodeRefusesWhatItCannotRead(t *testing.T) {
	// A compressed WAVE is refused by name rather than played as noise.
	path := write(t, "adpcm.wav", wav(2, 1, sampleRate, 4, []byte{0x11, 0x22}))

	if _, err := Decode(path); err == nil {
		t.Fatal("decoded an ADPCM file, want a refusal")
	}
}

func TestDecodeMissingFile(t *testing.T) {
	if _, err := Decode(filepath.Join(t.TempDir(), "nothing.wav")); err == nil {
		t.Fatal("decoded a file that does not exist")
	}
}
