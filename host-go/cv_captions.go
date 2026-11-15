package main

// Caption editor support (the "Captions" page): word timings from the speech engine, loading and
// saving captions, the bundled caption fonts, and burning styled captions (ASS, made by the page)
// into a video.
//
// The fonts are free (SIL Open Font License, see fonts/licences) and built into the helper, so the
// page's live preview and FFmpeg's burned-in result use exactly the same letters on every computer:
// Noto Sans, Noto Sans Bengali, Montserrat Black, Anton and Hind Siliguri.

import (
	"bytes"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

//go:embed fonts/*.ttf fonts/licences/*.txt
var captionFonts embed.FS

const cvCaptions = "captions" // converter tool: burn an ASS file from the Captions page into a video

var (
	fontsOnce sync.Once
	fontsPath string
	fontsErr  error
)

// captionFontsDir writes the bundled fonts to a folder FFmpeg can read (once per helper version).
func captionFontsDir() (string, error) {
	fontsOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "anysave-caption-fonts-"+helperVersion)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fontsErr = err
			return
		}
		ents, _ := fs.ReadDir(captionFonts, "fonts")
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			b, _ := captionFonts.ReadFile("fonts/" + e.Name())
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(p); err == nil && st.Size() == int64(len(b)) {
				continue
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				fontsErr = err
				return
			}
		}
		fontsPath = dir
	})
	return fontsPath, fontsErr
}

// serveFont gives the page a bundled font for its live preview.
func serveFont(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("name"))
	b, err := captionFonts.ReadFile("fonts/" + name)
	if err != nil || !strings.HasSuffix(name, ".ttf") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/ttf")
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}

// ---------- word timings ----------

// capWord is one spoken word and when it was said (seconds).
type capWord struct {
	T string  `json:"t"`
	S float64 `json:"s"`
	E float64 `json:"e"`
}

