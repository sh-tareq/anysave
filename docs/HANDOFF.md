# AnySave: handover

Read this first if you are picking up the project (a new Claude account, another AI, or a developer).
Then read [ROADMAP.md](ROADMAP.md) for what to build next, and the main [README](../README.md),
especially "Things that are easy to break".

Last updated: 12 November 2026, after release **v1.0.5**.

---

## 1. What AnySave is

A free, open-source (GPL-3.0) downloader by **Shahidul H Tareq** (GitHub `sh-tareq`, repo
`sh-tareq/anysave`, public). No ads, no tracking, no account.

- Downloads videos from YouTube, Instagram, TikTok, Facebook, Vimeo and 1,000+ sites (yt-dlp),
  including playlists, channels and YouTube Mixes, with batch selection.
- Saves ad videos from **Meta Ad Library** and **TikTok Creative Center** (pick one, some or all).
- Download manager for any file (pause/resume, queue, folders by type), catches downloads started by websites.
- Converter (MP4/WebM/MKV/MOV/GIF, MP3/M4A/WAV/Opus, resize, target size, trim).
- Platforms: **Chrome/Edge/Brave extension + native helper on Mac and Windows**, and an **Android app**.
  Not in the Chrome Web Store or Google Play (they ban YouTube downloaders), so everything is side-loaded.

The owner is building a personal brand around tech, digital marketing and AI (Facebook page + LinkedIn,
Bangla and English). He wants the app to become "really advanced, ultra high tech, robust and flawless",
with all of Internet Download Manager's (IDM) features plus AnySave's own. See ROADMAP.md.

## 2. Current versions and state

| Part | Version | Where |
|---|---|---|
| Desktop extension | 1.0.5 | `extension/manifest.json` → `"version"` |
| Android app | 1.0.3 (versionCode 11) | `android/app/build.gradle.kts` |
| Helper | built from `host-go/` (no own version number yet) | |
| Latest GitHub release | **v1.0.5** (APK + desktop zip) | github.com/sh-tareq/anysave/releases |

The owner's Mac and his Android phone (Xiaomi Mi 8, Android 10, adb serial `cb5fdfb5`) run these builds.

**Not yet tested / known gaps:**
- The **Windows installer** (`installers/windows/install.ps1`) has never run on real Windows. Phase 0 of the
  roadmap adds a GitHub Actions Windows test for this.
- Meta ads through the helper inside the new video queue; Watch later / Liked lists with a real YouTube login;
  big playlists on non-YouTube sites.
- Older audit items still open (see §8).

## 3. Architecture

```
extension/      Chrome MV3 extension
  background.js   service worker: job store (chrome.storage.local "jobs"), video queue (3 at once),
                  native messaging, Chrome downloads (files), catching website downloads,
                  toolbar icon (animated progress ring + % badge + ✓ for unseen finished)
  popup.*         per-page UI: single video (live progress button), playlists/channels/Mixes picker,
                  Meta/TikTok ad lists (checkboxes, Select all, live progress pills), recent downloads
  manager.*       full download manager + converter page (Open, drag-out, multi-link paste, queue states)
  capture.js      TikTok Creative Center: remembers ads that played on hover
host-go/        native messaging helper (Go, no deps). Actions: info, playlist, download (streams progress),
                pause/cancel (via messages), convert (ffmpeg), probe, pickFile, fileInfo, reveal, open.
                Finds yt-dlp, ffmpeg, deno in ./bin next to itself.
installers/     mac/install-mac.sh (shipped as "Install AnySave.command"), windows/install.ps1 + .bat.
                Download yt-dlp, ffmpeg, Deno with SHA-256 verification; register the native host.
android/        Kotlin + Jetpack Compose app, youtubedl-android (yt-dlp + ffmpeg), bundled QuickJS
                (app/src/main/jniLibs/*/libqjs.so) as YouTube's JS runtime.
scripts/build.sh  builds helper for mac (universal) + windows, assembles dist/ zip, copies the APK.
scripts/fetch-quickjs.sh  refreshes the bundled QuickJS (checks SHA-256).
dist-assets/    marketing images (LinkedIn, Facebook carousel EN + BN) and R8 mappings (mappings are gitignored).
```

