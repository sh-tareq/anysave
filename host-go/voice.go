package main

// Voice Lab: clean up a voice recording and read text aloud, all on this computer.
//
// The engine is sherpa-onnx (Apache-2.0, k2-fsa/sherpa-onnx), run as its two small command-line
// tools. Neither the engine nor the voices are in the installer: each "pack" is downloaded the
// first time it's needed, checked against a pinned SHA-256, and kept in the models folder.
//
//	engine        sherpa-onnx-offline-tts + sherpa-onnx-offline-denoiser + ONNX Runtime   ~20 MB
//	clean-strong  DPDFNet-2 48 kHz (Ceva, Apache-2.0)                                     ~11 MB
//	clean-light   GTCRN (MIT)                                                              0.5 MB
//	kokoro        Kokoro-82M v1.0 int8, 53 voices in 8 languages (Apache-2.0)            132 MB
//	bangla        Piper bn_BD "google" medium, 16 voices (data: OpenSLR 37 CC-BY-SA 4.0
//	              and CMU Indic), plus espeak-ng-data (GPL-3)                             ~84 MB
//
// What real tests showed (15 November 2026, M4 Pro): Kokoro reads English, Spanish, French, Hindi,
// Italian, Brazilian Portuguese and Chinese well (whisper heard them back almost word for word) at
// about twice real time. Its Japanese came back as nonsense, so it isn't offered. The Bangla voice is
// fast but only fair: whisper caught part of the words. DPDFNet removed nearly all of a hiss-and-hum
// test noise without making the voice quieter; GTCRN removed most of it; FFmpeg's afftdn about half.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	cvVoiceClean = "voiceclean"
	cvSpeak      = "speak"
	sherpaVer    = "1.13.8"
)

type voiceFile struct {
	URL    string
	SHA256 string
	Size   int64
	Name   string                    // saved as this inside the pack folder (single files)
	Keep   func(entry string) string // archives (.tar.bz2, .tar.gz, .zip): where an entry goes, "" = skip it
}

type voicePack struct {
	ID    string
	Label string
	Files []voiceFile
	After func(dir string) error // once everything is downloaded (inside the new folder)
}

func (p voicePack) size() int64 {
	var n int64
	for _, f := range p.Files {
		n += f.Size
	}
	return n
}

const (
	sherpaRel = "https://github.com/k2-fsa/sherpa-onnx/releases/download/"
	piperRev  = "https://huggingface.co/rhasspy/piper-voices/resolve/6b9cfaacb4fb2e02a9621227e3c1a48bec9710c3/bn/bn_BD/google/medium/"
)

// stripTop drops the archive's top folder ("kokoro-int8-multi-lang-v1_0/x" -> "x").
func stripTop(e string) string {
	if i := strings.IndexByte(e, '/'); i >= 0 {
		return e[i+1:]
	}
	return ""
}

func enginePack() voicePack {
	var f voiceFile
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		f = voiceFile{URL: sherpaRel + "v1.13.8/sherpa-onnx-v1.13.8-osx-arm64-shared.tar.bz2",
			SHA256: "b10e5c7e2c30ea03de9c442655d14860d9edc475c6251d58a8f5f06e913a1d56", Size: 20314448}
	case "darwin/amd64":
		f = voiceFile{URL: sherpaRel + "v1.13.8/sherpa-onnx-v1.13.8-osx-x64-shared.tar.bz2",
			SHA256: "54aad64acee9d2d596535a6080d6f22602a720af5460e1d50461b1e1b06bee40", Size: 22846038}
	case "windows/amd64":
		// The MT build has the C++ runtime inside, so it doesn't need the Visual C++ redistributable.
		f = voiceFile{URL: sherpaRel + "v1.13.8/sherpa-onnx-v1.13.8-win-x64-shared-MT-Release.tar.bz2",
			SHA256: "6dffdc715a4465b989446a6105265d2cb345e7101591a17d35534b6758f6e8df", Size: 24805859}
	default:
		return voicePack{ID: "engine", Label: "voice engine"}
	}
	f.Keep = func(e string) string {
		base := filepath.Base(e)
		switch {
		case base == exe("sherpa-onnx-offline-tts") || base == exe("sherpa-onnx-offline-denoiser"):
			return "bin/" + base
		case strings.Contains(e, "/bin/") && strings.HasSuffix(base, ".dll"): // Windows: DLLs beside the .exe
			return "bin/" + base
		case strings.Contains(e, "/lib/") && strings.HasPrefix(base, "libonnxruntime") && strings.HasSuffix(base, ".dylib"):
			return "lib/" + base // the tools look in @loader_path/../lib
		}
		return ""
	}
	return voicePack{ID: "engine", Label: "voice engine", Files: []voiceFile{f}}
}