// whisperWords reads whisper-cli's full JSON (-ojf) and returns its words with times.
// Whisper's tokens can split one letter's bytes (Bangla needs several tokens per letter), so the
// tokens are joined byte by byte and only then split into words at spaces.
func whisperWords(path string, offset float64) []capWord {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Transcription []struct {
			Tokens []struct {
				Text    json.RawMessage `json:"text"`
				Offsets struct {
					From float64 `json:"from"`
					To   float64 `json:"to"`
				} `json:"offsets"`
			} `json:"tokens"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		logf("caption words: %s: %v", filepath.Base(path), err)
		return nil
	}
	var out []capWord
	for _, seg := range doc.Transcription {
		var buf []byte
		type span struct {
			a, b     int
			from, to float64
		}
		var spans []span
		for _, t := range seg.Tokens {
			txt := rawJSONString(t.Text)
			if len(txt) == 0 || bytes.HasPrefix(txt, []byte("[_")) {
				continue
			}
			spans = append(spans, span{len(buf), len(buf) + len(txt), t.Offsets.From / 1000, t.Offsets.To / 1000})
			buf = append(buf, txt...)
		}
		at := func(i int) span {
			for _, s := range spans {
				if i >= s.a && i < s.b {
					return s
				}
			}
			return spans[len(spans)-1]
		}
		var segWords []capWord
		ok := true
		for i := 0; i < len(buf); {
			for i < len(buf) && isSpaceByte(buf[i]) {
				i++
			}
			j := i
			for j < len(buf) && !isSpaceByte(buf[j]) {
				j++
			}
			if j > i {
				w := buf[i:j]
				if !utf8.Valid(w) {
					ok = false
					break
				}
				s, e := at(i), at(j-1)
				word := capWord{string(w), s.from + offset, e.to + offset}
				if len(segWords) > 0 && !hasLetter(word.T) { // "," "." "?" go with the word before
					p := &segWords[len(segWords)-1]
					p.T += word.T
					p.E = word.E
				} else {
					segWords = append(segWords, word)
				}
			}
			i = j
		}
		if ok {
			out = append(out, segWords...)
		}
	}
	for i := range out { // whisper sometimes gives a word no length
		if out[i].E < out[i].S+0.05 {
			out[i].E = out[i].S + 0.05
		}
	}
	return out
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' }

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) {
			return true
		}
	}
	return false
}

// rawJSONString unquotes a JSON string without touching bytes that aren't valid UTF-8 on their own
// (encoding/json would turn them into U+FFFD and lose them).
func rawJSONString(raw json.RawMessage) []byte {
	if len(raw) < 2 || raw[0] != '"' {
		return nil
	}
	in := raw[1 : len(raw)-1]
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c != '\\' || i+1 >= len(in) {
			out = append(out, c)
			continue
		}
		i++
		switch in[i] {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'u':
			if i+4 < len(in) {
				if v, err := strconv.ParseUint(string(in[i+1:i+5]), 16, 32); err == nil {
					out = utf8.AppendRune(out, rune(v))
					i += 4
					continue
				}
			}
			out = append(out, 'u')
		default: // \" \\ \/
			out = append(out, in[i])
		}
	}
	return out
}

// Word timings are kept in AnySave's own data folder, not next to the video, so a transcription
// still only adds the .srt and .txt the user expects.
func captionWordsPath(srt string) string {
	abs, _ := filepath.Abs(srt)
	if real, err := filepath.EvalSymlinks(abs); err == nil { // the same file reached two ways (/var = /private/var on a Mac)
		abs = real
	}
	h := sha1.Sum([]byte(abs))
	return filepath.Join(filepath.Dir(modelsDir()), "captions", hex.EncodeToString(h[:8])+".json")
}

func saveCaptionWords(srt string, words []capWord) {
	if len(words) == 0 {
		return
	}
	p := captionWordsPath(srt)
	os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := json.Marshal(map[string]any{"srt": srt, "words": words})
	if err := os.WriteFile(p, b, 0o644); err != nil {
		logf("caption words: %v", err)
	}
}

func loadCaptionWords(srt string) []capWord {
	b, err := os.ReadFile(captionWordsPath(srt))
	if err != nil {
		return nil
	}
	var d struct {
		Words []capWord `json:"words"`
	}
	json.Unmarshal(b, &d)
	return d.Words
}

// ---------- loading and saving ----------

type capCue struct {
	S     float64   `json:"s"`
	E     float64   `json:"e"`
	Text  string    `json:"text"`
	Words []capWord `json:"words,omitempty"`
}

var squash = regexp.MustCompile(`[\s\p{P}]+`)

// loadCaptions reads subtitles and gives every line its words' times where they're known.
// A line whose text no longer matches the words (it was edited elsewhere) gets none; the page then
// spreads the line's time over its words.
func loadCaptions(srt string) ([]capCue, error) {
	b, err := os.ReadFile(srt)
	if err != nil {
		return nil, cvError("Couldn't read " + filepath.Base(srt) + ".")
	}
	subs := parseSRT(strings.TrimPrefix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\ufeff"))
	if len(subs) == 0 {
		return nil, cvError(filepath.Base(srt) + " has no subtitle lines.")
	}
	words := loadCaptionWords(srt)
	cues := make([]capCue, 0, len(subs))
	wi := 0
	for _, s := range subs {
		c := capCue{S: s.Start, E: s.End, Text: strings.TrimSpace(s.Text)}
		var mine []capWord
		for wi < len(words) && (words[wi].S+words[wi].E)/2 < s.Start {
			wi++
		}
		k := wi
		for k < len(words) && (words[k].S+words[k].E)/2 <= s.End+0.05 {
			mine = append(mine, words[k])
			k++
		}
		var joined strings.Builder
		for _, w := range mine {
			joined.WriteString(w.T)
		}
		if len(mine) > 0 && squash.ReplaceAllString(joined.String(), "") == squash.ReplaceAllString(c.Text, "") {
			c.Words = mine
			wi = k
		}
		cues = append(cues, c)
	}
	return cues, nil
}

// findCaptions lists subtitle files next to a video that belong to it ("video.en.srt", "video.srt").
func findCaptions(video string) []map[string]any {
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	ents, _ := os.ReadDir(filepath.Dir(video))
	var out []map[string]any
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(n), ".srt") || !strings.HasPrefix(n, base+".") {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		lang := strings.TrimSuffix(strings.TrimPrefix(n, base+"."), filepath.Ext(n))
		if strings.HasPrefix(lang, ".") || lang == "srt" {
			lang = ""
		}
		out = append(out, map[string]any{"path": filepath.Join(filepath.Dir(video), n), "lang": lang, "modified": st.ModTime().Unix()})
	}
	return out
}

// captionOp answers the Captions page's requests (cv_server.go's op calls this).
func (s *cvServer) captionOp(req map[string]any, path string) map[string]any {
	switch str(req, "op") {
	case "captionsFind":
		found := findCaptions(path)
		for _, f := range found {
			s.allow(f["path"].(string))
		}
		return map[string]any{"ok": true, "found": found}
	case "captionsLoad":
		cues, err := loadCaptions(path)
		if err != nil {
			return fail(errMsg(err))
		}
		return map[string]any{"ok": true, "cues": cues, "words": len(loadCaptionWords(path)) > 0}
	case "captionsSave":
		// path is the video; text is the finished file from the page.
		format := strings.ToLower(str(req, "format"))
		if !setOf("srt", "vtt", "ass", "txt")[format] {
			return fail("Unknown caption format.")
		}
		text := str(req, "text")
		if strings.TrimSpace(text) == "" {
			return fail("There are no captions to save.")
		}
		var out string
		if b, _ := req["temp"].(bool); b {
			out = filepath.Join(s.tmp, fmt.Sprintf("captions-%d.%s", time.Now().UnixNano(), format))
		} else {
			dir := filepath.Join(downloadDir, "Converted")
			os.MkdirAll(dir, 0o755)
			name := safeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			if l := str(req, "lang"); langCodeRe.MatchString(l) && l != "auto" {
				name += "." + l
			}
			out = uniquePath(dir, name, format)
		}
		if format == "srt" || format == "txt" {
			text = strings.ReplaceAll(text, "\n", lineEnd())
		}
		if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
			return fail("Couldn't save the captions: " + err.Error())
		}
		s.allow(out)
		return map[string]any{"ok": true, "file": out}
	}
	return fail("Unknown request")
}

func lineEnd() string {
	if os.PathSeparator == '\\' {
		return "\r\n"
	}
	return "\n"
}

// ---------- burning ----------

// burnCaptions draws the page's styled captions (an ASS file) onto the video with the bundled fonts.
func (j *cvJob) burnCaptions(video, ass, out string) error {
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	fonts, err := captionFontsDir()
	if err != nil {
		return cvError("Couldn't prepare the caption fonts: " + err.Error())
	}
	mi := probeMedia(video)
	if !mi.HasVideo || mi.Still {
		return cvError("Captions can only be drawn on a video.")
	}
	plain := j.tmp("captions.ass")
	if err := copyFile(ass, plain); err != nil {
		return cvError("Couldn't read the captions.")
	}
	esc := strings.NewReplacer(`\`, "/", ":", `\:`, "'", `\'`)
	vf := fmt.Sprintf("subtitles='%s':fontsdir='%s',scale=trunc(iw/2)*2:trunc(ih/2)*2",
		esc.Replace(filepath.ToSlash(plain)), esc.Replace(filepath.ToSlash(fonts)))
	a := []string{"-i", video, "-map", "0:v:0", "-map", "0:a:0?", "-vf", vf}
	a = append(a, videoCodec(encoders(ff), "h264", 0, true, "mp4", j.spec.opt("hw") == "1")...)
	switch mi.ACodec {
	case "aac", "mp3", "alac":
		a = append(a, "-c:a", "copy")
	default:
		a = append(a, "-c:a", "aac", "-b:a", "192k")
	}
	a = append(a, "-movflags", "+faststart", out)
	return j.runFF(a, mi.Duration, "mp4")
}
