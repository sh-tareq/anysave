// "Convert anything" for the desktop (Chrome extension and the Mac/Windows apps): the same tools as the
// Android app. Video, audio and pictures go through FFmpeg; PDFs through pdfcpu; documents, archives,
// subtitles and spreadsheets through small converters in cv_*.go. The manager page shows live previews
// through the preview server in cv_server.go.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cvSpec is what the user picked on the Convert page. Tool says which converter runs; Opts holds
// every other choice as text (the same names the Android app uses).
type cvSpec struct {
	Tool        string            `json:"tool"`
	Format      string            `json:"format"`
	Height      *int              `json:"height"`
	TargetMb    *int              `json:"targetMb"`
	StartSec    *float64          `json:"startSec"`
	EndSec      *float64          `json:"endSec"`
	RemoveAudio bool              `json:"removeAudio"`
	Opts        map[string]string `json:"opts"`
}

func (s cvSpec) opt(k string) string { return strings.TrimSpace(s.Opts[k]) }
func (s cvSpec) optF(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.ReplaceAll(s.opt(k), ",", "."), 64); err == nil {
		return v
	}
	return def
}
func (s cvSpec) optI(k string, def int) int {
	if v, err := strconv.Atoi(s.opt(k)); err == nil {
		return v
	}
	return def
}

// cvError carries a message that is already plain enough to show as it is.
type cvError string

func (e cvError) Error() string { return string(e) }

var errCancelled = cvError("Cancelled")

const (
	cvMedia       = "media"
	cvJoin        = "join"
	cvFrames      = "frames"
	cvAddSubs     = "addsubs"
	cvImage       = "image"
	cvImagesPdf   = "images-pdf"
	cvPdfImages   = "pdf-images"
	cvPdfMerge    = "pdf-merge"
	cvPdfPages    = "pdf-pages"
	cvPdfCompress = "pdf-compress"
	cvPdfRotate   = "pdf-rotate"
	cvPdfProtect  = "pdf-protect"
	cvPdfUnlock   = "pdf-unlock"
	cvDoc         = "doc"
	cvUnpack      = "unpack"
	cvPack        = "pack"
	cvSubs        = "subs"
	cvData        = "data"
)

var (
	cvVideoExt   = setOf("mp4", "mkv", "webm", "mov", "avi", "m4v", "3gp", "flv", "wmv", "ts", "mts", "m2ts", "mpg", "mpeg", "ogv", "vob")
	cvAudioExt   = setOf("mp3", "m4a", "aac", "wav", "flac", "ogg", "oga", "opus", "wma", "amr", "aiff", "aif", "alac", "mka", "caf")
	cvImageExt   = setOf("jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "hif", "avif", "bmp", "tif", "tiff", "ico", "jfif")
	cvDocExt     = setOf("docx", "odt", "epub", "md", "markdown", "txt", "html", "htm", "xhtml", "rtf")
	cvArchiveExt = setOf("zip", "7z", "tar", "gz", "tgz", "bz2", "tbz2", "xz", "txz", "zst", "tzst", "rar", "cbz", "cbr", "cb7", "iso", "001", "z01")
	cvSubsExt    = setOf("srt", "vtt", "ass", "ssa", "lrc")
	cvDataExt    = setOf("csv", "tsv", "json", "xlsx")
)

func setOf(s ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range s {
		m[x] = true
	}
	return m
}

func extOf(name string) string { return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) }

// cvKindOf says what kind of file something is, as far as the converter cares.
func cvKindOf(name string) string {
	e := extOf(name)
	switch {
	case cvSubsExt[e]:
		return "subs"
	case cvDataExt[e]:
		return "data"
	case e == "pdf":
		return "pdf"
	case cvImageExt[e]:
		return "image"
	case cvAudioExt[e]:
		return "audio"
	case cvVideoExt[e]:
		return "video"
	case cvDocExt[e]:
		return "doc"
	case cvArchiveExt[e]:
		return "archive"
	}
	return "other"
}

func baseNoExt(p string) string {
	b := filepath.Base(p)
	b = strings.TrimSuffix(b, filepath.Ext(b))
	if len([]rune(b)) > 100 {
		b = string([]rune(b)[:100])
	}
	return b
}

var unsafeName = regexp.MustCompile(`[\\/:*?"<>|\n\r\t]+`)

func safeName(s string) string {
	s = strings.Join(strings.Fields(unsafeName.ReplaceAllString(s, " ")), " ")
	if len([]rune(s)) > 100 {
		s = string([]rune(s)[:100])
	}
	if s == "" {
		return "AnySave"
	}
	return s
}

func uniqueDir(dir, base string) string {
	p := filepath.Join(dir, base)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)", base, i))
	}
}

// uniqueFile is uniquePath for names whose extension may have two parts (".tar.gz").
func uniqueFile(dir, base, ext string) string { return uniquePath(dir, base, ext) }

func andMore(paths []string) string {
	b := baseNoExt(paths[0])
	if len(paths) > 1 {
		b += fmt.Sprintf(" and %d more", len(paths)-1)
	}
	return b
}

// ---------- running a tool ----------