Key protocol facts:
- Native host name `com.vidgrab.host`, binary `vidgrab-host`, extension ID `jgkodjdjabccfmfbfmiinmppmcpgomai`
  (fixed by `key` in the manifest). **Never rename** (breaks installed copies). `keys/` is not in git.
- Video jobs: the extension sends `{action:"download", job:<id>, url, format, sort, audioOnly, cookies, folder}`.
  Each job keeps unfinished pieces in `Downloads/AnySave/<folder>/.anysave-parts/<job>`; `{action:"cancel"}`
  deletes them, `{action:"pause"}` or the browser closing keeps them (resume continues).
- yt-dlp needs a JavaScript runtime for YouTube: desktop passes `--js-runtimes deno:<path>`, Android
  `--js-runtimes quickjs:<nativeLibraryDir>/libqjs.so`. Without it YouTube offers mostly audio.
- Playlists: `--yes-playlist --flat-playlist -I 1:300 -J` (Mixes `list=RD…` use the watch URL and `1:50`).

## 4. Owner's preferences and rules (important)

- **Android release builds only, always R8:** `cd android && ./gradlew assembleRelease`. Copy
  `android/app/build/outputs/mapping/release/mapping.txt` to `dist-assets/r8-mapping-<versionName>.txt`
  (gitignored). Never overwrite the mapping of a version that was already published.
- Install on his phone with `adb -s cb5fdfb5 install -r <arm64 release apk>`: same signing key, keeps his data.
  A debug build would need an uninstall (loses data). Don't.
- Bump versions for each release: `android/app/build.gradle.kts` (versionCode +1, versionName) and
  `extension/manifest.json`. `FILE_USER_AGENT` strings only carry "1.0".
