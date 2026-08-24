//go:build windows

package cpu

import (
	"errors"
	"syscall"
	"unsafe"
)

// currentProcess is what GetCurrentProcess answers: the constant -1, not
// something to be looked up.
const currentProcess = ^uintptr(0)

var (
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	getSystemCPUSetInformation = kernel32.NewProc("GetSystemCpuSetInformation")
	getLogicalProcessorInfo    = kernel32.NewProc("GetLogicalProcessorInformation")
	getProcessAffinityMask     = kernel32.NewProc("GetProcessAffinityMask")
	setProcessAffinityMask     = kernel32.NewProc("SetProcessAffinityMask")
)

// cpuSetInfo is Win32's SYSTEM_CPU_SET_INFORMATION, flattened: the union has one
// arm and Kind says which. Records are walked by their own Size rather than by
// this struct's, which is what keeps a newer Windows adding a field from shifting
// every record after the first.
type cpuSetInfo struct {
	Size uint32
	Kind uint32

	ID                    uint32
	Group                 uint16
	LogicalProcessorIndex uint8
	CoreIndex             uint8
	LastLevelCacheIndex   uint8
	NUMANodeIndex         uint8
	EfficiencyClass       uint8
	Flags                 uint8
	SchedulingClass       uint32
	AllocationTag         uint64
}

// cpuSetInformation is the only Kind defined.
//
// The flags beside it are deliberately not read. Parked is the one that looks
// useful and is the one that would break this: core parking is a power state the
// scheduler moves minute to minute, and on an idle laptop most of the machine
// carries it. A mask built from what was awake at startup would be a fraction of
// the cores, permanently.
const cpuSetInformation = 0

// logicalProcessorInfo is Win32's SYSTEM_LOGICAL_PROCESSOR_INFORMATION — the
// original, not the Ex form. The Ex form's cache record ends in a union whose
// layout differs between SDK versions; this one is a fixed 32 bytes and carries
// the processor mask directly, which is the whole of what a chiplet needs.
type logicalProcessorInfo struct {
	ProcessorMask uintptr
	Relationship  uint32
	_             uint32

	CacheLevel         uint8
	CacheAssociativity uint8
	CacheLineSize      uint16
	CacheSize          uint32
	CacheType          uint32
	_                  [4]byte
}

const relationCache = 2

// errorInsufficientBuffer is how the second enumeration reports the length it
// wants; the first reports it through the returned length alone.
const errorInsufficientBuffer = syscall.Errno(122)

/* Detection */

// detect reads the machine's cores. Every failure below degrades to a topology
// with no split rather than to no topology at all: the baseline is what "all
// cores" restores to, and it is worth having even when nothing else is legible.
func detect() Topology {
	var process, system uintptr

	ok, _, _ := getProcessAffinityMask.Call(currentProcess,
		uintptr(unsafe.Pointer(&process)), uintptr(unsafe.Pointer(&system)))
	if ok == 0 || process == 0 {
		return Topology{}
	}

	topology := Topology{All: maskCores(process)}

	sets, err := cpuSets()
	if err != nil {
		return topology
	}

	// SetProcessAffinityMask names processors within the process's own group, and a
	// machine past 64 of them has more than one. Rather than pin to a group these
	// indices may not be in, such a machine is reported as having no split — see
	// docs/known-gaps.md.
	for _, set := range sets {
		if set.Group != 0 {
			return topology
		}
	}

	// Chiplets are read before efficiency classes because AMD's own tooling has
	// been seen to publish a class on parts whose difference is cache, which would
	// otherwise name the larger-cache, slower-clocking chiplet the performance one.
	if performance, efficiency, split := chiplets(topology.All, sets); split {
		topology.Performance, topology.Efficiency = performance, efficiency
		return topology
	}

	if performance, efficiency, split := hybrid(topology.All, sets); split {
		topology.Performance, topology.Efficiency = performance, efficiency
		topology.Hybrid = true
	}

	return topology
}

// hybrid splits by EfficiencyClass, which Windows reports per logical processor
// and which rises with speed. The top class is the performance cores and
// everything below it is efficiency: the parts carrying three classes put their
// slowest cores at the bottom, not in the middle.
func hybrid(all []int, sets []cpuSetInfo) (performance, efficiency []int, split bool) {
	class := make(map[int]uint8, len(sets))

	var top uint8
	for _, set := range sets {
		class[int(set.LogicalProcessorIndex)] = set.EfficiencyClass
		top = max(top, set.EfficiencyClass)
	}

	for _, core := range all {
		reported, found := class[core]
		if !found {
			return nil, nil, false // a core no record covers: absent is not slow
		}
		if reported == top {
			performance = append(performance, core)
			continue
		}
		efficiency = append(efficiency, core)
	}

	return performance, efficiency, len(performance) > 0 && len(efficiency) > 0
}

