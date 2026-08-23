package audio

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// Device is one input or output the machine offers.
//
// ID is the backend's own identifier rendered as hex, which is what a setting
// stores. It is stable for as long as the endpoint exists but says nothing when
// it does not, so a device is looked up by ID and then by Name — a headset
// re-enumerated under a new ID is still the one the reader picked.
type Device struct {
	ID   string
	Name string

	Default bool
}

// Inputs lists the microphones the machine offers, and Outputs the speakers.
// Both enumerate the backend, so neither belongs on the UI thread.
func Inputs() ([]Device, error) { return devices(malgo.Capture) }

// Outputs lists the playback devices.
func Outputs() ([]Device, error) { return devices(malgo.Playback) }

func devices(kind malgo.DeviceType) ([]Device, error) {
	ctx, err := context()
	if err != nil {
		return nil, err
	}

	found, err := ctx.Devices(kind)
	if err != nil {
		return nil, err
	}

	out := make([]Device, 0, len(found))
	for i := range found {
		out = append(out, Device{
			ID:      found[i].ID.String(),
			Name:    found[i].Name(),
			Default: found[i].IsDefault != 0,
		})
	}

	return out, nil
}

// deviceIDPointer resolves what a setting stored into the identifier the backend
// will take. An empty id, an id nothing answers to and a device that has since
// been unplugged all come back nil, which miniaudio reads as "the system
// default" — the fallback a reader whose headset is not plugged in wants.
//
// The pointer has to be C memory: malgo hands the config to ma_device_init by
// address, and a Go pointer inside a struct passed to C is what cgo's pointer
// rules forbid. malgo allocates it and frees nothing, so the answers are kept
// and handed back rather than allocated per open — a leak of one identifier per
// device the client has ever opened, rather than one per open.
var (
	deviceIDMu    sync.Mutex
	deviceIDCache = map[string]unsafe.Pointer{}
)

func deviceIDPointer(kind malgo.DeviceType, id string) unsafe.Pointer {
	if id == "" {
		return nil
	}

	deviceIDMu.Lock()
	defer deviceIDMu.Unlock()

	if cached, ok := deviceIDCache[id]; ok {
		return cached
	}

	ctx, err := context()
	if err != nil {
		return nil
	}

	found, err := ctx.Devices(kind)
	if err != nil {
		return nil
	}

	for i := range found {
		if found[i].ID.String() != id {
			continue
		}

		resolved := found[i].ID
		pointer := resolved.Pointer()
		deviceIDCache[id] = pointer

		return pointer
	}

	return nil
}

/* The process's one context */

// miniaudio's context owns the backend's own connection — an audio session on
// WASAPI, a client on PulseAudio — so there is one per process rather than one
// per Engine. It is never uninitialised: a client that has played a sound keeps
// the backend open until it exits, and tearing it down while a device callback
// is in flight is a use-after-free rather than a tidy-up.
var (
	contextOnce sync.Once
	contextPtr  *malgo.AllocatedContext
	contextErr  error
)

func context() (*malgo.AllocatedContext, error) {
	contextOnce.Do(func() {
		// No log proc. miniaudio's is unlevelled — malgo hands on a bare string —
		// and at its default it narrates every symbol it loads and every format it
		// negotiates, which is a page of log per device open. What actually matters
		// arrives as a returned error, and those are logged where they are handled.
		contextPtr, contextErr = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
		if contextErr != nil {
			contextErr = fmt.Errorf("audio backend: %w", contextErr)
		}
	})

	if contextErr != nil {
		return nil, contextErr
	}
	if contextPtr == nil {
		return nil, errors.New("audio backend unavailable")
	}

	return contextPtr, nil
}
