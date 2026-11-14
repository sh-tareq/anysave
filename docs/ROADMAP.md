# AnySave roadmap: "IDM and beyond"

Goal (owner's words): make AnySave **really advanced, ultra high tech, robust and flawless**, with every useful
feature of Internet Download Manager (IDM) plus AnySave's own, as a **desktop app (Mac + Windows) + browser
extension + Android app**. The extension works alone; extension + app together unlock everything.

Status: **Phases 0–3 are built, with gaps.** They were first tested for real on 13 November 2026 (until then only
by automated tests); see HANDOFF.md §2a for what was verified and fixed. The scheduler, named queues, after-done sleep/shut down/quit and
extension notifications and the site crawler were added that day, and Android got the same features.
Phase 4 has started: subtitles / chapters / cover art, checksum check, proxy, import/export and the `anysave <link>`
command are done on desktop (HANDOFF.md §2a). Offline transcription (whisper.cpp) is done on desktop (HANDOFF.md §2a). Still open in phase 4: FTP,
synchronization, Defender scan, smart file names, searchable transcripts, transcription on Android, phone ↔ computer,
Firefox, Bangla UI.

---

## 1. Product shape

| | Extension only | App only | Extension + app |
|---|---|---|---|
| Files, caught website downloads, ad libraries | ✅ Chrome's downloader | ✅ paste/drop a link | ✅ fast engine |
| YouTube + 1,000 sites | ❌ needs the app | ✅ | ✅ |
| Popup, floating video button, right-click | ✅ | ❌ | ✅ |
| Downloads continue with the browser closed | ❌ | ✅ | ✅ |
| Scheduler, sleep/shut down when done | ❌ | ✅ | ✅ |
| Any browser (Safari, Firefox, …) | ❌ | ✅ copy/share/drop | ✅ |

**Who handles downloads** (setting + quick switch in the popup): *AnySave (fast)* (recommended default) /
*Chrome* / *Ask each time*. Plus rules: minimum size, file types, sites to leave to Chrome, Alt-click = Chrome for
this one, Ctrl-click = force AnySave. Automatic fallback to Chrome when a server refuses multi-connection or the
login. Login-protected downloads need cookies for any site → ask for the optional `<all_urls>` permission only when
the user turns this on.

**Hand-over to Chrome:** the engine downloads fast, then (phase 1) offers the finished file to Chrome from a
one-time `127.0.0.1` URL so it appears in Chrome's own download list (native drag/open/show); phase 2 streams it
live so Chrome shows it from the first second with no copy at the end. Bind to 127.0.0.1 only, random port,
one-time token, serve only that file.

## 2. Phases

### Phase 0: foundations (do first)
- GitHub Actions on **windows-latest, macos-latest, ubuntu-latest** (free for public repos), `workflow_dispatch` + on push:
  - build helper; run `go test` (engine tests once they exist);
  - **Windows:** build the zip and run `install.ps1` for real (downloads, checksum checks, registry keys for
    Chrome/Edge/Brave/Chromium, "busy" check), then run the installed helper: `--check`, an `info`, a `playlist`,
    a short real download, cancel (taskkill), `open` on a missing file; check Downloads paths;
  - **Mac:** same with `install-mac.sh` (it ends with `open -R`, harmless in CI);
  - **Extension:** Puppeteer + Chrome for Testing with the native host registered in the profile (see HANDOFF §5);
  - **Android:** `assembleRelease` + unit tests; emulator smoke test if feasible.
- Engine "torture" test server (Go `httptest`): drops connections mid-way, no Range support, slow, file changes
  during download (ETag/Last-Modified + `If-Range`), wrong/absent Content-Length, redirects, 403 on second
  connection. Engine must never produce a corrupt file: verify size, and hash when the server gives one.
- Beta and stable release channels; signed update manifests (e.g. minisign/Ed25519) so a hacked server can't
  push malware; rollback.
- "Copy diagnostics" button (log + versions) instead of telemetry ("no tracking" promise).

### Phase 1: the engine (Go package `host-go/engine` or a new module), used by the current helper
- Multi-connection HTTP(S) downloads with **dynamic segmentation** (split the largest remaining part when a
  connection finishes), **adaptive connection count** (start ~4, grow while throughput rises, up to 32,
  per-host limit), connection reuse, HTTP/2 and HTTP/3 (quic-go) where available.
- Resume after network loss / sleep / restart (persist segment map next to the part file); validate with
  ETag/Last-Modified/If-Range; retries with backoff.
- "Who handles downloads" switch + rules + Alt/Ctrl click + fallback; hand-over to Chrome when finished.
- Speed test: 1 connection vs engine on large public test files; show the owner the numbers.

### Phase 2: desktop app (as built: a Swift menu-bar app on Mac and a hand-written tray app on Windows, both showing the extension's manager UI served by the Go helper; Wails was the plan but was not used)
- Menu bar (Mac) / tray (Windows) app with live progress; downloads continue with Chrome closed.
- The extension talks to the app (keep the native host name `com.anysave.host` for compatibility).
- Floating drop box, global shortcut (download copied link), system-wide clipboard watch, Mac Share menu /
  Windows "Send to", native notifications and sounds.
- Real installers (`.dmg`, `.exe`), self-update of the app and yt-dlp with signature checks.
- Code signing before public launch: Apple Developer ($99/yr, notarization). Windows: Azure Trusted Signing
  (~$10/mo) is limited to some countries; from Bangladesh use **Certum's open-source code signing certificate**
  (~€25–70/yr).
- Safari Web Extension bundled inside the Mac app.

### Phase 3: IDM essentials and grabbing
- Refresh expired link (re-capture new URL, continue same parts), speed limiter (global/per download),
  named queues, scheduler, after-done actions (sleep/shut down/quit/open), duplicate warning, notifications,
  per-connection progress view + speed graph + statistics.
- Multi-connection video downloads: yt-dlp `--concurrent-fragments`, aria2c as external downloader.
- Floating "Download" button over videos on any page; "download everything on this page" (filters by type/size);
  site crawler (depth, filters); pattern batch (`file[01-99].zip`); drag-and-drop links.

### Phase 4: pro, AI and reach
- Proxy/SOCKS (system proxy by default), FTP, synchronization (re-download changed files), Windows Defender scan
  after download, command line (`anysave <url>`), import/export lists, checksum verify (paste a SHA-256),
  subtitles / chapters / cover art (yt-dlp), smart file names.
- **Offline AI:** transcribe any video/ad with whisper.cpp locally, searchable swipe file, subtitles in Bangla/English,
  smart names/categories. Strong fit for the owner's AI/marketing audience.
- Phone ↔ computer over local Wi-Fi (send links/files, no cloud).
- Firefox version, Android gets the same engine (Kotlin) + youtubedl-android `aria2c` module, Bangla UI.

## 3. IDM feature map (for completeness)

Have now: browser takeover (via Chrome's downloader), Chrome/Edge/Brave, right-click, categories, video streams
(HLS/DASH via yt-dlp, 1,000+ sites), MP3/MP4 conversion, resume for videos, playlists/batch.
Build: everything in phases 1–4 above.
Skip: dial-up hang-up, MMS, skins (we have light/dark), system-wide capture in non-browser apps (not possible from
an extension; common antivirus trigger).
Never use IDM's name or look in the product.

## 4. Confirmed architecture & product decisions

Confirmed with the owner (12 November 2026) — industry-standard defaults locked in:

1. **App toolkit:** planned as Wails v2; **built instead** as a native Swift app (Mac, `installers/mac/app/main.swift`)
   and a hand-written tray app (Windows, `host-go/desktop_windows.go`) around the Go helper's local service and
   the shared HTML/CSS/JS manager UI. No Wails dependency.
2. **Build order:** **Engine first (Phase 1), then Desktop App (Phase 2)**.
3. **Code signing:** **Sign before public desktop app launch in Phase 2** (unsigned builds + Ed25519 signed update manifests until then).
4. **Default "Who handles downloads":** **AnySave (fast engine) with automatic silent fallback to Chrome** (plus `Alt`-click to bypass AnySave, `Ctrl`-click to force AnySave).
5. **All-sites access:** **A normal host permission (`*://*/*`) since 1.2.0**, so the AnySave button works on videos on any site (the extension is installed unpacked, so Chrome grants it without a prompt). Chrome logins (cookies) are still only sent to the helper when the user turns that on.
6. **Pricing & architecture:** **100% Free & Open Source forever** (local-first, zero telemetry, no accounts or license servers).
