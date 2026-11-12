# AnySave: notes for AI assistants

Start by reading `docs/HANDOFF.md` (current state, architecture, the owner's rules, how to test) and
`docs/ROADMAP.md` (what to build next). The README's "Things that are easy to break" section also applies.

Rules that matter most:
- Android: ship only `./gradlew assembleRelease` (R8). Save `mapping.txt` as `dist-assets/r8-mapping-<version>.txt`.
- Never rename `com.anysave.host`, `anysave-host`, `com.anysave.app` or the extension `key`/ID.
- Ask before committing; branch, then fast-forward `main` and push. The owner publishes GitHub releases himself:
  prepare the notes + exact `gh release create` command.
- Test for real (Puppeteer + Chrome for Testing, the helper directly, `adb` on his phone) before saying something works,
  and say plainly what wasn't tested.
- Explain things to the owner in plain, simple language.
