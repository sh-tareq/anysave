// AnySave native messaging host (macOS + Windows).
//
// Chrome launches this program and talks to it over stdin/stdout using the
// native messaging protocol (4-byte little-endian length + UTF-8 JSON).
// It wraps yt-dlp (and ffmpeg), which live in a "bin" folder next to it.
//
// Actions:
//
//	info      -> {ok, title, thumbnail, duration, uploader, url, options[], hasFfmpeg, needsFfmpeg}
//	download  -> streams {type: progress|done|error} messages
//	reveal    -> shows a downloaded file in Finder / Explorer
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	h264Sort      = "res,fps,vcodec:h264,acodec:m4a"
	helperVersion = "1.1.0"
	maxLogBytes   = 2 * 1024 * 1024 // rotate anysave.log at 2 MB
)

var (
	appDir      string // folder holding this executable
	downloadDir string
	writeMu     sync.Mutex
	logMu       sync.Mutex
	stdin       = bufio.NewReader(os.Stdin)
)

// ---------- native messaging framing ----------

func readMessage() (map[string]any, error) {
	var n uint32
	if err := binary.Read(stdin, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(stdin, buf); err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(buf, &m)
}

func send(v any) {
	data, _ := json.Marshal(v)
	writeMu.Lock()
	defer writeMu.Unlock()
	binary.Write(os.Stdout, binary.LittleEndian, uint32(len(data)))
	os.Stdout.Write(data)
}

func logf(format string, a ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	p := filepath.Join(appDir, "anysave.log")
	if st, err := os.Stat(p); err == nil && st.Size() >= maxLogBytes {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("2006-01-02 15:04:05 ")+format+"\n", a...)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// ---------- tool discovery ----------

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func findTool(name string) string {
	bin := filepath.Join(appDir, "bin")
	candidates := []string{filepath.Join(bin, exe(name))}
	if name == "yt-dlp" && runtime.GOOS == "darwin" {
		// Unpacked macOS build (see install script for why not the single-file one)
		candidates = append(candidates, filepath.Join(bin, "yt-dlp_app", "yt-dlp_macos"), filepath.Join(bin, "yt-dlp_macos"))
	}
	for _, c := range candidates {
		if isFile(c) {
			return c
		}
	}
	// Fall back to common install locations and PATH.
	extra := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		extra = append(extra, filepath.Join(home, ".local", "bin"))
	}
	for _, d := range extra {
		if p := filepath.Join(d, exe(name)); isFile(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func baseArgs(cookieFile string) (string, []string, error) {
	ytdlp := findTool("yt-dlp")
	if ytdlp == "" {
		return "", nil, errors.New("yt-dlp not found. Re-run the AnySave installer.")
	}
	args := []string{"--no-playlist", "--no-warnings", "--color", "never", "--encoding", "utf-8"}
	if ff := findTool("ffmpeg"); ff != "" {
		args = append(args, "--ffmpeg-location", ff)
	}
	// YouTube needs a JavaScript runtime to unlock its video formats. Ours lives in bin/,
	// which isn't on the PATH Chrome gives us, so tell yt-dlp where it is.
	if deno := findTool("deno"); deno != "" {
		args = append(args, "--js-runtimes", "deno:"+deno)
	}
	if cookieFile != "" {
		args = append(args, "--cookies", cookieFile)
	}
	return ytdlp, args, nil
}

func command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	setProcAttrs(cmd) // per-OS: hide console window / own process group
	return cmd
}

// writeCookies stores cookies.txt text from the extension in a private temp
// file. The caller must remove it. Returns "" when there are no cookies.
func writeCookies(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	f, err := os.CreateTemp("", "anysave-*.txt") // mode 0600
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return f.Name(), err
}

func lastError(stderr string) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(stderr, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	msg := "yt-dlp failed"
	if len(lines) > 0 {
		msg = lines[len(lines)-1]
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "ERROR") { // errors can span lines; keep a few
			end := i + 3
			if end > len(lines) {
				end = len(lines)
			}
			msg = strings.Join(lines[i:end], " ")
			break
		}
	}
	msg = strings.TrimSpace(strings.Replace(msg, "ERROR:", "", 1))
	if msg == "" {
		msg = "yt-dlp failed (see anysave.log)"
	}
	return friendlyError(msg)
}

// friendlyError turns common yt-dlp failures into plain language.
func friendlyError(msg string) string {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "checksum mismatch"):
		return "The file doesn't match the checksum you gave, so it wasn't kept. " +
			"It may be damaged or changed. Check the checksum and try again."
	case strings.Contains(l, "cloudflare") || strings.Contains(l, "anti-bot") || strings.Contains(l, "captcha"):
		return "This website blocks automated downloads, so AnySave can't download from it. " +
			"Use the site's own download button if it has one."
	case strings.Contains(l, "unsupported url"):
		return "No downloadable video found on this page. Open it, play the video, then use the AnySave popup."
	case strings.Contains(l, "drm"):
		return "This video is DRM-protected, so it can't be downloaded."
	case strings.Contains(l, "http error 404"):
		return "The video or file wasn't found (404). The link may be broken or removed."
	case strings.Contains(l, "http error 403"):
		return "The website refused the download (403). It may need you to be logged in, or it blocks downloads."
	}
	return msg
}

