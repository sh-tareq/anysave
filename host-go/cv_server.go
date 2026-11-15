package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The preview server behind the Convert page. It only listens on 127.0.0.1, every request needs the
// token, and it only reads files the user picked or dropped, or files in AnySave's own folder.
//
//	POST /cv/op      {op: pick|info|sample|image|doc|subs|archive|data|renderStart|renderDone, ...}
//	GET  /cv/file    ?path=   the file itself (with seeking), for <video>, <img> and pdf.js
//	GET  /cv/thumb   ?path=&w=&t=   a JPEG of a picture or a video frame
//	POST /cv/upload  ?name=   a dropped file (or a rendered PDF page with &dest=)
//
// In the Chrome extension the page starts it with the "cvSession" action and it lives as long as the
// page is open; in the Mac and Windows apps the daemon serves it on its own port.
type cvServer struct {
	token   string
	tmp     string
	mu      sync.Mutex
	allowed map[string]bool
	dests   map[string]bool // folders made by renderStart
}

func newCvServer(token string) (*cvServer, error) {
	sweepPreviewDirs()
	tmp, err := os.MkdirTemp("", "anysave-preview-")
	if err != nil {
		return nil, err
	}
	// Keep our folder "fresh" while we run, so another AnySave starting up never sweeps it.
	go func() {
		for range time.Tick(10 * time.Minute) {
			now := time.Now()
			if os.Chtimes(tmp, now, now) != nil {
				return
			}
		}
	}()
	return &cvServer{token: token, tmp: tmp, allowed: map[string]bool{}, dests: map[string]bool{}}, nil
}

// sweepPreviewDirs deletes preview folders left behind by an AnySave that was closed or killed
// (copies of dropped files, samples, thumbnails). A running AnySave touches its folder every
// 10 minutes, so anything untouched for an hour belongs to nobody.
func sweepPreviewDirs() {
	old, _ := filepath.Glob(filepath.Join(os.TempDir(), "anysave-preview-*"))
	for _, d := range old {
		if st, err := os.Stat(d); err == nil && st.IsDir() && time.Since(st.ModTime()) > time.Hour {
			os.RemoveAll(d)
		}
	}
}

func (s *cvServer) close() { os.RemoveAll(s.tmp) }

func (s *cvServer) allow(p string) {
	if abs, err := filepath.Abs(p); err == nil {
		s.mu.Lock()
		s.allowed[abs] = true
		s.mu.Unlock()
	}
}

