package arc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	reNum   = regexp.MustCompile(`(?i)^(.*)\.(\d{3})$`)        // name.zip.001
	rePart  = regexp.MustCompile(`(?i)^(.*)\.part(\d+)\.rar$`) // name.part01.rar
	reOldR  = regexp.MustCompile(`(?i)^(.*)\.(rar|r\d\d)$`)    // name.rar, name.r00
	reZipZ  = regexp.MustCompile(`(?i)^(.*)\.(zip|z\d\d)$`)    // name.zip, name.z01
	reNewer = regexp.MustCompile(`(?i)^(.*)\.(\d+)$`)
)

// Parts finds every part of a split archive from any one of them. kind is "split" (.001 parts
// joined end to end), "rar" (RAR volumes), "zsplit" (.z01 … .zip) or "" for a single file.
func Parts(p string) ([]string, string) {
	dir, base := filepath.Dir(p), filepath.Base(p)
	if m := reNum.FindStringSubmatch(base); m != nil {
		var out []string
		for i := 1; i < 10000; i++ {
			q := filepath.Join(dir, fmt.Sprintf("%s.%03d", m[1], i))
			if !isFile(q) {
				break
			}
			out = append(out, q)
		}
		if len(out) > 0 {
			return out, "split"
		}
	}
	if m := rePart.FindStringSubmatch(base); m != nil {
		width := len(m[2])
		var out []string
		for i := 1; i < 10000; i++ {
			q := filepath.Join(dir, fmt.Sprintf("%s.part%0*d.rar", m[1], width, i))
			if !isFile(q) {
				q = findCase(dir, fmt.Sprintf("%s.part%0*d.rar", m[1], width, i))
			}
			if q == "" || !isFile(q) {
				break
			}
			out = append(out, q)
		}
		if len(out) > 0 {
			return out, "rar"
		}
	}
	if m := reOldR.FindStringSubmatch(base); m != nil {
		first := findCase(dir, m[1]+".rar")
		if first != "" && isFile(first) {
			out := []string{first}
			for i := 0; i < 100; i++ {
				q := findCase(dir, fmt.Sprintf("%s.r%02d", m[1], i))
				if q == "" {
					break
				}
				out = append(out, q)
			}
			if len(out) > 1 || strings.EqualFold(filepath.Ext(base), ".rar") {
				if len(out) > 1 {
					return out, "rar"
				}
				return out, ""
			}
		}
	}
	if m := reZipZ.FindStringSubmatch(base); m != nil {
		var out []string
		for i := 1; i < 100; i++ {
			q := findCase(dir, fmt.Sprintf("%s.z%02d", m[1], i))
			if q == "" {
				break
			}
			out = append(out, q)
		}
		last := findCase(dir, m[1]+".zip")
		if len(out) > 0 && last != "" {
			return append(out, last), "zsplit"
		}
	}
	return []string{p}, ""
}