// cvRun runs spec on paths and puts the result (a file, or a folder when there are several) in outDir.
// progress gets 0–100. A cancelled ctx stops the work and removes what was half made.
func cvRun(ctx context.Context, paths []string, spec cvSpec, outDir string, progress func(float64)) (result string, err error) {
	return cvRunStaged(ctx, paths, spec, outDir, progress, nil)
}

// cvRunStaged is cvRun that also says what it's doing now ("Downloading…", "Removing noise"…).
func cvRunStaged(ctx context.Context, paths []string, spec cvSpec, outDir string, progress func(float64), stage func(string)) (result string, err error) {
	return cvRunFull(ctx, paths, spec, outDir, progress, stage, nil)
}

// cvRunFull is cvRunStaged that can also leave a short note about the result ("Unpacked 12 files…").
func cvRunFull(ctx context.Context, paths []string, spec cvSpec, outDir string, progress func(float64), stage func(string), note func(string)) (result string, err error) {
	if len(paths) == 0 && spec.Tool != cvSpeak && spec.Tool != cvTranslate {
		return "", cvError("Choose a file first.")
	}
	folders := spec.Tool == cvPack || spec.Tool == cvArcEdit // archives can hold folders
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || (st.IsDir() && !folders) {
			return "", cvError("The file " + filepath.Base(p) + " isn't there any more.")
		}
	}
	if spec.Opts == nil {
		spec.Opts = map[string]string{}
	}
	if spec.Tool == "" {
		spec.Tool = cvMedia
	}
	if progress == nil {
		progress = func(float64) {}
	}
	os.MkdirAll(outDir, 0o755)
	work, err := os.MkdirTemp("", "anysave-cv-")
	if err != nil {
		return "", cvError("Couldn't make a work folder: " + err.Error())
	}
	defer os.RemoveAll(work)

	j := &cvJob{ctx: ctx, spec: spec, work: work, progress: progress, stageFn: stage, noteFn: note}
	first := ""
	if len(paths) > 0 {
		first = paths[0]
	}
	base := baseNoExt(first)
	format := strings.ToLower(spec.Format)
	var made string // what to delete if it fails
	defer func() {
		if r := recover(); r != nil {
			err = cvError(fmt.Sprint(r))
		}
		if err != nil && made != "" {
			os.RemoveAll(made)
		}
		if err != nil && ctx.Err() != nil {
			err = errCancelled
		}
	}()
	file := func(name, ext string) string { made = uniqueFile(outDir, safeName(name), ext); return made }
	folder := func(name string) string {
		made = uniqueDir(outDir, safeName(name))
		os.MkdirAll(made, 0o755)
		return made
	}

	switch spec.Tool {
	case cvMedia:
		if format == "" {
			format = "mp4"
		}
		if len(paths) > 1 { // batch: every file the same way, into a folder
			dir := folder(andMore(paths) + " (" + strings.ToUpper(format) + ")")
			for i, p := range paths {
				sub := j.sub(float64(i)/float64(len(paths))*100, 100/float64(len(paths)))
				if err := sub.media(p, uniquePath(dir, baseNoExt(p), format), format); err != nil {
					return "", err
				}
			}
			return dir, nil
		}
		out := file(base+mediaTag(spec), format)
		return out, j.media(first, out, format)
	case cvJoin:
		audio := true
		for _, p := range paths {
			if probeMedia(p).HasVideo && cvKindOf(p) != "audio" {
				audio = false
			}
		}
		ext := "mp4"
		if audio {
			ext = "m4a"
		}
		out := file(andMore(paths), ext)
		return out, j.join(paths, out, audio)
	case cvFrames:
		if format != "png" {
			format = "jpg"
		}
		dir := folder(base + " pictures")
		return dir, j.frames(first, dir, spec.optF("every", 1), format)
	case cvAddSubs:
		var video, subs string
		for _, p := range paths {
			switch cvKindOf(p) {
			case "subs":
				subs = p
			default:
				video = p
			}
		}
		if video == "" || subs == "" {
			return "", cvError("Choose one video and one subtitle file.")
		}
		burn := spec.opt("mode") != "track"
		ext := "mp4"
		if !burn && extOf(video) == "mkv" {
			ext = "mkv"
		}
		out := file(baseNoExt(video)+" (subtitles)", ext)
		return out, j.addSubs(video, subs, out, burn)
	case cvCaptions:
		var video, ass string
		for _, p := range paths {
			if extOf(p) == "ass" {
				ass = p
			} else {
				video = p
			}
		}
		if video == "" || ass == "" {
			return "", cvError("Choose a video and make captions first.")
		}
		name := baseNoExt(video) + " (captions)"
		if _, ok := frameRatios[spec.opt("frame")]; ok {
			name = baseNoExt(video) + " (captions " + strings.ReplaceAll(spec.opt("frame"), ":", "x") + ")"
		}
		out := file(name, "mp4")
		return out, j.burnCaptions(video, ass, out)
	case cvSpeak:
		if format != "wav" && format != "m4a" {
			format = "mp3"
		}
		out := file(speakName(spec.Opts["text"]), format)
		return out, j.speakJob(out, format)
	case cvTranslate:
		name := strings.TrimSpace(spec.opt("name"))
		if name == "" {
			name = "Translated"
		}
		out := file(name+"."+spec.opt("to"), "srt")
		return out, j.translateJob(out)
	case cvDub:
		var video, srt string
		for _, p := range paths {
			if extOf(p) == "srt" {
				srt = p
			} else {
				video = p
			}
		}
		if video == "" || srt == "" {
			return "", cvError("Choose a video and translate its captions first.")
		}
		out := file(baseNoExt(video)+" ("+langName(spec.opt("lang"))+" dub)", dubOutput(video))
		return out, j.dubJob(video, srt, out)
	case cvVoiceClean:
		if len(paths) > 1 {
			dir := folder(andMore(paths) + " (clean voice)")
			for i, p := range paths {
				sub := j.sub(float64(i)/float64(len(paths))*100, 100/float64(len(paths)))
				f := cleanOutput(p, format)
				if err := sub.voiceCleanJob(p, uniquePath(dir, baseNoExt(p)+" (clean voice)", f), f); err != nil {
					return "", err
				}
			}
			return dir, nil
		}
		f := cleanOutput(first, format)
		out := file(base+" (clean voice)", f)
		return out, j.voiceCleanJob(first, out, f)
	case cvImage:
		if format == "" {
			format = "jpg"
		}
		ext := format
		if ext == "tiff" {
			ext = "tif"
		}
		if len(paths) == 1 {
			out := file(base, ext)
			return out, j.image(first, out, format)
		}
		dir := folder(andMore(paths))
		for i, p := range paths {
			if err := j.check(); err != nil {
				return "", err
			}
			if err := j.image(p, uniquePath(dir, baseNoExt(p), ext), format); err != nil {
				return "", err
			}
			progress(float64(i+1) * 100 / float64(len(paths)))
		}
		return dir, nil
	case cvImagesPdf:
		out := file(andMore(paths), "pdf")
		return out, j.imagesToPdf(paths, out)
	case cvPdfImages:
		// The page renders the pages with pdf.js and sends them (see cvSaveRendered); nothing to do here.
		return "", cvError("Pages to pictures runs in the page. Reload the Convert page and try again.")
	case cvPdfMerge:
		out := file(andMore(paths), "pdf")
		return out, j.pdfMerge(paths, out)
	case cvPdfPages:
		if spec.opt("mode") == "split" {
			dir := folder(base + " pages")
			return dir, j.pdfSplit(first, dir)
		}
		label := spec.opt("pages")
		if label == "" {
			label = "all"
		}
		out := file(base+" (pages "+label+")", "pdf")
		return out, j.pdfPick(first, out)
	case cvPdfCompress:
		out := file(base+" (smaller)", "pdf")
		return out, j.pdfCompress(first, out)
	case cvPdfRotate:
		out := file(base+" (rotated)", "pdf")
		return out, j.pdfRotate(first, out)
	case cvPdfProtect:
		out := file(base+" (locked)", "pdf")
		return out, j.pdfProtect(first, out)
	case cvPdfUnlock:
		out := file(base+" (unlocked)", "pdf")
		return out, j.pdfUnlock(first, out)
	case cvDoc:
		if format == "" {
			format = "pdf"
		}
		out := file(base, format)
		return out, j.doc(first, out, format)
	case cvUnpack:
		return j.unpackArc(first, outDir)
	case cvArcTest:
		return j.testArc(first)
	case cvArcRepair:
		return j.repairArc(first, outDir)
	case cvArcConvert:
		return j.convertArc(first, outDir)
	case cvArcEdit:
		j.spec.Opts["add"] = jsonString(paths[1:])
		return j.editArc(first)
	case cvArcSplit:
		return j.splitArc(first)
	case cvArcJoin:
		return j.joinArc(first, outDir)
	case cvPack:
		if format != "7z" && format != "tar.gz" {
			format = "zip"
		}
		if d := spec.opt("dest"); d != "" && d != "downloads" {
			outDir = j.arcDest(first, "")
			if d == "next" && isPreviewCopy(first) {
				outDir = filepath.Join(downloadDir, "Converted")
			}
			os.MkdirAll(outDir, 0o755)
		}
		name := spec.opt("name")
		if name == "" {
			name = andMore(paths)
			if len(paths) == 1 {
				name = base
			}
		}
		if format == "tar.gz" {
			out := file(name, format)
			return out, j.packTarGz(paths, out)
		}
		out := file(name, format)
		made = ""
		return j.packArc(paths, out) // arc removes its own parts if it fails
	case cvSubs:
		if format == "" {
			format = "srt"
		}
		out := file(base, format)
		return out, j.subs(first, out, format)
	case cvData:
		if format == "" {
			format = "xlsx"
		}
		out := file(base, format)
		return out, dataConvert(first, out)
	}
	return "", cvError("This kind of conversion isn't available in this version.")
}

