//go:build !linux && !windows && !darwin

package video

// No sandbox exists to offer here; the child is still its own process with
// its own address space, which is the boundary the design leans on.

import "os/exec"

func sandboxArgv(tool string, args []string, media string) []string {
	return append([]string{tool}, args...)
}

func platformAttrs(cmd *exec.Cmd) {}

func harden(cmd *exec.Cmd) func() { return nil }