// ---------- info ----------

type format struct {
	Height *float64 `json:"height"`
	Vcodec string   `json:"vcodec"`
	Acodec string   `json:"acodec"`
}

type videoInfo struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Thumbnail   string      `json:"thumbnail"`
	Duration    *float64    `json:"duration"`
	Uploader    string      `json:"uploader"`
	Channel     string      `json:"channel"`
	Extractor   string      `json:"extractor_key"`
	Formats     []format    `json:"formats"`
	Entries     []videoInfo `json:"entries"`
}

type option struct {
	Label     string `json:"label"`
	Format    string `json:"format"`
	Sort      string `json:"sort,omitempty"`
	AudioOnly bool   `json:"audioOnly,omitempty"`
}

func hasV(f format) bool { return f.Vcodec != "" && f.Vcodec != "none" && f.Height != nil }
func hasA(f format) bool { return f.Acodec != "" && f.Acodec != "none" }

func sortedHeights(set map[int]bool) []int {
	var hs []int
	for h := range set {
		hs = append(hs, h)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(hs)))
	return hs
}

func buildOptions(formats []format, hasFfmpeg bool) []option {
	var opts []option
	if hasFfmpeg {
		// Highest resolution first; at equal resolution prefer H.264 + AAC so the
		// MP4 plays everywhere. YouTube only has H.264 up to 1080p.
		opts = append(opts, option{Label: "Best quality (MP4)", Format: "bv*+ba/b", Sort: h264Sort})
		all, avc := map[int]bool{}, map[int]bool{}
		for _, f := range formats {
			if hasV(f) {
				h := int(*f.Height)
				all[h] = true
				if strings.HasPrefix(f.Vcodec, "avc") {
					avc[h] = true
				}
			}
		}
		for _, h := range sortedHeights(all) {
			if h < 144 {
				continue
			}
			suffix := ""
			if len(avc) > 0 && !avc[h] {
				suffix = " (VP9/AV1)"
			}
			opts = append(opts, option{
				Label:  fmt.Sprintf("%dp MP4%s", h, suffix),
				Format: fmt.Sprintf("bv*[height<=%d]+ba/b[height<=%d]", h, h),
				Sort:   h264Sort,
			})
		}
		return append(opts, option{Label: "Audio only (MP3)", Format: "ba/b", AudioOnly: true})
	}
	// No ffmpeg: only formats that already contain video+audio in one file.
	single := map[int]bool{}
	for _, f := range formats {
		if hasV(f) && hasA(f) {
			single[int(*f.Height)] = true
		}
	}
	hs := sortedHeights(single)
	if len(hs) > 0 {
		opts = append(opts, option{Label: "Best single-file quality", Format: "b"})
	}
	for _, h := range hs {
		opts = append(opts, option{Label: fmt.Sprintf("%dp", h), Format: fmt.Sprintf("b[height<=%d]", h)})
	}
	return append(opts, option{Label: "Audio only (M4A)", Format: "ba[ext=m4a]/ba/b", AudioOnly: true})
}