func mediaTag(s cvSpec) string {
	var tags []string
	if s.Height != nil {
		tags = append(tags, fmt.Sprintf("%dp", *s.Height))
	}
	if s.TargetMb != nil && *s.TargetMb > 0 {
		tags = append(tags, fmt.Sprintf("%dMB", *s.TargetMb))
	}
	if s.StartSec != nil || s.EndSec != nil {
		tags = append(tags, "trim")
	}
	if v := s.opt("speed"); v != "" && v != "1" {
		tags = append(tags, v+"x")
	}
	if len(tags) == 0 {
		return ""
	}
	return " (" + strings.Join(tags, " ") + ")"
}

type cvJob struct {
	ctx      context.Context
	spec     cvSpec
	work     string
	progress func(float64)
	stageFn  func(string)
	noteFn   func(string)
}

// stage says what the job is doing now, for the progress line.
func (j *cvJob) stage(s string) {
	if j.stageFn != nil {
		j.stageFn(s)
	}
}

func (j *cvJob) check() error {
	if j.ctx.Err() != nil {
		return errCancelled
	}
	return nil
}

// sub is the same job reporting into [from, from+span] of the whole.
func (j *cvJob) sub(from, span float64) *cvJob {
	c := *j
	c.progress = func(p float64) { j.progress(from + p*span/100) }
	return &c
}

