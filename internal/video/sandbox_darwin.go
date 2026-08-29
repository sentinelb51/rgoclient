package video

// The macOS sandbox is sandbox-exec: deprecated in the headers, load-bearing
// in every browser, and the only per-process containment reachable without an
// entitlement. The profile denies the network and file writes and allows the
// rest — an allowlist profile tight enough to matter is also tight enough to
// break somebody's Homebrew ffmpeg, and a player that never plays teaches the
// reader to turn the sandbox off. Whether it runs here is answered once, the
// way Linux answers for bubblewrap.

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const videoNice = 10

// sandboxProfile keeps the child off the network and off the disk. Reads are
// allowed: the input file is the point, and the dynamic loader needs the
// system. What a read could steal leaves through no socket and lands in
// pixels only this machine draws.
const sandboxProfile = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
`

var (
	sandboxOnce sync.Once
	sandboxExec string // empty when sandbox-exec is missing or refuses the profile
)

func sandboxArgv(tool string, args []string, media string) []string {
	sandboxOnce.Do(func() { probeSandboxExec(tool) })
	if sandboxExec == "" {
		return append([]string{tool}, args...)
	}

	argv := []string{sandboxExec, "-p", sandboxProfile, tool}

	return append(argv, args...)
}

func probeSandboxExec(tool string) {
	sb, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return
	}

	cmd := exec.Command(sb, "-p", sandboxProfile, tool, "-version")
	if err := cmd.Start(); err != nil {
		return
	}
	timer := time.AfterFunc(5*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()

	if cmd.Wait() == nil {
		sandboxExec = sb
	}
}

func platformAttrs(cmd *exec.Cmd) {}

func harden(cmd *exec.Cmd) func() {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, videoNice)

	return nil
}