func actionInfo(msg map[string]any) map[string]any {
	url := str(msg, "url")
	cookieFile, err := writeCookies(str(msg, "cookies"))
	if cookieFile != "" {
		defer os.Remove(cookieFile)
	}
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	ytdlp, args, err := baseArgs(cookieFile)
	args = append(args, ytdlpProxyArgs(str(msg, "proxy"))...)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	if !webLink(url) {
		return map[string]any{"ok": false, "error": "That isn't a web link."}
	}
	cmd := command(ytdlp, append(args, "-J", "--", url)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	attachChildJob(cmd)
	// A stuck lookup would leave the popup spinning forever.
	var timedOut atomic.Bool
	timer := time.AfterFunc(90*time.Second, func() { timedOut.Store(true); killTree(cmd) })
	err = cmd.Wait()
	timer.Stop()
	if timedOut.Load() {
		return map[string]any{"ok": false, "error": "The site took too long to answer. Try again in a moment."}
	}
	if err != nil {
		logf("info failed: %s", stderr.String())
		return map[string]any{"ok": false, "error": lastError(stderr.String())}
	}
	var info videoInfo
	if err := json.Unmarshal([]byte(stdout.String()), &info); err != nil {
		return map[string]any{"ok": false, "error": "Could not read yt-dlp output"}
	}
	if len(info.Entries) > 0 { // e.g. Instagram carousel: describe the first item
		info = info.Entries[0]
	}
	hasFfmpeg := findTool("ffmpeg") != ""
	opts := buildOptions(info.Formats, hasFfmpeg)
	needsFfmpeg := true
	for _, o := range opts {
		if !o.AudioOnly {
			needsFfmpeg = false
		}
	}
	title := info.Title
	if title == "" {
		title = info.Description
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80])
		}
	}
	uploader := info.Uploader
	if uploader == "" {
		uploader = info.Channel
	}
	return map[string]any{
		"ok": true, "url": url, "title": title, "thumbnail": info.Thumbnail,
		"duration": info.Duration, "uploader": uploader, "extractor": info.Extractor,
		"hasFfmpeg": hasFfmpeg, "needsFfmpeg": needsFfmpeg, "options": opts,
	}
}

// ---------- download ----------

func num(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func humanSpeed(bps float64) string {
	if bps <= 0 {
		return ""
	}
	for _, u := range []string{"B/s", "KB/s", "MB/s", "GB/s"} {
		if bps < 1024 {
			return fmt.Sprintf("%.1f %s", bps, u)
		}
		bps /= 1024
	}
	return fmt.Sprintf("%.1f TB/s", bps)
}

func humanETA(s string) string {
	if strings.TrimSpace(s) == "NA" || s == "" {
		return "?"
	}
	sec := int(num(s))
	if sec < 3600 {
		return fmt.Sprintf("%d:%02d", sec/60, sec%60)
	}
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec%3600/60, sec%60)
}

