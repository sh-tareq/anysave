#!/bin/bash
# VidGrab installer for macOS. Double-click to run.
#  - Helper app + yt-dlp + ffmpeg  -> ~/Library/Application Support/VidGrab
#  - Chrome extension folder       -> ~/VidGrab/extension
#  - Registers the helper with Chrome, Edge, Brave and Chromium
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"
APP="$HOME/Library/Application Support/VidGrab"
BIN="$APP/bin"
EXT_DEST="$HOME/VidGrab/extension"
HOST_NAME="com.vidgrab.host"
EXT_ID="jgkodjdjabccfmfbfmiinmppmcpgomai"   # fixed by the "key" in extension/manifest.json

echo "=== Installing VidGrab ==="
if [ ! -f "$SRC/helper/vidgrab-host" ] || [ ! -d "$SRC/extension" ]; then
  echo "✗ This installer must be run from the unzipped VidGrab-mac folder"
  echo "  (it needs the 'helper' and 'extension' folders next to it)."
  echo "  Developers: run scripts/build.sh and use dist/VidGrab-mac/."
  exit 1
fi
mkdir -p "$BIN" "$(dirname "$EXT_DEST")"

# 1. Helper app
cp "$SRC/helper/vidgrab-host" "$APP/vidgrab-host"
chmod +x "$APP/vidgrab-host"

# 2. Extension (kept in a permanent place so the download folder can be deleted)
rm -rf "$EXT_DEST"
cp -R "$SRC/extension" "$EXT_DEST"

# 3. yt-dlp, unpacked build. The single-file build unpacks Python on every run,
#    and macOS blocks those files when Chrome starts it ("Python.framework is damaged").
echo "↓ Downloading yt-dlp…"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fL --retry 5 --retry-delay 3 --retry-all-errors --progress-bar -o "$TMP/yt-dlp.zip" \
  "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos.zip"
rm -rf "$BIN/yt-dlp_app"
mkdir -p "$BIN/yt-dlp_app"
unzip -q "$TMP/yt-dlp.zip" -d "$BIN/yt-dlp_app"
chmod +x "$BIN/yt-dlp_app/yt-dlp_macos"

# 4. ffmpeg (needed to join YouTube video + audio and to make MP3s)
if [ ! -x "$BIN/ffmpeg" ]; then
  echo "↓ Downloading ffmpeg…"
  case "$(uname -m)" in arm64) ARCH=arm64 ;; *) ARCH=amd64 ;; esac
  curl -fL --retry 5 --retry-delay 3 --retry-all-errors --progress-bar -o "$TMP/ffmpeg.zip" \
    "https://ffmpeg.martin-riedl.de/redirect/latest/macos/$ARCH/release/ffmpeg.zip"
  unzip -q -o "$TMP/ffmpeg.zip" -d "$TMP"
  mv "$TMP/ffmpeg" "$BIN/ffmpeg"
  chmod +x "$BIN/ffmpeg"
fi

# Files that came from a downloaded zip carry macOS's "downloaded from the
# internet" flag, which would stop Chrome from starting the helper.
xattr -dr com.apple.quarantine "$APP" "$EXT_DEST" 2>/dev/null || true

# 5. Register the helper with each browser that is installed
for TARGET in \
  "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts" \
  "$HOME/Library/Application Support/Microsoft Edge/NativeMessagingHosts" \
  "$HOME/Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts" \
  "$HOME/Library/Application Support/Chromium/NativeMessagingHosts"; do
  [ -d "$(dirname "$TARGET")" ] || continue
  mkdir -p "$TARGET"
  cat > "$TARGET/$HOST_NAME.json" <<EOF
{
  "name": "$HOST_NAME",
  "description": "VidGrab helper",
  "path": "$APP/vidgrab-host",
  "type": "stdio",
  "allowed_origins": ["chrome-extension://$EXT_ID/"]
}
EOF
  echo "✓ Registered with $(basename "$(dirname "$TARGET")")"
done

echo
"$APP/vidgrab-host" --check
echo
echo "=== Done! Last step, in Chrome: ==="
echo "  1. Go to chrome://extensions and turn on Developer mode (top right)"
echo "  2. Click 'Load unpacked' and choose the folder:  $EXT_DEST"
echo "     (a Finder window showing it is opening now)"
echo "  3. Quit Chrome (Cmd+Q) and open it again"
echo
echo "To update VidGrab later, run this installer again."
open -R "$EXT_DEST" || true
