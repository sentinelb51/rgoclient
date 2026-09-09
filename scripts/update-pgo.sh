#!/usr/bin/env bash
#
# Regenerates cmd/rgoclient/default.pgo, the profile the compiler builds the
# client against. `go build` picks it up by name -- -pgo=auto has been the
# default since Go 1.21 -- so nothing in the workflows names it.
#
#   ./scripts/update-pgo.sh          regenerate, report the diff in cost
#   ./scripts/update-pgo.sh --check  measure the current profile, change nothing
#
# What it profiles is internal/app's virtual benchmarks, which are the client's
# hot path with no display attached: message widgets built, markdown parsed,
# RichText wrapped and measured, the column laid out, scrolled and paged. The
# two footprint benchmarks are excluded deliberately -- they call runtime.GC to
# measure a live heap, and a forced collection is most of a profile that
# includes one.
#
# A stale profile costs correctness nothing: it only guides inlining and
# devirtualisation, and a call graph it does not know about is compiled the way
# it always was. Regenerate it when the hot path moves -- a new widget on the
# message row, a change to how the column mounts -- rather than on a schedule.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

profile="cmd/rgoclient/default.pgo"
benches='BenchmarkOpenChannel|BenchmarkWheelTick|BenchmarkPrependPage|BenchmarkAppendLive'

check=0
[ "${1:-}" = "--check" ] && check=1

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

say() { printf '\n== %s ==\n' "$*"; }

say "measuring against the current profile"
go test ./internal/app/ -run '^$' -bench "$benches" -benchtime 500x -count 5 \
	| grep '^Benchmark' > "$work/before" || true
cat "$work/before"

if [ "$check" -eq 1 ]; then
	exit 0
fi

say "collecting"
# -pgo=off so the profile describes the client as written rather than the
# client the last profile already reshaped.
go test ./internal/app/ -pgo=off -run '^$' -bench "$benches" -benchtime 2500x \
	-cpuprofile "$work/new.pprof" -o "$work/app.test" > /dev/null

cp "$work/new.pprof" "$profile"

say "measuring against the new one"
go test ./internal/app/ -run '^$' -bench "$benches" -benchtime 500x -count 5 \
	| grep '^Benchmark' > "$work/after" || true
cat "$work/after"

say "written to $profile"