func actionDownload(msg map[string]any) {
	fail := func(m string) { send(map[string]any{"type": "error", "message": m}) }

	cookieFile, err := writeCookies(str(msg, "cookies"))
	if cookieFile != "" {
		defer os.Remove(cookieFile)
	}
	if err != nil {
		fail(err.Error())
		return
	}
	ytdlp, args, err := baseArgs(cookieFile)
	args = append(args, ytdlpProxyArgs(str(msg, "proxy"))...)
	if err != nil {
		fail(err.Error())
		return
	}
	// Sorted into Downloads/AnySave/Videos or /Music, like the Android app.
	outDir := downloadDir
	if folder := str(msg, "folder"); folder == "Videos" || folder == "Music" {
		outDir = filepath.Join(downloadDir, folder)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail(err.Error())
		return
	}
	if !webLink(str(msg, "url")) {
		fail("That isn't a web link.")
		return
	}
	// Each job keeps its unfinished pieces in its own folder, so cancelling one download
	// never deletes another's, and Resume (same job id) continues from the same pieces.
	partsDir := filepath.Join(outDir, ".anysave-parts", jobKey(msg))
	pruneParts(filepath.Dir(partsDir))
	fmtSel, sortSel := str(msg, "format"), str(msg, "sort")
	if fmtSel == "" {
		fmtSel = "b"
	}
	if sortSel == "" {
		sortSel = h264Sort
	}
	args = append(args,
		"-f", fmtSel, "-S", sortSel,
		"--concurrent-fragments", "4",
		"-P", outDir, "-P", "temp:"+partsDir,
		"-o", "%(title).150B [%(id)s]%(height& {}p|)s.%(ext)s",
		"--continue", // resume .part files after a pause
		"--newline", "--progress", "--no-simulate",
		"--progress-template", "download:ASP %(progress.downloaded_bytes)s|%(progress.total_bytes)s|"+
			"%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s",
		"--print", "after_move:ASF %(filepath)s",
	)
	if speedKbps, ok := msg["maxSpeedKbps"].(float64); ok && speedKbps > 0 {
		args = append(args, "--limit-rate", fmt.Sprintf("%dK", int(speedKbps)))
	}
	if useAria, _ := msg["useAria2"].(bool); useAria {
		if aria := findTool("aria2c"); aria != "" {
			args = append(args, "--downloader", aria, "--downloader-args", "aria2c:-x 8 -s 8 -k 1M")
		}
	}
	hasFfmpeg := findTool("ffmpeg") != ""
	args = append(args, extrasArgs(msg, hasFfmpeg)...)
	if audio, _ := msg["audioOnly"].(bool); audio && hasFfmpeg {
		args = append(args, "-x", "--audio-format", "mp3")
	} else if hasFfmpeg {
		args = append(args, "--merge-output-format", "mp4")
	}
	args = append(args, "--", str(msg, "url"))
	logf("download: %s %q", ytdlp, args)

	cmd := command(ytdlp, args...)
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		fail(err.Error())
		return
	}
	attachChildJob(cmd)

	// {"action":"cancel"} stops and deletes the unfinished pieces; {"action":"pause"} stops
	// and keeps them. If Chrome just closes our stdin (browser quit, extension reloaded), that
	// counts as a pause too, so the download can continue from where it was.
	var cancelled, paused atomic.Bool
	go func() {
		for {
			m, err := readMessage()
			if err != nil {
				paused.Store(true)
				break
			}
			if a := str(m, "action"); a == "pause" {
				paused.Store(true)
				break
			} else if a == "cancel" {
				break
			}
		}
		cancelled.Store(true)
		killTree(cmd)
	}()

	finalFile := ""
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case strings.HasPrefix(line, "ASP "):
			p := append(strings.Split(line[4:], "|"), "", "", "", "", "")
			done, total := num(p[0]), num(p[1])
			if total == 0 {
				total = num(p[2])
			}
			pct := 0.0
			if done > 0 && total > 0 {
				pct = float64(int(done/total*1000)) / 10
			}
			send(map[string]any{"type": "progress", "percent": pct, "speed": humanSpeed(num(p[3])), "eta": humanETA(p[4])})
		case strings.HasPrefix(line, "ASF "):
			finalFile = line[4:]
		}
	}
	err = cmd.Wait()
	if tail := stderr.String(); len(tail) > 2000 {
		logf("stderr: %s", tail[len(tail)-2000:])
	} else {
		logf("stderr: %s", tail)
	}

	switch {
	case paused.Load() && err != nil:
		send(map[string]any{"type": "paused"})
	case cancelled.Load() && err != nil:
		os.RemoveAll(partsDir)
		fail("Cancelled")
	case err == nil:
		os.RemoveAll(partsDir)
		var file any
		if finalFile != "" {
			file = finalFile
		}
		send(map[string]any{"type": "done", "file": file})
	case finalFile != "" && isFile(finalFile):
		// The video itself was saved; only an extra (subtitles, cover art) failed.
		logf("download finished with a non-fatal error: %v", err)
		os.RemoveAll(partsDir)
		send(map[string]any{"type": "done", "file": finalFile})
	default:
		fail(lastError(stderr.String()))
	}
}

