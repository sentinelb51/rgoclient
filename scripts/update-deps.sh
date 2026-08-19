#!/usr/bin/env bash
#
# Updates the module graph, holding back the patched Fyne and everything it pins.
#
#   ./scripts/update-deps.sh              report what is available, change nothing
#   ./scripts/update-deps.sh --apply      update, verify, revert if the tree breaks
#
# Fyne is bumped by update-fyne.sh in github.com/sentinelb51/rgoclient-fyne, which
# rebases our patches onto the new release and hands back a tag for the replace
# line. That require line here is only a label for the replace, so moving it
# would change nothing that is built -- while floating what it pins
# (typesetting, render, glfw) under a frozen copy is the thing that actually
# breaks the render path.
#
# Flags: --include-shared  also update deps we share with Fyne (golang.org/x/image)
#        --no-test         skip go test, the slow half of the check

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

apply=0
run_tests=1
include_shared=0
for arg in "$@"; do
	case "$arg" in
		--apply)          apply=1 ;;
		--no-test)        run_tests=0 ;;
		--include-shared) include_shared=1 ;;
		*)                echo "unknown flag: $arg" >&2; exit 2 ;;
	esac
done

say() { printf '\n== %s ==\n' "$*"; }
die() { printf '\nerror: %s\n' "$*" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Everything the patched Fyne names in its own go.mod, itself included. These
# are held: a frozen Fyne compiles against the versions it was cut with. Read
# from wherever the replace resolves, so this survives a bump of the fork.
go mod download fyne.io/fyne/v2 >/dev/null 2>&1 || true
fyne_dir="$(go list -m -f '{{.Dir}}' fyne.io/fyne/v2)"
[ -n "$fyne_dir" ] && [ -f "$fyne_dir/go.mod" ] \
	|| die "cannot locate the Fyne module go.mod -- is the replace in go.mod resolvable?"
go mod edit -json "$fyne_dir/go.mod" \
	| grep '"Path"' | sed 's/.*: "//;s/".*//' | sort -u > "$work/hold"

go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}}{{end}}' all \
	| grep -v '^$' | sort -u > "$work/direct"

if [ "$include_shared" -eq 1 ]; then
	echo 'fyne.io/fyne/v2' > "$work/hold-effective"
else
	cp "$work/hold" "$work/hold-effective"
fi
comm -13 "$work/hold-effective" "$work/direct" > "$work/candidates"
mapfile -t candidates < "$work/candidates"
[ "${#candidates[@]}" -gt 0 ] || die "no updatable direct dependencies"

snapshot() { go list -m -f '{{if not .Main}}{{.Path}} {{.Version}}{{end}}' all | grep -v '^$' | sort; }

say "updatable (${#candidates[@]})"
go list -m -u -e -f '{{if .Update}}   {{.Path}}  {{.Version}} -> {{.Update.Version}}{{else}}   {{.Path}}  {{.Version}} (current){{end}}' "${candidates[@]}"

say "held for the patched Fyne ($(wc -l < "$work/hold-effective"))"
go list -m -u -e -f '{{if .Update}}   {{.Path}}  {{.Version}} -> {{.Update.Version}} HELD{{end}}' $(cat "$work/hold-effective") 2>/dev/null | grep . \
	|| echo "   (none have updates available)"

if [ "$apply" -eq 0 ]; then
	say "report only -- re-run with --apply to update"
	exit 0
fi

cp go.mod "$work/go.mod.bak"
cp go.sum "$work/go.sum.bak"
restore() { cp "$work/go.mod.bak" go.mod; cp "$work/go.sum.bak" go.sum; }

snapshot > "$work/before"

say "updating"
# @latest per module rather than -u: MVS pulls only what the named modules
# actually need, instead of sweeping every transitive dependency upward.
targets=(); for m in "${candidates[@]}"; do targets+=("$m@latest"); done
if ! go get "${targets[@]}" || ! go mod tidy; then
	restore
	die "go get / go mod tidy failed -- go.mod and go.sum restored"
fi

snapshot > "$work/after"

say "moved"
join "$work/before" "$work/after" 2>/dev/null \
	| awk '$2 != $3 { printf "   %s  %s -> %s\n", $1, $2, $3 }' > "$work/moved"
comm -13 <(cut -d' ' -f1 "$work/before") <(cut -d' ' -f1 "$work/after") \
	| awk '{ printf "   %s  (new)\n", $1 }' >> "$work/moved"
[ -s "$work/moved" ] && cat "$work/moved" || echo "   (nothing moved -- already current)"

# A held module can still be dragged up by something that now demands it. The
# build is the real gate, but this says where to look when it fails.
if grep -qFf "$work/hold-effective" "$work/moved" 2>/dev/null; then
	say "WARNING: modules Fyne pins were moved anyway"
	grep -Ff "$work/hold-effective" "$work/moved" || true
	echo "   Something in the update demands a newer version. If the build fails,"
	echo "   this is why -- revert with: git checkout go.mod go.sum"
fi

say "go build ./... && go vet ./..."
if ! go build ./... || ! go vet ./...; then
	restore
	die "build or vet failed -- go.mod and go.sum restored"
fi
if [ "$run_tests" -eq 1 ]; then
	say "go test ./..."
	if ! go test ./...; then
		restore
		die "tests failed -- go.mod and go.sum restored"
	fi
fi

say "done -- go.mod and go.sum updated and verified"
echo "Review with: git diff go.mod"
