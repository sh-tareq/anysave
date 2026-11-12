// Download-manager and converter actions for the AnySave helper (2.0):
//
//	probe     -> is a link a file or a web page?  {ok, isFile, name, mime, length}
//	pickFile  -> native "choose a file" dialog     {ok, path, name, size}
//	convert   -> FFmpeg conversion, streams {type: progress|done|error}
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"vidgrab-host/engine"
)

// ---------- probe ----------

const fileUA = "AnySave/1.0 (+https://github.com/sh-tareq/anysave)"

func actionProbe(msg map[string]any) map[string]any {
	raw := str(msg, "url")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return map[string]any{"ok": false, "error": "Not a web link"}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	try := func(ua string) (*http.Response, error) {
		req, _ := http.NewRequest("HEAD", raw, nil)
		req.Header.Set("User-Agent", ua)
		return client.Do(req)
	}
	resp, err := try(fileUA)
	if err == nil && resp.StatusCode == 403 {
		resp.Body.Close()
		resp, err = try("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36")
	}
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	ctype := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	disp := resp.Header.Get("Content-Disposition")
	name := ""
	if disp != "" {
		if _, params, err := mime.ParseMediaType(disp); err == nil {
			name = params["filename"]
		}
	}
	if name == "" {
		name, _ = url.PathUnescape(filepath.Base(resp.Request.URL.Path))
		if name == "/" || name == "." {
			name = ""
		}
	}
	isHTML := ctype == "" || strings.HasPrefix(ctype, "text/html") || strings.Contains(ctype, "xhtml")
	isFile := resp.StatusCode >= 200 && resp.StatusCode < 300 && (!isHTML || strings.Contains(disp, "attachment"))
	return map[string]any{"ok": true, "isFile": isFile, "name": name, "mime": ctype, "length": resp.ContentLength}
}

// ---------- pick a file ----------

func actionPickFile(msg map[string]any) map[string]any {
	path, err := pickFile()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	if path == "" {
		return map[string]any{"ok": false, "cancelled": true}
	}
	st, err := os.Stat(path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true, "path": path, "name": filepath.Base(path), "size": st.Size(),
		"duration": probeDuration(path)}
}

// actionFileInfo gives the size and length of a file AnySave saved (for the converter).
func actionFileInfo(msg map[string]any) map[string]any {
	p, err := filepath.Abs(str(msg, "path"))
	base, _ := filepath.Abs(downloadDir)
	if err != nil || !strings.HasPrefix(p, base+string(filepath.Separator)) {
		return map[string]any{"ok": false, "error": "File not found"}
	}
	st, err := os.Stat(p)
	if err != nil {
		return map[string]any{"ok": false, "error": "File not found"}
	}
	return map[string]any{"ok": true, "size": st.Size(), "duration": probeDuration(p)}
}

var durRe = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

// probeDuration reads "Duration: 00:09:56.48" from ffmpeg -i (no ffprobe needed).
func probeDuration(path string) float64 {
	ff := findTool("ffmpeg")
	if ff == "" {
		return 0
	}
	out, _ := command(ff, "-hide_banner", "-i", path).CombinedOutput()
	m := durRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	s, _ := strconv.ParseFloat(m[3], 64)
	return float64(h*3600+mi*60) + s
}

// ---------- convert ----------

type convertSpec struct {
	Format      string   `json:"format"`
	Height      *int     `json:"height"`
	TargetMb    *int     `json:"targetMb"`
	StartSec    *float64 `json:"startSec"`
	EndSec      *float64 `json:"endSec"`
	RemoveAudio bool     `json:"removeAudio"`
}

var (
	encOnce sync.Once
	encSet  map[string]bool
)

func encoders(ff string) map[string]bool {
	encOnce.Do(func() {
		encSet = map[string]bool{}
		out, _ := command(ff, "-hide_banner", "-encoders").Output()
		re := regexp.MustCompile(`^\s*[VAS][.A-Z]{5}\s+(\S+)`)
		for _, l := range strings.Split(string(out), "\n") {
			if m := re.FindStringSubmatch(l); m != nil {
				encSet[m[1]] = true
			}
		}
	})
	return encSet
}

