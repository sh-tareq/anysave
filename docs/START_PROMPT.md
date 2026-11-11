# Prompt to continue AnySave in a new AI session

Paste this into the new session (Claude Code, or another AI coding tool) opened in the project folder
`/Users/shahidulhaque/App Dev/VidGrab` (or a clone of github.com/sh-tareq/anysave).

---

You're continuing work on **AnySave**, my free, open-source video and file downloader: a Chrome/Edge/Brave
extension with a Go native helper for Mac and Windows, plus an Android app. Another AI session built the
current version (v1.0.5) with me and left a full handover.

Before doing anything:
1. Read `docs/HANDOFF.md` completely (state, architecture, my rules, how to test), then `docs/ROADMAP.md`
   (the plan), then the README section "Things that are easy to break".
2. Look at the code you'll touch before changing it (`extension/`, `host-go/`, `installers/`, `android/`).
3. Reply with a short summary of where the project stands, and the open decisions in ROADMAP §4 with your
   recommendation for each. Wait for my answers before building.

Then we'll start with **Phase 0 (foundations)** from the roadmap: GitHub Actions tests on Windows, Mac and Linux,
including actually running the Windows installer (never tested on real Windows yet), plus the engine
torture-test server. After that, **Phase 1**: the multi-connection download engine and the "who handles
downloads: Chrome or AnySave" switch.

How I want you to work:
- Explain things in plain, simple language. I follow along on my phone too.
- Keep a short task list, work in small steps, and test for real before saying something works:
  Puppeteer + Chrome for Testing for the extension, the helper directly, and `adb` on my Android phone.
  Tell me plainly what you couldn't test.
- Android: release builds only (`./gradlew assembleRelease`, R8), save the mapping file, install on my phone
  with `adb install -r` (keeps my data).
- Ask before committing. Branch, then fast-forward `main` and push. I publish GitHub releases myself: give me
  the release notes file and the exact `gh release create` command.
- Never rename the internal "vidgrab" IDs (`com.vidgrab.host`, `vidgrab-host`, `com.vidgrab.app`, extension key).
- My goal: really advanced, ultra high tech, robust and flawless. Suggest better ideas when you see them.