func within(p, dir string) bool {
	if dir == "" {
		return false
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(d, p)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// readable gives the absolute path if the page may read it.
func (s *cvServer) readable(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	s.mu.Lock()
	ok := s.allowed[abs]
	s.mu.Unlock()
	if !ok {
		tmpReal, _ := filepath.EvalSymlinks(s.tmp)
		dl, _ := filepath.EvalSymlinks(downloadDir)
		ok = within(abs, tmpReal) || within(abs, s.tmp) || within(abs, dl) || within(abs, downloadDir)
	}
	if !ok || !isFile(abs) {
		return "", false
	}
	return abs, true
}

func (s *cvServer) authed(r *http.Request) bool {
	t := r.Header.Get("X-AnySave-Token")
	if t == "" {
		t = r.URL.Query().Get("token")
	}
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *cvServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); strings.HasPrefix(o, "chrome-extension://") || strings.HasPrefix(o, "http://127.0.0.1:") {
		w.Header().Set("Access-Control-Allow-Origin", o)
		w.Header().Set("Access-Control-Allow-Headers", "X-AnySave-Token, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch strings.TrimPrefix(r.URL.Path, "/cv/") {
	case "op":
		s.serveOp(w, r)
	case "file":
		p, ok := s.readable(r.URL.Query().Get("path"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		if ct := previewMime(p); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeFile(w, r, p)
	case "thumb":
		s.serveThumb(w, r)
	case "upload":
		s.serveUpload(w, r)
	case "font":
		serveFont(w, r)
	default:
		http.NotFound(w, r)
	}
}

func previewMime(p string) string {
	switch extOf(p) {
	case "mp4", "m4v", "mov":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mkv":
		return "video/x-matroska"
	case "mp3":
		return "audio/mpeg"
	case "m4a", "aac":
		return "audio/mp4"
	case "ogg", "oga", "opus":
		return "audio/ogg"
	case "wav":
		return "audio/wav"
	case "flac":
		return "audio/flac"
	case "pdf":
		return "application/pdf"
	case "jpg", "jpeg", "jfif":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "avif":
		return "image/avif"
	case "svg":
		return "text/plain" // never run a picked SVG's scripts
	case "html", "htm", "xhtml":
		return "text/plain"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(msg string) map[string]any { return map[string]any{"ok": false, "error": msg} }

func errMsg(err error) string {
	if ce, ok := err.(cvError); ok {
		return string(ce)
	}
	return "Couldn't make the preview: " + err.Error()
}

func (s *cvServer) fileURL(p string) string {
	return "/cv/file?path=" + url.QueryEscape(p) + "&token=" + url.QueryEscape(s.token)
}

func (s *cvServer) serveOp(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeJSON(w, fail("Bad request"))
		return
	}
	defer func() {
		if e := recover(); e != nil {
			logf("cv op panic: %v", e)
			writeJSON(w, fail(fmt.Sprint(e)))
		}
	}()
	writeJSON(w, s.op(r.Context(), req))
}

func (s *cvServer) fileEntry(p string) map[string]any {
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	return map[string]any{"path": p, "name": filepath.Base(p), "size": st.Size(), "kind": cvKindOf(p)}
}

func specOf(v any) cvSpec {
	var spec cvSpec
	if b, err := json.Marshal(v); err == nil {
		json.Unmarshal(b, &spec)
	}
	if spec.Opts == nil {
		spec.Opts = map[string]string{}
	}
	return spec
}

func (s *cvServer) op(ctx context.Context, req map[string]any) map[string]any {
	switch str(req, "op") {
	case "pick":
		multi, _ := req["multi"].(bool)
		paths, err := pickFiles(multi)
		if err != nil {
			return fail("Couldn't open the file chooser: " + err.Error())
		}
		var files []map[string]any
		for _, p := range paths {
			if e := s.fileEntry(p); e != nil {
				s.allow(p)
				files = append(files, e)
			}
		}
		return map[string]any{"ok": true, "files": files}
	case "voices", "voiceGet", "voiceSay", "voiceClean":
		return s.voiceOp(ctx, req)
	case "allow":
		// Files from AnySave's own downloads list (they're inside its folder anyway).
		p, ok := s.readable(str(req, "path"))
		if !ok {
			return fail("That file isn't there any more.")
		}
		return map[string]any{"ok": true, "file": s.fileEntry(p)}
	}

	path, ok := s.readable(str(req, "path"))
	if !ok && str(req, "op") != "renderDone" {
		return fail("That file isn't there any more, or AnySave may not read it.")
	}
	switch str(req, "op") {
	case "captionsFind", "captionsLoad", "captionsSave":
		return s.captionOp(req, path)
	case "info":
		return s.info(path)
	case "sample":
		return s.sample(ctx, path, specOf(req["spec"]), num(fmt.Sprint(req["at"])))
	case "image":
		return s.imageSample(ctx, path, specOf(req["spec"]))
	case "doc":
		h, err := docToHTML(path)
		if err != nil {
			return fail(errMsg(err))
		}
		words := len(strings.Fields(htmlToText(h)))
		if len(h) > 300<<10 {
			h = h[:300<<10]
			if i := strings.LastIndex(h, "<"); i > len(h)-200 {
				h = h[:i]
			}
		}
		return map[string]any{"ok": true, "html": h, "words": words}
	case "subs":
		cues, err := s.readCues(ctx, path)
		if err != nil {
			return fail(errMsg(err))
		}
		return map[string]any{"ok": true, "cues": cues}
	case "archive":
		entries, locked, err := archiveList(path, 500)
		if err != nil {
			return fail(errMsg(err))
		}
		return map[string]any{"ok": true, "entries": entries, "locked": locked}
	case "data":
		t, err := readTable(path)
		if err != nil {
			return fail(errMsg(err))
		}
		total := len(t.Rows)
		if len(t.Rows) > 100 {
			t.Rows = t.Rows[:100]
		}
		return map[string]any{"ok": true, "header": t.Header, "rows": t.Rows, "total": total}
	case "renderStart":
		// PDF pages to pictures: the page draws each page with pdf.js and uploads it into this folder.
		out := filepath.Join(downloadDir, "Converted")
		os.MkdirAll(out, 0o755)
		dir := uniqueDir(out, safeName(baseNoExt(path)+" pages"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail("Couldn't make the folder: " + err.Error())
		}
		s.mu.Lock()
		s.dests[dir] = true
		s.mu.Unlock()
		return map[string]any{"ok": true, "dir": dir}
	case "renderDone":
		dir := str(req, "dir")
		s.mu.Lock()
		known := s.dests[dir]
		if str(req, "cancel") == "1" || req["cancel"] == true {
			delete(s.dests, dir)
		}
		s.mu.Unlock()
		if !known {
			return fail("Unknown folder")
		}
		if req["cancel"] == true {
			os.RemoveAll(dir)
			return map[string]any{"ok": true}
		}
		ents, _ := os.ReadDir(dir)
		return map[string]any{"ok": true, "file": dir, "count": len(ents)}
	}
	return fail("Unknown request")
}

// info says what a file is, for the page to pick tools and show the right preview.
func (s *cvServer) info(p string) map[string]any {
	e := s.fileEntry(p)
	if e == nil {
		return fail("That file isn't there any more.")
	}
	res := map[string]any{"ok": true, "file": e, "url": s.fileURL(p)}
	switch cvKindOf(p) {
	case "video", "audio", "image":
		mi := probeMedia(p)
		res["media"] = mi
		if cvKindOf(p) == "video" && !mi.HasVideo && mi.HasAudio {
			e["kind"] = "audio" // e.g. an .mp4 or .webm that is only sound
		}
	case "pdf":
		res["locked"] = pdfLocked(p)
		if n, err := pdfPageCount(p); err == nil {
			res["pages"] = n
		}
	}
	return res
}

// sample converts 10 seconds starting at "at" with the real settings, so the page can play the
// result and estimate the whole file's size.
func (s *cvServer) sample(ctx context.Context, p string, spec cvSpec, at float64) map[string]any {
	mi := probeMedia(p)
	length := 10.0
	start := at
	if spec.StartSec != nil && *spec.StartSec > start {
		start = *spec.StartSec
	}
	end := mi.Duration
	if spec.EndSec != nil && *spec.EndSec > 0 && *spec.EndSec < end {
		end = *spec.EndSec
	}
	if end > 0 && start+length > end {
		start = math.Max(0, end-length)
		if spec.StartSec != nil && start < *spec.StartSec {
			start = *spec.StartSec
		}
	}
	if end > 0 && end-start < length {
		length = math.Max(0.5, end-start)
	}
	full := end
	if spec.StartSec != nil {
		full -= *spec.StartSec
	}
	sp := spec
	sp.Tool = cvMedia
	st, en := start, start+length
	sp.StartSec, sp.EndSec = &st, &en
	format := strings.ToLower(spec.Format)
	if format == "" {
		format = "mp4"
	}
	if str := spec.opt("preview"); str == "proxy" { // a small clip Chrome can play, for files it can't
		format = "mp4"
		h := 480
		sp = cvSpec{Tool: cvMedia, Format: "mp4", Height: &h, StartSec: &st, EndSec: &en, Opts: map[string]string{"hw": "1"}}
	}
	out := filepath.Join(s.tmp, fmt.Sprintf("sample-%d.%s", time.Now().UnixNano(), format))
	j := &cvJob{ctx: ctx, spec: sp, work: s.tmp, progress: func(float64) {}}
	began := time.Now()
	if err := j.media(p, out, format); err != nil {
		os.Remove(out)
		return fail(errMsg(err))
	}
	took := time.Since(began).Seconds()
	size := fileSize(out)
	speed := sp.optF("speed", 1)
	if speed < 0.25 || speed > 4 {
		speed = 1
	}
	est := int64(0)
	if length > 0 && full > 0 {
		est = int64(float64(size) * full / length)
	}
	if spec.TargetMb != nil && *spec.TargetMb > 0 {
		est = int64(*spec.TargetMb) << 20
	}
	return map[string]any{"ok": true, "url": s.fileURL(out), "path": out, "size": size, "seconds": length / speed,
		"start": start, "estimate": est, "took": took, "fullSeconds": full / speed, "media": probeMedia(out)}
}

// imageSample encodes the picture with the real settings, for the before/after slider.
func (s *cvServer) imageSample(ctx context.Context, p string, spec cvSpec) map[string]any {
	format := strings.ToLower(spec.Format)
	if format == "" {
		format = "jpg"
	}
	if format == "tif" {
		format = "tiff"
	}
	ext := format
	j := &cvJob{ctx: ctx, spec: spec, work: s.tmp, progress: func(float64) {}}
	out := filepath.Join(s.tmp, fmt.Sprintf("image-%d.%s", time.Now().UnixNano(), ext))
	if err := j.image(p, out, format); err != nil {
		os.Remove(out)
		return fail(errMsg(err))
	}
	res := map[string]any{"ok": true, "size": fileSize(out), "path": out}
	// What the browser can show: the result itself, or a PNG copy (TIFF, ICO, BMP...).
	if setOf("jpg", "png", "webp", "gif", "avif")[format] {
		res["url"] = s.fileURL(out)
	} else {
		res["url"] = "/cv/thumb?path=" + url.QueryEscape(out) + "&w=2000&token=" + url.QueryEscape(s.token)
	}
	mi := probeMedia(out)
	res["width"], res["height"] = mi.Width, mi.Height
	return res
}

// serveThumb gives a JPEG of a picture (any kind FFmpeg reads) or of a video frame at t seconds.
func (s *cvServer) serveThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ok := s.readable(q.Get("path"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	width, _ := strconv.Atoi(q.Get("w"))
	if width <= 0 || width > 4000 {
		width = 640
	}
	t, _ := strconv.ParseFloat(q.Get("t"), 64)
	st, _ := os.Stat(p)
	key := fmt.Sprintf("%x", sha1Hex(fmt.Sprintf("%s|%d|%d|%.2f", p, st.ModTime().UnixNano(), width, t)))
	out := filepath.Join(s.tmp, "thumb-"+key+".jpg")
	if !isFile(out) {
		var a []string
		if t > 0 {
			a = append(a, "-ss", ff3(t))
		}
		a = append(a, "-i", p, "-frames:v", "1")
		a = append(a, pictureFilter(p, fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease", width, width*2))...)
		a = append(a, "-q:v", "3", "-update", "1", out)
		j := &cvJob{ctx: r.Context(), work: s.tmp, progress: func(float64) {}}
		if err := j.quietFF(a); err != nil || !isFile(out) {
			http.Error(w, "no picture", http.StatusUnsupportedMediaType)
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, out)
}

func sha1Hex(s string) string {
	h := uint64(1469598103934665603) // FNV-1a is plenty for a cache name
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return strconv.FormatUint(h, 16)
}

// serveUpload saves a file dropped on the page (browsers don't tell pages where files are), or a
// PDF page the page rendered (dest = a renderStart folder).
func (s *cvServer) serveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := safeName(r.URL.Query().Get("name"))
	var dir string
	if d := r.URL.Query().Get("dest"); d != "" {
		s.mu.Lock()
		ok := s.dests[d]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, fail("Unknown folder"))
			return
		}
		dir = d
	} else {
		b := make([]byte, 6)
		rand.Read(b)
		dir = filepath.Join(s.tmp, "in-"+hex.EncodeToString(b))
		os.MkdirAll(dir, 0o755)
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		writeJSON(w, fail("Couldn't save the file: "+err.Error()))
		return
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	_, err = io.Copy(bw, r.Body)
	if err == nil {
		err = bw.Flush()
	}
	f.Close()
	if err != nil {
		os.Remove(p)
		writeJSON(w, fail("Couldn't save the file: "+err.Error()))
		return
	}
	s.allow(p)
	writeJSON(w, map[string]any{"ok": true, "file": s.fileEntry(p)})
}

var cueTimeRe = regexp.MustCompile(`(\d+):(\d+):(\d+)[,.](\d+)\s*-->\s*(\d+):(\d+):(\d+)[,.](\d+)`)

type cue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// readCues turns any subtitle file into a list of lines with times (through SRT).
func (s *cvServer) readCues(ctx context.Context, p string) ([]cue, error) {
	src := p
	if extOf(p) != "srt" {
		src = filepath.Join(s.tmp, fmt.Sprintf("cues-%d.srt", time.Now().UnixNano()))
		defer os.Remove(src)
		j := &cvJob{ctx: ctx, work: s.tmp, progress: func(float64) {}}
		if err := j.quietFF([]string{"-i", p, src}); err != nil {
			return nil, cvError("Couldn't read these subtitles.")
		}
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	return parseCues(string(b)), nil
}

func parseCues(text string) []cue {
	var out []cue
	tagStrip := regexp.MustCompile(`<[^>]+>|\{[^}]+\}`)
	for _, block := range blankLinesRe.Split(strings.ReplaceAll(strings.TrimPrefix(text, "\ufeff"), "\r\n", "\n"), -1) {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, l := range lines {
			m := cueTimeRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			ts := func(h, mi, se, ms string) float64 {
				a, _ := strconv.Atoi(h)
				b, _ := strconv.Atoi(mi)
				c, _ := strconv.Atoi(se)
				d, _ := strconv.ParseFloat("0."+ms, 64)
				return float64(a*3600+b*60+c) + d
			}
			t := strings.TrimSpace(tagStrip.ReplaceAllString(strings.Join(lines[i+1:], "\n"), ""))
			if t != "" {
				out = append(out, cue{ts(m[1], m[2], m[3], m[4]), ts(m[5], m[6], m[7], m[8]), t})
			}
			break
		}
		if len(out) >= 2000 {
			break
		}
	}
	return out
}

func newToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// actionCvSession runs the preview server for the Chrome extension's Convert page until the page closes.
func actionCvSession(msg map[string]any) {
	srv, err := newCvServer(newToken())
	if err != nil {
		send(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer srv.close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		send(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/cv/", srv)
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go hs.Serve(ln)
	send(map[string]any{"type": "ready", "base": "http://" + ln.Addr().String(), "token": srv.token})
	for { // Chrome closes stdin when the page closes
		if _, err := readMessage(); err != nil {
			break
		}
	}
	hs.Close()
}
