// VidGrab native messaging host (macOS + Windows).
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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const h264Sort = "res,fps,vcodec:h264,acodec:m4a"

var (
	appDir      string // folder holding this executable
	downloadDir string
	writeMu     sync.Mutex
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
	f, err := os.OpenFile(filepath.Join(appDir, "vidgrab.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
		return "", nil, errors.New("yt-dlp not found. Re-run the VidGrab installer.")
	}
	args := []string{"--no-playlist", "--no-warnings", "--color", "never", "--encoding", "utf-8"}
	if ff := findTool("ffmpeg"); ff != "" {
		args = append(args, "--ffmpeg-location", ff)
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
	f, err := os.CreateTemp("", "vidgrab-*.txt") // mode 0600
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
		msg = "yt-dlp failed (see vidgrab.log)"
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
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	cmd := command(ytdlp, append(args, "-J", url)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
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
	if err != nil {
		fail(err.Error())
		return
	}
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		fail(err.Error())
		return
	}
	fmtSel, sortSel := str(msg, "format"), str(msg, "sort")
	if fmtSel == "" {
		fmtSel = "b"
	}
	if sortSel == "" {
		sortSel = h264Sort
	}
	args = append(args,
		"-f", fmtSel, "-S", sortSel,
		"-o", filepath.Join(downloadDir, "%(title).150B [%(id)s]%(height& {}p|)s.%(ext)s"),
		"--newline", "--progress", "--no-simulate",
		"--progress-template", "download:VGP %(progress.downloaded_bytes)s|%(progress.total_bytes)s|"+
			"%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s",
		"--print", "after_move:VGF %(filepath)s",
	)
	hasFfmpeg := findTool("ffmpeg") != ""
	if audio, _ := msg["audioOnly"].(bool); audio && hasFfmpeg {
		args = append(args, "-x", "--audio-format", "mp3")
	} else if hasFfmpeg {
		args = append(args, "--merge-output-format", "mp4")
	}
	args = append(args, str(msg, "url"))
	logf("download: %s %q", ytdlp, args)

	started := time.Now().Add(-2 * time.Second)
	cmd := command(ytdlp, args...)
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		fail(err.Error())
		return
	}

	// Chrome closes our stdin when the user cancels: stop yt-dlp and its children.
	var cancelled atomic.Bool
	go func() {
		for {
			if _, err := readMessage(); err != nil {
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
		case strings.HasPrefix(line, "VGP "):
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
		case strings.HasPrefix(line, "VGF "):
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
	case cancelled.Load() && err != nil:
		removePartials(started)
		fail("Cancelled")
	case err == nil:
		var file any
		if finalFile != "" {
			file = finalFile
		}
		send(map[string]any{"type": "done", "file": file})
	default:
		fail(lastError(stderr.String()))
	}
}

// removePartials deletes the half-finished pieces a cancelled download leaves
// behind (.part / .ytdl files and per-format video/audio parts like "x.f399.mp4"),
// only for files written since this download started.
func removePartials(since time.Time) {
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		info, err := e.Info()
		if err != nil || e.IsDir() || info.ModTime().Before(since) {
			continue
		}
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") ||
			strings.Contains(name, ".part-Frag") || partFormat.MatchString(name) {
			os.Remove(filepath.Join(downloadDir, name))
		}
	}
}

var partFormat = regexp.MustCompile(`\.f\d+[\w-]*\.\w+$`)

// ---------- reveal ----------

func actionReveal(msg map[string]any) map[string]any {
	p, err := filepath.Abs(str(msg, "file"))
	base, _ := filepath.Abs(downloadDir)
	if err != nil || !strings.HasPrefix(p, base+string(filepath.Separator)) || !isFile(p) {
		return map[string]any{"ok": false, "error": "File not found"}
	}
	revealInFileManager(p)
	return map[string]any{"ok": true}
}

func main() {
	if self, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(self); err == nil {
			self = real
		}
		appDir = filepath.Dir(self)
	}
	home, _ := os.UserHomeDir()
	downloadDir = filepath.Join(home, "Downloads", "VidGrab")

	// `vidgrab-host --check` prints which tools were found (used by installers).
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		fmt.Printf("yt-dlp: %s\nffmpeg: %s\n", findTool("yt-dlp"), findTool("ffmpeg"))
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
			if action == "download" {
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
	case "reveal":
		send(actionReveal(msg))
	default:
		send(map[string]any{"ok": false, "error": "Unknown action: " + action})
	}
}
