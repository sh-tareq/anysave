#!/bin/bash
# AnySave installer for macOS. Double-click to run.
#  - Helper app + yt-dlp + ffmpeg  -> ~/Library/Application Support/AnySave
#  - Chrome extension folder       -> ~/AnySave/extension
#  - Registers the helper with Chrome, Edge, Brave and Chromium
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"
APP="$HOME/Library/Application Support/AnySave"
BIN="$APP/bin"
EXT_DEST="$HOME/AnySave/extension"
HOST_NAME="com.vidgrab.host"
EXT_ID="jgkodjdjabccfmfbfmiinmppmcpgomai"   # fixed by the "key" in extension/manifest.json

echo "=== Installing AnySave ==="
if [ ! -f "$SRC/helper/vidgrab-host" ] || [ ! -d "$SRC/extension" ]; then
  echo "✗ This installer must be run from the unzipped AnySave-mac folder"
  echo "  (it needs the 'helper' and 'extension' folders next to it)."
  echo "  Developers: run scripts/build.sh and use dist/AnySave-mac/."
  exit 1
fi
mkdir -p "$BIN" "$(dirname "$EXT_DEST")"

# 1. Helper app. Remove the old copy first: overwriting a signed program in place makes
#    macOS kill it on launch ("Killed: 9"), mostly on Apple Silicon.
rm -f "$APP/vidgrab-host"
cp "$SRC/helper/vidgrab-host" "$APP/vidgrab-host"
chmod +x "$APP/vidgrab-host"

# 2. Extension (kept in a permanent place so the download folder can be deleted)
rm -rf "$EXT_DEST"
cp -R "$SRC/extension" "$EXT_DEST"

# Upgrading from VidGrab: Chrome still loads ~/VidGrab/extension, so refresh that copy too
# (nothing breaks), and reuse the old ffmpeg instead of downloading it again.
OLD_APP="$HOME/Library/Application Support/VidGrab"
OLD_EXT="$HOME/VidGrab/extension"
UPGRADED=""
if [ -d "$OLD_EXT" ]; then
  rm -rf "$OLD_EXT"; cp -R "$SRC/extension" "$OLD_EXT"; UPGRADED=1
fi
if [ -x "$OLD_APP/bin/ffmpeg" ] && [ ! -x "$BIN/ffmpeg" ]; then cp "$OLD_APP/bin/ffmpeg" "$BIN/ffmpeg"; fi

# 3. yt-dlp, unpacked build. The single-file build unpacks Python on every run,
#    and macOS blocks those files when Chrome starts it ("Python.framework is damaged").
echo "↓ Downloading yt-dlp…"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fetch() { curl -fL --retry 5 --retry-delay 3 --retry-all-errors "$@"; }
# check <file> <expected sha256>: stops the install if a download isn't the published file.
check() {
  local got; got="$(shasum -a 256 "$1" | cut -d' ' -f1)"
  if [ -z "$2" ] || [ "$got" != "$2" ]; then
    echo "✗ $(basename "$1") didn't match its published checksum. Nothing was installed from it."
    echo "  Check your internet connection and run the installer again."
    exit 1
  fi
}
YTDLP_URL="https://github.com/yt-dlp/yt-dlp/releases/latest/download"
fetch --progress-bar -o "$TMP/yt-dlp.zip" "$YTDLP_URL/yt-dlp_macos.zip"
fetch -s -o "$TMP/SUMS" "$YTDLP_URL/SHA2-256SUMS"
check "$TMP/yt-dlp.zip" "$(awk '$2=="yt-dlp_macos.zip"{print $1}' "$TMP/SUMS")"
rm -rf "$BIN/yt-dlp_app"
mkdir -p "$BIN/yt-dlp_app"
unzip -q "$TMP/yt-dlp.zip" -d "$BIN/yt-dlp_app"
chmod +x "$BIN/yt-dlp_app/yt-dlp_macos"

# sysctl, not uname: a Terminal running under Rosetta reports x86_64 on Apple Silicon.
if [ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = "1" ]; then ARCH_HW=arm64; else ARCH_HW=x86_64; fi

