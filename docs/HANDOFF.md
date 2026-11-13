# AnySave: handover

Read this first if you are picking up the project (a new Claude account, another AI, or a developer).
Then read [ROADMAP.md](ROADMAP.md) for what to build next, and the main [README](../README.md),
especially "Things that are easy to break".

Last updated: 13 November 2026. Phases 0–3 are **built**, and on 13 November they were tested for real for
the first time (before that they had only passed automated tests). That testing found and fixed serious bugs:
see §2a for what was verified, what was fixed, and what still is not built or not tested.

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
| Desktop extension | 1.1.0 | `extension/manifest.json` → `"version"` |
| Android app | 1.0.4 (versionCode 12) | `android/app/build.gradle.kts` (`FastFileEngine.kt` multi-connection engine). Not released yet: the last published APK is 1.0.3. Bump before the next release. |
| Helper & Desktop App | 1.1.0 | `host-go/diagnostics.go` → `HelperVersion`. The desktop app is **not Wails**: on Mac it is a small Swift menu-bar app (`installers/mac/app/main.swift`, WKWebView) and on Windows a hand-written tray app (`host-go/desktop_windows.go`). Both show the extension's `manager.html`, served by the helper's background service (`host-go/daemon.go`, `anysave-host --daemon`). |
| Latest GitHub release | **v1.0.5** (v1.1.0 ready to publish) | github.com/sh-tareq/anysave/releases |

The owner's Mac and his Android phone (Xiaomi Mi 8, Android 10, adb serial `cb5fdfb5`) run these builds.

## 2a. What was really tested on 13 November 2026

Method: the real helper on real servers, Puppeteer + Chrome for Testing with the helper registered, the Mac app
launched against a scratch home, and a release build on the owner's phone (see §5).

**Verified working (after fixes):**
- Engine on real servers: 100 MB from `proof.ovh.net` in 5.6 s vs 6.3 s for one `curl`, same SHA-256;
  `ash-speed.hetzner.com` (allows 2 connections); pause/resume and cancel cleanup.
- Extension: the "who handles downloads" switch (AnySave / Chrome / Ask), hand-over to Chrome's download list,
  automatic fallback to Chrome, pause/resume/cancel, taking over a website's own download, Alt-click and Cmd-click,
  the rules (excluded sites, file types, minimum size), speed limit (measured 1.01 MB/s for a 1 MB/s cap),
  video overlay button, site grabber, pattern links (`part[01-03].zip`), the manager page during a download.
- Helper: a real YouTube download (360p) with yt-dlp.
- Desktop service (`--daemon`): token check, add link, pause/resume, cancel, speed limit, `anysave-host <url>`,
  quit mid-download then resume after restart. Mac app: starts the service, installs the helper, downloads, quits cleanly.
- Android (release build on the Mi 8): 100 MB from both test servers with correct SHA-256 (the OVH one failed
  before the fix: the server blocked the phone), pause and resume.

**Bugs found and fixed that day:**
- Engine (desktop and Android): opened a burst of connections, got the client blocked by the server, then froze or
  failed. Now: one new connection at a time, learns the server's limit, honours `Retry-After`, gives a refused piece
  back to the connection it was split from. Honest `AnySave/…` client name first, browser name only after a 403.
- Extension: files were saved into the Downloads root instead of `AnySave/<type>/` (Chrome drops the folder unless
  `onDeterminingFilename` suggests it again); a job was marked done before Chrome finished writing the file; a
  taken-over download lost its real name (`report.zip` became `attach`), which also broke the file-type rule; a
  Chrome download waiting for the user's "Keep" sat at 100% forever because AnySave hides Chrome's bubble.
- Link check (`probe`, desktop and Android) used HEAD only; some servers never answer HEAD. Falls back to a 1-byte GET.
- Desktop service handed its secret token to anyone who opened `/` without one.

**Phase 4 quick pieces, added and tested the same day (desktop only, not on Android):**
- Subtitles (tick-boxes for English, Bangla and 13 other languages), chapter marks and cover art inside the
  video file: helper `extrasArgs` in `main.go`, settings `subLangs` / `embedChapters` / `embedCover`. Tested on a
  real YouTube video (subtitle tracks, 3 chapters, cover). A subtitle that can't be fetched never fails the video.
  Do not use `en.*` for languages: YouTube lists dozens of machine-translated tracks (`en-de`…) and answers 429.
- Checksum: paste a SHA-256 after the link in the manager (engine checks, a mismatch is not kept), a "check"
  button on finished rows (`hash` helper action), and `anysave --sha256 <hash> <link>`.
- Proxy (`host-go/proxy.go`, setting `proxy`): blank = system proxy (macOS `scutil --proxy`, Windows registry,
  or `HTTP(S)_PROXY`), `direct`, or `http://` / `socks5://host:port`. Used by the engine, the link check and yt-dlp.
  Tested: a download through a local HTTP proxy, in Go tests and in the browser.
