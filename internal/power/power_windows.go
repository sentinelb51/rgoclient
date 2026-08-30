//go:build windows

package power

import (
	"log"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// currentProcess is what GetCurrentProcess answers: the constant -1, not
// something to be looked up.
const currentProcess = ^uintptr(0)

// NewLazySystemDLL, as everywhere else here that loads by name: winmm is the
// one DLL this package names that is not already in every process, and the
// plain loader would look in the application directory first.
var (
	winmm       = windows.NewLazySystemDLL("winmm.dll")
	beginPeriod = winmm.NewProc("timeBeginPeriod")
	endPeriod   = winmm.NewProc("timeEndPeriod")

	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	setProcessInformation = kernel32.NewProc("SetProcessInformation")
)

// coarseTick is what a timed wait is rounded up to with nobody asking for
// better: 64 a second, which is the multimedia timer's idle resolution on every
// desktop Windows. Read as the threshold for *asking* rather than as a claim
// about the machine — a platform that was already finer than this costs one
// balanced pair of calls and nothing else.
//
// Since Windows 10 2004 the request is the calling process's alone, so this no
// longer holds the whole machine's clock fast the way it used to.
const coarseTick = 15625 * time.Microsecond

// period is the resolution asked for. One millisecond is the finest the
// multimedia timer expresses; a machine whose own minimum is coarser refuses
// rather than clamping, which is what unsupported records.
const period = 1

// processPowerThrottling is PROCESS_INFORMATION_CLASS's fourth member, and
// executionSpeed the one bit of PROCESS_POWER_THROTTLING_STATE asked for here.
//
// The state's other bit, IGNORE_TIMER_RESOLUTION, is deliberately left alone: it
// tells Windows to disregard a timer resolution this process asked for, which is
// something Precise has already given up by the time Throttle is reached. Asking
// for it as well would only add a bit that older builds reject the whole call
// over.
const (
	processPowerThrottling = 4
	throttlingVersion      = 1
	executionSpeed         = 0x1
)

// powerThrottlingState is PROCESS_POWER_THROTTLING_STATE. ControlMask names the
// policies being decided and StateMask which of them are on, so clearing one is
// the same call with the bit dropped from StateMask alone — not an absent
// ControlMask, which would mean "no opinion" and leave the last one standing.
type powerThrottlingState struct {
	Version     uint32
	ControlMask uint32
	StateMask   uint32
}

var (
	mu sync.Mutex

	// Each pair is what is currently applied and whether the platform has already
	// refused. A refusal is permanent — the call cannot start working later — so
	// it is recorded rather than retried on every focus change.
	precise, noPrecision  bool
	throttled, noThrottle bool
)

// set raises the process's timer resolution where want is finer than the tick
// would express, and drops it again otherwise.
func set(want time.Duration) {
	on := want > 0 && want < coarseTick

	mu.Lock()
	defer mu.Unlock()

	if on == precise || noPrecision {
		return
	}

	call := endPeriod
	if on {
		call = beginPeriod
	}

	// TIMERR_NOERROR is zero and anything else is a refusal. A refused begin is
	// recorded and never retried; a refused end still counts as dropped here —
	// leaving precise true would short-circuit every later call and hold the
	// outstanding begin for the life of the process, the exact leak the balance
	// rule above exists to prevent.
	if ret, _, _ := call.Call(period); ret != 0 {
		noPrecision = true
		if on {
			log.Printf("power: this machine will not hold a %dms timer resolution", period)
		} else {
			log.Printf("power: timeEndPeriod(%d) refused; leaving the timer as it stands", period)
		}
	}

	precise = on
}

// throttle turns execution-speed throttling on or off for the whole process.
func throttle(on bool) {
	mu.Lock()
	defer mu.Unlock()

	if on == throttled || noThrottle {
		return
	}

	if err := setProcessInformation.Find(); err != nil {
		noThrottle = true

		return // pre-Windows 8
	}

	state := powerThrottlingState{Version: throttlingVersion, ControlMask: executionSpeed}
	if on {
		state.StateMask = executionSpeed
	}

	ok, _, err := setProcessInformation.Call(currentProcess, processPowerThrottling,
		uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if ok == 0 {
		noThrottle = true
		log.Println("power: this Windows has no execution-speed throttling:", err)

		return
	}

	throttled = on
}
