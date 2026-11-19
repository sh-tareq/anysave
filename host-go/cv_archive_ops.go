package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"anysave-host/arc"
)

// The Archives page's quick calls (the long ones are jobs, in cv_archive.go):
//
//	arcOpen     {path, password}  what's inside, how safe it is, the parts of a split set
//	arcPreview  {path, password, i}  one file from inside, for a quick look (text or picture)
//	arcHistory / arcForget {path?}   the recent archives list
//	arcTrash    {path}            move an archive (every part of it) to the Trash
//	pickFolder  {prompt}          choose a folder (where to unpack)
//	arcShow     {path, how}       open a result folder, or show a result in Finder / Explorer

const (
	arcMaxList    = 50000    // entries sent to the page
	arcMaxPreview = 32 << 20 // biggest file shown in a preview
	arcMaxText    = 256 << 10
)

func (s *cvServer) arcOp(ctx context.Context, req map[string]any) map[string]any {
	op := str(req, "op")
	switch op {
	case "pickFolder":
		p, err := pickFolder(strDef(str(req, "prompt"), "Choose a folder"))
		if err != nil {
			return fail("Couldn't open the folder chooser: " + err.Error())
		}
		if p != "" {
			s.arcDir(p)
			s.arcDir(filepath.Dir(p)) // a folder being packed: the archive goes next to it
		}
		return map[string]any{"ok": true, "path": p}
	case "arcShow":
		return s.arcShow(str(req, "path"), str(req, "how"))
	case "arcHistory":
		return map[string]any{"ok": true, "items": arcHistoryList()}
	case "arcForget":
		arcHistoryForget(str(req, "path"))
		return map[string]any{"ok": true, "items": arcHistoryList()}
	}
	path, ok := s.readable(str(req, "path"))
	if !ok && (arcInHistory(str(req, "path")) || (op != "arcTrash" && s.arcNear(str(req, "path")))) {
		path, ok = str(req, "path"), isFile(str(req, "path"))
		if ok {
			s.allow(path)
		}
	}
	if !ok {
		return fail("That file isn't there any more, or AnySave may not read it.")
	}
	switch op {
	case "arcOpen":
		return s.arcOpen(path, str(req, "password"))
	case "arcPreview":
		return s.arcPreview(ctx, path, str(req, "password"), int(num(fmt.Sprint(req["i"]))))
	case "arcTrash":
		parts, _ := arc.Parts(path)
		if isPreviewCopy(path) {
			return fail("This is a copy of a dropped file; the original is where you dropped it from.")
		}
		if err := moveToTrash(parts); err != nil {
			return fail("Couldn't move it to the Trash: " + err.Error())
		}
		arcHistoryForget(path)
		return map[string]any{"ok": true, "count": len(parts)}
	}
	return fail("Unknown request")
}

func strDef(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (s *cvServer) arcOpen(path, pw string) map[string]any {
	a, err := arc.Open(path, arc.Options{Password: pw})
	if errors.Is(err, arc.ErrNeedPassword) || errors.Is(err, arc.ErrWrongPassword) {
		return map[string]any{"ok": true, "needPassword": true, "wrong": errors.Is(err, arc.ErrWrongPassword),
			"name": filepath.Base(arc.FirstPart(path)), "path": path}
	}
	if err != nil {
		return fail(arcErr(err).Error())
	}
	defer a.Close()
	info := a.Info()
	es := a.Entries()
	more := 0
	if len(es) > arcMaxList {
		more = len(es) - arcMaxList
		es = es[:arcMaxList]
	}
	parts, _ := arc.Parts(path)
	first := arc.FirstPart(path)
	if !isPreviewCopy(path) {
		arcHistoryAdd(first, info.Format, info.Size)
	}
	for _, p := range parts {
		s.allow(p)
	}
	s.arcDir(filepath.Dir(first))
	return map[string]any{"ok": true, "path": path, "first": first, "name": arc.SetName(filepath.Base(first)),
		"info": info, "entries": es, "more": more, "safety": arc.Check(a), "parts": len(parts),
		"copy": isPreviewCopy(path), "folder": filepath.Dir(first)}
}

var textExt = setOf("txt", "md", "markdown", "csv", "tsv", "json", "xml", "html", "htm", "css", "js", "ts", "go", "py",
	"rb", "java", "c", "h", "cpp", "hpp", "cs", "rs", "swift", "kt", "sh", "bat", "ps1", "yml", "yaml", "toml", "ini",
	"cfg", "conf", "log", "srt", "vtt", "ass", "lrc", "nfo", "sql", "php", "plist", "gitignore", "license", "readme", "rtf", "svg")

func (s *cvServer) arcPreview(ctx context.Context, path, pw string, idx int) map[string]any {
	a, err := arc.Open(path, arc.Options{Password: pw})
	if err != nil {
		return fail(arcErr(err).Error())
	}
	defer a.Close()
	var target *arc.Entry
	for _, e := range a.Entries() {
		if e.Index == idx {
			target = e
		}
	}
	if target == nil || target.Dir || target.Link != "" || target.Special || target.Unsafe != "" {
		return fail("There's nothing to show for this item.")
	}
	if target.Size > arcMaxPreview {
		return fail("This file is too big for a quick look (" + arc.Bytes(target.Size) + "). Unpack it instead.")
	}
	dir, err := os.MkdirTemp(s.tmp, "look-")
	if err != nil {
		return fail(err.Error())
	}
	name := filepath.Base(target.Name)
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	out := filepath.Join(dir, safeName(name))
	if e := extOf(name); e != "" && !strings.HasSuffix(out, "."+e) {
		out += "." + e
	}
	err = a.Walk(ctx, func(e *arc.Entry) bool { return e == target }, func(e *arc.Entry, r io.Reader) error {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(r, arcMaxPreview+1))
		f.Close()
		if err != nil {
			return err
		}
		return arc.ErrStop
	})
	if err != nil && !errors.Is(err, arc.ErrStop) {
		os.RemoveAll(dir)
		return fail(arcErr(err).Error())
	}
	res := map[string]any{"ok": true, "name": name, "size": target.Size, "kind": cvKindOf(name), "url": s.fileURL(out)}
	e := extOf(name)
	if textExt[e] || (e == "" && target.Size < arcMaxText) || strings.EqualFold(name, "readme") {
		b, _ := os.ReadFile(out)
		cut := len(b) > arcMaxText
		if cut {
			b = b[:arcMaxText]
		}
		if utf8.Valid(b) || cut || looksText(b) {
			res["text"] = strings.ToValidUTF8(string(b), "�")
			res["cut"] = cut
		}
	}
	return res
}

