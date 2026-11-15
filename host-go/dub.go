package main

// Dubbing: translate captions into another language and speak them over the video, all on this computer.
//
//	translate  {lines: [{s,e,t}], from, to, model} -> an SRT in the new language (one line per sentence,
//	           with the original times)
//	dub        [video, translated .srt] {voice, keep} -> the video with the new voice, the original sound
//	           turned down underneath (or off)
//
// Translation is LMT-60 (NiuTrans, Apache-2.0, built on Qwen3), 60 languages, run by llama.cpp's
// llama-server (MIT), started for the job on 127.0.0.1 with a random key, and stopped after. The voices are
// Voice Lab's (voice.go). Engine and model download on first use, like the voices.
//
// What real tests showed (15 November 2026, M4 Pro): LMT-60 4B translated English <-> Bangla naturally and
// correctly, Hindi and Spanish well; the 1.7B model is close but a little less natural. Google's MADLAD-400
// 3B dropped words and merged sentences, so it isn't used. Translated speech is usually longer than the
// original: each sentence is said faster (up to 1.4x, with the voice's own speed control), and if it still
// doesn't fit the next one waits, so voices never overlap.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	cvTranslate = "translate"
	cvDub       = "dub"
	llamaBuild  = "b12413"
)

const lmtRev = "https://huggingface.co/mradermacher/"

func init() {
	voicePacks["mt-large"] = voicePack{ID: "mt-large", Label: "translation model", Files: []voiceFile{{
		URL:  lmtRev + "LMT-60-4B-GGUF/resolve/fd76914adb08e4aa076ef7044e0f3b54d5987a5d/LMT-60-4B.Q4_K_M.gguf",
		Name: "model.gguf", SHA256: "6ab245a422b854b4582401c1240b64c35899fe1c816af059aa23b38ac9707bb4", Size: 2716068544}}}
	voicePacks["mt-small"] = voicePack{ID: "mt-small", Label: "smaller translation model", Files: []voiceFile{{
		URL:  lmtRev + "LMT-60-1.7B-GGUF/resolve/f03d51b43463d8a457eee1cd765220de69e9bac8/LMT-60-1.7B.Q4_K_M.gguf",
		Name: "model.gguf", SHA256: "54c6244f100330264cef607188413ee82cb8a254f0cf950011aaa9cee74f7eb5", Size: 1282439264}}}
	if p := llamaPack(); len(p.Files) > 0 {
		voicePacks["llama"] = p
	}
}

func llamaPack() voicePack {
	rel := "https://github.com/ggml-org/llama.cpp/releases/download/" + llamaBuild + "/llama-" + llamaBuild + "-bin-"
	var f voiceFile
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		f = voiceFile{URL: rel + "macos-arm64.tar.gz", SHA256: "d224cf39222f62fe5da9504e812fcd97622996507c8432aa8d6c6bb182144dd7", Size: 16939960}
	case "darwin/amd64":
		f = voiceFile{URL: rel + "macos-x64.tar.gz", SHA256: "49f0e998e195463eb18f457bdc0894a168a4380da3da99dbddf681b0b2b61bd9", Size: 12687920}
	case "windows/amd64":
		f = voiceFile{URL: rel + "win-cpu-x64.zip", SHA256: "4d2e557f15ecdde59ff1be87f7cd208e43075abde93de29d61e7bbd7cb78004c", Size: 25142557}
	default:
		return voicePack{ID: "llama", Label: "translation engine"}
	}
	f.Keep = func(e string) string {
		base := filepath.Base(e)
		if base == exe("llama-server") || strings.HasSuffix(base, ".dylib") || strings.HasSuffix(base, ".dll") {
			return base // all in one folder: the server finds its libraries beside it
		}
		return ""
	}
	return voicePack{ID: "llama", Label: "translation engine", Files: []voiceFile{f}}
}

