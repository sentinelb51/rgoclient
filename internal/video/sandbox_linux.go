package video

// The Linux sandbox is bubblewrap where it works: empty namespaces (no
// network, no pids, no ipc), the toolchain's directories and the one input
// file read-only, nothing writable but a tmpfs /tmp, and the child dying with
// the client. Whether the profile works on this machine — unprivileged user
// namespaces are switched off on some distros, and ffmpeg can live in odd
// places — is answered once by running `ffmpeg -version` inside it, so a
// machine where it cannot falls back to a plain child under rlimits rather
// than to a player that silently never plays.

import (
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// videoNice keeps a decoding child from competing with the UI thread.
	videoNice = 10

	// childMemoryCap bounds the child's address space: generous for any real
	// decode at chat-card sizes, fatal for an allocation bomb.
	childMemoryCap = 2 << 30

	// childCPUCap is CPU seconds, not wall clock — room for hours of
	// playback, a ceiling for a decoder spun into a loop.
	childCPUCap = 3600
)

var (
	sandboxOnce sync.Once
	bwrapPath   string // empty when bwrap is missing or its profile fails here
)

// sandboxArgv wraps the tool in bubblewrap where the one-time self-test
// passed, and hands it back bare otherwise.
func sandboxArgv(tool string, args []string, media string) []string {
	sandboxOnce.Do(func() { probeBwrap(tool) })
	if bwrapPath == "" {
		return append([]string{tool}, args...)
	}

	argv := bwrapProfile(bwrapPath, tool, media)
	argv = append(argv, tool)

	return append(argv, args...)
}

// bwrapProfile is the sandbox: everything the toolchain could live under,
// read-only; the input file, read-only, at its own path so no argument needs
// rewriting; and nothing else — no network, no session, no environment.
func bwrapProfile(bwrap, tool, media string) []string {
	argv := []string{
		bwrap,
		"--unshare-all",
		"--die-with-parent",
		"--new-session",
		"--clearenv",
		"--cap-drop", "ALL",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}

	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/sbin", "/opt", "/etc", "/nix", "/snap", "/run/current-system"} {
		argv = append(argv, "--ro-bind-try", dir, dir)
	}

	// A tool outside those directories still has to exist inside; its target
	// too, LookPath answering with symlinks as readily as binaries.
	argv = append(argv, "--ro-bind-try", tool, tool)
	if resolved, err := filepath.EvalSymlinks(tool); err == nil && resolved != tool {
		argv = append(argv, "--ro-bind-try", resolved, resolved)
	}
	if media != "" && media != tool {
		argv = append(argv, "--ro-bind", media, media)
	}

	return append(argv, "--")
}

// probeBwrap decides once whether this machine's bubblewrap can run this
// ffmpeg under the exact profile playback will use.
func probeBwrap(tool string) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return
	}

	argv := bwrapProfile(bwrap, tool, "")
	argv = append(argv, tool, "-version")

	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return
	}
	timer := time.AfterFunc(5*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()

	if cmd.Wait() == nil {
		bwrapPath = bwrap
	}
}

func platformAttrs(cmd *exec.Cmd) {}

// harden applies what a running child may cost, best-effort — the process is
// already live for the milliseconds this takes, which is why it is the second
// fence and the sandbox is the first. Nothing to release on Linux.
func harden(cmd *exec.Cmd) func() {
	pid := cmd.Process.Pid

	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, videoNice)
	capLimit(pid, unix.RLIMIT_AS, childMemoryCap)
	capLimit(pid, unix.RLIMIT_CPU, childCPUCap)
	capLimit(pid, unix.RLIMIT_CORE, 0)
	if bwrapPath == "" {
		// Under bwrap nothing is writable anyway; a plain child gets "no
		// file this process writes may have bytes in it" instead.
		capLimit(pid, unix.RLIMIT_FSIZE, 0)
	}

	return nil
}

// capLimit lowers one of the child's limits to want, never above its own
// hard ceiling — prlimit refuses a soft limit past the hard one.
func capLimit(pid, resource int, want uint64) {
	var current unix.Rlimit
	if err := unix.Prlimit(pid, resource, nil, &current); err != nil {
		return
	}
	if want > current.Max {
		want = current.Max
	}

	_ = unix.Prlimit(pid, resource, &unix.Rlimit{Cur: want, Max: current.Max}, nil)
}
