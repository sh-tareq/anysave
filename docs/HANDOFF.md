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
| Desktop extension | 1.2.0 | `extension/manifest.json` → `"version"` |
| Android app | 1.2.0 (versionCode 13), package `com.anysave.app` | `android/app/build.gradle.kts`. Not released; the last published APK is 1.0.3 under the old package. `dist-assets/r8-mapping-1.2.0.txt` already exists from an earlier, never-published build: replace it with the real 1.2.0 mapping when the release is built. |
| Helper & Desktop App | 1.2.0 | `host-go/main.go` → `helperVersion` (also `installers/mac/app/Info.plist`, and `DefaultUserAgent` in `host-go/engine/engine.go`). The desktop app is **not Wails**: on Mac it is a small Swift menu-bar app (`installers/mac/app/main.swift`, WKWebView) and on Windows a hand-written tray app (`host-go/desktop_windows.go`). Both show the extension's `manager.html`, served by the helper's background service (`host-go/daemon.go`, `anysave-host --daemon`). |
| Latest GitHub release | **v1.0.5**. 1.2.0 is not published: the owner wants to check everything first. | github.com/sh-tareq/anysave/releases |

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
- Subtitles into the video (`host-go/subs.go`, helper action `subs`), two buttons on a finished transcript:
  "embed" adds a switchable subtitle track without re-encoding (`<name> (subtitles).mp4`, language-tagged);
  "burn" draws the captions onto the picture (`<name> (captions).mp4`, libx264), smaller text for portrait videos.
  Bangla is shaped correctly (libass + HarfBuzz in the installed FFmpeg; Kohinoor Bangla on Mac, Nirmala UI on
  Windows). Files are worked on as copies with plain names, so names with ' : etc. are fine.