var voicePacks = map[string]voicePack{
	"clean-strong": {ID: "clean-strong", Label: "strong clean-up", Files: []voiceFile{{
		URL: sherpaRel + "speech-enhancement-models/dpdfnet2_48khz_hr.onnx", Name: "dpdfnet2_48khz_hr.onnx",
		SHA256: "0b399f8a58dc4d70d8cd97541f5c39869406145193b957d00a03b66070944928", Size: 10596848}}},
	"clean-light": {ID: "clean-light", Label: "light clean-up", Files: []voiceFile{{
		URL: sherpaRel + "speech-enhancement-models/gtcrn_simple.onnx", Name: "gtcrn_simple.onnx",
		SHA256: "e77603ac0c23dac3227dd2d7135b3a585cbee2679048aecfa886657d3ae1b534", Size: 535638}}},
	"kokoro": {ID: "kokoro", Label: "voices for English and 6 more languages", Files: []voiceFile{{
		URL:    sherpaRel + "tts-models/kokoro-int8-multi-lang-v1_0.tar.bz2",
		SHA256: "72c347230107ef8a31098ec4d468a605244beef5f1ed00db97efa13518800653", Size: 132353410, Keep: stripTop}}},
	"bangla": {ID: "bangla", Label: "Bangla voices", Files: []voiceFile{
		{URL: piperRev + "bn_BD-google-medium.onnx", Name: "bn.onnx",
			SHA256: "f2e7518ed5534a755024a48c71b80bf617efaf12570bbdf3ce255a9526a8afd3", Size: 76782515},
		{URL: piperRev + "bn_BD-google-medium.onnx.json", Name: "bn.onnx.json",
			SHA256: "bc7e5e39e2a874bdad186620576ce18089b5a06c5645e258bcea3d56fdb11c0a", Size: 5494},
		{URL: sherpaRel + "tts-models/espeak-ng-data.tar.bz2", Keep: func(e string) string { return e },
			SHA256: "4135ccf82e1f40613491c0874d4945ae9e9c7840933d8e25a6f9e003d9ebf533", Size: 7252012},
	}, After: preparePiper},
}

func packByID(id string) (voicePack, bool) {
	if id == "engine" {
		p := enginePack()
		return p, len(p.Files) > 0
	}
	p, ok := voicePacks[id]
	return p, ok
}

func voiceRoot() string { return filepath.Join(modelsDir(), "voice") }

func packDir(id string) string {
	if id == "engine" {
		return filepath.Join(voiceRoot(), "engine-"+sherpaVer)
	}
	return filepath.Join(voiceRoot(), id)
}

func packReady(id string) bool { return isFile(filepath.Join(packDir(id), ".ready")) }

// ---------- downloading ----------

// voiceGetting is what this process is downloading now (pack -> bytes so far), for the page.
var (
	voiceMu      sync.Mutex
	voiceGetting = map[string]*voiceProgress{}
)

type voiceProgress struct {
	Done, Total int64
	Err         string
	wait        chan struct{}
}

// ensurePack returns the pack's folder, downloading it first if needed. Two callers asking for the
// same pack share one download.
func ensurePack(ctx context.Context, id string, progress func(done, total int64)) (string, error) {
	if packReady(id) {
		return packDir(id), nil
	}
	p, ok := packByID(id)
	if !ok {
		return "", cvError("Voice Lab doesn't work on this kind of computer yet.")
	}
	voiceMu.Lock()
	if g := voiceGetting[id]; g != nil && g.Err == "" {
		voiceMu.Unlock()
		for {
			select {
			case <-g.wait:
				if packReady(id) {
					return packDir(id), nil
				}
				return "", cvError(g.Err)
			case <-ctx.Done():
				return "", errCancelled
			case <-time.After(300 * time.Millisecond):
				voiceMu.Lock()
				d, t := g.Done, g.Total
				voiceMu.Unlock()
				progress(d, t)
			}
		}
	}
	g := &voiceProgress{Total: p.size(), wait: make(chan struct{})}
	voiceGetting[id] = g
	voiceMu.Unlock()
	err := installPack(ctx, p, func(d int64) {
		voiceMu.Lock()
		g.Done = d
		voiceMu.Unlock()
		progress(d, g.Total)
	})
	voiceMu.Lock()
	if err != nil {
		g.Err = errMsg(err)
	} else {
		delete(voiceGetting, id)
	}
	close(g.wait)
	voiceMu.Unlock()
	if err != nil {
		return "", err
	}
	return packDir(id), nil
}

func installPack(ctx context.Context, p voicePack, progress func(done int64)) error {
	final := packDir(p.ID)
	tmp := fmt.Sprintf("%s.part-%d", final, os.Getpid())
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return cvError("Couldn't make the voice folder: " + err.Error())
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 30 * time.Second, ResponseHeaderTimeout: 60 * time.Second, ForceAttemptHTTP2: true}}
	var before int64
	for _, f := range p.Files {
		dl := filepath.Join(tmp, ".download")
		// Download servers sometimes drop a connection; try each file up to three times.
		var err error
		for try := 1; try <= 3; try++ {
			if err = fetchChecked(ctx, client, f, dl, func(d int64) { progress(before + d) }); err == nil || ctx.Err() != nil {
				break
			}
			logf("voice: %s, try %d: %v", f.URL, try, err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(try) * 2 * time.Second):
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return errCancelled
			}
			return err
		}
		before += f.Size
		if f.Keep != nil {
			err := unpack(dl, f.URL, tmp, f.Keep)
			os.Remove(dl)
			if err != nil {
				return cvError("Couldn't unpack the " + p.Label + ": " + err.Error())
			}
		} else if err := os.Rename(dl, filepath.Join(tmp, f.Name)); err != nil {
			return err
		}
	}
	if p.After != nil {
		if err := p.After(tmp); err != nil {
			return cvError("Couldn't set up the " + p.Label + ": " + err.Error())
		}
	}
	os.WriteFile(filepath.Join(tmp, ".ready"), []byte(time.Now().Format(time.RFC3339)), 0o644)
	if packReady(p.ID) { // another AnySave finished first
		return nil
	}
	os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		return cvError("Couldn't save the " + p.Label + ": " + err.Error())
	}
	ok = true
	return nil
}

