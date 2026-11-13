# AnySave Bangla speech model

`ggml-large-v3-turbo-bn-q5_0.bin` (574,041,195 bytes) — kept here, out of git (too big) and out of
`dist/` (which `scripts/build.sh` empties on every build).
SHA-256 `f54478ad383b01304cfc4d6bde52f9f1af01e2885c7d3939383e041b593829ac`

This is **Mozilla.ai's `whisper-large-v3-turbo-bn`** — OpenAI's Whisper large-v3-turbo fine-tuned on
Common Voice Bangla — converted to whisper.cpp's ggml format and compressed to q5_0, so AnySave's
offline "Transcribe" can use it. Nothing else was changed.

- Original model: https://huggingface.co/mozilla-ai/whisper-large-v3-turbo-bn
  (Copyright Mozilla.ai; licensed under the Apache License, Version 2.0)
- Base model: https://huggingface.co/openai/whisper-large-v3-turbo (OpenAI, MIT licence)
- License: Apache License 2.0 — https://www.apache.org/licenses/LICENSE-2.0
- Modifications by AnySave: format conversion (safetensors float32 → ggml float16 with
  `host-go/cmd/convert-whisper`, header taken from `ggml-large-v3-turbo-q5_0.bin`), then
  quantization to q5_0 with whisper.cpp v1.9.5's `quantize` tool.

Not hosted yet. When the owner decides to ship it: upload this file with this README as the notice
(e.g. a GitHub release `models-bn-1` with `--latest=false`) and put the download URL in
`banglaModelURL` in `host-go/transcribe.go`.

To rebuild it yourself from Mozilla's original:

```bash
go run ./host-go/cmd/convert-whisper ggml-large-v3-turbo-q5_0.bin config.json model.safetensors ggml-bn-f16.bin
whisper-quantize ggml-bn-f16.bin ggml-large-v3-turbo-bn-q5_0.bin q5_0
```