func (j *cvJob) tmp(name string) string { return filepath.Join(j.work, name) }

// ---------- FFmpeg ----------

func ffmpegPath() (string, error) {
	ff := findTool("ffmpeg")
	if ff == "" {
		return "", cvError("FFmpeg isn't installed. Run the AnySave installer again.")
	}
	return ff, nil
}

// runFF runs FFmpeg with progress (outSecs = expected output length, 0 = unknown).
func (j *cvJob) runFF(args []string, outSecs float64, format string) error {
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	a := append([]string{"-hide_banner", "-y", "-nostats", "-progress", "pipe:1"}, args...)
	logf("cv ffmpeg: %q", a)
	cmd := command(ff, a...)
	stdout, _ := cmd.StdoutPipe()
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return cvError("Couldn't start FFmpeg: " + err.Error())
	}
	attachChildJob(cmd)
	done := make(chan struct{})
	go func() {
		select {
		case <-j.ctx.Done():
			killTree(cmd)
		case <-done:
		}
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if outSecs > 0 && (strings.HasPrefix(line, "out_time_us=") || strings.HasPrefix(line, "out_time_ms=")) {
			if us, err := strconv.ParseFloat(line[strings.IndexByte(line, '=')+1:], 64); err == nil {
				j.progress(math.Min(99, math.Max(0, us/1e6/outSecs*100)))
			}
		}
	}
	err = cmd.Wait()
	close(done)
	if j.ctx.Err() != nil {
		return errCancelled
	}
	if err != nil {
		logf("cv ffmpeg failed: %s", stderr.String())
		return cvError(convertError(stderr.String(), format))
	}
	j.progress(100)
	return nil
}

// tailBuffer keeps the last 32 KB written to it (FFmpeg can print a lot).
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 32<<10 {
		t.b = t.b[len(t.b)-32<<10:]
	}
	return len(p), nil
}
func (t *tailBuffer) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

// mediaInfo is what "ffmpeg -i" says about a file.
type mediaInfo struct {
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Rotation int     `json:"rotation"`
	FPS      float64 `json:"fps"`
	HasVideo bool    `json:"hasVideo"`
	HasAudio bool    `json:"hasAudio"`
	VCodec   string  `json:"vcodec"`
	ACodec   string  `json:"acodec"`
	Still    bool    `json:"still"` // a picture (or cover art) rather than moving video
	TileW    int     `json:"-"`
	TileH    int     `json:"-"`
	Streams  int     `json:"streams"`
	Subs     int     `json:"subs"`
	Bitrate  int     `json:"bitrate"` // kb/s for the whole file
}

var (
	vStreamRe  = regexp.MustCompile(`Stream #\d+:\d+[^:]*: Video: (\w+)[^\n]*?, (\d{2,5})x(\d{2,5})`)
	aStreamRe  = regexp.MustCompile(`Stream #\d+:\d+[^:]*: Audio: (\w+)`)
	sStreamRe  = regexp.MustCompile(`Stream #\d+:\d+[^:]*: Subtitle:`)
	fpsRe      = regexp.MustCompile(`, ([\d.]+) (?:fps|tbr)`)
	rotRe      = regexp.MustCompile(`rotation of (-?[\d.]+) degrees`)
	tileGridRe = regexp.MustCompile(`Tile Grid:.*?(\d{2,5})x(\d{2,5})`)
	brRe       = regexp.MustCompile(`bitrate: (\d+) kb/s`)
)

