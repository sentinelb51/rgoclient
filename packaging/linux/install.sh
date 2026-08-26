#!/bin/sh
# Files a release tarball into the current user's home, which is the whole of
# what turns a bare ELF into something a desktop can launch: a binary on PATH,
# an icon the shell can find by name, and the .desktop entry naming both.
#
# Per-user rather than system-wide on purpose. Nothing here is signed or
# packaged, so it has no business writing under /usr — and this way it needs no
# root, which a script somebody downloaded should not be asking for. Run it from
# the directory it was unpacked into.
set -eu

BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}"
ICON_DIR="$DATA_DIR/icons/hicolor/512x512/apps"
APP_DIR="$DATA_DIR/applications"

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ ! -f "$here/RGOClient" ]; then
	echo "RGOClient is not beside this script — run it from the unpacked tarball." >&2
	exit 1
fi

mkdir -p "$BIN_DIR" "$ICON_DIR" "$APP_DIR"

install -m 0755 "$here/RGOClient" "$BIN_DIR/rgoclient"
install -m 0644 "$here/rgo.png" "$ICON_DIR/rgoclient.png"
install -m 0644 "$here/rgoclient.desktop" "$APP_DIR/rgoclient.desktop"

# Both are absent on a minimal system and neither is required for the entry to
# work — they only save the shell noticing it on its own schedule.
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -qtf "$DATA_DIR/icons/hicolor" >/dev/null 2>&1 || true
fi

echo "Installed to $BIN_DIR/rgoclient"
case ":${PATH}:" in
*":$BIN_DIR:"*) ;;
*) echo "Note: $BIN_DIR is not on your PATH, so 'rgoclient' will not resolve from a shell." ;;
esac
