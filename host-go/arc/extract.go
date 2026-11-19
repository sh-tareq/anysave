package arc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// ExtractOptions says what to unpack and where.
type ExtractOptions struct {
	Dest      string   // the folder the result goes into
	Name      string   // folder name for "folder" and "smart" (usually the archive name without its extension)
	Mode      string   // "smart" (default): no extra folder when there's one thing inside; "folder"; "here"
	Policy    string   // when a name is taken: "rename" (default, keep both), "replace", "skip"
	Only      []string // entries (files or folders) to unpack; empty means everything
	Base      string   // the folder being browsed: its path is dropped from the selected items
	KeepJunk  bool     // keep __MACOSX folders, .DS_Store and Thumbs.db
	KeepGoing bool     // recovery: skip damaged files instead of stopping
	Progress  func(done, total int64, name string)
	FreeSpace func(dir string) int64 // bytes free on the disk of dir, or -1 when unknown
}

// Skipped is a file that wasn't unpacked, and why.
type Skipped struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

// ExtractResult says what was made.
type ExtractResult struct {
	Path     string    `json:"path"`  // the folder or file to show
	Items    []string  `json:"items"` // what was placed into Dest
	Files    int       `json:"files"`
	Dirs     int       `json:"dirs"`
	Bytes    int64     `json:"bytes"`
	Skipped  []Skipped `json:"skipped,omitempty"`
	Damaged  []Skipped `json:"damaged,omitempty"`
	Existing int       `json:"existing"` // names that were already taken
}

// BombError refuses an archive that would fill the disk.
type BombError struct{ Detail string }

func (e *BombError) Error() string { return e.Detail }

// IsJunk is true for the Mac/Windows clutter that gets packed by accident.
func IsJunk(name string) bool {
	if name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/") {
		return true
	}
	switch path.Base(name) {
	case ".DS_Store", "Thumbs.db", "desktop.ini":
		return true
	}
	return false
}

// Selected says whether e is one of the chosen items (or inside one).
func Selected(e *Entry, only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, o := range only {
		o = strings.Trim(o, "/")
		if o == "" || e.Name == o || strings.HasPrefix(e.Name, o+"/") {
			return true
		}
	}
	return false
}

