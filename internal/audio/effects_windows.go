//go:build windows

package audio

import (
	"encoding/hex"
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

/* COM */

var (
	ole32            = syscall.NewLazyDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coUninitialize   = ole32.NewProc("CoUninitialize")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	coTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidMMDeviceEnumerator = guid{0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidMMDeviceEnumerator   = guid{0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidAudioClient          = guid{0x1CB9AD4C, 0xDBFA, 0x4C32, [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidAudioEffectsManager  = guid{0x4460B3AE, 0x4B44, 0x4527, [8]byte{0x86, 0x76, 0x75, 0x48, 0xA8, 0xAC, 0xD2, 0x60}}
)

// Vtable slots, counted from QueryInterface. Every one of these interfaces
// derives straight from IUnknown, so its own methods start at 3.
const (
	slotRelease = 2

	slotGetDefaultAudioEndpoint = 4 // IMMDeviceEnumerator
	slotGetDevice               = 5

	slotActivate = 3 // IMMDevice

	slotInitialize   = 3 // IAudioClient
	slotGetMixFormat = 8
	slotGetService   = 14

	slotGetAudioEffects = 5 // IAudioEffectsManager
)

const (
	sOK            = 0
	sFalse         = 1
	rpcChangedMode = 0x80010106

	coinitMultithreaded = 0
	clsctxAll           = 23

	eCapture = 1 // EDataFlow
	eConsole = 0 // ERole: the role miniaudio opens the default with, so it is the one probed

	shareModeShared = 0

	effectStateOn = 1

	// probeBuffer is the stream length asked for, in REFERENCE_TIME's 100ns units.
	probeBuffer = 100000 // 10ms
)

// comCall invokes one method of a COM object by its vtable slot, the object
// itself being the `this` every call takes first.
//
// An object is held as an unsafe.Pointer rather than a uintptr for as long as it
// is live: a uintptr is a number the collector does not follow, and converting
// one back is the misuse vet is right to name.
func comCall(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtable := *(*unsafe.Pointer)(obj)
	method := *(*uintptr)(unsafe.Add(vtable, uintptr(slot)*unsafe.Sizeof(uintptr(0))))

	ret, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(obj)}, args...)...)

	return ret
}

func comRelease(obj unsafe.Pointer) {
	if obj != nil {
		comCall(obj, slotRelease)
	}
}

/* The probe */

// audioEffect is Win32's AUDIO_EFFECT (audioclient.h): a GUID naming the effect,
// whether this stream may change it, and whether it is on.
type audioEffect struct {
	ID          guid
	CanSetState int32
	State       int32
}

// effectKinds maps an effect to what it is. Every effect ksmedia.h defines
// shares one GUID but for the low byte of its first field, so the table is keyed
// by that byte and the rest of the GUID is checked once, in effectKind.
var effectKinds = map[byte]EffectKind{
	0xbe: EffectEchoCancellation,
	0xbf: EffectNoiseSuppression,
	0xc0: EffectGainControl,
	0xc1: EffectBeamforming,
	0xc2: EffectToneRemoval,
	0xcf: EffectBeamforming,          // far-field, which is the same thing to a reader
	0xd0: EffectDeepNoiseSuppression, // Voice Focus, the one a Copilot+ machine runs
}

// effectFamily is those GUIDs with the byte that tells them apart cleared.
var effectFamily = guid{0x6f64ad00, 0x8211, 0x11e2, [8]byte{0x8c, 0x70, 0x2c, 0x27, 0xd7, 0xf0, 0x01, 0xfa}}

func effectKind(id guid) EffectKind {
	family := id
	family.Data1 &^= 0xff

	if family != effectFamily {
		return EffectOther
	}

	kind, ok := effectKinds[byte(id.Data1)]
	if !ok {
		return EffectOther
	}

	return kind
}

// inputEffects asks Windows what it is already doing to a microphone.
//
// The list hangs off a stream rather than off the endpoint, so one has to be
// opened to ask: IAudioClient::GetService refuses an uninitialised client. It is
// initialised and never started, which is what keeps this from holding the
// microphone or lighting the recording indicator, and what makes it safe to run
// beside the capture it is being asked about.
//
// Before Windows 11 build 22000 there is no IAudioEffectsManager and GetService
// answers E_NOINTERFACE, which comes back as an error rather than as an empty
// list: nothing was learned, which is not the same as nothing being applied.
func inputEffects(id string) ([]Effect, error) {
	// Every pointer below belongs to the thread that initialised COM.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	owned, err := comInitialize()
	if err != nil {
		return nil, err
	}
	if owned {
		defer coUninitialize.Call()
	}

	device, err := openEndpoint(id)
	if err != nil {
		return nil, err
	}
	defer comRelease(device)

	client, err := openClient(device)
	if err != nil {
		return nil, err
	}
	defer comRelease(client)

	var manager unsafe.Pointer

	hr := comCall(client, slotGetService,
		uintptr(unsafe.Pointer(&iidAudioEffectsManager)),
		uintptr(unsafe.Pointer(&manager)),
	)
	if uint32(hr) != sOK {
		return nil, fmt.Errorf("audio: no effects manager for this input: %#x", uint32(hr))
	}
	defer comRelease(manager)

	var (
		effects unsafe.Pointer
		count   uint32
	)

	hr = comCall(manager, slotGetAudioEffects,
		uintptr(unsafe.Pointer(&effects)),
		uintptr(unsafe.Pointer(&count)),
	)
	if uint32(hr) != sOK {
		return nil, fmt.Errorf("audio: GetAudioEffects: %#x", uint32(hr))
	}
	if effects == nil {
		return nil, nil
	}
	defer coTaskMemFree.Call(uintptr(effects))

	var active []Effect

	for _, effect := range unsafe.Slice((*audioEffect)(effects), int(count)) {
		if effect.State != effectStateOn {
			continue
		}

		active = append(active, Effect{
			Kind:      effectKind(effect.ID),
			Removable: effect.CanSetState != 0,
		})
	}

	return active, nil
}

// comInitialize puts this thread in the multithreaded apartment and reports
// whether it is the caller's to undo — a thread somebody else initialised stays
// theirs, and uninitialising it from here would pull its objects out from under
// them.
func comInitialize() (bool, error) {
	hr, _, _ := coInitializeEx.Call(0, coinitMultithreaded)

	switch uint32(hr) {
	case sOK, sFalse:
		return true, nil

	case rpcChangedMode:
		return false, nil
	}

	return false, fmt.Errorf("audio: CoInitializeEx: %#x", uint32(hr))
}

// openEndpoint resolves what a setting stored, falling back to the default
// capture device — the same one miniaudio would have opened, which is why the
// role asked for is eConsole rather than the communications role this being a
// call client would otherwise suggest.
func openEndpoint(id string) (unsafe.Pointer, error) {
	var enumerator unsafe.Pointer

	hr, _, _ := coCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)),
		0,
		clsctxAll,
		uintptr(unsafe.Pointer(&iidMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	)
	if uint32(hr) != sOK {
		return nil, fmt.Errorf("audio: MMDeviceEnumerator: %#x", uint32(hr))
	}
	defer comRelease(enumerator)

	var device unsafe.Pointer

	if endpoint, ok := endpointID(id); ok {
		if wide, err := syscall.UTF16PtrFromString(endpoint); err == nil {
			comCall(enumerator, slotGetDevice,
				uintptr(unsafe.Pointer(wide)),
				uintptr(unsafe.Pointer(&device)),
			)
			runtime.KeepAlive(wide)
		}
	}

	// A device that has since been unplugged falls through to the default, which
	// is what capture itself does with the same setting.
	if device == nil {
		hr = comCall(enumerator, slotGetDefaultAudioEndpoint,
			eCapture,
			eConsole,
			uintptr(unsafe.Pointer(&device)),
		)
		if uint32(hr) != sOK || device == nil {
			return nil, fmt.Errorf("audio: no default input: %#x", uint32(hr))
		}
	}

	return device, nil
}

// openClient activates a stream on the endpoint and initialises it, that being
// the least that makes GetService answer. Nothing is ever read from it and it is
// never started.
func openClient(device unsafe.Pointer) (unsafe.Pointer, error) {
	var client unsafe.Pointer

	hr := comCall(device, slotActivate,
		uintptr(unsafe.Pointer(&iidAudioClient)),
		clsctxAll,
		0,
		uintptr(unsafe.Pointer(&client)),
	)
	if uint32(hr) != sOK {
		return nil, fmt.Errorf("audio: activate input: %#x", uint32(hr))
	}

	var format unsafe.Pointer

	hr = comCall(client, slotGetMixFormat, uintptr(unsafe.Pointer(&format)))
	if uint32(hr) != sOK {
		comRelease(client)

		return nil, fmt.Errorf("audio: GetMixFormat: %#x", uint32(hr))
	}
	defer coTaskMemFree.Call(uintptr(format))

	// The buffer duration must not be zero. Zero is documented as "let the engine
	// choose" and is what every other client passes, but on a capture endpoint it
	// does not return at all — it wedges the audio service rather than failing —
	// so a real length is asked for. Any is fine: nothing is ever read out of it.
	//
	// The two durations are REFERENCE_TIME, 100ns units and 64-bit, which is one
	// uintptr each only because nothing here is built for 32-bit Windows.
	hr = comCall(client, slotInitialize, shareModeShared, 0, probeBuffer, 0, uintptr(format), 0)
	if uint32(hr) != sOK {
		comRelease(client)

		return nil, fmt.Errorf("audio: initialise input: %#x", uint32(hr))
	}

	return client, nil
}

// endpointID turns what a setting stored back into the string WASAPI knows the
// endpoint by.
//
// A Device.ID is malgo's ma_device_id hex-encoded with trailing zero bytes
// stripped, and for WASAPI that union arm is the endpoint's own UTF-16 string —
// so the last character's high byte is among what was stripped, and the odd
// length it leaves has to be padded back before the pairs decode.
func endpointID(id string) (string, bool) {
	if id == "" {
		return "", false
	}

	raw, err := hex.DecodeString(id)
	if err != nil {
		return "", false
	}
	if len(raw)%2 != 0 {
		raw = append(raw, 0)
	}

	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		unit := uint16(raw[i]) | uint16(raw[i+1])<<8
		if unit == 0 {
			break
		}

		units = append(units, unit)
	}
	if len(units) == 0 {
		return "", false
	}

	return string(utf16.Decode(units)), true
}