func probeMedia(path string) mediaInfo {
	var mi mediaInfo
	ff := findTool("ffmpeg")
	if ff == "" {
		return mi
	}
	out, _ := command(ff, "-hide_banner", "-i", path).CombinedOutput()
	s := string(out)
	if m := durRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mn, _ := strconv.Atoi(m[2])
		sec, _ := strconv.ParseFloat(m[3], 64)
		mi.Duration = float64(h*3600+mn*60) + sec
	}
	for _, line := range strings.Split(s, "\n") {
		if m := vStreamRe.FindStringSubmatch(line); m != nil && !mi.HasVideo {
			still := strings.Contains(line, "attached pic") || m[1] == "mjpeg" || m[1] == "png" && !strings.Contains(line, "fps")
			if still && mi.HasVideo {
				continue
			}
			mi.VCodec = m[1]
			mi.Width, _ = strconv.Atoi(m[2])
			mi.Height, _ = strconv.Atoi(m[3])
			if f := fpsRe.FindStringSubmatch(line); f != nil {
				mi.FPS, _ = strconv.ParseFloat(f[1], 64)
			}
			mi.Still = still
			mi.HasVideo = !strings.Contains(line, "attached pic")
		}
		if m := aStreamRe.FindStringSubmatch(line); m != nil && !mi.HasAudio {
			mi.HasAudio = true
			mi.ACodec = m[1]
		}
		if sStreamRe.MatchString(line) {
			mi.Subs++
		}
		if strings.Contains(line, "Stream #") {
			mi.Streams++
		}
	}
	if m := rotRe.FindStringSubmatch(s); m != nil {
		r, _ := strconv.ParseFloat(m[1], 64)
		mi.Rotation = ((int(math.Round(r)) % 360) + 360) % 360
		if mi.Rotation == 90 || mi.Rotation == 270 {
			mi.Width, mi.Height = mi.Height, mi.Width
		}
	}
	if m := tileGridRe.FindStringSubmatch(s); m != nil {
		mi.TileW, _ = strconv.Atoi(m[1])
		mi.TileH, _ = strconv.Atoi(m[2])
		mi.Width, mi.Height = mi.TileW, mi.TileH
	}
	if m := brRe.FindStringSubmatch(s); m != nil {
		mi.Bitrate, _ = strconv.Atoi(m[1])
	}
	if mi.HasVideo && mi.Duration < 0.2 && !mi.HasAudio {
		mi.Still = true
	}
	return mi
}

// ---------- video / audio ----------

var cvAudioFormats = setOf("mp3", "m4a", "wav", "flac", "opus", "ogg")

func (j *cvJob) media(in, out, format string) error {
	s := j.spec
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	enc := encoders(ff)
	mi := probeMedia(in)
	var start, length float64
	if s.StartSec != nil && *s.StartSec > 0 {
		start = *s.StartSec
	}
	if s.EndSec != nil && *s.EndSec > start {
		length = *s.EndSec - start
	}
	speed := s.optF("speed", 1)
	if speed < 0.25 || speed > 4 {
		speed = 1
	}
	inSecs := length
	if inSecs == 0 && mi.Duration > 0 {
		inSecs = mi.Duration - start
	}
	outSecs := inSecs / speed
	audioOut := cvAudioFormats[format]
	if !audioOut && !mi.HasVideo && mi.HasAudio && format != "gif" {
		return cvError("This file has no picture, only sound. Choose an audio format such as MP3.")
	}
	removeAudio := s.RemoveAudio && !audioOut

	a := []string{}
	if start > 0 {
		a = append(a, "-ss", ff3(start))
	}
	a = append(a, "-i", in)
	if length > 0 {
		a = append(a, "-t", ff3(length))
	}
	vf := videoFilters(s, speed, s.Height)
	af := audioFilters(s, speed)

	var vbit int64
	if s.TargetMb != nil && *s.TargetMb > 0 && outSecs > 0 {
		audioBits := 128000.0
		if removeAudio {
			audioBits = 0
		}
		vbit = int64(float64(*s.TargetMb)*0.96*8*1024*1024/outSecs - audioBits)
		if vbit < 150000 {
			vbit = 150000
		}
	}
	hq := s.TargetMb != nil && *s.TargetMb == 0
	codec := s.opt("codec")
	if codec == "" {
		codec = "h264"
	}
	switch format {
	case "mp4", "mov", "mkv":
		a = append(a, "-map", "0:v:0?", "-map", "0:a:0?")
		if len(vf) > 0 {
			a = append(a, "-vf", strings.Join(vf, ","))
		}
		a = append(a, videoCodec(enc, codec, vbit, hq, format, s.opt("hw") == "1")...)
		if fps := s.opt("fps"); fps != "" {
			a = append(a, "-r", fps)
		}
		if removeAudio || !mi.HasAudio {
			a = append(a, "-an")
		} else {
			a = append(a, filterArgs("-af", af)...)
			a = append(a, "-c:a", "aac", "-b:a", "128k", "-ac", "2")
		}
		if format != "mkv" {
			a = append(a, "-movflags", "+faststart")
		}
	case "webm":
		a = append(a, "-map", "0:v:0?", "-map", "0:a:0?")
		if len(vf) > 0 {
			a = append(a, "-vf", strings.Join(vf, ","))
		}
		switch {
		case codec == "av1" && enc["libsvtav1"]:
			a = append(a, "-c:v", "libsvtav1", "-preset", "10")
		case enc["libvpx-vp9"]:
			a = append(a, "-c:v", "libvpx-vp9", "-row-mt", "1", "-deadline", "realtime", "-cpu-used", "8")
		case enc["libsvtav1"]:
			a = append(a, "-c:v", "libsvtav1", "-preset", "10")
		default:
			a = append(a, "-c:v", "libvpx")
		}
		if vbit > 0 {
			a = append(a, "-b:v", strconv.FormatInt(vbit, 10))
		} else if hq {
			a = append(a, "-crf", "28", "-b:v", "0")
		} else {
			a = append(a, "-crf", "33", "-b:v", "0")
		}
		if fps := s.opt("fps"); fps != "" {
			a = append(a, "-r", fps)
		}
		if removeAudio || !mi.HasAudio {
			a = append(a, "-an")
		} else {
			a = append(a, filterArgs("-af", af)...)
			if enc["libopus"] {
				a = append(a, "-c:a", "libopus", "-b:a", "96k")
			} else {
				a = append(a, "-c:a", "libvorbis")
			}
		}
	case "gif":
		h := 480
		if s.Height != nil {
			h = *s.Height
		}
		fps := s.optI("fps", 12)
		if fps > 25 {
			fps = 25
		}
		pre := append(videoFilters(s, speed, nil), fmt.Sprintf("fps=%d", fps), fmt.Sprintf("scale=-2:'min(%d,ih)':flags=lanczos", h))
		a = append(a, "-vf", strings.Join(pre, ",")+",split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse=dither=sierra2_4a", "-loop", "0", "-an")
	default: // audio
		if !mi.HasAudio {
			return cvError("This file has no sound to save.")
		}
		a = append(a, "-vn", "-map", "0:a:0")
		a = append(a, filterArgs("-af", af)...)
		kbps := s.opt("abitrate")
		switch format {
		case "mp3":
			if enc["libmp3lame"] {
				a = append(a, "-c:a", "libmp3lame")
				if kbps != "" {
					a = append(a, "-b:a", kbps+"k")
				} else {
					a = append(a, "-q:a", "2")
				}
			}
		case "m4a":
			a = append(a, "-c:a", "aac", "-b:a", orDefault(kbps, "192")+"k", "-ac", "2")
		case "wav":
			a = append(a, "-c:a", "pcm_s16le")
		case "flac":
			a = append(a, "-c:a", "flac")
			if !setOf("flac", "wav", "aiff", "aif", "alac")[extOf(in)] {
				a = append(a, "-sample_fmt", "s16")
			}
		case "opus":
			if enc["libopus"] {
				a = append(a, "-c:a", "libopus")
			} else {
				a = append(a, "-c:a", "opus", "-strict", "-2")
			}
			a = append(a, "-b:a", orDefault(kbps, "128")+"k")
		case "ogg":
			a = append(a, "-c:a", "libvorbis", "-b:a", orDefault(kbps, "160")+"k")
		default:
			return cvError("Unknown format: " + format)
		}
	}
	a = append(a, out)
	return j.runFF(a, outSecs, format)
}