// chiplets splits by last-level cache size. Cores sharing an L3 are one chiplet,
// and where the chiplets do not carry the same amount of it the smaller one is
// the part that was not given stacked cache and clocks higher for it. Equal
// caches are not a split: two chiplets alike on paper differ only in binning,
// which nothing here can read.
func chiplets(all []int, sets []cpuSetInfo) (performance, efficiency []int, split bool) {
	group := make(map[int]uint8, len(sets))
	for _, set := range sets {
		group[int(set.LogicalProcessorIndex)] = set.LastLevelCacheIndex
	}

	sizes := make(map[uint8]uint32)
	for mask, size := range lastLevelCaches() {
		for _, core := range maskCores(mask) {
			if index, found := group[core]; found {
				sizes[index] = size
			}
		}
	}
	if len(sizes) < 2 {
		return nil, nil, false
	}

	var smallest, largest uint32
	for _, size := range sizes {
		if smallest == 0 || size < smallest {
			smallest = size
		}
		largest = max(largest, size)
	}
	if smallest == largest {
		return nil, nil, false
	}

	for _, core := range all {
		size, found := sizes[group[core]]
		if !found {
			return nil, nil, false // a core belonging to no cache we could size
		}
		if size == smallest {
			performance = append(performance, core)
			continue
		}
		efficiency = append(efficiency, core)
	}

	return performance, efficiency, len(performance) > 0 && len(efficiency) > 0
}

/* Enumeration */

// cpuSets reads every logical processor's CPU set record.
func cpuSets() ([]cpuSetInfo, error) {
	if err := getSystemCPUSetInformation.Find(); err != nil {
		return nil, err // pre-Windows 10
	}

	var length uint32
	getSystemCPUSetInformation.Call(0, 0, uintptr(unsafe.Pointer(&length)), currentProcess, 0)
	if length == 0 {
		return nil, errors.New("cpu: no cpu set information")
	}

	buffer := make([]byte, length)
	ok, _, err := getSystemCPUSetInformation.Call(uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(length), uintptr(unsafe.Pointer(&length)), currentProcess, 0)
	if ok == 0 {
		return nil, err
	}

	record := uint32(unsafe.Sizeof(cpuSetInfo{}))

	var sets []cpuSetInfo
	for offset := uint32(0); offset+record <= length; {
		set := *(*cpuSetInfo)(unsafe.Pointer(&buffer[offset]))
		if set.Size < record {
			break
		}
		if set.Kind == cpuSetInformation {
			sets = append(sets, set)
		}
		offset += set.Size
	}

	return sets, nil
}

// lastLevelCaches maps each level-3 cache's processor mask to its size in bytes.
func lastLevelCaches() map[uintptr]uint32 {
	var length uint32

	ok, _, err := getLogicalProcessorInfo.Call(0, uintptr(unsafe.Pointer(&length)))
	if ok != 0 || err != errorInsufficientBuffer || length == 0 {
		return nil
	}

	buffer := make([]byte, length)
	if ok, _, _ := getLogicalProcessorInfo.Call(uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&length))); ok == 0 {
		return nil
	}

	record := uint32(unsafe.Sizeof(logicalProcessorInfo{}))
	caches := make(map[uintptr]uint32)

	for offset := uint32(0); offset+record <= length; offset += record {
		info := *(*logicalProcessorInfo)(unsafe.Pointer(&buffer[offset]))
		if info.Relationship == relationCache && info.CacheLevel == 3 {
			caches[info.ProcessorMask] = info.CacheSize
		}
	}

	return caches
}

/* Applying */

// pin hands Windows one mask for the whole process. Unlike the per-thread call
// the other platforms take, this reaches threads that already exist as well as
// the ones the runtime and the cgo libraries have yet to start.
func pin(cores []int) error {
	var mask uintptr

	for _, core := range cores {
		if core >= 64 {
			return errors.New("cpu: logical processor outside the process's group")
		}
		mask |= 1 << uint(core)
	}

	if ok, _, err := setProcessAffinityMask.Call(currentProcess, mask); ok == 0 {
		return err
	}

	return nil
}

// maskCores expands an affinity mask into logical processor indices.
func maskCores(mask uintptr) []int {
	var cores []int

	for core := range 64 {
		if mask&(1<<uint(core)) != 0 {
			cores = append(cores, core)
		}
	}

	return cores
}