// lmtLangs are LMT-60's languages: code -> the English name the model is prompted with.
var lmtLangs = map[string]string{
	"ar": "Arabic", "en": "English", "es": "Spanish", "de": "German", "fr": "French", "it": "Italian", "ja": "Japanese",
	"nl": "Dutch", "pl": "Polish", "pt": "Portuguese", "ru": "Russian", "tr": "Turkish", "zh": "Chinese",
	"bg": "Bulgarian", "bn": "Bengali", "cs": "Czech", "da": "Danish", "el": "Greek", "fa": "Persian", "fi": "Finnish",
	"hi": "Hindi", "hu": "Hungarian", "id": "Indonesian", "ko": "Korean", "nb": "Norwegian", "no": "Norwegian",
	"ro": "Romanian", "sk": "Slovak", "sv": "Swedish", "th": "Thai", "uk": "Ukrainian", "vi": "Vietnamese",
	"am": "Amharic", "az": "Azerbaijani", "bo": "Tibetan", "he": "Hebrew", "hr": "Croatian", "hy": "Armenian",
	"is": "Icelandic", "jv": "Javanese", "ka": "Georgian", "kk": "Kazakh", "km": "Khmer", "ky": "Kyrgyz", "lo": "Lao",
	"mr": "Marathi", "ms": "Malay", "my": "Burmese", "ne": "Nepali", "ps": "Pashto", "si": "Sinhala", "sw": "Swahili",
	"ta": "Tamil", "te": "Telugu", "tg": "Tajik", "tl": "Tagalog", "ug": "Uyghur", "ur": "Urdu", "uz": "Uzbek",
	"yue": "Cantonese",
}

// ---------- llama-server ----------

type llamaServer struct {
	base, key string
	stop      func()
	client    *http.Client
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// startLlama runs llama-server with the model until stop() (or ctx ends). Only this job knows its key.
func startLlama(ctx context.Context, model string) (*llamaServer, error) {
	bin := filepath.Join(packDir("llama"), exe("llama-server"))
	if !isFile(bin) {
		return nil, cvError("The translation engine is missing. Try again to download it.")
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	key := newToken()
	threads := runtime.NumCPU() - 2
	if threads < 2 {
		threads = 2
	}
	cmd := command(bin, "-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--api-key", key,
		"-c", "4096", "-np", "1", "-t", strconv.Itoa(threads), "--offline", "--no-webui")
	var log tailBuffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		return nil, cvError("Couldn't start the translation engine: " + err.Error())
	}
	attachChildJob(cmd)
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			killTree(cmd)
			<-exited
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-exited:
		}
	}()
	s := &llamaServer{base: "http://127.0.0.1:" + strconv.Itoa(port), key: key, stop: stop, client: &http.Client{Timeout: 3 * time.Minute}}
	deadline := time.Now().Add(3 * time.Minute) // a big model on a slow disk takes a while to load
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			logf("llama-server exited: %s", log.String())
			return nil, cvError("The translation engine stopped while starting. Your computer may not have enough free memory.")
		case <-ctx.Done():
			return nil, errCancelled
		case <-time.After(300 * time.Millisecond):
		}
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(s.base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s, nil
			}
		}
	}
	stop()
	return nil, cvError("The translation engine took too long to start.")
}

