//go:build windows

package audio

import (
	"fmt"
	"log"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

/* Win32 */

var (
	mmdevapi                   = syscall.NewLazyDLL("mmdevapi.dll")
	activateAudioInterfaceAsyc = mmdevapi.NewProc("ActivateAudioInterfaceAsync")

	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	createEventW           = kernel32.NewProc("CreateEventW")
	setEvent               = kernel32.NewProc("SetEvent")
	closeHandle            = kernel32.NewProc("CloseHandle")
	waitForMultipleObjects = kernel32.NewProc("WaitForMultipleObjects")
	getCurrentProcessID    = kernel32.NewProc("GetCurrentProcessId")

	avrt                            = syscall.NewLazyDLL("avrt.dll")
	avSetMmThreadCharacteristicsW   = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	avRevertMmThreadCharacteristics = avrt.NewProc("AvRevertMmThreadCharacteristics")
)

var (
	iidUnknown            = guid{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidAudioCaptureClient = guid{0xC8ADBD64, 0xE71E, 0x48A0, [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
	iidActivateHandler    = guid{0x41D949AB, 0x9862, 0x444A, [8]byte{0x80, 0xF6, 0xC2, 0x61, 0x33, 0x4D, 0xA5, 0xEB}}

	// IAgileObject is a marker with no methods of its own. Answering to it is
	// what the activation means by an "agile" handler: Windows calls back on an
	// MTA worker of its own choosing, and refusing the interface makes it
	// marshal instead — through an apartment this process never pumps.
	iidAgileObject = guid{0x94EA2B94, 0xE9CC, 0x49E0, [8]byte{0xC0, 0xFF, 0xEE, 0x64, 0xCA, 0x8F, 0x5B, 0x90}}
)

// Vtable slots, counted from QueryInterface, as in effects_windows.go.
const (
	slotQueryInterface = 0

	slotStart          = 10 // IAudioClient, continuing that file's list
	slotStopClient     = 11
	slotSetEventHandle = 13

	slotCaptureGetBuffer     = 3 // IAudioCaptureClient
	slotCaptureReleaseBuffer = 4
	slotCaptureNextPacket    = 5

	slotGetActivateResult = 3 // IActivateAudioInterfaceAsyncOperation
)

const (
	eRender = 0 // EDataFlow; effects_windows.go holds eCapture

	eNoInterface = 0x80004002

	vtBlob = 65 // VARENUM's VT_BLOB

	streamFlagsLoopback      = 0x00020000
	streamFlagsEventCallback = 0x00040000

	bufferFlagsSilent = 0x2

	activationTypeProcessLoopback = 1
	loopbackModeInclude           = 0
	loopbackModeExclude           = 1

	waveFormatPCM   = 1
	waveFormatFloat = 3

	// loopbackBuffer is the stream length asked for, in REFERENCE_TIME's 100ns
	// units. 20 ms is one Opus frame: deep enough that a late drain loses
	// nothing, shallow enough that it is not itself the latency.
	loopbackBuffer = 200000

	// pollInterval is how often the loop looks at a stream it is not being
	// woken for, in milliseconds — the device-loopback path, where WASAPI's
	// event is documented as event-driven capture but in practice stops firing
	// on an endpoint nothing is rendering to.
	pollInterval = 10

	waitTimeout = 0x00000102

	// processLoopbackDevice is VIRTUAL_AUDIO_DEVICE_PROCESS_LOOPBACK: not a
	// device anything enumerates, but the well-known path that turns
	// ActivateAudioInterfaceAsync into a per-process capture.
	processLoopbackDevice = "VAD\\Process_Loopback"
)

// waveFormatEx is Win32's WAVEFORMATEX. Packed exactly as the header declares
// it — 18 bytes, cbSize included — because the audio engine reads it by offset.
type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

// activationParams is AUDIOCLIENT_ACTIVATION_PARAMS with its union flattened to
// the one arm that exists: the process-loopback one.
type activationParams struct {
	ActivationType uint32
	TargetProcess  uint32
	LoopbackMode   uint32
}

// propVariant is PROPVARIANT holding a VT_BLOB, which is the only shape
// ActivateAudioInterfaceAsync takes its parameters in. The explicit pad is the
// union's 8-byte alignment on x64, and the whole is 24 bytes; getting either
// wrong hands the engine a garbage length.
type propVariant struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	BlobSize  uint32
	_         uint32
	BlobData  uintptr
}

/* The completion handler */

// activateVtbl is the COM vtable of our IActivateAudioInterfaceCompletionHandler.
// One instance, package-level, shared by every handler: it is read-only and its
// address must outlive any activation in flight.
type activateVtbl struct {
	QueryInterface    uintptr
	AddRef            uintptr
	Release           uintptr
	ActivateCompleted uintptr
}

// activateHandler is a COM object implemented in Go. vtbl is first because a
// COM object *is* a pointer to its vtable — everything else here is ours and
// Windows never looks at it.
//
// Reference counting is a no-op: the object is kept alive by the registry below
// for exactly as long as the activation it belongs to, which outlives every
// call Windows can make on it.
type activateHandler struct {
	vtbl *activateVtbl

	done chan struct{}
	once sync.Once
}

var (
	activateHandlerVtbl *activateVtbl

	// handlers keeps every live handler reachable from Go, keyed by the pointer
	// Windows calls back with. A callback arrives on a thread the runtime does
	// not own, so it cannot hold a Go pointer of its own to start from.
	handlersMu sync.Mutex
	handlers   = map[uintptr]*activateHandler{}
)

func init() {
	activateHandlerVtbl = &activateVtbl{
		// The two pointers are typed rather than taken as uintptr: they are C's,
		// so converting them back would be the misuse vet is right to name.
		QueryInterface: syscall.NewCallback(func(this uintptr, riid *guid, ppv *uintptr) uintptr {
			if ppv == nil || riid == nil {
				return eNoInterface
			}

			if *riid != iidUnknown && *riid != iidActivateHandler && *riid != iidAgileObject {
				*ppv = 0

				return eNoInterface
			}

			*ppv = this

			return sOK
		}),

		AddRef:  syscall.NewCallback(func(uintptr) uintptr { return 1 }),
		Release: syscall.NewCallback(func(uintptr) uintptr { return 1 }),

		// The result is read by whoever is waiting, off the operation it already
		// holds, so this only has to say that there is one.
		ActivateCompleted: syscall.NewCallback(func(this, _ uintptr) uintptr {
			handlersMu.Lock()
			handler := handlers[this]
			handlersMu.Unlock()

			if handler != nil {
				handler.once.Do(func() { close(handler.done) })
			}

			return sOK
		}),
	}
}

// newActivateHandler returns a handler and the function that retires it.
func newActivateHandler() (*activateHandler, func()) {
	handler := &activateHandler{vtbl: activateHandlerVtbl, done: make(chan struct{})}

	// Windows holds this pointer across the activation, so the collector is told
	// not to move it. Go's heap does not compact today; the pin is what keeps
	// that from being something this file quietly depends on.
	var pinner runtime.Pinner
	pinner.Pin(handler)

	key := uintptr(unsafe.Pointer(handler))

	handlersMu.Lock()
	handlers[key] = handler
	handlersMu.Unlock()

	return handler, func() {
		handlersMu.Lock()
		delete(handlers, key)
		handlersMu.Unlock()

		pinner.Unpin()
	}
}

/* The capture */

// wasapiLoopback is one open loopback stream. Everything COM lives on the one
// thread run() locks, so nothing here is touched from two places: Close only
// sets the stop event.
type wasapiLoopback struct {
	stop    uintptr // manual-reset event, set by Close
	stopped atomic.Bool
	ended   chan struct{}
}

// openLoopback starts capturing what the machine plays. The open is
// synchronous — the caller gets a working stream or the reason it is not one —
// while the capture itself runs on the thread it is set up on, that thread
// being the one every COM pointer below belongs to.
func openLoopback(cfg LoopbackConfig, sink *Loopback) (loopbackStream, LoopbackFormat, error) {
	stop, _, err := createEventW.Call(0, 1, 0, 0) // manual-reset, unsignalled
	if stop == 0 {
		return nil, LoopbackFormat{}, fmt.Errorf("audio: loopback stop event: %w", err)
	}

	s := &wasapiLoopback{stop: stop, ended: make(chan struct{})}

	ready := make(chan loopbackOpened, 1)
	go s.run(cfg, sink, ready)

	opened := <-ready
	if opened.err != nil {
		closeHandle.Call(stop)

		return nil, LoopbackFormat{}, opened.err
	}

	return s, opened.format, nil
}

// loopbackOpened is what the capture thread reports back once: the format it
// settled on, or why there is no capture.
type loopbackOpened struct {
	format LoopbackFormat
	err    error
}

// Close stops the capture and waits for the thread to let go of its COM
// pointers, so a caller that opens another straight away cannot race the old
// one's teardown.
func (s *wasapiLoopback) Close() error {
	if s.stopped.Swap(true) {
		return nil
	}

	setEvent.Call(s.stop)
	<-s.ended
	closeHandle.Call(s.stop)

	return nil
}

// run owns the stream for its whole life: the apartment, every COM pointer, the
// event and the loop. It reports once through ready — either the reason there
// is no capture, or nil — and everything after that is the loop's.
func (s *wasapiLoopback) run(cfg LoopbackConfig, sink *Loopback, ready chan<- loopbackOpened) {
	defer close(s.ended)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	owned, err := comInitialize()
	if err != nil {
		ready <- loopbackOpened{err: err}

		return
	}
	if owned {
		defer coUninitialize.Call()
	}

	client, useEvent, format, err := openLoopbackStream(cfg)
	if err != nil {
		ready <- loopbackOpened{err: err}

		return
	}
	defer comRelease(client)

	var wake uintptr

	if useEvent {
		wake, _, err = createEventW.Call(0, 0, 0, 0) // auto-reset
		if wake == 0 {
			ready <- loopbackOpened{err: fmt.Errorf("audio: loopback event: %w", err)}

			return
		}
		defer closeHandle.Call(wake)

		if hr := comCall(client, slotSetEventHandle, wake); uint32(hr) != sOK {
			ready <- loopbackOpened{err: fmt.Errorf("audio: loopback event handle: %#x", uint32(hr))}

			return
		}
	}

	var capture unsafe.Pointer

	hr := comCall(client, slotGetService,
		uintptr(unsafe.Pointer(&iidAudioCaptureClient)), uintptr(unsafe.Pointer(&capture)))
	if uint32(hr) != sOK {
		ready <- loopbackOpened{err: fmt.Errorf("audio: loopback capture client: %#x", uint32(hr))}

		return
	}
	defer comRelease(capture)

	if hr := comCall(client, slotStart); uint32(hr) != sOK {
		ready <- loopbackOpened{err: fmt.Errorf("audio: start loopback: %#x", uint32(hr))}

		return
	}
	defer comCall(client, slotStopClient)

	ready <- loopbackOpened{format: format}

	// MMCSS is what keeps the drain ahead of a 20 ms buffer while the machine is
	// busy doing the thing being shared. Best-effort: a machine that refuses
	// still captures, it is only less certain about when.
	if handle, _, _ := avSetMmThreadCharacteristicsW.Call(
		uintptr(unsafe.Pointer(proAudio)), uintptr(unsafe.Pointer(new(uint32)))); handle != 0 {
		defer avRevertMmThreadCharacteristics.Call(handle)
	}

	s.drain(capture, sink, format, wake)
}

var proAudio, _ = syscall.UTF16PtrFromString("Pro Audio")

// drain is the loop. It wakes on the engine's event where there is one and on a
// short timeout where there is not, and each time takes every packet waiting —
// a wake is a signal that there is work, never a count of it.
func (s *wasapiLoopback) drain(capture unsafe.Pointer, sink *Loopback, format LoopbackFormat, wake uintptr) {
	waits := []uintptr{s.stop}
	if wake != 0 {
		waits = append(waits, wake)
	}

	// scrap is the float conversion's landing place, grown to whatever the
	// largest packet turns out to be and reused after that.
	var scrap []int16

	for {
		result, _, _ := waitForMultipleObjects.Call(
			uintptr(len(waits)), uintptr(unsafe.Pointer(&waits[0])), 0, pollInterval)

		// Index 0 is the stop event; a timeout is the polling path's normal wake.
		if result == 0 {
			return
		}
		if result != waitTimeout && result != 1 {
			log.Printf("loopback wait: %#x", uint32(result))

			return
		}

		for {
			var packet uint32

			if hr := comCall(capture, slotCaptureNextPacket,
				uintptr(unsafe.Pointer(&packet))); uint32(hr) != sOK {
				return
			}
			if packet == 0 {
				break
			}

			var (
				data   unsafe.Pointer
				frames uint32
				flags  uint32
			)

			hr := comCall(capture, slotCaptureGetBuffer,
				uintptr(unsafe.Pointer(&data)),
				uintptr(unsafe.Pointer(&frames)),
				uintptr(unsafe.Pointer(&flags)),
				0, 0,
			)
			if uint32(hr) != sOK {
				return
			}

			samples := int(frames) * format.Channels

			switch {
			case samples == 0:

			case flags&bufferFlagsSilent != 0:
				// A silent packet's buffer is not required to hold zeros, so the
				// silence is written rather than copied.
				scrap = grow(scrap, samples)
				clear(scrap[:samples])
				sink.push(scrap[:samples])

			case format.BitDepth == 16:
				sink.push(unsafe.Slice((*int16)(data), samples))

			default:
				scrap = grow(scrap, samples)
				floats := unsafe.Slice((*float32)(data), samples)

				for i, v := range floats {
					scrap[i] = clampToPCM(v)
				}
				sink.push(scrap[:samples])
			}

			comCall(capture, slotCaptureReleaseBuffer, uintptr(frames))
		}
	}
}

// openLoopbackStream activates a client, initialises it, and answers what it
// actually opened at along with whether an event can drive it.
//
// **Process loopback is the path for both cases**, not only for a named window.
// It is the one that takes the format it is given — the engine converts into the
// virtual device — where a plain device loopback is a tap on the endpoint and
// runs at whatever the engine happens to mix at, refusing anything else with
// AUDCLNT_E_UNSUPPORTED_FORMAT. A machine mixing at 44.1 kHz therefore cannot
// serve a device loopback to Opus at all without a resampler, and this whole
// path exists to have none.
//
// So the whole machine is captured as *everything but this client*, which is
// better than the device tap in the way that matters most: sharing a screen
// while in a call would otherwise send the other participants' voices back into
// the room, on top of themselves.
//
// The device tap stays as the fallback for Windows before build 20348, where
// there is no process loopback at all. There it takes the engine's own rate,
// which is usable exactly when that rate is one Opus encodes natively.
func openLoopbackStream(cfg LoopbackConfig) (unsafe.Pointer, bool, LoopbackFormat, error) {
	pid, exclude := cfg.ProcessID, cfg.Exclude
	if pid == 0 {
		pid, exclude = currentProcessID(), true
	}

	client, err := processLoopbackClient(pid, exclude)
	if err == nil {
		hr := initialiseLoopback(client, cfg.Format, true)
		if hr == sOK {
			return client, true, cfg.Format, nil
		}

		comRelease(client)
		err = fmt.Errorf("process loopback refused %s: %#x", cfg.Format, hr)
	}

	client, mix, deviceErr := defaultRenderClient()
	if deviceErr != nil {
		return nil, false, LoopbackFormat{}, fmt.Errorf("audio: %w (and %w)", deviceErr, err)
	}

	format, formatErr := deviceLoopbackFormat(cfg.Format, mix)
	if formatErr != nil {
		comRelease(client)

		return nil, false, LoopbackFormat{}, fmt.Errorf("audio: %w (and %w)", formatErr, err)
	}

	if hr := initialiseLoopback(client, format, false); hr != sOK {
		comRelease(client)

		return nil, false, LoopbackFormat{}, fmt.Errorf(
			"audio: the default output refused %s: %#x (and %w)", format, hr, err)
	}

	// Polled rather than event-driven: an endpoint nothing is rendering to stops
	// signalling, and a share would go silent for as long as the machine did.
	return client, false, format, nil
}

// initialiseLoopback is IAudioClient::Initialize as every path here calls it.
func initialiseLoopback(client unsafe.Pointer, f LoopbackFormat, event bool) uint32 {
	wave := waveFormat(f)

	flags := uintptr(streamFlagsLoopback)
	if event {
		flags |= streamFlagsEventCallback
	}

	return uint32(comCall(client, slotInitialize,
		shareModeShared, flags, loopbackBuffer, 0, uintptr(unsafe.Pointer(&wave)), 0))
}

// deviceLoopbackFormat is what the endpoint tap can actually be asked for. The
// engine converts depth and channels for a shared-mode client and does not
// convert the rate, so the rate is the engine's — usable only where that is one
// Opus takes, since resampling it here is the thing this path exists without.
func deviceLoopbackFormat(want LoopbackFormat, mix waveFormatEx) (LoopbackFormat, error) {
	got := want
	got.SampleRate = int(mix.SamplesPerSec)

	if !slices.Contains(LoopbackRates, got.SampleRate) {
		return LoopbackFormat{}, fmt.Errorf(
			"this machine mixes at %d Hz, which Opus does not encode natively; "+
				"set the output device to 48000 Hz, or update Windows for per-process capture",
			got.SampleRate)
	}

	return got, nil
}

// currentProcessID is this client, which the whole-machine capture excludes.
func currentProcessID() uint32 {
	pid, _, _ := getCurrentProcessID.Call()

	return uint32(pid)
}

// processLoopbackClient activates the per-process capture. Windows 10 build
// 20348 is where this arrived; anything older answers with an error here rather
// than being probed for beforehand.
func processLoopbackClient(pid uint32, exclude bool) (unsafe.Pointer, error) {
	mode := uint32(loopbackModeInclude)
	if exclude {
		mode = loopbackModeExclude
	}

	params := activationParams{
		ActivationType: activationTypeProcessLoopback,
		TargetProcess:  pid,
		LoopbackMode:   mode,
	}

	blob := propVariant{
		VT:       vtBlob,
		BlobSize: uint32(unsafe.Sizeof(params)),
		BlobData: uintptr(unsafe.Pointer(&params)),
	}

	device, err := syscall.UTF16PtrFromString(processLoopbackDevice)
	if err != nil {
		return nil, err
	}

	handler, retire := newActivateHandler()
	defer retire()

	var operation unsafe.Pointer

	hr, _, _ := activateAudioInterfaceAsyc.Call(
		uintptr(unsafe.Pointer(device)),
		uintptr(unsafe.Pointer(&iidAudioClient)),
		uintptr(unsafe.Pointer(&blob)),
		uintptr(unsafe.Pointer(handler)),
		uintptr(unsafe.Pointer(&operation)),
	)
	runtime.KeepAlive(device)
	runtime.KeepAlive(&params)

	if uint32(hr) != sOK {
		return nil, fmt.Errorf("audio: activate process loopback: %#x", uint32(hr))
	}
	defer comRelease(operation)

	<-handler.done

	var (
		result  uint32
		unknown unsafe.Pointer
	)

	hr = comCall(operation, slotGetActivateResult,
		uintptr(unsafe.Pointer(&result)), uintptr(unsafe.Pointer(&unknown)))
	if uint32(hr) != sOK {
		return nil, fmt.Errorf("audio: process loopback result: %#x", uint32(hr))
	}
	if result != sOK {
		if unknown != nil {
			comRelease(unknown)
		}

		return nil, fmt.Errorf("audio: process %d cannot be captured: %#x", pid, result)
	}
	defer comRelease(unknown)

	var client unsafe.Pointer

	hr = comCall(unknown, slotQueryInterface,
		uintptr(unsafe.Pointer(&iidAudioClient)), uintptr(unsafe.Pointer(&client)))
	if uint32(hr) != sOK || client == nil {
		return nil, fmt.Errorf("audio: process loopback client: %#x", uint32(hr))
	}

	return client, nil
}

// defaultRenderClient activates a plain IAudioClient on the default output,
// which initialised with the loopback flag captures the whole mix, and reads
// back the format the engine is mixing at — the only format such a tap can be
// opened with.
func defaultRenderClient() (unsafe.Pointer, waveFormatEx, error) {
	var enumerator unsafe.Pointer

	hr, _, _ := coCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)),
		0,
		clsctxAll,
		uintptr(unsafe.Pointer(&iidMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	)
	if uint32(hr) != sOK {
		return nil, waveFormatEx{}, fmt.Errorf("MMDeviceEnumerator: %#x", uint32(hr))
	}
	defer comRelease(enumerator)

	var device unsafe.Pointer

	if hr := comCall(enumerator, slotGetDefaultAudioEndpoint,
		eRender, eConsole, uintptr(unsafe.Pointer(&device))); uint32(hr) != sOK || device == nil {
		return nil, waveFormatEx{}, fmt.Errorf("no default output to capture: %#x", uint32(hr))
	}
	defer comRelease(device)

	var client unsafe.Pointer

	if hr := comCall(device, slotActivate,
		uintptr(unsafe.Pointer(&iidAudioClient)), clsctxAll, 0,
		uintptr(unsafe.Pointer(&client))); uint32(hr) != sOK {
		return nil, waveFormatEx{}, fmt.Errorf("activate output: %#x", uint32(hr))
	}

	// The mix format's own header is longer than WAVEFORMATEX where it is
	// extensible, which it almost always is; only the common prefix is read, and
	// the rate in it is what the tap is fixed at.
	var mix unsafe.Pointer

	if hr := comCall(client, slotGetMixFormat, uintptr(unsafe.Pointer(&mix))); uint32(hr) != sOK || mix == nil {
		comRelease(client)

		return nil, waveFormatEx{}, fmt.Errorf("GetMixFormat: %#x", uint32(hr))
	}

	format := *(*waveFormatEx)(mix)
	coTaskMemFree.Call(uintptr(mix))

	return client, format, nil
}

// waveFormat spells a LoopbackFormat the way the audio engine reads one.
func waveFormat(f LoopbackFormat) waveFormatEx {
	tag := uint16(waveFormatPCM)
	if f.BitDepth == 32 {
		tag = waveFormatFloat
	}

	block := uint16(f.bytesPerFrame())

	return waveFormatEx{
		FormatTag:      tag,
		Channels:       uint16(f.Channels),
		SamplesPerSec:  uint32(f.SampleRate),
		AvgBytesPerSec: uint32(f.SampleRate) * uint32(block),
		BlockAlign:     block,
		BitsPerSample:  uint16(f.BitDepth),
	}
}

// grow returns a buffer of at least n, reusing what it was given.
func grow(buf []int16, n int) []int16 {
	if cap(buf) >= n {
		return buf[:cap(buf)]
	}

	return make([]int16, n)
}

// clampToPCM converts one float sample, rounding rather than truncating and
// holding the two ends: the engine's float mix is not bounded to ±1 and a wrap
// is a click where a clip is not.
func clampToPCM(v float32) int16 {
	switch {
	case v >= 1:
		return 32767
	case v <= -1:
		return -32768
	}

	if v < 0 {
		return int16(v*32768 - 0.5)
	}

	return int16(v*32767 + 0.5)
}

// loopbackAvailable is what LoopbackAvailable answers.
// WASAPI has both: the whole mix off the default output, and one process
// through the virtual device.
const loopbackAvailable = true