func ff3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func filterArgs(flag string, f []string) []string {
	if len(f) == 0 {
		return nil
	}
	return []string{flag, strings.Join(f, ",")}
}

func videoFilters(s cvSpec, speed float64, height *int) []string {
	var f []string
	switch s.opt("aspect") { // crop to a shape, keeping the middle
	case "9:16":
		f = append(f, "crop='min(iw,ih*9/16)':'min(ih,iw*16/9)'")
	case "1:1":
		f = append(f, "crop='min(iw,ih)':'min(iw,ih)'")
	case "4:5":
		f = append(f, "crop='min(iw,ih*4/5)':'min(ih,iw*5/4)'")
	case "16:9":
		f = append(f, "crop='min(iw,ih*16/9)':'min(ih,iw*9/16)'")
	}
	switch s.opt("rotate") {
	case "90":
		f = append(f, "transpose=1")
	case "180":
		f = append(f, "transpose=1,transpose=1")
	case "270":
		f = append(f, "transpose=2")
	}
	switch s.opt("flip") {
	case "h":
		f = append(f, "hflip")
	case "v":
		f = append(f, "vflip")
	}
	if height != nil {
		f = append(f, fmt.Sprintf("scale=-2:'min(%d,ih)'", *height))
	}
	if s.opt("stabilize") == "1" {
		f = append(f, "deshake")
	}
	if speed != 1 {
		f = append(f, "setpts=PTS/"+strconv.FormatFloat(speed, 'f', -1, 64))
	}
	if len(f) > 0 {
		f = append(f, "format=yuv420p")
	}
	return f
}

func audioFilters(s cvSpec, speed float64) []string {
	var f []string
	if s.opt("denoise") == "1" {
		f = append(f, "highpass=f=80", "afftdn=nr=12:nf=-30")
	}
	if speed != 1 { // atempo takes 0.5–2 per step
		left := speed
		for left > 2 {
			f = append(f, "atempo=2.0")
			left /= 2
		}
		for left < 0.5 {
			f = append(f, "atempo=0.5")
			left /= 0.5
		}
		f = append(f, "atempo="+strconv.FormatFloat(left, 'f', 4, 64))
	}
	// loudnorm works at 192 kHz inside; bring it back to a normal rate.
	if s.opt("loudnorm") == "1" {
		f = append(f, "loudnorm=I=-16:TP=-1.5:LRA=11", "aresample=48000")
	}
	return f
}