- Import / export of the link list (manager toolbar; JSON export, import of that file or any text file of links).
- `anysave <link>…` command (`host-go/cli.go`): several links, `--wait`, `--sha256`, `--help`. The Mac installer
  links `anysave` into a folder on the PATH (or `~/.local/bin` plus one line in `~/.zprofile`); the Windows
  installer adds the AnySave folder to the user PATH.
- Mac app: menu-bar item is the logo with a progress ring and a finish tick (`BarIcon`; `ANYSAVE_BAR_DUMP=<folder>`
  saves the frames for testing); the web view now shows page dialogs, file pickers and Save panels.
- Not tested for these: a computer that really has a system proxy, HTTPS-through-proxy and SOCKS5 against a real
  proxy, yt-dlp with `--proxy`, the Windows PATH step and console output on Windows, the Mac installer's PATH step
  on a real home folder, and the new dialogs / Import / Export inside the Mac app (they are tested in Chrome only).

**Offline transcription (14 November 2026)** — "Transcribe" on a finished video or music row (manager page,
desktop app): pick the spoken language, get `<name>.<lang>.srt` and `<name>.<lang>.txt` next to the file.
- Engine: whisper.cpp v1.9.5. Mac: `scripts/build-whisper.sh` compiles `whisper-cli` straight with clang (no CMake),
  universal, Metal on Apple Silicon, pinned to commit `d1be6fde`; `build.sh` calls it and ships it in the package and
  the app bundle. Windows: the installer downloads the official `whisper-bin-x64.zip` from build `b5454` (same commit),
  SHA-256 pinned, into `bin\whisper\`. Helper action `transcribe` (`host-go/transcribe.go`).
- Models download on first use into the per-user app data folder (`~/Library/Application Support/AnySave/models`,
  `%LOCALAPPDATA%\AnySave\models`), SHA-256 checked: English uses `ggml-small-q8_0` (252 MB), every other language
  `ggml-large-v3-turbo-q5_0` (547 MB).
- Measured on the owner's M4 Pro: English 2 minutes in ~4 s, nearly word-perfect. Bangla 3 minutes in ~73 s,
  understandable but with many spelling mistakes (phonetic spellings, names in Latin letters).
- Things learned the hard way: auto-detect calls Bangla "Gujarati" (so the user picks the language); the small model
  produces garbage for Bangla even when told the language; Whisper spends about one token per byte on Bangla and stops
  at ~220 tokens per 30-second window, dropping ~80% of the speech, so non-English audio is cut at pauses into pieces
  of at most 7 s; `-sns` (suppress non-speech) made English text repeat itself, so it is not used.
- Not tested: Windows (CPU only there, so the 547 MB model will be slow), Intel Macs (CPU), very long videos,
  music-heavy audio, the model download through a proxy, Transcribe inside the Mac app window.

**Internal names changed (13 November 2026, at the owner's request):** every internal ID now says AnySave:
native host `com.anysave.host`, helper binary `anysave-host`, Android package `com.anysave.app`, Mac bundle
`com.anysave.desktop`, log file `anysave.log`, keystore `android/anysave-release.jks` (alias `anysave`, same key).
The extension key/ID did not change. Consequences: the Android app is a **new app** to Android (it installs beside
any older copy and does not carry over its data; older copies cannot update to it), and desktop users must run the
new installer once so the browser finds the helper under its new name. These are now the IDs that must never change.

**Built later the same day (they were listed as done in the roadmap but did not exist):**
- Scheduler and named queues (extension: "scheduler and queues" section of `background.js`; desktop:
  `host-go/schedule.go`). Settings `scheduleOn`, `scheduleStart`, `scheduleStop`, `scheduleQueue` ("" = all
  downloads), `queues` (comma-separated names). Jobs carry `queue`; a job that must wait has status `scheduled`;
  `heldBySchedule` marks one paused by the schedule; `ignoreSchedule` is set by "Start now" or a manual resume.
  Messages `startNow`, `moveQueue`. The extension checks every minute (`chrome.alarms`), the desktop service every 15 s.
- When all downloads finish: sleep, shut down, quit (`host-go/afterdone.go`, helper action `power`). A 60-second
  warning first; a new download, a changed setting or Cancel calls it off. **`ANYSAVE_DRY_POWER=1` makes the helper
  only log the action: always set it in tests.** Real sleep and shut down were never executed.
- Finish/fail notifications in the extension (`chrome.notifications`; several at once are summed up).
- The extension now needs the `notifications` and `alarms` permissions.

- Site crawler (`host-go/crawl.go`, helper action `crawl`, "Scan a website for files" panel in the manager):
  follows links of the same website up to 3 levels, honours robots.txt, 3 pages at a time with a pause, limits of
  300 pages / 2,000 files / about 50 seconds; file-type filter and "stay inside the start folder".
- Android now has the same features: subtitles / chapters / cover art, proxy (`Net.kt`), checksum (pasted with the
  link, and "Check SHA-256" on a finished file), scheduler and queues (`Schedule.kt`, status `SCHEDULED`, an alarm at
  each boundary), import / export of the link list (`Backup.kt`). On Android 12+ the system may refuse to start a
  download while the app is closed; it then starts when the app is opened (only Android 10 was tested).
  Tested on the Mi 8: subtitles + chapters + cover art inside a real YouTube video, the scheduler (waits, Start now,
  starts by itself when the schedule is switched off), checksum right and wrong. Not tested on the phone: proxy,
  moving between queues, import/export, the alarm firing at the real hour, pausing when the hours end.
- Video extras can make YouTube refuse the video now and then (HTTP 403 on the video data; seen on the phone).
  Both the helper and the Android app therefore try once more without the extras, so the video is never lost.
  `ANYSAVE_TEST_FAIL_EXTRAS=1` forces that path in the helper for tests. Android writes the raw reason of a failed
  download to the system log: `adb logcat -s AnySave`.

**CI:** the GitHub workflow (Windows, macOS, Ubuntu) has passed on every push of 13 November, so the Windows
installer and helper do install and run on real Windows there. Nobody has used the Windows app by hand.

**Still not tested:** anything on real Windows; how the Mac app window, menu-bar item, drop box, global shortcut,
clipboard watch and notifications look and behave (the test session could not take screenshots of the Mac);
the `.dmg`/`.exe` installers; self-update; Safari; refresh-expired-link; aria2c; cancel cleanup on Android;
Android engine speed against a single connection; the "Keep" prompt being answered by a person; Alt/Cmd-click and
the overlay on sites other than the five built-in ones (needs the optional all-sites permission);
a real Option-click (the test simulated it in two steps).

**Other known gaps:**
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
- Native host name `com.anysave.host`, binary `anysave-host`, extension ID `jgkodjdjabccfmfbfmiinmppmcpgomai`
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
  `chrome://extensions`. For extension-only changes you can `rsync -a --delete extension/ ~/AnySave/extension/`.
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
  Use a fixed `userDataDir`, put `com.anysave.host.json` there pointing to a wrapper script that runs the helper with
  `HOME=<scratch>/home` (so test downloads don't go to the owner's real Downloads) and symlink `bin/` to
  `~/Library/Application Support/AnySave/bin` for yt-dlp/ffmpeg/deno.
