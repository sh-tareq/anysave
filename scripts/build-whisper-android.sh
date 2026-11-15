#!/usr/bin/env bash
# Builds whisper.cpp's command-line program for the Android app (speech to text for Captions),
# one per CPU type, and puts it in jniLibs as libwhisper.so (Android only lets apps run programs
# from their native-library folder, hence the lib*.so name).
#
# arm64 gets two builds: libwhisper.so works on every 64-bit ARM phone; libwhisper_dot.so uses the
# "dot product" instructions of newer phones (about 2018+ flagships, 2020+ mid-range) and is faster.
# The app picks it only when /proc/cpuinfo lists "asimddp", and falls back if it ever crashes.
#
# usage: scripts/build-whisper-android.sh            (needs the Android NDK and CMake)
# The source is downloaded at a fixed release and checked against a fixed commit.
set -euo pipefail

WHISPER_TAG="v1.9.5"
WHISPER_COMMIT="d1be6fde11ac6e0407606b4e42fe72d34add8037"
NDK="${ANDROID_NDK:-$(ls -d "$HOME/Library/Android/sdk/ndk/"* | sort -V | tail -1)}"
DEST="$(cd "$(dirname "$0")/.." && pwd)/android/app/src/main/jniLibs"
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

STRIP="$(ls "$NDK"/toolchains/llvm/prebuilt/*/bin/llvm-strip | head -1)"
# CMake: the one on PATH, else the Android SDK's.
CMAKE="$(command -v cmake || ls -d "$HOME/Library/Android/sdk/cmake/"*/bin/cmake | sort -V | tail -1)"

# abi  output-name  extra cmake options
build() {
  local abi="$1" name="$2"; shift 2
  local b="$WORK/build-$name-$abi"
  "$CMAKE" -S "$SRC" -B "$b" -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_TOOLCHAIN_FILE="$NDK/build/cmake/android.toolchain.cmake" \
    -DANDROID_ABI="$abi" -DANDROID_PLATFORM=android-26 \
    -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF \
    -DWHISPER_BUILD_EXAMPLES=ON -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF \
    -DWHISPER_SDL2=OFF -DWHISPER_CURL=OFF "$@" > "$b.log" 2>&1
  "$CMAKE" --build "$b" --target whisper-cli -j 8 >> "$b.log" 2>&1
  mkdir -p "$DEST/$abi"
  "$STRIP" -o "$DEST/$abi/$name.so" "$b/bin/whisper-cli"
  echo "✓ $abi/$name.so ($(du -h "$DEST/$abi/$name.so" | cut -f1))"
}

build arm64-v8a   libwhisper     -DGGML_CPU_ARM_ARCH=armv8-a
build arm64-v8a   libwhisper_dot -DGGML_CPU_ARM_ARCH=armv8.2-a+dotprod+fp16
build armeabi-v7a libwhisper
build x86_64      libwhisper
