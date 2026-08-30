//go:build !windows

package power

// Neither request exists here, and both are a silent no-op rather than an error:
// nothing above chooses whether to ask, so there is nothing for a caller to do
// with a refusal.
//
// The timer has no analogue — Linux and macOS both honour a timed wait at the
// length it asked for, so there is nothing to buy. Throttling has one on each
// and neither is usable. Linux's is nice, and it is one-way: an unprivileged
// process may raise its nice value and cannot lower it again without
// CAP_SYS_NICE (RLIMIT_NICE defaults to 0), so a client that went quiet in the
// background would stay quiet for the rest of the session. macOS's is
// PRIO_DARWIN_BG, which throttles disk I/O hard enough to hold up the very
// notification a backgrounded client exists to deliver.

import "time"

func set(time.Duration) {}

func throttle(bool) {}