func videoCodec(enc map[string]bool, codec string, bitrate int64, hq bool, container string, hw bool) []string {
	rate := func(crf string) []string {
		if bitrate > 0 {
			b := strconv.FormatInt(bitrate, 10)
			return []string{"-b:v", b, "-maxrate", b, "-bufsize", strconv.FormatInt(bitrate*2, 10)}
		}
		return []string{"-crf", crf}
	}
	tag := []string{}
	if container != "mkv" {
		tag = []string{"-tag:v", "hvc1"}
	}
	// The Mac's own video chip: much faster, files a little bigger at the same quality.
	if hw && codec != "av1" && bitrate == 0 { // a target size needs the exact software encoder
		name := "h264_videotoolbox"
		if codec == "h265" {
			name = "hevc_videotoolbox"
		}
		if enc[name] {
			a := []string{"-c:v", name, "-pix_fmt", "yuv420p"}
			if bitrate > 0 {
				a = append(a, "-b:v", strconv.FormatInt(bitrate, 10))
			} else if hq {
				a = append(a, "-q:v", "70")
			} else {
				a = append(a, "-q:v", "58")
			}
			if codec == "h265" {
				a = append(a, tag...)
			}
			return a
		}
	}
	switch {
	case codec == "h265" && enc["libx265"]:
		crf := "26"
		if hq {
			crf = "22"
		}
		a := append([]string{"-c:v", "libx265", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-x265-params", "log-level=error"}, rate(crf)...)
		return append(a, tag...)
	case codec == "av1" && enc["libsvtav1"]:
		a := []string{"-c:v", "libsvtav1", "-preset", "10", "-pix_fmt", "yuv420p"}
		if bitrate > 0 {
			return append(a, "-b:v", strconv.FormatInt(bitrate, 10))
		}
		if hq {
			return append(a, "-crf", "28")
		}
		return append(a, "-crf", "34")
	case enc["libx264"]:
		crf := "23"
		if hq {
			crf = "19"
		}
		return append([]string{"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p"}, rate(crf)...)
	case enc["h264_videotoolbox"]:
		return []string{"-c:v", "h264_videotoolbox", "-q:v", "60"}
	}
	return []string{"-c:v", "mpeg4", "-q:v", "4"}
}

// join puts several clips (or songs) one after another. Every clip is fitted into the first clip's frame.
func (j *cvJob) join(paths []string, out string, audioOnly bool) error {
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	var a []string
	var parts, labels strings.Builder
	total := 0.0
	infos := make([]mediaInfo, len(paths))
	for i, p := range paths {
		infos[i] = probeMedia(p)
		total += infos[i].Duration
		a = append(a, "-i", p)
	}
	if audioOnly {
		for i := range paths {
			fmt.Fprintf(&parts, "[%d:a]aformat=sample_rates=44100:channel_layouts=stereo[a%d];", i, i)
			fmt.Fprintf(&labels, "[a%d]", i)
		}
		fmt.Fprintf(&parts, "%sconcat=n=%d:v=0:a=1[outa]", labels.String(), len(paths))
		a = append(a, "-filter_complex", parts.String(), "-map", "[outa]", "-c:a", "aac", "-b:a", "192k")
	} else {
		w, h := infos[0].Width/2*2, infos[0].Height/2*2
		if w == 0 || h == 0 {
			w, h = 1280, 720
		}
		for i := range paths {
			fmt.Fprintf(&parts, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p[v%d];", i, w, h, w, h, i)
			if infos[i].HasAudio {
				fmt.Fprintf(&parts, "[%d:a]aformat=sample_rates=44100:channel_layouts=stereo[a%d];", i, i)
			} else {
				fmt.Fprintf(&parts, "anullsrc=r=44100:cl=stereo,atrim=duration=%.3f[a%d];", math.Max(0.1, infos[i].Duration), i)
			}
			fmt.Fprintf(&labels, "[v%d][a%d]", i, i)
		}
		fmt.Fprintf(&parts, "%sconcat=n=%d:v=1:a=1[outv][outa]", labels.String(), len(paths))
		a = append(a, "-filter_complex", parts.String(), "-map", "[outv]", "-map", "[outa]")
		a = append(a, videoCodec(encoders(ff), "h264", 0, false, "mp4", j.spec.opt("hw") == "1")...)
		a = append(a, "-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart")
	}
	a = append(a, out)
	return j.runFF(a, total, extOf(out))
}

// frames saves a picture every "every" seconds (0 = every frame).
func (j *cvJob) frames(in, dir string, every float64, format string) error {
	vf := "null"
	if every > 0 {
		vf = "fps=1/" + strconv.FormatFloat(every, 'f', -1, 64)
	}
	a := []string{"-i", in, "-vf", vf}
	if format == "jpg" {
		a = append(a, "-q:v", "2")
	}
	a = append(a, filepath.Join(dir, filepath.Base(dir)+" %04d."+format))
	if err := j.runFF(a, probeMedia(in).Duration, format); err != nil {
		return err
	}
	if ents, _ := os.ReadDir(dir); len(ents) == 0 {
		return cvError("No pictures came out of this video.")
	}
	return nil
}

// addSubs burns subtitles into the picture, or adds them as a track the player can turn on and off.
func (j *cvJob) addSubs(video, subs, out string, burn bool) error {
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	a := []string{"-i", video}
	if burn {
		// Copy to a plain name: the subtitles filter needs its path escaped, and simple is safest.
		plain := j.tmp("subs." + extOf(subs))
		if err := copyFile(subs, plain); err != nil {
			return cvError("Couldn't read the subtitle file.")
		}
		size := map[string]int{"small": 16, "large": 28}[j.spec.opt("size")]
		if size == 0 {
			size = 22
		}
		p := strings.NewReplacer(`\`, "/", ":", `\:`, "'", `\'`).Replace(filepath.ToSlash(plain))
		style := fmt.Sprintf("Fontsize=%d,Bold=1,Outline=2,Shadow=0,MarginV=24", size)
		a = append(a, "-vf", fmt.Sprintf("subtitles='%s':force_style='%s'", p, style))
		a = append(a, videoCodec(encoders(ff), "h264", 0, true, "mp4", j.spec.opt("hw") == "1")...)
		a = append(a, "-c:a", "copy", "-movflags", "+faststart")
	} else {
		a = append(a, "-i", subs, "-map", "0:v?", "-map", "0:a?", "-map", "1:0", "-c:v", "copy", "-c:a", "copy")
		if extOf(out) == "mkv" {
			a = append(a, "-c:s", "srt")
		} else {
			a = append(a, "-c:s", "mov_text", "-movflags", "+faststart")
		}
	}
	a = append(a, out)
	return j.runFF(a, probeMedia(video).Duration, extOf(out))
}

// subs converts a subtitle file, moved earlier/later by opts.shift seconds.
func (j *cvJob) subs(in, out, format string) error {
	if extOf(in) == format && j.spec.optF("shift", 0) == 0 {
		return cvError("The file is already " + strings.ToUpper(format) + ". Pick another format, or move the timing.")
	}
	var a []string
	if shift := j.spec.optF("shift", 0); shift != 0 {
		a = append(a, "-itsoffset", strconv.FormatFloat(shift, 'f', 3, 64))
	}
	a = append(a, "-i", in)
	if format == "lrc" {
		a = append(a, "-f", "lrc")
	}
	a = append(a, out)
	err := j.runFF(a, 0, format)
	if err != nil {
		return cvError("Couldn't read these subtitles. The file may be damaged or empty.")
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// ---------- the "convert" action (one job, streamed to Chrome or the app) ----------

func actionConvertAny(msg map[string]any) {
	var spec cvSpec
	if b, err := json.Marshal(msg["spec"]); err == nil {
		json.Unmarshal(b, &spec)
	}
	var paths []string
	if ps, ok := msg["paths"].([]any); ok {
		for _, p := range ps {
			if s, ok := p.(string); ok && s != "" {
				paths = append(paths, s)
			}
		}
	}
	if len(paths) == 0 && str(msg, "path") != "" {
		paths = []string{str(msg, "path")}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // Chrome closes stdin on cancel
		for {
			if _, err := readMessage(); err != nil {
				break
			}
		}
		cancel()
	}()
	began := time.Now()
	last := time.Time{}
	label := "Converting"
	var labelMu sync.Mutex
	stage := func(s string) { labelMu.Lock(); label = s; labelMu.Unlock(); last = time.Time{} }
	progress := func(p float64) {
		if time.Since(last) < 250*time.Millisecond && p < 100 {
			return
		}
		last = time.Now()
		eta := "?"
		if p > 2 && p < 100 {
			eta = humanETA(strconv.FormatFloat(time.Since(began).Seconds()/p*(100-p), 'f', 0, 64))
		}
		labelMu.Lock()
		l := label
		labelMu.Unlock()
		send(map[string]any{"type": "progress", "percent": math.Min(99, float64(int(p*10))/10), "speed": l, "eta": eta})
	}
	outDir := filepath.Join(downloadDir, "Converted")
	if spec.Tool == cvTranslate { // the page reads it back; "Save translated subtitles" makes the real file
		outDir = translateDir()
	}
	var notes []string
	res, err := cvRunFull(ctx, paths, spec, outDir, progress, stage, func(n string) { notes = append(notes, n) })
	if err != nil {
		var ce cvError
		msg := err.Error()
		if !errors.As(err, &ce) {
			logf("convert failed: %v", err)
			msg = "Couldn't convert: " + msg
		}
		send(map[string]any{"type": "error", "message": msg})
		return
	}
	resp := map[string]any{"type": "done", "file": res}
	if len(notes) > 0 {
		resp["note"] = strings.Join(notes, " ")
	}
	if st, err := os.Stat(res); err == nil && st.IsDir() {
		n := 0
		filepath.WalkDir(res, func(_ string, d os.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				n++
			}
			return nil
		})
		resp["folder"] = true
		resp["count"] = n
	}
	send(resp)
}

// sortedKeys is a small helper for stable output.
func sortedKeys(m map[string]bool) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
