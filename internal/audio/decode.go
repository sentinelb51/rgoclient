package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/hajimehoshi/go-mp3"
)

// What a sound file is allowed to be. These are not tuning: a key click is
// milliseconds and a ping is under a second, so anything past them is somebody
// having pointed the setting at an album, which would be decoded into memory and
// then played over every keystroke.
const (
	maxSoundBytes   = 16 << 20 // on disk, before decoding
	maxSoundSeconds = 30       // after, whatever it decoded to
)

// Decode reads a sound file and converts it to the device's own format. WAV and
// MP3 are what is supported — see docs/known-gaps.md — and the kind is read off
// the content rather than the extension, a file being renamed being the ordinary
// way this goes wrong.
func Decode(path string) (*Sound, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSoundBytes {
		return nil, fmt.Errorf("sound file is %d MiB, over the %d MiB limit", info.Size()>>20, maxSoundBytes>>20)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var (
		samples  []float32
		channels int
		rate     int
	)

	switch {
	case bytes.HasPrefix(data, []byte("RIFF")):
		samples, channels, rate, err = decodeWAV(data)
	default:
		samples, channels, rate, err = decodeMP3(data)
	}
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 || channels == 0 || rate == 0 {
		return nil, errors.New("sound file holds no audio")
	}

	return &Sound{takes: [][]byte{encode(samples, channels, rate)}}, nil
}

/* WAV */

// decodeWAV reads a RIFF/WAVE file, handing back interleaved samples with the
// channel count and rate they were written at.
func decodeWAV(data []byte) ([]float32, int, int, error) {
	if len(data) < 12 || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return nil, 0, 0, errors.New("not a WAVE file")
	}

	var (
		format, bits, channels int
		rate                   int
		payload                []byte
	)

	// Chunks are walked rather than assumed in order: a file written by anything
	// but the simplest encoder carries LIST, fact or cue chunks between the two
	// that matter, and each is padded to an even length.
	for offset := 12; offset+8 <= len(data); {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4:]))
		body := data[offset+8:]
		if size > len(body) {
			size = len(body) // a truncated file: play what is there rather than refusing it
		}
		body = body[:size]

		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, 0, errors.New("WAVE format chunk is too short")
			}

			format = int(binary.LittleEndian.Uint16(body[0:]))
			channels = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))

			// WAVE_FORMAT_EXTENSIBLE says nothing itself; the real format is the first
			// two bytes of the GUID that follows the channel mask.
			if format == 0xFFFE && len(body) >= 26 {
				format = int(binary.LittleEndian.Uint16(body[24:]))
			}
		case "data":
			payload = body
		}

		offset += 8 + size + size&1
	}

	if payload == nil {
		return nil, 0, 0, errors.New("WAVE file holds no data chunk")
	}

	samples, err := wavSamples(payload, format, bits)
	if err != nil {
		return nil, 0, 0, err
	}

	return samples, channels, rate, nil
}

// wavSamples converts a data chunk to floats. Only the encodings something is
// likely to hand a desktop client are read; anything compressed is refused by
// name rather than played as noise.
func wavSamples(payload []byte, format, bits int) ([]float32, error) {
	switch {
	case format == 1 && bits == 8: // unsigned, unlike every wider PCM width
		samples := make([]float32, len(payload))
		for i, b := range payload {
			samples[i] = (float32(b) - 128) / 128
		}

		return samples, nil

	case format == 1 && bits == 16:
		samples := make([]float32, len(payload)/2)
		for i := range samples {
			samples[i] = float32(int16(binary.LittleEndian.Uint16(payload[i*2:]))) / 32768
		}

		return samples, nil

	case format == 1 && bits == 24:
		samples := make([]float32, len(payload)/3)
		for i := range samples {
			b := payload[i*3:]
			v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
			if v&0x800000 != 0 {
				v |= ^0xFFFFFF // sign-extend the 24th bit into the top byte
			}
			samples[i] = float32(v) / 8388608
		}

		return samples, nil

	case format == 1 && bits == 32:
		samples := make([]float32, len(payload)/4)
		for i := range samples {
			samples[i] = float32(int32(binary.LittleEndian.Uint32(payload[i*4:]))) / 2147483648
		}

		return samples, nil

	case format == 3 && bits == 32:
		samples := make([]float32, len(payload)/4)
		for i := range samples {
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[i*4:]))
		}

		return samples, nil

	case format == 3 && bits == 64:
		samples := make([]float32, len(payload)/8)
		for i := range samples {
			samples[i] = float32(math.Float64frombits(binary.LittleEndian.Uint64(payload[i*8:])))
		}

		return samples, nil
	}

	return nil, fmt.Errorf("unsupported WAVE encoding (format %d, %d-bit)", format, bits)
}

