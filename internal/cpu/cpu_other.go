//go:build !windows && !linux

package cpu

import "errors"

// detect reports nothing here. macOS is the platform this is written for: it has
// no affinity API at all — thread_policy_set's affinity tag is a hint the
// scheduler is free to ignore and does ignore on Apple silicon — so there is no
// honest answer to give. An empty topology has no split, which is what keeps the
// setting off the page rather than offering one that would do nothing. See
// docs/known-gaps.md.
func detect() Topology {

	return Topology{}
}

func pin([]int) error {

	return errors.New("cpu: this platform cannot pin a process to cores")
}
