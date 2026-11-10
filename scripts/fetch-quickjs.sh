#!/bin/bash
# Puts QuickJS (quickjs-ng) into the Android app as libqjs.so, one per CPU type.
# yt-dlp needs a JavaScript runtime to unlock YouTube's video formats; Deno has no Android
# build, so the app ships QuickJS. The binaries are fully static, so they run on Android.
# Android only lets apps run programs from their native-library folder, hence the lib*.so name.
# To update: change VERSION and the hashes (from the release's asset digests on GitHub).
set -euo pipefail

VERSION="v0.18.0"
DEST="$(cd "$(dirname "$0")/.." && pwd)/android/app/src/main/jniLibs"

# abi  release-asset  sha256
while read -r ABI ASSET SUM; do
  mkdir -p "$DEST/$ABI"
  OUT="$DEST/$ABI/libqjs.so"
  curl -fsSL --retry 5 -o "$OUT.tmp" "https://github.com/quickjs-ng/quickjs/releases/download/$VERSION/$ASSET"
  if [ "$(shasum -a 256 "$OUT.tmp" | cut -d' ' -f1)" != "$SUM" ]; then
    rm -f "$OUT.tmp"; echo "✗ $ASSET doesn't match its published checksum"; exit 1
  fi
  mv "$OUT.tmp" "$OUT"
  echo "✓ $ABI: $ASSET ($VERSION)"
done <<EOF
arm64-v8a   qjs-linux-aarch64 2c005c7171424a598d31c53c8dc4a0ae2e4958eb6fbb37908c885a307c78187c
armeabi-v7a qjs-linux-armv7   4bbc426083b4a31f35cbb9471ce6138a8d8db8efdfce15b596597ad60ddd79ac
x86_64      qjs-linux-x86_64  85ded23b9fabe8c1644fc40a8d2bbdd71157affcbd807359c618cc717ee24d7d
EOF
