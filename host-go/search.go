package main

// Searching transcripts: every "<name>.<lang>.srt" that Transcribe saved, found on disk (so the
// list survives clearing the download list), searched word by word with the time each match is
// said.
//
//	{"action":"searchTranscripts","query":"campaign","dirs":["/…/Downloads/AnySave"]}
//	-> {"ok":true,"results":[{"srt","txt","video","name","lang","modified","count","matches":[{"time","text"}]}],
//	    "scanned":12}
//
// An empty query lists all transcripts (newest first), which is the "library" view.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	searchMaxFiles   = 5000
	searchMaxDepth   = 5
	searchMaxPerFile = 30
	searchMaxResults = 200
)

var transcriptNameRe = regexp.MustCompile(`^(.+)\.([a-z]{2,3}|auto)\.srt$`)

var mediaExts = []string{".mp4", ".mkv", ".webm", ".mov", ".m4v", ".avi", ".mp3", ".m4a", ".wav", ".flac", ".ogg", ".opus", ".aac"}

// normalizeForSearch lowercases and drops characters that make the same word look different:
// zero-width joiners (common in Bangla text) and punctuation.
func normalizeForSearch(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\u200c' || r == '\u200d' || r == '\ufeff':
			continue
		case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r):
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

type transcriptMatch struct {
	Time string  `json:"time"`
	Sec  float64 `json:"sec"`
	Text string  `json:"text"`
}

type transcriptResult struct {
	SRT      string            `json:"srt"`
	TXT      string            `json:"txt,omitempty"`
	Video    string            `json:"video,omitempty"`
	Name     string            `json:"name"`
	Lang     string            `json:"lang"`
	Modified int64             `json:"modified"`
	Count    int               `json:"count"`
	Matches  []transcriptMatch `json:"matches,omitempty"`
	Preview  string            `json:"preview,omitempty"`
}

func clock(sec float64) string {
	s := int(sec)
	if s >= 3600 {
		return srtTime(sec)[:8]
	}
	return srtTime(sec)[3:8]
}

// findTranscripts walks the folders (not too deep, not too many files) for transcript files.
func findTranscripts(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range dirs {
		root = filepath.Clean(root)
		if root == "" || root == "." || !isDir(root) {
			continue
		}
		base := strings.Count(root, string(filepath.Separator))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".anysave-parts" || strings.HasPrefix(d.Name(), ".") && p != root ||
					strings.Count(p, string(filepath.Separator))-base > searchMaxDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if len(out) >= searchMaxFiles {
				return filepath.SkipAll
			}
			if transcriptNameRe.MatchString(d.Name()) && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func searchTranscripts(query string, dirs []string) ([]transcriptResult, int) {
	files := findTranscripts(dirs)
	q := normalizeForSearch(query)
	var words []string
	if q != "" {
		words = strings.Fields(q)
	}
	var results []transcriptResult
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		m := transcriptNameRe.FindStringSubmatch(filepath.Base(f))
		dir := filepath.Dir(f)
		r := transcriptResult{SRT: f, Name: m[1], Lang: m[2]}
		if st, err := os.Stat(f); err == nil {
			r.Modified = st.ModTime().UnixMilli()
		}
		if t := strings.TrimSuffix(f, ".srt") + ".txt"; isFile(t) {
			r.TXT = t
		}
		for _, e := range mediaExts {
			if v := filepath.Join(dir, m[1]+e); isFile(v) {
				r.Video = v
				break
			}
		}
		subs := parseSRT(string(b))
		if len(words) == 0 {
			var pv []string
			for _, s := range subs {
				if len(strings.Join(pv, " ")) > 160 {
					break
				}
				pv = append(pv, strings.ReplaceAll(s.Text, "\n", " "))
			}
			r.Preview = strings.Join(pv, " ")
			results = append(results, r)
			continue
		}
		// A match: every word of the query appears in this subtitle, or in it together with the
		// next one (a phrase can be split across two lines).
		for i, s := range subs {
			text := normalizeForSearch(s.Text)
			joined := text
			if i+1 < len(subs) {
				joined += " " + normalizeForSearch(subs[i+1].Text)
			}
			if !containsAll(text, words) && !(containsAll(joined, words) && !containsAll(normalizeForSearch(nextText(subs, i)), words)) {
				continue
			}
			r.Count++
			if len(r.Matches) < searchMaxPerFile {
				r.Matches = append(r.Matches, transcriptMatch{Time: clock(s.Start), Sec: s.Start, Text: strings.ReplaceAll(s.Text, "\n", " ")})
			}
		}
		if r.Count > 0 {
			results = append(results, r)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if len(words) > 0 && results[i].Count != results[j].Count {
			return results[i].Count > results[j].Count
		}
		return results[i].Modified > results[j].Modified
	})
	if len(results) > searchMaxResults {
		results = results[:searchMaxResults]
	}
	return results, len(files)
}

func nextText(subs []subtitle, i int) string {
	if i+1 < len(subs) {
		return subs[i+1].Text
	}
	return ""
}

func containsAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func actionSearchTranscripts(msg map[string]any) map[string]any {
	dirs := []string{downloadDir}
	if list, ok := msg["dirs"].([]any); ok {
		for _, d := range list {
			if s, ok := d.(string); ok && s != "" {
				dirs = append(dirs, s)
			}
		}
	}
	results, scanned := searchTranscripts(str(msg, "query"), dirs)
	if results == nil {
		results = []transcriptResult{}
	}
	return map[string]any{"ok": true, "results": results, "scanned": scanned}
}
