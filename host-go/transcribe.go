package main

// Offline transcription with whisper.cpp: speech in a downloaded video or audio file becomes
// text (.txt) and subtitles (.srt) saved next to it. Nothing leaves the computer except the
// one-time download of the speech model.
//
//	{"action":"transcribe","path":"/…/video.mp4","lang":"bn"}
//	-> progress… then {"type":"done","file":"/…/video.bn.srt","srt":…,"txt":…,"lang":"bn"}
//
// What real tests showed (13 November 2026, M4 Pro):
//   - English: the small model (252 MB) is fast and nearly word-perfect, read in normal
//     30-second windows.
//   - Bangla: the small model fails completely; the large-v3-turbo model (547 MB) works when
//     told the language (auto-detect took Bangla for Gujarati). Whisper writes Bangla with
//     about one token per byte and stops after ~220 tokens per 30-second window, which drops
//     most of the speech. So for every language but English the audio is cut at pauses into
//     pieces of a few seconds, each well under that limit.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type speechModel struct {
	Name   string // file name (on Hugging Face ggerganov/whisper.cpp unless URL is set)
	SHA256 string
	Size   int64
	Label  string
	URL    string // where to download it, when not from ggerganov/whisper.cpp
}

var (
	modelStandard = speechModel{"ggml-small-q8_0.bin", "49c8fb02b65e6049d5fa6c04f81f53b867b5ec9540406812c643f177317f779f", 264464607, "standard speech model (252 MB)", ""}
	modelBest     = speechModel{"ggml-large-v3-turbo-q5_0.bin", "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2", 574041195, "speech model (547 MB)", ""}
	// Bangla: Mozilla.ai's large-v3-turbo fine-tuned on Common Voice Bangla (Apache-2.0,
	// mozilla-ai/whisper-large-v3-turbo-bn), converted to ggml and compressed to q5_0 for AnySave.
	// In a side-by-side test on a BBC Bangla report it spelled words and names correctly where the
	// general model wrote them phonetically, and caught ~23% more of the speech.
	modelBangla = speechModel{"ggml-large-v3-turbo-bn-q5_0.bin", "f54478ad383b01304cfc4d6bde52f9f1af01e2885c7d3939383e041b593829ac", 574041195,
		"Bangla speech model (547 MB)", banglaModelURL}
	modelURLBase = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"
	langCodeRe   = regexp.MustCompile(`^(auto|[a-z]{2,3})$`)
)

const (
	pieceMax = 7.0 // seconds; Bangla fits easily within the token limit at this length
	pieceMin = 2.0
)

// modelsDir is the per-user app data folder (not inside the app, so models survive updates):
// ~/Library/Application Support/AnySave/models on a Mac, %LOCALAPPDATA%\AnySave\models on Windows.
func modelsDir() string {
	var base string
	var err error
	if runtime.GOOS == "windows" {
		base, err = os.UserCacheDir() // %LOCALAPPDATA%: big files don't belong in the roaming profile
	} else {
		base, err = os.UserConfigDir()
	}
	if err != nil {
		return filepath.Join(appDir, "models")
	}
	return filepath.Join(base, "AnySave", "models")
}

// chooseModel: English is fast and accurate with the standard model; everything else needs the
// better one (the standard model produced garbage for Bangla).
func chooseModel(lang, quality string) speechModel {
	switch quality {
	case "standard":
		return modelStandard
	case "best":
		return modelBest
	}
	switch lang {
	case "en":
		return modelStandard
	case "bn":
		// Until the converted model is hosted, use it only if it is already on this computer.
		if banglaModelURL != "-" || isFile(filepath.Join(modelsDir(), modelBangla.Name)) {
			return modelBangla
		}
	}
	return modelBest
}

// banglaModelURL is where the converted Bangla model is downloaded from ("-" = not hosted yet).
const banglaModelURL = "-"