// Extract unpacks the archive safely: everything goes first into a hidden folder inside Dest, names
// that would escape are refused, links and device files are never made, sizes are checked against
// what the archive says, and nothing half-made is left behind after an error or Cancel.
func Extract(ctx context.Context, a Archive, o ExtractOptions) (*ExtractResult, error) {
	if w := a.Info().Warning; w != "" {
		return nil, &BombError{Detail: w}
	}
	if o.Mode == "" {
		o.Mode = "smart"
	}
	if o.Name == "" {
		o.Name = "Unpacked"
	}
	res := &ExtractResult{}
	base := strings.Trim(o.Base, "/")
	var total int64
	want := map[*Entry]bool{}
	for _, e := range a.Entries() {
		if !Selected(e, o.Only) {
			continue
		}
		if !o.KeepJunk && IsJunk(e.Name) {
			continue
		}
		switch {
		case e.Unsafe != "":
			res.Skipped = append(res.Skipped, Skipped{e.Name, "unsafe name: " + e.Unsafe})
			continue
		case e.Link != "" && !e.Dir:
			if e.Link == "special" {
				res.Skipped = append(res.Skipped, Skipped{e.Name, "device or special file"})
			} else {
				res.Skipped = append(res.Skipped, Skipped{e.Name, "link (links aren't unpacked, for safety)"})
			}
			continue
		case e.Special:
			res.Skipped = append(res.Skipped, Skipped{e.Name, "device or special file"})
			continue
		}
		want[e] = true
		if e.Size > 0 {
			total += e.Size
		}
	}
	if len(want) == 0 {
		if len(res.Skipped) > 0 {
			return res, &BombError{Detail: "Nothing could be unpacked safely: " + res.Skipped[0].Why + "."}
		}
		return nil, &UnsupportedError{Detail: "There's nothing to unpack."}
	}
	if err := os.MkdirAll(o.Dest, 0o755); err != nil {
		return nil, err
	}
	free := int64(-1)
	if o.FreeSpace != nil {
		free = o.FreeSpace(o.Dest)
	}
	const margin = 64 << 20
	if free >= 0 && total+margin > free {
		return nil, &BombError{Detail: fmt.Sprintf("This needs %s of space, but the disk has only %s free.", Bytes(total), Bytes(max(0, free)))}
	}
	stage, err := os.MkdirTemp(o.Dest, ".anysave-unpacking-")
	if err != nil {
		return nil, err
	}
	os.Chmod(stage, 0o755) // it may become the result folder
	defer os.RemoveAll(stage)

	var done int64
	buf := make([]byte, 1<<20)
	walkErr := a.Walk(ctx, func(e *Entry) bool { return want[e] }, func(e *Entry, r io.Reader) error {
		rel := e.Name
		if base != "" && strings.HasPrefix(rel, base+"/") {
			rel = rel[len(base)+1:]
		}
		local := localPath(stage, rel)
		if e.Dir {
			if err := os.MkdirAll(local, 0o755); err == nil {
				res.Dirs++
			}
			return nil
		}
		if r == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode(e))
		if errors.Is(err, os.ErrExist) { // the same name twice (or A.txt and a.txt on a Mac): keep both
			local = uniquePath(local)
			f, err = os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode(e))
		}
		if err != nil {
			return err
		}
		var n int64
		var cerr error
		for {
			k, rerr := r.Read(buf)
			if k > 0 {
				if _, werr := f.Write(buf[:k]); werr != nil {
					cerr = werr
					break
				}
				n += int64(k)
				done += int64(k)
				if free >= 0 && done+margin > free {
					cerr = &BombError{Detail: "The disk is nearly full, so unpacking was stopped."}
					break
				}
				if o.Progress != nil {
					o.Progress(done, total, e.Name)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				cerr = rerr
				break
			}
		}
		if err := f.Close(); cerr == nil {
			cerr = err
		}
		if cerr != nil {
			os.Remove(local)
			if o.KeepGoing && isDamage(cerr) {
				res.Damaged = append(res.Damaged, Skipped{e.Name, plainDamage(cerr)})
				return nil
			}
			return cerr
		}
		if !e.Modified.IsZero() {
			os.Chtimes(local, e.Modified, e.Modified)
		}
		res.Files++
		res.Bytes += n
		return nil
	})
	if walkErr != nil {
		if !(o.KeepGoing && isDamage(walkErr) && res.Files > 0) {
			return nil, walkErr
		}
		res.Damaged = append(res.Damaged, Skipped{"(the rest of the archive)", plainDamage(walkErr)})
	}
	if ctx.Err() != nil {
		return nil, ErrCancelled
	}

	tops, _ := os.ReadDir(stage)
	if len(tops) == 0 {
		return nil, &UnsupportedError{Detail: "There's nothing to unpack."}
	}
	mode := o.Mode
	if mode == "smart" && len(tops) > 1 {
		mode = "folder"
	}
	switch mode {
	case "folder":
		target := filepath.Join(o.Dest, safeComponent(o.Name))
		if _, err := os.Lstat(target); err == nil && (o.Policy == "" || o.Policy == "rename") {
			target = uniquePath(target)
		}
		p, err := place(stage, target, o.Policy, res)
		if err != nil {
			return nil, err
		}
		res.Path, res.Items = p, []string{p}
	default: // "smart" with one item, or "here"
		for _, t := range tops {
			p, err := place(filepath.Join(stage, t.Name()), filepath.Join(o.Dest, t.Name()), o.Policy, res)
			if err != nil {
				return nil, err
			}
			res.Items = append(res.Items, p)
		}
		res.Path = o.Dest
		if len(res.Items) == 1 {
			res.Path = res.Items[0]
		}
	}
	return res, nil
}