// fetchChecked downloads f.URL to dest and checks its size and SHA-256.
func fetchChecked(parent context.Context, client *http.Client, f voiceFile, dest string, progress func(int64)) error {
	// A download that stops sending for a minute is given up, rather than waiting forever.
	ctx, stop := context.WithCancel(parent)
	defer stop()
	const stallLimit = 60 * time.Second
	watchdog := time.AfterFunc(stallLimit, stop)
	defer watchdog.Stop()
	stalled := func() bool { return ctx.Err() != nil && parent.Err() == nil }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", fileUA)
	resp, err := client.Do(req)
	if err != nil {
		if stalled() {
			return cvError("The download got stuck. Check the internet connection and try again.")
		}
		if parent.Err() != nil {
			return errCancelled
		}
		logf("voice: download %s: %v", f.URL, err)
		return cvError("Couldn't download the files this needs. Check the internet connection and try again.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cvError(fmt.Sprintf("Couldn't download the files this needs (the server said %d). Try again later.", resp.StatusCode))
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	var done int64
	buf := make([]byte, 256<<10)
	last := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallLimit)
			if _, werr := out.Write(buf[:n]); werr != nil {
				if strings.Contains(strings.ToLower(werr.Error()), "no space") {
					return cvError("The disk is full. Free some space and try again.")
				}
				return werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				progress(done)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if stalled() {
				return cvError("The download got stuck. Check the internet connection and try again.")
			}
			if parent.Err() != nil {
				return errCancelled
			}
			return cvError("The download stopped halfway. Try again.")
		}
	}
	progress(done)
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		logf("voice: checksum mismatch for %s (%d bytes)", f.URL, done)
		return cvError("The downloaded files were damaged or changed, so AnySave didn't use them. Try again.")
	}
	return nil
}