// ensureModel returns the model's path, downloading and checking it the first time.
func ensureModel(ctx context.Context, m speechModel, client *http.Client, progress func(done, total int64)) (string, error) {
	dir := modelsDir()
	path := filepath.Join(dir, m.Name)
	if st, err := os.Stat(path); err == nil && st.Size() == m.Size {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	url := m.URL
	if url == "" {
		url = modelURLBase + m.Name
	}
	if url == "-" {
		return "", fmt.Errorf("the %s isn't available for download yet", m.Label)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", fileUA)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn't download the speech model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("couldn't download the speech model (HTTP %d)", resp.StatusCode)
	}
	part := path + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256*1024)
	last := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(part)
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if time.Since(last) > 300*time.Millisecond {
				last = time.Now()
				progress(done, m.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return "", fmt.Errorf("the speech model download was interrupted: %w", rerr)
		}
	}
	f.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != m.SHA256 {
		os.Remove(part)
		return "", fmt.Errorf("the downloaded speech model is damaged (checksum mismatch); please try again")
	}
	if err := os.Rename(part, path); err != nil {
		return "", err
	}
	return path, nil
}

// silenceRe reads ffmpeg's silencedetect output.
var silenceRe = regexp.MustCompile(`silence_(start|end): (-?[0-9.]+)`)

// cutPoints picks where to cut the audio: in a pause, so that no piece is longer than pieceMax.
// silences are [start,end] pairs. A stretch without any pause is cut hard at pieceMax.
func cutPoints(total float64, silences [][2]float64) []float64 {
	var mids []float64
	for _, s := range silences {
		if s[1] > s[0] {
			mids = append(mids, (s[0]+s[1])/2)
		}
	}
	sort.Float64s(mids)
	var cuts []float64
	start := 0.0
	for total-start > pieceMax {
		best := -1.0
		for _, m := range mids {
			if m > start+pieceMin && m <= start+pieceMax {
				best = m // the last pause that still keeps the piece short enough
			}
		}
		if best < 0 {
			best = start + pieceMax
		}
		cuts = append(cuts, best)
		start = best
	}
	return cuts
}

// srt helpers ------------------------------------------------------------------------------

type subtitle struct {
	Start, End float64
	Text       string
}

var srtTimeRe = regexp.MustCompile(`(\d+):(\d+):(\d+)[,.](\d+)\s*-->\s*(\d+):(\d+):(\d+)[,.](\d+)`)

func parseSRT(text string) []subtitle {
	var out []subtitle
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, l := range lines {
			m := srtTimeRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			n := func(k int) float64 { v, _ := strconv.ParseFloat(m[k], 64); return v }
			start := n(1)*3600 + n(2)*60 + n(3) + n(4)/1000
			end := n(5)*3600 + n(6)*60 + n(7) + n(8)/1000
			t := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			if t != "" {
				out = append(out, subtitle{start, end, t})
			}
			break
		}
	}
	return out
}

func srtTime(s float64) string {
	if s < 0 {
		s = 0
	}
	ms := int64(s*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

func writeSRT(subs []subtitle) string {
	var b strings.Builder
	for i, s := range subs {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(s.Start), srtTime(s.End), s.Text)
	}
	return b.String()
}

// cleanText removes broken characters (a letter cut in half at a piece boundary) and
// whisper's non-speech markers.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = collapseRepeats(s)
	s = strings.ReplaceAll(s, "঵", "") // a stray Bengali code point the model sometimes emits
	return strings.TrimSpace(s)
}

// the action ------------------------------------------------------------------------------

var whisperProgressRe = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