- Git: he asks before commits. Work on a short branch, then fast-forward `main` and push. Commit messages end with
  `Co-Authored-By: <the AI model> <noreply@anthropic.com>` (or your own tool's equivalent).
- **He publishes GitHub releases himself** (the AI's auto mode blocked `gh release create`). Prepare the notes file
  (format: "What's new" at the top, earlier versions in a collapsed `<details>`, SHA-256 checksums of
  `dist/AnySave.apk` and `dist/AnySave-Desktop-Mac-Windows.zip`) and give him the exact command, e.g.
  `gh release create vX.Y.Z -R sh-tareq/anysave --target main --title "AnySave X.Y.Z" --latest --notes-file <notes.md> dist/AnySave.apk dist/AnySave-Desktop-Mac-Windows.zip`.
  Start from the previous release body: `gh release view vX -R sh-tareq/anysave --json body -q .body`.
- Desktop install on his Mac: `./scripts/build.sh`, unzip `dist/AnySave-Desktop-Mac-Windows.zip` somewhere temporary,
  run `bash "AnySave-Desktop/AnySave-mac/Install AnySave.command"`, then he clicks **reload** on AnySave in
  `chrome://extensions`. For extension-only changes you can `rsync -a --delete extension/ ~/AnySave/extension/`
  (and `~/VidGrab/extension/`, an old path his Chrome may still load).
- Communication: plain, clear language; he follows on his phone too. He is non-native in English; he writes
  Bangla posts. Don't brag about AnySave in his personal "About" texts. On his own page don't refer to him in
  the third person in Bangla (use "— Shahidul H Tareq" as a signature).
- Don't use IDM's name or look inside the app (trademark); comparisons in his own posts are fine.

## 5. How to test (what worked)

- **Extension in a real browser:** Puppeteer + Chrome for Testing (branded Chrome ignores `--load-extension`).
  `npm i puppeteer@24` in a scratch folder, `npx @puppeteer/browsers install chrome@stable --path ./browsers`,
  launch with `enableExtensions: [<repo>/extension]`, get the service worker with
  `browser.waitForTarget(t => t.type()==="service_worker")` and call background functions directly
  (`startFile`, `updateJob`, `getJobs`, `adoptDownload`, `ring` …). For the popup, open `popup.html` in a tab and
  patch `chrome.tabs.query` with `page.evaluateOnNewDocument` to fake the active page URL.
- **Extension + real helper:** Chrome for Testing reads native hosts from `<userDataDir>/NativeMessagingHosts/`.
  Use a fixed `userDataDir`, put `com.vidgrab.host.json` there pointing to a wrapper script that runs the helper with
  `HOME=<scratch>/home` (so test downloads don't go to the owner's real Downloads) and symlink `bin/` to
  `~/Library/Application Support/AnySave/bin` for yt-dlp/ffmpeg/deno.
- **Helper alone:** send one native-messaging frame (4-byte little-endian length + JSON) on stdin and read the reply,
  e.g. `{"action":"playlist","url":"https://www.youtube.com/@BlenderStudio/shorts"}`. Real downloads: use the Blender
  channel shorts or `https://www.youtube.com/watch?v=aqz-KE-bpKQ` (Big Buck Bunny).
- **Android:** release build + `adb install -r`; drive with `adb shell am start -n com.vidgrab.app/.MainActivity
  -a android.intent.action.SEND -t text/plain --es android.intent.extra.TEXT '<url>'`, `adb shell input tap/swipe`,
  `adb exec-out screencap -p > shot.png`. Screen is 1080×2248.
- Go: `go vet ./... && GOOS=windows go vet ./...` in `host-go/`. Go may be at `~/.local/go/bin`.
- AI tool limits seen: running downloaded third-party binaries on the Mac and publishing releases were blocked by the
  agent's safety mode; heredocs with `<<'EOF'` sometimes tripped a command checker (write files with the editor tool).

## 6. Recent history (what was done, newest first)

- v1.0.5 / Android 1.0.3: playlists and channels with batch selection and one quality for the batch; YouTube Mixes
  (watch URL, first 50); Watch later / Liked use the login; clear errors; video download queue (3 at once,
  "Queued"); live progress on the single-video Download button, on every ad/playlist row and the batch button;
  several links at once (manager + Android); fixed stretched header label; Android: shared `BatchList.kt`,
  job-id collision fix.
- 1.0.4: live progress pills for ads, animated toolbar ring with % badge, selection outline no longer clipped.
- 1.0.3: Open with default app, double-click to open, drag files out of the list; helper `open` action.
- 1.0.2: ignore Chrome's download history replayed at startup (it showed "99✓" and brought cleared items back),
  one-time cleanup; ad selection (checkboxes, Select all) on desktop and Android.
- 1.0.1: big reliability pass (see git log 34d36a3): per-job partial folders, checksum-verified installers,
  Deno (desktop) and QuickJS (Android) for YouTube, Android notification/queue/backup fixes, toolbar progress.

## 7. Marketing material (not code)

- `dist-assets/facebook-anysave/` (English) and `dist-assets/facebook-anysave-bn/` (Bangla): 5-image carousels
  (1080×1350). Not committed yet. They were rendered from an HTML template with Puppeteer.
- The owner's Bangla Facebook post about AnySave (ransomware story with cracked IDM) and his page intro/About texts
  were written in the previous session.

## 8. Open items from the earlier audit (not done yet)

Desktop: Windows Downloads path is hard-coded (use the Known Folder API); log file never rotates and lives next to
the helper; no uninstaller; no version handshake between extension and helper; no auto-update of yt-dlp/helper;
no code signing; raw ffmpeg errors shown on convert failure; Windows child processes may survive if Chrome kills
the host (use a Job Object); Mac `osascript` file picker may open behind Chrome.
Extension: no `_locales`/i18n; manager tabs/chips/progress bars lack ARIA roles; no `:focus-visible` styles;
no notifications on finish/fail.
Android: `targetSdk` 34 (Play needs 35); no predictive back; "Save location" row does nothing; no themed icon;
no localization; dark-mode launch flash (window theme is light).
