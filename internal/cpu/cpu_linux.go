//go:build linux

package cpu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// cpuSet is the kernel's cpu_set_t: a bitmap of 1024 logical processors, which
// is the ceiling the syscall itself is defined against.
type cpuSet [16]uint64

func (s *cpuSet) add(core int) {

	if core >= 0 && core < len(s)*64 {
		s[core/64] |= 1 << uint(core%64)
	}
}

func (s *cpuSet) cores() []int {
	var cores []int

	for word, bits := range s {
		for bit := range 64 {
			if bits&(1<<uint(bit)) != 0 {
				cores = append(cores, word*64+bit)
			}
		}
	}

	return cores
}

// sysfsCPU is where the kernel publishes the machine's topology. A path rather
// than a constant elsewhere because both halves of detection read from under it.
const sysfsCPU = "/sys/devices/system/cpu"

/* Detection */

// detect reads the machine's cores. As on Windows, every failure degrades to a
// topology with no split rather than to no topology at all — the baseline is
// what "all cores" restores to.
func detect() Topology {
	var set cpuSet

	if err := affinity(0, &set, false); err != nil {
		return Topology{}
	}

	topology := Topology{All: set.cores()}

	// Chiplets before efficiency classes, for the reason given in cpu_windows.go.
	if ccd0, ccd1, split := chiplets(topology.All); split {
		topology.CCD0, topology.CCD1 = ccd0, ccd1
		return topology
	}

	if performance, efficiency, split := hybrid(topology.All); split {
		topology.Performance, topology.Efficiency = performance, efficiency
	}

	return topology
}

// hybrid splits by the two performance-monitoring units a hybrid x86 part
// registers. `cpu_core` and `cpu_atom` each list the processors they cover, which
// is the kernel's own answer to which core is which and needs no guess about
// frequencies that boost.
func hybrid(all []int) (performance, efficiency []int, split bool) {
	fast := cpuList("/sys/devices/cpu_core/cpus")
	slow := cpuList("/sys/devices/cpu_atom/cpus")

	if len(fast) == 0 || len(slow) == 0 {
		return nil, nil, false
	}

	for _, core := range all {
		switch {
		case fast[core]:
			performance = append(performance, core)
		case slow[core]:
			efficiency = append(efficiency, core)
		default:
			return nil, nil, false // a core neither unit claims
		}
	}

	return performance, efficiency, len(performance) > 0 && len(efficiency) > 0
}

// chiplets splits by L3 domain, on the same rule as Windows: an AMD part whose
// cores sit behind exactly two level-3 caches is a CCD0 and a CCD1, in the
// order the machine numbers its processors. The vendor gate is not pedantry —
// two L3 domains on anything else (a second socket, a hypervisor's invented
// topology) are not chiplets — and a domain of one processor is likewise an
// invention, not a CCD.
func chiplets(all []int) (ccd0, ccd1 []int, split bool) {
	if !amd() {
		return nil, nil, false
	}

	var keys []string
	groups := make(map[string][]int, 2)

	for _, core := range all {
		key, found := lastLevelGroup(core)
		if !found {
			return nil, nil, false
		}
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], core)
	}
	if len(keys) != 2 || len(groups[keys[0]]) < 2 || len(groups[keys[1]]) < 2 {
		return nil, nil, false
	}

	return groups[keys[0]], groups[keys[1]], true
}

// lastLevelGroup reports which level-3 cache one core sits behind, as the
// kernel's own text for the processors sharing it — an identity to group by
// rather than parse. The index directories are not numbered by level, so the
// level is read rather than assumed.
func lastLevelGroup(core int) (string, bool) {
	indices, err := filepath.Glob(filepath.Join(sysfsCPU, "cpu"+strconv.Itoa(core), "cache", "index*"))
	if err != nil {
		return "", false
	}

	for _, index := range indices {
		if strings.TrimSpace(readFile(filepath.Join(index, "level"))) != "3" {
			continue
		}
		if list := strings.TrimSpace(readFile(filepath.Join(index, "shared_cpu_list"))); list != "" {
			return list, true
		}
	}

	return "", false
}

// amd reports whether the processor is AMD's, from the kernel's copy of CPUID's
// vendor string.
func amd() bool {

	for _, line := range strings.Split(readFile("/proc/cpuinfo"), "\n") {
		if name, value, found := strings.Cut(line, ":"); found && strings.TrimSpace(name) == "vendor_id" {
			return strings.TrimSpace(value) == "AuthenticAMD"
		}
	}

	return false
}

// cpuList reads one of sysfs's processor lists ("0-7,16-23").
func cpuList(path string) map[int]bool {
	cores := make(map[int]bool)

	for _, span := range strings.Split(strings.TrimSpace(readFile(path)), ",") {
		if span == "" {
			continue
		}

		low, high, ranged := strings.Cut(span, "-")
		first, err := strconv.Atoi(strings.TrimSpace(low))
		if err != nil {
			continue
		}
		last := first
		if ranged {
			if last, err = strconv.Atoi(strings.TrimSpace(high)); err != nil {
				continue
			}
		}

		for core := first; core <= last; core++ {
			cores[core] = true
		}
	}

	return cores
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(data)
}

/* Applying */

// pin sets the affinity of every thread in the process.
//
// The syscall is per-thread, not per-process: a thread started later inherits
// the mask of the one that started it, so setting them all covers the runtime's
// future Ms and the cgo libraries' threads too. The walk runs twice because a
// thread created between the listing and the last call would have inherited from
// a thread not yet set.
func pin(cores []int) error {
	var set cpuSet
	for _, core := range cores {
		set.add(core)
	}

	var err error
	for range 2 {
		if err = affinity(0, &set, true); err != nil {
			return err
		}

		threads, readErr := os.ReadDir("/proc/self/task")
		if readErr != nil {
			return readErr
		}

		for _, thread := range threads {
			tid, convErr := strconv.Atoi(thread.Name())
			if convErr != nil {
				continue
			}
			// A thread that has exited since the listing is not a failure.
			_ = affinity(tid, &set, true)
		}
	}

	return err
}

// affinity is sched_getaffinity and sched_setaffinity, which take the same three
// arguments in the same order.
func affinity(tid int, set *cpuSet, write bool) error {
	call := uintptr(syscall.SYS_SCHED_GETAFFINITY)
	if write {
		call = uintptr(syscall.SYS_SCHED_SETAFFINITY)
	}

	_, _, errno := syscall.Syscall(call, uintptr(tid), unsafe.Sizeof(*set), uintptr(unsafe.Pointer(set)))
	if errno != 0 {
		return errno
	}

	return nil
}
