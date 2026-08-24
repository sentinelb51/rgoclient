// Package cpu decides which of the machine's logical processors the client runs
// on.
//
// Two machines have more than one kind of core in a way a program can read
// rather than guess. Intel's hybrid parts split into performance and efficiency
// cores and say so per logical processor. An AMD part whose cores sit behind
// exactly two L3 caches is a pair of chiplets, reported by the machine's own
// numbering — CCD0 and CCD1 — the only name that holds across the range: which
// chiplet carries the better bins is CPPC's preferred-core ranking, which
// Windows offers no user-mode read for. The vendor gate keeps the name honest:
// two L3 domains on anything but an AMD part — a second socket, a hypervisor's
// invented topology — are not chiplets.
//
// Nothing here imports anything else of ours. What the two kinds are *for* is a
// policy, and it lives with the setting that names it.
package cpu

import (
	"errors"
	"runtime"
	"sync"
)

// Topology is the machine's logical processors sorted into the kinds a setting
// can name. The indices are the platform's own numbering, an affinity mask being
// built straight out of them.
type Topology struct {
	// All is every logical processor the process may use, as it stood at startup.
	// An affinity set from outside — `start /affinity`, taskset, a container — is
	// the ceiling rather than something to widen past: it is a decision already
	// made by somebody.
	All []int

	// Performance and Efficiency are Intel's hybrid split, where the efficiency
	// cores trade speed for power and the platform says which is which.
	Performance []int
	Efficiency  []int

	// CCD0 and CCD1 are AMD's chiplets: the machine's two L3 domains in its own
	// order, CCD0 holding the lowest-numbered processor. The index is the whole of
	// the name — which chiplet carries the stacked cache or the better bins is the
	// part's own business, read nowhere here.
	CCD0 []int
	CCD1 []int
}

// Split reports whether the machine has more than one kind of core. Without one
// there is nothing to choose between, which is what keeps the setting off a page
// where every one of its values would pick the same processors.
func (t Topology) Split() bool {

	return (len(t.Performance) > 0 && len(t.Efficiency) > 0) || (len(t.CCD0) > 0 && len(t.CCD1) > 0)
}

var (
	detectOnce sync.Once
	detected   Topology
)

// Detect reports the machine's cores. The answer cannot change while the process
// runs — the baseline is read once, before anything has moved it — so it is read
// once and kept.
func Detect() Topology {
	detectOnce.Do(func() { detected = detect() })

	return detected
}

// ErrNoCores is what Pin answers rather than pinning to nothing, an empty mask
// being a process that can never be scheduled.
var ErrNoCores = errors.New("cpu: no logical processors to pin to")

// Pin restricts the process to cores and matches GOMAXPROCS to how many that is.
//
// The runtime counted its processors once, at startup, and does not hear about
// this. Leaving GOMAXPROCS where it was would schedule that many goroutines onto
// however few cores are left, which is the one way this could cost more than it
// saves.
func Pin(cores []int) error {

	if len(cores) == 0 {
		return ErrNoCores
	}

	if err := pin(cores); err != nil {
		return err
	}

	runtime.GOMAXPROCS(len(cores))

	return nil
}
