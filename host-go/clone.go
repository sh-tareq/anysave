package main

// Copy a voice (Voice Lab): about 10–20 seconds of one person talking becomes a voice that can read
// any English text, offline. The model is Kyutai's Pocket TTS (CC-BY-4.0) as exported for
// sherpa-onnx; it needs no transcript, only the sample. A voice is only made after the person says
// it's their own voice or they have permission (the page asks; the answer is stored with the voice).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	cloneMin = 5.0  // seconds of sound needed at least
	cloneMax = 20.0 // seconds kept (longer doesn't help and is slower)
)

func init() {
	voicePacks["pocket"] = voicePack{ID: "pocket", Label: "voice copying", Files: []voiceFile{{
		URL:    sherpaRel + "tts-models/sherpa-onnx-pocket-tts-int8-2026-01-26.tar.bz2",
		SHA256: "2f3b88823cbbb9bf0b2477ec8ae7b3fec417b3a87b6bb5f256dba66f2ad967cb", Size: 98336520,
		Keep: func(e string) string {
			e = stripTop(e)
			if strings.HasPrefix(e, "test_wavs") || e == "README.md" {
				return ""
			}
			return e
		}}}}
}

// myVoice is a copied voice, kept in <models>/voice/mine/<id>/ as voice.wav + voice.json.
type myVoice struct {
	Name    string  `json:"name"`
	Made    string  `json:"made"`
	Consent string  `json:"consent"` // what the person ticked, word for word
	Secs    float64 `json:"secs"`
}

func myVoicesDir() string { return filepath.Join(voiceRoot(), "mine") }

func myVoices() []ttsVoice {
	ents, _ := os.ReadDir(myVoicesDir())
	type found struct {
		v    ttsVoice
		made string
	}
	var fs []found
	for _, e := range ents {
		dir := filepath.Join(myVoicesDir(), e.Name())
		var m myVoice
		b, err := os.ReadFile(filepath.Join(dir, "voice.json"))
		if err != nil || json.Unmarshal(b, &m) != nil || !isFile(filepath.Join(dir, "voice.wav")) {
			continue
		}
		fs = append(fs, found{ttsVoice{ID: "my-" + e.Name(), Name: m.Name, Lang: "Your voices", Pack: "pocket", Code: "en",
			ref: filepath.Join(dir, "voice.wav")}, m.Made})
	}
	sort.Slice(fs, func(a, b int) bool { return fs[a].made < fs[b].made })
	out := make([]ttsVoice, len(fs))
	for i, f := range fs {
		out[i] = f.v
	}
	return out
}

func pocketArgs(v ttsVoice, speed float64) []string {
	k := func(n string) string { return filepath.Join(packDir("pocket"), n) }
	return []string{"--pocket-lm-flow=" + k("lm_flow.int8.onnx"), "--pocket-lm-main=" + k("lm_main.int8.onnx"),
		"--pocket-encoder=" + k("encoder.onnx"), "--pocket-decoder=" + k("decoder.int8.onnx"),
		"--pocket-text-conditioner=" + k("text_conditioner.onnx"), "--pocket-vocab-json=" + k("vocab.json"),
		"--pocket-token-scores-json=" + k("token_scores.json"), "--reference-audio=" + v.ref,
		"--speed=" + ff3(speed)}
}

const consentText = "This is my own voice, or I have the person's clear permission to copy it."

// copyVoice turns a recording (from `at` seconds) into a saved voice.
func (s *cvServer) copyVoice(j *cvJob, path, name string, at float64) map[string]any {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "My voice"
	}
	name = clip(name, 40)
	taken := map[string]bool{}
	for _, v := range myVoices() {
		taken[strings.ToLower(v.Name)] = true
	}
	for i, base := 2, name; taken[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	mi := probeMedia(path)
	if mi.Duration > 0 && !mi.HasAudio {
		return fail(filepath.Base(path) + " has no sound.")
	}
	if at < 0 || (mi.Duration > 0 && at > mi.Duration-cloneMin) {
		at = 0
	}
	// Clean the sample when the strong clean-up is here: background noise gets copied too otherwise.
	src, start := path, at
	if packReady("engine") && packReady("clean-strong") {
		if c, err := j.denoise(path, "strong", at, cloneMax+1, cloneMax+1); err == nil {
			src, start = c, 0
		} else {
			logf("voice copy: clean-up skipped: %v", err)
		}
	}
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	tmp := filepath.Join(myVoicesDir(), id+".part")
	os.MkdirAll(tmp, 0o755)
	wav := filepath.Join(tmp, "voice.wav")
	a := []string{}
	if start > 0 {
		a = append(a, "-ss", ff3(start))
	}
	a = append(a, "-i", src, "-vn", "-map", "0:a:0", "-ac", "1", "-ar", "24000",
		// drop silence at the start, then keep up to cloneMax seconds
		"-af", "silenceremove=start_periods=1:start_threshold=-45dB,highpass=f=70,atrim=0:"+ff3(cloneMax)+",loudnorm=I=-18:TP=-2,aresample=24000",
		"-c:a", "pcm_s16le", wav)
	if err := j.runFF(a, cloneMax, "wav"); err != nil {
		os.RemoveAll(tmp)
		return fail(errMsg(err))
	}
	rate, pcm, err := readWav(wav)
	secs := 0.0
	if err == nil && rate > 0 {
		secs = float64(len(pcm)/2) / float64(rate)
	}
	if secs < cloneMin {
		os.RemoveAll(tmp)
		if mi.Duration-at >= cloneMin {
			return fail(fmt.Sprintf("AnySave heard less than %d seconds of clear talking in it. Record again somewhere quiet, close to the microphone, and talk for 10–20 seconds.", int(cloneMin)))
		}
		return fail(fmt.Sprintf("That's too short: AnySave needs at least %d seconds of someone talking (10–20 is best).", int(cloneMin)))
	}
	b, _ := json.MarshalIndent(myVoice{Name: name, Made: time.Now().Format(time.RFC3339), Consent: consentText, Secs: secs}, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, "voice.json"), b, 0o644); err != nil {
		os.RemoveAll(tmp)
		return fail("Couldn't save the voice: " + err.Error())
	}
	if err := os.Rename(tmp, filepath.Join(myVoicesDir(), id)); err != nil {
		os.RemoveAll(tmp)
		return fail("Couldn't save the voice: " + err.Error())
	}
	logf("voice copy: made %s (%.1f s)", id, secs)
	r := voiceState()
	r["made"] = "my-" + id
	r["secs"] = secs
	return r
}

func forgetVoice(id string) map[string]any {
	id = strings.TrimPrefix(id, "my-")
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return fail("Unknown voice.")
	}
	dir := filepath.Join(myVoicesDir(), id)
	if !isFile(filepath.Join(dir, "voice.json")) {
		return fail("That voice is already gone.")
	}
	if err := os.RemoveAll(dir); err != nil {
		return fail("Couldn't delete the voice: " + err.Error())
	}
	return voiceState()
}

// voiceCopyOp is the page's "Make voice" (needs consent) and "Delete".
func (s *cvServer) voiceCopyOp(ctx context.Context, req map[string]any, j *cvJob) map[string]any {
	if str(req, "op") == "voiceForget" {
		return forgetVoice(str(req, "voice"))
	}
	if b, _ := req["consent"].(bool); !b {
		return fail("Tick the box to say it's your voice or you have permission.")
	}
	path, ok := s.readable(str(req, "path"))
	if !ok {
		return fail("That file isn't there any more, or AnySave may not read it.")
	}
	return s.copyVoice(j, path, str(req, "name"), num(fmt.Sprint(req["at"])))
}