// place moves src to dst. When dst exists: "rename" keeps both, "skip" keeps the old one,
// "replace" overwrites; two folders are merged.
func place(src, dst, policy string, res *ExtractResult) (string, error) {
	dstInfo, err := os.Lstat(dst)
	if err != nil {
		return dst, os.Rename(src, dst)
	}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return "", err
	}
	if srcInfo.IsDir() && dstInfo.IsDir() && policy != "" && policy != "rename" {
		kids, _ := os.ReadDir(src)
		for _, k := range kids {
			if _, err := place(filepath.Join(src, k.Name()), filepath.Join(dst, k.Name()), policy, res); err != nil {
				return "", err
			}
		}
		return dst, nil
	}
	res.Existing++
	switch policy {
	case "skip":
		return dst, nil
	case "replace":
		if dstInfo.IsDir() != srcInfo.IsDir() || dstInfo.IsDir() {
			if err := os.RemoveAll(dst); err != nil {
				return "", err
			}
		}
		return dst, os.Rename(src, dst)
	}
	d := uniquePath(dst)
	return d, os.Rename(src, d)
}

func fileMode(e *Entry) os.FileMode {
	if e.Mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func isDamage(err error) bool {
	var d *DamagedError
	return errors.As(err, &d)
}

func plainDamage(err error) string {
	var d *DamagedError
	if errors.As(err, &d) {
		return d.Detail
	}
	return err.Error()
}

// localPath makes a safe path under root for a cleaned entry name: overlong parts are shortened,
// control characters (and on Windows the characters it forbids) replaced.
func localPath(root, rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = safeComponent(p)
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func safeComponent(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		case runtime.GOOS == "windows" && strings.ContainsRune(`<>:"|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if runtime.GOOS == "windows" {
		s = strings.TrimRight(s, ". ")
		up := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
		switch up {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "LPT1", "LPT2", "LPT3":
			s = "_" + s
		}
	}
	if s == "" || s == "." || s == ".." {
		s = "_"
	}
	if len(s) > 255 { // most disks allow 255 bytes per name
		ext := path.Ext(s)
		if len(ext) > 16 {
			ext = ""
		}
		stem := s[:len(s)-len(ext)]
		cut := 250 - len(ext)
		for cut > 0 && !utf8.RuneStart(stem[cut]) {
			cut--
		}
		s = stem[:cut] + "~" + ext
	}
	return s
}

// Bytes is a short size: "12 MB".
func Bytes(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n < 1<<40:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.2f TB", float64(n)/(1<<40))
}

// ---------- test ----------

// TestResult is the outcome of testing an archive.
type TestResult struct {
	Files int       `json:"files"`
	Bytes int64     `json:"bytes"`
	Bad   []Skipped `json:"bad,omitempty"`
}

// Test reads every file and checks it against its checksum, without saving anything.
func Test(ctx context.Context, a Archive, progress func(done, total int64, name string)) (*TestResult, error) {
	var total int64
	for _, e := range a.Entries() {
		if e.Size > 0 && !e.Dir {
			total += e.Size
		}
	}
	res := &TestResult{}
	var done int64
	buf := make([]byte, 1<<20)
	err := a.Walk(ctx, func(e *Entry) bool { return !e.Dir && e.Link == "" && !e.Special && e.Unsafe == "" }, func(e *Entry, r io.Reader) error {
		if r == nil {
			return nil
		}
		for {
			k, rerr := r.Read(buf)
			done += int64(k)
			res.Bytes += int64(k)
			if progress != nil && k > 0 {
				progress(done, total, e.Name)
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				if isDamage(rerr) {
					res.Bad = append(res.Bad, Skipped{e.Name, plainDamage(rerr)})
					return nil
				}
				return rerr
			}
		}
		res.Files++
		return nil
	})
	if err != nil {
		if !isDamage(err) {
			return nil, err
		}
		res.Bad = append(res.Bad, Skipped{"(the rest of the archive)", plainDamage(err)})
	}
	return res, nil
}
