#!/bin/bash
# AnySave uninstaller for macOS. Double-click to run.
# Removes what the installer added. Your downloads (Downloads/AnySave) are NOT touched.
#
#  - ~/Library/Application Support/AnySave   (helper, yt-dlp, ffmpeg, speech models, settings, logs)
#  - ~/AnySave/extension                      (the extension folder Chrome loads)
#  - the browser registrations of the helper  (Chrome, Edge, Brave, Chromium)
#  - AnySave.app in Applications, and its "Start at Login" entry (~/Library/LaunchAgents)
#  - the `anysave` Terminal command and the PATH line the installer may have added to ~/.zprofile
#
# Options: --yes  don't ask first.
set -uo pipefail

# Tests point these at a scratch folder so nothing real is touched.
ROOT="${ANYSAVE_UNINSTALL_ROOT:-}"          # prefix for system folders (/Applications, /usr/local/bin…)
NO_KILL="${ANYSAVE_NO_KILL:-0}"             # don't stop running AnySave programs

[ -n "${HOME:-}" ] && [ -d "$HOME" ] || { echo "HOME isn't set; stopping."; exit 1; }
APP="$HOME/Library/Application Support/AnySave"
EXT_ROOT="$HOME/AnySave"
EXT_DEST="$EXT_ROOT/extension"
HOST_NAME="com.anysave.host"

echo "=== Uninstall AnySave ==="
echo "This removes the AnySave app, its helper, the speech models and its settings."
echo "Your downloaded files in Downloads/AnySave are kept."
if [ "${1:-}" != "--yes" ]; then
  printf "Continue? Type y and press Return: "
  read -r answer
  case "$answer" in y|Y|yes|YES) ;; *) echo "Nothing was removed."; exit 0 ;; esac
fi

# 1. Stop AnySave if it is running (the menu-bar app and its background service).
if [ "$NO_KILL" != "1" ]; then
  osascript -e 'tell application "AnySave" to quit' >/dev/null 2>&1 || true
  pkill -f "Application Support/AnySave/anysave-host" 2>/dev/null || true
  pkill -f "AnySave.app/Contents/MacOS/" 2>/dev/null || true
  sleep 1
fi

removed=()
remove_dir() { # only a real folder, never a link or an empty path
  local d="$1"
  if [ -n "$d" ] && [ -d "$d" ] && [ ! -L "$d" ]; then
    rm -rf -- "$d" && removed+=("$d")
  fi
}
remove_file() {
  local f="$1"
  if [ -n "$f" ] && { [ -f "$f" ] || [ -L "$f" ]; }; then
    rm -f -- "$f" && removed+=("$f")
  fi
}

# 2. Helper, tools, speech models, settings, logs.
remove_dir "$APP"

# 3. The extension folder (and ~/AnySave if nothing else is in it).
remove_dir "$EXT_DEST"
rmdir "$EXT_ROOT" 2>/dev/null && removed+=("$EXT_ROOT") || true

# 4. Browser registrations.
for d in \
  "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts" \
  "$HOME/Library/Application Support/Microsoft Edge/NativeMessagingHosts" \
  "$HOME/Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts" \
  "$HOME/Library/Application Support/Chromium/NativeMessagingHosts"; do
  remove_file "$d/$HOST_NAME.json"
done

# 5. The app, and its "Start at Login" entry.
[ "$NO_KILL" != "1" ] && { launchctl bootout "gui/$(id -u)/com.anysave.desktop" >/dev/null 2>&1 || true; }
remove_file "$HOME/Library/LaunchAgents/com.anysave.desktop.plist"
for a in "$ROOT/Applications/AnySave.app" "$HOME/Applications/AnySave.app"; do
  remove_dir "$a"
done

# 6. The `anysave` command: only links that point into AnySave's own folder.
for d in "$ROOT/usr/local/bin" "$ROOT/opt/homebrew/bin" "$HOME/.local/bin" "$HOME/bin"; do
  l="$d/anysave"
  if [ -L "$l" ] && [[ "$(readlink "$l")" == *"Application Support/AnySave/"* ]]; then
    remove_file "$l"
  fi
done
#    …and the two lines the installer may have added to ~/.zprofile.
if [ -f "$HOME/.zprofile" ] && grep -q '# AnySave command (anysave <link>)' "$HOME/.zprofile"; then
  cp "$HOME/.zprofile" "$HOME/.zprofile.before-anysave-uninstall"
  awk 'skip>0 {skip--; next} /^# AnySave command \(anysave <link>\)$/ {skip=1; next} {print}' \
    "$HOME/.zprofile.before-anysave-uninstall" > "$HOME/.zprofile"
  removed+=("the AnySave line in ~/.zprofile (a copy is in ~/.zprofile.before-anysave-uninstall)")
fi

echo
if [ ${#removed[@]} -eq 0 ]; then
  echo "AnySave wasn't installed (nothing to remove)."
else
  echo "Removed:"
  for r in "${removed[@]}"; do echo "  - $r"; done
fi
echo
echo "Last step, in Chrome: open chrome://extensions and click Remove on AnySave."
echo "Your downloads are still in Downloads/AnySave."
