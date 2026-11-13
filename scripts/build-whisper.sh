#!/usr/bin/env bash
# Builds whisper.cpp's command-line program (whisper-cli) for macOS as one universal file
# (Apple Silicon + Intel), straight with clang: no CMake or Homebrew needed.
# On Apple Silicon it uses the graphics chip (Metal, shaders built into the program); on Intel
# Macs the CPU with Apple's Accelerate library.
#
# usage: scripts/build-whisper.sh <output-file>
# The source is downloaded at a fixed release and checked against a fixed commit.
set -euo pipefail

WHISPER_TAG="v1.9.5"
WHISPER_COMMIT="d1be6fde11ac6e0407606b4e42fe72d34add8037"
OUT="${1:?usage: build-whisper.sh <output-file>}"
WORK="${WHISPER_WORK:-$(mktemp -d)}"
SRC="$WORK/whisper.cpp"

if [ ! -d "$SRC/.git" ]; then
  git clone -q --depth 1 --branch "$WHISPER_TAG" https://github.com/ggml-org/whisper.cpp "$SRC" 2>/dev/null
fi
got="$(git -C "$SRC" rev-parse HEAD)"
if [ "$got" != "$WHISPER_COMMIT" ]; then
  echo "whisper.cpp $WHISPER_TAG is commit $got, expected $WHISPER_COMMIT. Refusing to build." >&2
  exit 1
fi

G="$SRC/ggml/src"
GEN="$WORK/gen"
mkdir -p "$GEN"
sed -e 's/@GGML_VERSION@/0.26.0/' -e "s/@GGML_BUILD_COMMIT@/${WHISPER_COMMIT:0:8}/" "$G/ggml-version.h.in" > "$GEN/ggml-version.h"