// jobKey names a download's parts folder: the extension's job id, so a resumed job finds
// its pieces again; without one, the URL and format.
func jobKey(msg map[string]any) string {
	if id := safeKey.ReplaceAllString(str(msg, "job"), ""); id != "" {
		return id
	}
	h := sha1.Sum([]byte(str(msg, "url") + "|" + str(msg, "format") + "|" + str(msg, "sort")))
	return hex.EncodeToString(h[:8])
}

// pruneParts removes pieces of downloads that were left unfinished for over a week.
func pruneParts(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

var safeKey = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// webLink reports whether s is an http(s) link (never something yt-dlp could read as an option).
func webLink(s string) bool {
	u, err := neturl.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// ---------- playlists and channels ----------

type playlistEntry struct {
	Type       string   `json:"_type"`
	IEKey      string   `json:"ie_key"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	WebpageURL string   `json:"webpage_url"`
	Duration   *float64 `json:"duration"`
	Thumbnail  string   `json:"thumbnail"`
	Thumbnails []struct {
		URL   string `json:"url"`
		Width int    `json:"width"`
	} `json:"thumbnails"`
}

// actionPlaylist lists the videos of a playlist or channel page (first 300), without
// downloading anything, so the popup can let the user pick which ones to save.
func actionPlaylist(msg map[string]any) map[string]any {
	url := str(msg, "url")
	if !webLink(url) {
		return map[string]any{"ok": false, "error": "That isn't a web link."}
	}
	cookieFile, err := writeCookies(str(msg, "cookies"))
	if cookieFile != "" {
		defer os.Remove(cookieFile)
	}
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	ytdlp, args, err := baseArgs(cookieFile)
	args = append(args, ytdlpProxyArgs(str(msg, "proxy"))...)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	// A YouTube Mix (list=RD…) never ends; the first 50 is plenty and much quicker.
	limit := "1:300"
	if strings.Contains(url, "list=RD") {
		limit = "1:50"
	}
	cmd := command(ytdlp, append(args, "--yes-playlist", "--flat-playlist", "-I", limit, "-J", "--", url)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	attachChildJob(cmd)
	var timedOut atomic.Bool
	timer := time.AfterFunc(120*time.Second, func() { timedOut.Store(true); killTree(cmd) })
	err = cmd.Wait()
	timer.Stop()
	if timedOut.Load() {
		return map[string]any{"ok": false, "error": "The site took too long to list the videos. Try again in a moment."}
	}
	if err != nil {
		logf("playlist failed: %s", stderr.String())
		return map[string]any{"ok": false, "error": lastError(stderr.String())}
	}
	var pl struct {
		Type     string          `json:"_type"`
		Title    string          `json:"title"`
		Uploader string          `json:"uploader"`
		Channel  string          `json:"channel"`
		Entries  []playlistEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &pl); err != nil {
		return map[string]any{"ok": false, "error": "Could not read yt-dlp output"}
	}
	entries := []map[string]any{}
	for _, e := range pl.Entries {
		// Skip sub-lists (a channel's "Videos" / "Shorts" tabs) and anything without a link.
		if e.Type == "playlist" || strings.HasSuffix(e.IEKey, "Tab") {
			continue
		}
		link := e.URL
		if !webLink(link) {
			link = e.WebpageURL
		}
		if !webLink(link) {
			continue
		}
		thumb := e.Thumbnail
		for _, t := range e.Thumbnails { // a small one is enough for the list
			if t.URL != "" && (thumb == "" || (t.Width >= 120 && t.Width <= 480)) {
				thumb = t.URL
			}
		}
		entry := map[string]any{"url": link, "id": e.ID, "title": e.Title, "thumbnail": thumb}
		if e.Duration != nil {
			entry["duration"] = *e.Duration
		}
		entries = append(entries, entry)
	}
	uploader := pl.Uploader
	if uploader == "" {
		uploader = pl.Channel
	}
	return map[string]any{"ok": true, "isPlaylist": pl.Type == "playlist", "title": pl.Title, "uploader": uploader, "entries": entries}
}

// ---------- reveal / open ----------

// actionOpen opens a file AnySave saved with the system's default app.
func actionOpen(msg map[string]any) map[string]any {
	p, err := filepath.Abs(str(msg, "file"))
	base, _ := filepath.Abs(downloadDir)
	if err != nil || !strings.HasPrefix(p, base+string(filepath.Separator)) || !isFile(p) {
		return map[string]any{"ok": false, "error": "File not found. It may have been moved or deleted."}
	}
	if err := openWithDefaultApp(p); err != nil {
		return map[string]any{"ok": false, "error": "Couldn't open the file: " + err.Error()}
	}
	return map[string]any{"ok": true}
}

func actionReveal(msg map[string]any) map[string]any {
	p, err := filepath.Abs(str(msg, "file"))
	base, _ := filepath.Abs(downloadDir)
	if err != nil || !strings.HasPrefix(p, base+string(filepath.Separator)) || !isFile(p) {
		return map[string]any{"ok": false, "error": "File not found"}
	}
	revealInFileManager(p)
	return map[string]any{"ok": true}
}

// ---------- diagnostics ----------

func toolVersion(path string, args ...string) string {
	if path == "" {
		return "not found"
	}
	c := command(path, args...)
	var out strings.Builder
	c.Stdout = &out
	c.Stderr = &out
	if err := c.Start(); err != nil {
		return "found (" + filepath.Base(path) + ")"
	}
	attachChildJob(c)
	timer := time.AfterFunc(4*time.Second, func() { killTree(c) })
	_ = c.Wait()
	timer.Stop()
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(out.String(), "\r\n", "\n"), "\n")[0])
	if line == "" {
		return "found (" + filepath.Base(path) + ")"
	}
	return line
}

func scrubPaths(s string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	return s
}

func readLogTail(maxLines int) string {
	p := filepath.Join(appDir, "anysave.log")
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return "(no log entries yet)"
	}
	if len(data) > 16384 {
		data = data[len(data)-16384:]
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return scrubPaths(strings.Join(lines, "\n"))
}

func actionDiagnostics(msg map[string]any) map[string]any {
	yt := findTool("yt-dlp")
	ff := findTool("ffmpeg")
	dn := findTool("deno")
	ytVer := toolVersion(yt, "--version")
	ffVer := toolVersion(ff, "-version")
	dnVer := toolVersion(dn, "--version")
	extVer := str(msg, "extensionVersion")
	if extVer == "" {
		extVer = "unknown"
	}
	logTail := readLogTail(40)
	var b strings.Builder
	fmt.Fprintf(&b, "AnySave Diagnostics\n")
	fmt.Fprintf(&b, "===================\n")
	fmt.Fprintf(&b, "Extension: %s\n", extVer)
	fmt.Fprintf(&b, "Helper:    %s (%s/%s)\n", helperVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "yt-dlp:    %s\n", ytVer)
	fmt.Fprintf(&b, "ffmpeg:    %s\n", ffVer)
	fmt.Fprintf(&b, "deno:      %s\n", dnVer)
	fmt.Fprintf(&b, "Downloads: %s\n", scrubPaths(downloadDir))
	fmt.Fprintf(&b, "\nRecent log:\n%s\n", logTail)
	return map[string]any{
		"ok":            true,
		"helperVersion": helperVersion,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"ytdlp":         ytVer,
		"ffmpeg":        ffVer,
		"deno":          dnVer,
		"text":          b.String(),
	}
}

func main() {
	selfName := ""
	if self, err := os.Executable(); err == nil {
		selfName = strings.ToLower(filepath.Base(self))
		if real, err := filepath.EvalSymlinks(self); err == nil {
			self = real
		}
		appDir = filepath.Dir(self)
	}
	downloadDir = filepath.Join(userDownloadsDir(), "AnySave")

	// `anysave-host --check` prints which tools were found (used by installers).
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		fmt.Printf("helper: %s (%s/%s)\nyt-dlp: %s\nffmpeg: %s\ndeno: %s\n",
			helperVersion, runtime.GOOS, runtime.GOARCH,
			findTool("yt-dlp"), findTool("ffmpeg"), findTool("deno"))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(helperVersion)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		_ = runDaemon(false, "")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--app" {
		_ = runDaemon(true, "")
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "--add" {
		_ = sendLinkToDaemon(os.Args[2])
		return
	}
	// `anysave <link>...` from a terminal, an anysave:// link from the system, "Send to".
	if code, handled := runCLI(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 {
		arg1 := strings.TrimSpace(os.Args[1])
		l := strings.ToLower(arg1)
		// Windows "Send to -> AnySave" on a .url shortcut or .txt file of links.
		if (strings.HasSuffix(l, ".url") || strings.HasSuffix(l, ".txt")) && isFile(arg1) {
			if data, err := os.ReadFile(arg1); err == nil {
				urlRe := regexp.MustCompile(`https?://[^\s"'<>]+`)
				for _, m := range urlRe.FindAllString(string(data), -1) {
					_ = sendLinkToDaemon(m)
				}
			}
			return
		}
	}
	// If launched as `AnySave.exe` directly with no arguments, start the desktop app + tray.
	if len(os.Args) == 1 && (selfName == "anysave" || selfName == "anysave.exe") {
		_ = runDaemon(true, "")
		return
	}

	msg, err := readMessage()
	if err != nil {
		return
	}
	action := str(msg, "action")
	defer func() {
		if r := recover(); r != nil {
			logf("panic: %v", r)
			if action == "download" || action == "fastDownload" || action == "convert" {
				send(map[string]any{"type": "error", "message": fmt.Sprint(r)})
			} else {
				send(map[string]any{"ok": false, "error": fmt.Sprint(r)})
			}
		}
	}()
	switch action {
	case "info":
		send(actionInfo(msg))
	case "download":
		actionDownload(msg)
	case "fastDownload":
		actionFastDownload(msg)
	case "reveal":
		send(actionReveal(msg))
	case "open":
		send(actionOpen(msg))
	case "playlist":
		send(actionPlaylist(msg))
	case "probe":
		send(actionProbe(msg))
	case "pickFile":
		send(actionPickFile(msg))
	case "fileInfo":
		send(actionFileInfo(msg))
	case "hash":
		send(actionHash(msg))
	case "convert":
		actionConvert(msg)
	case "diagnostics":
		send(actionDiagnostics(msg))
	default:
		send(map[string]any{"ok": false, "error": "Unknown action: " + action})
	}
}

