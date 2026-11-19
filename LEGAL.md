# AnySave: Legal notice, disclaimer and privacy

*Last updated: 9 November 2026 · AnySave is made by Shahidul H Tareq*

## 1. What AnySave is for

AnySave is a personal tool for saving videos and files **you are allowed to save**, and for converting your own
media, for example:

- content you made or uploaded yourself
- content whose owner has given you permission
- content published under a licence that allows downloading (such as Creative Commons) or in the public domain
- uses your local law allows, such as personal backup or fair use / fair dealing

## 2. Copyright warning

Most videos on YouTube, Facebook, Instagram and TikTok are protected by copyright and belong to the people who made them.
**Downloading, copying, re-uploading, sharing, selling or publicly showing someone else's video without permission can
infringe copyright, and in many countries it can be a civil or criminal offence.** It may also break the terms of service
of the site you downloaded from, which can get your account suspended.

Copyright laws differ from country to country. If you are not sure whether you may download something, **don't**.
Ask the owner, or ask a lawyer.

## 3. Your responsibility

By using AnySave you agree that:

- you are solely responsible for what you download and how you use it
- you will only download content you have the right to download
- you will not use AnySave to infringe anyone's copyright or other rights, or to get around paywalls,
  DRM or other access restrictions
- you will follow the terms of service of each website you use

## 4. No warranty, no liability

AnySave is provided **"as is"**, free of charge, without any warranty of any kind. Websites change often, and AnySave may
stop working at any time. To the fullest extent the law allows, the author (Shahidul H Tareq) is **not liable** for any
loss, damage, claim or legal consequence arising from your use or misuse of AnySave or of any downloaded content.

## 5. Not affiliated

AnySave is an independent project. It is **not affiliated with, endorsed by or sponsored by** YouTube or Google,
Meta (Facebook, Instagram), TikTok or ByteDance. Their names are used only to describe which websites AnySave works with.
All trademarks belong to their owners.

## 6. Privacy

- AnySave has **no account, no analytics, no ads and no tracking**. The author's servers receive nothing from you.
- Downloads go directly from the website to your device, and conversions happen on your device.
- To tell a file from a web page, AnySave may first ask that website about the link (a small request that downloads nothing).
- **Desktop:** when "Catch downloads from websites" is on, AnySave sees the downloads you start in your browser
  (their address and file name) so it can show and sort them. This stays on your computer. Turn it off in the manager.
- If you log in to a site (Android) or tick "Use my Chrome login" (desktop), the login cookies stay **on your device**.
  They are sent only to that same website. On desktop they are written to a temporary file that is deleted as soon as
  the download finishes.
- Voice Lab and dubbing download their engines, voices and translation model once, when first used (from GitHub and
  Hugging Face). Your recordings, text and translations never leave your computer.
- **Copied voices** (Voice Lab): the voice sample and the copy are kept only on your computer, in AnySave's models
  folder, until you delete them. You may only copy your own voice or a voice whose owner has clearly agreed, and you
  must not use a copied voice to pretend to be someone or to mislead people. You are responsible for how you use it.
- AnySave connects to GitHub to download and update yt-dlp. The desktop installer also downloads ffmpeg
  (Mac: ffmpeg.martin-riedl.de, Windows: GitHub).

## 7. Copyright complaints

If you believe AnySave is being used to infringe your rights, contact the author. Note that AnySave hosts no
content, so infringing copies are on the user's own device and the original stays on the website it came from.

## 8. Open-source software

AnySave is built on these open-source projects:

| Component | Licence | Where |
|---|---|---|
| yt-dlp | The Unlicense (public domain) | all platforms |
| youtubedl-android | GPL-3.0 | Android app |
| FFmpeg | LGPL-2.1 / GPL (depending on build) | all platforms |
| Python | PSF License | Android app (bundled), desktop (inside yt-dlp) |
| QuickJS | MIT | Android app (bundled) |
| Jetpack Compose, AndroidX, Kotlin | Apache-2.0 | Android app |
| Coil | Apache-2.0 | Android app |
| Go standard library | BSD-3-Clause | desktop helper |
| pdfcpu | Apache-2.0 | desktop helper (PDF tools) |
| bodgit/sevenzip | BSD-3-Clause | desktop helper (reads 7z archives) |
| nwaples/rardecode | BSD-2-Clause | desktop helper (reads RAR archives; AnySave never makes RAR files) |
| ulikunitz/xz | BSD-3-Clause | desktop helper (XZ, LZMA and LZMA2: reads .xz/.tar.xz, packs 7z archives) |
| golang.org/x/text | BSD-3-Clause | desktop helper (old ZIP file names in the DOS code page) |
| bsdtar / libarchive (part of macOS and Windows 10/11) | BSD-2-Clause | desktop helper reads ISO disc images with the system's own copy; not shipped with AnySave |
| PDF.js | Apache-2.0 | extension and desktop (PDF previews) |
| whisper.cpp and the Whisper models | MIT | desktop (transcription, captions); Android app (Captions: whisper.cpp bundled, model downloaded on first use) |
| Noto Sans, Noto Sans Bengali, Montserrat, Anton, Hind Siliguri | SIL Open Font License 1.1 | desktop helper and Android app (caption fonts; licences in `host-go/fonts/licences` and the app's `assets/fonts/licences`) |
| Poppins ExtraBold, Bebas Neue, Bangers, Lobster | SIL Open Font License 1.1 | desktop helper (more caption fonts; licences in `host-go/fonts/licences`) |
| Permanent Marker | Apache-2.0 | desktop helper (caption font; licence in `host-go/fonts/licences`) |
| andybalholm/brotli | MIT | desktop helper (unpacks downloads a server sent compressed) |
| klauspost/compress | BSD-3-Clause | desktop helper (unpacks downloads a server sent compressed; also used by the archive tools) |
| sherpa-onnx (k2-fsa) and ONNX Runtime (Microsoft) | Apache-2.0 / MIT | desktop Voice Lab (downloaded on first use); Android app (Studio: bundled) |
| LLVM libc++ (from the Android NDK) | Apache-2.0 with LLVM exception | Android app (bundled, needed by the voice engine) |
| Kitten TTS micro v0.8 (KittenML), sherpa-onnx build | Apache-2.0 | Android app, English read aloud (downloaded on first use) |
| Kokoro-82M v1.0 voices (hexgrad), sherpa-onnx int8 build | Apache-2.0 | desktop Voice Lab, read aloud; Android app, read aloud in 6 more languages |
| Piper voice bn_BD "google" medium (rhasspy) | MIT (Piper); trained on Google's Bengali speech data, OpenSLR 37, CC BY-SA 4.0, and the CMU Indic voices (Carnegie Mellon University, Language Technologies Institute, © 2009-2017) | desktop Voice Lab and Android app, Bangla read aloud |
| eSpeak NG pronunciation data | GPL-3.0 | desktop Voice Lab and Android app (read aloud) |
| DPDFNet (Ceva) | Apache-2.0 | desktop Voice Lab and Android app, strong clean-up |
| GTCRN | MIT | desktop Voice Lab and Android app, light clean-up |
| Pocket TTS (Kyutai), ONNX export by KevinAHM for sherpa-onnx | CC-BY-4.0 (model), MIT (code); © Kyutai, https://huggingface.co/kyutai/pocket-tts. Unchanged, except unneeded sample files are left out | desktop Voice Lab and Android app, copy a voice |
| llama.cpp (ggml-org) | MIT | desktop dubbing, translation (downloaded on first use) |
| LMT-60 4B / 1.7B translation models (NiuTrans, built on Qwen3 by Alibaba Cloud) | Apache-2.0 | desktop dubbing, translation |

Because the Android app includes youtubedl-android (GPL-3.0), **the AnySave Android app is distributed under the
GNU General Public License v3.0**. You may get its complete source code from the author, and you may use, study,
change and share it under that licence. See `LICENSE`.