- **Helper alone:** send one native-messaging frame (4-byte little-endian length + JSON) on stdin and read the reply,
  e.g. `{"action":"playlist","url":"https://www.youtube.com/@BlenderStudio/shorts"}`. Real downloads: use the Blender
  channel shorts or `https://www.youtube.com/watch?v=aqz-KE-bpKQ` (Big Buck Bunny).
- **Android:** release build + `adb install -r`; drive with `adb shell am start -n com.anysave.app/.MainActivity
  -a android.intent.action.SEND -t text/plain --es android.intent.extra.TEXT '<url>'`, `adb shell input tap/swipe`,
  `adb exec-out screencap -p > shot.png`. Screen is 1080×2248.
- Go: `go vet ./... && GOOS=windows go vet ./...` in `host-go/`. Go may be at `~/.local/go/bin`.
- **Engine trace:** run the helper with `ANYSAVE_TRACE=1` to get one line per HTTP request in `anysave.log`
  (range, status, `Retry-After`). This is how the connection-burst bug was found.
- **Be gentle with public test servers.** `proof.ovh.net` allows about 4 requests, then answers 429 with
  `Retry-After: 10`, and silently blocks the IP for 10+ minutes after a burst of refused requests. Use the local
  torture server (`host-go/torture`) for anything repetitive.
- Chrome for Testing: headless works with `enableExtensions`; set the download folder through
  `<profile>/Default/Preferences` (`download.default_directory`). Content scripts only run on the five built-in
  sites unless the optional all-sites permission is granted; to test them locally, launch with
  `--host-resolver-rules=MAP dl.youtube.com 127.0.0.1` (links must still point to `127.0.0.1`, the helper can't
  resolve the fake name). An automated Alt-click does not start a download.
- Mac app without touching the owner's files: launch `AnySave.app/Contents/MacOS/AnySave` with `HOME` and
  `CFFIXED_USER_HOME` set to a scratch folder.
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
the host (use a Job Object); Mac `osascript` file picker may open behind Chrome (fix added 13 Nov: the script activates itself first; not yet confirmed by a person, because testing it pops a dialog on the owner's screen).
Extension: no `_locales`/i18n; manager tabs/chips/progress bars lack ARIA roles; no `:focus-visible` styles;
no notifications on finish/fail.
Android: `targetSdk` 34 (Play needs 35); no predictive back; "Save location" row (now opens Downloads/AnySave, falling back to the system Downloads screen); no themed icon;
no localization; dark-mode launch flash (window theme is light).
