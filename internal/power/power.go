// Package power decides how hard the operating system tries on this process's
// behalf: whether a timed wait is honoured at the length it asked for, and
// whether the process may be run cheaply.
//
// Both are asked for and given up again as the window moves in and out of the
// front, so neither is a startup decision — which is the whole reason they are
// two calls rather than one flag. Nothing here imports anything else of ours.
// What the two are *for* is a policy and lives with the settings that name it:
// a window nobody is looking at needs neither a fine frame clock nor a fast
// core, and a call in progress overrules both.
package power

import "time"

// Precise asks that timed waits shorter than want be honoured at their stated
// length rather than rounded up to the platform's own tick, and gives that up
// again at a want of zero or one the platform already meets.
//
// The client's frame clock *is* a timed wait: the driver parks in the OS event
// queue for whatever is left until the next frame deadline. Rounded up, a
// deadline finer than the tick is a frame rate the client cannot reach however
// little it has to draw.
//
// Whether want is short enough to be worth asking about is the platform's own
// question, so it is passed rather than compared here. Calls must balance — the
// platform counts requests — which is what makes this idempotent rather than a
// setter. Safe from any goroutine.
func Precise(want time.Duration) {
	set(want)
}

// Throttle asks the OS to run this process cheaply — efficient cores, lower
// clocks — and stops asking. Idempotent and safe from any goroutine.
//
// It reaches every thread in the process, including whichever one the audio
// backend renders on, so the caller owns the question of when that is
// acceptable.
func Throttle(on bool) {
	throttle(on)
}
