#!/bin/bash
# Builds the VidGrab helper for macOS (universal) and Windows, then makes the
# zips to share:  dist/VidGrab-mac.zip  and  dist/VidGrab-windows.zip
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
BUILD="$DIST/build"

GO="$(command -v go || true)"
[ -z "$GO" ] && [ -x "$HOME/.local/go/bin/go" ] && GO="$HOME/.local/go/bin/go"
[ -z "$GO" ] && [ -x /usr/local/go/bin/go ] && GO=/usr/local/go/bin/go
if [ -z "$GO" ]; then echo "Go not found. Install it from https://go.dev/dl/"; exit 1; fi
echo "Using $("$GO" version)"

rm -rf "$DIST"
mkdir -p "$BUILD"
cd "$ROOT/host-go"
export CGO_ENABLED=0
LDFLAGS="-s -w"

echo "Building helper…"
GOOS=darwin  GOARCH=arm64 "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$BUILD/vidgrab-host-arm64" .
GOOS=darwin  GOARCH=amd64 "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$BUILD/vidgrab-host-amd64" .
GOOS=windows GOARCH=amd64 "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$BUILD/vidgrab-host.exe" .
lipo -create -output "$BUILD/vidgrab-host" "$BUILD/vidgrab-host-arm64" "$BUILD/vidgrab-host-amd64"
codesign --force --sign - "$BUILD/vidgrab-host"   # ad-hoc signature, required on Apple Silicon

# ---- One desktop zip: Mac + Windows folders side by side ----
PKG="$BUILD/VidGrab-Desktop"

MAC="$PKG/VidGrab-mac"
mkdir -p "$MAC/helper"
cp "$BUILD/vidgrab-host" "$MAC/helper/"
cp -R "$ROOT/extension" "$MAC/extension"
cp "$ROOT/installers/mac/install-mac.sh" "$MAC/Install VidGrab.command"
cp "$ROOT/installers/mac/How to install.txt" "$MAC/"
chmod +x "$MAC/Install VidGrab.command"

# Windows tools expect CRLF line endings
WIN="$PKG/VidGrab-windows"
mkdir -p "$WIN/helper"
cp "$BUILD/vidgrab-host.exe" "$WIN/helper/"
cp -R "$ROOT/extension" "$WIN/extension"
for f in "Install VidGrab.bat" "install.ps1" "How to install.txt"; do
  perl -pe 's/\r?\n/\r\n/' "$ROOT/installers/windows/$f" > "$WIN/$f"
done

perl -pe 's/\r?\n/\r\n/' "$ROOT/installers/Read me first.txt" > "$PKG/Read me first.txt"
cp "$ROOT/dist-assets/vidgrab-logo-1024.png" "$PKG/VidGrab logo.png"
cp "$ROOT/LEGAL.md" "$PKG/Legal notice.txt"
cp "$ROOT/LICENSE" "$PKG/LICENSE.txt"
# ditto keeps the Mac installer's "executable" flag; Windows unzips it normally
(cd "$BUILD" && ditto -c -k --norsrc --keepParent VidGrab-Desktop "$DIST/VidGrab-Desktop-Mac-Windows.zip")

# ---- Android APK (build first with: cd android && ./gradlew assembleRelease) ----
APKS="$ROOT/android/app/build/outputs/apk/release"
if [ -f "$APKS/app-arm64-v8a-release.apk" ]; then
  cp "$APKS/app-arm64-v8a-release.apk" "$DIST/VidGrab.apk"   # 64-bit phones (nearly all in use)
  # Keep the R8 mapping (not shared) to read crash reports from this build
  VER=$(grep -o 'versionName = "[^"]*"' "$ROOT/android/app/build.gradle.kts" | cut -d'"' -f2)
  mkdir -p "$ROOT/dist-assets"
  cp "$ROOT/android/app/build/outputs/mapping/release/mapping.txt" "$ROOT/dist-assets/r8-mapping-$VER.txt"
fi

rm -rf "$BUILD"
echo
ls -lh "$DIST"