// untarBz2 unpacks the entries keep() wants into dir. Nothing can land outside dir.
// unpack unpacks the entries keep() wants from a .tar.bz2, .tar.gz or .zip into dir. Nothing can land
// outside dir; a symbolic link is kept only when it points at a file beside it (libfoo.dylib -> libfoo.1.dylib).
func unpack(path, url, dir string, keep func(string) string) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	if strings.HasSuffix(url, ".zip") {
		st, err := fh.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(fh, st.Size())
		if err != nil {
			return err
		}
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			err = writePackFile(dir, keep(zf.Name), zf.Mode(), rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	var r io.Reader = bzip2.NewReader(fh)
	if strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz") {
		gz, err := gzip.NewReader(fh)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel := keep(strings.TrimPrefix(hd.Name, "./"))
		switch hd.Typeflag {
		case tar.TypeReg:
			if err := writePackFile(dir, rel, os.FileMode(hd.Mode), tr); err != nil {
				return err
			}
		case tar.TypeDir:
			if rel != "" && within(filepath.Join(dir, filepath.FromSlash(rel)), dir) {
				os.MkdirAll(filepath.Join(dir, filepath.FromSlash(rel)), 0o755)
			}
		case tar.TypeSymlink:
			if rel == "" || strings.ContainsAny(hd.Linkname, `/\`) || runtime.GOOS == "windows" {
				continue
			}
			dst := filepath.Join(dir, filepath.FromSlash(rel))
			if !within(dst, dir) {
				continue
			}
			os.MkdirAll(filepath.Dir(dst), 0o755)
			os.Remove(dst)
			if err := os.Symlink(hd.Linkname, dst); err != nil {
				return err
			}
		}
	}
}

func writePackFile(dir, rel string, mode os.FileMode, r io.Reader) error {
	if rel == "" {
		return nil
	}
	dst := filepath.Join(dir, filepath.FromSlash(rel))
	if !within(dst, dir) {
		return nil
	}
	os.MkdirAll(filepath.Dir(dst), 0o755)
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// preparePiper turns a Piper voice into what sherpa-onnx reads: the settings sherpa-onnx wants are
// added to the model file as ONNX metadata, and the phoneme table becomes tokens.txt.
func preparePiper(dir string) error {
	var cfg struct {
		Audio struct {
			SampleRate int `json:"sample_rate"`
		} `json:"audio"`
		Espeak struct {
			Voice string `json:"voice"`
		} `json:"espeak"`
		Language struct {
			NameEnglish string `json:"name_english"`
		} `json:"language"`
		NumSpeakers  int              `json:"num_speakers"`
		PhonemeIDMap map[string][]int `json:"phoneme_id_map"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "bn.onnx.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	type tok struct {
		s  string
		id int
	}
	var toks []tok
	for s, ids := range cfg.PhonemeIDMap {
		// sherpa-onnx reads one symbol per line; Piper's few multi-letter entries are never produced by espeak.
		if len([]rune(s)) == 1 && len(ids) > 0 {
			toks = append(toks, tok{s, ids[0]})
		}
	}
	sort.Slice(toks, func(a, b int) bool { return toks[a].id < toks[b].id })
	var tb strings.Builder
	for _, t := range toks {
		fmt.Fprintf(&tb, "%s %d\n", t.s, t.id)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte(tb.String()), 0o644); err != nil {
		return err
	}
	meta := [][2]string{{"model_type", "vits"}, {"comment", "piper"}, {"language", cfg.Language.NameEnglish},
		{"voice", cfg.Espeak.Voice}, {"has_espeak", "1"}, {"n_speakers", strconv.Itoa(cfg.NumSpeakers)},
		{"sample_rate", strconv.Itoa(cfg.Audio.SampleRate)}}
	// A protobuf message may be extended by appending fields: ModelProto.metadata_props is field 14,
	// each a StringStringEntryProto {key = 1, value = 2}.
	pb := func(field int, v []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(field<<3|2))
		out = binary.AppendUvarint(out, uint64(len(v)))
		return append(out, v...)
	}
	var extra []byte
	for _, kv := range meta {
		extra = append(extra, pb(14, append(pb(1, []byte(kv[0])), pb(2, []byte(kv[1]))...))...)
	}
	f, err := os.OpenFile(filepath.Join(dir, "bn.onnx"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(extra)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ---------- the voices ----------

type ttsVoice struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Lang   string `json:"lang"`   // shown as a group: "English (US)", "Bangla"...
	Gender string `json:"gender"` // "woman", "man" or ""
	Pack   string `json:"pack"`
	Code   string `json:"code"` // language code: en, es, bn…
	sid    int
	code   string // kokoro language
	ref    string // copied voices: the sample
}

var kokoroNames = []string{"af_alloy", "af_aoede", "af_bella", "af_heart", "af_jessica", "af_kore", "af_nicole",
	"af_nova", "af_river", "af_sarah", "af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael",
	"am_onyx", "am_puck", "am_santa", "bf_alice", "bf_emma", "bf_isabella", "bf_lily", "bm_daniel", "bm_fable",
	"bm_george", "bm_lewis", "ef_dora", "em_alex", "ff_siwis", "hf_alpha", "hf_beta", "hm_omega", "hm_psi", "if_sara",
	"im_nicola", "jf_alpha", "jf_gongitsune", "jf_nezumi", "jf_tebukuro", "jm_kumo", "pf_dora", "pm_alex", "pm_santa",
	"zf_xiaobei", "zf_xiaoni", "zf_xiaoxiao", "zf_xiaoyi", "zm_yunjian", "zm_yunxi", "zm_yunxia", "zm_yunyang", "em_santa"}

var kokoroLangs = map[byte][2]string{ // first letter -> label, code
	'a': {"English (US)", "en-us"}, 'b': {"English (UK)", "en-gb"}, 'e': {"Spanish", "es"}, 'f': {"French", "fr"},
	'h': {"Hindi", "hi"}, 'i': {"Italian", "it"}, 'p': {"Portuguese (Brazil)", "pt-br"}, 'z': {"Chinese", "zh"},
}

// Bangla speakers worth offering, in order. In tests 0 and 4 were the clearest.
var banglaSpeakers = []int{0, 4, 15, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}

func ttsVoices() []ttsVoice {
	var vs []ttsVoice
	for sid, n := range kokoroNames {
		l, ok := kokoroLangs[n[0]]
		if !ok {
			continue // Japanese: not good enough yet
		}
		g := "woman"
		if n[1] == 'm' {
			g = "man"
		}
		name := n[3:]
		name = strings.ToUpper(name[:1]) + name[1:]
		vs = append(vs, ttsVoice{ID: "k" + strconv.Itoa(sid), Name: name, Lang: l[0], Gender: g, Pack: "kokoro", Code: l[1][:2], sid: sid, code: l[1]})
	}
	for i, sid := range banglaSpeakers {
		vs = append(vs, ttsVoice{ID: "bn" + strconv.Itoa(sid), Name: "Voice " + strconv.Itoa(i+1), Lang: "Bangla", Pack: "bangla", Code: "bn", sid: sid})
	}
	return append(vs, myVoices()...)
}

func voiceByID(id string) (ttsVoice, bool) {
	for _, v := range ttsVoices() {
		if v.ID == id {
			return v, true
		}
	}
	return ttsVoice{}, false
}

// voiceState is what the Voice Lab page shows: which packs are here and the voices.
func voiceState() map[string]any {
	packs := map[string]any{}
	ids := []string{"engine", "clean-strong", "clean-light", "kokoro", "bangla", "pocket", "llama", "mt-large", "mt-small"}
	voiceMu.Lock()
	defer voiceMu.Unlock()
	for _, id := range ids {
		p, ok := packByID(id)
		e := map[string]any{"ready": packReady(id), "size": p.size(), "label": p.Label, "works": ok}
		if g := voiceGetting[id]; g != nil {
			if g.Err != "" {
				e["error"] = g.Err
			} else {
				e["done"] = g.Done
			}
		}
		packs[id] = e
	}
	return map[string]any{"ok": true, "packs": packs, "voices": ttsVoices(), "langs": lmtLangs}
}

// ---------- reading aloud ----------

func sherpaTool(name string) (string, error) {
	p := filepath.Join(packDir("engine"), "bin", exe(name))
	if !isFile(p) {
		return "", cvError("The voice engine is missing. Try again to download it.")
	}
	return p, nil
}

// splitText cuts text into pieces of about max characters, at paragraph and sentence ends.
// pause says how long a gap follows each piece.
type textPiece struct {
	Text  string
	Pause float64
}

var sentenceEnd = regexp.MustCompile(`([.!?;:।॥。！？]+["'”’)\]]*)\s+|([。！？।])`)

func splitText(text string, max int) []textPiece {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out []textPiece
	for _, para := range regexp.MustCompile(`\n\s*\n+`).Split(text, -1) {
		para = strings.Join(strings.Fields(strings.ReplaceAll(para, "\n", " ")), " ")
		if para == "" {
			continue
		}
		var sentences []string
		last := 0
		for _, m := range sentenceEnd.FindAllStringIndex(para, -1) {
			sentences = append(sentences, strings.TrimSpace(para[last:m[1]]))
			last = m[1]
		}
		if rest := strings.TrimSpace(para[last:]); rest != "" {
			sentences = append(sentences, rest)
		}
		var cur strings.Builder
		flush := func(pause float64) {
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, textPiece{t, pause})
			}
			cur.Reset()
		}
		for _, s := range sentences {
			for len(s) > max { // one very long "sentence": cut at a space
				cut := strings.LastIndexAny(s[:max], " ,،、，")
				if cut >= max/3 {
					_, w := utf8.DecodeRuneInString(s[cut:])
					cut += w // keep the comma with the words before it
				} else {
					cut = max
					for cut > 0 && !utf8.RuneStart(s[cut]) {
						cut--
					}
				}
				if cur.Len() > 0 {
					flush(0.25)
				}
				out = append(out, textPiece{strings.TrimSpace(s[:cut]), 0.15})
				s = strings.TrimSpace(s[cut:])
			}
			if cur.Len() > 0 && cur.Len()+len(s) > max {
				flush(0.25)
			}
			if cur.Len() > 0 {
				cur.WriteByte(' ')
			}
			cur.WriteString(s)
		}
		flush(0.6)
	}
	return out
}

// speakable keeps the text from being read as an option by the command-line tool.
func speakable(s string) string {
	s = strings.TrimLeftFunc(s, func(r rune) bool { return r == '-' || unicode.IsSpace(r) })
	return s
}

func ttsArgs(v ttsVoice, speed float64, threads int) ([]string, error) {
	dir := packDir(v.Pack)
	ls := strconv.FormatFloat(1/speed, 'f', 3, 64)
	a := []string{"--num-threads=" + strconv.Itoa(threads), "--sid=" + strconv.Itoa(v.sid)}
	switch v.Pack {
	case "pocket":
		a = append(a, pocketArgs(v, speed)...)
	case "kokoro":
		k := func(n string) string { return filepath.Join(dir, n) }
		a = append(a, "--kokoro-model="+k("model.int8.onnx"), "--kokoro-voices="+k("voices.bin"),
			"--kokoro-tokens="+k("tokens.txt"), "--kokoro-data-dir="+k("espeak-ng-data"), "--kokoro-length-scale="+ls)
		switch v.code {
		case "en-us":
			a = append(a, "--kokoro-lexicon="+k("lexicon-us-en.txt"))
		case "en-gb":
			a = append(a, "--kokoro-lexicon="+k("lexicon-gb-en.txt"))
		case "zh":
			a = append(a, "--kokoro-lexicon="+k("lexicon-us-en.txt")+","+k("lexicon-zh.txt"),
				"--tts-rule-fsts="+k("date-zh.fst")+","+k("phone-zh.fst")+","+k("number-zh.fst"))
		default:
			a = append(a, "--kokoro-lang="+v.code)
		}
	case "bangla":
		a = append(a, "--vits-model="+filepath.Join(dir, "bn.onnx"), "--vits-tokens="+filepath.Join(dir, "tokens.txt"),
			"--vits-data-dir="+filepath.Join(dir, "espeak-ng-data"), "--vits-noise-scale=0.667",
			"--vits-noise-scale-w=0.8", "--vits-length-scale="+ls)
	default:
		return nil, cvError("Unknown voice.")
	}
	return a, nil
}

// runTool runs a sherpa-onnx tool and stops it if the job is cancelled.
func runTool(ctx context.Context, bin string, args ...string) error {
	cmd := command(bin, args...)
	var out tailBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return cvError("Couldn't start the voice engine: " + err.Error())
	}
	attachChildJob(cmd)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killTree(cmd)
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return errCancelled
	}
	if err != nil {
		logf("voice: %s failed: %v\n%s", filepath.Base(bin), err, out.String())
		return cvError("The voice engine stopped with an error. Try again, or try another voice.")
	}
	return nil
}

