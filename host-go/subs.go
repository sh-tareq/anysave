package main

// Subtitles into a video, after "Transcribe":
//
//	{"action":"subs","video":"/…/talk.mp4","srt":"/…/talk.bn.srt","lang":"bn","mode":"embed"|"burn"}
//
//	embed  the subtitles travel inside the file as a track the viewer can switch on or off.
//	       No re-encoding: takes seconds. Saved as "<name> (subtitles).<ext>".
//	burn   the text is drawn onto the picture, like captions in social media videos. Re-encodes
//	       the video. Saved as "<name> (captions).mp4".
//
// Bangla needs proper text shaping (joined letters); the FFmpeg AnySave installs has libass with
// HarfBuzz, and the font is chosen per language (Kohinoor Bangla on a Mac, Nirmala UI on Windows).

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ISO 639-2 codes for the subtitle track's language tag (players show the language name).
var lang3 = map[string]string{"en": "eng", "bn": "ben", "hi": "hin", "ur": "urd", "ar": "ara", "es": "spa", "fr": "fra",
	"de": "deu", "pt": "por", "ru": "rus", "tr": "tur", "id": "ind", "zh": "zho", "ja": "jpn", "ko": "kor"}

// captionFont picks a font that has the language's letters.
func captionFont(lang string) string {
	if runtime.GOOS == "windows" {
		switch lang {
		case "bn", "hi":
			return "Nirmala UI"
		case "ar", "ur":
			return "Segoe UI"
		case "zh":
			return "Microsoft YaHei"
		case "ja":
			return "Yu Gothic"
		case "ko":
			return "Malgun Gothic"
		}
		return "Arial"
	}
	switch lang {
	case "bn":
		return "Kohinoor Bangla"
	case "hi":
		return "Kohinoor Devanagari"
	case "ar", "ur":
		return "Geeza Pro"
	case "zh":
		return "PingFang SC"
	case "ja":
		return "Hiragino Sans"
	case "ko":
		return "Apple SD Gothic Neo"
	}
	return "Helvetica Neue"
}

// freeName returns path, or "path (2)" style names when the file already exists.
func freeName(dir, base, ext string) string {
	p := filepath.Join(dir, base+ext)
	for i := 2; isFile(p); i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s %d%s", base, i, ext))
	}
	return p
}

func actionSubs(msg map[string]any) {
	fail := func(m string) { send(map[string]any{"type": "error", "message": m}) }
	video, srt := str(msg, "video"), str(msg, "srt")
	if !isFile(video) {
		fail("The video isn't there any more. It may have been moved or deleted.")
		return
	}
	if !isFile(srt) {
		fail("The subtitle file isn't there any more. Transcribe the video again.")
		return
	}
	ff := findTool("ffmpeg")
	if ff == "" {
		fail("ffmpeg not found. Re-run the AnySave installer.")
		return
	}
	lang := str(msg, "lang")
	mode := str(msg, "mode")
	if mode != "burn" {
		mode = "embed"
	}

	// libass and the -i of a subtitle file both dislike unusual characters in paths, so work on
	// copies with plain names in a temporary folder.
	work, err := os.MkdirTemp("", "anysave-subs-")
	if err != nil {
		fail(err.Error())
		return
	}
	defer os.RemoveAll(work)
	b, err := os.ReadFile(srt)
	if err != nil {
		fail("Couldn't read the subtitles.")
		return
	}
	if err := os.WriteFile(filepath.Join(work, "subs.srt"), b, 0o600); err != nil {
		fail(err.Error())
		return
	}

	dir := filepath.Dir(video)
	ext := strings.ToLower(filepath.Ext(video))
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	var args []string
	var out string
	if mode == "embed" {
		codec := "mov_text" // MP4, M4V, MOV
		switch ext {
		case ".mkv":
			codec = "srt"
		case ".webm":
			codec = "webvtt"
		case ".mp4", ".m4v", ".mov":
		default:
			ext = ".mkv" // anything else: MKV carries every kind of track
			codec = "srt"
		}
		out = freeName(dir, base+" (subtitles)", ext)
		args = []string{"-hide_banner", "-nostdin", "-y", "-i", video, "-i", filepath.Join(work, "subs.srt"),
			"-map", "0", "-map", "1:0", "-c", "copy", "-c:s", codec, "-disposition:s:0", "default"}
		if l := lang3[lang]; l != "" {
			args = append(args, "-metadata:s:s:0", "language="+l)
		}
	} else {
		w, h := videoSize(ff, video)
		size := 22
		if h > w && w > 0 {
			size = 13 // portrait video (reels, shorts): smaller text so lines fit the width
		}
		style := fmt.Sprintf("FontName=%s,FontSize=%d,Outline=2,Shadow=0,MarginV=%d,BorderStyle=1", captionFont(lang), size, 30)
		filter := "subtitles=subs.srt:force_style='" + style + "'"
		if runtime.GOOS == "darwin" {
			filter = "subtitles=subs.srt:fontsdir=/System/Library/Fonts:force_style='" + style + "'"
		}
		out = freeName(dir, base+" (captions)", ".mp4")
		enc := encoders(ff)
		args = []string{"-hide_banner", "-nostdin", "-y", "-i", video, "-vf", filter}
		if enc["libx264"] {
			args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p")
		}
		args = append(args, "-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart", "-sn")
	}
	args = append(args, "-progress", "pipe:1", "-nostats", out)
	logf("subs: %q", args)

	total := probeDuration(video)
	cmd := command(ff, args...)
	cmd.Dir = work
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
	label := "Adding subtitles"
	if mode == "burn" {
		label = "Drawing captions"
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "out_time_us=") || total <= 0 {
			continue
		}
		us, err := strconv.ParseFloat(strings.TrimSpace(line[len("out_time_us="):]), 64)
		if err != nil {
			continue
		}
		pct := us / 1e6 / total * 100
		if pct > 99 {
			pct = 99
		}
		eta := "?"
		if pct > 2 {
			eta = humanETA(strconv.FormatFloat(time.Since(began).Seconds()/pct*(100-pct), 'f', 0, 64))
		}
		send(map[string]any{"type": "progress", "percent": float64(int(pct*10)) / 10, "speed": label, "eta": eta})
	}
	err = cmd.Wait()
	switch {
	case cancelled.Load():
		os.Remove(out)
		fail("Cancelled")
	case err != nil:
		os.Remove(out)
		logf("subs failed: %s", stderr.String())
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		fail("Couldn't add the subtitles: " + strings.TrimSpace(lines[len(lines)-1]))
	default:
		send(map[string]any{"type": "done", "file": out})
	}
}

// videoSize returns the first video stream's width and height (0, 0 if unknown).
func videoSize(ff, path string) (int, int) {
	out, _ := command(ff, "-hide_banner", "-i", path).CombinedOutput()
	m := videoSizeRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return w, h
}

// The size in a line like "Stream #0:0: Video: h264 (High), yuv420p, 1280x720 [SAR 1:1 DAR 16:9], …"
var videoSizeRe = regexp.MustCompile(`Stream #\d+:\d+.*?: Video: .*?, (\d{2,5})x(\d{2,5})`)