func actionTranscribe(msg map[string]any) {
	fail := func(m string) { send(map[string]any{"type": "error", "message": m}) }
	in := str(msg, "path")
	if !isFile(in) {
		fail("The file isn't there any more. It may have been moved or deleted.")
		return
	}
	ff := findTool("ffmpeg")
	wc := findTool("whisper-cli")
	if ff == "" || wc == "" {
		fail("The speech engine isn't installed. Run the AnySave installer again.")
		return
	}
	lang := strings.ToLower(strings.TrimSpace(str(msg, "lang")))
	if lang == "" {
		lang = "auto"
	}
	if !langCodeRe.MatchString(lang) {
		fail("Unknown language: " + lang)
		return
	}
	model := chooseModel(lang, str(msg, "quality"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelled atomic.Bool
	var running atomic.Pointer[exec.Cmd]
	go func() { // Chrome closes stdin (or sends anything) on cancel
		for {
			m, err := readMessage()
			if err != nil || str(m, "action") == "cancel" || str(m, "action") == "pause" {
				break
			}
		}
		cancelled.Store(true)
		cancel()
		if c := running.Load(); c != nil {
			killTree(c)
		}
	}()
	stopped := func() bool {
		if cancelled.Load() {
			fail("Cancelled")
			return true
		}
		return false
	}
	progress := func(pct float64, what, eta string) {
		send(map[string]any{"type": "progress", "percent": float64(int(pct*10)) / 10, "speed": what, "eta": eta})
	}

	// 1. The speech model (first time only).
	client := &http.Client{Transport: &http.Transport{Proxy: proxyFor(str(msg, "proxy"))}}
	modelPath, err := ensureModel(ctx, model, client, func(done, total int64) {
		progress(float64(done)/float64(total)*100, "Downloading "+model.Label+" (once)", "")
	})
	if stopped() {
		return
	}
	if err != nil {
		fail(err.Error())
		return
	}

	// 2. The sound, as whisper wants it.
	work, err := os.MkdirTemp("", "anysave-transcribe-")
	if err != nil {
		fail(err.Error())
		return
	}
	defer os.RemoveAll(work)
	wav := filepath.Join(work, "audio.wav")
	progress(0, "Reading the sound…", "")
	if out, err := commandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-y", "-i", in, "-vn", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav).CombinedOutput(); err != nil {
		if stopped() {
			return
		}
		if strings.Contains(string(out), "does not contain any stream") || strings.Contains(string(out), "matches no streams") {
			fail("This file has no sound to transcribe.")
		} else {
			fail("Couldn't read the sound of this file.")
		}
		logf("transcribe ffmpeg: %s", out)
		return
	}
	total := probeDuration(wav)

	// 3. Pieces: one for English, short ones cut at pauses for other languages.
	inputs := []string{wav}
	starts := []float64{0}
	if lang != "en" && lang != "auto" && total > pieceMax {
		out, _ := commandContext(ctx, ff, "-hide_banner", "-nostats", "-i", wav, "-af", "silencedetect=noise=-32dB:d=0.25", "-f", "null", "-").CombinedOutput()
		if stopped() {
			return
		}
		var sil [][2]float64
		var open float64 = -1
		for _, m := range silenceRe.FindAllStringSubmatch(string(out), -1) {
			v, _ := strconv.ParseFloat(m[2], 64)
			if m[1] == "start" {
				open = v
			} else if open >= 0 {
				sil = append(sil, [2]float64{open, v})
				open = -1
			}
		}
		cuts := cutPoints(total, sil)
		if len(cuts) > 0 {
			var times []string
			for _, c := range cuts {
				times = append(times, strconv.FormatFloat(c, 'f', 3, 64))
			}
			pat := filepath.Join(work, "p%05d.wav")
			if out, err := commandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-y", "-i", wav, "-f", "segment",
				"-segment_times", strings.Join(times, ","), "-c", "copy", pat).CombinedOutput(); err != nil {
				if stopped() {
					return
				}
				logf("transcribe segment: %s", out)
				fail("Couldn't prepare the sound for transcription.")
				return
			}
			inputs, starts = nil, append([]float64{0}, cuts...)
			for i := range starts {
				inputs = append(inputs, filepath.Join(work, fmt.Sprintf("p%05d.wav", i)))
			}
		}
	}

	// 4. whisper.
	threads := runtime.NumCPU()
	if threads > 8 {
		threads = 8
	}
	args := []string{"-m", modelPath, "-l", lang, "-osrt", "-t", strconv.Itoa(threads), "-pp"} // not -sns: it made the text repeat itself in tests
	if len(inputs) == 1 {
		args = append(args, "-ml", "60", "-sow") // subtitle lines of a readable length
	}
	args = append(args, inputs...)
	cmd := command(wc, args...)
	cmd.Dir = work
	stderr, _ := cmd.StderrPipe()
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		fail("Couldn't start the speech engine: " + err.Error())
		return
	}
	attachChildJob(cmd)
	running.Store(cmd)
	began := time.Now()
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	piece := 0
	var tail []string
	for sc.Scan() {
		line := sc.Text()
		if len(tail) < 40 {
			tail = append(tail, line)
		} else {
			tail = append(tail[1:], line)
		}
		var pct float64 = -1
		switch {
		case strings.HasPrefix(line, "main: processing"):
			piece++
			if len(inputs) > 1 {
				pct = float64(piece-1) / float64(len(inputs)) * 100
			}
		case len(inputs) == 1:
			if m := whisperProgressRe.FindStringSubmatch(line); m != nil {
				pct, _ = strconv.ParseFloat(m[1], 64)
			}
		}
		if pct >= 0 {
			if pct > 99 {
				pct = 99
			}
			eta := "?"
			if pct > 3 {
				eta = humanETA(strconv.FormatFloat(time.Since(began).Seconds()/pct*(100-pct), 'f', 0, 64))
			}
			progress(pct, "Transcribing", eta)
		}
	}
	err = cmd.Wait()
	if stopped() {
		return
	}
	if err != nil {
		logf("whisper failed: %v\n%s", err, strings.Join(tail, "\n"))
		fail("The speech engine stopped unexpectedly. Please try again.")
		return
	}

	// 5. Put the pieces together and save next to the file.
	var subs []subtitle
	for i, p := range inputs {
		b, err := os.ReadFile(p + ".srt")
		if err != nil {
			continue
		}
		for _, s := range parseSRT(string(b)) {
			s.Text = cleanText(s.Text)
			if s.Text == "" {
				continue
			}
			s.Start += starts[i]
			s.End += starts[i]
			subs = append(subs, s)
		}
	}
	if len(subs) == 0 {
		fail("No speech was found in this file.")
		return
	}
	for i := 0; i+1 < len(subs); i++ { // pieces overlap by a few milliseconds; players dislike that
		if subs[i].End > subs[i+1].Start {
			subs[i].End = subs[i+1].Start
		}
	}
	var txt strings.Builder
	for i, s := range subs {
		if i > 0 {
			// a new paragraph after a long pause, otherwise one flowing text
			if s.Start-subs[i-1].End > 2.5 {
				txt.WriteString("\n\n")
			} else {
				txt.WriteString(" ")
			}
		}
		txt.WriteString(strings.ReplaceAll(s.Text, "\n", " "))
	}
	base := strings.TrimSuffix(in, filepath.Ext(in))
	srtPath := base + "." + lang + ".srt"
	txtPath := base + "." + lang + ".txt"
	if err := os.WriteFile(srtPath, []byte(writeSRT(subs)), 0o644); err != nil {
		fail("Couldn't save the subtitles: " + err.Error())
		return
	}
	if err := os.WriteFile(txtPath, []byte(txt.String()+"\n"), 0o644); err != nil {
		fail("Couldn't save the text: " + err.Error())
		return
	}
	send(map[string]any{"type": "done", "file": srtPath, "srt": srtPath, "txt": txtPath, "lang": lang,
		"seconds": int(time.Since(began).Seconds())})
}

// commandContext is command() that also stops when ctx is cancelled.
func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcAttrs(cmd)
	return cmd
}

// collapseRepeats removes the loops whisper sometimes gets stuck in ("কোনো কোনো কোনো কোনো…"):
// a word or a phrase of up to 8 words repeated 3 or more times in a row is kept once.
func collapseRepeats(s string) string {
	w := strings.Fields(s)
	if len(w) < 3 {
		return s
	}
	key := func(x string) string { return strings.Trim(x, ".,;:!?।'\"") }
	var out []string
	for i := 0; i < len(w); {
		done := false
		for n := 8; n >= 1 && !done; n-- {
			if i+3*n > len(w) {
				continue
			}
			reps := 1
			for i+(reps+1)*n <= len(w) {
				same := true
				for k := 0; k < n; k++ {
					if key(w[i+k]) != key(w[i+reps*n+k]) {
						same = false
						break
					}
				}
				if !same {
					break
				}
				reps++
			}
			if reps >= 3 {
				out = append(out, w[i:i+n]...)
				i += reps * n
				done = true
			}
		}
		if !done {
			out = append(out, w[i])
			i++
		}
	}
	return strings.Join(out, " ")
}