// speak reads text aloud into a WAV file (mono, 16-bit, the voice's own sample rate).
func speak(ctx context.Context, work, text string, v ttsVoice, speed float64, progress func(float64)) (string, error) {
	tts, err := sherpaTool("sherpa-onnx-offline-tts")
	if err != nil {
		return "", err
	}
	if speed < 0.5 || speed > 2 {
		speed = 1
	}
	pieces := splitText(text, 600)
	if len(pieces) == 0 {
		return "", cvError("Type or paste some text first.")
	}
	threads := runtime.NumCPU()
	if threads > 4 {
		threads = 4
	}
	base, err := ttsArgs(v, speed, threads)
	if err != nil {
		return "", err
	}
	var all []byte
	rate := 0
	total := 0
	for _, p := range pieces {
		total += len(p.Text)
	}
	doneChars := 0
	for i, p := range pieces {
		if ctx.Err() != nil {
			return "", errCancelled
		}
		t := speakable(p.Text)
		if t == "" {
			continue
		}
		wav := filepath.Join(work, fmt.Sprintf("piece%04d.wav", i))
		if err := runTool(ctx, tts, append(base, "--output-filename="+wav, t)...); err != nil {
			return "", err
		}
		r, pcm, err := readWav(wav)
		os.Remove(wav)
		if err != nil {
			return "", cvError("The voice engine made no sound for: " + clip(t, 40))
		}
		rate = r
		all = append(all, pcm...)
		all = append(all, make([]byte, int(p.Pause*float64(r))*2)...)
		doneChars += len(p.Text)
		progress(float64(doneChars) * 100 / float64(total))
	}
	if rate == 0 {
		return "", cvError("There was nothing to read aloud.")
	}
	out := filepath.Join(work, "speech.wav")
	return out, writeWav(out, rate, all)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// readWav returns a 16-bit mono WAV's sample rate and samples.
func readWav(p string) (int, []byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, nil, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, nil, fmt.Errorf("not a WAV file")
	}
	rate, bits, ch := 0, 0, 0
	for i := 12; i+8 <= len(b); {
		id := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4 : i+8]))
		body := b[i+8 : min(len(b), i+8+n)]
		switch id {
		case "fmt ":
			if len(body) >= 16 {
				ch = int(binary.LittleEndian.Uint16(body[2:4]))
				rate = int(binary.LittleEndian.Uint32(body[4:8]))
				bits = int(binary.LittleEndian.Uint16(body[14:16]))
			}
		case "data":
			if rate == 0 || bits != 16 || ch != 1 {
				return 0, nil, fmt.Errorf("unexpected WAV format (%d Hz, %d bit, %d ch)", rate, bits, ch)
			}
			return rate, body, nil
		}
		i += 8 + n + n%2
	}
	return 0, nil, fmt.Errorf("no sound in the WAV file")
}