func uniquePath(dir, base, ext string) string {
	p := filepath.Join(dir, base+"."+ext)
	for i := 1; isFile(p); i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s (%d).%s", base, i, ext))
	}
	return p
}

func actionConvert(msg map[string]any) {
	fail := func(m string) { send(map[string]any{"type": "error", "message": m}) }
	ff := findTool("ffmpeg")
	if ff == "" {
		fail("ffmpeg not found. Re-run the AnySave installer.")
		return
	}
	in := str(msg, "path")
	if !isFile(in) {
		fail("File not found")
		return
	}
	var spec convertSpec
	if b, err := json.Marshal(msg["spec"]); err == nil {
		json.Unmarshal(b, &spec)
	}
	if spec.Format == "" {
		spec.Format = "mp4"
	}
	enc := encoders(ff)
	total := probeDuration(in)
	var start, length float64
	if spec.StartSec != nil && *spec.StartSec > 0 {
		start = *spec.StartSec
	}
	if spec.EndSec != nil && *spec.EndSec > start {
		length = *spec.EndSec - start
	}
	outSecs := length
	if outSecs == 0 && total > 0 {
		outSecs = total - start
	}

	outDir := filepath.Join(downloadDir, "Converted")
	os.MkdirAll(outDir, 0o755)
	base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
	var tags []string
	if spec.Height != nil {
		tags = append(tags, fmt.Sprintf("%dp", *spec.Height))
	}
	if spec.TargetMb != nil {
		tags = append(tags, fmt.Sprintf("%dMB", *spec.TargetMb))
	}
	if length > 0 || start > 0 {
		tags = append(tags, "trim")
	}
	if len(tags) > 0 {
		base += " (" + strings.Join(tags, " ") + ")"
	}
	out := uniquePath(outDir, base, spec.Format)

	a := []string{"-hide_banner", "-y", "-nostats", "-progress", "pipe:1"}
	if start > 0 {
		a = append(a, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	a = append(a, "-i", in)
	if length > 0 {
		a = append(a, "-t", strconv.FormatFloat(length, 'f', 3, 64))
	}
	scale := ""
	if spec.Height != nil {
		scale = fmt.Sprintf("scale=-2:'min(%d,ih)'", *spec.Height)
	}
	h264 := []string{"-c:v", "mpeg4", "-q:v", "4"}
	if enc["libx264"] {
		h264 = []string{"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p"}
	} else if enc["h264_videotoolbox"] {
		h264 = []string{"-c:v", "h264_videotoolbox"}
	}
	aac := []string{"-c:a", "aac", "-b:a", "128k", "-ac", "2"}
	var vbit int64
	if spec.TargetMb != nil && outSecs > 0 {
		// 4% margin for container overhead, so the file lands under the target
		vbit = int64(float64(*spec.TargetMb)*0.96*8*1024*1024/outSecs) - 128000
		if vbit < 150000 {
			vbit = 150000
		}
	}
	quality := func(crf string) []string {
		if vbit > 0 {
			v := strconv.FormatInt(vbit, 10)
			return []string{"-b:v", v, "-maxrate", v, "-bufsize", strconv.FormatInt(vbit*2, 10)}
		}
		if enc["libx264"] {
			return []string{"-crf", crf}
		}
		return nil
	}
	switch spec.Format {
	case "mp4", "mov", "mkv":
		if scale != "" {
			a = append(a, "-vf", scale)
		}
		a = append(a, h264...)
		a = append(a, quality("23")...)
		if spec.RemoveAudio {
			a = append(a, "-an")
		} else {
			a = append(a, aac...)
		}
		if spec.Format != "mkv" {
			a = append(a, "-movflags", "+faststart")
		}
	case "webm":
		if scale != "" {
			a = append(a, "-vf", scale)
		}
		if enc["libvpx-vp9"] {
			a = append(a, "-c:v", "libvpx-vp9", "-row-mt", "1", "-deadline", "realtime", "-cpu-used", "8")
		} else {
			a = append(a, "-c:v", "libvpx")
		}
		if vbit > 0 {
			a = append(a, "-b:v", strconv.FormatInt(vbit, 10))
		} else {
			a = append(a, "-crf", "33", "-b:v", "0")
		}
		if spec.RemoveAudio {
			a = append(a, "-an")
		} else if enc["libopus"] {
			a = append(a, "-c:a", "libopus", "-b:a", "96k")
		} else {
			a = append(a, "-c:a", "libvorbis")
		}
	case "gif":
		h := 480
		if spec.Height != nil {
			h = *spec.Height
		}
		a = append(a, "-vf", fmt.Sprintf("fps=12,scale=-2:'min(%d,ih)':flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse", h), "-loop", "0", "-an")
	case "mp3":
		a = append(a, "-vn")
		if enc["libmp3lame"] {
			a = append(a, "-c:a", "libmp3lame", "-q:a", "2")
		}
	case "m4a":
		a = append(a, "-vn", "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	case "wav":
		a = append(a, "-vn", "-c:a", "pcm_s16le")
	case "opus":
		a = append(a, "-vn")
		if enc["libopus"] {
			a = append(a, "-c:a", "libopus", "-b:a", "128k")
		} else {
			a = append(a, "-c:a", "opus", "-strict", "-2")
		}
	default:
		fail("Unknown format: " + spec.Format)
		return
	}
	a = append(a, out)
	logf("convert: %q", a)

	cmd := command(ff, a...)
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		fail(err.Error())
		return
	}
	attachChildJob(cmd)
	var cancelled atomic.Bool
	go func() { // Chrome closes stdin on cancel
		for {
			if _, err := readMessage(); err != nil {
				break
			}
		}
		cancelled.Store(true)
		killTree(cmd)
	}()
	began := time.Now()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "out_time_us=") || strings.HasPrefix(line, "out_time_ms=") {
			us, err := strconv.ParseFloat(strings.TrimSpace(line[strings.IndexByte(line, '=')+1:]), 64)
			if err != nil || outSecs <= 0 {
				continue
			}
			pct := us / 1e6 / outSecs * 100
			if pct > 99 {
				pct = 99
			}
			eta := "?"
			if pct > 2 {
				eta = humanETA(strconv.FormatFloat(time.Since(began).Seconds()/pct*(100-pct), 'f', 0, 64))
			}
			send(map[string]any{"type": "progress", "percent": float64(int(pct*10)) / 10, "speed": "Converting", "eta": eta})
		}
	}
	err := cmd.Wait()
	switch {
	case cancelled.Load():
		os.Remove(out)
		fail("Cancelled")
	case err != nil:
		os.Remove(out)
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		fail("Couldn't convert: " + strings.TrimSpace(lines[len(lines)-1]))
	default:
		send(map[string]any{"type": "done", "file": out})
	}
}