var plainLangRe = regexp.MustCompile(`^[A-Za-z]{2,3}$`)
var subLangRe = regexp.MustCompile(`^[A-Za-z0-9.*_-]{1,24}$`)

// extrasArgs turns the optional extras of a video download into yt-dlp arguments:
//
//	subLangs  "en,fr"  subtitles in these languages, put inside the video file (needs FFmpeg)
//	chapters  true     chapter marks inside the file (needs FFmpeg)
//	cover     true     the video's thumbnail as cover art inside the file (needs FFmpeg)
//
// A video without subtitles or chapters simply downloads without them.
func extrasArgs(msg map[string]any, hasFfmpeg bool) []string {
	if !hasFfmpeg {
		return nil
	}
	var args []string
	var langs []string
	for _, l := range strings.FieldsFunc(str(msg, "subLangs"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if subLangRe.MatchString(l) {
			// "en" also takes en-US and en-GB, "zh" takes zh-Hans and zh-Hant, "pt" takes pt-BR.
			// Not "en.*": YouTube lists dozens of machine-translated tracks such as "en-de",
			// and asking for all of them gets the download refused (HTTP 429).
			if plainLangRe.MatchString(l) {
				l += "(-[A-Z0-9][A-Za-z0-9]*)?"
			}
			langs = append(langs, l)
		}
	}
	audio, _ := msg["audioOnly"].(bool)
	if len(langs) > 0 && !audio {
		// Also the automatic captions when the uploader gave none. --ignore-errors turns a
		// subtitle that can't be fetched into a warning, so it never costs the video itself.
		args = append(args, "--write-subs", "--write-auto-subs", "--sub-langs", strings.Join(langs, ","),
			"--embed-subs", "--convert-subs", "srt", "--compat-options", "no-keep-subs", "--ignore-errors")
	}
	if on, _ := msg["chapters"].(bool); on {
		args = append(args, "--embed-chapters")
	}
	if on, _ := msg["cover"].(bool); on {
		args = append(args, "--embed-thumbnail", "--convert-thumbnails", "jpg")
	}
	return args
}
