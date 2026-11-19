package arc

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/flate"
)

// CreateOptions are the choices for a new archive.
type CreateOptions struct {
	Format       string // "zip" or "7z"
	Level        int    // 0 = store (no packing) … 9 = smallest; 5 is normal
	Password     string // AES-256
	EncryptNames bool   // 7z only: the list of names is locked too
	Solid        bool   // 7z only: pack files together (smaller, slower to take one out)
	Split        int64  // bytes per part; 0 = one file
	Comment      string // ZIP only
	KeepJunk     bool   // pack .DS_Store / Thumbs.db too
}

// FileHeader describes one item being added.
type FileHeader struct {
	Name     string // "/"-separated path inside the archive
	Dir      bool
	Size     int64 // -1 when unknown
	Modified time.Time
	Mode     fs.FileMode
}

// Writer adds items to a new archive.
type Writer interface {
	Add(h FileHeader, r io.Reader) error
	Close() error
}

// NewWriter starts a ZIP or 7z archive on v.
func NewWriter(v *VolWriter, o CreateOptions) (Writer, error) {
	switch o.Format {
	case "zip", "":
		return newZipWriter(v, o), nil
	case "7z":
		return newSevenWriter(v, o)
	}
	return nil, &UnsupportedError{Detail: "AnySave makes ZIP and 7z archives. (RAR can't be made: its licence doesn't allow it.)"}
}

// ---------- ZIP ----------

type zipWriter struct {
	bw *bufio.Writer
	w  *zip.Writer
	o  CreateOptions
}

// Already packed tight: storing them is much faster and as small.
var packedExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true,
	".avif": true, ".mp4": true, ".m4v": true, ".mov": true, ".mkv": true, ".webm": true, ".mp3": true, ".m4a": true,
	".aac": true, ".ogg": true, ".opus": true, ".flac": true, ".zip": true, ".7z": true, ".rar": true, ".gz": true,
	".xz": true, ".bz2": true, ".zst": true, ".docx": true, ".xlsx": true, ".pptx": true, ".epub": true, ".apk": true, ".dmg": true}

func newZipWriter(v *VolWriter, o CreateOptions) *zipWriter {
	bw := bufio.NewWriterSize(v, 1<<20)
	w := zip.NewWriter(bw)
	level := o.Level
	if level < 1 || level > 9 {
		level = 6
	}
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, level)
	})
	if o.Comment != "" {
		w.SetComment(trimComment(o.Comment))
	}
	return &zipWriter{bw: bw, w: w, o: o}
}

func trimComment(c string) string {
	if len(c) > 65000 {
		c = c[:65000]
	}
	return c
}

func (z *zipWriter) method(name string) uint16 {
	if z.o.Level == 0 || packedExt[strings.ToLower(path.Ext(name))] {
		return zip.Store
	}
	return zip.Deflate
}