COMMON_SRC=(
  "$G/ggml.c" "$G/ggml.cpp" "$G/ggml-alloc.c" "$G/ggml-backend.cpp" "$G/ggml-backend-meta.cpp"
  "$G/ggml-opt.cpp" "$G/ggml-threading.cpp" "$G/ggml-quants.c" "$G/gguf.cpp" "$G/ggml-backend-reg.cpp" "$G/ggml-backend-dl.cpp"
  "$G/ggml-cpu/ggml-cpu.c" "$G/ggml-cpu/ggml-cpu.cpp" "$G/ggml-cpu/repack.cpp" "$G/ggml-cpu/hbm.cpp"
  "$G/ggml-cpu/quants.c" "$G/ggml-cpu/traits.cpp" "$G/ggml-cpu/binary-ops.cpp" "$G/ggml-cpu/unary-ops.cpp"
  "$G/ggml-cpu/vec.cpp" "$G/ggml-cpu/ops.cpp" "$G/ggml-cpu/tiled/tiled.cpp" "$G/ggml-cpu/tiled/tiled-kernel.cpp"
  "$SRC/src/whisper.cpp" "$SRC/src/aneforge/whisper-aneforge.cpp"
  "$SRC/examples/common.cpp" "$SRC/examples/common-ggml.cpp" "$SRC/examples/common-whisper.cpp"
  "$SRC/examples/grammar-parser.cpp" "$SRC/examples/cli/cli.cpp"
)
# examples/ first: it and ggml-cpu/ both have a "common.h" (each source finds its own sibling first).
INCLUDES=(-I"$SRC/examples" -I"$SRC/include" -I"$SRC/ggml/include" -I"$G" -I"$G/ggml-cpu" -I"$SRC/src" -I"$GEN")
DEFS=(-DNDEBUG -DGGML_USE_CPU -DGGML_USE_ACCELERATE -DACCELERATE_NEW_LAPACK -DACCELERATE_LAPACK_ILP64
      -DGGML_USE_CPU_REPACK -DGGML_VERSION=\"0.26.0\" -DGGML_COMMIT=\"${WHISPER_COMMIT:0:8}\"
      -DWHISPER_VERSION=\"${WHISPER_TAG#v}\" -D_DARWIN_C_SOURCE)

# Metal shaders, built into the program the way whisper.cpp's own build does it
# (GGML_METAL_EMBED_LIBRARY): each kernel with its headers inlined, wrapped in an asm .incbin.
M="$G/ggml-metal"
metal_embed() {
  local out="$WORK/metal-embed"
  mkdir -p "$out"
  local k kind sym src hdrs
  for src in "$M"/kernels/*.metal; do
    kind="$(basename "$src" .metal)"
    case "$kind" in fa_common|fa_vec_common) continue ;; esac
    sym="${kind//-/_}"
    hdrs=("$M/kernels/common.h")
    grep -q '#include "dequantize.h"' "$src" && hdrs+=("$M/kernels/dequantize.h")
    grep -q '#include "quantize.h"' "$src" && hdrs+=("$M/kernels/quantize.h")
    grep -q '#include "fa_common.metal"' "$src" && hdrs+=("$M/kernels/fa_common.metal")
    grep -q '#include "fa_vec_common.metal"' "$src" && hdrs+=("$M/kernels/fa_vec_common.metal")
    cat "${hdrs[@]}" "$src" \
      | sed -e '/#include "common.h"/d' -e '/#include "dequantize.h"/d' -e '/#include "quantize.h"/d' \
            -e '/#include "fa_common.metal"/d' -e '/#include "fa_vec_common.metal"/d' -e '/#pragma once/d' \
      | sed -e "/__embed_ggml-common.h__/r $G/ggml-common.h" -e '/__embed_ggml-common.h__/d' \
      | sed -e "/#include \"ggml-metal-impl.h\"/r $M/ggml-metal-impl.h" -e '/#include "ggml-metal-impl.h"/d' \
      > "$out/$kind.metal"
    {
      echo ".section __DATA,__ggml_metallib"
      echo ".globl _ggml_metallib_${sym}_start"
      echo "_ggml_metallib_${sym}_start:"
      echo ".incbin \"$out/$kind.metal\""
      echo ".globl _ggml_metallib_${sym}_end"
      echo "_ggml_metallib_${sym}_end:"
    } > "$out/$kind.s"
  done
  ls "$out"/*.s
}

build_arch() { # arch, extra flags, arch sources...
  local arch="$1"; shift
  local flags="$1"; shift
  local link_extra=()
  local obj="$WORK/obj-$arch"
  mkdir -p "$obj"
  local objs=()
  local i=0
  for f in "${COMMON_SRC[@]}" "$@"; do
    i=$((i + 1))
    local o="$obj/$i-$(basename "$f").o"
    case "$f" in
      *.s) clang -arch "$arch" -c "$f" -o "$o" ;;
      *.m) clang -arch "$arch" -O3 $flags "${INCLUDES[@]}" -I"$M" "${DEFS[@]}" -c "$f" -o "$o" ;;
      *.c) clang -arch "$arch" -O3 -std=c11 $flags "${INCLUDES[@]}" "${DEFS[@]}" -c "$f" -o "$o" ;;
      *) clang++ -arch "$arch" -O3 -std=c++17 $flags "${INCLUDES[@]}" -I"$M" "${DEFS[@]}" -c "$f" -o "$o" ;;
    esac &
    objs+=("$o")
    # a few at a time
    if (( i % 8 == 0 )); then wait; fi
  done
  wait
  [[ " $flags " == *" -DGGML_USE_METAL "* ]] && link_extra=(-framework Foundation -framework Metal -framework MetalKit)
  clang++ -arch "$arch" -mmacosx-version-min=12.0 "${objs[@]}" -framework Accelerate ${link_extra[@]+"${link_extra[@]}"} -o "$WORK/whisper-cli-$arch"
}

export MACOSX_DEPLOYMENT_TARGET=12.0
METAL_ASM=($(metal_embed))
build_arch arm64 "-mcpu=apple-m1 -DGGML_USE_METAL -DGGML_METAL_EMBED_LIBRARY -DGGML_METAL_NDEBUG" \
  "$G/ggml-cpu/arch/arm/quants.c" "$G/ggml-cpu/arch/arm/repack.cpp" "$G/ggml-cpu/arch/arm/cpu-feats.cpp" \
  "$M/ggml-metal.cpp" "$M/ggml-metal-device.m" "$M/ggml-metal-device.cpp" "$M/ggml-metal-common.cpp" \
  "$M/ggml-metal-context.m" "$M/ggml-metal-fusion.cpp" "$M/ggml-metal-ops.cpp" "$M/ggml-metal-tuning.cpp" \
  "${METAL_ASM[@]}"
build_arch x86_64 "-mavx2 -mfma -mf16c" "$G/ggml-cpu/arch/x86/quants.c" "$G/ggml-cpu/arch/x86/repack.cpp" "$G/ggml-cpu/arch/x86/cpu-feats.cpp"

lipo -create -output "$OUT" "$WORK/whisper-cli-arm64" "$WORK/whisper-cli-x86_64"
codesign --force --sign - "$OUT" 2>/dev/null || true
echo "Built $OUT ($(lipo -archs "$OUT"))"