/* MP3 */

// decodeMP3 reads an MP3, which go-mp3 always hands back as 16-bit stereo
// whatever the source was — so the channel count is fixed rather than read.
func decodeMP3(data []byte) ([]float32, int, int, error) {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}

	// Bounded by what the engine will keep anyway, so a file that decodes far
	// larger than it looked on disk is cut rather than held.
	limit := maxSoundSeconds * decoder.SampleRate() * 2 * 2
	pcm := make([]byte, 0, min(int(max(decoder.Length(), 0)), limit))
	buf := make([]byte, 32<<10)

	for len(pcm) < limit {
		n, err := decoder.Read(buf)
		pcm = append(pcm, buf[:n]...)
		if err != nil {
			break // io.EOF, or a truncated file — whatever decoded is what plays
		}
	}

	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
	}

	return samples, 2, decoder.SampleRate(), nil
}

/* Conversion to the device's format */

// encode folds decoded samples down to the device's channel count and rate and
// writes them as the interleaved 16-bit stream a player reads.
func encode(samples []float32, channels, rate int) []byte {
	stereo := fold(samples, channels)
	stereo = resample(stereo, rate, sampleRate)

	if frames := len(stereo) / channelCount; frames > maxSoundSeconds*sampleRate {
		stereo = stereo[:maxSoundSeconds*sampleRate*channelCount]
	}

	return pcm(stereo)
}

// fold makes any channel layout stereo. Mono is duplicated; anything wider than
// stereo is averaged down rather than having two of its channels picked, so a
// sound mixed for surround does not come back missing whatever was in the rest.
func fold(samples []float32, channels int) []float32 {
	switch {
	case channels <= 0:
		return nil
	case channels == channelCount:
		return samples
	}

	frames := len(samples) / channels
	stereo := make([]float32, frames*channelCount)

	for i := range frames {
		var sum float32
		for c := range channels {
			sum += samples[i*channels+c]
		}

		mean := sum / float32(channels)
		stereo[i*channelCount] = mean
		stereo[i*channelCount+1] = mean
	}

	return stereo
}

// resample moves interleaved stereo to the device's rate, interpolating between
// the two frames either side. Linear is enough for clips this short — the
// artefacts it leaves are above what a 25 ms click has any content at.
func resample(stereo []float32, from, to int) []float32 {
	if from == to || from <= 0 || len(stereo) == 0 {
		return stereo
	}

	frames := len(stereo) / channelCount
	want := int(float64(frames) * float64(to) / float64(from))
	if want <= 0 {
		return nil
	}

	out := make([]float32, want*channelCount)
	step := float64(from) / float64(to)

	for i := range want {
		at := float64(i) * step
		lower := int(at)
		fraction := float32(at - float64(lower))
		upper := min(lower+1, frames-1)

		for c := range channelCount {
			a := stereo[lower*channelCount+c]
			b := stereo[upper*channelCount+c]
			out[i*channelCount+c] = a + (b-a)*fraction
		}
	}

	return out
}

// pcm writes samples as signed 16-bit little endian, clamping rather than
// wrapping: a file mastered hot overflows, and a wrap is a crack where a clamp
// is a flat top nobody hears on a click.
func pcm(samples []float32) []byte {
	out := make([]byte, len(samples)*2)

	for i, sample := range samples {
		v := int32(max(min(sample, 1), -1) * 32767)
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(v)))
	}

	return out
}