- Transcripts tab in the manager (`host-go/search.go`, helper action `searchTranscripts`): lists every
  `<name>.<lang>.srt` found under Downloads/AnySave and the folders of Transcribe jobs (so clearing the download list
  doesn't lose them), and searches a word or phrase in any language, showing the time it was said. Case, punctuation
  and zero-width joiners are ignored; a phrase split over two subtitle lines is found. Open text / open video use
  `openPath`, which the helper only allows inside Downloads/AnySave. Spelling mistakes in Bangla transcripts mean a
  correctly spelled search word can miss: there is no fuzzy matching yet.
- Bangla model (14 Nov): Mozilla.ai's `whisper-large-v3-turbo-bn` (Apache-2.0), converted with
  `host-go/cmd/convert-whisper` (pure Go, no PyTorch; header from the general turbo model) and compressed to q5_0
  with whisper.cpp's quantize tool. Side by side on the BBC clip: correct spelling and Bangla names instead of
  phonetic spelling, ~23% more speech, 60 s instead of 73 s for 3 minutes. **Not hosted yet:** `banglaModelURL`
  in `transcribe.go` is "-", so Bangla keeps using the general model unless the file is already in the models
  folder. To ship it: upload `dist-assets/models/ggml-large-v3-turbo-bn-q5_0.bin` (with `dist-assets/models/README.md` as the
  notice) as a GitHub release (suggested tag `models-bn-1`, `--latest=false`) and put its download URL in
  `banglaModelURL`. SHA-256 `f54478ad…29ac`, 574,041,195 bytes.
- Repetition loops ("কোনো কোনো কোনো…"): `collapseRepeats` keeps one copy of a word or phrase (up to 8 words)
  repeated 3+ times in a row; `-mc 0` was tried and made the text much shorter, so previous-text context stays on.
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

**Mac menu-bar mode (tested on the owner's Mac, 2026-11-14):** closing the window (or ⌘W) removes the Dock icon and AnySave keeps running from the menu bar; reopening brings both back; the login entry (`~/Library/LaunchAgents/com.anysave.desktop.plist`, run through `launchctl bootstrap`) starts it with `--background`, no window, no Dock icon; "Start at Login" in the menu bar removes and re-adds it. Not tried: a real log-out/log-in.

**Still not tested:** anything on real Windows; how the Mac app window, menu-bar item, drop box, global shortcut,
clipboard watch and notifications look and behave (the test session could not take screenshots of the Mac);
the `.dmg`/`.exe` installers; self-update; Safari; refresh-expired-link; aria2c; cancel cleanup on Android;
Android engine speed against a single connection; the "Keep" prompt being answered by a person; Alt/Cmd-click on
sites other than the test page; the video button on Facebook and Instagram feeds (need a login), YouTube's
hover previews, TikTok's main feed and X (tested: a YouTube video, Meta Ad Library, TikTok Creative Center,
an ordinary site); a real Option-click (the test simulated it in two steps).

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
                  (manager colours and wording since 15 Nov 2026: file badge, "46% · 4.2 MB/s · 1 min 12 s left")
  manager.*       full download manager (IDM-style since 15 Nov 2026): top add-link box, sidebar (status,
                  file types, queues, tools, each with a count), labelled toolbar acting on the selected rows,
                  sortable table (Name/Size/Status/Speed/Time left), details panel for the selected row
                  (all per-file actions: Verify, Transcribe, Subtitles, Convert, new link, queue), status bar
                  (speed graph, speed limit, "when done"), Settings on its own page, and the
                  "before you download" window (`#addDlg`: helper `probe` for files, `info` for videos with
                  sizes per quality; setting `askBeforeAdd`). Views: downloads, convert, transcripts, grab,
                  settings (`showTab(name)`, also from the URL hash). Same file runs in the desktop app
                  window; `desktop-bridge.js` puts its "Desktop app" card into `#desktopSettings`.
                  Tested by pt/t13.js (plus t1–t12).
  capture.js      TikTok Creative Center: remembers ads that played on hover
  video-overlay.js  AnySave icon button on videos (any site, also embedded players): click → quality
                  menu from the helper's `info` for the video's own page (found from the post/thumbnail
                  link on YouTube, Facebook, Instagram, TikTok, X); ad libraries and plain video files
                  → "Video file". Tested by pt/t12.js.
host-go/        native messaging helper (Go, no deps). Actions: info, playlist, download (streams progress),
                pause/cancel (via messages), convert (ffmpeg), probe, pickFile, fileInfo, reveal, open.
                Finds yt-dlp, ffmpeg, deno in ./bin next to itself.
installers/     mac/install-mac.sh (shipped as "Install AnySave.command"), windows/install.ps1 + .bat.
                Download yt-dlp, ffmpeg, Deno with SHA-256 verification; register the native host.
android/        Kotlin + Jetpack Compose app, youtubedl-android (yt-dlp + ffmpeg), bundled QuickJS
                (app/src/main/jniLibs/*/libqjs.so) as YouTube's JS runtime. Same look as the manager since
                15 Nov 2026: fixed colours in Theme.kt (`AnySaveColors.status` for progress/ok/warn/err, file
                type badge colours), one row design for Home and Downloads (DownloadRow.kt: badge or thumbnail,
                "42% · 1.2 MB/s · 0:12 left", amber Paused, green Done), Downloads tab with filters, search,
                sort, long-press multi-select, details sheet (Open/Share/Copy link/Check SHA-256/Delete/Remove),
                the "before you download" window (setting `askBeforeAdd`, rename before saving), Settings in
                cards. While yt-dlp prepares a video (YouTube 10 s to minutes; HLS never gives a percent) the
                row says "Getting the video…" instead of a bare "Starting…".
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

- 15 Nov 2026 (late night): **Copy a voice** in Voice Lab's "Read text aloud" tab (`extension/voice.js`, helper
  `host-go/clone.go`). Record (up to 20 s, MediaRecorder), choose or drop a recording, name it, tick *"This is my own
  voice, or I have the person's clear permission to copy it"* (the helper refuses without it and stores that sentence
  with the voice), then it appears under the language **"Your voices"**, reads text and can be used for English dubbing.
  Model: Kyutai **Pocket TTS** (CC-BY-4.0, ~100M), sherpa-onnx int8 export (pack `pocket`, 98 MB, pinned SHA-256), run by
  the same sherpa-onnx 1.13.8 CLI (`--pocket-*`, `--reference-audio`, `--speed`). English only (Kyutai's newer French/
  German/Spanish/Portuguese/Italian/Dutch models would need our own ONNX export). Samples: 5–20 s kept, cleaned with the
  strong clean-up when it's installed, silence at the start cut, 24 kHz mono, saved in `<models>/voice/mine/<id>/`
  (voice.wav + voice.json); delete removes the folder. Rejected: ZipVoice (weights CC-BY-NC, Emilia data), VoxCPM2 and
  Chatterbox (would need a Python/torch engine). No model clones Bangla. Mac app: `NSMicrophoneUsageDescription` added
  and the web view grants the microphone only to the helper's 127.0.0.1 page (`requestMediaCapturePermissionFor`).
  Tested: `TestVoiceCopyForReal` (noisy video → voice in ~1.5 s, noise floor −32 dB → silent, whisper heard the reading
  word for word; a WebM recording of speech works; a plain tone is rejected with a clear message; no voice without
  consent; delete can't leave the folder). The page in Chrome for Testing and app mode: drop/choose, consent gate,
  make, Listen, save MP3, record (with a stand-in microphone playing speech, because headless Chrome's fake microphone
  hangs on this Mac), delete, reload keeps voices, narrow layout, no page errors. **Not tested:** a real microphone,
  the in-app download of the `pocket` pack (LuLu firewall blocks test builds; placed from the same pinned file with
  AnySave's own unpack), the real Mac app window (incl. the microphone prompt; the Swift change only compiled),
  Windows, Intel Mac. A WebM recording shows its length as "Infinity" in the sample player (Chrome's recorder doesn't
  write a duration); the voice is still made correctly.
- 15 Nov 2026 (late night): **Dubbing** — a "Dub into another language" card on the Captions page (`extension/captions.js`,
  helper `host-go/dub.go`). Captions are grouped into sentences (sentence ends, pauses over 1 s, or 220 chars), then
  **translated offline** by LMT-60 (NiuTrans, Apache-2.0, 60 languages) through llama.cpp's `llama-server` (MIT, build
  b12413) on 127.0.0.1 with a random port and API key. Two model packs: Better (4B Q4_K_M, 2.7 GB) and Smaller
  (1.7B Q4_K_M, 1.3 GB); llama.cpp itself is a 12–25 MB pack. All pinned by SHA-256 like the voice packs (`unpack()`
  now reads .tar.bz2/.tar.gz/.zip). MADLAD was tried and dropped (it skipped words). The owner then edits the lines,
  hears any line, picks a voice and how loud the original sound stays (quietly underneath / half / off), and makes the
  dubbed video (converter tool `dub`: each line spoken, sped up to 1.4× if it doesn't fit its slot, placed on one track,
  mixed with FFmpeg, video copied) or saves the translated SRT. Languages with no voice (e.g. German) say "subtitles
  only". The translation is written to `$TMPDIR/anysave-translate` (the page may read that folder), so only files the
  owner asks for land in Converted. Edits are kept with the video's caption project.
  Tested: `TestDubForReal` (English→Bangla with Piper, English→Spanish with Kokoro; 17 s for both incl. engine start);
  whisper heard the Spanish dub word for word, the Bangla dub recognisably. The whole card in Chrome for Testing and in
  app mode: translate (5 s), edit a line, ▶ a line, make the dub (6 s), save SRT (edit kept), reload keeps the lines,
  German = subtitles only, no page errors. **Not tested:** the in-app *download* of llama.cpp/LMT (this Mac's LuLu
  firewall blocks every fresh test build, so packs were placed by hand from the same pinned files and unpacked with
  AnySave's own `unpack`); the Smaller model in the app (tested only in research); the real Mac app window; Windows;
  an Intel Mac.
- 15 Nov 2026 (night): **Voice Lab** (Chrome extension + Mac app, `extension/voice.js`, helper `host-go/voice.go`).
  Two tabs. *Clean up a recording*: Strong (DPDFNet-2 48 kHz), Light (GTCRN 16 kHz) or Basic (FFmpeg afftdn, no
  download), plus "Even out the loudness" (loudnorm −16 LUFS); a 12-second before/after preview from any point; saves
  the video with clean sound or just the audio (converter tool `voiceclean`). *Read text aloud*: Kokoro v1.0 (English
  US/UK, Spanish, French, Hindi, Italian, Brazilian Portuguese, Chinese; Japanese left out because it came back as
  nonsense) and Piper Bangla (16 voices, only fair); speed, Listen (first 400 chars or the selection), open/drop a
  TXT/SRT/VTT, save MP3/M4A/WAV (converter tool `speak`, no input file). The engine is sherpa-onnx 1.13.8's two CLI tools,
  **not bundled**: engine and each model "pack" download on first use into `<models>/voice/<pack>/` with pinned
  SHA-256s, a 60-second stall limit, and a `.ready` marker; Piper's model is turned into a sherpa-onnx one in Go
  (ONNX metadata appended as protobuf field 14, tokens.txt from `phoneme_id_map`). Long text is read in ~600-character
  pieces (sentence ends; pauses between paragraphs); long recordings are cleaned in 5-minute pieces.
  Windows uses the win-x64 MT build (no VC++ runtime needed) but is **untested**; Intel Mac uses osx-x64 (untested).
  Voice cloning and dubbing are not built (cloning only ever with a consent step).
  Tested: `TestVoiceLabForReal` (`ANYSAVE_VOICE_TEST=1`, real downloads into a scratch HOME): every pack downloaded,
  checked and unpacked; English/Spanish/Bangla saved as MP3/M4A/WAV; a noisy video cleaned three ways (noise floor
  −32 dB → −83 strong, −64 light, −53 basic; whisper heard the cleaned speech word for word; length kept). The whole page
  in Chrome for Testing and through the daemon (app mode): in-page download, previews (0.3–1.8 s), saves, Listen,
  SRT→text, reload keeps text. Hugging Face's CDN dropped Go connections for a few minutes during testing, hence the
  retries and stall limit. Not tested: the real Mac app window, Windows, an Intel Mac.

- 15 Nov 2026 (evening): **Captions page** (Chrome extension + Mac app, `extension/captions.js`, helper
  `host-go/cv_captions.go`). Drop/choose a video (or "Edit captions" on a finished video or transcript) → Make captions
  (the offline speech engine; `transcribe` now also asks whisper for `-ojf` and keeps per-word times in
  `<app data>/AnySave/captions/<hash of the .srt path>.json`, not next to the video) → edit lines (Enter splits,
  ⌫ joins, times, find/replace, move all, words per line, undo) → six looks (Clean, Bold pop, Headline, Boxed,
  Word by word, Bangla) with font/size/colours/outline/box/position and word highlight (light up, pop, appear) →
  save SRT/VTT/ASS/text or "Make video with captions" (converter tool `captions`: the page writes an ASS file, FFmpeg
  burns it with `fontsdir` = the bundled fonts). Fonts are built into the helper (`host-go/fonts`, OFL) and served to
  the page at `/cv/font`, so preview and result use the same letters; sizes convert with each font's win line height
  (measured: FFmpeg's font size = win ascent+descent). Bangla runs switch to Noto Sans Bengali / Hind Siliguri; other
  scripts fall back to system fonts in both. Boxed uses libass BorderStyle 4 (one box per caption). The page keeps
  each video's edits in localStorage (name+size). Tested: helper transcription + word timings, preview vs burned frame
  side by side (Bold pop, Bangla, Boxed match), the whole page in Chrome for Testing and through the daemon, and in the
  real Mac window (WKWebView) up to transcription + fonts (the screen was locked, so its look and burn weren't
  checked there). Also: preview temp folders of a closed AnySave are swept on start; English uses the bigger speech
  model when only that one is downloaded.

- 15 Nov 2026 (later): Android app redesigned to match the manager (see Architecture); popup and video button
  menu restyled with the manager colours; Android/helper show an IP address as the site name. Tested on the Mi 8
  (release build): YouTube download, before-download window + rename, speed limit, pause/resume, multi-select +
  remove, share-to-AnySave, search, sort, Check SHA-256; then Retry, Refresh expired link (only the missing pieces
  were fetched, SHA-256 matched), Wi-Fi only (network marked metered with `cmd netpolicy`), schedule (waits, then
  starts), auto-download of a copied YouTube link, Convert to MP3. Fixed on the way: file errors now say what to do
  ("The site refused the download (error 403). If the link has expired, use Refresh expired link"; the engine used
  to report a 403 as "Connection closed before the piece finished"); the before-download window no longer shows an
  error page's size ("1 KB") and warns when the site answered with an error. Found: MIUI holds back the schedule's
  alarm while AnySave is restricted in the background (the alarm fired the moment the restriction was lifted);
  Settings now shows a tip on Xiaomi/Redmi/POCO (or when Android reports the restriction) that opens App info
  (Autostart, Battery → No restrictions). Not tested on the phone: dark mode (MIUI ignores the adb switch), Quick
  Settings tile, Convert with trim or resize.
- 15 Nov 2026: manager page redesigned in an IDM style (see Architecture); helper `info` gives an estimated size
  per quality; "Use my Chrome login & retry" on video login errors; a late update for a removed download no longer
  brings it back as an empty row (extension and helper); Mac window remembers its size, minimum 720×480.
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

## 8. Open items (checked against the code on 14 November 2026)

Done since the original audit: log rotation (2 MB), Windows Downloads via the Known Folder API, Windows child
processes in a Job Object, extension notifications, a yt-dlp update button (desktop app), Android "Save location",
uninstallers (`Uninstall AnySave.command`, `Uninstall AnySave.bat` + `uninstall.ps1`), plain-language converter
errors, and a version check between extension and helper (helper action `version`; the manager shows a notice and
new actions fail with "Run the AnySave installer again" when the helper is older).

Still open:
- Desktop: no code signing, by the owner's decision (14 Nov 2026: no Apple, Microsoft or Google developer accounts
  for now); the install instructions explain the first-open warnings on Mac, Windows and Android; the Mac file
  picker fix is unconfirmed by a person; no automatic update of the helper itself.
- Extension: no `_locales`/i18n (Bangla UI).
- Android: `targetSdk` 34 (only matters for Google Play); no predictive back; no themed icon; no localization;
  dark-mode launch flash; no transcription.
- Not tested by a person: everything on Windows (CI installs, transcribes a sample and uninstalls there), the Mac
  app window/menu-bar icon, real sleep/shut down, the uninstallers on a real computer.