func looksText(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return len(b) > 0
}

// ---------- recent archives ----------

type arcRecent struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Format string `json:"format"`
	Size   int64  `json:"size"`
	When   int64  `json:"when"` // Unix ms
}

var arcHistMu sync.Mutex

func arcHistoryFile() string { return filepath.Join(appDir, "archive-history.json") }

func arcHistoryRead() []arcRecent {
	var l []arcRecent
	if b, err := os.ReadFile(arcHistoryFile()); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

func arcHistoryWrite(l []arcRecent) {
	b, _ := json.MarshalIndent(l, "", " ")
	tmp := arcHistoryFile() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, arcHistoryFile())
	}
}

func arcHistoryAdd(p, format string, size int64) {
	arcHistMu.Lock()
	defer arcHistMu.Unlock()
	l := []arcRecent{{Path: p, Name: arc.SetName(filepath.Base(p)), Format: format, Size: size, When: time.Now().UnixMilli()}}
	for _, r := range arcHistoryRead() {
		if r.Path != p && len(l) < 30 {
			l = append(l, r)
		}
	}
	arcHistoryWrite(l)
}

func arcHistoryList() []arcRecent {
	arcHistMu.Lock()
	defer arcHistMu.Unlock()
	out := []arcRecent{}
	for _, r := range arcHistoryRead() {
		if isFile(r.Path) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When > out[j].When })
	return out
}

func arcInHistory(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range arcHistoryList() {
		if r.Path == p {
			return true
		}
		if parts, _ := arc.Parts(r.Path); len(parts) > 1 {
			for _, q := range parts {
				if q == p {
					return true
				}
			}
		}
	}
	return false
}

func arcHistoryForget(p string) {
	arcHistMu.Lock()
	defer arcHistMu.Unlock()
	var l []arcRecent
	if p != "" {
		first := arc.FirstPart(p)
		for _, r := range arcHistoryRead() {
			if r.Path != p && r.Path != first {
				l = append(l, r)
			}
		}
	}
	arcHistoryWrite(l)
}

func (s *cvServer) arcDir(d string) {
	if abs, err := filepath.Abs(d); err == nil {
		s.mu.Lock()
		s.arcDirs[abs] = true
		s.mu.Unlock()
	}
}

// arcShow opens a folder (never a file: a file from an archive could be a program) or shows an
// item in Finder / Explorer. Only places next to an archive the user opened, a folder they chose,
// or AnySave's downloads folder.
func (s *cvServer) arcShow(p, how string) map[string]any {
	abs, err := filepath.Abs(p)
	if err != nil || p == "" {
		return fail("That isn't there any more.")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fail("That isn't there any more. It may have been moved or deleted.")
	}
	ok := within(abs, downloadDir) || within(abs, userDownloadsDir())
	s.mu.Lock()
	for d := range s.arcDirs {
		if abs == d || within(abs, d) {
			ok = true
		}
	}
	s.mu.Unlock()
	if !ok {
		return fail("AnySave can only show what it unpacked or made here.")
	}
	if how == "open" && st.IsDir() {
		if err := openWithDefaultApp(abs); err != nil {
			return fail("Couldn't open the folder: " + err.Error())
		}
		return map[string]any{"ok": true}
	}
	revealInFileManager(abs)
	return map[string]any{"ok": true}
}

// arcNear is true for a file in the Downloads folder, or next to an archive the user opened (a
// repaired or converted copy, the parts of a split set), or in a folder they chose: the Archives
// page may look inside it. Moving to the Trash is never allowed this way.
func (s *cvServer) arcNear(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil || p == "" {
		return false
	}
	if within(abs, userDownloadsDir()) {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for d := range s.arcDirs {
		if within(abs, d) {
			return true
		}
	}
	return false
}
