#!/bin/bash
# Builds the AnySave helper for macOS (universal) and Windows, then makes the
# zips to share:  dist/AnySave-mac.zip  and  dist/AnySave-windows.zip
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
GOOS=windows GOARCH=amd64 "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$BUILD/installer-stub.exe" ./cmd/installer-win
lipo -create -output "$BUILD/vidgrab-host" "$BUILD/vidgrab-host-arm64" "$BUILD/vidgrab-host-amd64"
codesign --force --sign - "$BUILD/vidgrab-host"   # ad-hoc signature, required on Apple Silicon

# ---- Native macOS AnySave.app bundle ----
echo "Building native macOS AnySave.app…"
APP_BUNDLE="$BUILD/AnySave.app"
mkdir -p "$APP_BUNDLE/Contents/MacOS" "$APP_BUNDLE/Contents/Resources"
swiftc -O -target arm64-apple-macos13.0 "$ROOT/installers/mac/app/main.swift" -o "$BUILD/AnySave-app-arm64"
swiftc -O -target x86_64-apple-macos13.0 "$ROOT/installers/mac/app/main.swift" -o "$BUILD/AnySave-app-amd64"
lipo -create -output "$APP_BUNDLE/Contents/MacOS/AnySave" "$BUILD/AnySave-app-arm64" "$BUILD/AnySave-app-amd64"
cp "$BUILD/vidgrab-host" "$APP_BUNDLE/Contents/MacOS/vidgrab-host"
cp "$ROOT/installers/mac/app/Info.plist" "$APP_BUNDLE/Contents/Info.plist"
cp -R "$ROOT/extension" "$APP_BUNDLE/Contents/Resources/extension"
codesign --force --deep --sign - "$APP_BUNDLE"

# ---- One desktop zip: Mac + Windows folders side by side ----
PKG="$BUILD/AnySave-Desktop"

MAC="$PKG/AnySave-mac"
mkdir -p "$MAC/helper"
cp "$BUILD/vidgrab-host" "$MAC/helper/"
cp -R "$APP_BUNDLE" "$MAC/AnySave.app"
cp -R "$ROOT/extension" "$MAC/extension"
cp "$ROOT/installers/mac/install-mac.sh" "$MAC/Install AnySave.command"
cp "$ROOT/installers/mac/How to install.txt" "$MAC/"
chmod +x "$MAC/Install AnySave.command"

# Windows tools expect CRLF line endings
WIN="$PKG/AnySave-windows"
mkdir -p "$WIN/helper"
cp "$BUILD/vidgrab-host.exe" "$WIN/helper/"
cp -R "$ROOT/extension" "$WIN/extension"
for f in "Install AnySave.bat" "install.ps1" "How to install.txt"; do
  perl -pe 's/\r?\n/\r\n/' "$ROOT/installers/windows/$f" > "$WIN/$f"
done

perl -pe 's/\r?\n/\r\n/' "$ROOT/installers/Read me first.txt" > "$PKG/Read me first.txt"
cp "$ROOT/dist-assets/vidgrab-logo-1024.png" "$PKG/AnySave logo.png"
cp "$ROOT/LEGAL.md" "$PKG/Legal notice.txt"
cp "$ROOT/LICENSE" "$PKG/LICENSE.txt"
# ditto keeps the Mac installer's "executable" flag; Windows unzips it normally
(cd "$BUILD" && ditto -c -k --norsrc --keepParent AnySave-Desktop "$DIST/AnySave-Desktop-Mac-Windows.zip")

# ---- Single-file Windows Setup executable (dist/AnySave-Setup.exe) ----
echo "Building dist/AnySave-Setup.exe…"
(cd "$WIN" && zip -qr "$BUILD/win-payload.zip" .)
python3 - "$BUILD/installer-stub.exe" "$BUILD/win-payload.zip" "$DIST/AnySave-Setup.exe" <<'PY'
import struct, sys
stub_path, zip_path, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(stub_path, "rb") as f1, open(zip_path, "rb") as f2:
    stub = f1.read()
    payload = f2.read()
trailer = struct.pack("<Q", len(payload)) + b"ANYSAVE1"
with open(out_path, "wb") as out:
    out.write(stub)
    out.write(payload)
    out.write(trailer)
PY

# ---- Native macOS Disk Image (dist/AnySave-Mac.dmg) ----
if command -v hdiutil >/dev/null 2>&1; then
  echo "Building dist/AnySave-Mac.dmg…"
  hdiutil create -volname "AnySave" -srcfolder "$MAC" -ov -format UDZO "$DIST/AnySave-Mac.dmg" >/dev/null
fi

# ---- Android APK (build first with: cd android && ./gradlew assembleRelease) ----
APKS="$ROOT/android/app/build/outputs/apk/release"
if [ -f "$APKS/app-arm64-v8a-release.apk" ]; then
  cp "$APKS/app-arm64-v8a-release.apk" "$DIST/AnySave.apk"   # 64-bit phones (nearly all in use)
  # Keep the R8 mapping (not shared) to read crash reports from this build
  VER=$(grep -o 'versionName = "[^"]*"' "$ROOT/android/app/build.gradle.kts" | cut -d'"' -f2)
  mkdir -p "$ROOT/dist-assets"
  cp "$ROOT/android/app/build/outputs/mapping/release/mapping.txt" "$ROOT/dist-assets/r8-mapping-$VER.txt"
fi

rm -rf "$BUILD"
echo
ls -lh "$DIST"