# 4. ffmpeg (needed to join YouTube video + audio and to make MP3s)
if [ ! -x "$BIN/ffmpeg" ]; then
  echo "↓ Downloading ffmpeg…"
  if [ "$ARCH_HW" = "arm64" ]; then ARCH=arm64; else ARCH=amd64; fi
  # The "latest" link redirects to a versioned file; its checksum sits next to it.
  FF_URL="$(curl -s -o /dev/null -w '%{redirect_url}' --retry 5 \
    "https://ffmpeg.martin-riedl.de/redirect/latest/macos/$ARCH/release/ffmpeg.zip")"
  [ -n "$FF_URL" ] || { echo "✗ Couldn't reach the ffmpeg download site. Run the installer again."; exit 1; }
  fetch --progress-bar -o "$TMP/ffmpeg.zip" "$FF_URL"
  check "$TMP/ffmpeg.zip" "$(fetch -s "$FF_URL.sha256" | cut -d' ' -f1)"
  unzip -q -o "$TMP/ffmpeg.zip" -d "$TMP"
  mv "$TMP/ffmpeg" "$BIN/ffmpeg"
  chmod +x "$BIN/ffmpeg"
fi

# 5. Deno: the JavaScript runtime yt-dlp needs to unlock YouTube's video formats.
if [ ! -x "$BIN/deno" ]; then
  echo "↓ Downloading Deno (needed for YouTube)…"
  if [ "$ARCH_HW" = "arm64" ]; then DENO=deno-aarch64-apple-darwin.zip; else DENO=deno-x86_64-apple-darwin.zip; fi
  DENO_URL="https://github.com/denoland/deno/releases/latest/download/$DENO"
  fetch --progress-bar -o "$TMP/deno.zip" "$DENO_URL"
  check "$TMP/deno.zip" "$(fetch -s "$DENO_URL.sha256sum" | cut -d' ' -f1)"
  unzip -q -o "$TMP/deno.zip" -d "$TMP"
  mv "$TMP/deno" "$BIN/deno"
  chmod +x "$BIN/deno"
fi

# Files that came from a downloaded zip carry macOS's "downloaded from the
# internet" flag, which would stop Chrome from starting the helper.
xattr -dr com.apple.quarantine "$APP" "$EXT_DEST" 2>/dev/null || true
[ -n "$UPGRADED" ] && xattr -dr com.apple.quarantine "$OLD_EXT" 2>/dev/null || true

# 5b. Native macOS Desktop App (AnySave.app -> /Applications or ~/Applications)
if [ -d "$SRC/AnySave.app" ]; then
  APP_DEST="/Applications/AnySave.app"
  if [ ! -w "/Applications" ]; then
    mkdir -p "$HOME/Applications"
    APP_DEST="$HOME/Applications/AnySave.app"
  fi
  rm -rf "$APP_DEST"
  cp -R "$SRC/AnySave.app" "$APP_DEST"
  xattr -dr com.apple.quarantine "$APP_DEST" 2>/dev/null || true
  echo "✓ Installed AnySave.app -> $APP_DEST"
fi

# 6. Register the helper with each browser (also ones not installed yet, so installing
#    Chrome, Edge or Brave later still works)
for TARGET in \
  "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts" \
  "$HOME/Library/Application Support/Microsoft Edge/NativeMessagingHosts" \
  "$HOME/Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts" \
  "$HOME/Library/Application Support/Chromium/NativeMessagingHosts"; do
  mkdir -p "$TARGET"
  cat > "$TARGET/$HOST_NAME.json" <<EOF
{
  "name": "$HOST_NAME",
  "description": "AnySave helper",
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
echo "Already installed? Just click the reload button on AnySave in chrome://extensions."
echo "To update AnySave later, run this installer again."
# The old helper folder isn't used any more (the browser registration now points here).
rm -rf "$OLD_APP"
if [ -n "$UPGRADED" ]; then
  echo
  echo "Updated: just click the reload button on the extension in chrome://extensions."
  echo "(Optional: remove it and 'Load unpacked' from $EXT_DEST, then delete ~/VidGrab.)"
fi
if [ "${ANYSAVE_NO_OPEN:-0}" != "1" ]; then
  open -R "$EXT_DEST" || true
fi