func (z *zipWriter) Add(h FileHeader, r io.Reader) error {
	fh := &zip.FileHeader{Name: h.Name, Modified: h.Modified}
	if h.Modified.IsZero() {
		fh.Modified = time.Now()
	}
	mode := h.Mode.Perm()
	if h.Dir {
		fh.Name = strings.TrimSuffix(h.Name, "/") + "/"
		fh.SetMode(fs.ModeDir | 0o755)
		_, err := z.w.CreateHeader(fh)
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	fh.SetMode(mode)
	fh.Method = z.method(h.Name)
	if z.o.Password == "" {
		w, err := z.w.CreateHeader(fh)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		return err
	}
	return z.addAES(fh, r)
}

// addAES writes a WinZip AES-256 (AE-2) entry: the data is packed, then encrypted in counter mode,
// then signed with HMAC-SHA1. Sizes go in a data descriptor after the data.
func (z *zipWriter) addAES(fh *zip.FileHeader, r io.Reader) error {
	method := fh.Method
	fh.Method = 99
	fh.Flags |= 0x1 | 0x8
	fh.Extra = append(fh.Extra, 0x01, 0x99, 7, 0, 2, 0, 'A', 'E', 3, byte(method), byte(method>>8))
	fh.CRC32 = 0
	w, err := z.w.CreateRaw(fh)
	if err != nil {
		return err
	}
	salt := make([]byte, aesSaltLen(3))
	rand.Read(salt)
	key, macKey, verify := aesKeys(z.o.Password, salt, 3)
	cw := &countW{w: w}
	cw.Write(salt)
	cw.Write(verify)
	block, _ := newAES(key)
	mac := hmac.New(sha1.New, macKey)
	enc := &ctrWriter{w: io.MultiWriter(cw, mac), s: newWzCTR(block)}
	var n int64
	if method == zip.Deflate {
		level := z.o.Level
		if level < 1 || level > 9 {
			level = 6
		}
		fw, _ := flate.NewWriter(enc, level)
		n, err = io.Copy(fw, r)
		if err == nil {
			err = fw.Close()
		}
	} else {
		n, err = io.Copy(enc, r)
	}
	if err != nil {
		return err
	}
	cw.Write(mac.Sum(nil)[:10])
	fh.UncompressedSize64 = uint64(n)
	fh.CompressedSize64 = uint64(cw.n)
	fh.UncompressedSize = uint32(min(fh.UncompressedSize64, 0xffffffff))
	fh.CompressedSize = uint32(min(fh.CompressedSize64, 0xffffffff))
	return nil
}

func (z *zipWriter) Close() error {
	if err := z.w.Close(); err != nil {
		return err
	}
	return z.bw.Flush()
}

type countW struct {
	w io.Writer
	n int64
}

func (c *countW) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

type ctrWriter struct {
	w   io.Writer
	s   *wzCTR
	buf []byte
}

func (c *ctrWriter) Write(p []byte) (int, error) {
	if cap(c.buf) < len(p) {
		c.buf = make([]byte, len(p))
	}
	b := c.buf[:len(p)]
	c.s.xor(b, p)
	return c.w.Write(b)
}

// ---------- making an archive from files on disk ----------

type diskItem struct {
	path string
	name string
	info fs.FileInfo
}

// CreateResult says what was written.
type CreateResult struct {
	Parts []string `json:"parts"`
	Files int      `json:"files"`
	Bytes int64    `json:"bytes"`
}

// collect lists the files and folders to pack, named relative to each item's own folder.
// The archive being written is never packed into itself.
func collect(items []string, under string, out string, keepJunk bool) ([]diskItem, int64, error) {
	var list []diskItem
	var total int64
	taken := map[string]bool{}
	outAbs, _ := filepath.Abs(out)
	unique := func(name string) string {
		n := name
		for i := 2; taken[strings.ToLower(n)]; i++ {
			ext := path.Ext(name)
			n = strings.TrimSuffix(name, ext) + " (" + itoa(i) + ")" + ext
		}
		taken[strings.ToLower(n)] = true
		return n
	}
	for _, it := range items {
		st, err := os.Lstat(it)
		if err != nil {
			return nil, 0, err
		}
		top := unique(path.Join(under, filepath.Base(it)))
		if !st.IsDir() {
			if st.Mode().IsRegular() {
				list = append(list, diskItem{it, top, st})
				total += st.Size()
			}
			continue
		}
		root := it
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: skip it
			}
			if abs, _ := filepath.Abs(p); abs == outAbs || strings.HasPrefix(abs, outAbs+".") && strings.Contains(abs, ".anysave-tmp") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			name := top
			if rel != "." {
				name = path.Join(top, filepath.ToSlash(rel))
			}
			if !keepJunk && IsJunk(path.Base(name)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			switch {
			case d.IsDir():
				list = append(list, diskItem{p, name, info})
			case info.Mode().IsRegular():
				list = append(list, diskItem{p, name, info})
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return list, total, nil
}

// Create packs files and folders into a new ZIP or 7z archive (split into parts if asked).
func Create(ctx context.Context, out string, items []string, o CreateOptions, progress func(done, total int64, name string)) (*CreateResult, error) {
	list, total, err := collect(items, "", out, o.KeepJunk)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, &UnsupportedError{Detail: "There's nothing to pack."}
	}
	v, err := NewVolWriter(out, o.Split)
	if err != nil {
		return nil, err
	}
	res := &CreateResult{}
	fail := func(err error) (*CreateResult, error) {
		v.Remove()
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		return nil, err
	}
	w, err := NewWriter(v, o)
	if err != nil {
		return fail(err)
	}
	var done int64
	for _, it := range list {
		if ctx.Err() != nil {
			return fail(ErrCancelled)
		}
		h := FileHeader{Name: it.name, Dir: it.info.IsDir(), Size: it.info.Size(), Modified: it.info.ModTime(), Mode: it.info.Mode()}
		if h.Dir {
			if err := w.Add(h, nil); err != nil {
				return fail(err)
			}
			continue
		}
		f, err := os.Open(it.path)
		if err != nil {
			return fail(err)
		}
		pr := &progressReader{ctx: ctx, r: f, fn: func(n int64) {
			done += n
			if progress != nil {
				progress(done, total, it.name)
			}
		}}
		err = w.Add(h, pr)
		f.Close()
		if err != nil {
			return fail(err)
		}
		res.Files++
		res.Bytes += h.Size
	}
	if err := w.Close(); err != nil {
		return fail(err)
	}
	if err := v.Close(); err != nil {
		return fail(err)
	}
	res.Parts = v.Parts()
	return res, nil
}

type progressReader struct {
	ctx context.Context
	r   io.Reader
	fn  func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	if p.ctx.Err() != nil {
		return 0, ErrCancelled
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.fn(int64(n))
	}
	return n, err
}

// ---------- converting ----------

// Convert writes everything in a into a new ZIP or 7z archive, without unpacking to disk first.
func Convert(ctx context.Context, a Archive, out string, o CreateOptions, progress func(done, total int64, name string)) (*CreateResult, error) {
	if w := a.Info().Warning; w != "" {
		return nil, &BombError{Detail: w}
	}
	var total int64
	for _, e := range a.Entries() {
		if e.Size > 0 {
			total += e.Size
		}
	}
	v, err := NewVolWriter(out, o.Split)
	if err != nil {
		return nil, err
	}
	w, err := NewWriter(v, o)
	if err != nil {
		v.Remove()
		return nil, err
	}
	res := &CreateResult{}
	var done int64
	err = a.Walk(ctx, func(e *Entry) bool {
		return e.Unsafe == "" && e.Link == "" && !e.Special && (o.KeepJunk || !IsJunk(e.Name))
	},
		func(e *Entry, r io.Reader) error {
			h := FileHeader{Name: e.Name, Dir: e.Dir, Size: e.Size, Modified: e.Modified, Mode: e.Mode}
			if e.Dir {
				return w.Add(h, nil)
			}
			if r == nil {
				return nil
			}
			pr := &progressReader{ctx: ctx, r: r, fn: func(n int64) {
				done += n
				if progress != nil {
					progress(done, total, e.Name)
				}
			}}
			if err := w.Add(h, pr); err != nil {
				return err
			}
			res.Files++
			return nil
		})
	if err == nil {
		err = w.Close()
	}
	if err == nil {
		err = v.Close()
	}
	if err != nil {
		v.Remove()
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		return nil, err
	}
	res.Parts = v.Parts()
	res.Bytes = done
	return res, nil
}

// ---------- changing a ZIP or 7z in place ----------

// EditOptions are the changes to make inside an archive.
type EditOptions struct {
	Delete   []string          // files or folders inside
	Rename   map[string]string // old name → new name (a folder renames everything in it)
	Add      []string          // files or folders on disk
	AddTo    string            // folder inside the archive to add them to
	Comment  *string           // a new comment (ZIP only); nil = keep
	Password string            // to read a locked archive, and to lock what's added
	Level    int
}

// Edit changes a ZIP or 7z archive. It writes a new copy next to it and swaps it in only when
// everything worked, so the original is never left half-changed.
func Edit(ctx context.Context, p string, o EditOptions, progress func(done, total int64, name string)) error {
	a, err := Open(p, Options{Password: o.Password})
	if err != nil {
		return err
	}
	info := a.Info()
	if !info.Editable {
		a.Close()
		if len(info.Volumes) > 1 {
			return &UnsupportedError{Detail: "Split archives can't be changed. Join the parts first."}
		}
		return &UnsupportedError{Detail: "Only ZIP and 7z archives can be changed. Convert it to ZIP or 7z first."}
	}
	if o.Comment != nil && info.Format != "zip" {
		a.Close()
		return &UnsupportedError{Detail: "Only ZIP archives can have a comment."}
	}
	tmp := p + ".anysave-tmp"
	if info.Format == "zip" {
		err = editZip(ctx, a.(*zipArchive), tmp, o, progress)
	} else {
		err = edit7z(ctx, a, tmp, o, progress)
	}
	a.Close()
	if err != nil {
		os.Remove(tmp)
		if ctx.Err() != nil {
			return ErrCancelled
		}
		return err
	}
	return os.Rename(tmp, p)
}

func newNameFn(o EditOptions, existing []*Entry) (func(string) (string, bool), error) {
	del := func(name string) bool {
		for _, d := range o.Delete {
			d = strings.Trim(d, "/")
			if d != "" && (name == d || strings.HasPrefix(name, d+"/")) {
				return true
			}
		}
		return false
	}
	type ren struct{ from, to string }
	var rens []ren
	for from, to := range o.Rename {
		from, to = strings.Trim(from, "/"), strings.Trim(to, "/")
		clean, why := cleanName(to)
		if from == "" || clean == "" || why != "" {
			return nil, &UnsupportedError{Detail: "That name can't be used: " + to}
		}
		rens = append(rens, ren{from, clean})
	}
	sort.Slice(rens, func(i, j int) bool { return len(rens[i].from) > len(rens[j].from) })
	fn := func(name string) (string, bool) {
		if del(name) {
			return "", false
		}
		for _, r := range rens {
			if name == r.from {
				return r.to, true
			}
			if strings.HasPrefix(name, r.from+"/") {
				return r.to + name[len(r.from):], true
			}
		}
		return name, true
	}
	seen := map[string]bool{}
	for _, e := range existing {
		if n, keep := fn(e.Name); keep {
			k := strings.ToLower(strings.TrimSuffix(n, "/"))
			if seen[k] && !e.Dir {
				return nil, &UnsupportedError{Detail: "There's already something called " + n + " in the archive."}
			}
			seen[k] = true
		}
	}
	return fn, nil
}

func editZip(ctx context.Context, a *zipArchive, tmp string, o EditOptions, progress func(done, total int64, name string)) error {
	nameOf, err := newNameFn(o, a.entries)
	if err != nil {
		return err
	}
	if len(o.Add) > 0 && a.info.Encrypted && o.Password == "" {
		return ErrNeedPassword
	}
	if len(o.Add) > 0 && a.info.Encrypted {
		// Check the password before writing anything: it locks the files being added too.
		if err := a.Walk(ctx, func(e *Entry) bool { return e.Encrypted }, func(e *Entry, r io.Reader) error {
			if r != nil {
				var b [1]byte
				if _, err := r.Read(b[:]); err != nil && err != io.EOF {
					return err
				}
			}
			return ErrStop
		}); err != nil {
			return err
		}
	}
	v, err := NewVolWriter(tmp, 0)
	if err != nil {
		return err
	}
	defer v.Close()
	co := CreateOptions{Format: "zip", Level: o.Level, Password: o.Password, Comment: a.info.Comment}
	if co.Level == 0 {
		co.Level = 6
	}
	if o.Comment != nil {
		co.Comment = *o.Comment
	}
	if !a.info.Encrypted {
		co.Password = ""
	}
	zw := newZipWriter(v, co)
	if co.Comment == "" {
		zw.w.SetComment("")
	}
	taken := map[string]bool{}
	for _, e := range a.entries {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		name, keep := nameOf(e.Name)
		if !keep {
			continue
		}
		z := e.ref.(*zent)
		fh := &zip.FileHeader{Name: z.name, Flags: keepDescFlag(z), Method: z.method, CRC32: z.crc,
			CompressedSize64: uint64(z.csize), UncompressedSize64: uint64(z.usize),
			ModifiedTime: z.dosTime, ModifiedDate: z.dosDate, Extra: stripZip64(z.extra), ExternalAttrs: z.mode}
		fh.Flags |= 0x800 // names are kept as UTF-8
		if z.unix {
			fh.CreatorVersion = 3 << 8
		}
		if name != e.Name {
			fh.Name = name
			if e.Dir {
				fh.Name += "/"
			}
			fh.Flags |= 0x800
		}
		taken[strings.ToLower(strings.TrimSuffix(fh.Name, "/"))] = true
		w, err := zw.w.CreateRaw(fh)
		if err != nil {
			return err
		}
		if !e.Dir && z.csize > 0 {
			if _, err := io.Copy(w, io.NewSectionReader(a.src, z.dataOff, z.csize)); err != nil {
				return err
			}
		}
	}
	if err := addFiles(ctx, zw, o, taken, tmp, progress); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return v.Close()
}

func edit7z(ctx context.Context, a Archive, tmp string, o EditOptions, progress func(done, total int64, name string)) error {
	info := a.Info()
	if info.Encrypted && o.Password == "" {
		return ErrNeedPassword
	}
	nameOf, err := newNameFn(o, a.Entries())
	if err != nil {
		return err
	}
	v, err := NewVolWriter(tmp, 0)
	if err != nil {
		return err
	}
	defer v.Close()
	co := CreateOptions{Format: "7z", Level: o.Level, Solid: info.Solid, EncryptNames: info.NamesEncrypted}
	if co.Level == 0 {
		co.Level = 5
	}
	if info.Encrypted {
		co.Password = o.Password
	}
	w, err := newSevenWriter(v, co)
	if err != nil {
		return err
	}
	taken := map[string]bool{}
	var total, done int64
	for _, e := range a.Entries() {
		total += max(e.Size, 0)
	}
	err = a.Walk(ctx, func(e *Entry) bool { _, keep := nameOf(e.Name); return keep && e.Unsafe == "" }, func(e *Entry, r io.Reader) error {
		name, _ := nameOf(e.Name)
		taken[strings.ToLower(name)] = true
		h := FileHeader{Name: name, Dir: e.Dir, Size: e.Size, Modified: e.Modified, Mode: e.Mode}
		if e.Dir || r == nil {
			if e.Dir {
				return w.Add(h, nil)
			}
			return nil
		}
		return w.Add(h, &progressReader{ctx: ctx, r: r, fn: func(n int64) {
			done += n
			if progress != nil {
				progress(done, total, name)
			}
		}})
	})
	if err != nil {
		return err
	}
	if err := addFiles(ctx, w, o, taken, tmp, progress); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return v.Close()
}

func addFiles(ctx context.Context, w Writer, o EditOptions, taken map[string]bool, tmp string, progress func(done, total int64, name string)) error {
	if len(o.Add) == 0 {
		return nil
	}
	under, why := cleanName(o.AddTo)
	if why != "" {
		under = ""
	}
	list, total, err := collect(o.Add, under, tmp, false)
	if err != nil {
		return err
	}
	var done int64
	for _, it := range list {
		name := it.name
		if taken[strings.ToLower(name)] && !it.info.IsDir() {
			name = uniqueInArchive(name, taken)
		}
		if it.info.IsDir() && taken[strings.ToLower(name)] {
			continue
		}
		taken[strings.ToLower(name)] = true
		h := FileHeader{Name: name, Dir: it.info.IsDir(), Size: it.info.Size(), Modified: it.info.ModTime(), Mode: it.info.Mode()}
		if h.Dir {
			if err := w.Add(h, nil); err != nil {
				return err
			}
			continue
		}
		f, err := os.Open(it.path)
		if err != nil {
			return err
		}
		err = w.Add(h, &progressReader{ctx: ctx, r: f, fn: func(n int64) {
			done += n
			if progress != nil {
				progress(done, total, name)
			}
		}})
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func uniqueInArchive(name string, taken map[string]bool) string {
	ext := path.Ext(name)
	for i := 2; ; i++ {
		n := strings.TrimSuffix(name, ext) + " (" + itoa(i) + ")" + ext
		if !taken[strings.ToLower(n)] {
			return n
		}
	}
}

var errNoHash = errors.New("no hash")

var _ hash.Hash = nil
