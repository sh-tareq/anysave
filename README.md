# AnySave

Made by **Shahidul H Tareq**.

Download videos from **YouTube, Instagram, TikTok, Facebook, Vimeo and 1,000+ more sites**, save ad videos from **Meta Ad Library** and **TikTok Creative Center**, download **any file** with pause/resume, and **convert** videos and audio, on Android, Mac and Windows.

| Platform | What people get | How it works |
|---|---|---|
| macOS | Chrome / Edge / Brave extension + helper | Extension → native helper (Go) → yt-dlp + ffmpeg |
| Windows | Chrome / Edge / Brave extension + helper | Same as macOS |
| Android | AnySave app (APK) | Share a link to AnySave → yt-dlp + ffmpeg bundled in the app |

Chrome on Android has no extensions, so Android gets its own app.

## Download

Get the latest version from **[Releases](https://github.com/sh-tareq/anysave/releases/latest)**:

- **AnySave.apk**: Android 8 and newer
- **AnySave-Desktop-Mac-Windows.zip**: Chrome, Edge or Brave on Mac and Windows. Unzip it and follow
  "How to install.txt" in the folder for your computer.

## Features

| | Android app | Chrome / Edge / Brave extension |
|---|---|---|
| Videos from 1,000+ sites (yt-dlp) | ✓ paste, share, copy, Quick Settings tile | ✓ popup on any page, right-click menu |
| Built-in browser that finds video files on any page | ✓ | ✓ page scan in the popup |
| Meta Ad Library / TikTok Creative Center | ✓ | ✓ |
| Download manager: any file, pause/resume, queue, folders | ✓ Downloads tab | ✓ manager page |
| Converter: MP4/WebM/MKV/MOV/GIF, MP3/M4A/WAV/Opus, resize, target size, trim | ✓ Convert tab | ✓ manager page |
| Catch downloads started by a site's own download button | | ✓ (switch in the manager) |
| Download progress | a notification per download, and one if it fails | ring, percent or count on the toolbar icon, ✓ when finished |

Sites behind anti-bot protection (for example Cloudflare challenges) and DRM-protected services are not supported, by design.

## Build everything to share

```bash
cd android && ./gradlew assembleRelease && cd ..   # Android APKs
./scripts/build.sh                                  # Mac + Windows zips, copies APKs
```

Output in `dist/`:

| File | For |
|---|---|
| `AnySave-Desktop-Mac-Windows.zip` | Mac and Windows. Contains `AnySave-mac/` and `AnySave-windows/`, each with an installer and "How to install.txt". |
| `AnySave.apk` | Android phones (64-bit, R8-minified, signed with `android/vidgrab-release.jks`). |

The R8 mapping for each Android release is kept in `dist-assets/r8-mapping-<version>.txt`
(not shared). Use it to read crash stack traces from that build.
Logos are regenerated with `swift scripts/make-icons.swift`.
The Android app bundles QuickJS (`android/app/src/main/jniLibs/*/libqjs.so`), the JavaScript
runtime yt-dlp needs for YouTube; refresh it with `scripts/fetch-quickjs.sh` (checks each
file against its published SHA-256). The desktop installers download Deno for the same reason.

Each zip includes a `How to install.txt` written for non-technical people.

Building needs Go (`~/.local/go` or on `PATH`) and, for Android, Android Studio's SDK.

## Layout

```
extension/            Chrome extension (MV3). Popup UI + service worker.
host-go/              Native messaging helper for macOS + Windows (Go, no dependencies).
installers/mac/       install-mac.sh (packaged as "Install AnySave.command") + How to install.txt
installers/windows/   Install AnySave.bat, install.ps1 + How to install.txt
android/              Android app (Kotlin, Compose, youtubedl-android)
scripts/build.sh      Builds the helper for all platforms and assembles dist/
```

### Where the desktop installers put things

| | macOS | Windows |
|---|---|---|
| Helper, yt-dlp, ffmpeg | `~/Library/Application Support/AnySave` | `%LOCALAPPDATA%\AnySave` |
| Extension to load in Chrome | `~/AnySave/extension` | `%USERPROFILE%\AnySave\extension` |
| Browser registration | NativeMessagingHosts JSON files | `HKCU\Software\…\NativeMessagingHosts` |
| Downloads | `~/Downloads/AnySave` | `Downloads\AnySave` |

Re-running the installer updates yt-dlp, which fixes most "stopped working" problems.

## Things that are easy to break

- **Extension ID.** The `key` in `extension/manifest.json` fixes the ID to
  `jgkodjdjabccfmfbfmiinmppmcpgomai`. The installers allow only that ID to use the helper.
  `keys/key.pem` is the matching private key. Keep it out of git.
- **macOS yt-dlp build.** The installer uses the unpacked `yt-dlp_macos.zip`, not the
  single-file `yt-dlp_macos`. The single-file build unpacks Python on every run, and
  macOS blocks those files when Chrome starts it ("Python.framework is damaged").
- **Logins.** macOS blocks other programs from reading Chrome's cookie database. So when
  "Use my Chrome login" is ticked, the extension reads cookies for that one site through
  `chrome.cookies` and passes them to the helper. The helper writes them to a private
  temp file and deletes it when yt-dlp finishes.
  On Android, Settings → Accounts logs in to Instagram, Facebook, TikTok, Vimeo or YouTube in a
  WebView and keeps those cookies in the app's private storage.
- **Internal IDs still say "vidgrab".** The Android package (`com.vidgrab.app`), the extension key/ID,
  the native host name (`com.vidgrab.host`) and the helper binary (`vidgrab-host`) keep the project's
  original working name on purpose. Changing them would break updates for installed copies. Don't rename them.
- **Android signing key.** `android/vidgrab-release.jks` + `android/keystore.properties`.
  Back these up. People can only install updates signed with the same key.
- **Android yt-dlp updates.** The app updates yt-dlp at most every 2 days, before it
  reports "ready". Running yt-dlp while it's being replaced fails with
  "zipimport.ZipImportError".

## Debugging

- Desktop helper log: `vidgrab.log` next to the helper (see the table above).
- `vidgrab-host --check` prints which yt-dlp and ffmpeg it found.
- Android: `adb logcat | grep -i vidgrab`

## Legal

- Full notice (disclaimer, responsibility, privacy, open-source licences): [LEGAL.md](LEGAL.md).
  The same text is in the Android app (Settings → Legal & disclaimer) and the extension (`extension/legal.html`).
- Both apps make the user accept the notice before the first download. Bump `TERMS_VERSION` in
  `android/.../Legal.kt` and `extension/popup.js` to ask everyone again after changing it.
- **The Android app is GPL-3.0** because it bundles youtubedl-android (GPL-3.0). Anyone who receives the APK is
  entitled to its source code. See [LICENSE](LICENSE).

## Notes

- The Chrome Web Store and Google Play don't allow YouTube downloaders, so everything is side-loaded.
- Only download content you have the rights to. Each platform's terms of service apply.
