#!/bin/bash
# Makes the desktop app icons from the logo (dist-assets/vidgrab-logo-1024.png):
#   installers/mac/app/AppIcon.icns        Mac app + disk image icon
#   installers/windows/anysave.ico         Windows icon (shortcuts)
#   host-go/rsrc_windows_amd64.syso        icon inside vidgrab-host.exe
#   host-go/cmd/installer-win/rsrc_windows_amd64.syso   icon inside AnySave-Setup.exe
# The results are committed, so a normal build doesn't need to run this.
# Run it again only when the logo changes (macOS, needs sips + iconutil + python3).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LOGO="$ROOT/dist-assets/vidgrab-logo-1024.png"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---- Mac: AppIcon.icns ----
SET="$TMP/AppIcon.iconset"
mkdir -p "$SET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$LOGO" --out "$SET/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d "$LOGO" --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$SET" -o "$ROOT/installers/mac/app/AppIcon.icns"
echo "✓ installers/mac/app/AppIcon.icns"

# ---- Windows: anysave.ico (PNG images inside an ICO container) ----
for s in 16 24 32 48 64 128 256; do
  sips -z $s $s "$LOGO" --out "$TMP/ico-$s.png" >/dev/null
done
python3 "$ROOT/scripts/make-win-resources.py" "$TMP" \
  "$ROOT/installers/windows/anysave.ico" \
  "$ROOT/host-go/rsrc_windows_amd64.syso" \
  "$ROOT/host-go/cmd/installer-win/rsrc_windows_amd64.syso"
