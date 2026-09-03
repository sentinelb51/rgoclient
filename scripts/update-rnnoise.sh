#!/bin/sh
# Re-vendor internal/audio/rnnoise from xiph/rnnoise.
#
# Upstream is autotools and downloads its model at ./autogen.sh time; this
# client is cgo and a fresh clone must build with nothing fetched. The gap is
# closed here rather than at build time: the model is turned into the binary
# blob rnnoise_data.bin, which is committed and go:embed'd, and the 74 MB
# rnnoise_data.c that would otherwise carry those weights as C literals is
# reduced to the init function alone.
#
# Needs: git, curl, sha256sum, tar and a C compiler. Nothing here runs during a
# build.
set -e

# The commit and the model that goes with it. Upstream names the model archive
# by its own SHA-256, so the pin and the checksum are one string — the same
# discipline internal/deps uses for ffmpeg.
COMMIT=70f1d256acd4b34a572f999a05c87bf00b67730d
MODEL=0a8755f8e2d834eff6a54714ecc7d75f9932e845df35f8b59bc52a7cfe6e8b37

root=$(cd "$(dirname "$0")/.." && pwd)
dst=$root/internal/audio/rnnoise
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "cloning xiph/rnnoise @ $COMMIT"
git -C "$work" clone --quiet https://gitlab.xiph.org/xiph/rnnoise.git src
git -C "$work/src" checkout --quiet "$COMMIT"

echo "fetching model $MODEL"
curl -sSLo "$work/model.tar.gz" \
	"https://media.xiph.org/rnnoise/models/rnnoise_data-$MODEL.tar.gz"
echo "$MODEL  $work/model.tar.gz" | sha256sum -c -
tar xzf "$work/model.tar.gz" -C "$work/src"

# The weights, as the bytes rnnoise_model_from_buffer parses. DISABLE_DEBUG_FLOAT
# is upstream's own configure default and is what drops the float duplicate of
# every int8 array: 3.4 MB rather than 11. The "wb" is ours — upstream opens the
# blob in text mode, which corrupts it on Windows.
echo "building the weight blob"
sed -i 's/fopen("weights_blob.bin", "w")/fopen("weights_blob.bin", "wb")/' \
	"$work/src/src/write_weights.c"
cc=${CC:-$(command -v cc || command -v gcc)}
(cd "$work/src" && "$cc" -O2 -DDUMP_BINARY_WEIGHTS -DDISABLE_DEBUG_FLOAT \
	-Iinclude -Isrc src/write_weights.c -o dump_weights -lm && ./dump_weights)

echo "vendoring"
rm -f "$dst"/*.c "$dst"/*.h "$dst"/rnnoise_data.bin
cp "$work/src/include/rnnoise.h" "$dst/"
cp "$work/src/COPYING" "$dst/"
for f in _kiss_fft_guts.h arch.h celt_lpc.c celt_lpc.h common.h cpu_support.h \
	denoise.c denoise.h kiss_fft.c kiss_fft.h nnet.c nnet.h nnet_arch.h \
	nnet_default.c opus_types.h parse_lpcnet_weights.c pitch.c pitch.h rnn.c \
	rnn.h rnnoise_data.h rnnoise_tables.c vec.h vec_avx.h vec_neon.h; do
	cp "$work/src/src/$f" "$dst/"
done
# Headers only: the runtime-dispatch .c files are upstream's answer to not
# knowing the target at compile time, and cgo does know — see march_amd64.go.
cp "$work/src/src/x86/x86_arch_macros.h" "$work/src/src/x86/x86cpu.h" "$dst/"
cp "$work/src/weights_blob.bin" "$dst/rnnoise_data.bin"

# cgo compiles every .c in the package directory with one set of flags and can
# reach no subdirectory, so the tree is flat and these three includes follow it.
sed -i 's|#include "x86/x86_arch_macros.h"|#include "x86_arch_macros.h"|' \
	"$dst/nnet.h" "$dst/vec.h"
sed -i 's|#include "x86/x86cpu.h"|#include "x86cpu.h"|' "$dst/vec_avx.h"

# Everything before init_rnnoise in rnnoise_data.c is the weights as C literals,
# which USE_WEIGHTS_FILE excludes anyway. Dropping the text keeps 74 MB out of
# the repository and off every compile.
{
	printf '/* rgoclient: upstream'\''s rnnoise_data.c reduced to what\n'
	printf '   USE_WEIGHTS_FILE compiles. The weights it would otherwise\n'
	printf '   carry are rnnoise_data.bin. Regenerate with\n'
	printf '   scripts/update-rnnoise.sh. */\n\n'
	printf '#include "rnnoise_data.h"\n#include "nnet.h"\n\n'
	sed -n '/^#ifndef DUMP_BINARY_WEIGHTS/,$p' "$work/src/src/rnnoise_data.c"
} >"$dst/rnnoise_data.c"

echo "applying the gain floor"
git -C "$root" apply "$root/scripts/rnnoise-gain-floor.patch"

# Last, because both upstream's checkout and git apply write CRLF under
# core.autocrlf=true, and what is committed stays LF.
sed -i 's/\r$//' "$dst"/*.c "$dst"/*.h "$dst/COPYING"

echo "done — $COMMIT, model $MODEL"