// findCase finds name in dir, ignoring upper/lower case (".R00" next to ".rar").
func findCase(dir, name string) string {
	p := filepath.Join(dir, name)
	if isFile(p) {
		return p
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// FirstPart is the part to open (and the name to show) for any part of a set.
func FirstPart(p string) string {
	parts, _ := Parts(p)
	return parts[0]
}

// SetName is the archive's name without part numbers: "film.part02.rar" → "film.rar".
func SetName(p string) string {
	base := filepath.Base(p)
	if m := reNum.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	if m := rePart.FindStringSubmatch(base); m != nil {
		return m[1] + ".rar"
	}
	if m := reZipZ.FindStringSubmatch(base); m != nil && !strings.EqualFold(m[2], "zip") {
		return m[1] + ".zip"
	}
	if m := reOldR.FindStringSubmatch(base); m != nil && !strings.EqualFold(m[2], "rar") {
		return m[1] + ".rar"
	}
	return base
}

// ---------- writing in parts ----------

// VolWriter writes one file, or parts of at most Split bytes named name.001, name.002, …
// WriteAt can patch bytes already written (the 7z start header).
type VolWriter struct {
	Name  string
	Split int64
	files []*os.File
	cur   int64 // bytes in the current (last) part
	total int64
}

func NewVolWriter(name string, split int64) (*VolWriter, error) {
	v := &VolWriter{Name: name, Split: split}
	if err := v.next(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *VolWriter) partName(i int) string {
	if v.Split <= 0 {
		return v.Name
	}
	return fmt.Sprintf("%s.%03d", v.Name, i+1)
}

func (v *VolWriter) next() error {
	f, err := os.OpenFile(v.partName(len(v.files)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	v.files = append(v.files, f)
	v.cur = 0
	return nil
}

func (v *VolWriter) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if v.Split > 0 && v.cur >= v.Split {
			if err := v.next(); err != nil {
				return n, err
			}
		}
		chunk := p
		if v.Split > 0 && int64(len(chunk)) > v.Split-v.cur {
			chunk = chunk[:v.Split-v.cur]
		}
		w, err := v.files[len(v.files)-1].Write(chunk)
		n += w
		v.cur += int64(w)
		v.total += int64(w)
		p = p[w:]
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (v *VolWriter) WriteAt(p []byte, off int64) (int, error) {
	n := 0
	for len(p) > 0 {
		i, in := 0, off
		if v.Split > 0 {
			i, in = int(off/v.Split), off%v.Split
		}
		if i >= len(v.files) {
			return n, io.ErrShortWrite
		}
		chunk := p
		if v.Split > 0 && int64(len(chunk)) > v.Split-in {
			chunk = chunk[:v.Split-in]
		}
		w, err := v.files[i].WriteAt(chunk, in)
		n += w
		off += int64(w)
		p = p[w:]
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (v *VolWriter) Written() int64 { return v.total }

// Parts lists the files written.
func (v *VolWriter) Parts() []string {
	var out []string
	for i := range v.files {
		out = append(out, v.partName(i))
	}
	return out
}

func (v *VolWriter) Close() error {
	var first error
	for _, f := range v.files {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	// With split on, a set that fits in one part is still "name.001": rename it to plain name.
	if first == nil && v.Split > 0 && len(v.files) == 1 {
		os.Rename(v.partName(0), v.Name)
		v.Split = 0
	}
	return first
}

// Remove deletes what was written (after a failure or cancel).
func (v *VolWriter) Remove() {
	v.Close()
	for i := range v.files {
		os.Remove(v.partName(i))
	}
	os.Remove(v.Name)
}

// ---------- split / join existing files ----------

// SplitFile cuts any file into parts of size bytes: name.001, name.002, …
func SplitFile(ctx context.Context, src string, size int64, progress func(done, total int64)) ([]string, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	v := &VolWriter{Name: src, Split: size}
	if err := v.next(); err != nil {
		return nil, err
	}
	err = copyProgress(ctx, v, f, st.Size(), progress)
	if err != nil {
		v.Remove()
		return nil, err
	}
	parts := v.Parts()
	for _, f := range v.files {
		f.Close()
	}
	return parts, nil
}

// JoinParts joins a split set into one file. ".001" parts are joined end to end; a ".z01 … .zip"
// set is rebuilt into one normal ZIP. Returns the file made.
func JoinParts(ctx context.Context, any string, outDir string, progress func(done, total int64)) (string, error) {
	parts, kind := Parts(any)
	if len(parts) < 2 {
		return "", &UnsupportedError{Detail: "This isn't one part of a split set (.001, .z01 or .part1.rar)."}
	}
	if kind == "rar" {
		return "", &UnsupportedError{Detail: "RAR parts can't be joined into one RAR, but AnySave opens and unpacks them as they are: open the first part."}
	}
	name := SetName(parts[0])
	out := uniquePath(filepath.Join(outDir, name))
	if kind == "zsplit" {
		return out, RebuildZip(ctx, parts, out, progress)
	}
	src, err := openMulti(parts)
	if err != nil {
		return "", err
	}
	defer src.Close()
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	err = copyProgress(ctx, f, src.reader(), src.size, progress)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
		return "", err
	}
	return out, nil
}

func copyProgress(ctx context.Context, w io.Writer, r io.Reader, total int64, progress func(done, total int64)) error {
	buf := make([]byte, 1<<20)
	var done int64
	for {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// uniquePath adds " (2)", " (3)" … before the extension until the name is free.
func uniquePath(p string) string {
	if _, err := os.Lstat(p); err != nil {
		return p
	}
	dir, base := filepath.Dir(p), filepath.Base(p)
	ext := ""
	for _, e := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"} {
		if strings.HasSuffix(strings.ToLower(base), e) {
			ext = base[len(base)-len(e):]
		}
	}
	if ext == "" {
		ext = filepath.Ext(base)
		if len(ext) > 6 || strings.ContainsAny(ext, " ") {
			ext = ""
		}
	}
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		q := filepath.Join(dir, stem+" ("+strconv.Itoa(i)+")"+ext)
		if _, err := os.Lstat(q); err != nil {
			return q
		}
	}
}

// UniquePath is uniquePath for the app.
func UniquePath(p string) string { return uniquePath(p) }

func sortStrings(s []string) []string { sort.Strings(s); return s }