func writeWav(p string, rate int, pcm []byte) error {
	var h bytes.Buffer
	w := func(v any) { binary.Write(&h, binary.LittleEndian, v) }
	h.WriteString("RIFF")
	w(uint32(36 + len(pcm)))
	h.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	h.WriteString("data")
	w(uint32(len(pcm)))
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err := f.Write(h.Bytes()); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(pcm); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ---------- the two jobs ----------

// voiceDownload fetches the packs a job needs, as the first part of its progress.
func (j *cvJob) voiceDownload(ids ...string) error {
	var need []voicePack
	var total int64
	for _, id := range ids {
		if !packReady(id) {
			p, ok := packByID(id)
			if !ok {
				return cvError("Voice Lab doesn't work on this kind of computer yet.")
			}
			need = append(need, p)
			total += p.size()
		}
	}
	var before int64
	for _, p := range need {
		j.stage(fmt.Sprintf("Downloading the %s (%s, once)", p.Label, mbText(p.size())))
		if _, err := ensurePack(j.ctx, p.ID, func(d, _ int64) {
			j.progress(float64(before+d) * 100 / float64(total))
		}); err != nil {
			return err
		}
		before += p.size()
	}
	return nil
}

func mbText(n int64) string {
	if n < 1<<20 {
		return "under 1 MB"
	}
	return fmt.Sprintf("%d MB", (n+(1<<19))>>20)
}

// speakJob: opts.text, opts.voice, opts.speed; format mp3 (default), m4a or wav.
func (j *cvJob) speakJob(out, format string) error {
	v, ok := voiceByID(j.spec.opt("voice"))
	if !ok {
		return cvError("Pick a voice first.")
	}
	text := j.spec.Opts["text"]
	if strings.TrimSpace(text) == "" {
		return cvError("Type or paste some text first.")
	}
	if !packReady("engine") || !packReady(v.Pack) {
		if err := j.sub(0, 30).voiceDownload("engine", v.Pack); err != nil {
			return err
		}
		j = j.sub(30, 70)
	}
	j.stage("Reading aloud")
	wav, err := speak(j.ctx, j.work, text, v, j.spec.optF("speed", 1), func(p float64) { j.progress(p * 0.9) })
	if err != nil {
		return err
	}
	return j.voiceEncode(wav, out, format, nil)
}

// voiceEncode saves a WAV as mp3/m4a/wav, with extra audio filters.
func (j *cvJob) voiceEncode(wav, out, format string, af []string) error {
	if format == "wav" && len(af) == 0 {
		return os.Rename(wav, out)
	}
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	a := []string{"-i", wav}
	a = append(a, filterArgs("-af", af)...)
	a = append(a, audioCodecArgs(encoders(ff), format)...)
	a = append(a, out)
	j.stage("Saving")
	return j.sub(90, 10).runFF(a, 0, format)
}

func audioCodecArgs(enc map[string]bool, format string) []string {
	switch format {
	case "mp3":
		if enc["libmp3lame"] {
			return []string{"-c:a", "libmp3lame", "-q:a", "2"}
		}
		return nil
	case "m4a":
		return []string{"-c:a", "aac", "-b:a", "192k"}
	case "flac":
		return []string{"-c:a", "flac"}
	case "opus", "ogg":
		if enc["libopus"] {
			return []string{"-c:a", "libopus", "-b:a", "96k"}
		}
		return nil
	}
	return []string{"-c:a", "pcm_s16le"}
}

// cleanMode returns the pack and sample rate for a strength ("" pack = FFmpeg only).
func cleanMode(strength string) (pack string, rate int) {
	switch strength {
	case "light":
		return "clean-light", 16000
	case "basic":
		return "", 48000
	}
	return "clean-strong", 48000
}

// voiceFilters: what runs after the noise is gone. Basic mode does its noise removal here too.
func voiceFilters(strength string, level bool) []string {
	f := []string{"highpass=f=70"}
	if strength == "basic" {
		f = append(f, "afftdn=nr=20:nf=-30")
	}
	if level {
		f = append(f, "loudnorm=I=-16:TP=-1.5:LRA=11", "aresample=48000") // loudnorm alone would write 192 kHz
	}
	return f
}

// denoise makes a mono WAV of in's sound (from start, for length seconds; 0 = all) with the noise
// removed. In basic mode it only extracts the sound; the removal is in voiceFilters.
func (j *cvJob) denoise(in, strength string, start, length, total float64) (string, error) {
	pack, rate := cleanMode(strength)
	pieces := filepath.Join(j.work, "in")
	os.MkdirAll(pieces, 0o755)
	a := []string{}
	if start > 0 {
		a = append(a, "-ss", ff3(start))
	}
	a = append(a, "-i", in)
	if length > 0 {
		a = append(a, "-t", ff3(length))
	}
	a = append(a, "-vn", "-map", "0:a:0", "-ac", "1", "-ar", strconv.Itoa(rate), "-c:a", "pcm_s16le")
	if pack == "" {
		out := filepath.Join(j.work, "clean.wav")
		j.stage("Reading the sound")
		return out, j.sub(0, 30).runFF(append(a, out), lenOr(length, total), "wav")
	}
	// The noise remover reads a whole file into memory, so long recordings go in 5-minute pieces.
	a = append(a, "-f", "segment", "-segment_time", "300", filepath.Join(pieces, "p%04d.wav"))
	j.stage("Reading the sound")
	if err := j.sub(0, 10).runFF(a, lenOr(length, total), "wav"); err != nil {
		return "", err
	}
	den, err := sherpaTool("sherpa-onnx-offline-denoiser")
	if err != nil {
		return "", err
	}
	model := filepath.Join(packDir(pack), voicePacks[pack].Files[0].Name)
	flag := "--speech-denoiser-dpdfnet-model="
	if pack == "clean-light" {
		flag = "--speech-denoiser-gtcrn-model="
	}
	names, _ := filepath.Glob(filepath.Join(pieces, "p*.wav"))
	sort.Strings(names)
	if len(names) == 0 {
		return "", cvError("This file has no sound to clean.")
	}
	var all []byte
	outRate := 0
	j.stage("Removing noise")
	for i, p := range names {
		c := p + ".clean.wav"
		if err := runTool(j.ctx, den, flag+model, "--num-threads=2", "--input-wav="+p, "--output-wav="+c); err != nil {
			return "", err
		}
		r, pcm, err := readWav(c)
		if err != nil {
			return "", cvError("The noise remover made no sound.")
		}
		outRate = r
		all = append(all, pcm...)
		os.Remove(p)
		os.Remove(c)
		j.progress(10 + float64(i+1)*75/float64(len(names)))
	}
	out := filepath.Join(j.work, "clean.wav")
	return out, writeWav(out, outRate, all)
}

func lenOr(length, total float64) float64 {
	if length > 0 {
		return length
	}
	return total
}

// voiceCleanJob: opts.strength strong|light|basic, opts.level "0" to leave the loudness alone.
// Audio in -> audio out in the chosen format; video in -> the same video with the cleaned sound.
func (j *cvJob) voiceCleanJob(in, out, format string) error {
	mi := probeMedia(in)
	if !mi.HasAudio {
		return cvError(filepath.Base(in) + " has no sound to clean.")
	}
	strength := j.spec.opt("strength")
	pack, _ := cleanMode(strength)
	if pack != "" && (!packReady("engine") || !packReady(pack)) {
		if err := j.sub(0, 20).voiceDownload("engine", pack); err != nil {
			return err
		}
		j = j.sub(20, 80)
	}
	clean, err := j.sub(0, 85).denoise(in, strength, 0, 0, mi.Duration)
	if err != nil {
		return err
	}
	af := voiceFilters(strength, j.spec.opt("level") != "0")
	if !(mi.HasVideo && !mi.Still) || cvAudioFormats[format] {
		return j.voiceEncode(clean, out, format, af)
	}
	ff, err := ffmpegPath()
	if err != nil {
		return err
	}
	a := []string{"-i", in, "-i", clean, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy"}
	a = append(a, filterArgs("-af", af)...)
	if extOf(out) == "webm" && encoders(ff)["libopus"] {
		a = append(a, "-c:a", "libopus", "-b:a", "128k")
	} else {
		a = append(a, "-c:a", "aac", "-b:a", "192k")
	}
	a = append(a, "-ac", "2")
	if extOf(out) == "mp4" || extOf(out) == "mov" || extOf(out) == "m4v" {
		a = append(a, "-movflags", "+faststart")
	}
	a = append(a, out)
	j.stage("Saving")
	return j.sub(85, 15).runFF(a, mi.Duration, format)
}

// cleanOutput picks the result's format: what was asked, else the same kind of file as the input.
func cleanOutput(in, format string) string {
	if format != "" && format != "same" {
		return format
	}
	e := extOf(in)
	switch {
	case setOf("mp4", "mov", "m4v", "mkv", "webm")[e]:
		return e
	case cvVideoExt[e]:
		return "mp4"
	case setOf("mp3", "m4a", "wav", "flac")[e]:
		return e
	}
	return "m4a"
}

// speakName makes a file name from the first words of the text.
func speakName(text string) string {
	words := strings.Fields(text)
	if len(words) > 6 {
		words = words[:6]
	}
	n := clip(strings.Join(words, " "), 50)
	n = strings.TrimRight(n, ".,;:!?…")
	if n == "" {
		n = "Read aloud"
	}
	return n
}

// ---------- previews for the page ----------

// voicePreview: op "voiceSay" {voice, speed, text} reads up to ~400 characters; op "voiceClean"
// {path, strength, level, at} cleans 12 seconds from "at" and returns before and after.
func (s *cvServer) voiceOp(ctx context.Context, req map[string]any) map[string]any {
	switch str(req, "op") {
	case "voices":
		return voiceState()
	case "voiceGet":
		id := str(req, "pack")
		if _, ok := packByID(id); !ok {
			return fail("Voice Lab doesn't work on this kind of computer yet.")
		}
		ids := []string{id}
		if id != "engine" {
			ids = []string{"engine", id}
		}
		go func() {
			for _, id := range ids {
				if _, err := ensurePack(context.Background(), id, func(int64, int64) {}); err != nil {
					logf("voice: download %s: %v", id, err)
					return
				}
			}
		}()
		return voiceState()
	}
	dir, err := os.MkdirTemp(s.tmp, "voice-")
	if err != nil {
		return fail(err.Error())
	}
	j := &cvJob{ctx: ctx, spec: cvSpec{Opts: map[string]string{}}, work: dir, progress: func(float64) {}}
	switch str(req, "op") {
	case "voiceCopy", "voiceForget":
		return s.voiceCopyOp(ctx, req, j)
	case "voiceSay":
		v, ok := voiceByID(str(req, "voice"))
		if !ok {
			return fail("Pick a voice first.")
		}
		if !packReady("engine") || !packReady(v.Pack) {
			return map[string]any{"ok": false, "need": v.Pack}
		}
		text := clip(strings.TrimSpace(str(req, "text")), 400)
		wav, err := speak(ctx, dir, text, v, num(fmt.Sprint(req["speed"])), func(float64) {})
		if err != nil {
			return fail(errMsg(err))
		}
		return map[string]any{"ok": true, "url": s.fileURL(wav)}
	case "voiceClean":
		path, ok := s.readable(str(req, "path"))
		if !ok {
			return fail("That file isn't there any more, or AnySave may not read it.")
		}
		strength := str(req, "strength")
		if pack, _ := cleanMode(strength); pack != "" && (!packReady("engine") || !packReady(pack)) {
			return map[string]any{"ok": false, "need": pack}
		}
		mi := probeMedia(path)
		if !mi.HasAudio {
			return fail(filepath.Base(path) + " has no sound to clean.")
		}
		at := num(fmt.Sprint(req["at"]))
		if at < 0 || at > mi.Duration-1 {
			at = 0
		}
		const span = 12.0
		before := filepath.Join(dir, "before.wav")
		if err := j.runFF([]string{"-ss", ff3(at), "-i", path, "-t", ff3(span), "-vn", "-map", "0:a:0", "-c:a", "pcm_s16le", before}, span, "wav"); err != nil {
			return fail(errMsg(err))
		}
		clean, err := j.denoise(path, strength, at, span, span)
		if err != nil {
			return fail(errMsg(err))
		}
		after := filepath.Join(dir, "after.wav")
		a := []string{"-i", clean}
		a = append(a, filterArgs("-af", voiceFilters(strength, str(req, "level") != "0"))...)
		if err := j.runFF(append(a, "-c:a", "pcm_s16le", after), span, "wav"); err != nil {
			return fail(errMsg(err))
		}
		return map[string]any{"ok": true, "before": s.fileURL(before), "after": s.fileURL(after), "at": at}
	}
	return fail("Unknown request")
}