// translateOne asks the model for one sentence.
func (s *llamaServer) translateOne(ctx context.Context, text, from, to string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	prompt := "<|im_start|>user\nTranslate the following text from " + from + " into " + to + ":\n" + from + ": " + text +
		"\n" + to + ":<|im_end|>\n<|im_start|>assistant\n"
	n := len([]rune(text))*4 + 64 // a long translation is fine; a runaway one is not
	if n > 1024 {
		n = 1024
	}
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "n_predict": n, "temperature": 0, "cache_prompt": true,
		"stop": []string{"<|im_end|>", "<|im_start|>"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/completion", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errCancelled
		}
		return "", cvError("The translation engine stopped answering.")
	}
	defer resp.Body.Close()
	var out struct {
		Content string `json:"content"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || json.Unmarshal(b, &out) != nil {
		logf("translate: HTTP %d: %s", resp.StatusCode, clip(string(b), 300))
		return "", cvError("The translation engine gave an error.")
	}
	return cleanTranslation(out.Content, to), nil
}

// cleanTranslation keeps just the translated sentence: no "Bengali:" label, no extra lines.
func cleanTranslation(s, to string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "[end of text]", ""))
	s = strings.TrimPrefix(s, to+":")
	if i := strings.Index(s, "\n\n"); i > 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), " ")
}

// ---------- the translate job ----------

type dubLine struct {
	S float64 `json:"s"`
	E float64 `json:"e"`
	T string  `json:"t"`
}

func mtPack(model string) string {
	if model == "small" {
		return "mt-small"
	}
	return "mt-large"
}

// translateJob: opts.lines (JSON [{s,e,t}]), opts.from, opts.to, opts.model (large|small). Writes an SRT.
func (j *cvJob) translateJob(out string) error {
	var lines []dubLine
	if err := json.Unmarshal([]byte(j.spec.Opts["lines"]), &lines); err != nil || len(lines) == 0 {
		return cvError("There are no captions to translate.")
	}
	from, ok1 := lmtLangs[j.spec.opt("from")]
	to, ok2 := lmtLangs[j.spec.opt("to")]
	if !ok1 || !ok2 {
		return cvError("Pick the spoken language and the language to translate into.")
	}
	if from == to {
		return cvError("The captions are already in " + to + ".")
	}
	pack := mtPack(j.spec.opt("model"))
	if !packReady("llama") || !packReady(pack) {
		if err := j.sub(0, 40).voiceDownload("llama", pack); err != nil {
			return err
		}
		j = j.sub(40, 60)
	}
	j.stage("Starting the translation engine")
	srv, err := startLlama(j.ctx, filepath.Join(packDir(pack), "model.gguf"))
	if err != nil {
		return err
	}
	defer srv.stop()
	j.stage("Translating into " + to)
	total := 0
	for _, l := range lines {
		total += len(l.T) + 20
	}
	done := 0
	var sb strings.Builder
	for i, l := range lines {
		if err := j.check(); err != nil {
			return err
		}
		t := strings.TrimSpace(l.T)
		if t != "" {
			if t, err = srv.translateOne(j.ctx, t, from, to); err != nil {
				return err
			}
		}
		fmt.Fprintf(&sb, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(l.S), srtTime(l.E), t)
		done += len(l.T) + 20
		j.progress(float64(done) * 100 / float64(total))
	}
	return os.WriteFile(out, []byte("\ufeff"+sb.String()), 0o644)
}

// ---------- the dub job ----------

// dubJob: paths = video and the translated SRT; opts.voice, opts.keep ("quiet" default, "off", "half").
func (j *cvJob) dubJob(video, srt, out string) error {
	v, ok := voiceByID(j.spec.opt("voice"))
	if !ok {
		return cvError("Pick a voice first.")
	}
	b, err := os.ReadFile(srt)
	if err != nil {
		return cvError("Couldn't read the translated captions.")
	}
	subs := parseSRT(strings.TrimPrefix(string(b), "\ufeff"))
	if len(subs) == 0 {
		return cvError("There are no translated lines to speak.")
	}
	mi := probeMedia(video)
	if !packReady("engine") || !packReady(v.Pack) {
		if err := j.sub(0, 25).voiceDownload("engine", v.Pack); err != nil {
			return err
		}
		j = j.sub(25, 75)
	}
	j.stage("Speaking the new language")
	wav, err := j.sub(0, 85).dubTrack(subs, v, mi.Duration)
	if err != nil {
		return err
	}
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	vol := "0.12"
	switch j.spec.opt("keep") {
	case "off":
		vol = "0"
	case "half":
		vol = "0.35"
	}
	a := []string{"-i", video, "-i", wav}
	if mi.HasAudio && vol != "0" {
		a = append(a, "-filter_complex", "[0:a:0]aresample=48000,volume="+vol+"[bg];[1:a]aresample=48000[dub];"+
			"[bg][dub]amix=inputs=2:duration=longest:normalize=0,loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000[a]",
			"-map", "0:v:0", "-map", "[a]")
	} else {
		a = append(a, "-filter_complex", "[1:a]loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000[a]", "-map", "0:v:0", "-map", "[a]")
	}
	a = append(a, "-c:v", "copy")
	if extOf(out) == "webm" && encoders(ff)["libopus"] {
		a = append(a, "-c:a", "libopus", "-b:a", "128k")
	} else {
		a = append(a, "-c:a", "aac", "-b:a", "192k")
	}
	a = append(a, "-ac", "2")
	if extOf(out) == "mp4" || extOf(out) == "mov" || extOf(out) == "m4v" {
		a = append(a, "-movflags", "+faststart")
	}
	j.stage("Saving")
	return j.sub(85, 15).runFF(append(a, out), math.Max(mi.Duration, 1), extOf(out))
}

// dubTrack speaks every line at its time and returns one WAV with the whole new voice track.
func (j *cvJob) dubTrack(subs []subtitle, v ttsVoice, videoLen float64) (string, error) {
	const gap = 0.08
	var track []byte // 16-bit mono at the voice's rate
	rate := 0
	free := 0.0 // when the previous line has finished
	for i, sub := range subs {
		if err := j.check(); err != nil {
			return "", err
		}
		text := strings.TrimSpace(sub.Text)
		if text == "" {
			continue
		}
		next := videoLen
		for k := i + 1; k < len(subs); k++ {
			if strings.TrimSpace(subs[k].Text) != "" {
				next = subs[k].Start
				break
			}
		}
		start := math.Max(sub.Start, free)
		slot := math.Max(0.3, next-start-gap)
		if next <= 0 {
			slot = math.Inf(1)
		}
		dir, _ := os.MkdirTemp(j.work, "line")
		say := func(speed float64) (int, []byte, error) {
			w, err := speak(j.ctx, dir, text, v, speed, func(float64) {})
			if err != nil {
				return 0, nil, err
			}
			return readWav(w)
		}
		r, pcm, err := say(1)
		if err != nil {
			return "", err
		}
		if d := float64(len(pcm)/2) / float64(r); d > slot {
			// Say it faster with the voice's own speed control (up to 1.4x); past that, the next line waits.
			if r2, pcm2, err := say(math.Min(d/slot, 1.4)); err == nil {
				r, pcm = r2, pcm2
			}
		}
		os.RemoveAll(dir)
		rate = r
		at := int(start*float64(r)) * 2
		if at > len(track) {
			track = append(track, make([]byte, at-len(track))...)
		}
		track = append(track[:at], pcm...)
		free = start + float64(len(pcm)/2)/float64(r) + gap
		j.progress(float64(i+1) * 100 / float64(len(subs)))
	}
	if rate == 0 {
		return "", cvError("There are no translated lines to speak.")
	}
	out := filepath.Join(j.work, "dub.wav")
	return out, writeWav(out, rate, track)
}

// dubOutput: the same kind of video as the input when FFmpeg can copy the picture into it.
func dubOutput(video string) string {
	switch e := extOf(video); e {
	case "mp4", "mov", "m4v", "mkv", "webm":
		return e
	}
	return "mp4"
}

// langName is the English name of a language code ("bn" -> "Bengali"), for file names.
func langName(code string) string {
	if n, ok := lmtLangs[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// translateDir holds finished translations until the page reads them back; the page only
// saves a real subtitle file when asked ("Save translated subtitles").
func translateDir() string { return filepath.Join(os.TempDir(), "anysave-translate") }