// ---------- fast multi-connection file download (engine) ----------

var allowedFolders = map[string]bool{
	"Videos": true, "Music": true, "Pictures": true, "Documents": true,
	"Archives": true, "Apps": true, "Converted": true, "Other": true,
}

func actionFastDownload(msg map[string]any) {
	fail := func(m string) { send(map[string]any{"type": "error", "message": m}) }
	rawURL := str(msg, "url")
	if !webLink(rawURL) {
		fail("That isn't a web link.")
		return
	}

	outDir := downloadDir
	if folder := str(msg, "folder"); allowedFolders[folder] {
		outDir = filepath.Join(downloadDir, folder)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail(err.Error())
		return
	}

	key := jobKey(msg)
	handover, _ := msg["handover"].(bool)
	destDir := outDir
	if handover {
		// Stage in .anysave-parts/ready-<key> so Chrome can save into outDir via 127.0.0.1 handover without duplicate files.
		destDir = filepath.Join(outDir, ".anysave-parts", "ready-"+key)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var paused, cancelled atomic.Bool
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
				cancelled.Store(true)
				break
			}
		}
		cancel()
	}()

	var liveStream *engine.LiveStreamServer
	var liveServed atomic.Bool
	defer func() {
		if liveStream != nil {
			liveStream.Close()
		}
	}()

	res, err := engine.Download(ctx, engine.Options{
		URL:            rawURL,
		DestDir:        destDir,
		FileName:       str(msg, "fileName"),
		StateKey:       key,
		Cookies:        str(msg, "cookies"),
		Referrer:       str(msg, "referrer"),
		ExpectedSHA256: str(msg, "sha256"),
		OnStart: func(info engine.StartInfo) {
			if !handover {
				return
			}
			ls, lerr := engine.ServeLiveStream(info, 20*time.Second)
			if lerr != nil {
				return
			}
			liveStream = ls
			send(map[string]any{
				"type":       "handover",
				"url":        ls.URL,
				"fileName":   info.FileName,
				"totalBytes": info.TotalBytes,
				"live":       true,
			})
		},
		OnVerified: func() bool {
			if liveStream == nil {
				return false
			}
			ok := <-liveStream.Done
			liveStream.Close()
			liveStream = nil
			if ok {
				liveServed.Store(true)
			}
			return ok
		},
		OnProgress: func(p engine.Progress) {
			if paused.Load() || cancelled.Load() {
				return
			}
			eta := "?"
			if p.ETASeconds >= 0 {
				eta = humanETA(strconv.Itoa(p.ETASeconds))
			}
			send(map[string]any{
				"type":        "progress",
				"percent":     float64(int(p.Percent*10)) / 10,
				"doneBytes":   p.DoneBytes,
				"totalBytes":  p.TotalBytes,
				"speed":       humanSpeed(p.SpeedBPS),
				"eta":         eta,
				"activeConns": p.ActiveConns,
			})
		},
	})

	if err != nil {
		if paused.Load() {
			send(map[string]any{"type": "paused"})
			return
		}
		if cancelled.Load() {
			_ = os.RemoveAll(filepath.Join(destDir, ".anysave-parts", key))
			if handover {
				_ = os.RemoveAll(destDir)
			}
			fail("Cancelled")
			return
		}
		var fb *engine.ErrFallbackRecommended
		if errors.As(err, &fb) {
			logf("fastDownload fallback for %s: %v", rawURL, err)
			send(map[string]any{"type": "fallback", "reason": fb.Error()})
			return
		}
		logf("fastDownload error for %s: %v", rawURL, err)
		fail(friendlyError(err.Error()))
		return
	}

	if handover {
		if liveServed.Load() {
			_ = os.RemoveAll(destDir)
			send(map[string]any{
				"type":       "done",
				"file":       filepath.Join(outDir, res.FileName),
				"fileName":   res.FileName,
				"totalBytes": res.TotalBytes,
				"sha256":     res.SHA256,
				"multiConn":  res.MultiConn,
				"peakConns":  res.PeakConns,
				"handedOver": true,
			})
			return
		}
		h, herr := engine.ServeOneTimeFile(res.FilePath, res.FileName, 20*time.Second, true)
		if herr == nil {
			send(map[string]any{
				"type":       "handover",
				"url":        h.URL,
				"fileName":   res.FileName,
				"totalBytes": res.TotalBytes,
				"sha256":     res.SHA256,
				"multiConn":  res.MultiConn,
				"peakConns":  res.PeakConns,
			})
			served := <-h.Done
			h.Close()
			if served {
				_ = os.RemoveAll(destDir)
				send(map[string]any{
					"type":       "done",
					"file":       filepath.Join(outDir, res.FileName),
					"fileName":   res.FileName,
					"totalBytes": res.TotalBytes,
					"sha256":     res.SHA256,
					"multiConn":  res.MultiConn,
					"peakConns":  res.PeakConns,
					"handedOver": true,
				})
				return
			}
		}
		// Fallback if handover wasn't fetched by Chrome: move the staged file into outDir directly.
		finalPath := uniquePath(outDir, strings.TrimSuffix(res.FileName, filepath.Ext(res.FileName)), strings.TrimPrefix(filepath.Ext(res.FileName), "."))
		if filepath.Ext(res.FileName) == "" {
			finalPath = filepath.Join(outDir, res.FileName)
		}
		_ = os.Rename(res.FilePath, finalPath)
		_ = os.RemoveAll(destDir)
		res.FilePath = finalPath
	}

	send(map[string]any{
		"type":       "done",
		"file":       res.FilePath,
		"fileName":   filepath.Base(res.FilePath),
		"totalBytes": res.TotalBytes,
		"sha256":     res.SHA256,
		"multiConn":  res.MultiConn,
		"peakConns":  res.PeakConns,
	})
}
