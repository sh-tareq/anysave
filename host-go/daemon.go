// AnySave Desktop App & Persistent Background Daemon (Phase 2).
//
// Runs when invoked with `--daemon` (background service) or `--app` (desktop app window +
// menu bar / system tray). Manages persistent jobs in `jobs.json` and settings in `settings.json`
// inside `appDir` so downloads and conversions continue running even when all browsers and
// windows are closed.
package main

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"anysave-host/updater"
)

//go:embed desktop_assets/*
var desktopAssets embed.FS

type daemonInfo struct {
	Port    int    `json:"port"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

type daemonServer struct {
	powerToken  int  // changes whenever a download starts or settings change (cancels a pending sleep/shut down)
	quitting    bool // true just before the service stops, so the app can close as well
	mu          sync.Mutex
	port        int
	token       string
	selfBin     string
	extDir      string
	jobs        map[string]map[string]any
	settings    map[string]any
	activeProcs map[string]io.Writer // jobId -> stdin of worker process
	subscribers map[chan string]struct{}
	httpSrv     *http.Server
	quitCh      chan struct{}
	lastClip    string
}

var (
	videoSiteRegex = regexp.MustCompile(`(?i)(^|\.)(youtube\.com|youtu\.be|facebook\.com|fb\.watch|instagram\.com|tiktok\.com|vimeo\.com|x\.com|twitter\.com|twitch\.tv|reddit\.com|dailymotion\.com|soundcloud\.com)$`)
	fileExtRegex   = regexp.MustCompile(`(?i)\.(mp4|mkv|webm|mov|avi|m4v|3gp|flv|wmv|ts|mpe?g|mp3|m4a|aac|wav|flac|ogg|opus|wma|jpe?g|png|gif|webp|heic|bmp|svg|tiff?|pdf|docx?|xlsx?|pptx?|txt|csv|rtf|odt|ods|epub|zip|rar|7z|tar|gz|tgz|bz2|xz|iso|dmg|apk|exe|msi|pkg|deb|rpm|appimage|bin)$`)
)

func daemonInfoPath() string {
	return filepath.Join(appDir, "daemon.json")
}

func readDaemonInfo() (*daemonInfo, error) {
	b, err := os.ReadFile(daemonInfoPath())
	if err != nil {
		return nil, err
	}
	var info daemonInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func checkRunningDaemon() *daemonInfo {
	info, err := readDaemonInfo()
	if err != nil || info.Port <= 0 || info.Token == "" {
		return nil
	}
	client := &http.Client{Timeout: 600 * time.Millisecond}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/ping", info.Port), nil)
	req.Header.Set("X-AnySave-Token", info.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return info
	}
	return nil
}

func findExtensionDir() string {
	candidates := []string{
		filepath.Join(appDir, "extension"),
		filepath.Join(filepath.Dir(appDir), "Resources", "extension"), // macOS .app bundle
		filepath.Join(filepath.Dir(appDir), "extension"),              // repo / unpacked zip
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "AnySave", "extension"),
		)
	}
	for _, c := range candidates {
		if isFile(filepath.Join(c, "manager.html")) {
			return c
		}
	}
	return ""
}

func runDaemon(openWindow bool, initialURL string) error {
	if existing := checkRunningDaemon(); existing != nil {
		client := &http.Client{Timeout: 3 * time.Second}
		if initialURL != "" {
			body, _ := json.Marshal(map[string]any{"type": "addLink", "url": initialURL})
			req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/action", existing.Port), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-AnySave-Token", existing.Token)
			if r, err := client.Do(req); err == nil {
				r.Body.Close()
			}
		}
		if openWindow {
			openAppWindow(fmt.Sprintf("http://127.0.0.1:%d/?token=%s", existing.Port, existing.Token), false)
		}
		return nil
	}

	_ = os.MkdirAll(appDir, 0o755)
	tokBytes := make([]byte, 24)
	if _, err := rand.Read(tokBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokBytes)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	selfBin, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(selfBin); err == nil {
		selfBin = real
	}

	d := &daemonServer{
		port:        port,
		token:       token,
		selfBin:     selfBin,
		extDir:      findExtensionDir(),
		jobs:        make(map[string]map[string]any),
		settings:    defaultDaemonSettings(),
		activeProcs: make(map[string]io.Writer),
		subscribers: make(map[chan string]struct{}),
		quitCh:      make(chan struct{}),
	}
	d.loadPersistedState()

	mux := http.NewServeMux()
	d.registerRoutes(mux)
	d.httpSrv = &http.Server{
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
	}

	infoBytes, _ := json.MarshalIndent(daemonInfo{
		Port:    port,
		Token:   token,
		PID:     os.Getpid(),
		Version: helperVersion,
	}, "", "  ")
	_ = os.WriteFile(daemonInfoPath(), infoBytes, 0o600)
	defer os.Remove(daemonInfoPath())

	go func() {
		_ = d.httpSrv.Serve(ln)
	}()

	go d.watchClipboardLoop()
	go d.scheduleLoop()

	if initialURL != "" {
		go func() {
			_ = d.addLink(initialURL, "", "")
		}()
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if openWindow {
		openAppWindow(fmt.Sprintf("%s/?token=%s", baseURL, token), false)
	}

	runPlatformApp(baseURL, token, d)
	return nil
}

func defaultDaemonSettings() map[string]any {
	return map[string]any{
		"captureAll":      true,
		"downloadHandler": "anysave",
		"minSizeMb":       float64(0),
		"excludedSites":   "",
		"fileTypes":       "",
		"loginFastSites":  false,
		"clipboardWatch":  false,
		"dropBox":         false,
		"notifications":   true,
		"releaseChannel":  "stable",
		"maxSpeedKbps":    float64(0),
		"subLangs":        "",    // subtitles to put inside videos, e.g. "en" or "en,fr"
		"embedChapters":   true,  // chapter marks inside videos
		"embedCover":      false, // thumbnail as cover art inside videos and music
		"scheduleOn":      false, // only download inside the time window
		"scheduleStart":   "01:00",
		"scheduleStop":    "07:00",
		"scheduleQueue":   "", // "" = every download, or the name of one queue
		"queues":          "", // names of the user's extra queues, comma separated
		"proxy":           "", // "" = system proxy, "direct", or http://host:port / socks5://host:port
		"afterDoneAction": "none",
	}
}

func (d *daemonServer) jobsFilePath() string     { return filepath.Join(appDir, "jobs.json") }
func (d *daemonServer) settingsFilePath() string { return filepath.Join(appDir, "settings.json") }

func (d *daemonServer) loadPersistedState() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if b, err := os.ReadFile(d.settingsFilePath()); err == nil {
		var s map[string]any
		if json.Unmarshal(b, &s) == nil {
			for k, v := range s {
				d.settings[k] = v
			}
		}
	}
	if b, err := os.ReadFile(d.jobsFilePath()); err == nil {
		var j map[string]map[string]any
		if json.Unmarshal(b, &j) == nil {
			for id, job := range j {
				st, _ := job["status"].(string)
				if st == "starting" || st == "downloading" || st == "queued" {
					job["status"] = "paused"
					job["speed"] = ""
					job["eta"] = ""
					if k := str(job, "kind"); k == "transcribe" || k == "convert" || k == "subs" {
						// These can't continue where they stopped: offer Retry instead.
						job["status"] = "error"
						job["error"] = "Stopped when AnySave closed. Retry to start again."
					}
				}
				d.jobs[id] = job
			}
		}
	}
}

func (d *daemonServer) saveJobsLocked() {
	b, err := json.Marshal(d.jobs)
	if err != nil {
		return
	}
	tmp := d.jobsFilePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, d.jobsFilePath())
	}
}

func (d *daemonServer) saveSettingsLocked() {
	b, err := json.MarshalIndent(d.settings, "", "  ")
	if err != nil {
		return
	}
	tmp := d.settingsFilePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, d.settingsFilePath())
	}
}

func (d *daemonServer) broadcastLocked(includeSettings bool) {
	payload := map[string]any{
		"jobs": d.jobs,
	}
	if includeSettings {
		payload["settings"] = d.settings
		payload["version"] = helperVersion
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := string(b)
	for ch := range d.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (d *daemonServer) updateJob(id string, patch map[string]any) map[string]any {
	d.mu.Lock()
	cur, ok := d.jobs[id]
	if !ok {
		cur = map[string]any{
			"id":        id,
			"createdAt": float64(time.Now().UnixMilli()),
		}
		d.jobs[id] = cur
	}
	prevStatus, _ := cur["status"].(string)
	for k, v := range patch {
		cur[k] = v
	}
	newStatus, _ := cur["status"].(string)
	notifyOn, _ := d.settings["notifications"].(bool)
	title, _ := cur["title"].(string)
	fileName, _ := cur["fileName"].(string)
	d.saveJobsLocked()
	d.broadcastLocked(false)
	d.mu.Unlock()

	if notifyOn && prevStatus != "done" && newStatus == "done" {
		name := fileName
		if name == "" {
			name = title
		}
		go notifyOS("AnySave — Download Complete", name)
	}
	if prevStatus != "done" && newStatus == "done" {
		go d.afterAllDone(id)
	}
	if newStatus == "starting" && prevStatus != "starting" {
		d.mu.Lock()
		d.powerToken++ // a download started: call off a pending sleep / shut down
		d.mu.Unlock()
	}
	return cur
}

func (d *daemonServer) checkAuth(r *http.Request) bool {
	if tok := r.Header.Get("X-AnySave-Token"); tok == d.token {
		return true
	}
	if tok := r.URL.Query().Get("token"); tok == d.token {
		return true
	}
	return false
}

func (d *daemonServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		if !d.checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		if !d.checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ytVer := toolVersion(findTool("yt-dlp"), "--version")
		d.mu.Lock()
		resp, _ := json.Marshal(map[string]any{
			"ok":       true,
			"version":  helperVersion,
			"ytdlp":    ytVer,
			"jobs":     d.jobs,
			"settings": d.settings,
			"quitting": d.quitting,
		})
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	})

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		if !d.checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch := make(chan string, 16)
		d.mu.Lock()
		d.subscribers[ch] = struct{}{}
		initBytes, _ := json.Marshal(map[string]any{"jobs": d.jobs, "settings": d.settings, "version": helperVersion})
		d.mu.Unlock()

		defer func() {
			d.mu.Lock()
			delete(d.subscribers, ch)
			d.mu.Unlock()
		}()

		fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
		fl.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				fl.Flush()
			}
		}
	})

	mux.HandleFunc("/api/action", func(w http.ResponseWriter, r *http.Request) {
		if !d.checkAuth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res := d.handleAction(req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	mux.HandleFunc("/desktop-bridge.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := desktopAssets.ReadFile("desktop_assets/desktop-bridge.js")
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(b)
	})

	mux.HandleFunc("/drop", func(w http.ResponseWriter, r *http.Request) {
		b, _ := desktopAssets.ReadFile("desktop_assets/drop.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
		if p == "" || p == "." || p == "manager.html" {
			// The page carries the secret token, so it is only given to someone who already has it
			// (the app always opens it with ?token=...). Without this any local program could read it.
			if !d.checkAuth(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			mgrPath := filepath.Join(d.extDir, "manager.html")
			b, err := os.ReadFile(mgrPath)
			if err != nil {
				http.Error(w, "AnySave UI files not found. Run the installer again.", http.StatusNotFound)
				return
			}
			inject := fmt.Sprintf(`<script>window.__ANYSAVE_TOKEN=%q;</script><script src="/desktop-bridge.js"></script><script src="manager.js"></script>`, d.token)
			html := strings.Replace(string(b), `<script src="manager.js"></script>`, inject, 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(html))
			return
		}
		full := filepath.Join(d.extDir, p)
		rel, err := filepath.Rel(d.extDir, full)
		if err != nil || strings.HasPrefix(rel, "..") || !isFile(full) {
			http.NotFound(w, r)
			return
		}
		if ct := mime.TypeByExtension(filepath.Ext(full)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeFile(w, r, full)
	})
}

var (
	daemonVideoSitesRe = regexp.MustCompile(`(?i)(^|\.)(youtube\.com|youtu\.be|tiktok\.com|instagram\.com|facebook\.com|fb\.watch|twitter\.com|x\.com|vimeo\.com|dailymotion\.com|twitch\.tv|reddit\.com|bilibili\.com|soundcloud\.com)$`)
	daemonFileExtRe    = regexp.MustCompile(`(?i)\.(zip|rar|7z|tar|gz|tgz|bz2|xz|dmg|pkg|exe|msi|iso|apk|pdf|docx?|xlsx?|pptx?|csv|txt|epub|mp4|mkv|webm|mov|avi|mp3|m4a|flac|wav|ogg|opus|jpe?g|png|gif|webp|svg)$`)
	unsafeFileCharsRe  = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)
)

func newDaemonJobID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

func safeDaemonFileName(s string) string {
	clean := strings.TrimSpace(unsafeFileCharsRe.ReplaceAllString(s, "_"))
	if len(clean) > 120 {
		clean = clean[:120]
	}
	if clean == "" {
		return "download"
	}
	return clean
}

func siteLabelOf(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "Web"
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch {
	case strings.Contains(h, "youtube") || h == "youtu.be":
		return "YouTube"
	case strings.Contains(h, "tiktok"):
		return "TikTok"
	case strings.Contains(h, "instagram"):
		return "Instagram"
	case strings.Contains(h, "facebook") || h == "fb.watch":
		return "Facebook"
	case h == "x.com" || strings.Contains(h, "twitter"):
		return "X"
	default:
		parts := strings.Split(h, ".")
		if len(parts) > 0 && len(parts[0]) > 0 {
			return strings.ToUpper(parts[0][:1]) + parts[0][1:]
		}
		return "Web"
	}
}

func categoryFolderOf(name, kind string, audioOnly bool) string {
	if kind == "convert" {
		return "Converted"
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.Split(name, "?")[0]), "."))
	if audioOnly || isOneOf(ext, "mp3", "m4a", "aac", "wav", "flac", "ogg", "opus", "wma") {
		return "Music"
	}
	if kind == "video" || isOneOf(ext, "mp4", "mkv", "webm", "mov", "avi", "m4v", "flv", "wmv", "ts", "mpg", "mpeg", "3gp") {
		return "Videos"
	}
	if isOneOf(ext, "jpg", "jpeg", "png", "gif", "webp", "heic", "bmp", "svg", "tif", "tiff") {
		return "Pictures"
	}
	if isOneOf(ext, "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt", "csv", "rtf", "odt", "ods", "epub") {
		return "Documents"
	}
	if isOneOf(ext, "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "iso", "dmg") {
		return "Archives"
	}
	if isOneOf(ext, "apk", "exe", "msi", "pkg", "deb", "rpm", "appimage") {
		return "Apps"
	}
	return "Other"
}

func isOneOf(s string, list ...string) bool {
	for _, item := range list {
		if s == item {
			return true
		}
	}
	return false
}

func (d *daemonServer) handleAction(req map[string]any) map[string]any {
	action := str(req, "type")
	if action == "" {
		action = str(req, "action")
	}

	switch action {
	case "info":
		req["proxy"] = d.proxySetting()
		return actionInfo(req)
	case "playlist":
		req["proxy"] = d.proxySetting()
		return actionPlaylist(req)
	case "probe":
		req["proxy"] = d.proxySetting()
		return actionProbe(req)
	case "crawl":
		req["proxy"] = d.proxySetting()
		return actionCrawl(req)
	case "pickFile":
		return actionPickFile(req)
	case "fileInfo":
		return actionFileInfo(req)
	case "hashFile":
		file := ""
		d.mu.Lock()
		if j, ok := d.jobs[str(req, "id")]; ok {
			file = str(j, "file")
		}
		d.mu.Unlock()
		if file == "" {
			return map[string]any{"ok": false, "error": "File not found"}
		}
		return actionHash(map[string]any{"path": file})
	case "diagnostics":
		res := actionDiagnostics(req)
		d.mu.Lock()
		var failedLines []string
		for _, j := range d.jobs {
			if str(j, "status") == "error" && len(failedLines) < 5 {
				failedLines = append(failedLines, fmt.Sprintf("  - [%s] %s: %s", str(j, "kind"), str(j, "site"), str(j, "error")))
			}
		}
		d.mu.Unlock()
		if len(failedLines) > 0 {
			if txt, ok := res["text"].(string); ok {
				res["text"] = txt + "\nRecent failed jobs:\n" + strings.Join(failedLines, "\n") + "\n"
			}
		}
		return res

	case "open":
		file := str(req, "file")
		if file == "" {
			if id := str(req, "id"); id != "" {
				d.mu.Lock()
				if j, ok := d.jobs[id]; ok {
					file = str(j, "file")
					if t := str(j, "txt"); t != "" {
						file = t // a transcript opens as text
					}
				}
				d.mu.Unlock()
			}
		}
		if file == "" {
			return map[string]any{"ok": false, "error": "File not found"}
		}
		return actionOpen(map[string]any{"file": file})

	case "reveal", "revealDirect":
		file := str(req, "file")
		if file == "" {
			if id := str(req, "id"); id != "" {
				d.mu.Lock()
				if j, ok := d.jobs[id]; ok {
					file = str(j, "file")
				}
				d.mu.Unlock()
			}
		}
		if file == "" {
			return map[string]any{"ok": false, "error": "File not found"}
		}
		return actionReveal(map[string]any{"file": file})

	case "addLink":
		return d.addLink(str(req, "url"), str(req, "title"), str(req, "queue"))

	case "download":
		id := d.startVideoJob(req)
		return map[string]any{"ok": true, "id": id}

	case "directDownload":
		src := str(req, "src")
		title := str(req, "title")
		id := d.startFastFileJob(src, safeDaemonFileName(title)+".mp4", title, "", "", "")
		return map[string]any{"ok": true, "id": id}

	case "file":
		id := d.startFastFileJob(str(req, "url"), str(req, "name"), str(req, "title"), d.jobInQueue(str(req, "id"), str(req, "queue")), str(req, "referrer"), str(req, "sha256"))
		return map[string]any{"ok": true, "id": id}

	case "chooseHandler":
		id := str(req, "id")
		d.mu.Lock()
		j := d.jobs[id]
		d.mu.Unlock()
		if j != nil {
			d.startFastFileJob(str(j, "url"), str(j, "fileName"), str(j, "title"), id, str(j, "referrer"), str(j, "expectedSha256"))
		}
		return map[string]any{"ok": true}

	case "convert":
		id := d.startConvertJob(req)
		return map[string]any{"ok": true, "id": id}

	case "transcribe":
		id := d.startTranscribeJob(req)
		return map[string]any{"ok": true, "id": id}

	case "subs":
		id := d.startSubsJob(req)
		return map[string]any{"ok": true, "id": id}

	case "searchTranscripts":
		return actionSearchTranscripts(req)

	case "openPath":
		return actionOpen(map[string]any{"file": str(req, "file")}) // only files inside Downloads/AnySave

	case "pause":
		d.pauseJob(str(req, "id"))
		return map[string]any{"ok": true}

	case "resume", "startNow":
		// The user asked for this one by hand: it runs even outside the time window.
		d.updateJob(str(req, "id"), map[string]any{"ignoreSchedule": true, "heldBySchedule": false})
		d.resumeJob(str(req, "id"))
		return map[string]any{"ok": true}

	case "moveQueue":
		d.updateJob(str(req, "id"), map[string]any{"queue": strings.TrimSpace(str(req, "queue")), "ignoreSchedule": false})
		d.scheduleTick()
		return map[string]any{"ok": true}

	case "cancel":
		d.cancelJob(str(req, "id"))
		return map[string]any{"ok": true}

	case "retry":
		d.retryJob(str(req, "id"))
		return map[string]any{"ok": true}

	case "refreshUrl":
		id := str(req, "id")
		newURL := strings.TrimSpace(str(req, "url"))
		if id == "" || !webLink(newURL) {
			return map[string]any{"ok": false, "error": "Please enter a valid http(s) link."}
		}
		d.updateJob(id, map[string]any{
			"url":   newURL,
			"site":  siteLabelOf(newURL),
			"error": nil,
		})
		d.resumeJob(id)
		return map[string]any{"ok": true}

	case "remove":
		id := str(req, "id")
		d.mu.Lock()
		delete(d.jobs, id)
		d.saveJobsLocked()
		d.broadcastLocked(false)
		d.mu.Unlock()
		return map[string]any{"ok": true}

	case "clear":
		d.mu.Lock()
		for k, j := range d.jobs {
			st := str(j, "status")
			if st == "done" || st == "error" || st == "cancelled" {
				delete(d.jobs, k)
			}
		}
		d.saveJobsLocked()
		d.broadcastLocked(false)
		d.mu.Unlock()
		return map[string]any{"ok": true}

	case "seen":
		return map[string]any{"ok": true}

	case "setCapture":
		on, _ := req["on"].(bool)
		d.mu.Lock()
		d.settings["captureAll"] = on
		d.saveSettingsLocked()
		d.broadcastLocked(true)
		d.mu.Unlock()
		return map[string]any{"ok": true}

	case "setSettings":
		patch, _ := req["patch"].(map[string]any)
		d.mu.Lock()
		for k, v := range patch {
			d.settings[k] = v
		}
		dropOn, hasDrop := patch["dropBox"].(bool)
		for k := range patch {
			if strings.HasPrefix(k, "schedule") {
				defer d.scheduleTick() // the window or its switch changed: apply it at once
				break
			}
		}
		if _, changed := patch["afterDoneAction"]; changed {
			d.powerToken++ // calls off a pending sleep / shut down
		}
		d.saveSettingsLocked()
		d.broadcastLocked(true)
		d.mu.Unlock()
		if hasDrop && dropOn {
			go openAppWindow(fmt.Sprintf("http://127.0.0.1:%d/drop?token=%s", d.port, d.token), true)
		}
		return map[string]any{"ok": true, "settings": d.settings}

	case "getSettings":
		d.mu.Lock()
		defer d.mu.Unlock()
		return map[string]any{"ok": true, "settings": d.settings}

	case "downloadClipboard":
		clip := strings.TrimSpace(readClipboardText())
		if !webLink(clip) {
			return map[string]any{"ok": false, "error": "Clipboard does not contain an http(s) link"}
		}
		d.mu.Lock()
		d.lastClip = clip
		d.mu.Unlock()
		return d.addLink(clip, "", "")

	case "openMainWindow":
		go openAppWindow(fmt.Sprintf("http://127.0.0.1:%d/?token=%s", d.port, d.token), false)
		return map[string]any{"ok": true}

	case "openDropBox":
		go openAppWindow(fmt.Sprintf("http://127.0.0.1:%d/drop?token=%s", d.port, d.token), true)
		return map[string]any{"ok": true}

	case "selfUpdate":
		return d.selfUpdate(req)

	case "syncJob":
		id := str(req, "id")
		if patch, ok := req["patch"].(map[string]any); ok && id != "" {
			d.updateJob(id, patch)
		}
		return map[string]any{"ok": true}

	case "quit":
		select {
		case <-d.quitCh:
		default:
			close(d.quitCh)
		}
		return map[string]any{"ok": true}

	default:
		return map[string]any{"ok": false, "error": "Unknown action: " + action}
	}
}

// proxySetting is the user's proxy choice ("" = system proxy).
func (d *daemonServer) proxySetting() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, _ := d.settings["proxy"].(string)
	return p
}

func (d *daemonServer) addLink(rawURL, title, queue string) map[string]any {
	rawURL = strings.TrimSpace(rawURL)
	u, err := neturl.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return map[string]any{"ok": false, "error": "That isn't a web link."}
	}
	video := func() map[string]any {
		id := d.startVideoJob(map[string]any{
			"url":    rawURL,
			"title":  title,
			"format": "bv*+ba/b",
			"sort":   h264Sort,
			"queue":  queue,
		})
		return map[string]any{"ok": true, "id": id}
	}
	host := strings.ToLower(u.Hostname())
	if daemonVideoSitesRe.MatchString(host) {
		return video()
	}
	if daemonFileExtRe.MatchString(u.Path) {
		id := d.startFastFileJob(rawURL, "", title, d.jobInQueue("", queue), "", "")
		return map[string]any{"ok": true, "id": id}
	}
	p := actionProbe(map[string]any{"url": rawURL, "proxy": d.proxySetting()})
	if ok, _ := p["ok"].(bool); ok {
		if isF, _ := p["isFile"].(bool); isF {
			id := d.startFastFileJob(rawURL, str(p, "name"), title, d.jobInQueue("", queue), "", "")
			return map[string]any{"ok": true, "id": id}
		}
	}
	return video()
}

func (d *daemonServer) startVideoJob(req map[string]any) string {
	id := str(req, "id")
	if id == "" {
		id = newDaemonJobID()
	}
	rawURL := str(req, "url")
	title := str(req, "title")
	if title == "" {
		title = rawURL
	}
	formatSel := str(req, "format")
	if formatSel == "" {
		formatSel = "bv*+ba/b"
	}
	sortSel := str(req, "sort")
	if sortSel == "" {
		sortSel = h264Sort
	}
	audioOnly, _ := req["audioOnly"].(bool)
	qualityLabel := str(req, "qualityLabel")
	if qualityLabel == "" {
		if audioOnly {
			qualityLabel = "MP3"
		} else {
			qualityLabel = "Best MP4"
		}
	}
	folder := "Videos"
	if audioOnly {
		folder = "Music"
	}

	d.updateJob(id, map[string]any{
		"kind":         "video",
		"url":          rawURL,
		"title":        title,
		"site":         siteLabelOf(rawURL),
		"format":       formatSel,
		"sort":         sortSel,
		"audioOnly":    audioOnly,
		"qualityLabel": qualityLabel,
		"status":       "starting",
		"percent":      0.0,
		"speed":        "",
		"eta":          "",
		"error":        nil,
	})
	if q := strings.TrimSpace(str(req, "queue")); q != "" {
		d.updateJob(id, map[string]any{"queue": q})
	}
	if d.holdForSchedule(id) {
		return id
	}

	// If title was just the URL, fetch the real video title in the background.
	if title == rawURL {
		go func() {
			info := actionInfo(map[string]any{"url": rawURL})
			if ok, _ := info["ok"].(bool); ok && str(info, "title") != "" {
				d.updateJob(id, map[string]any{"title": str(info, "title")})
			}
		}()
	}

	d.mu.Lock()
	maxSpeed, _ := d.settings["maxSpeedKbps"].(float64)
	subLangs, _ := d.settings["subLangs"].(string)
	chapters, _ := d.settings["embedChapters"].(bool)
	cover, _ := d.settings["embedCover"].(bool)
	proxy, _ := d.settings["proxy"].(string)
	d.mu.Unlock()
	if reqSpeed, ok := req["maxSpeedKbps"].(float64); ok && reqSpeed >= 0 {
		maxSpeed = reqSpeed
	}

	go d.startDaemonWorker(id, map[string]any{
		"action":       "download",
		"job":          id,
		"url":          rawURL,
		"format":       formatSel,
		"sort":         sortSel,
		"audioOnly":    audioOnly,
		"folder":       folder,
		"maxSpeedKbps": maxSpeed,
		"subLangs":     subLangs,
		"chapters":     chapters,
		"cover":        cover,
		"proxy":        proxy,
	})
	return id
}

func (d *daemonServer) startFastFileJob(rawURL, name, title, id, referrer, sha256 string) string {
	if id == "" {
		id = newDaemonJobID()
	}
	if name == "" {
		if u, err := neturl.Parse(rawURL); err == nil {
			base, _ := neturl.PathUnescape(filepath.Base(u.Path))
			if base != "" && base != "/" && base != "." {
				name = base
			}
		}
	}
	fileName := safeDaemonFileName(name)
	if title == "" {
		title = fileName
	}
	folder := categoryFolderOf(fileName, "file", false)

	d.updateJob(id, map[string]any{
		"kind":           "file",
		"engine":         "fast",
		"url":            rawURL,
		"title":          title,
		"fileName":       fileName,
		"site":           siteLabelOf(rawURL),
		"status":         "starting",
		"percent":        0.0,
		"speed":          "",
		"eta":            "",
		"qualityLabel":   "Fast engine",
		"referrer":       referrer,
		"expectedSha256": sha256,
		"error":          nil,
	})
	if d.holdForSchedule(id) {
		return id
	}

	d.mu.Lock()
	maxSpeed, _ := d.settings["maxSpeedKbps"].(float64)
	d.mu.Unlock()

	go d.startDaemonWorker(id, map[string]any{
		"action":       "fastDownload",
		"job":          id,
		"url":          rawURL,
		"fileName":     fileName,
		"folder":       folder,
		"referrer":     referrer,
		"sha256":       sha256,
		"handover":     false,
		"maxSpeedKbps": maxSpeed,
		"proxy":        d.proxySetting(),
	})
	return id
}

func (d *daemonServer) startConvertJob(req map[string]any) string {
	id := str(req, "id")
	if id == "" {
		id = newDaemonJobID()
	}
	path := str(req, "path")
	name := str(req, "name")
	spec, _ := req["spec"].(map[string]any)
	fmtName := strings.ToUpper(str(spec, "format"))
	if fmtName == "" {
		fmtName = "MP4"
	}

	d.updateJob(id, map[string]any{
		"kind":         "convert",
		"url":          path,
		"title":        name,
		"spec":         spec,
		"site":         "Local file",
		"qualityLabel": "→ " + fmtName,
		"status":       "starting",
		"percent":      0.0,
		"speed":        "",
		"eta":          "",
		"error":        nil,
	})

	go d.startDaemonWorker(id, map[string]any{
		"action": "convert",
		"path":   path,
		"spec":   spec,
	})
	return id
}

var transcribeLangNames = map[string]string{"auto": "Auto-detected", "en": "English", "bn": "Bangla", "hi": "Hindi", "ur": "Urdu",
	"ar": "Arabic", "es": "Spanish", "fr": "French", "de": "German", "pt": "Portuguese", "ru": "Russian", "tr": "Turkish",
	"id": "Indonesian", "zh": "Chinese", "ja": "Japanese", "ko": "Korean"}

// startTranscribeJob turns the speech in a finished download into text and subtitles.
func (d *daemonServer) startTranscribeJob(req map[string]any) string {
	id := str(req, "id")
	if id == "" {
		id = newDaemonJobID()
	}
	lang := str(req, "lang")
	if !langCodeRe.MatchString(lang) {
		lang = "en"
	}
	label := transcribeLangNames[lang]
	if label == "" {
		label = lang
	}
	d.mu.Lock()
	d.settings["transcribeLang"] = lang
	d.saveSettingsLocked()
	d.mu.Unlock()
	d.updateJob(id, map[string]any{
		"kind": "transcribe", "url": str(req, "path"), "title": str(req, "name"), "lang": lang,
		"site": "Local file", "qualityLabel": label + " text", "status": "starting", "percent": 0.0,
		"speed": "", "eta": "", "error": nil, "file": nil, "txt": nil,
	})
	go d.startDaemonWorker(id, map[string]any{"action": "transcribe", "path": str(req, "path"), "lang": lang, "proxy": d.proxySetting()})
	return id
}

// startSubsJob puts a transcript's subtitles into its video (as a track, or drawn on the picture).
func (d *daemonServer) startSubsJob(req map[string]any) string {
	id := str(req, "id")
	if id == "" {
		id = newDaemonJobID()
	}
	mode := str(req, "mode")
	label := "Subtitles inside (switchable)"
	if mode == "burn" {
		label = "Captions drawn on the video"
	} else {
		mode = "embed"
	}
	d.updateJob(id, map[string]any{
		"kind": "subs", "url": str(req, "video"), "srt": str(req, "srt"), "lang": str(req, "lang"), "mode": mode,
		"title": str(req, "name"), "site": "Local file", "qualityLabel": label, "status": "starting", "percent": 0.0,
		"speed": "", "eta": "", "error": nil, "file": nil,
	})
	go d.startDaemonWorker(id, map[string]any{"action": "subs", "video": str(req, "video"), "srt": str(req, "srt"),
		"lang": str(req, "lang"), "mode": mode})
	return id
}

func (d *daemonServer) startDaemonWorker(id string, payload map[string]any) {
	cmd := command(d.selfBin)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		d.updateJob(id, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		d.updateJob(id, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		d.updateJob(id, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	attachChildJob(cmd)

	d.mu.Lock()
	d.activeProcs[id] = stdinPipe
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.activeProcs, id)
		d.mu.Unlock()
		_ = stdinPipe.Close()
		_ = cmd.Wait()
	}()

	// Write native-messaging framed payload to worker stdin.
	b, _ := json.Marshal(payload)
	_ = binary.Write(stdinPipe, binary.LittleEndian, uint32(len(b)))
	_, _ = stdinPipe.Write(b)

	for {
		var n uint32
		if err := binary.Read(stdoutPipe, binary.LittleEndian, &n); err != nil {
			break
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(stdoutPipe, buf); err != nil {
			break
		}
		var msg map[string]any
		if json.Unmarshal(buf, &msg) != nil {
			continue
		}
		switch str(msg, "type") {
		case "progress":
			patch := map[string]any{
				"status":  "downloading",
				"percent": msg["percent"],
				"speed":   str(msg, "speed"),
				"eta":     str(msg, "eta"),
			}
			if tb, ok := msg["totalBytes"].(float64); ok && tb > 0 {
				patch["totalBytes"] = tb
			}
			if db, ok := msg["doneBytes"].(float64); ok && db > 0 {
				patch["doneBytes"] = db
			}
			if ac, ok := msg["activeConns"].(float64); ok && ac > 1 {
				patch["qualityLabel"] = fmt.Sprintf("Fast (%.0fx)", ac)
			}
			if segs, ok := msg["segments"]; ok && segs != nil {
				patch["segments"] = segs
			}
			d.updateJob(id, patch)

		case "done":
			file := str(msg, "file")
			fileName := str(msg, "fileName")
			if fileName == "" && file != "" {
				fileName = filepath.Base(file)
			}
			patch := map[string]any{
				"status":     "done",
				"percent":    100.0,
				"file":       file,
				"fileName":   fileName,
				"speed":      "",
				"eta":        "",
				"finishedAt": float64(time.Now().UnixMilli()),
			}
			if tb, ok := msg["totalBytes"].(float64); ok && tb > 0 {
				patch["totalBytes"] = tb
				patch["doneBytes"] = tb
			} else if file != "" {
				if st, err := os.Stat(file); err == nil {
					patch["totalBytes"] = float64(st.Size())
					patch["doneBytes"] = float64(st.Size())
				}
			}
			if sha := str(msg, "sha256"); sha != "" {
				patch["sha256"] = sha
			}
			if t := str(msg, "txt"); t != "" {
				patch["txt"] = t
			}
			if mc, _ := msg["multiConn"].(bool); mc {
				if pc, ok := msg["peakConns"].(float64); ok && pc > 1 {
					patch["qualityLabel"] = fmt.Sprintf("Fast (%.0fx)", pc)
				}
			}
			d.updateJob(id, patch)
			return

		case "paused":
			// Paused by the schedule (not by the user): it waits for the next time window.
			d.mu.Lock()
			held, _ := d.jobs[id]["heldBySchedule"].(bool)
			d.mu.Unlock()
			status := "paused"
			if held {
				status = "scheduled"
			}
			d.updateJob(id, map[string]any{"status": status, "speed": "", "eta": ""})
			return

		case "error", "fallback":
			errMsg := str(msg, "message")
			if errMsg == "" {
				errMsg = str(msg, "reason")
			}
			st := "error"
			if errMsg == "Cancelled" {
				st = "cancelled"
			}
			d.updateJob(id, map[string]any{"status": st, "error": errMsg, "speed": "", "eta": ""})
			return
		}
	}
}

func (d *daemonServer) sendWorkerControl(id, action string) bool {
	d.mu.Lock()
	w, ok := d.activeProcs[id]
	d.mu.Unlock()
	if !ok || w == nil {
		return false
	}
	b, _ := json.Marshal(map[string]any{"action": action})
	_ = binary.Write(w, binary.LittleEndian, uint32(len(b)))
	_, _ = w.Write(b)
	return true
}

func (d *daemonServer) pauseJob(id string) {
	d.mu.Lock()
	st := str(d.jobs[id], "status")
	d.mu.Unlock()
	if st == "scheduled" || st == "queued" { // not running yet: just stop it from starting
		d.updateJob(id, map[string]any{"status": "paused", "heldBySchedule": false})
		return
	}
	if d.sendWorkerControl(id, "pause") {
		d.updateJob(id, map[string]any{"status": "paused", "speed": "", "eta": "", "heldBySchedule": false})
	}
}

func (d *daemonServer) resumeJob(id string) {
	d.mu.Lock()
	j := d.jobs[id]
	d.mu.Unlock()
	if j == nil {
		return
	}
	if str(j, "kind") == "file" {
		d.startFastFileJob(str(j, "url"), str(j, "fileName"), str(j, "title"), id, str(j, "referrer"), str(j, "expectedSha256"))
	} else if str(j, "kind") == "video" {
		d.startVideoJob(j)
	}
}

func (d *daemonServer) cancelJob(id string) {
	d.sendWorkerControl(id, "cancel")
	d.updateJob(id, map[string]any{"status": "cancelled", "speed": "", "eta": ""})
}

func (d *daemonServer) retryJob(id string) {
	d.mu.Lock()
	j := d.jobs[id]
	d.mu.Unlock()
	if j == nil {
		return
	}
	switch str(j, "kind") {
	case "file":
		d.startFastFileJob(str(j, "url"), str(j, "fileName"), str(j, "title"), id, str(j, "referrer"), str(j, "expectedSha256"))
	case "convert":
		d.startConvertJob(map[string]any{
			"id":   id,
			"path": str(j, "url"),
			"name": str(j, "title"),
			"spec": j["spec"],
		})
	case "transcribe":
		d.startTranscribeJob(map[string]any{"id": id, "path": str(j, "url"), "name": str(j, "title"), "lang": str(j, "lang")})
	case "subs":
		d.startSubsJob(map[string]any{"id": id, "video": str(j, "url"), "srt": str(j, "srt"), "lang": str(j, "lang"),
			"mode": str(j, "mode"), "name": str(j, "title")})
	default:
		d.startVideoJob(j)
	}
}

func (d *daemonServer) watchClipboardLoop() {
	// Initialize lastClip with current clipboard so we don't immediately download whatever was already on the clipboard at startup.
	if initClip := strings.TrimSpace(readClipboardText()); initClip != "" {
		d.mu.Lock()
		d.lastClip = initClip
		d.mu.Unlock()
	}
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-d.quitCh:
			return
		case <-ticker.C:
			d.mu.Lock()
			watchOn, _ := d.settings["clipboardWatch"].(bool)
			prev := d.lastClip
			d.mu.Unlock()
			if !watchOn {
				continue
			}
			clip := strings.TrimSpace(readClipboardText())
			if clip == "" || clip == prev {
				continue
			}
			d.mu.Lock()
			d.lastClip = clip
			d.mu.Unlock()
			if !webLink(clip) {
				continue
			}
			u, err := neturl.Parse(clip)
			if err != nil {
				continue
			}
			if daemonVideoSitesRe.MatchString(u.Hostname()) || daemonFileExtRe.MatchString(u.Path) {
				logf("clipboard watch auto-adding: %s", clip)
				d.addLink(clip, "", "")
			}
		}
	}
}

func (d *daemonServer) selfUpdate(req map[string]any) map[string]any {
	ytdlp := findTool("yt-dlp")
	ytUpdated := false
	ytMsg := ""
	if ytdlp != "" {
		cmd := command(ytdlp, "-U")
		out, err := cmd.CombinedOutput()
		ytMsg = strings.TrimSpace(string(out))
		if err == nil {
			ytUpdated = true
		}
	}
	// If a signed update manifest URL is provided, verify it with the Ed25519 public key.
	manifestURL := str(req, "manifestUrl")
	pubKey := str(req, "publicKey")
	if manifestURL != "" && pubKey != "" {
		client := &http.Client{Timeout: 10 * time.Second}
		if resp, err := client.Get(manifestURL); err == nil {
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			d.mu.Lock()
			chStr, _ := d.settings["releaseChannel"].(string)
			d.mu.Unlock()
			if chStr == "" {
				chStr = "stable"
			}
			m, verr := updater.Verify(raw, pubKey, updater.Channel(chStr), helperVersion)
			if verr != nil {
				return map[string]any{"ok": false, "error": "Update signature verification failed: " + verr.Error()}
			}
			return map[string]any{
				"ok":        true,
				"ytUpdated": ytUpdated,
				"version":   m.Version,
				"message":   fmt.Sprintf("yt-dlp updated (%s) · Verified manifest v%s", toolVersion(ytdlp, "--version"), m.Version),
			}
		}
	}
	return map[string]any{
		"ok":        true,
		"ytUpdated": ytUpdated,
		"version":   helperVersion,
		"message":   fmt.Sprintf("yt-dlp %s · AnySave v%s is up to date ✓", toolVersion(ytdlp, "--version"), helperVersion),
		"details":   ytMsg,
	}
}

// sendLinkToDaemon forwards a URL (from `anysave://` or "Send to" / CLI) to the running daemon,
// starting the daemon in the background if it isn't running yet.
func sendLinkToDaemon(rawURL string) error {
	_, err := daemonAction(map[string]any{"type": "addLink", "url": cleanLink(rawURL)})
	return err
}
