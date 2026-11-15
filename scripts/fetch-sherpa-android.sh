#!/usr/bin/env bash
# Puts the voice engine (sherpa-onnx's read-aloud and noise-removal programs, k2-fsa, Apache-2.0)
# into the Android app for Voice Lab, one set per CPU type. Android only lets apps run programs from
# their native-library folder, hence the lib*.so names:
#   libsherpa_tts.so      sherpa-onnx-offline-tts
#   libsherpa_denoise.so  sherpa-onnx-offline-denoiser
#   libonnxruntime.so     ONNX Runtime (MIT), which both need
#   libc++_shared.so      the C++ library from the Android NDK (both need it)
# The programs find the libraries next to them ($ORIGIN). Models are downloaded by the app.
# To update: change VERSION and the hashes (from the release's asset digests on GitHub).
set -euo pipefail

VERSION="v1.13.8"
DEST="$(cd "$(dirname "$0")/.." && pwd)/android/app/src/main/jniLibs"
NDK="${ANDROID_NDK:-$(ls -d "$HOME/Library/Android/sdk/ndk/"* | sort -V | tail -1)}"
TC="$(ls -d "$NDK"/toolchains/llvm/prebuilt/* | head -1)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# abi  release-cpu  ndk-triple  sha256
while read -r ABI CPU TRIPLE SUM; do
  NAME="sherpa-onnx-$VERSION-android-$CPU-termux-shared"
  curl -fsSL --retry 5 -o "$WORK/$NAME.tar.bz2" "https://github.com/k2-fsa/sherpa-onnx/releases/download/$VERSION/$NAME.tar.bz2"
  if [ "$(shasum -a 256 "$WORK/$NAME.tar.bz2" | cut -d' ' -f1)" != "$SUM" ]; then
    echo "✗ $NAME doesn't match its published checksum"; exit 1
  fi
  tar xjf "$WORK/$NAME.tar.bz2" -C "$WORK" "$NAME/bin/sherpa-onnx-offline-tts" "$NAME/bin/sherpa-onnx-offline-denoiser" "$NAME/lib/libonnxruntime.so"
  mkdir -p "$DEST/$ABI"
  cp "$WORK/$NAME/bin/sherpa-onnx-offline-tts" "$DEST/$ABI/libsherpa_tts.so"
  cp "$WORK/$NAME/bin/sherpa-onnx-offline-denoiser" "$DEST/$ABI/libsherpa_denoise.so"
  cp "$WORK/$NAME/lib/libonnxruntime.so" "$DEST/$ABI/libonnxruntime.so"
  "$TC/bin/llvm-strip" -o "$DEST/$ABI/libc++_shared.so" "$TC/sysroot/usr/lib/$TRIPLE/libc++_shared.so"
  echo "✓ $ABI: sherpa-onnx $VERSION"
done <<EOF
arm64-v8a   aarch64     aarch64-linux-android  ad7bac42adc6295293fe6d36475936afb227cdc757cf293c02a706f6aca4e0a2
armeabi-v7a armeabi-v7a arm-linux-androideabi  c0035cdb556ba1207fce6a869a2160f0075389bdd17a6043e2deeb67789d45f6
x86_64      x86_64      x86_64-linux-android   d2616a94c166a28846af1b53483c759463f43308bd9fd1f5374d3e81f898c384
EOF
